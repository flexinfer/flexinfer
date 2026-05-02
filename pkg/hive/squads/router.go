package squads

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// DefaultMinConfidence is the v2 starting threshold for routing a backlog
// item to a non-fallback squad. Mirrors `policy.squads.routing.min_confidence`
// in spec §"Policy file additions".
const DefaultMinConfidence = 0.6

// DefaultBaselineConfidence is the confidence the router assigns when a
// matching squad has zero outcomes recorded. Bootstraps a squad's first
// runs without a chicken-and-egg problem; once outcomes accumulate, the
// real measurement takes over.
const DefaultBaselineConfidence = 0.65

// DefaultSampleWindow is the rolling window the router consults for
// SuccessRate. Mirrors the spec's "last 30 outcomes" example.
const DefaultSampleWindow = 30

// Router maps a backlog item to a squad based on path scope and the
// squad's recent outcome history. It is read-only; configuration changes
// flow through the Loader and the canonical store.
type Router struct {
	loader *Loader
	store  *store.Store

	MinConfidence      float64
	BaselineConfidence float64
	SampleWindow       int
}

// NewRouter wires a router with the loader's manifest snapshot as the
// candidate set and the store's squad_outcomes as the confidence input.
func NewRouter(loader *Loader, st *store.Store) *Router {
	return &Router{
		loader:             loader,
		store:              st,
		MinConfidence:      DefaultMinConfidence,
		BaselineConfidence: DefaultBaselineConfidence,
		SampleWindow:       DefaultSampleWindow,
	}
}

// Decision is the router's output: which squad the item routes to, why,
// and how confident the call is. The reconciler reads it to decide
// whether to invoke the squad-specific planner or fall through to the
// v1 generic path.
type Decision struct {
	// SquadName is the chosen squad's name, or FallbackName when no squad
	// scored above MinConfidence.
	SquadName string

	// PathClass is the doublestar glob the chosen squad matched against
	// the item's files. Empty when SquadName == FallbackName.
	PathClass string

	// Confidence is the success rate over the sample window when
	// SampleSize > 0; the BaselineConfidence when the squad matched but
	// had no prior outcomes; or 0 for the fallback decision.
	Confidence float64

	// SampleSize is the count of squad_outcomes rows that informed
	// Confidence (0 means cold-start baseline was used).
	SampleSize int

	// Reason is a short human-readable explanation suitable for logs +
	// the HUD's routing-trace UI.
	Reason string
}

// Pick selects the best squad for a backlog item. It never errors on
// empty input — items with no slice files route to FallbackName so the
// reconciler still progresses.
func (r *Router) Pick(ctx context.Context, item *store.BacklogItem) (Decision, error) {
	if r == nil || r.loader == nil || r.store == nil {
		return Decision{}, errors.New("squads: router is nil")
	}
	if r.MinConfidence <= 0 {
		r.MinConfidence = DefaultMinConfidence
	}
	if r.BaselineConfidence <= 0 {
		r.BaselineConfidence = DefaultBaselineConfidence
	}
	if r.SampleWindow <= 0 {
		r.SampleWindow = DefaultSampleWindow
	}

	paths := candidatePaths(item)
	if len(paths) == 0 {
		return fallback("no candidate paths on item"), nil
	}
	manifests := r.loader.Current()
	if len(manifests) == 0 {
		return fallback("no squads loaded"), nil
	}

	type candidate struct {
		name       string
		pathClass  string
		confidence float64
		sample     int
	}
	var cands []candidate
	for name, m := range manifests {
		if !m.IsEnabled() || name == FallbackName {
			continue
		}
		match := m.MatchesAny(paths)
		if match == "" {
			continue
		}
		conf, n, err := r.confidence(ctx, name, match)
		if err != nil {
			return Decision{}, err
		}
		cands = append(cands, candidate{
			name: name, pathClass: match, confidence: conf, sample: n,
		})
	}
	if len(cands) == 0 {
		return fallback("no squad paths matched item"), nil
	}

	// Order: confidence desc, then path-class length desc (specificity),
	// then name asc (deterministic tie-break).
	sort.Slice(cands, func(i, j int) bool {
		ai, bi := cands[i], cands[j]
		if ai.confidence != bi.confidence {
			return ai.confidence > bi.confidence
		}
		if len(ai.pathClass) != len(bi.pathClass) {
			return len(ai.pathClass) > len(bi.pathClass)
		}
		return ai.name < bi.name
	})

	best := cands[0]
	if best.confidence < r.MinConfidence {
		return Decision{
			SquadName:  FallbackName,
			Confidence: 0,
			SampleSize: best.sample,
			Reason: fmt.Sprintf("best squad %q confidence %.3f < min %.3f",
				best.name, best.confidence, r.MinConfidence),
		}, nil
	}
	return Decision{
		SquadName:  best.name,
		PathClass:  best.pathClass,
		Confidence: best.confidence,
		SampleSize: best.sample,
		Reason:     reasonFor(best.sample, best.confidence),
	}, nil
}

// confidence returns the squad's success rate over the configured window
// for a given path class. Squads with zero historical outcomes use the
// baseline so they bootstrap; squads with measured outcomes use the
// measurement.
func (r *Router) confidence(ctx context.Context, squad, pathClass string) (float64, int, error) {
	rate, n, err := r.store.Squads.SuccessRate(ctx, squad, pathClass, r.SampleWindow)
	if err != nil {
		return 0, 0, fmt.Errorf("squads: success rate %s/%s: %w", squad, pathClass, err)
	}
	if n == 0 {
		return r.BaselineConfidence, 0, nil
	}
	return rate, n, nil
}

// candidatePaths collects every file path declared on the backlog item's
// slices. When slices are empty (rare; unsliced item), the spec doc path
// is used as a single-element fallback.
func candidatePaths(item *store.BacklogItem) []string {
	if item == nil {
		return nil
	}
	var paths []string
	for _, sl := range item.Slices {
		paths = append(paths, sl.Files...)
	}
	if len(paths) == 0 && item.SpecDoc != "" {
		paths = []string{item.SpecDoc}
	}
	return paths
}

func fallback(reason string) Decision {
	return Decision{SquadName: FallbackName, Reason: reason}
}

func reasonFor(sample int, conf float64) string {
	if sample == 0 {
		return fmt.Sprintf("baseline confidence %.3f (no prior outcomes)", conf)
	}
	return fmt.Sprintf("success_rate=%.3f over %d outcomes", conf, sample)
}
