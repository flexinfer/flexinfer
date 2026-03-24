package mobile

import (
	"net/http"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

func (d *MobileDomain) handleMobileTasks(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	limit := parseMobileLimit(r, DefaultLimit, MaxLimit)
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	sessionFilter := strings.TrimSpace(r.URL.Query().Get("session_id"))
	searchFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))

	snap := monitor.FleetSnapshot{}
	if fleet := d.deps.Monitors().Fleet; fleet != nil {
		snap = fleet.Snapshot()
	}

	var (
		tasks []bridge.TaskInfo
		err   error
	)
	if sessionFilter == "" && (len(snap.Tasks) > 0 || len(snap.Agents) > 0) {
		// Prefer the already-polled fleet snapshot for the default task feed.
		// Under daemon lock pressure, direct task-list calls can block long
		// enough to make the mobile surface feel broken even though the snapshot
		// already has usable task/projected-task context.
		tasks = snap.Tasks
	} else if sessionFilter != "" {
		tasks, err = d.deps.Agent().Tasks(sessionFilter)
	} else {
		tasks, err = d.deps.Agent().AllTasks()
	}
	if err != nil {
		if fleet := d.deps.Monitors().Fleet; fleet != nil && fleet.Ready() && len(snap.Tasks) == 0 {
			if refreshErr := fleet.Refresh(); refreshErr == nil {
				snap = fleet.Snapshot()
			}
		}
		tasks = snap.Tasks
	} else if len(tasks) == 0 && len(snap.Tasks) > 0 {
		// A successful-but-empty response is usually a transient refresh race.
		// Keep serving the last known good fleet snapshot instead of blanking
		// the mobile task feed.
		tasks = snap.Tasks
	}
	if tasks == nil {
		tasks = []bridge.TaskInfo{}
	}

	result := buildMobileTaskFeed(tasks, snap)
	if len(result) == 0 && err != nil && len(snap.Tasks) == 0 {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_unavailable", "failed to load tasks")
		return
	}
	result = filterMobileTasks(result, statusFilter, agentFilter, sessionFilter, searchFilter)
	counts := summarizeMobileTaskCounts(result)
	if len(result) > limit {
		result = result[:limit]
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"tasks":  result,
		"counts": counts,
		"coordination": map[string]any{
			"summary":          snap.Coordination.Summary,
			"blockers":         limitMobileSlice(filterMobileTaskBlockers(snap.Coordination.Blockers, result, agentFilter, sessionFilter), 10),
			"risky_namespaces": limitMobileSlice(filterMobileNamespaces(snap.Coordination.Namespaces, result), 6),
		},
	})
}
