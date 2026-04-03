package agentcontext

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// SessionSvc manages session lifecycle, persistence, and reaping.
type SessionSvc struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	// Per-agent start locks keep concurrent starts from creating duplicate
	// active sessions for the same agent/namespace scope.
	startLocks sync.Map

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

	// countContextEntries returns entry count + total tokens from the context
	// collection for a given session ID. Used to recompute stats at list time
	// when persisted values are stale (e.g. after HUD restart).
	countContextEntries func(ctx context.Context, sessionID string) (entries int, tokens int)
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

	ss.sessions = make(map[string]*Session, len(points))
	loaded := 0
	latestByIdentity := make(map[string]*Session)
	for _, p := range points {
		sess, err := PayloadToSession(p.Payload)
		if err != nil || sess == nil {
			continue
		}
		key := sessionIdentityKey(sess.AgentID, sess.Namespace)
		if current, ok := latestByIdentity[key]; ok && !sess.StartedAt.After(current.StartedAt) {
			continue
		}
		latestByIdentity[key] = sess
	}
	for _, sess := range latestByIdentity {
		ss.sessions[sess.ID] = sess
		loaded++
	}

	if loaded > 0 {
		ss.logger.Info("restored active sessions", "count", loaded)
	}
	return nil
}

func (ss *SessionSvc) lockAgentStart(agentID string) func() {
	key := strings.TrimSpace(agentID)
	if key == "" {
		return func() {}
	}

	muAny, _ := ss.startLocks.LoadOrStore(key, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func normalizeSessionScope(agentID, namespace string) (string, string) {
	return strings.TrimSpace(agentID), strings.TrimSpace(namespace)
}

func sessionIdentityKey(agentID, namespace string) string {
	agentID, namespace = normalizeSessionScope(agentID, namespace)
	return agentID + "\x00" + namespace
}

func sessionMatchesIdentity(sess *Session, agentID, namespace string) bool {
	if sess == nil {
		return false
	}
	return strings.TrimSpace(sess.AgentID) == agentID && strings.TrimSpace(sess.Namespace) == namespace
}
