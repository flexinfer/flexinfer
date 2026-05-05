package agentcontext

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// Start creates a new session or resumes an existing one.
func (ss *SessionSvc) Start(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := strings.TrimSpace(v.String("agent_id", ss.cfg.DefaultAgentID))
	namespace := strings.TrimSpace(v.String("namespace", ss.cfg.DefaultNamespace))
	project := strings.TrimSpace(v.String("project", ""))
	description := strings.TrimSpace(v.String("description", ""))
	workingDir := strings.TrimSpace(v.String("working_dir", ""))
	resumeID := strings.TrimSpace(v.String("resume_session_id", ""))
	parentSessionID := strings.TrimSpace(v.String("parent_session_id", ""))
	pipelineRef := pipelineRefFromLegacyArgs(args)
	project = canonicalProject(project, namespace, pipelineRef)

	if agentID == "" {
		return mcp.ErrorResult(fmt.Errorf("agent_id is required")), nil
	}

	startUnlock := ss.lockAgentStart(agentID)
	defer startUnlock()

	// Check for resume
	if resumeID != "" {
		existing, err := ss.Get(ctx, resumeID)
		if err != nil || existing == nil {
			return mcp.ErrorResult(fmt.Errorf("session %s not found or cannot be resumed", resumeID)), nil
		}
		if existing.Status != string(SessionStatusActive) {
			existing.Status = string(SessionStatusActive)
			existing.EndedAt = nil
			if err := ss.Persist(ctx, existing); err != nil {
				return mcp.ErrorResult(fmt.Errorf("persist resumed session: %w", err)), nil
			}
		}
		project := canonicalProject(existing.Project, existing.Namespace, existing.PipelineRef)
		result := map[string]any{
			"ok":         true,
			"session_id": resumeID,
			"resumed":    true,
			"agent_id":   existing.AgentID,
			"project":    project,
		}
		if existing.PipelineRef != nil {
			result["pipeline_ref"] = pipelineRefToPayload(existing.PipelineRef)
		}
		if ss.enrichResult != nil {
			ss.enrichResult(ctx, result, existing.AgentID, existing.Namespace)
		}
		return mcp.JSONResult(result)
	}

	// Idempotent start: if an active session already exists for this agent in the
	// same namespace, check whether it's live or stale (crash recovery).
	var recoveredFrom string
	if existing := ss.activeSessionForAgentNamespace(ctx, agentID, namespace); existing != nil {
		if ss.isAgentHeartbeatStale(agentID) {
			// Crash recovery: auto-end the orphaned session, then create a new one.
			recoveredFrom = existing.ID
			ss.logger.Info("crash recovery: ending stale session",
				"session_id", recoveredFrom, "agent_id", agentID)
			now := time.Now()
			existing.Status = string(SessionStatusEnded)
			existing.EndedAt = &now
			ss.mu.Lock()
			ss.sessions[recoveredFrom] = existing
			ss.mu.Unlock()
			if err := ss.Persist(ctx, existing); err != nil {
				ss.logger.Warn("failed to persist crash-recovered session end",
					"session_id", recoveredFrom, "error", err)
			}
			ss.metrics.SessionsActive.Add(-1)
			// Fall through to create a new session.
		} else {
			// Live session — return it (idempotency).
			project := canonicalProject(existing.Project, existing.Namespace, existing.PipelineRef)
			result := map[string]any{
				"ok":              true,
				"session_id":      existing.ID,
				"agent_id":        existing.AgentID,
				"namespace":       existing.Namespace,
				"project":         project,
				"started_at":      existing.StartedAt.Format(time.RFC3339),
				"already_existed": true,
			}
			if existing.PipelineRef != nil {
				result["pipeline_ref"] = pipelineRefToPayload(existing.PipelineRef)
			}
			if ss.enrichResult != nil {
				ss.enrichResult(ctx, result, existing.AgentID, existing.Namespace)
			}
			return mcp.JSONResult(result)
		}
	}

	// End any prior active sessions for this agent (non-crash path).
	if recoveredFrom == "" {
		ss.EndActiveForAgent(ctx, agentID)
	}

	// Create new session
	sessionID := GenerateID(agentID, "", time.Now().String(), time.Now())
	session := &Session{
		ID:          sessionID,
		AgentID:     agentID,
		Namespace:   namespace,
		Project:     project,
		StartedAt:   time.Now(),
		Status:      string(SessionStatusActive),
		Description: description,
		WorkingDir:  workingDir,
		PipelineRef: pipelineRef,
	}

	// Link to parent session hierarchy (subagent grouping).
	if parentSessionID != "" {
		session.ParentSessionID = parentSessionID
		if parent, err := ss.Get(ctx, parentSessionID); err == nil && parent != nil {
			if parent.RootSessionID != "" {
				session.RootSessionID = parent.RootSessionID
			} else {
				session.RootSessionID = parent.ID
			}
		} else {
			session.RootSessionID = parentSessionID
		}
	}

	ss.mu.Lock()
	ss.sessions[sessionID] = session
	ss.mu.Unlock()

	result := map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"agent_id":   agentID,
		"namespace":  namespace,
		"project":    project,
		"started_at": session.StartedAt.Format(time.RFC3339),
	}
	if recoveredFrom != "" {
		result["recovered_from"] = recoveredFrom
	}
	if pipelineRef != nil {
		result["pipeline_ref"] = pipelineRefToPayload(pipelineRef)
	}

	if err := ss.Persist(ctx, session); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist session: %v", err)
	}

	ss.metrics.SessionsActive.Add(1)
	ss.metrics.SessionsTotal.Add(1)

	if ss.enrichResult != nil {
		ss.enrichResult(ctx, result, agentID, namespace)
	}

	ss.publisher.Publish(EventTypeSessionStart, SessionStartEvent{
		SessionID:   sessionID,
		AgentID:     agentID,
		Namespace:   namespace,
		Project:     project,
		Description: description,
		WorkingDir:  workingDir,
		StartedAt:   session.StartedAt,
		ParentID:    session.ParentSessionID,
		RootID:      session.RootSessionID,
	})

	return mcp.JSONResult(result)
}

// isAgentHeartbeatStale checks whether the given agent's presence heartbeat
// has expired. Returns true (stale) when: (a) no presence is registered, or
// (b) the last heartbeat is older than HeartbeatTTL. This is used by crash
// recovery to decide whether an existing active session is orphaned.
func (ss *SessionSvc) isAgentHeartbeatStale(agentID string) bool {
	if ss.isPresenceStale == nil {
		// No presence registry wired — be conservative, treat as live.
		return false
	}
	return ss.isPresenceStale(agentID)
}

func (ss *SessionSvc) activeSessionForAgentNamespace(ctx context.Context, agentID, namespace string) *Session {
	agentID, namespace = normalizeSessionScope(agentID, namespace)
	if agentID == "" {
		return nil
	}

	ss.mu.RLock()
	for _, sess := range ss.sessions {
		if sessionMatchesIdentity(sess, agentID, namespace) && sess.Status == string(SessionStatusActive) {
			ss.mu.RUnlock()
			return sess
		}
	}
	ss.mu.RUnlock()

	if ss.qdrant == nil {
		return nil
	}

	conds := []any{
		Match("agent_id", agentID),
		Match("status", string(SessionStatusActive)),
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}

	points, err := ss.qdrant.ScrollPoints(ctx, FilterMust(conds...), 100, false)
	if err != nil {
		return nil
	}

	var newest *Session
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		if !sessionMatchesIdentity(sess, agentID, namespace) || sess.Status != string(SessionStatusActive) {
			continue
		}
		if newest == nil || sess.StartedAt.After(newest.StartedAt) {
			newest = sess
		}
	}
	if newest == nil {
		return nil
	}

	ss.mu.Lock()
	if existing, ok := ss.sessions[newest.ID]; ok && existing.Status == string(SessionStatusActive) &&
		sessionMatchesIdentity(existing, agentID, namespace) {
		ss.mu.Unlock()
		return existing
	}
	ss.sessions[newest.ID] = newest
	ss.mu.Unlock()

	return newest
}
