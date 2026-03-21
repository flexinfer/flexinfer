package fleet

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// handleAgentNudge creates a nudge for delivery to a target agent.
func (d *FleetDomain) handleAgentNudge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetAgentID string `json:"target_agent_id"`
		Type          string `json:"type"`
		Lane          string `json:"lane"`
		Content       string `json:"content"`
		FromAgent     string `json:"from_agent,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.TargetAgentID == "" || body.Content == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "target_agent_id and content are required", nil)
		return
	}
	if body.Type == "" {
		body.Type = "message"
	}
	if body.FromAgent == "" {
		body.FromAgent = "hud"
	}

	lane := chooseNudgeLane(body.Lane, body.Type)
	nudgeID := d.deps.NudgeQueue().QueueNudge(body.TargetAgentID, body.Type, lane, body.Content, body.FromAgent)

	d.deps.BroadcastAgentEvent("agent.nudge.created", map[string]any{
		"nudge_id":        nudgeID,
		"target_agent_id": body.TargetAgentID,
		"type":            body.Type,
		"from_agent":      body.FromAgent,
	})

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"nudge_id": nudgeID,
		"status":   "pending",
	})
}

// handleAgentNudgeQueue returns nudge queue status for an agent.
func (d *FleetDomain) handleAgentNudgeQueue(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if agentID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "agent_id query parameter is required", nil)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, bridge.NudgeQueueStatusResponse{
		OK:     true,
		Status: d.deps.NudgeQueue().StatusView(agentID),
	})
}

// handleAgentNudgeQueuePolicy returns current queue policy.
func (d *FleetDomain) handleAgentNudgeQueuePolicy(w http.ResponseWriter, _ *http.Request) {
	d.deps.WriteJSON(w, http.StatusOK, bridge.NudgeQueuePolicyResponse{
		OK:     true,
		Policy: d.deps.NudgeQueue().PolicyView(),
	})
}

// handleAgentNudgeQueuePolicyUpdate applies runtime queue policy updates.
func (d *FleetDomain) handleAgentNudgeQueuePolicyUpdate(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	var body bridge.NudgeQueuePolicyMutation
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	body = body.Normalize()
	if !body.HasMutation() {
		d.deps.WriteError(w, http.StatusBadRequest, "at least one policy field is required", nil)
		return
	}
	if err := body.Validate(); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid nudge queue policy", err)
		return
	}

	before, after, err := d.deps.NudgeQueue().ApplyPolicy(body)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid nudge queue policy", err)
		return
	}

	updatedBy := strings.TrimSpace(body.UpdatedBy)
	if updatedBy == "" {
		updatedBy = strings.TrimSpace(r.Header.Get("X-Agent-ID"))
	}
	if updatedBy == "" {
		updatedBy = "hud-admin"
	}

	d.deps.Logger().Info("nudge queue policy updated",
		"updated_by", updatedBy,
		"cap_before", before.Cap,
		"cap_after", after.Cap,
		"drop_policy_before", before.DropPolicy,
		"drop_policy_after", after.DropPolicy,
		"debounce_ms_before", before.DebounceMs,
		"debounce_ms_after", after.DebounceMs,
	)

	d.deps.BroadcastAgentEvent("agent.nudge.policy.updated", map[string]any{
		"updated_by": updatedBy,
		"before":     before,
		"after":      after,
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
	})

	d.deps.WriteJSON(w, http.StatusOK, bridge.NudgeQueuePolicyResponse{
		OK:     true,
		Policy: after,
	})
}

func chooseNudgeLane(lane, nudgeType string) string {
	lane = strings.TrimSpace(strings.ToLower(lane))
	switch lane {
	case "control", "handoff", "advice", "default":
		return lane
	}

	switch strings.ToLower(strings.TrimSpace(nudgeType)) {
	case "context_inject", "pause_request":
		return "control"
	case "task_redirect":
		return "handoff"
	case "message":
		return "advice"
	default:
		return "default"
	}
}
