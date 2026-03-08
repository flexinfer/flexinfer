// api_agent_nudge.go implements nudge queue REST handlers and sandbox policy helpers.
//
// Handlers:
//   - POST /api/agent/nudge
//   - GET  /api/agent/nudge-queue
//   - GET  /api/agent/nudge-queue-policy
//   - POST /api/agent/nudge-queue-policy
package hud

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// handleAgentNudge creates a nudge for delivery to a target agent.
// POST /api/agent/nudge
func (a *App) handleAgentNudge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetAgentID string `json:"target_agent_id"`
		Type          string `json:"type"`    // context_inject, task_redirect, pause_request, message
		Lane          string `json:"lane"`    // optional lane override
		Content       string `json:"content"` // nudge payload
		FromAgent     string `json:"from_agent,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.TargetAgentID == "" || body.Content == "" {
		a.writeError(w, http.StatusBadRequest, "target_agent_id and content are required", nil)
		return
	}
	if body.Type == "" {
		body.Type = "message"
	}
	if body.FromAgent == "" {
		body.FromAgent = "hud"
	}

	nudgeID := NewNudgeID(body.TargetAgentID)
	entry := NudgeEntry{
		ID:        nudgeID,
		Type:      body.Type,
		Lane:      chooseNudgeLane(body.Lane, body.Type),
		Content:   body.Content,
		FromAgent: body.FromAgent,
		CreatedAt: time.Now().Format(time.RFC3339),
	}

	a.nudgeQueue.Add(body.TargetAgentID, entry)

	a.broadcastAgentEvent("agent.nudge.created", map[string]any{
		"nudge_id":        nudgeID,
		"target_agent_id": body.TargetAgentID,
		"type":            body.Type,
		"from_agent":      body.FromAgent,
	})

	a.writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"nudge_id": nudgeID,
		"status":   "pending",
	})
}

// handleAgentNudgeQueue returns nudge queue status for an agent.
// GET /api/agent/nudge-queue?agent_id=...
func (a *App) handleAgentNudgeQueue(w http.ResponseWriter, r *http.Request) {
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if agentID == "" {
		a.writeError(w, http.StatusBadRequest, "agent_id query parameter is required", nil)
		return
	}
	a.writeJSON(w, http.StatusOK, bridge.NudgeQueueStatusResponse{
		OK:     true,
		Status: toNudgeQueueStatusView(a.nudgeQueue.Status(agentID)),
	})
}

// handleAgentNudgeQueuePolicy returns current queue policy.
// GET /api/agent/nudge-queue-policy
func (a *App) handleAgentNudgeQueuePolicy(w http.ResponseWriter, _ *http.Request) {
	a.writeJSON(w, http.StatusOK, bridge.NudgeQueuePolicyResponse{
		OK:     true,
		Policy: toNudgeQueuePolicyView(a.nudgeQueue.Config()),
	})
}

// handleAgentNudgeQueuePolicyUpdate applies runtime queue policy updates.
// POST /api/agent/nudge-queue-policy
func (a *App) handleAgentNudgeQueuePolicyUpdate(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdminToken(w, r) {
		return
	}

	var body bridge.NudgeQueuePolicyMutation
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	body = body.Normalize()
	if !body.HasMutation() {
		a.writeError(w, http.StatusBadRequest, "at least one policy field is required", nil)
		return
	}
	if err := body.Validate(); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid nudge queue policy", err)
		return
	}

	update := NudgeQueuePolicyUpdate{
		DebounceMs: body.DebounceMs,
		Cap:        body.Cap,
		DropPolicy: body.DropPolicy,
	}
	if body.LanePriority != nil {
		update.LanePriority = append([]string(nil), body.LanePriority...)
	}

	before := a.nudgeQueue.Config()
	after, err := a.nudgeQueue.UpdateConfig(update)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid nudge queue policy", err)
		return
	}

	updatedBy := strings.TrimSpace(body.UpdatedBy)
	if updatedBy == "" {
		updatedBy = strings.TrimSpace(r.Header.Get("X-Agent-ID"))
	}
	if updatedBy == "" {
		updatedBy = "hud-admin"
	}

	beforeView := toNudgeQueuePolicyView(before)
	afterView := toNudgeQueuePolicyView(after)

	a.logger.Info("nudge queue policy updated",
		"updated_by", updatedBy,
		"cap_before", beforeView.Cap,
		"cap_after", afterView.Cap,
		"drop_policy_before", beforeView.DropPolicy,
		"drop_policy_after", afterView.DropPolicy,
		"debounce_ms_before", beforeView.DebounceMs,
		"debounce_ms_after", afterView.DebounceMs,
	)

	a.broadcastAgentEvent("agent.nudge.policy.updated", map[string]any{
		"updated_by": updatedBy,
		"before":     beforeView,
		"after":      afterView,
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
	})

	a.writeJSON(w, http.StatusOK, bridge.NudgeQueuePolicyResponse{
		OK:     true,
		Policy: afterView,
	})
}

type nudgeQueuePolicyView = bridge.NudgeQueuePolicy

func toNudgeQueuePolicyView(cfg NudgeQueueConfig) nudgeQueuePolicyView {
	return nudgeQueuePolicyView{
		DebounceMs:   int(cfg.Debounce / time.Millisecond),
		Cap:          cfg.Cap,
		DropPolicy:   string(cfg.DropPolicy),
		LanePriority: append([]string(nil), cfg.LanePriority...),
	}
}

func toNudgeQueueStatusView(status NudgeQueueStatus) bridge.NudgeQueueStatus {
	return bridge.NudgeQueueStatus{
		Pending:      status.Pending,
		Dropped:      status.Dropped,
		ByLane:       status.ByLane,
		DebounceMs:   status.DebounceMs,
		Cap:          status.Cap,
		DropPolicy:   string(status.DropPolicy),
		LanePriority: append([]string(nil), status.LanePriority...),
	}
}

// maybeSandboxNudge checks the cached sandbox_policy for require_sandbox patterns.
// If the agent's current task matches and no active sandbox exists, a nudge is queued.
func (a *App) maybeSandboxNudge(agentID, currentTask string) {
	cached, ok := a.cache.Get("sandbox_policy")
	if !ok {
		return
	}
	policy, ok := cached.(map[string]any)
	if !ok {
		return
	}
	if !matchesSandboxPolicy(currentTask, policy) {
		return
	}

	// Check if agent already has an active sandbox (via devbox_summary cache).
	if summary, ok := a.cache.Get("sandbox_summary"); ok {
		if m, ok := summary.(map[string]any); ok {
			if running, _ := m["running"].(float64); running > 0 {
				return // sandbox already running
			}
		}
	}

	a.nudgeQueue.Add(agentID, NudgeEntry{
		ID:        NewNudgeID(agentID),
		Type:      "context_inject",
		Lane:      "control",
		Content:   "Your current task matches sandbox policy (require_sandbox). Consider using devbox_exec instead of running commands directly on the host.",
		FromAgent: "hud",
		CreatedAt: time.Now().Format(time.RFC3339),
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

// matchesSandboxPolicy returns true if the task string contains any of the
// require_sandbox patterns from the sandbox policy.
func matchesSandboxPolicy(task string, policy map[string]any) bool {
	patterns, ok := policy["require_sandbox"]
	if !ok {
		return false
	}
	patternList, ok := patterns.([]any)
	if !ok {
		return false
	}
	taskLower := strings.ToLower(task)
	for _, p := range patternList {
		if s, ok := p.(string); ok && strings.Contains(taskLower, strings.ToLower(s)) {
			return true
		}
	}
	return false
}
