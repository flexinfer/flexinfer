package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// crossRepoListDefaultLimit caps the default page size for the list view.
const crossRepoListDefaultLimit = 50

// crossRepoListMaxLimit caps the per-request window so a single GET can't
// drag every historical run into one response payload.
const crossRepoListMaxLimit = 200

// crossRepoValidStates enumerates the lifecycle states a caller is
// allowed to filter on. Mirrors store.CrossRepoState constants. Kept
// here so the 400 response can quote the exact accepted set without
// reflecting over package-level vars.
var crossRepoValidStates = []store.CrossRepoState{
	store.CrossRepoPlanning,
	store.CrossRepoOpen,
	store.CrossRepoGatesGreen,
	store.CrossRepoMerging,
	store.CrossRepoMerged,
	store.CrossRepoReverted,
	store.CrossRepoFailed,
}

// crossRepoTerminalStates are states an abort cannot transition out of.
// A run that already merged, reverted, or failed has no live work for
// the integrator to interrupt — return 409 instead of silently moving
// the row.
var crossRepoTerminalStates = map[store.CrossRepoState]bool{
	store.CrossRepoMerged:   true,
	store.CrossRepoReverted: true,
	store.CrossRepoFailed:   true,
}

// crossRepoAbortableStates are the states a run can transition out of
// when an admin aborts it. Anything outside this set is rejected.
var crossRepoAbortableStates = map[store.CrossRepoState]bool{
	store.CrossRepoOpen:       true,
	store.CrossRepoGatesGreen: true,
	store.CrossRepoMerging:    true,
}

// crossRepoRunSummary is the per-row shape returned by the list/get
// handlers. Mirrors store.CrossRepoRun but exposes JSON tags so the
// HUD doesn't have to parse the DAO struct's field-name convention.
type crossRepoRunSummary struct {
	ID                string                     `json:"id"`
	BacklogItemID     string                     `json:"backlog_item_id"`
	State             store.CrossRepoState       `json:"state"`
	AtomicityStrategy string                     `json:"atomicity_strategy"`
	Repos             []store.CrossRepoRepoEntry `json:"repos"`
	CreatedAt         time.Time                  `json:"created_at"`
	UpdatedAt         time.Time                  `json:"updated_at"`
}

// crossRepoListResponse is the GET /runs envelope. Total reports the
// pre-limit count so the HUD can render "showing N of M" without a
// second round-trip.
type crossRepoListResponse struct {
	Runs   []crossRepoRunSummary `json:"runs"`
	Total  int                   `json:"total"`
	Limit  int                   `json:"limit"`
	Filter string                `json:"filter,omitempty"`
}

// crossRepoAbortResponse is the POST /abort envelope. Returns the new
// state plus what the previous state was so the HUD can render a
// "moved from X to Y at T" line without keeping local state.
type crossRepoAbortResponse struct {
	ID            string               `json:"id"`
	State         store.CrossRepoState `json:"state"`
	PreviousState store.CrossRepoState `json:"previous_state"`
	AbortedAt     time.Time            `json:"aborted_at"`
}

// handleCrossRepoList returns cross-repo runs. Filters:
//   - backlog_id: most-specific filter, takes precedence over state
//   - state: comma-separated lifecycle states (e.g. "open,gates_green")
//
// Returns 400 if state contains an unknown value. Returns an empty
// list (not 500) when the store has no rows — the HUD's empty state
// must not be a degraded response.
func (o *operator) handleCrossRepoList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	backlogID := strings.TrimSpace(q.Get("backlog_id"))
	stateRaw := strings.TrimSpace(q.Get("state"))

	limit := parseLimit(q.Get("limit"), crossRepoListDefaultLimit)
	if limit > crossRepoListMaxLimit {
		limit = crossRepoListMaxLimit
	}

	// "Most-specific filter wins": backlog_id is row-set narrowing, state
	// is row-set categorisation. Logging the conflict makes the choice
	// debuggable without breaking a caller who passed both by mistake.
	if backlogID != "" && stateRaw != "" {
		o.logger.Warn("cross-repo list: backlog_id + state both set; preferring backlog_id",
			"backlog_id", backlogID, "state", stateRaw)
	}

	rows, filter, err := o.fetchCrossRepoRows(ctx, backlogID, stateRaw)
	if err != nil {
		if errors.Is(err, errCrossRepoBadState) {
			http.Error(w, "invalid state value(s); allowed: "+
				strings.Join(crossRepoValidStateStrings(), ","),
				http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	out := make([]crossRepoRunSummary, 0, len(rows))
	for _, run := range rows {
		out = append(out, summarizeCrossRepoRun(run))
	}
	writeJSON(w, http.StatusOK, crossRepoListResponse{
		Runs:   out,
		Total:  total,
		Limit:  limit,
		Filter: filter,
	})
}

// handleCrossRepoGet returns a single cross-repo run by id. 404 with a
// clear message when the row isn't in the canonical store.
func (o *operator) handleCrossRepoGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "missing run id", http.StatusBadRequest)
		return
	}
	run, err := o.store.CrossRepo.GetRun(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "cross-repo run not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, summarizeCrossRepoRun(run))
}

// handleCrossRepoAbort moves a live cross-repo run into the "failed"
// state. Admin-gated by the requireAdmin wrap on the route. The abort
// is a state-mark + audit trail; a follow-up slice can wire the
// integrator interrupt.
//
// TODO(slice 4.4 followup): signal running integrator via
// Integrator.Abort(id) once that interface exists.
func (o *operator) handleCrossRepoAbort(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "missing run id", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	run, err := o.store.CrossRepo.GetRun(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "cross-repo run not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	prev := run.State
	if crossRepoTerminalStates[prev] {
		http.Error(w, "cross-repo run already in terminal state "+string(prev)+
			"; abort rejected", http.StatusConflict)
		return
	}
	if !crossRepoAbortableStates[prev] {
		http.Error(w, "cross-repo run in state "+string(prev)+
			" is not abortable; expected one of: "+
			strings.Join(crossRepoAbortableStateStrings(), ","),
			http.StatusConflict)
		return
	}
	if err := o.store.CrossRepo.SetState(ctx, id, store.CrossRepoFailed); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "cross-repo run not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now().UTC()
	o.logger.Info("cross-repo run aborted",
		"id", id, "previous_state", string(prev))
	writeJSON(w, http.StatusOK, crossRepoAbortResponse{
		ID:            id,
		State:         store.CrossRepoFailed,
		PreviousState: prev,
		AbortedAt:     now,
	})
}

// errCrossRepoBadState is returned by fetchCrossRepoRows when the
// caller's state filter contains an unknown value. The handler turns
// it into a 400; tests rely on the sentinel to assert the path.
var errCrossRepoBadState = errors.New("cross-repo: invalid state filter")

// fetchCrossRepoRows resolves the list query against the DAO and
// returns the rows + a human-readable filter description suitable for
// echoing back to the caller.
func (o *operator) fetchCrossRepoRows(ctx context.Context, backlogID, stateRaw string) ([]*store.CrossRepoRun, string, error) {
	if backlogID != "" {
		rows, err := o.store.CrossRepo.ListByBacklog(ctx, backlogID)
		if err != nil {
			return nil, "", err
		}
		return rows, "backlog_id=" + backlogID, nil
	}
	if stateRaw == "" {
		// No filter: union ListByState across every valid state. The
		// DAO doesn't expose a list-all method this slice can rely on,
		// so we fan out instead — keeps this slice independent of any
		// DAO change a sibling worktree might land.
		var out []*store.CrossRepoRun
		for _, st := range crossRepoValidStates {
			rows, err := o.store.CrossRepo.ListByState(ctx, st)
			if err != nil {
				return nil, "", err
			}
			out = append(out, rows...)
		}
		sortCrossRepoRowsNewestFirst(out)
		return out, "", nil
	}
	wanted, err := parseCrossRepoStateFilter(stateRaw)
	if err != nil {
		return nil, "", err
	}
	var out []*store.CrossRepoRun
	for _, st := range wanted {
		rows, err := o.store.CrossRepo.ListByState(ctx, st)
		if err != nil {
			return nil, "", err
		}
		out = append(out, rows...)
	}
	sortCrossRepoRowsNewestFirst(out)
	return out, "state=" + stateRaw, nil
}

// parseCrossRepoStateFilter splits the comma-separated `state` query
// parameter into store.CrossRepoState values, rejecting unknowns.
func parseCrossRepoStateFilter(raw string) ([]store.CrossRepoState, error) {
	parts := strings.Split(raw, ",")
	allowed := make(map[store.CrossRepoState]bool, len(crossRepoValidStates))
	for _, st := range crossRepoValidStates {
		allowed[st] = true
	}
	out := make([]store.CrossRepoState, 0, len(parts))
	for _, p := range parts {
		s := store.CrossRepoState(strings.TrimSpace(p))
		if s == "" {
			continue
		}
		if !allowed[s] {
			return nil, errCrossRepoBadState
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, errCrossRepoBadState
	}
	return out, nil
}

// summarizeCrossRepoRun maps the DAO struct to the JSON envelope. Kept
// separate from the type so an empty Repos slice still serialises as
// `[]` rather than `null`.
func summarizeCrossRepoRun(r *store.CrossRepoRun) crossRepoRunSummary {
	repos := r.Repos
	if repos == nil {
		repos = []store.CrossRepoRepoEntry{}
	}
	return crossRepoRunSummary{
		ID:                r.ID,
		BacklogItemID:     r.BacklogItemID,
		State:             r.State,
		AtomicityStrategy: r.AtomicityStrategy,
		Repos:             repos,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

// sortCrossRepoRowsNewestFirst orders the unioned rows by creation
// time descending so the most recent runs lead the response.
func sortCrossRepoRowsNewestFirst(rows []*store.CrossRepoRun) {
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
}

// crossRepoValidStateStrings renders the valid-state set as []string for
// human-facing error messages.
func crossRepoValidStateStrings() []string {
	out := make([]string, 0, len(crossRepoValidStates))
	for _, s := range crossRepoValidStates {
		out = append(out, string(s))
	}
	return out
}

// crossRepoAbortableStateStrings renders the abortable-state set as
// []string for the 409 message body.
func crossRepoAbortableStateStrings() []string {
	out := make([]string, 0, len(crossRepoAbortableStates))
	for s := range crossRepoAbortableStates {
		out = append(out, string(s))
	}
	sort.Strings(out)
	return out
}
