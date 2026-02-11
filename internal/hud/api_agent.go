// api_agent.go implements REST endpoints for the agent lifecycle.
//
// These endpoints proxy to the AgentBridge and provide a simple HTTP interface
// that CLI tools and hooks can call without needing the MCP protocol directly.
// All routes are registered under /api/agent/*.
package hud

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// mustMarshal marshals v to JSON, returning nil on error.
func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

// broadcastAgentEvent sends a granular agent SSE event to all browser clients.
func (a *App) broadcastAgentEvent(eventType string, payload any) {
	if a.sseHub == nil {
		return
	}
	data := mustMarshal(payload)
	if data == nil {
		return
	}
	a.sseHub.Broadcast(bridge.SSEEvent{
		ID:        fmt.Sprintf("%s-%d", eventType, time.Now().UnixMilli()),
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      data,
	})
}

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

	// Broadcast a granular SSE event immediately so the overlay reacts in <100ms.
	a.broadcastAgentEvent("agent.session.start", map[string]any{
		"session_id": result.SessionID,
		"agent_id":   body.AgentID,
		"namespace":  body.Namespace,
	})

	// Async fleet refresh for full snapshot consistency (non-blocking).
	go a.fleetMonitor.Refresh()

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

	ended, err := a.agent.EndSession(bridge.SessionEndParams{
		SessionID: body.SessionID,
		AgentID:   body.AgentID,
		Summarize: body.Summarize,
	})
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to end session", err)
		return
	}

	if ended {
		a.broadcastAgentEvent("agent.session.end", map[string]any{
			"session_id": body.SessionID,
			"agent_id":   body.AgentID,
		})

		go a.fleetMonitor.Refresh()

		// Trigger async LLM summarization if coordinator is available.
		if a.coordinator != nil {
			go a.coordinator.OnSessionEnd(body.SessionID, body.AgentID)
		}
	} else {
		a.logger.Info("session end: no active session found, skipping",
			"agent_id", body.AgentID, "session_id", body.SessionID)
	}

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

	// Coalesce rapid heartbeats: skip the MCP round-trip if we saw one recently.
	cacheKey := "hb:" + body.AgentID
	if _, ok := a.cache.Get(cacheKey); ok {
		// Cache hit — broadcast SSE for overlay responsiveness, skip MCP call.
		a.broadcastAgentEvent("agent.heartbeat", map[string]any{
			"agent_id":  body.AgentID,
			"status":    body.Status,
			"timestamp": time.Now().Format(time.RFC3339),
		})
		a.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	if err := a.agent.PresenceHeartbeat(body.AgentID, body.Status); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to send heartbeat", err)
		return
	}

	a.cache.Set(cacheKey, true, 10*time.Second)

	a.broadcastAgentEvent("agent.heartbeat", map[string]any{
		"agent_id":  body.AgentID,
		"status":    body.Status,
		"timestamp": time.Now().Format(time.RFC3339),
	})

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

	a.broadcastAgentEvent("agent.task.update", map[string]any{
		"task_id":    body.TaskID,
		"status":     body.Status,
		"resolution": body.Resolution,
	})

	go a.fleetMonitor.Refresh()

	a.writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleAgentWorkflowDefine registers a workflow definition.
// POST /api/agent/workflow-define
func (a *App) handleAgentWorkflowDefine(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body["name"] == nil || body["steps"] == nil {
		a.writeError(w, http.StatusBadRequest, "name and steps are required", nil)
		return
	}

	result, err := a.agent.WorkflowDefine(body)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to define workflow", err)
		return
	}

	a.writeJSON(w, http.StatusOK, result)
}

// handleAgentWorkflowDefinitions lists registered workflow definitions.
// GET /api/agent/workflow-definitions?namespace=...
func (a *App) handleAgentWorkflowDefinitions(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")

	defs, err := a.agent.WorkflowDefinitions(namespace)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list workflow definitions", err)
		return
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"definitions": defs,
		"count":       len(defs),
	})
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

// handleAgentContextAdd proxies context entries to agent-context and broadcasts
// SSE events for devbox-titled entries so the sandbox panel shows live activity.
// POST /api/agent/context/add
func (a *App) handleAgentContextAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string           `json:"session_id,omitempty"`
		Entries   []map[string]any `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if len(body.Entries) == 0 {
		a.writeError(w, http.StatusBadRequest, "entries array is required", nil)
		return
	}

	// Forward to agent-context MCP server.
	if err := a.agent.ContextAdd(body.SessionID, body.Entries); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to add context entries", err)
		return
	}

	// Detect devbox events and broadcast SSE for the sandbox panel.
	for _, entry := range body.Entries {
		title, _ := entry["title"].(string)
		if len(title) > 7 && title[:7] == "devbox." {
			// Parse "devbox.<type>: <project>" format.
			rest := title[7:]
			eventType := rest
			project := ""
			if idx := strings.Index(rest, ": "); idx >= 0 {
				eventType = rest[:idx]
				project = rest[idx+2:]
			}
			content, _ := entry["content"].(string)
			a.broadcastAgentEvent("hud.sandbox.event", map[string]any{
				"type":      eventType,
				"project":   project,
				"detail":    content,
				"timestamp": time.Now().Format(time.RFC3339),
			})
		}
	}

	a.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
