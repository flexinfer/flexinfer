// api_agent.go implements REST endpoints for the agent lifecycle.
//
// These endpoints proxy to the AgentBridge and provide a simple HTTP interface
// that CLI tools and hooks can call without needing the MCP protocol directly.
// All routes are registered under /api/agent/*.
package hud

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
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
	var body bridge.SessionStartParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.AgentID == "" {
		a.writeError(w, http.StatusBadRequest, "agent_id is required", nil)
		return
	}

	result, err := a.agent.StartSession(body)
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

	// Async sandbox auto-provision: if policy says auto_provision, detect + build sandbox.
	go a.maybeAutoProvisionSandbox(body.Namespace)

	a.writeJSON(w, http.StatusOK, result)
}

// maybeAutoProvisionSandbox checks sandbox policy and triggers devbox_detect + devbox_build
// for the project namespace. Runs asynchronously; errors are logged but not propagated.
func (a *App) maybeAutoProvisionSandbox(namespace string) {
	// Check cached sandbox policy.
	cached, ok := a.cache.Get("sandbox_policy")
	if !ok {
		return
	}
	policy, ok := cached.(map[string]any)
	if !ok {
		return
	}
	autoProvision, _ := policy["auto_provision"].(bool)
	if !autoProvision {
		return
	}

	// Extract project from namespace (format: "project/branch" or just "project").
	project := namespace
	if i := strings.Index(namespace, "/"); i > 0 {
		project = namespace[:i]
	}
	if project == "" {
		return
	}

	// Call devbox_detect to check if project has a recognizable fingerprint.
	detectResult, err := a.client.CallTool("devbox_detect", map[string]any{"project": project})
	if err != nil {
		a.logger.Debug("sandbox auto-provision: detect failed", "project", project, "error", err)
		return
	}

	// Parse detect result to check if a fingerprint exists.
	var detect map[string]any
	if err := json.Unmarshal(detectResult, &detect); err != nil {
		return
	}
	if detect["fingerprint_hash"] == nil || detect["fingerprint_hash"] == "" {
		return
	}

	// Trigger async build (non-blocking, devbox handles idempotency).
	_, err = a.client.CallTool("devbox_build", map[string]any{"project": project})
	if err != nil {
		a.logger.Debug("sandbox auto-provision: build failed", "project", project, "error", err)
		return
	}
	a.logger.Info("sandbox auto-provisioned", "project", project)
}

// handleAgentSessionEnd ends an agent session.
// POST /api/agent/session-end
func (a *App) handleAgentSessionEnd(w http.ResponseWriter, r *http.Request) {
	var body bridge.SessionEndParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.SessionID == "" && body.AgentID == "" {
		a.writeError(w, http.StatusBadRequest, "session_id or agent_id is required", nil)
		return
	}

	ended, err := a.agent.EndSession(body)
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
		Namespace   string `json:"namespace,omitempty"`
		// EnsureSession auto-bootstraps a session when heartbeat clients lack
		// dedicated session-start hooks (for example proxy-only integrations).
		EnsureSession bool `json:"ensure_session,omitempty"`

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
	if body.EnsureSession {
		namespace := strings.TrimSpace(body.Namespace)
		if err := a.ensureHeartbeatSession(body.AgentID, namespace, body.AgentType, body.Description); err != nil {
			a.logger.Warn("heartbeat ensure-session failed", "agent_id", body.AgentID, "error", err)
		}
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

			resp := map[string]any{"ok": true}
			if pending := a.nudgeQueue.Count(body.AgentID); pending > 0 {
				resp["nudge_queue"] = a.nudgeQueue.Status(body.AgentID)
			}
			if nudges := a.nudgeQueue.Drain(body.AgentID); len(nudges) > 0 {
				resp["nudges"] = nudges
			}
			a.writeJSON(w, http.StatusOK, resp)
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
				if pending := a.nudgeQueue.Count(body.AgentID); pending > 0 {
					resp["nudge_queue"] = a.nudgeQueue.Status(body.AgentID)
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
	if pending := a.nudgeQueue.Count(body.AgentID); pending > 0 {
		resp["nudge_queue"] = a.nudgeQueue.Status(body.AgentID)
	}

	// Build directives block with actionable info for the agent.
	directives := make(map[string]any)

	// Include pending handoffs and dispatched tasks.
	if handoffs, err := a.agent.HandoffListForAgent(body.AgentID); err == nil && len(handoffs) > 0 {
		directives["pending_handoffs"] = len(handoffs)
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
			directives["dispatched_tasks"] = dispatched
		}
	}

	if len(directives) > 0 {
		resp["directives"] = directives
	}

	// Sandbox policy nudge: if agent's current task matches require_sandbox patterns
	// and no active sandbox exists for the agent, queue a context_inject nudge.
	if body.CurrentTask != "" {
		a.maybeSandboxNudge(body.AgentID, body.CurrentTask)
	}

	// Include pending nudges drained from the HUD nudge queue.
	if nudges := a.nudgeQueue.Drain(body.AgentID); len(nudges) > 0 {
		resp["nudges"] = nudges
	}

	a.writeJSON(w, http.StatusOK, resp)
}

func (a *App) ensureHeartbeatSession(agentID, namespace, agentType, description string) error {
	if strings.TrimSpace(agentID) == "" {
		return nil
	}

	cacheKey := "hb-session:" + agentID + ":" + namespace
	if _, ok := a.cache.Get(cacheKey); ok {
		return nil
	}

	active, err := a.agent.GetActiveSession(agentID)
	if err != nil {
		return err
	}
	if active != nil {
		if namespace == "" || strings.TrimSpace(active.Namespace) == namespace {
			a.cache.Set(cacheKey, true, 30*time.Second)
			return nil
		}
	}

	if namespace == "" {
		namespace = "agents/" + agentID
	}
	if strings.TrimSpace(agentType) == "" {
		agentType = agentID
	}
	if strings.TrimSpace(description) == "" {
		description = "Heartbeat bootstrap session"
	}

	result, err := a.agent.StartSession(bridge.SessionStartParams{
		Namespace:   namespace,
		AgentID:     agentID,
		AgentType:   agentType,
		Description: description,
		AutoRecall:  false,
	})
	if err != nil {
		return err
	}

	a.cache.Set(cacheKey, true, 30*time.Second)
	if result != nil {
		a.broadcastAgentEvent("agent.session.bootstrap", map[string]any{
			"agent_id":   agentID,
			"agent_type": agentType,
			"session_id": result.SessionID,
			"namespace":  namespace,
		})
	}

	return nil
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
	var body bridge.UpdateTaskParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.ID == "" {
		a.writeError(w, http.StatusBadRequest, "task_id is required", nil)
		return
	}

	if err := a.agent.UpdateTask(body); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to update task", err)
		return
	}

	a.broadcastAgentEvent("agent.task.update", map[string]any{
		"task_id":    body.ID,
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

// handleAgentContextInspect returns a context budget breakdown for an agent/session.
// GET /api/agent/context-inspect?agent_id=...&session_id=...&detail=true&limit=200
func (a *App) handleAgentContextInspect(w http.ResponseWriter, r *http.Request) {
	req, err := bridge.ParseContextInspectRequest(r.URL.Query())
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	result, err := a.agent.ContextInspect(req.AgentID, req.SessionID, req.Detail, req.Limit)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "context inspect failed", err)
		return
	}
	a.writeJSON(w, http.StatusOK, result)
}

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

func (a *App) requireAdminToken(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(a.config.AdminToken)
	if expected == "" {
		a.writeError(w, http.StatusForbidden, "admin token is not configured; set HUD_ADMIN_TOKEN", nil)
		return false
	}
	actual := extractAdminToken(r)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
		a.writeError(w, http.StatusUnauthorized, "invalid admin token", nil)
		return false
	}
	return true
}

func extractAdminToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	if token := strings.TrimSpace(r.Header.Get("X-Admin-Token")); token != "" {
		return token
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
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

// handleKnowledge performs a cross-agent knowledge search.
// GET /api/knowledge?query=...&category=...&budget=...
func (a *App) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("query")
	if query == "" {
		query = "recent decisions and findings"
	}
	category := r.URL.Query().Get("category")
	budget := 8000
	if b := r.URL.Query().Get("budget"); b != "" {
		if parsed, err := strconv.Atoi(b); err == nil && parsed > 0 {
			budget = parsed
		}
	}

	result, err := a.agent.KnowledgeRecall(query, category, budget)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "knowledge recall failed", err)
		return
	}

	// Group entries by category for the frontend.
	grouped := make(map[string][]bridge.KnowledgeEntry)
	for _, e := range result.Entries {
		cat := e.EntryType
		if cat == "" {
			cat = "note"
		}
		// Filter by category if specified.
		if category != "" && cat != category {
			continue
		}
		grouped[cat] = append(grouped[cat], e)
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"entries":      result.Entries,
		"grouped":      grouped,
		"count":        result.Count,
		"total_tokens": result.TotalTokens,
		"token_budget": result.TokenBudget,
	})
}
