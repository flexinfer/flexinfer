package main

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// handlePolicyProposalsList returns proposals filtered by state.
// Default ?state=pending. ?state=all expands to every lifecycle state so
// the HUD can render history without needing N polls. Phase 7 slice 7.2.
func (o *operator) handlePolicyProposalsList(w http.ResponseWriter, r *http.Request) {
	stateParam := r.URL.Query().Get("state")
	var (
		rows []*store.PolicyProposal
		err  error
	)
	switch stateParam {
	case "":
		rows, err = o.store.PolicyProposals.ListByState(r.Context(), store.PolicyProposalPending)
	case "all":
		// Convenience: explicit "all" returns every row regardless of
		// state. The DAO doesn't expose a bulk lister, so iterate the
		// known state values and concatenate. Order is best-effort
		// (newest-first within each state) — HUD renders chronologically
		// from CreatedAt anyway.
		rows = []*store.PolicyProposal{}
		for _, s := range []store.PolicyProposalState{
			store.PolicyProposalPending,
			store.PolicyProposalAppliedHuman,
			store.PolicyProposalAppliedAuto,
			store.PolicyProposalRejected,
			store.PolicyProposalReverted,
		} {
			batch, e := o.store.PolicyProposals.ListByState(r.Context(), s)
			if e != nil {
				err = e
				break
			}
			rows = append(rows, batch...)
		}
	default:
		rows, err = o.store.PolicyProposals.ListByState(r.Context(), store.PolicyProposalState(stateParam))
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// handlePolicyProposalApply transitions a pending proposal to applied_human
// and stamps a 24h revert_deadline. Phase 7 slice 7.2. v2.0 only marks
// the row; the actual policy.yaml mutation is a manual human follow-up.
// v2.1 will wire auto-apply for `relax` proposals against this same row.
func (o *operator) handlePolicyProposalApply(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProposalID(w, r)
	if !ok {
		return
	}
	deadline := time.Now().UTC().Add(24 * time.Hour)
	if err := o.store.PolicyProposals.Apply(r.Context(), id, store.PolicyProposalAppliedHuman, deadline); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "policy proposal not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := o.store.PolicyProposals.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handlePolicyProposalReject transitions a pending proposal to rejected.
// Phase 7 slice 7.2. Already-applied / reverted proposals return 404
// because the DAO's UPDATE WHERE state='pending' affects 0 rows in
// those cases — surfacing that as 404 keeps the contract simple.
func (o *operator) handlePolicyProposalReject(w http.ResponseWriter, r *http.Request) {
	id, ok := parseProposalID(w, r)
	if !ok {
		return
	}
	if err := o.store.PolicyProposals.Reject(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "policy proposal not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := o.store.PolicyProposals.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// parseProposalID extracts {id} from the path and rejects malformed
// values with 400. Returns the integer id and whether the caller may
// proceed.
func parseProposalID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "id must be a positive integer", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
