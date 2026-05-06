package proxy

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// modelAliasCacheTTL is how long the model alias cache is valid before refresh.
const modelAliasCacheTTL = 5 * time.Second

// ModelResolver handles model name resolution: service labels, aliases,
// LoRA adapters, and label group membership.
type ModelResolver struct {
	client    client.Client
	namespace string

	// Service label resolution
	serviceLabelCache   sync.Map // map[string]string: service label -> model name
	labelGroupCache     sync.Map // map[string][]string: label -> []modelName (all claimants)
	labelGroupModels    sync.Map // map[string][]string: modelName -> []relatedModelNames (reverse index)
	serviceLabelCacheMu sync.Mutex
	lastCacheRefresh    time.Time

	// Model alias resolution
	modelAliasCache   sync.Map // map[string]string: alias -> K8s model name
	modelAliasCacheMu sync.Mutex
	lastAliasRefresh  time.Time
}

// NewModelResolver creates a new ModelResolver with the given K8s client and namespace.
func NewModelResolver(c client.Client, namespace string) *ModelResolver {
	return &ModelResolver{
		client:    c,
		namespace: namespace,
	}
}

// ResolveServiceLabel resolves a service label to an actual model name.
// Returns the model name if the label was resolved, or the original input if no mapping found.
func (r *ModelResolver) ResolveServiceLabel(ctx context.Context, labelOrModelName string) string {
	// First check cache
	if modelName, ok := r.serviceLabelCache.Load(labelOrModelName); ok {
		return modelName.(string)
	}

	// Refresh cache if stale (>5 seconds old) or first time
	r.refreshServiceLabelCache(ctx)

	// Check cache again after refresh
	if modelName, ok := r.serviceLabelCache.Load(labelOrModelName); ok {
		return modelName.(string)
	}

	// Not a service label, return as-is (it's probably a model name)
	return labelOrModelName
}

// refreshServiceLabelCache updates the service label to model name mapping.
// It scans all Services in the namespace for the AnnotationActiveServiceLabels annotation.
// Detects and warns about conflicts when multiple services claim the same label.
func (r *ModelResolver) refreshServiceLabelCache(ctx context.Context) {
	r.serviceLabelCacheMu.Lock()
	defer r.serviceLabelCacheMu.Unlock()

	// Skip if recently refreshed
	if time.Since(r.lastCacheRefresh) < serviceLabelCacheTTL {
		return
	}

	// List all Services in the namespace
	var services corev1.ServiceList
	if err := r.client.List(ctx, &services, client.InNamespace(r.namespace)); err != nil {
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

	// Clear the caches
	r.serviceLabelCache = sync.Map{}
	r.labelGroupCache = sync.Map{}
	r.labelGroupModels = sync.Map{}

	// Second pass: build caches
	// - serviceLabelCache: first claimant per label (backward compat for ResolveServiceLabel)
	// - labelGroupCache: all claimants per label (for multi-model routing)
	// - labelGroupModels: reverse index from model name to all group members
	groupMembers := make(map[string]map[string]bool) // modelName -> set of related models

	for label, claimants := range labelClaims {
		if len(claimants) > 1 {
			slog.Info("serviceLabel shared by multiple models",
				"label", label, "models", claimants)
		}
		// First claimant for backward-compat single-model resolution
		r.serviceLabelCache.Store(label, claimants[0])
		// All claimants for group routing
		r.labelGroupCache.Store(label, claimants)
		slog.Debug("service label cache updated", "label", label, "model", claimants[0])

		// Build reverse index for labels with 2+ claimants
		if len(claimants) >= 2 {
			for _, member := range claimants {
				if groupMembers[member] == nil {
					groupMembers[member] = make(map[string]bool)
				}
				for _, other := range claimants {
					groupMembers[member][other] = true
				}
			}
		}
	}

	// Store reverse index: each model -> all group members (including itself)
	for model, memberSet := range groupMembers {
		members := make([]string, 0, len(memberSet))
		for m := range memberSet {
			members = append(members, m)
		}
		r.labelGroupModels.Store(model, members)
	}

	r.lastCacheRefresh = time.Now()
}

// ResolveModelAlias resolves a servedModelName or alias to the K8s Model resource name.
// Returns the K8s name if the alias was resolved, or the original input if no mapping found.
func (r *ModelResolver) ResolveModelAlias(ctx context.Context, nameOrAlias string) string {
	// Check cache first
	if k8sName, ok := r.modelAliasCache.Load(nameOrAlias); ok {
		return k8sName.(string)
	}

	// Refresh cache if stale
	r.refreshModelAliasCache(ctx)

	// Check again after refresh
	if k8sName, ok := r.modelAliasCache.Load(nameOrAlias); ok {
		return k8sName.(string)
	}

	return nameOrAlias
}

// refreshModelAliasCache rebuilds the alias -> K8s name mapping from all v1alpha2 Models.
// It maps both spec.litellm.servedModelName and spec.litellm.aliases[] to the resource name.
func (r *ModelResolver) refreshModelAliasCache(ctx context.Context) {
	r.modelAliasCacheMu.Lock()
	defer r.modelAliasCacheMu.Unlock()

	if time.Since(r.lastAliasRefresh) < modelAliasCacheTTL {
		return
	}

	var models aiv1alpha2.ModelList
	if err := r.client.List(ctx, &models, client.InNamespace(r.namespace)); err != nil {
		slog.Warn("failed to list models for alias cache refresh", "error", err)
		return
	}

	// Collect all alias claims to detect conflicts
	aliasClaims := make(map[string][]string) // alias -> []resourceName
	addClaim := func(alias, resourceName string) {
		for _, existing := range aliasClaims[alias] {
			if existing == resourceName {
				return
			}
		}
		aliasClaims[alias] = append(aliasClaims[alias], resourceName)
	}

	for _, m := range models.Items {
		resourceName := m.Name
		if m.Spec.LiteLLM == nil {
			continue
		}
		if served := m.Spec.LiteLLM.ServedModelName; served != "" && served != resourceName {
			addClaim(served, resourceName)
		}
		for _, alias := range m.Spec.LiteLLM.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" && alias != resourceName {
				addClaim(alias, resourceName)
			}
		}
	}

	// Clear and rebuild cache
	r.modelAliasCache = sync.Map{}

	for alias, claimants := range aliasClaims {
		if len(claimants) > 1 {
			slog.Warn("model alias claimed by multiple models",
				"alias", alias, "models", claimants, "using", claimants[0])
		}
		r.modelAliasCache.Store(alias, claimants[0])
		slog.Debug("model alias cache updated", "alias", alias, "model", claimants[0])
	}

	r.lastAliasRefresh = time.Now()
}

// ResolveLoRAAdapter checks if a model name matches a LoRA adapter and returns
// the parent model name if so. Returns the original name and false if not a LoRA adapter.
func (r *ModelResolver) ResolveLoRAAdapter(ctx context.Context, modelName string) (parentModel string, isLoRA bool) {
	adapterList := &aiv1alpha2.LoRAAdapterList{}
	if err := r.client.List(ctx, adapterList, client.InNamespace(r.namespace)); err != nil {
		return modelName, false
	}

	for _, adapter := range adapterList.Items {
		if adapter.Spec.AdapterName == modelName {
			return adapter.Spec.ModelRef, true
		}
	}

	return modelName, false
}

// IsModelInLabelGroup checks if a model is part of a label group (shares service labels with other models).
func (r *ModelResolver) IsModelInLabelGroup(modelName string) bool {
	_, ok := r.labelGroupModels.Load(modelName)
	return ok
}

// RangeLabelGroupModels iterates over all label group model entries.
func (r *ModelResolver) RangeLabelGroupModels(f func(modelName string, groupMembers []string) bool) {
	r.labelGroupModels.Range(func(key, value any) bool {
		return f(key.(string), value.([]string))
	})
}
