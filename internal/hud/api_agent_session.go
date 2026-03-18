// api_agent_session.go implements session lifecycle and heartbeat REST handlers.
//
// Handlers:
//   - POST /api/agent/session-start
//   - POST /api/agent/session-end
//   - GET  /api/agent/session
//   - POST /api/agent/session-list
//   - POST /api/agent/session-prune
//   - POST /api/agent/heartbeat
package hud

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

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
	detect, err := bridge.ParseToolResultMap(detectResult)
	if err != nil {
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
		a.logger.Debug("session end: no active session found, skipping",
			"agent_id", body.AgentID, "session_id", body.SessionID)
	}

	a.writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAgentHeartbeat updates an agent's presence heartbeat.
// POST /api/agent/heartbeat
func (a *App) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	var body bridge.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.AgentID == "" {
		a.writeError(w, http.StatusBadRequest, "agent_id is required", nil)
		return
	}
	var ensureSessionErr error
	if body.EnsureSession {
		namespace := strings.TrimSpace(body.Namespace)
		if err := a.ensureHeartbeatSession(body.AgentID, namespace, body.AgentType, body.Description); err != nil {
			ensureSessionErr = err
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

	result, err := a.agent.PresenceHeartbeat(body.AgentID, body.HeartbeatParams())
	if err != nil {
		// Some clients send heartbeats without ever calling session-start or
		// presence-register. Make this endpoint resilient by self-registering
		// presence once, then retrying the heartbeat.
		if isPresenceNotRegisteredErr(err) {
			if body.EnsureSession {
				if ensureSessionErr == nil {
					namespace := strings.TrimSpace(body.Namespace)
					ensureSessionErr = a.ensureHeartbeatSession(body.AgentID, namespace, body.AgentType, body.Description)
				}
				if ensureSessionErr != nil {
					a.writeError(w, http.StatusBadGateway, "failed to bootstrap session for heartbeat", ensureSessionErr)
					return
				}

				result, err = a.agent.PresenceHeartbeat(body.AgentID, body.HeartbeatParams())
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

				if isPresenceNotRegisteredErr(err) {
					a.writeError(w, http.StatusBadGateway, "failed to bootstrap session for heartbeat", err)
					return
				}
			}

			_ = a.agent.PresenceRegister(body.AgentID, body.SessionID, body.AgentType, body.Description, body.HeartbeatTTLSeconds)
			result, err = a.agent.PresenceHeartbeat(body.AgentID, body.HeartbeatParams())
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

// handleAgentSessionList lists sessions with optional filters.
// POST /api/agent/session-list
func (a *App) handleAgentSessionList(w http.ResponseWriter, r *http.Request) {
	var params map[string]any
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	result, err := a.agent.ListSessions(params)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list sessions", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(result)
}

// handleAgentSessionPrune prunes stale sessions.
// POST /api/agent/session-prune
func (a *App) handleAgentSessionPrune(w http.ResponseWriter, r *http.Request) {
	var params map[string]any
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	result, err := a.agent.PruneSessions(params)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to prune sessions", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(result)
}
