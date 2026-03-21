package fleet

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// handleAgentDispatch dispatches a task to a specific agent from the HUD.
func (d *FleetDomain) handleAgentDispatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetAgentID   string   `json:"target_agent_id"`
		SourceSessionID string   `json:"source_session_id"`
		Title           string   `json:"title"`
		Context         string   `json:"context"`
		Priority        string   `json:"priority"`
		Tags            []string `json:"tags"`
		FilePath        string   `json:"file_path"`
		LineNumber      int      `json:"line_number"`
		BlockedBy       []string `json:"blocked_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	body.TargetAgentID = strings.TrimSpace(body.TargetAgentID)
	body.SourceSessionID = strings.TrimSpace(body.SourceSessionID)
	body.Title = strings.TrimSpace(body.Title)
	body.Context = strings.TrimSpace(body.Context)
	body.FilePath = strings.TrimSpace(body.FilePath)
	body.Tags = normalizeStringList(body.Tags)
	body.BlockedBy = normalizeStringList(body.BlockedBy)
	body.Priority = normalizeTaskPriority(body.Priority)
	if body.LineNumber < 0 {
		body.LineNumber = 0
	}

	if body.TargetAgentID == "" || body.Title == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "target_agent_id and title are required", nil)
		return
	}

	result, err := d.deps.Agent().DispatchTask(bridge.DispatchTaskParams{
		TargetAgentID:   body.TargetAgentID,
		SourceSessionID: body.SourceSessionID,
		Title:           body.Title,
		Context:         body.Context,
		Priority:        body.Priority,
		Tags:            body.Tags,
		FilePath:        body.FilePath,
		LineNumber:      body.LineNumber,
		BlockedBy:       body.BlockedBy,
	})
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to dispatch task", err)
		return
	}

	d.deps.BroadcastAgentEvent("agent.task.dispatched", map[string]any{
		"target_agent_id": body.TargetAgentID,
		"title":           body.Title,
		"priority":        body.Priority,
	})

	go d.deps.FleetRefresh()

	d.deps.WriteJSON(w, http.StatusOK, result)
}

// handleClaimRelease force-releases a file claim for an agent.
func (d *FleetDomain) handleClaimRelease(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	filePath := r.PathValue("file_path")
	if agentID == "" || filePath == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "agent_id and file_path are required", nil)
		return
	}

	if err := d.deps.Agent().ReleaseFileClaim(agentID, filePath); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to release claim", err)
		return
	}

	d.deps.BroadcastAgentEvent("hud.claim.released", map[string]any{
		"agent_id":  agentID,
		"file_path": filePath,
	})

	go d.deps.FleetRefresh()

	d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "released"})
}

func normalizeTaskPriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "low", "medium", "high", "critical":
		return strings.ToLower(strings.TrimSpace(priority))
	default:
		return "medium"
	}
}

func normalizeStringList(values []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		normalized = append(normalized, v)
	}
	return normalized
}
