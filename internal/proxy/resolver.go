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
	"sync"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// resolveLoRAAdapter checks if a model name matches a LoRA adapter and returns
// the parent model name if so. Returns the original name and false if not a LoRA adapter.
func (p *Proxy) resolveLoRAAdapter(ctx context.Context, modelName string) (parentModel string, isLoRA bool) {
	adapterList := &aiv1alpha2.LoRAAdapterList{}
	if err := p.client.List(ctx, adapterList, client.InNamespace(p.namespace)); err != nil {
		return modelName, false
	}

	for _, adapter := range adapterList.Items {
		if adapter.Spec.AdapterName == modelName {
			return adapter.Spec.ModelRef, true
		}
	}

	return modelName, false
}

// resolveServiceLabel resolves a service label to an actual model name.
// Returns the model name if the label was resolved, or the original input if no mapping found.
func (p *Proxy) resolveServiceLabel(ctx context.Context, labelOrModelName string) string {
	// First check cache
	if modelName, ok := p.serviceLabelCache.Load(labelOrModelName); ok {
		return modelName.(string)
	}

	// Refresh cache if stale (>5 seconds old) or first time
	p.refreshServiceLabelCache(ctx)

	// Check cache again after refresh
	if modelName, ok := p.serviceLabelCache.Load(labelOrModelName); ok {
		return modelName.(string)
	}

	// Not a service label, return as-is (it's probably a model name)
	return labelOrModelName
}

// refreshServiceLabelCache updates the service label to model name mapping.
// It scans all Services in the namespace for the AnnotationActiveServiceLabels annotation.
// Detects and warns about conflicts when multiple services claim the same label.
func (p *Proxy) refreshServiceLabelCache(ctx context.Context) {
	p.serviceLabelCacheMu.Lock()
	defer p.serviceLabelCacheMu.Unlock()

	// Skip if recently refreshed
	if time.Since(p.lastCacheRefresh) < serviceLabelCacheTTL {
		return
	}

	// List all Services in the namespace
	var services corev1.ServiceList
	if err := p.client.List(ctx, &services, client.InNamespace(p.namespace)); err != nil {
		slog.Warn("failed to list services for label cache refresh", "error", err)
		return
	}

	// First pass: collect all label claims to detect conflicts
	// If AnnotationActiveServiceLabels is present (even empty), it takes precedence:
	//   - non-empty: use those labels (active group leader)
	//   - empty: service is in a managed group but inactive — no labels
	// Only fall back to AnnotationServiceLabels when active annotation is absent
	// (service is not part of a managed shared-GPU group).
	labelClaims := make(map[string][]string) // label -> []serviceName
	for _, svc := range services.Items {
		labels := ""
		if svc.Annotations != nil {
			if active, ok := svc.Annotations[AnnotationActiveServiceLabels]; ok {
				labels = active // may be empty — means "no active labels"
			} else if static, ok := svc.Annotations[AnnotationServiceLabels]; ok && static != "" {
				labels = static
			}
		}
		if labels == "" {
			continue
		}
		for _, label := range strings.Split(labels, ",") {
			label = strings.TrimSpace(label)
			if label != "" {
				labelClaims[label] = append(labelClaims[label], svc.Name)
			}
		}
	}

	// Clear the cache
	p.serviceLabelCache = sync.Map{}

	// Second pass: build cache and warn on conflicts
	for label, claimants := range labelClaims {
		if len(claimants) > 1 {
			slog.Warn("serviceLabel claimed by multiple services",
				"label", label, "services", claimants, "using", claimants[0])
		}
		// Use first claimant (deterministic based on k8s list order)
		p.serviceLabelCache.Store(label, claimants[0])
		slog.Debug("service label cache updated", "label", label, "model", claimants[0])
	}

	p.lastCacheRefresh = time.Now()
}

// modelAliasCacheTTL is how long the model alias cache is valid before refresh.
const modelAliasCacheTTL = 5 * time.Second

// resolveModelAlias resolves a servedModelName or alias to the K8s Model resource name.
// Returns the K8s name if the alias was resolved, or the original input if no mapping found.
func (p *Proxy) resolveModelAlias(ctx context.Context, nameOrAlias string) string {
	// Check cache first
	if k8sName, ok := p.modelAliasCache.Load(nameOrAlias); ok {
		return k8sName.(string)
	}

	// Refresh cache if stale
	p.refreshModelAliasCache(ctx)

	// Check again after refresh
	if k8sName, ok := p.modelAliasCache.Load(nameOrAlias); ok {
		return k8sName.(string)
	}

	return nameOrAlias
}

// refreshModelAliasCache rebuilds the alias → K8s name mapping from all v1alpha2 Models.
// It maps both spec.litellm.servedModelName and spec.litellm.aliases[] to the resource name.
func (p *Proxy) refreshModelAliasCache(ctx context.Context) {
	p.modelAliasCacheMu.Lock()
	defer p.modelAliasCacheMu.Unlock()

	if time.Since(p.lastAliasRefresh) < modelAliasCacheTTL {
		return
	}

	var models aiv1alpha2.ModelList
	if err := p.client.List(ctx, &models, client.InNamespace(p.namespace)); err != nil {
		slog.Warn("failed to list models for alias cache refresh", "error", err)
		return
	}

	// Collect all alias claims to detect conflicts
	aliasClaims := make(map[string][]string) // alias -> []resourceName

	for _, m := range models.Items {
		resourceName := m.Name
		if m.Spec.LiteLLM == nil {
			continue
		}
		if served := m.Spec.LiteLLM.ServedModelName; served != "" && served != resourceName {
			aliasClaims[served] = append(aliasClaims[served], resourceName)
		}
		for _, alias := range m.Spec.LiteLLM.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" && alias != resourceName {
				aliasClaims[alias] = append(aliasClaims[alias], resourceName)
			}
		}
	}

	// Clear and rebuild cache
	p.modelAliasCache = sync.Map{}

	for alias, claimants := range aliasClaims {
		if len(claimants) > 1 {
			slog.Warn("model alias claimed by multiple models",
				"alias", alias, "models", claimants, "using", claimants[0])
		}
		p.modelAliasCache.Store(alias, claimants[0])
		slog.Debug("model alias cache updated", "alias", alias, "model", claimants[0])
	}

	p.lastAliasRefresh = time.Now()
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
