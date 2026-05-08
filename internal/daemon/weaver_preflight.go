package daemon

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/flexinfer"
	"github.com/crb2nu/loom/pkg/weaver"
)

// WeaverPreflight summarizes the result of comparing weaver's configured
// router/subagent/domain models against what FlexInfer's /v1/models
// catalog actually advertises. Surfaces in `loom/weaver/status` so
// operators see a degraded banner in HUD/iOS/extension instead of
// silent 404s on the first query.
//
// See .loom/111-product-spec-weaver-qwen3-integration-2026-05-08.md
// (PRE-001..PRE-003).
type WeaverPreflight struct {
	// Degraded is true when at least one configured model isn't in the
	// FlexInfer catalog (or the catalog could not be fetched).
	Degraded bool `json:"degraded"`
	// MissingModels lists the configured-but-not-advertised models.
	// Ordered for stable JSON output.
	MissingModels []string `json:"missing_models"`
	// ReadyModels lists the catalog model IDs that ARE configured.
	// Ordered for stable JSON output.
	ReadyModels []string `json:"ready_models"`
	// CatalogSize is the total number of models advertised by the
	// FlexInfer proxy (including ones we don't use). Useful in the HUD
	// to confirm the proxy is reachable even if our specific models
	// aren't there.
	CatalogSize int `json:"catalog_size"`
	// CatalogError is non-empty when the /v1/models call failed. When
	// set, MissingModels is the full configured set (we couldn't
	// verify any) and ReadyModels is empty.
	CatalogError string `json:"catalog_error,omitempty"`
	// CheckedAt is the wall-clock time the preflight ran.
	CheckedAt time.Time `json:"checked_at"`
}

// preflightDeadline caps how long the daemon waits for /v1/models at
// boot. Short because we never want preflight to delay daemon startup;
// failure here logs a warning and we proceed.
const preflightDeadline = 5 * time.Second

// modelLister is the slice of pkg/flexinfer.Client we depend on. Lifts
// out for tests; production code passes the real client.
type modelLister interface {
	Models(ctx context.Context) ([]flexinfer.ModelInfo, error)
}

// runWeaverPreflight queries FlexInfer for its model catalog and
// compares it against the configured router/subagent/domain models.
// Never returns an error; the caller treats CatalogError as the
// degraded signal.
func runWeaverPreflight(ctx context.Context, client modelLister, cfg weaver.Config, registry *weaver.DomainRegistry) WeaverPreflight {
	pre := WeaverPreflight{CheckedAt: time.Now().UTC()}

	configured := configuredModels(cfg, registry)

	pctx, cancel := context.WithTimeout(ctx, preflightDeadline)
	defer cancel()

	models, err := client.Models(pctx)
	if err != nil {
		pre.Degraded = true
		pre.CatalogError = err.Error()
		pre.MissingModels = sortedKeys(configured)
		return pre
	}

	pre.CatalogSize = len(models)
	advertised := make(map[string]bool, len(models))
	for _, m := range models {
		advertised[m.ID] = true
	}

	for name := range configured {
		if advertised[name] {
			pre.ReadyModels = append(pre.ReadyModels, name)
		} else {
			pre.MissingModels = append(pre.MissingModels, name)
		}
	}
	sort.Strings(pre.ReadyModels)
	sort.Strings(pre.MissingModels)
	pre.Degraded = len(pre.MissingModels) > 0
	return pre
}

// configuredModels returns the set of model names weaver expects to
// route to: router model, subagent model, and any per-domain Model
// overrides. Empty strings (defaults) are skipped.
func configuredModels(cfg weaver.Config, registry *weaver.DomainRegistry) map[string]struct{} {
	out := make(map[string]struct{}, 4)
	add := func(name string) {
		if name == "" {
			return
		}
		out[name] = struct{}{}
	}
	add(cfg.RouterModel)
	add(cfg.SubagentModel)
	if registry != nil {
		for _, sub := range registry.List() {
			// Only flexinfer-backed domains route through the proxy;
			// spawn-backed domains hit headless agent pods instead.
			if sub.IsFlexInferBackend() {
				add(sub.Model)
			}
		}
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// preflightStore is a tiny mutex-guarded holder used by the daemon to
// publish the latest preflight result without exposing the field
// directly. Keeps reads safe across the JSON-RPC dispatch goroutines.
type preflightStore struct {
	mu      sync.RWMutex
	current WeaverPreflight
	set     bool
}

func (s *preflightStore) Set(p WeaverPreflight) {
	s.mu.Lock()
	s.current = p
	s.set = true
	s.mu.Unlock()
}

func (s *preflightStore) Get() (WeaverPreflight, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current, s.set
}
