package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync/atomic"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

	counterAny, _ := p.labelRRCounters.LoadOrStore(label, new(atomic.Uint64))
	counter := counterAny
	idx := counter.Add(1) - 1
	return candidates[idx%uint64(len(candidates))]
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
