package mobile

import (
	"net/http"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
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

	var (
		tasks []bridge.TaskInfo
		err   error
	)
	if sessionFilter != "" {
		tasks, err = d.deps.Agent().Tasks(sessionFilter)
	} else {
		tasks, err = d.deps.Agent().AllTasks()
	}
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list tasks")
		return
	}
	if tasks == nil {
		tasks = []bridge.TaskInfo{}
	}

	filtered := make([]bridge.TaskInfo, 0, len(tasks))
	counts := taskCounts{}
	for _, t := range tasks {
		taskStatus := normalizeMobileTaskStatus(t.Status)
		if statusFilter != "" && taskStatus != statusFilter {
			continue
		}
		if agentFilter != "" && !strings.EqualFold(strings.TrimSpace(t.AgentID), agentFilter) {
			continue
		}
		if searchFilter != "" {
			searchHaystack := strings.ToLower(strings.Join([]string{
				t.ID, t.SessionID, t.AgentID, t.Namespace, t.Title, t.Context,
			}, " "))
			if !strings.Contains(searchHaystack, searchFilter) {
				continue
			}
		}
		filtered = append(filtered, t)
		switch taskStatus {
		case "pending":
			counts.Pending++
		case "in_progress":
			counts.InProgress++
		case "blocked":
			counts.Blocked++
		case "completed":
			counts.Completed++
		}
	}

	sortSliceStable(filtered, func(i, j int) bool {
		ti := parseMobileTime(filtered[i].UpdatedAt)
		tj := parseMobileTime(filtered[j].UpdatedAt)
		if ti.Equal(tj) {
			return filtered[i].ID < filtered[j].ID
		}
		return ti.After(tj)
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	result := make([]taskDTO, len(filtered))
	for i, t := range filtered {
		result[i] = MapMobileTask(t)
	}

	snap := d.deps.Monitors().Fleet.Snapshot()
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
