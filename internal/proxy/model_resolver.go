package proxy

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// modelAliasCacheTTL is how long the model alias cache is valid before refresh.
const modelAliasCacheTTL = 5 * time.Second

// labelSnapshot is one complete service-label cache. A refresh builds a new
// one and publishes it with a single atomic store; readers never see a cache
// that is being cleared or refilled. Its maps are never written after
// publication.
type labelSnapshot struct {
	first       map[string]string   // service label -> first claimant (sorted)
	groups      map[string][]string // service label -> all claimants (sorted)
	groupModels map[string][]string // model -> members of every shared label it is in
}

// ModelResolver handles model name resolution: service labels, aliases,
// LoRA adapters, and label group membership.
type ModelResolver struct {
	client    client.Client
	namespace string

	// Service label resolution. The mutex serializes refreshes (and guards
	// lastCacheRefresh and loggedSharedLabels); readers only load labels.
	labels              atomic.Pointer[labelSnapshot]
	serviceLabelCacheMu sync.Mutex
	lastCacheRefresh    time.Time
	// loggedSharedLabels remembers the last-logged claimant set per shared label
	// so steady-state refreshes (every serviceLabelCacheTTL) stay silent.
	// We only emit when membership transitions: first observation, member added
	// or removed, or label drops below 2 claimants.
	loggedSharedLabels map[string]string

	// Model alias resolution: alias -> K8s model name, published like labels.
	aliases           atomic.Pointer[map[string]string]
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
//
// Single-model semantics only: when multiple models claim the same label
// (shared service-labels for horizontal capacity, e.g. a two-instance fleet),
// this returns only the first claimant. Callers that need to load-balance
// across all claimants should use ResolveServiceLabelGroup.
func (r *ModelResolver) ResolveServiceLabel(ctx context.Context, labelOrModelName string) string {
	// First check cache
	if modelName, ok := r.labelSnapshot().first[labelOrModelName]; ok {
		return modelName
	}

	// Refresh cache if stale (>5 seconds old) or first time
	r.refreshServiceLabelCache(ctx)

	// Check cache again after refresh
	if modelName, ok := r.labelSnapshot().first[labelOrModelName]; ok {
		return modelName
	}

	// Not a service label, return as-is (it's probably a model name)
	return labelOrModelName
}

// ResolveServiceLabelGroup returns all models that claim a service label.
//
// The returned slice is in stable (sorted) order so callers can use a
// round-robin counter without observing reshuffled positions across refreshes.
// The bool return is true when the input matches a known service label
// (regardless of how many claimants), false when it is not a service label
// (likely a direct model name) so the caller can fall through to alias lookup.
//
// For single-claimant labels this returns a one-element slice; the routing
// path can treat that as a no-op pick.
func (r *ModelResolver) ResolveServiceLabelGroup(ctx context.Context, labelOrModelName string) ([]string, bool) {
	if members, ok := r.labelSnapshot().groups[labelOrModelName]; ok {
		return members, true
	}
	r.refreshServiceLabelCache(ctx)
	if members, ok := r.labelSnapshot().groups[labelOrModelName]; ok {
		return members, true
	}
	return nil, false
}

// labelSnapshot is the published service-label cache; empty before the
// first refresh. Reading a nil map is safe, so callers index it directly.
func (r *ModelResolver) labelSnapshot() *labelSnapshot {
	if snap := r.labels.Load(); snap != nil {
		return snap
	}
	return &labelSnapshot{}
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

	// Second pass: build a new snapshot, published below in one store
	// - serviceLabelCache: first claimant per label (backward compat for ResolveServiceLabel)
	// - labelGroupCache: all claimants per label (for multi-model routing)
	// - labelGroupModels: reverse index from model name to all group members
	snap := &labelSnapshot{
		first:       make(map[string]string, len(labelClaims)),
		groups:      make(map[string][]string, len(labelClaims)),
		groupModels: make(map[string][]string),
	}
	groupMembers := make(map[string]map[string]bool) // modelName -> set of related models

	if r.loggedSharedLabels == nil {
		r.loggedSharedLabels = make(map[string]string)
	}
	currentShared := make(map[string]struct{}, len(labelClaims))
	for label, claimants := range labelClaims {
		// Sort for deterministic ordering across refreshes so callers using a
		// round-robin counter against labelGroupCache see stable positions.
		sort.Strings(claimants)
		if len(claimants) > 1 {
			signature := strings.Join(claimants, ",")
			currentShared[label] = struct{}{}
			if prev, ok := r.loggedSharedLabels[label]; !ok || prev != signature {
				slog.Info("serviceLabel shared by multiple models",
					"label", label, "models", claimants)
				r.loggedSharedLabels[label] = signature
			}
		}
		// First claimant for backward-compat single-model resolution
		snap.first[label] = claimants[0]
		// All claimants for group routing
		snap.groups[label] = claimants
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
		snap.groupModels[model] = members
	}
	r.labels.Store(snap)

	// Drop labels that are no longer shared so a later re-share is logged again.
	for label := range r.loggedSharedLabels {
		if _, still := currentShared[label]; !still {
			delete(r.loggedSharedLabels, label)
		}
	}

	r.lastCacheRefresh = time.Now()
}

// ResolveModelAlias resolves a servedModelName or alias to the K8s Model resource name.
// Returns the K8s name if the alias was resolved, or the original input if no mapping found.
func (r *ModelResolver) ResolveModelAlias(ctx context.Context, nameOrAlias string) string {
	// Check cache first
	if k8sName, ok := r.aliasSnapshot()[nameOrAlias]; ok {
		return k8sName
	}

	// Refresh cache if stale
	r.refreshModelAliasCache(ctx)

	// Check again after refresh
	if k8sName, ok := r.aliasSnapshot()[nameOrAlias]; ok {
		return k8sName
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

	for i := range models.Items {
		m := &models.Items[i]
		resourceName := m.Name
		if m.Spec.LiteLLM == nil {
			continue
		}
		// Parked models (spec.litellm.enabled=false) must not be alias-resolvable
		// — a stale alias should never route a request to a de-advertised model.
		if litellmExplicitlyDisabled(m) {
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

	// Build a new map and publish it in one store.
	cache := make(map[string]string, len(aliasClaims))
	for alias, claimants := range aliasClaims {
		if len(claimants) > 1 {
			slog.Warn("model alias claimed by multiple models",
				"alias", alias, "models", claimants, "using", claimants[0])
		}
		cache[alias] = claimants[0]
		slog.Debug("model alias cache updated", "alias", alias, "model", claimants[0])
	}
	r.aliases.Store(&cache)

	r.lastAliasRefresh = time.Now()
}

// aliasSnapshot is the published alias cache; nil before the first refresh.
func (r *ModelResolver) aliasSnapshot() map[string]string {
	if m := r.aliases.Load(); m != nil {
		return *m
	}
	return nil
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
	_, ok := r.labelSnapshot().groupModels[modelName]
	return ok
}

// RangeLabelGroupModels iterates over all label group model entries.
func (r *ModelResolver) RangeLabelGroupModels(f func(modelName string, groupMembers []string) bool) {
	for model, members := range r.labelSnapshot().groupModels {
		if !f(model, members) {
			return
		}
	}
}
