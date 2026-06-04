package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/internal/routing"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Routing annotation for models
const (
	AnnotationRouting = "flexinfer.ai/routing"
)

// watchEndpoints periodically updates the router with current pod endpoints for each model.
// This enables direct pod routing for session affinity and prefix-based routing.
func (p *Proxy) watchEndpoints(ctx context.Context) {
	ticker := time.NewTicker(endpointWatchInterval)
	defer ticker.Stop()

	slog.Info("starting endpoint watcher for routing")

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.refreshEndpoints(ctx)
		}
	}
}

// refreshEndpoints updates the router with current endpoints for models that have routing enabled.
// Only models with the flexinfer.ai/routing annotation will get direct pod routing;
// others will use Kubernetes Service DNS for load balancing.
func (p *Proxy) refreshEndpoints(ctx context.Context) {
	start := time.Now()
	defer func() {
		endpointRefreshDuration.Observe(time.Since(start).Seconds())
	}()

	// List all Services in namespace (each model has a service)
	var services corev1.ServiceList
	if err := p.client.List(ctx, &services, client.InNamespace(p.namespace)); err != nil {
		slog.Warn("failed to list services for endpoint refresh", "error", err)
		return
	}

	for _, svc := range services.Items {
		// Only process services that look like model services
		if svc.Spec.Selector == nil {
			continue
		}
		modelName, hasModelLabel := svc.Spec.Selector["flexinfer.ai/model"]
		if !hasModelLabel {
			continue
		}

		// Pre-initialize all per-model metrics so Grafana shows 0 instead of "No data".
		InitModelMetrics(modelName)

		// List endpoints for this service to track endpoint count for all models.
		var endpoints corev1.Endpoints
		if err := p.client.Get(ctx, client.ObjectKey{Name: svc.Name, Namespace: p.namespace}, &endpoints); err != nil {
			continue
		}

		// Count ready addresses for the endpoint_count gauge (all models).
		var readyCount int
		for _, subset := range endpoints.Subsets {
			readyCount += len(subset.Addresses)
		}
		endpointCount.WithLabelValues(modelName).Set(float64(readyCount))

		// Check if this model has routing annotation or is in a label group
		// Models with explicit routing strategy or shared service labels get direct pod routing
		hasRoutingAnnotation := p.modelHasRoutingAnnotation(ctx, modelName)
		isInLabelGroup := p.isModelInLabelGroup(modelName)
		if !hasRoutingAnnotation && !isInLabelGroup {
			// Remove from router if previously added, so it falls back to Service DNS
			p.router.RemoveModel(modelName)
			// Clear endpoint routing cache for this model
			p.endpointCache.Delete(modelName)
			continue
		}

		// Collect ready pod addresses, skipping pods on terminating nodes.
		var podAddresses []string
		for _, subset := range endpoints.Subsets {
			port := defaultBackendPort
			for _, pp := range subset.Ports {
				port = pp.Port
				break
			}
			for _, addr := range subset.Addresses {
				// Skip pods on nodes marked for spot termination
				if addr.NodeName != nil && p.activator.IsNodeTerminating(ctx, *addr.NodeName) {
					slog.Debug("skipping endpoint on terminating node", "model", modelName, "node", *addr.NodeName)
					continue
				}
				podAddresses = append(podAddresses, fmt.Sprintf("%s:%d", addr.IP, port))
			}
		}

		// Track endpoint changes for routing-enabled models
		p.trackEndpointChanges(modelName, podAddresses)

		// Update router if we have endpoints
		if len(podAddresses) > 0 {
			p.router.UpdateEndpoints(modelName, podAddresses)
			slog.Debug("updated routing endpoints", "model", modelName, "endpoints", len(podAddresses))
		}
	}

	// Aggregation pass: for models in label groups, combine endpoints from all group members.
	// This overwrites each model's router ring with the union of all group members' endpoints,
	// enabling cross-node load balancing for models sharing service labels.
	p.resolver.RangeLabelGroupModels(func(modelName string, groupMembers []string) bool {
		seen := make(map[string]bool)
		var aggregated []string
		for _, member := range groupMembers {
			if cached, ok := p.endpointCache.Load(member); ok {
				for _, ep := range cached {
					if !seen[ep] {
						seen[ep] = true
						aggregated = append(aggregated, ep)
					}
				}
			}
		}

		if len(aggregated) > 0 {
			p.router.UpdateEndpoints(modelName, aggregated)
			slog.Debug("updated label group routing endpoints",
				"model", modelName, "group_members", groupMembers, "endpoints", len(aggregated))
		}
		return true
	})
}

// trackEndpointChanges compares current endpoints with cached ones and updates metrics.
func (p *Proxy) trackEndpointChanges(modelName string, newEndpoints []string) {
	// Update endpoint count gauge
	endpointCount.WithLabelValues(modelName).Set(float64(len(newEndpoints)))

	// Get previous endpoints from cache
	var oldEndpoints []string
	if cached, ok := p.endpointCache.Load(modelName); ok {
		oldEndpoints = cached
	}

	// Create sets for comparison
	oldSet := make(map[string]bool, len(oldEndpoints))
	for _, ep := range oldEndpoints {
		oldSet[ep] = true
	}
	newSet := make(map[string]bool, len(newEndpoints))
	for _, ep := range newEndpoints {
		newSet[ep] = true
	}

	// Count additions
	for ep := range newSet {
		if !oldSet[ep] {
			endpointChangesTotal.WithLabelValues(modelName, "added").Inc()
		}
	}

	// Count removals
	for ep := range oldSet {
		if !newSet[ep] {
			endpointChangesTotal.WithLabelValues(modelName, "removed").Inc()
		}
	}

	// Store updated cache
	p.endpointCache.Store(modelName, newEndpoints)
}

// isModelInLabelGroup checks if a model is part of a label group (shares service labels with other models).
func (p *Proxy) isModelInLabelGroup(modelName string) bool {
	return p.resolver.IsModelInLabelGroup(modelName)
}

// modelHasRoutingAnnotation checks if a model has the flexinfer.ai/routing annotation set.
func (p *Proxy) modelHasRoutingAnnotation(ctx context.Context, modelName string) bool {
	// Check v1alpha2 Model first
	m := &aiv1alpha2.Model{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err == nil {
		if m.Annotations != nil {
			if _, ok := m.Annotations[AnnotationRouting]; ok {
				return true
			}
		}
		return false
	}

	// Fallback: check v1alpha1 ModelDeployment (deprecated)
	md := &aiv1alpha1.ModelDeployment{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md); err == nil {
		if md.Annotations != nil {
			if _, ok := md.Annotations[AnnotationRouting]; ok {
				return true
			}
		}
	}

	return false
}

// getRoutingStrategy returns the routing strategy for a model from its annotation.
func (p *Proxy) getRoutingStrategy(ctx context.Context, modelName string) routing.Strategy {
	if !p.routingEnabled {
		return routing.StrategyDefault
	}

	// Check v1alpha2 Model first
	m := &aiv1alpha2.Model{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err == nil {
		if m.Annotations != nil {
			if strategy, ok := m.Annotations[AnnotationRouting]; ok {
				return routing.Strategy(strategy)
			}
		}
		return routing.StrategyDefault
	}

	// Fallback: check v1alpha1 ModelDeployment (deprecated)
	md := &aiv1alpha1.ModelDeployment{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md); err == nil {
		if md.Annotations != nil {
			if strategy, ok := md.Annotations[AnnotationRouting]; ok {
				return routing.Strategy(strategy)
			}
		}
		return routing.StrategyDefault
	}

	return routing.StrategyDefault
}

// Note on label-group default routing:
//
// Previously this function auto-defaulted to `StrategyLeastLoaded` whenever a
// model was in a label group (shared service-labels with another model). Paired
// with `refreshEndpoints`' label-group aggregation pass, that wrote the UNION
// of all group members' pod endpoints into each member's router ring. So a
// request resolved to `gemma4-26b-a4b-gptq-5930k` (body rewritten to that
// served-model-name) could be forwarded to a pod from a different group member
// (`gemma4-26b-a4b-gptq` on the 7900xtx), which then 404'd because vLLM's
// `--served-model-name` didn't match the body's `model` field.
//
// Under shared service-labels with the round-robin picker (proxy.go:417,
// resolver.go:pickReadyMember), cross-model selection happens BEFORE
// `serveProxy`. The router/aggregation pair would re-pick across the same
// group, racing the picker. Removing the auto-default keeps the router branch
// dormant unless an operator explicitly opts in with `flexinfer.ai/routing`.
// Aggregation in `refreshEndpoints` is preserved for that explicit case
// (see `TestRefreshEndpoints_LabelGroupAggregation`).

// serveProxy forwards the request to the appropriate backend.
// If the model name resolves to a LoRA adapter, the request is routed to the
// parent model's endpoint while preserving the adapter name in the request body
// so the backend (vLLM) routes to the correct LoRA weights.
func (p *Proxy) serveProxy(w http.ResponseWriter, r *http.Request, modelName string) {
	ctx := r.Context()

	// Check if the model name is a LoRA adapter; if so, route to parent model
	// but keep the adapter name in the request body for the backend.
	resolvedModel := modelName
	isLoRA := false
	if parentModel, ok := p.resolver.ResolveLoRAAdapter(ctx, modelName); ok {
		resolvedModel = parentModel
		isLoRA = true
	}

	// Get the backend port for this model (defaults to 8000 if not found)
	port := p.getBackendPort(ctx, resolvedModel)

	// Get the actual backend model name (e.g., HuggingFace model ID)
	// For LoRA adapters, skip model rewriting — the adapter name must pass through.
	var backendModelName string
	if !isLoRA {
		backendModelName = p.getBackendModelName(ctx, resolvedModel)
	}

	// Read body for routing decision and model rewriting
	var bodyBytes []byte
	if r.Body != nil && r.Body != http.NoBody &&
		(r.Method == http.MethodPost || r.Method == http.MethodPut) {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		if cerr := r.Body.Close(); cerr != nil {
			slog.Debug("failed to close request body after read", "error", cerr)
		}
		if err != nil {
			slog.Debug("failed to read request body for routing decision", "error", err)
			bodyBytes = nil
		}
	}

	// routeBody is the original payload used for strategy routing decisions
	// (must be captured before model-name rewriting below).
	routeBody := bodyBytes
	forwardBody := bodyBytes

	// Rewrite model name in request body if needed (JSON only — skip for multipart/form-data)
	if backendModelName != "" && len(forwardBody) > 0 &&
		strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		forwardBody = p.rewriteModelInBody(forwardBody, backendModelName)
	}

	// Clamp max_tokens against the target model's context window so a client
	// that sets max_tokens == max_model_len (common default in OpenAI-compatible
	// UIs and LiteLLM) does not end up with a 0-token prompt budget and a
	// guaranteed 400 from vLLM. No-op when the model has no declared context
	// window or when max_tokens already fits.
	if len(forwardBody) > 0 &&
		strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var originalMaxTokens, clampedMaxTokens int
		forwardBody, originalMaxTokens, clampedMaxTokens = p.maybeClampMaxTokensForResponse(ctx, resolvedModel, forwardBody)
		if originalMaxTokens >= 0 && clampedMaxTokens >= 0 {
			w.Header().Set("X-FlexInfer-MaxTokens-Clamped", fmt.Sprintf("%d->%d", originalMaxTokens, clampedMaxTokens))
		}
	}

	// Forward to the upstream with self-healing retry on dial-class failures
	// (stale direct-load target / rolling backend pod). See routing_retry.go.
	p.forwardWithRetry(w, r, modelName, resolvedModel, port, routeBody, forwardBody)
}

// getBackendModelName returns the actual model identifier used by the backend.
// Prefers servedModelName from config (used by vLLM --served-model-name), then
// falls back to the HF source model ID (e.g., "Qwen/Qwen2.5-7B-Instruct").
func (p *Proxy) getBackendModelName(ctx context.Context, modelName string) string {
	// Check v1alpha2 Model first
	m := &aiv1alpha2.Model{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err == nil {
		// Use servedModelName if set (vLLM --served-model-name, llama.cpp alias, etc.)
		if cfg := m.Spec.GetConfigMap(); cfg != nil {
			if v, ok := cfg["servedModelName"]; ok {
				if s, ok := v.(string); ok && s != "" {
					return s
				}
			}
		}
		return extractModelFromSource(m.Spec.Source)
	} else if !errors.IsNotFound(err) {
		return ""
	}

	// Fallback: v1alpha1 ModelDeployment (deprecated)
	md := &aiv1alpha1.ModelDeployment{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md); err == nil {
		return md.Spec.Model
	}

	return ""
}

// getBackendPort returns the port for a model's backend service.
// The reconciled Service is the source of truth when present; backend defaults
// are only a fallback for old objects or tests without a Service.
func (p *Proxy) getBackendPort(ctx context.Context, modelName string) int32 {
	if port, ok := p.getServicePort(ctx, modelName); ok {
		return port
	}

	// Check v1alpha2 Model first
	m := &aiv1alpha2.Model{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err == nil {
		if b, ok := backend.Get(m.Spec.Backend); ok {
			return b.Port()
		}
		return defaultBackendPort
	} else if !errors.IsNotFound(err) {
		return defaultBackendPort
	}

	// Fallback: v1alpha1 ModelDeployment (deprecated)
	md := &aiv1alpha1.ModelDeployment{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md); err == nil {
		if b, ok := backend.Get(md.Spec.Backend); ok {
			return b.Port()
		}
		return defaultBackendPort
	}
	return defaultBackendPort
}

func (p *Proxy) getServicePort(ctx context.Context, modelName string) (int32, bool) {
	svc := &corev1.Service{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, svc); err != nil {
		if !errors.IsNotFound(err) {
			slog.Debug("failed to get model service for backend port", "model", modelName, "error", err)
		}
		if cached, ok := p.lastKnownServicePorts.Load(modelName); ok {
			return cached, true
		}
		return 0, false
	}

	var picked int32
	for _, port := range svc.Spec.Ports {
		if port.Name == "http" && port.Port > 0 {
			picked = port.Port
			break
		}
	}
	if picked == 0 {
		for _, port := range svc.Spec.Ports {
			if port.Port > 0 {
				picked = port.Port
				break
			}
		}
	}
	if picked == 0 {
		return 0, false
	}
	p.lastKnownServicePorts.Store(modelName, picked)
	return picked, true
}

// rewriteModelInBody replaces the "model" field in a JSON request body with the backend model name.
// This allows clients to use FlexInfer model names/aliases while backends receive their native model IDs.
//
// Uses surgical byte-level replacement when possible to avoid full JSON parse/remarshal overhead.
// Falls back to full parse when the fast path cannot locate the field.
func (p *Proxy) rewriteModelInBody(body []byte, backendModelName string) []byte {
	// Fast path: locate "model" key and splice the value
	if result := spliceModelField(body, backendModelName); result != nil {
		return result
	}

	// Slow path: full JSON parse
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return body
	}
	if _, ok := data["model"]; !ok {
		return body
	}
	data["model"] = backendModelName
	modified, err := json.Marshal(data)
	if err != nil {
		return body
	}
	return modified
}

// spliceModelField performs surgical byte-level replacement of the "model" JSON value.
// Returns nil if the fast path is not applicable (no "model" key, nested object, etc.).
func spliceModelField(body []byte, replacement string) []byte {
	// Find "model" key — must be a top-level key (before any nested object).
	needle := []byte(`"model"`)
	idx := bytes.Index(body, needle)
	if idx < 0 {
		return nil
	}

	// Walk past the key and the colon
	pos := idx + len(needle)
	for pos < len(body) && (body[pos] == ' ' || body[pos] == '\t' || body[pos] == '\n' || body[pos] == '\r') {
		pos++
	}
	if pos >= len(body) || body[pos] != ':' {
		return nil
	}
	pos++ // skip ':'
	for pos < len(body) && (body[pos] == ' ' || body[pos] == '\t' || body[pos] == '\n' || body[pos] == '\r') {
		pos++
	}
	if pos >= len(body) || body[pos] != '"' {
		return nil // value isn't a string — bail to full parse
	}

	// Find the end of the quoted value, handling escaped quotes
	valStart := pos
	pos++ // skip opening '"'
	for pos < len(body) {
		if body[pos] == '\\' {
			pos += 2 // skip escaped char
			continue
		}
		if body[pos] == '"' {
			break
		}
		pos++
	}
	if pos >= len(body) {
		return nil // unterminated string
	}
	valEnd := pos + 1 // include closing '"'

	// Build replacement value (JSON-escaped string)
	newVal := []byte(strconv.Quote(replacement))

	// Splice: body[:valStart] + newVal + body[valEnd:]
	result := make([]byte, 0, len(body)-valEnd+valStart+len(newVal))
	result = append(result, body[:valStart]...)
	result = append(result, newVal...)
	result = append(result, body[valEnd:]...)
	return result
}

// loadOrCreateProxy returns a cached httputil.ReverseProxy for the target URL,
// creating a new one if the entry is missing or has expired past proxyTTL.
// Returns false if the URL cannot be parsed.
func (p *Proxy) loadOrCreateProxy(targetURL string) (*httputil.ReverseProxy, bool) {
	if entry, ok := p.proxyMap.Load(targetURL); ok {
		if time.Since(entry.created) < proxyTTL {
			return entry.proxy, true
		}
		// Entry expired — delete and recreate below.
		p.proxyMap.Delete(targetURL)
	}

	u, err := url.Parse(targetURL)
	if err != nil {
		slog.Error("invalid proxy target URL", "targetURL", targetURL, "error", err)
		return nil, false
	}
	rp := httputil.NewSingleHostReverseProxy(u)
	// Wrap the default Director to propagate W3C trace context into the
	// upstream request so the backend can continue the distributed trace
	// started at the proxy edge (see handleRequest's Extract+Start). The
	// inbound request's context — carried into the cloned outbound request —
	// holds the active span. Injection is a no-op when tracing is disabled,
	// because the global propagator is the default no-op until InitTracing
	// installs the W3C propagator under FLEXINFER_OTEL_ENABLED=true.
	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		origDirector(req)
		otel.GetTextMapPropagator().Inject(req.Context(), propagation.HeaderCarrier(req.Header))
	}
	rp.ModifyResponse = p.logUpstreamUsage
	// On a transport (dial) error — which httputil.ReverseProxy reports only
	// before any response byte is written — record it on the per-request
	// forwardResult so forwardWithRetry can self-heal and retry. When no retry
	// context is present (direct callers / tests), preserve the default 502.
	rp.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		if fr := forwardResultFromContext(req.Context()); fr != nil {
			fr.err = err
			return
		}
		slog.Debug("upstream transport error (no retry context)", "target", targetURL, "error", err)
		validation.WriteError(rw, http.StatusBadGateway, "upstream unavailable", "upstream_error", "bad_gateway")
	}
	p.proxyMap.Store(targetURL, proxyEntry{proxy: rp, created: time.Now()})
	return rp, true
}

// updateLastAccess updates the last-access timestamp for a model.
func (p *Proxy) updateLastAccess(ctx context.Context, modelName string) {
	// Optimization: Don't update on every request to avoid API spam.
	// Only update if current LastAccessTime is old (> 1 minute ago).

	// Try v1alpha2 Model first
	var modelErr error
	for i := 0; i < 3; i++ {
		m := &aiv1alpha2.Model{}
		if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err != nil {
			modelErr = err
			break
		}
		if m.Status.LastActiveTime != nil && time.Since(m.Status.LastActiveTime.Time) < lastAccessThrottleInterval {
			return
		}

		now := metav1.Now()
		m.Status.LastActiveTime = &now
		if err := p.client.Status().Update(ctx, m); err != nil {
			if errors.IsConflict(err) {
				continue
			}
			slog.Debug("failed to update LastActiveTime", "model", modelName, "error", err)
		}
		return
	}
	if modelErr == nil {
		slog.Debug("failed to update LastActiveTime after retries", "model", modelName)
		return
	}
	if !errors.IsNotFound(modelErr) {
		slog.Warn("error fetching model for stats update", "model", modelName, "error", modelErr)
		return
	}

	// Fallback: v1alpha1 ModelDeployment (deprecated)
	md := &aiv1alpha1.ModelDeployment{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md); err != nil {
		slog.Warn("error fetching modeldeployment for stats update", "model", modelName, "error", err)
		return
	}

	if md.Status.LastAccessTime != nil && time.Since(md.Status.LastAccessTime.Time) < lastAccessThrottleInterval {
		return
	}

	now := metav1.Now()
	md.Status.LastAccessTime = &now
	if err := p.client.Status().Update(ctx, md); err != nil {
		slog.Debug("failed to update LastAccessTime", "model", modelName, "error", err)
	}
}
