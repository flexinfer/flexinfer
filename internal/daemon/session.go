package daemon

import (
	"sync"
	"time"
)

// SessionManager manages HTTP sessions for the Streamable HTTP transport.
type SessionManager struct {
	sessions    sync.Map
	maxSessions int
	timeout     time.Duration
}

// HTTPSession represents an authenticated HTTP client session.
type HTTPSession struct {
	ID          string
	CreatedAt   time.Time
	LastAccess  time.Time
	Initialized bool
	ClientInfo  string // client name from initialize
}

// NewSessionManager creates a new session manager.
func NewSessionManager(maxSessions int, timeout time.Duration) *SessionManager {
	if timeout == 0 {
		timeout = 30 * time.Minute
	}
	if maxSessions == 0 {
		maxSessions = 1000
	}
	return &SessionManager{
		maxSessions: maxSessions,
		timeout:     timeout,
	}
}

// Get returns a session by ID.
func (m *SessionManager) Get(id string) (*HTTPSession, bool) {
	val, ok := m.sessions.Load(id)
	if !ok {
		return nil, false
	}
	return val.(*HTTPSession), true
}

// Store adds a session.
func (m *SessionManager) Store(session *HTTPSession) {
	m.sessions.Store(session.ID, session)
}

// Delete removes a session.
func (m *SessionManager) Delete(id string) bool {
	_, loaded := m.sessions.LoadAndDelete(id)
	return loaded
}

// Count returns the number of active sessions.
func (m *SessionManager) Count() int {
	count := 0
	m.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// ReapExpired removes sessions idle longer than the timeout. Returns the count of reaped sessions.
func (m *SessionManager) ReapExpired() int {
	reaped := 0
	now := time.Now()
	m.sessions.Range(func(key, value any) bool {
		session := value.(*HTTPSession)
		if now.Sub(session.LastAccess) > m.timeout {
			m.sessions.Delete(key)
			reaped++
		}
		return true
	})
	return reaped
}
