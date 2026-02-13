// api_agent.go implements REST endpoints for the agent lifecycle.
//
// These endpoints proxy to the AgentBridge and provide a simple HTTP interface
// that CLI tools and hooks can call without needing the MCP protocol directly.
// All routes are registered under /api/agent/*.
package hud

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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

// broadcastAgentEvent sends a granular agent SSE event to all browser clients
// and appends it to the timeline event log.
func (a *App) broadcastAgentEvent(eventType string, payload any) {
	data := mustMarshal(payload)
	if data == nil {
		return
	}

	now := time.Now()

	// Append to timeline event log for the unified activity feed.
	if a.eventLog != nil {
		agentID, _ := extractStringField(payload, "agent_id")
		agentType, _ := extractStringField(payload, "agent_type")
		a.eventLog.Append(TimelineEntry{
			Timestamp: now,
			EventType: eventType,
			AgentID:   agentID,
			AgentType: agentType,
			Data:      data,
		})
	}

	// Broadcast via SSE.
	if a.sseHub == nil {
		return
	}
	a.sseHub.Broadcast(bridge.SSEEvent{
		ID:        fmt.Sprintf("%s-%d", eventType, now.UnixMilli()),
		Type:      eventType,
		Timestamp: now,
		Data:      data,
	})
}

// extractStringField safely extracts a string field from a payload (map or struct).
func extractStringField(payload any, key string) (string, bool) {
	if m, ok := payload.(map[string]any); ok {
		if v, ok := m[key].(string); ok {
			return v, true
		}
	}
	return "", false
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
		"agent_type": body.AgentType,
		"namespace":  body.Namespace,
	})

	// Increment daily KPI counter.
	if !result.AlreadyExisted {
		a.fleetMonitor.IncrementKPI("sessions", 1)
	}

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
		AgentID     string `json:"agent_id"`
		SessionID   string `json:"session_id,omitempty"`
		Status      string `json:"status,omitempty"`
		AgentType   string `json:"agent_type,omitempty"`
		Description string `json:"description,omitempty"`

		ActiveFiles []string `json:"active_files,omitempty"`
		CurrentTask string   `json:"current_task,omitempty"`
		Branch      string   `json:"branch,omitempty"`

		HeartbeatTTLSeconds int `json:"heartbeat_ttl_seconds,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.AgentID == "" {
		a.writeError(w, http.StatusBadRequest, "agent_id is required", nil)
		return
	}

	// Coalesce rapid heartbeats: skip the MCP round-trip if we saw an identical
	// payload recently.
	cacheKey := "hb:" + body.AgentID
	fp := heartbeatFingerprint(body.Status, body.CurrentTask, body.Branch, body.ActiveFiles)
	if prev, ok := a.cache.Get(cacheKey); ok {
		if prevFP, ok := prev.(string); ok && prevFP == fp {
			// Cache hit — broadcast SSE for overlay responsiveness, skip MCP call.
			a.broadcastAgentEvent("agent.heartbeat", map[string]any{
				"agent_id":     body.AgentID,
				"status":       body.Status,
				"current_task": body.CurrentTask,
				"active_files": body.ActiveFiles,
				"branch":       body.Branch,
				"timestamp":    time.Now().Format(time.RFC3339),
			})
			a.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
			return
		}
	}

	result, err := a.agent.PresenceHeartbeat(body.AgentID, bridge.PresenceHeartbeatParams{
		Status:      body.Status,
		ActiveFiles: body.ActiveFiles,
		CurrentTask: body.CurrentTask,
		Branch:      body.Branch,
	})
	if err != nil {
		// Some clients send heartbeats without ever calling session-start or
		// presence-register. Make this endpoint resilient by self-registering
		// presence once, then retrying the heartbeat.
		if isPresenceNotRegisteredErr(err) {
			_ = a.agent.PresenceRegister(body.AgentID, body.SessionID, body.AgentType, body.Description, body.HeartbeatTTLSeconds)
			result, err = a.agent.PresenceHeartbeat(body.AgentID, bridge.PresenceHeartbeatParams{
				Status:      body.Status,
				ActiveFiles: body.ActiveFiles,
				CurrentTask: body.CurrentTask,
				Branch:      body.Branch,
			})
			if err == nil {
				a.cache.Set(cacheKey, fp, 10*time.Second)
				a.broadcastAgentEvent("agent.heartbeat", map[string]any{
					"agent_id":     body.AgentID,
					"status":       body.Status,
					"current_task": body.CurrentTask,
					"active_files": body.ActiveFiles,
					"branch":       body.Branch,
					"timestamp":    time.Now().Format(time.RFC3339),
				})

				// Update monitor-backed snapshots promptly so /api/presence reflects
				// the heartbeat without waiting for the next polling interval.
				go a.fleetMonitor.Refresh()

				resp := map[string]any{"ok": true}
				if result != nil && result.HasConflicts {
					resp["has_conflicts"] = true
					resp["conflicts"] = result.Conflicts
				}
				a.writeJSON(w, http.StatusOK, resp)
				return
			}
		}

		a.writeError(w, http.StatusBadGateway, "failed to send heartbeat", err)
		return
	}

	a.cache.Set(cacheKey, fp, 10*time.Second)

	a.broadcastAgentEvent("agent.heartbeat", map[string]any{
		"agent_id":     body.AgentID,
		"status":       body.Status,
		"current_task": body.CurrentTask,
		"active_files": body.ActiveFiles,
		"branch":       body.Branch,
		"timestamp":    time.Now().Format(time.RFC3339),
	})

	// Update KPI counters.
	a.fleetMonitor.IncrementKPI("sessions", 0) // ensure day reset

	// Update monitor-backed snapshots promptly so /api/presence reflects
	// the heartbeat without waiting for the next polling interval.
	go a.fleetMonitor.Refresh()

	resp := map[string]any{"ok": true}
	if result != nil && result.HasConflicts {
		resp["has_conflicts"] = true
		resp["conflicts"] = result.Conflicts
	}

	// Heartbeat enrichment: include pending handoffs and dispatched tasks.
	if handoffs, err := a.agent.HandoffListForAgent(body.AgentID); err == nil && len(handoffs) > 0 {
		resp["pending_handoffs"] = len(handoffs)
		dispatched := make([]map[string]any, 0)
		for _, h := range handoffs {
			if strings.HasPrefix(h.Summary, "[Dispatched] ") {
				dispatched = append(dispatched, map[string]any{
					"id":    h.ID,
					"title": strings.TrimPrefix(h.Summary, "[Dispatched] "),
				})
			}
		}
		if len(dispatched) > 0 {
			resp["dispatched_tasks"] = dispatched
		}
	}

	a.writeJSON(w, http.StatusOK, resp)
}

func heartbeatFingerprint(status, currentTask, branch string, activeFiles []string) string {
	afs := append([]string(nil), activeFiles...)
	sort.Strings(afs)

	h := sha256.New()
	// Field separators so concatenations can't collide.
	h.Write([]byte(status))
	h.Write([]byte{0})
	h.Write([]byte(currentTask))
	h.Write([]byte{0})
	h.Write([]byte(branch))
	h.Write([]byte{0})
	for _, f := range afs {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil))
}

func isPresenceNotRegisteredErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "not registered") &&
		strings.Contains(msg, "agent_presence_register")
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

	// Increment daily KPI counter for completed tasks.
	if body.Status == "completed" {
		a.fleetMonitor.IncrementKPI("tasks_completed", 1)
	}

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
