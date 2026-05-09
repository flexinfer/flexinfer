// api_engrams.go exposes the engram tech-tree summary to the HUD.
//
// The summary endpoint is a thin aggregator over agent_engram_list — it
// returns counts by proof_status and tier so the catalog view can render a
// single-line "Engrams: N verified · M stale · K failing" badge without the
// frontend having to walk the full library.
package hud

import (
	"net/http"
)

// handleEngramSummary returns aggregate counts of engrams by proof_status
// and tier.
//
// Response shape:
//
//	{
//	  "total":     <int>,
//	  "by_status": {"unverified": int, "verified": int, "stale": int, "failing": int},
//	  "by_tier":   {"tier:1": int, "tier:2": int, "tier:3": int}
//	}
//
// The agent bridge is required; if it is not configured (e.g. tests with a
// minimal App), the endpoint returns an empty summary instead of erroring
// so the catalog view can render a "no data yet" state.
func (a *App) handleEngramSummary(w http.ResponseWriter, r *http.Request) {
	if a.agent == nil {
		a.writeJSON(w, http.StatusOK, emptyEngramSummary())
		return
	}

	summary, err := a.agent.EngramSummary()
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "engram summary", err)
		return
	}

	a.writeJSON(w, http.StatusOK, summary)
}

// emptyEngramSummary returns a zero-valued summary with all keys present so
// frontend code can index without nil checks.
func emptyEngramSummary() map[string]any {
	return map[string]any{
		"total": 0,
		"by_status": map[string]int{
			"unverified": 0,
			"verified":   0,
			"stale":      0,
			"failing":    0,
		},
		"by_tier": map[string]int{},
	}
}
