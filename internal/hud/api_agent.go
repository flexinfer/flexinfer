// api_agent.go implements REST endpoints for the agent lifecycle.
//
// These endpoints proxy to the AgentBridge and provide a simple HTTP interface
// that CLI tools and hooks can call without needing the MCP protocol directly.
// All routes are registered under /api/agent/*.
package hud

import (
	"encoding/json"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// --- Agent lifecycle API handlers ---

// handleAgentSessionStart creates a new agent session (idempotent).
// POST /api/agent/session-start
func (a *App) handleAgentSessionStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Namespace   string `json:"namespace"`
		AgentID     string `json:"agent_id"`
		AgentType   string `json:"agent_type"`
		Description string `json:"description"`
		AutoRecall  bool   `json:"auto_recall"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.AgentID == "" {
		a.writeError(w, http.StatusBadRequest, "agent_id is required", nil)
		return
	}

	result, err := a.agent.StartSession(bridge.SessionStartParams{
		Namespace:   body.Namespace,
		AgentID:     body.AgentID,
		AgentType:   body.AgentType,
		Description: body.Description,
		AutoRecall:  body.AutoRecall,
	})
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to start session", err)
		return
	}

	// Trigger a fleet refresh so the HUD picks up the new session immediately.
	a.fleetMonitor.Refresh()

	a.writeJSON(w, http.StatusOK, result)
}

// handleAgentSessionEnd ends an agent session.
// POST /api/agent/session-end
func (a *App) handleAgentSessionEnd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"session_id"`
		AgentID   string `json:"agent_id"`
		Summarize bool   `json:"summarize"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.SessionID == "" && body.AgentID == "" {
		a.writeError(w, http.StatusBadRequest, "session_id or agent_id is required", nil)
		return
	}

	if err := a.agent.EndSession(bridge.SessionEndParams{
		SessionID: body.SessionID,
		AgentID:   body.AgentID,
		Summarize: body.Summarize,
	}); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to end session", err)
		return
	}

	a.fleetMonitor.Refresh()

	a.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAgentHeartbeat updates an agent's presence heartbeat.
// POST /api/agent/heartbeat
func (a *App) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AgentID   string `json:"agent_id"`
		SessionID string `json:"session_id,omitempty"`
		Status    string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.AgentID == "" {
		a.writeError(w, http.StatusBadRequest, "agent_id is required", nil)
		return
	}

	if err := a.agent.PresenceHeartbeat(body.AgentID, body.Status); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to send heartbeat", err)
		return
	}

	a.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAgentTaskUpdate updates a task's status via the lifecycle API.
// POST /api/agent/task-update
func (a *App) handleAgentTaskUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskID     string `json:"task_id"`
		Status     string `json:"status"`
		Resolution string `json:"resolution,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.TaskID == "" {
		a.writeError(w, http.StatusBadRequest, "task_id is required", nil)
		return
	}

	if err := a.agent.UpdateTask(bridge.UpdateTaskParams{
		ID:         body.TaskID,
		Status:     body.Status,
		Resolution: body.Resolution,
	}); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to update task", err)
		return
	}

	a.fleetMonitor.Refresh()

	a.writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleAgentSession returns the active session for an agent.
// GET /api/agent/session?agent_id=...
func (a *App) handleAgentSession(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		a.writeError(w, http.StatusBadRequest, "agent_id query param is required", nil)
		return
	}

	session, err := a.agent.GetActiveSession(agentID)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to get session", err)
		return
	}
	if session == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"session": nil})
		return
	}

	a.writeJSON(w, http.StatusOK, map[string]any{"session": session})
}
