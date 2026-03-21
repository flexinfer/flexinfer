package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// SessionState represents the lifecycle state of a proxy session.
type SessionState string

const (
	SessionActive   SessionState = "active"
	SessionDraining SessionState = "draining"
	SessionExpired  SessionState = "expired"
	SessionClosed   SessionState = "closed"
)

// ProxySession represents a proxy client session with the daemon.
type ProxySession struct {
	ID           string            `json:"id"`
	PriorID      string            `json:"priorId,omitempty"`
	DaemonEpoch  int64             `json:"daemonEpoch"`
	State        SessionState      `json:"state"`
	CreatedAt    time.Time         `json:"createdAt"`
	LastSeenAt   time.Time         `json:"lastSeenAt"`
	LeaseExpires time.Time         `json:"leaseExpires"`
	ClientInfo   SessionClientInfo `json:"clientInfo"`
}

// SessionClientInfo holds metadata about the connecting proxy client.
type SessionClientInfo struct {
	AgentHint string `json:"agentHint,omitempty"`
	HostPID   string `json:"hostPid,omitempty"`
	Version   string `json:"version,omitempty"`
}

// SessionManager manages proxy sessions for the daemon.
type SessionManager struct {
	mu          sync.RWMutex
	sessions    map[string]*ProxySession
	maxSessions int
	leaseTime   time.Duration
	daemonEpoch int64
	logger      *slog.Logger
}

// NewSessionManager creates a new session manager.
func NewSessionManager(maxSessions int, leaseTime time.Duration, daemonEpoch int64, logger *slog.Logger) *SessionManager {
	if maxSessions <= 0 {
		maxSessions = 1000
	}
	if leaseTime <= 0 {
		leaseTime = 30 * time.Minute
	}
	return &SessionManager{
		sessions:    make(map[string]*ProxySession),
		maxSessions: maxSessions,
		leaseTime:   leaseTime,
		daemonEpoch: daemonEpoch,
		logger:      logger,
	}
}

// Open creates a new proxy session, returning it. If maxSessions is reached,
// the session with the oldest LastSeenAt is evicted (LRU).
func (m *SessionManager) Open(clientInfo SessionClientInfo, priorID string) *ProxySession {
	m.mu.Lock()
	defer m.mu.Unlock()

	// LRU eviction if at capacity.
	if len(m.sessions) >= m.maxSessions {
		m.evictOldestLocked()
	}

	now := time.Now()
	sess := &ProxySession{
		ID:           generateSessionID(),
		PriorID:      priorID,
		DaemonEpoch:  m.daemonEpoch,
		State:        SessionActive,
		CreatedAt:    now,
		LastSeenAt:   now,
		LeaseExpires: now.Add(m.leaseTime),
		ClientInfo:   clientInfo,
	}
	m.sessions[sess.ID] = sess
	return sess
}

// Heartbeat refreshes a session's lease. Returns an error if the session is
// not found or the provided epoch does not match the daemon epoch.
func (m *SessionManager) Heartbeat(id string, epoch int64) (*ProxySession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}

	if epoch != m.daemonEpoch {
		return nil, fmt.Errorf("epoch mismatch: client=%d daemon=%d", epoch, m.daemonEpoch)
	}

	if sess.State != SessionActive {
		return nil, fmt.Errorf("session not active: %s (state=%s)", id, sess.State)
	}

	now := time.Now()
	sess.LastSeenAt = now
	sess.LeaseExpires = now.Add(m.leaseTime)
	return sess, nil
}

// Close marks a session as closed and removes it.
func (m *SessionManager) Close(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[id]
	if !ok {
		return false
	}
	sess.State = SessionClosed
	delete(m.sessions, id)
	return true
}

// Get returns a session by ID.
func (m *SessionManager) Get(id string) (*ProxySession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sess, ok := m.sessions[id]
	return sess, ok
}

// Touch updates a session's LastSeenAt and lease expiry. This is called
// implicitly on RPC calls that carry a session_id.
func (m *SessionManager) Touch(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[id]
	if !ok || sess.State != SessionActive {
		return
	}
	now := time.Now()
	sess.LastSeenAt = now
	sess.LeaseExpires = now.Add(m.leaseTime)
}

// Count returns the total number of sessions.
func (m *SessionManager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// ActiveCount returns the number of sessions in active state.
func (m *SessionManager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, sess := range m.sessions {
		if sess.State == SessionActive {
			count++
		}
	}
	return count
}

// ReapExpired removes sessions whose lease has expired. Returns the count reaped.
func (m *SessionManager) ReapExpired() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	reaped := 0
	for id, sess := range m.sessions {
		if sess.State == SessionActive && now.After(sess.LeaseExpires) {
			sess.State = SessionExpired
			delete(m.sessions, id)
			reaped++
		}
	}
	return reaped
}

// DrainAll transitions all active sessions to draining state.
func (m *SessionManager) DrainAll() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	drained := 0
	for _, sess := range m.sessions {
		if sess.State == SessionActive {
			sess.State = SessionDraining
			drained++
		}
	}
	return drained
}

// IsDraining returns true if any sessions are in draining state.
func (m *SessionManager) IsDraining() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, sess := range m.sessions {
		if sess.State == SessionDraining {
			return true
		}
	}
	return false
}

// Epoch returns the daemon epoch this manager was initialized with.
func (m *SessionManager) Epoch() int64 {
	return m.daemonEpoch
}

// evictOldestLocked removes the session with the oldest LastSeenAt.
// Caller must hold m.mu.
func (m *SessionManager) evictOldestLocked() {
	var oldestID string
	var oldestTime time.Time

	for id, sess := range m.sessions {
		if oldestID == "" || sess.LastSeenAt.Before(oldestTime) {
			oldestID = id
			oldestTime = sess.LastSeenAt
		}
	}

	if oldestID != "" {
		if m.logger != nil {
			m.logger.Debug("evicting oldest session (LRU)", "session_id", oldestID)
		}
		delete(m.sessions, oldestID)
	}
}

func generateSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails.
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
