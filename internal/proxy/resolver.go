package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/internal/routing"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Label-group routing mode constants. See Config.LabelGroupRouting.
const (
	labelGroupRoutingRR                = ""
	labelGroupRoutingRRAlias           = "round-robin"
	labelGroupRoutingLeastLoaded       = "least-loaded"
	labelGroupRoutingPrefixOrRR        = "prefix-or-rr"
	labelGroupRoutingSessionOrRR       = "session-or-rr"
	labelGroupRoutingPrefixSessionOrRR = "prefix-session-or-rr"
)

// isValidLabelGroupRoutingMode reports whether the given value is one of the
// recognized label-group routing modes. Used by Config.Validate.
func isValidLabelGroupRoutingMode(mode string) bool {
	switch mode {
	case labelGroupRoutingRR,
		labelGroupRoutingRRAlias,
		labelGroupRoutingLeastLoaded,
		labelGroupRoutingPrefixOrRR,
		labelGroupRoutingSessionOrRR,
		labelGroupRoutingPrefixSessionOrRR:
		return true
	default:
		return false
	}
}

// canonicalLabelGroupRoutingMode folds "round-robin" to the empty default so
// runtime checks can compare against the empty string.
func canonicalLabelGroupRoutingMode(mode string) string {
	if mode == labelGroupRoutingRRAlias {
		return labelGroupRoutingRR
	}
	return mode
}

// Service label annotation constants for model routing.
// These are aliases for the canonical values in pkg/constants, kept for
// backward compatibility within the proxy package and its tests.
var (
	AnnotationActiveServiceLabels = constants.ServiceAnnotationActiveLabels
	AnnotationServiceLabels       = constants.ServiceAnnotationServiceLabels
)

// getModelDeployment fetches the ModelDeployment resource.
func (p *Proxy) getModelDeployment(ctx context.Context, modelName string) (*aiv1alpha1.ModelDeployment, error) {
	md := &aiv1alpha1.ModelDeployment{}
	err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md)
	return md, err
}

// getModel fetches the v1alpha2 Model resource.
func (p *Proxy) getModel(ctx context.Context, modelName string) (*aiv1alpha2.Model, error) {
	m := &aiv1alpha2.Model{}
	err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m)
	return m, err
}

// pickReadyMember chooses one model from a shared service-label group.
//
// Policy: prefer Ready members and round-robin across them so a two-instance
// fleet (e.g. gemma4-26b-a4b-gptq on 7900xtx + gemma4-26b-a4b-gptq-5930k on
// 5930k claiming `quality-chat`) actually distributes traffic instead of
// pinning to whichever claimant the resolver returned first. If no members
// are Ready, returns the first one in the stable sorted order so the
// existing cold-start path engages on a single deterministic target — picking
// "the alphabetically first member" beats picking "whichever shows up first
// in a random map iteration" because operators can predict which instance
// will warm up.
//
// Single-member groups short-circuit (no counter increment, no Phase lookup)
// so direct model-name requests stay zero-overhead.
//
// `label` keys the per-label round-robin counter (TypedSyncMap.LoadOrStore
// is racy-but-safe: a duplicate counter created by a concurrent first-call
// is discarded; subsequent calls see the stored one).
func (p *Proxy) pickReadyMember(ctx context.Context, label string, members []string) string {
	if len(members) == 1 {
		return members[0]
	}

	// Snapshot Ready status. The Model fetch goes through the controller-
	// runtime cache, which is in-memory and indexed — cheap per call.
	ready := make([]string, 0, len(members))
	for _, name := range members {
		m, err := p.getModel(ctx, name)
		if err == nil && m.Status.Phase == aiv1alpha2.ModelPhaseReady {
			ready = append(ready, name)
		}
	}

	candidates := ready
	if len(candidates) == 0 {
		// No member is Ready. Return the alphabetically first member so the
		// cold-start path in serveOpenAI fires on a deterministic target.
		candidates = members
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	return p.pickRoundRobin(label, candidates)
}

// pickRoundRobin chooses from an already-filtered candidate set. Keeping this
// separate lets least-loaded routing retain fair tie-breaking without
// repeating Model readiness lookups or accidentally widening the candidate
// set back to every Ready member.
func (p *Proxy) pickRoundRobin(label string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	counterAny, _ := p.labelRRCounters.LoadOrStore(label, new(atomic.Uint64))
	idx := counterAny.Add(1) - 1
	return candidates[idx%uint64(len(candidates))]
}

// pickLeastLoaded chooses the Ready model with the fewest in-flight proxy
// connections. Ties round-robin so an idle fleet still spreads burst arrivals
// instead of pinning to the first member in stable resolver order.
func (p *Proxy) pickLeastLoaded(label string, candidates []string) string {
	if len(candidates) <= 1 {
		return p.pickRoundRobin(label, candidates)
	}

	minConnections := p.GetActiveConnections(candidates[0])
	leastLoaded := []string{candidates[0]}
	for _, name := range candidates[1:] {
		connections := p.GetActiveConnections(name)
		switch {
		case connections < minConnections:
			minConnections = connections
			leastLoaded = []string{name}
		case connections == minConnections:
			leastLoaded = append(leastLoaded, name)
		}
	}

	return p.pickRoundRobin(label, leastLoaded)
}

// pickReadyMemberRouted is the production entry point for label-group
// resolution. It honors the configured FLEXINFER_PROXY_LABEL_GROUP_ROUTING
// mode: when empty / "round-robin", delegates to pickReadyMember for exact
// legacy behavior. "least-loaded" chooses the Ready member with the fewest
// in-flight connections. Other modes extract a routing key (prefix and/or
// session) and consistent-hash across the Ready candidates so multi-turn
// traffic pins to the same replica, raising the chance of an APC hit on
// turn-2+ (see F4-proxy-prefix-pinning in `.loom/brainstorm-f4-long-context-
// agent-2026-05-25.md`).
//
// Every call emits exactly one
// flexinfer_proxy_label_group_route_decisions_total{label,strategy,outcome}
// increment plus one target-hit increment. `strategy` is the configured mode;
// `outcome` is one of:
//   - default_rr: mode was empty / round-robin
//   - least_loaded: lowest active-connection count won
//   - fallback_single: only one (Ready) candidate
//   - fallback_no_ready: zero Ready candidates; deterministic cold-start path
//   - fallback_no_key: mode was non-default but no key was extractable
//   - hashed_prefix | hashed_session: consistent-hash pin took effect
//
// Fallback paths preserve existing semantics by delegating to
// pickReadyMember; the legacy RR counter still advances so consecutive
// no-key requests distribute across replicas.
func (p *Proxy) pickReadyMemberRouted(
	ctx context.Context,
	label string,
	members []string,
	req *http.Request,
	bodyBytes []byte,
) string {
	strategy := p.labelGroupRouting
	if strategy == labelGroupRoutingRR {
		return observeLabelGroupPick(label, "round-robin", "default_rr", p.pickReadyMember(ctx, label, members))
	}

	if len(members) == 1 {
		return observeLabelGroupPick(label, strategy, "fallback_single", members[0])
	}

	ready := make([]string, 0, len(members))
	for _, name := range members {
		m, err := p.getModel(ctx, name)
		if err == nil && m.Status.Phase == aiv1alpha2.ModelPhaseReady {
			ready = append(ready, name)
		}
	}

	if len(ready) == 0 {
		return observeLabelGroupPick(label, strategy, "fallback_no_ready", p.pickReadyMember(ctx, label, members))
	}
	if len(ready) == 1 {
		return observeLabelGroupPick(label, strategy, "fallback_single", ready[0])
	}

	if strategy == labelGroupRoutingLeastLoaded {
		return observeLabelGroupPick(label, strategy, "least_loaded", p.pickLeastLoaded(label, ready))
	}

	key, outcome := extractRoutingKey(strategy, req, bodyBytes)
	if key == "" {
		return observeLabelGroupPick(label, strategy, "fallback_no_key", p.pickReadyMember(ctx, label, members))
	}

	return observeLabelGroupPick(label, strategy, outcome, consistentHashPick(key, ready))
}

func observeLabelGroupPick(label, strategy, outcome, model string) string {
	labelGroupRouteDecisionsTotal.WithLabelValues(label, strategy, outcome).Inc()
	labelGroupRouteTargetHitsTotal.WithLabelValues(label, strategy, model).Inc()
	return model
}

// extractRoutingKey returns the routing key and outcome label appropriate for
// the configured mode. Empty key means no extractable key — caller should
// fall back to round-robin.
func extractRoutingKey(strategy string, req *http.Request, bodyBytes []byte) (string, string) {
	switch strategy {
	case labelGroupRoutingPrefixOrRR:
		if k, _ := routing.ExtractPrefixKey(req, bodyBytes); k != "" {
			return k, "hashed_prefix"
		}
	case labelGroupRoutingSessionOrRR:
		if k, _ := routing.ExtractSessionKey(req, bodyBytes); k != "" {
			return k, "hashed_session"
		}
	case labelGroupRoutingPrefixSessionOrRR:
		if k, _ := routing.ExtractPrefixKey(req, bodyBytes); k != "" {
			return k, "hashed_prefix"
		}
		if k, _ := routing.ExtractSessionKey(req, bodyBytes); k != "" {
			return k, "hashed_session"
		}
	}
	return "", ""
}

// consistentHashPick deterministically picks one candidate from members
// based on key. SHA-256(key) folded to uint64, mod len(sortedMembers).
//
// This is NOT a true consistent-hash ring: removing one member from a 2-set
// reroutes every key. For label-group routing that's acceptable — groups
// have 2-3 members and membership changes are rare (Model CR add/remove).
// The benefit we need is "same key → same target across calls", which
// simple SHA-mod gives us with one allocation per call.
func consistentHashPick(key string, members []string) string {
	if len(members) == 0 {
		return ""
	}
	if len(members) == 1 {
		return members[0]
	}
	sorted := make([]string, len(members))
	copy(sorted, members)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(key))
	h := binary.BigEndian.Uint64(sum[:8])
	return sorted[h%uint64(len(sorted))]
}

// extractModelNameAndBody extracts the model name from a request and returns the body bytes.
// The body is restored to the request for downstream handlers.
// For multipart/form-data requests (e.g., /v1/images/edits), body bytes are returned as nil
// to signal that downstream JSON rewriting must be skipped.
func (p *Proxy) extractModelNameAndBody(r *http.Request) (string, []byte) {
	var bodyBytes []byte
	ct := r.Header.Get("Content-Type")

	// Check X-Model-ID header first
	modelName := r.Header.Get("X-Model-ID")
	if modelName != "" {
		// Still need to read body for validation (JSON only)
		if r.Method == http.MethodPost && strings.Contains(ct, "application/json") {
			if b, err := io.ReadAll(r.Body); err == nil {
				bodyBytes = b
			} else {
				slog.Debug("failed to read request body for model extraction (X-Model-ID)", "error", err)
				bodyBytes = nil
			}
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
		}
		return modelName, bodyBytes
	}

	// Fallback: Use path prefix /model/<name>/...
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(pathParts) > 1 && pathParts[0] == "model" {
		modelName = pathParts[1]
		// Strip the /model/<name> prefix for upstream
		r.URL.Path = "/" + strings.Join(pathParts[2:], "/")
		// Still need to read body for validation (JSON only)
		if r.Method == http.MethodPost && strings.Contains(ct, "application/json") {
			if b, err := io.ReadAll(r.Body); err == nil {
				bodyBytes = b
			} else {
				slog.Debug("failed to read request body for model extraction (path)", "error", err)
				bodyBytes = nil
			}
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
		}
		return modelName, bodyBytes
	}

	// Fallback: Check JSON Body (OpenAI Standard)
	if r.Method == http.MethodPost && strings.Contains(ct, "application/json") {
		if b, err := io.ReadAll(r.Body); err == nil {
			bodyBytes = b
		} else {
			slog.Debug("failed to read request body for model extraction (json)", "error", err)
			bodyBytes = nil
		}
		// Restore body immediately so the proxy can upstream it
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		r.ContentLength = int64(len(bodyBytes)) // Update ContentLength for downstream handlers

		// Parse partial JSON to find "model" field
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(bodyBytes, &payload); err == nil && payload.Model != "" {
			return payload.Model, bodyBytes
		}
	}

	// Fallback: Check multipart/form-data body (OpenAI /v1/images/edits)
	if r.Method == http.MethodPost && strings.Contains(ct, "multipart/form-data") {
		modelName, _ = extractModelFromMultipart(r)
		return modelName, nil // nil signals non-JSON body; skip model rewriting downstream
	}

	return "", bodyBytes
}

// extractModelFromMultipart extracts the "model" form field from a multipart/form-data request.
// The request body is buffered and restored so downstream handlers can read the full payload.
func extractModelFromMultipart(r *http.Request) (string, error) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Debug("failed to read multipart body", "error", err)
		return "", err
	}
	// Restore body for downstream (reverse proxy must forward the original payload)
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	r.ContentLength = int64(len(bodyBytes))

	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return "", err
	}
	boundary, ok := params["boundary"]
	if !ok {
		return "", nil
	}

	mr := multipart.NewReader(bytes.NewReader(bodyBytes), boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		if part.FormName() == "model" {
			val, _ := io.ReadAll(part)
			return strings.TrimSpace(string(val)), nil
		}
	}
	return "", nil
}

// extractModelName extracts the model name from a request (for backward compatibility).
func (p *Proxy) extractModelName(r *http.Request) string {
	modelName, _ := p.extractModelNameAndBody(r)
	return modelName
}

// extractModelFromSource extracts the model identifier from a v1alpha2 Source string.
func extractModelFromSource(source string) string {
	switch {
	case strings.HasPrefix(source, "HF://"):
		return strings.TrimPrefix(source, "HF://")
	case strings.HasPrefix(source, "ollama://"):
		return strings.TrimPrefix(source, "ollama://")
	case strings.HasPrefix(source, "file://"):
		return strings.TrimPrefix(source, "file://")
	case strings.HasPrefix(source, "pvc://"):
		rest := strings.TrimPrefix(source, "pvc://")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) == 2 {
			return "/" + parts[1]
		}
		return ""
	default:
		return source
	}
}
