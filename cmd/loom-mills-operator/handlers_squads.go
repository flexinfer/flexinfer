package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/crb2nu/loom/pkg/mills/squads"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// squadsMemoryDefaultLimit is the default page size for memory recall.
const squadsMemoryDefaultLimit = 20

// squadsMemoryMaxLimit caps memory recall to prevent the canonical store
// from being asked to materialise unbounded result sets in one request.
const squadsMemoryMaxLimit = 200

// squadsOutcomesDefaultLimit is the default page size for outcomes.
const squadsOutcomesDefaultLimit = 30

// squadsOutcomesMaxLimit caps the per-request outcomes window.
const squadsOutcomesMaxLimit = 200

// squadsRecentMemory is the size of the per-squad memory page returned
// inline by GET /api/mills/squads and GET /api/mills/squads/{name}.
const squadsRecentMemory = 20

// squadsOutcomeWindow is the rolling window the list view uses to compute
// the success rate joined into each squad row. Mirrors the router's
// DefaultSampleWindow so the HUD's headline number agrees with the
// router's actual confidence input.
const squadsOutcomeWindow = 30

// squadsListEntry is the per-row shape returned by GET /api/mills/squads.
// The squad fields are flattened on top of an outcome stats sub-object so
// the HUD can render a card without a second round-trip.
type squadsListEntry struct {
	Squad        *store.Squad      `json:"squad"`
	OutcomeStats squadsOutcomeStat `json:"outcome_stats"`
}

// squadsOutcomeStat is the join of the per-squad rolling outcomes that
// the list + detail endpoints surface.
type squadsOutcomeStat struct {
	Window         int     `json:"window"`           // outcome rows considered
	Total          int     `json:"total"`            // outcomes in the window
	MergedClean    int     `json:"merged_clean"`     // success count
	MergedRegress  int     `json:"merged_regressed"` // post-merge regression
	Failed         int     `json:"failed"`           // non-merged failure
	SelfVetoed     int     `json:"self_vetoed"`      // pre-merge self-veto
	SuccessRate    float64 `json:"success_rate"`     // merged_clean / total
	TotalCostUSD   float64 `json:"total_cost_usd"`   // sum CostUSD over window
	InFlightActive int     `json:"in_flight"`        // active pipeline runs (slice fills 0; left for follow-up)
}

// squadDetail is the response shape for GET /api/mills/squads/{name}.
type squadDetail struct {
	Squad        *store.Squad          `json:"squad"`
	RecentMemory []*store.SquadMemory  `json:"recent_memory"`
	Outcomes     []*store.SquadOutcome `json:"recent_outcomes"`
	OutcomeStats squadsOutcomeStat     `json:"outcome_stats"`
}

// squadRouteTestRequest is the admin POST body for /route-test.
type squadRouteTestRequest struct {
	BacklogID string `json:"backlog_id"`
}

// squadRouteTestCandidate is one row in the candidate scoring set the
// route-test response returns. The router's Pick() only exposes the
// winning Decision; the route-test endpoint runs the same scoring loop
// inline so HUD can render the full ranking.
type squadRouteTestCandidate struct {
	Name       string  `json:"name"`
	PathClass  string  `json:"path_class"`
	Confidence float64 `json:"confidence"`
	SampleSize int     `json:"sample_size"`
	Enabled    bool    `json:"enabled"`
}

// squadRouteTestResponse is the route-test response body.
type squadRouteTestResponse struct {
	BacklogID  string                    `json:"backlog_id"`
	Decision   squads.Decision           `json:"decision"`
	Candidates []squadRouteTestCandidate `json:"candidates"`
}

// handleSquadsList returns every squad row joined with its 30-row outcome
// stats. Returns [] (not 500) when no squads are loaded so the HUD can
// render an empty state without special-casing the call.
func (o *operator) handleSquadsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := o.store.Squads.ListSquads(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]squadsListEntry, 0, len(rows))
	for _, sq := range rows {
		stats, err := o.computeOutcomeStats(ctx, sq.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out = append(out, squadsListEntry{Squad: sq, OutcomeStats: stats})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSquadGet returns one squad's full detail: the row, recent memory
// (top N by importance), and the recent outcomes. 404 when the squad is
// not in the canonical store.
func (o *operator) handleSquadGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "missing squad name", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	sq, err := o.store.Squads.GetSquad(ctx, name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "squad not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	memory, err := o.store.Squads.MemoryRecall(ctx, name, "", squadsRecentMemory)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	outcomes, err := o.store.Squads.ListOutcomes(ctx, name, squadsOutcomeWindow)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stats, err := o.computeOutcomeStats(ctx, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, squadDetail{
		Squad:        sq,
		RecentMemory: memory,
		Outcomes:     outcomes,
		OutcomeStats: stats,
	})
}

// handleSquadMemory returns paginated working-memory rows for a squad.
// Optional `kind` filter; `limit` defaults to 20 and caps at 200. The
// squad must exist in the canonical store; 404 otherwise.
func (o *operator) handleSquadMemory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "missing squad name", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if _, err := o.store.Squads.GetSquad(ctx, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "squad not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), squadsMemoryDefaultLimit)
	if limit > squadsMemoryMaxLimit {
		limit = squadsMemoryMaxLimit
	}
	kind := store.SquadMemoryKind(r.URL.Query().Get("kind"))
	rows, err := o.store.Squads.MemoryRecall(ctx, name, kind, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []*store.SquadMemory{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleSquadOutcomes returns the most recent N outcomes for a squad.
// The squad must exist; 404 otherwise. limit default 30, cap 200.
func (o *operator) handleSquadOutcomes(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "missing squad name", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if _, err := o.store.Squads.GetSquad(ctx, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "squad not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), squadsOutcomesDefaultLimit)
	if limit > squadsOutcomesMaxLimit {
		limit = squadsOutcomesMaxLimit
	}
	rows, err := o.store.Squads.ListOutcomes(ctx, name, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []*store.SquadOutcome{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleSquadRouteTest is the admin-gated dry-run of the squad router. It
// loads the named backlog item, runs squads.NewRouter against the live
// loader snapshot + canonical store, and returns the chosen Decision plus
// the candidate scoring set. The router is constructed inline so any
// fsnotify-driven manifest reload is reflected on the next call.
//
// The path's {name} is the squad name the caller wants to test; it isn't
// load-bearing for routing (the router decides which squad), but it's
// included in the URL for symmetry with the rest of the squads surface
// and to give policy administrators a clear "I'm testing X" intent.
func (o *operator) handleSquadRouteTest(w http.ResponseWriter, r *http.Request) {
	if o.squadsLoader == nil {
		http.Error(w, "squads loader not configured (missing LOOM_MILLS_SQUADS_PATH or empty manifest dir)",
			http.StatusServiceUnavailable)
		return
	}

	var req squadRouteTestRequest
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if req.BacklogID == "" {
		http.Error(w, "backlog_id is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	item, err := o.store.Backlog.Get(ctx, req.BacklogID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "backlog item not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	router := squads.NewRouter(o.squadsLoader, o.store)
	decision, err := router.Pick(ctx, item)
	if err != nil {
		http.Error(w, "route: "+err.Error(), http.StatusInternalServerError)
		return
	}

	cands, err := o.scoreSquadCandidates(ctx, router, item)
	if err != nil {
		http.Error(w, "score candidates: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, squadRouteTestResponse{
		BacklogID:  req.BacklogID,
		Decision:   decision,
		Candidates: cands,
	})
}

// computeOutcomeStats joins the squad's recent outcomes into the summary
// shape the list + detail endpoints return. Walks ListOutcomes once so
// each call against a squad is a single DB read.
func (o *operator) computeOutcomeStats(ctx context.Context, squad string) (squadsOutcomeStat, error) {
	stat := squadsOutcomeStat{Window: squadsOutcomeWindow}
	rows, err := o.store.Squads.ListOutcomes(ctx, squad, squadsOutcomeWindow)
	if err != nil {
		return stat, err
	}
	for _, o := range rows {
		stat.Total++
		stat.TotalCostUSD += o.CostUSD
		switch o.Outcome {
		case store.SquadOutcomeMergedClean:
			stat.MergedClean++
		case store.SquadOutcomeMergedRegressed:
			stat.MergedRegress++
		case store.SquadOutcomeFailed:
			stat.Failed++
		case store.SquadOutcomeSelfVetoed:
			stat.SelfVetoed++
		}
	}
	if stat.Total > 0 {
		stat.SuccessRate = float64(stat.MergedClean) / float64(stat.Total)
	}
	return stat, nil
}

// scoreSquadCandidates re-runs the squad scoring loop the router uses
// internally so the response can show the full ranking, not just the
// winner. Order: confidence DESC, then path-class length DESC, then name
// ASC — matches squads.Router.Pick's sort.
func (o *operator) scoreSquadCandidates(ctx context.Context, router *squads.Router, item *store.BacklogItem) ([]squadRouteTestCandidate, error) {
	manifests := o.squadsLoader.Current()
	if len(manifests) == 0 {
		return []squadRouteTestCandidate{}, nil
	}
	paths := backlogItemPaths(item)
	out := make([]squadRouteTestCandidate, 0, len(manifests))
	for name, m := range manifests {
		if name == squads.FallbackName {
			continue
		}
		match := m.MatchesAny(paths)
		if match == "" {
			continue
		}
		rate, n, err := o.store.Squads.SuccessRate(ctx, name, match, router.SampleWindow)
		if err != nil {
			return nil, err
		}
		conf := rate
		if n == 0 {
			conf = router.BaselineConfidence
		}
		out = append(out, squadRouteTestCandidate{
			Name:       name,
			PathClass:  match,
			Confidence: conf,
			SampleSize: n,
			Enabled:    m.IsEnabled(),
		})
	}
	sortCandidates(out)
	return out, nil
}

// backlogItemPaths mirrors squads.candidatePaths (which is unexported).
// Kept here so the route-test handler can build the same candidate set
// without exporting that helper from the router package.
func backlogItemPaths(item *store.BacklogItem) []string {
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

// sortCandidates orders the candidate set by confidence DESC,
// path-class length DESC, name ASC — same predicate the router uses, so
// the response's first row matches Decision.SquadName whenever a match
// exists at or above MinConfidence.
func sortCandidates(c []squadRouteTestCandidate) {
	// Insertion sort keeps the dependency-light surface small (the file
	// is already imports-heavy) and the typical N is < 10 squads, so
	// O(n^2) is fine.
	for i := 1; i < len(c); i++ {
		j := i
		for j > 0 && candidateLess(c[j], c[j-1]) {
			c[j], c[j-1] = c[j-1], c[j]
			j--
		}
	}
}

func candidateLess(a, b squadRouteTestCandidate) bool {
	if a.Confidence != b.Confidence {
		return a.Confidence > b.Confidence
	}
	if len(a.PathClass) != len(b.PathClass) {
		return len(a.PathClass) > len(b.PathClass)
	}
	return a.Name < b.Name
}
