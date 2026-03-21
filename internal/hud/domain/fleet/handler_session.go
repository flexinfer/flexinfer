package fleet

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
func (d *FleetDomain) handleAgentSessionStart(w http.ResponseWriter, r *http.Request) {
	var body bridge.SessionStartParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.AgentID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "agent_id is required", nil)
		return
	}

	result, err := d.deps.Agent().StartSession(body)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to start session", err)
		return
	}

	if !result.AlreadyExisted {
		d.deps.BroadcastAgentEvent("agent.session.start", map[string]any{
			"session_id": result.SessionID,
			"agent_id":   body.AgentID,
			"agent_type": body.AgentType,
			"namespace":  body.Namespace,
		})
		d.deps.FleetIncrementKPI("sessions", 1)
		go d.deps.FleetRefresh()
		go d.deps.MaybeAutoProvisionSandbox(body.Namespace)
	}

	d.deps.WriteJSON(w, http.StatusOK, result)
}

// handleAgentSessionEnd ends an agent session.
func (d *FleetDomain) handleAgentSessionEnd(w http.ResponseWriter, r *http.Request) {
	var body bridge.SessionEndParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.SessionID == "" && body.AgentID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "session_id or agent_id is required", nil)
		return
	}

	ended, err := d.deps.Agent().EndSession(body)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to end session", err)
		return
	}

	if ended {
		d.deps.BroadcastAgentEvent("agent.session.end", map[string]any{
			"session_id": body.SessionID,
			"agent_id":   body.AgentID,
		})
		go d.deps.FleetRefresh()
		d.deps.OnSessionEnd(body.SessionID, body.AgentID)
	} else {
		d.deps.Logger().Debug("session end: no active session found, skipping",
			"agent_id", body.AgentID, "session_id", body.SessionID)
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAgentHeartbeat updates an agent's presence heartbeat.
func (d *FleetDomain) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	var body bridge.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.AgentID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "agent_id is required", nil)
		return
	}
	var ensureSessionErr error
	if body.EnsureSession {
		namespace := strings.TrimSpace(body.Namespace)
		if err := d.ensureHeartbeatSession(body.AgentID, namespace, body.AgentType, body.Description); err != nil {
			ensureSessionErr = err
			d.deps.Logger().Warn("heartbeat ensure-session failed", "agent_id", body.AgentID, "error", err)
		}
	}

	// Coalesce rapid heartbeats: skip the MCP round-trip if we saw an identical
	// payload recently.
	cacheKey := "hb:" + body.AgentID
	fp := heartbeatFingerprint(body.Status, body.CurrentTask, body.Branch, body.ActiveFiles)
	if prev, ok := d.deps.CacheGet(cacheKey); ok {
		if prevFP, ok := prev.(string); ok && prevFP == fp {
			d.broadcastHeartbeat(body)

			resp := map[string]any{"ok": true}
			if pending := d.deps.NudgeQueue().Count(body.AgentID); pending > 0 {
				resp["nudge_queue"] = d.deps.NudgeQueue().StatusView(body.AgentID)
			}
			if nudges := d.deps.NudgeQueue().Drain(body.AgentID); len(nudges) > 0 {
				resp["nudges"] = nudges
			}
			d.deps.WriteJSON(w, http.StatusOK, resp)
			return
		}
	}

	result, err := d.deps.Agent().PresenceHeartbeat(body.AgentID, body.HeartbeatParams())
	if err != nil {
		if isPresenceNotRegisteredErr(err) {
			if body.EnsureSession {
				if ensureSessionErr == nil {
					namespace := strings.TrimSpace(body.Namespace)
					ensureSessionErr = d.ensureHeartbeatSession(body.AgentID, namespace, body.AgentType, body.Description)
				}
				if ensureSessionErr != nil {
					d.deps.WriteError(w, http.StatusBadGateway, "failed to bootstrap session for heartbeat", ensureSessionErr)
					return
				}

				result, err = d.deps.Agent().PresenceHeartbeat(body.AgentID, body.HeartbeatParams())
				if err == nil {
					d.deps.CacheSet(cacheKey, fp, 10*time.Second)
					d.broadcastHeartbeat(body)
					go d.deps.FleetRefresh()

					resp := map[string]any{"ok": true}
					if result != nil && result.HasConflicts {
						resp["has_conflicts"] = true
						resp["conflicts"] = result.Conflicts
					}
					if pending := d.deps.NudgeQueue().Count(body.AgentID); pending > 0 {
						resp["nudge_queue"] = d.deps.NudgeQueue().StatusView(body.AgentID)
					}
					d.deps.WriteJSON(w, http.StatusOK, resp)
					return
				}

				if isPresenceNotRegisteredErr(err) {
					d.deps.WriteError(w, http.StatusBadGateway, "failed to bootstrap session for heartbeat", err)
					return
				}
			}

			_ = d.deps.Agent().PresenceRegister(body.AgentID, body.SessionID, body.AgentType, body.Description, body.HeartbeatTTLSeconds)
			result, err = d.deps.Agent().PresenceHeartbeat(body.AgentID, body.HeartbeatParams())
			if err == nil {
				d.deps.CacheSet(cacheKey, fp, 10*time.Second)
				d.broadcastHeartbeat(body)
				go d.deps.FleetRefresh()

				resp := map[string]any{"ok": true}
				if result != nil && result.HasConflicts {
					resp["has_conflicts"] = true
					resp["conflicts"] = result.Conflicts
				}
				if pending := d.deps.NudgeQueue().Count(body.AgentID); pending > 0 {
					resp["nudge_queue"] = d.deps.NudgeQueue().StatusView(body.AgentID)
				}
				d.deps.WriteJSON(w, http.StatusOK, resp)
				return
			}
		}

		d.deps.WriteError(w, http.StatusBadGateway, "failed to send heartbeat", err)
		return
	}

	d.deps.CacheSet(cacheKey, fp, 10*time.Second)
	d.broadcastHeartbeat(body)
	d.deps.FleetIncrementKPI("sessions", 0) // ensure day reset
	go d.deps.FleetRefresh()

	resp := map[string]any{"ok": true}
	if result != nil && result.HasConflicts {
		resp["has_conflicts"] = true
		resp["conflicts"] = result.Conflicts
	}
	if pending := d.deps.NudgeQueue().Count(body.AgentID); pending > 0 {
		resp["nudge_queue"] = d.deps.NudgeQueue().StatusView(body.AgentID)
	}

	// Build directives block with actionable info for the agent.
	directives := make(map[string]any)
	if handoffs, err := d.deps.Agent().HandoffListForAgent(body.AgentID); err == nil && len(handoffs) > 0 {
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

	// Sandbox policy nudge.
	if body.CurrentTask != "" {
		d.maybeSandboxNudge(body.AgentID, body.CurrentTask)
	}

	if nudges := d.deps.NudgeQueue().Drain(body.AgentID); len(nudges) > 0 {
		resp["nudges"] = nudges
	}

	d.deps.WriteJSON(w, http.StatusOK, resp)
}

// handleAgentSession returns the active session for an agent.
func (d *FleetDomain) handleAgentSession(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "agent_id query param is required", nil)
		return
	}

	session, err := d.deps.Agent().GetActiveSession(agentID)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to get session", err)
		return
	}
	if session == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"session": nil})
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"session": session})
}

// handleAgentSessionList lists sessions with optional filters.
func (d *FleetDomain) handleAgentSessionList(w http.ResponseWriter, r *http.Request) {
	var params map[string]any
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	result, err := d.deps.Agent().ListSessions(params)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to list sessions", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(result)
}

// handleAgentSessionPrune prunes stale sessions.
func (d *FleetDomain) handleAgentSessionPrune(w http.ResponseWriter, r *http.Request) {
	var params map[string]any
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	result, err := d.deps.Agent().PruneSessions(params)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to prune sessions", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(result)
}

// handleAgentSessionDetail serves the rich session detail.
func (d *FleetDomain) handleAgentSessionDetail(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if sessionID == "" && agentID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "session_id or agent_id query parameter required", nil)
		return
	}

	sessions, err := d.deps.Agent().Sessions()
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to list sessions", err)
		return
	}

	var found *bridge.SessionInfo
	for i := range sessions {
		s := &sessions[i]
		if sessionID != "" && strings.TrimSpace(s.ID) == sessionID {
			found = s
			break
		}
		if agentID != "" && strings.TrimSpace(s.AgentID) == agentID && s.Status == "active" {
			found = s
			break
		}
	}
	if found == nil {
		d.deps.WriteError(w, http.StatusNotFound, "session not found", nil)
		return
	}

	result := map[string]any{"session": found}

	inspect, err := d.deps.Agent().ContextInspect(found.AgentID, found.ID, true, 200)
	if err == nil && inspect != nil {
		result["entry_breakdown"] = inspect.ByEntryType
		result["top_entries"] = inspect.TopEntries
		result["tasks"] = inspect.Tasks

		var decisions, errors []bridge.ContextInspectTopEntry
		for _, e := range inspect.TopEntries {
			switch e.EntryType {
			case "decision":
				decisions = append(decisions, e)
			case "error":
				errors = append(errors, e)
			}
		}
		result["decisions"] = decisions
		result["errors"] = errors
	}

	d.deps.WriteJSON(w, http.StatusOK, result)
}

// --- Helpers ---

func (d *FleetDomain) ensureHeartbeatSession(agentID, namespace, agentType, description string) error {
	if strings.TrimSpace(agentID) == "" {
		return nil
	}

	cacheKey := "hb-session:" + agentID + ":" + namespace
	if _, ok := d.deps.CacheGet(cacheKey); ok {
		return nil
	}

	active, err := d.deps.Agent().GetActiveSession(agentID)
	if err != nil {
		return err
	}
	if active != nil {
		if namespace == "" || strings.TrimSpace(active.Namespace) == namespace {
			d.deps.CacheSet(cacheKey, true, 30*time.Second)
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

	result, err := d.deps.Agent().StartSession(bridge.SessionStartParams{
		Namespace:   namespace,
		AgentID:     agentID,
		AgentType:   agentType,
		Description: description,
		AutoRecall:  false,
	})
	if err != nil {
		return err
	}

	d.deps.CacheSet(cacheKey, true, 30*time.Second)
	if result != nil {
		d.deps.BroadcastAgentEvent("agent.session.bootstrap", map[string]any{
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

func (d *FleetDomain) broadcastHeartbeat(body bridge.HeartbeatRequest) {
	d.deps.BroadcastAgentEvent("agent.heartbeat", map[string]any{
		"agent_id":     body.AgentID,
		"status":       body.Status,
		"current_task": body.CurrentTask,
		"active_files": body.ActiveFiles,
		"branch":       body.Branch,
		"timestamp":    time.Now().Format(time.RFC3339),
	})
}

// maybeSandboxNudge checks the cached sandbox_policy for require_sandbox patterns.
// If the agent's current task matches and no active sandbox exists, a nudge is queued.
func (d *FleetDomain) maybeSandboxNudge(agentID, currentTask string) {
	cached, ok := d.deps.CacheGet("sandbox_policy")
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

	if summary, ok := d.deps.CacheGet("sandbox_summary"); ok {
		if m, ok := summary.(map[string]any); ok {
			if running, _ := m["running"].(float64); running > 0 {
				return
			}
		}
	}

	d.deps.NudgeQueue().QueueNudge(agentID, "context_inject", "control",
		"Your current task matches sandbox policy (require_sandbox). Consider using devbox_exec instead of running commands directly on the host.",
		"hud")
}

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
