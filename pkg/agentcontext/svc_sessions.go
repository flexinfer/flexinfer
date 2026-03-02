package agentcontext

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// SessionSvc manages session lifecycle, persistence, and reaping.
type SessionSvc struct {
	mu       sync.RWMutex
	sessions map[string]*Session

	qdrant  *QdrantClient // CollSessions
	cfg     Config
	logger  *slog.Logger
	metrics *Metrics

	// Cross-domain cleanup callbacks (wired by Service for HandleSessionEnd).
	releaseClaimsForAgent    func(agentID string) int
	removePresence           func(agentID string) bool
	deletePresenceFromQdrant func(ctx context.Context, agentID string) error
	orphanWorktrees          func(agentID string)
	markTasksStale           func(ctx context.Context, sessionID string) int

	// Enrichment callback (for HandleSessionStart coordination info).
	enrichResult func(ctx context.Context, result map[string]any, agentID, namespace string)

	// Summary callbacks (for HandleSessionEnd).
	generateSummary func(ctx context.Context, session *Session) error
	runSummaryAsync func(session *Session)

	// Reaper callback — returns IDs of agents with live presence.
	liveAgentIDs func() []string
}

// NewSessionSvc creates a new SessionSvc.
func NewSessionSvc(qdrant *QdrantClient, cfg Config, logger *slog.Logger, metrics *Metrics) *SessionSvc {
	return &SessionSvc{
		sessions: make(map[string]*Session),
		qdrant:   qdrant,
		cfg:      cfg,
		logger:   logger,
		metrics:  metrics,
	}
}

// Get retrieves a session by ID, checking in-memory cache first then Qdrant.
func (ss *SessionSvc) Get(ctx context.Context, sessionID string) (*Session, error) {
	ss.mu.RLock()
	if sess, ok := ss.sessions[sessionID]; ok {
		ss.mu.RUnlock()
		return sess, nil
	}
	ss.mu.RUnlock()

	if ss.qdrant == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	p, err := ss.qdrant.GetPoint(ctx, sessionID, false)
	if err != nil {
		return nil, err
	}
	sess, err := PayloadToSession(p.Payload)
	if err != nil || sess == nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	ss.mu.Lock()
	// Re-check: another goroutine may have loaded the same session concurrently
	if existing, ok := ss.sessions[sessionID]; ok {
		ss.mu.Unlock()
		return existing, nil
	}
	ss.sessions[sessionID] = sess
	ss.mu.Unlock()

	return sess, nil
}

// Persist stores a session to Qdrant.
func (ss *SessionSvc) Persist(ctx context.Context, session *Session) error {
	if ss.qdrant == nil {
		return nil
	}
	payload := SessionToPayload(*session)
	dummyVector := make([]float64, sessionsVectorSize)

	point := Point{
		ID:      session.ID,
		Vector:  dummyVector,
		Payload: payload,
	}

	if err := ss.qdrant.EnsureCollection(ctx, sessionsVectorSize); err != nil {
		return err
	}
	return ss.qdrant.Upsert(ctx, []Point{point}, true)
}

// Start creates a new session or resumes an existing one.
func (ss *SessionSvc) Start(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.String("agent_id", ss.cfg.DefaultAgentID)
	namespace := v.String("namespace", ss.cfg.DefaultNamespace)
	description := v.String("description", "")
	workingDir := v.String("working_dir", "")
	resumeID := v.String("resume_session_id", "")

	if agentID == "" {
		return mcp.ErrorResult(fmt.Errorf("agent_id is required")), nil
	}

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
		result := map[string]any{
			"ok":         true,
			"session_id": resumeID,
			"resumed":    true,
			"agent_id":   existing.AgentID,
		}
		if ss.enrichResult != nil {
			ss.enrichResult(ctx, result, existing.AgentID, existing.Namespace)
		}
		return mcp.JSONResult(result)
	}

	// End any prior active sessions for this agent.
	ss.EndActiveForAgent(ctx, agentID)

	// Create new session
	sessionID := GenerateID(agentID, "", time.Now().String(), time.Now())
	session := &Session{
		ID:          sessionID,
		AgentID:     agentID,
		Namespace:   namespace,
		StartedAt:   time.Now(),
		Status:      string(SessionStatusActive),
		Description: description,
		WorkingDir:  workingDir,
	}

	ss.mu.Lock()
	ss.sessions[sessionID] = session
	ss.mu.Unlock()

	result := map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"agent_id":   agentID,
		"namespace":  namespace,
		"started_at": session.StartedAt.Format(time.RFC3339),
	}

	if err := ss.Persist(ctx, session); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist session: %v", err)
	}

	ss.metrics.SessionsActive.Add(1)
	ss.metrics.SessionsTotal.Add(1)

	if ss.enrichResult != nil {
		ss.enrichResult(ctx, result, agentID, namespace)
	}
	return mcp.JSONResult(result)
}

// End marks a session as ended and optionally generates a summary.
func (ss *SessionSvc) End(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	summarize := v.Bool("summarize", true)
	summaryAsync := v.Bool("summary_async", false)
	cleanup := v.Bool("cleanup", true)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := ss.Get(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}
	now := time.Now()
	session.EndedAt = &now
	session.Status = string(SessionStatusEnded)
	ss.mu.Lock()
	ss.sessions[sessionID] = session
	ss.mu.Unlock()
	ss.metrics.SessionsActive.Add(-1)

	result := map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"ended_at":   now.Format(time.RFC3339),
		"summarized": false,
	}

	if err := ss.Persist(ctx, session); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist session end: %v", err)
	}

	// Optionally generate summary
	if summarize && ss.cfg.AutoSummarize {
		if summaryAsync {
			result["summary_queued"] = true
			if ss.runSummaryAsync != nil {
				go ss.runSummaryAsync(session)
			}
		} else if ss.generateSummary != nil {
			if err := ss.generateSummary(ctx, session); err != nil {
				result["summary_error"] = err.Error()
			} else {
				result["summarized"] = true
				session.Status = string(SessionStatusSummarized)
				if err := ss.Persist(ctx, session); err != nil {
					result["_persist_error"] = err.Error()
				}
			}
		}
	}

	// Auto-cleanup coordination resources
	if cleanup {
		agentID := session.AgentID
		cleanedUp := map[string]any{}

		if ss.releaseClaimsForAgent != nil {
			released := ss.releaseClaimsForAgent(agentID)
			cleanedUp["file_claims_released"] = released
		}

		if ss.removePresence != nil {
			hadPresence := ss.removePresence(agentID)
			cleanedUp["presence_deregistered"] = hadPresence

			if hadPresence && ss.deletePresenceFromQdrant != nil {
				if err := ss.deletePresenceFromQdrant(ctx, agentID); err != nil {
					ss.logger.Warn("failed to delete presence from Qdrant", "agent_id", agentID, "error", err)
				}
			}
		}

		if ss.orphanWorktrees != nil {
			ss.orphanWorktrees(agentID)
			cleanedUp["worktrees_orphaned"] = true
		}

		if ss.markTasksStale != nil {
			staleTasks := ss.markTasksStale(ctx, sessionID)
			cleanedUp["tasks_marked_stale"] = staleTasks
		}

		result["cleanup"] = cleanedUp
	}

	return mcp.JSONResult(result)
}

// List returns sessions matching optional filters.
func (ss *SessionSvc) List(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.String("agent_id", "")
	namespace := v.String("namespace", "")
	status := v.String("status", "")
	limit := v.Int("limit", 20)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var conds []any
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}
	if status != "" {
		conds = append(conds, Match("status", status))
	}

	var filter map[string]any
	if len(conds) > 0 {
		filter = FilterMust(conds...)
	}

	points, err := ss.qdrant.ScrollPoints(ctx, filter, limit, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list sessions: %w", err)), nil
	}

	sessions := make([]Session, 0, len(points))
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		sessions = append(sessions, *sess)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.After(sessions[j].StartedAt)
	})

	if len(sessions) > limit {
		sessions = sessions[:limit]
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"sessions": sessions,
		"count":    len(sessions),
	})
}

// Delete removes a session by ID.
func (ss *SessionSvc) Delete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ss.mu.Lock()
	_, existed := ss.sessions[sessionID]
	delete(ss.sessions, sessionID)
	ss.mu.Unlock()

	if ss.qdrant != nil {
		if err := ss.qdrant.Delete(ctx, []string{sessionID}); err != nil {
			return mcp.ErrorResult(fmt.Errorf("delete session from Qdrant: %w", err)), nil
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"session_id": sessionID,
		"existed":    existed,
	})
}

// Prune deletes stale sessions matching status and age criteria.
func (ss *SessionSvc) Prune(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	maxAgeHours := v.Int("max_age_hours", 72)
	statusFilter := v.String("status", "ended,summarized")
	dryRun := v.Bool("dry_run", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	pruned, err := ss.PruneSessions(ctx, maxAgeHours, statusFilter, dryRun)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("prune sessions: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"pruned":  pruned,
		"dry_run": dryRun,
	})
}

// PruneSessions deletes sessions matching status and age criteria.
func (ss *SessionSvc) PruneSessions(ctx context.Context, maxAgeHours int, statusFilter string, dryRun bool) (int, error) {
	if ss.qdrant == nil {
		return 0, nil
	}

	statuses := strings.Split(statusFilter, ",")
	for i, st := range statuses {
		statuses[i] = strings.TrimSpace(st)
	}

	cutoff := time.Now().Add(-time.Duration(maxAgeHours) * time.Hour)

	var statusConds []any
	for _, st := range statuses {
		if st != "" {
			statusConds = append(statusConds, Match("status", st))
		}
	}
	if len(statusConds) == 0 {
		return 0, nil
	}

	filter := FilterMust(FilterShould(statusConds...))
	points, err := ss.qdrant.ScrollPoints(ctx, filter, 1000, false)
	if err != nil {
		return 0, fmt.Errorf("scroll sessions: %w", err)
	}

	var toDelete []string
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		ts := sess.StartedAt
		if sess.EndedAt != nil {
			ts = *sess.EndedAt
		}
		if ts.Before(cutoff) {
			toDelete = append(toDelete, sess.ID)
		}
	}

	if dryRun || len(toDelete) == 0 {
		return len(toDelete), nil
	}

	if err := ss.qdrant.Delete(ctx, toDelete); err != nil {
		return 0, fmt.Errorf("delete sessions: %w", err)
	}

	ss.mu.Lock()
	for _, id := range toDelete {
		delete(ss.sessions, id)
	}
	ss.mu.Unlock()

	ss.logger.Info("pruned stale sessions", "count", len(toDelete), "max_age_hours", maxAgeHours, "statuses", statusFilter)
	return len(toDelete), nil
}

// EndActiveForAgent ends all active sessions belonging to the given agent.
func (ss *SessionSvc) EndActiveForAgent(ctx context.Context, agentID string) {
	now := time.Now()

	// End in-memory sessions.
	ss.mu.Lock()
	for _, sess := range ss.sessions {
		if sess.AgentID == agentID && sess.Status == string(SessionStatusActive) {
			sess.Status = string(SessionStatusEnded)
			sess.EndedAt = &now
		}
	}
	ss.mu.Unlock()

	// End persisted sessions in Qdrant.
	if ss.qdrant == nil {
		return
	}
	filter := FilterMust(
		Match("agent_id", agentID),
		Match("status", "active"),
	)
	points, err := ss.qdrant.ScrollPoints(ctx, filter, 500, false)
	if err != nil || len(points) == 0 {
		return
	}
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		sess.Status = string(SessionStatusEnded)
		sess.EndedAt = &now
		if err := ss.Persist(ctx, sess); err != nil {
			ss.logger.Warn("failed to end stale session for expired agent",
				"session_id", sess.ID, "agent_id", agentID, "error", err)
		}
	}
	ss.logger.Info("ended active sessions for expired agent",
		"agent_id", agentID, "count", len(points))
}

// EndStale finds active sessions older than maxAgeHours whose agents
// have no current presence, and marks them ended.
func (ss *SessionSvc) EndStale(ctx context.Context, maxAgeHours int) int {
	if ss.qdrant == nil {
		return 0
	}

	cutoff := time.Now().Add(-time.Duration(maxAgeHours) * time.Hour)

	filter := FilterMust(Match("status", "active"))
	points, err := ss.qdrant.ScrollPoints(ctx, filter, 1000, false)
	if err != nil {
		ss.logger.Warn("endStaleSessions scroll failed", "error", err)
		return 0
	}

	// Snapshot of agents with live presence.
	var liveAgents map[string]bool
	if ss.liveAgentIDs != nil {
		ids := ss.liveAgentIDs()
		liveAgents = make(map[string]bool, len(ids))
		for _, id := range ids {
			liveAgents[id] = true
		}
	}

	now := time.Now()
	var ended int
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		if liveAgents[sess.AgentID] {
			continue
		}
		if sess.StartedAt.After(cutoff) {
			continue
		}
		sess.Status = string(SessionStatusEnded)
		sess.EndedAt = &now
		if err := ss.Persist(ctx, sess); err != nil {
			ss.logger.Warn("failed to persist stale session end", "session_id", sess.ID, "error", err)
			continue
		}
		ss.mu.Lock()
		if existing, ok := ss.sessions[sess.ID]; ok {
			existing.Status = string(SessionStatusEnded)
			existing.EndedAt = &now
		}
		ss.mu.Unlock()
		ended++
	}
	return ended
}

// RunReaper periodically prunes old sessions and reaps stale active sessions.
func (ss *SessionSvc) RunReaper(ctx context.Context) {
	interval := time.Duration(ss.cfg.SessionReaperInterval) * time.Second
	if interval <= 0 {
		interval = 30 * time.Minute
	}

	// Immediate sweep on startup.
	ss.reaperTick(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ss.reaperTick(ctx)
		}
	}
}

func (ss *SessionSvc) reaperTick(ctx context.Context) {
	statusFilter := "ended,summarized"
	pruned, err := ss.PruneSessions(ctx, ss.cfg.SessionReaperMaxAge, statusFilter, false)
	if err != nil {
		ss.logger.Warn("session reaper failed", "error", err)
	} else if pruned > 0 {
		ss.logger.Info("session reaper completed", "pruned", pruned, "filter", statusFilter)
	}

	activeMaxAge := ss.cfg.SessionReaperActiveMaxAge
	if activeMaxAge <= 0 {
		activeMaxAge = 24
	}
	ended := ss.EndStale(ctx, activeMaxAge)
	if ended > 0 {
		ss.logger.Info("ended stale active sessions", "count", ended, "max_age_hours", activeMaxAge)
	}
}

// LoadFromQdrant loads active sessions into the in-memory cache.
func (ss *SessionSvc) LoadFromQdrant(ctx context.Context) error {
	if ss.qdrant == nil {
		return nil
	}

	points, err := ss.qdrant.ScrollPoints(ctx, FilterMust(Match("status", string(SessionStatusActive))), 500, false)
	if err != nil {
		return err
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	loaded := 0
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		ss.sessions[sess.ID] = sess
		loaded++
	}

	if loaded > 0 {
		ss.logger.Info("restored active sessions", "count", loaded)
	}
	return nil
}
