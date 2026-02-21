package daemon

import (
	"sync"
	"testing"
	"time"
)

func TestSessionManager_OpenAndGet(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute, 1, nil)

	sess := sm.Open(SessionClientInfo{AgentHint: "test-agent", Version: "1.0"}, "")
	if sess.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if sess.DaemonEpoch != 1 {
		t.Fatalf("expected epoch 1, got %d", sess.DaemonEpoch)
	}
	if sess.State != SessionActive {
		t.Fatalf("expected state active, got %s", sess.State)
	}
	if sess.ClientInfo.AgentHint != "test-agent" {
		t.Fatalf("expected AgentHint 'test-agent', got %q", sess.ClientInfo.AgentHint)
	}

	got, ok := sm.Get(sess.ID)
	if !ok {
		t.Fatal("expected session to be found")
	}
	if got.ID != sess.ID {
		t.Fatalf("expected ID %q, got %q", sess.ID, got.ID)
	}

	// Nonexistent
	_, ok = sm.Get("nonexistent")
	if ok {
		t.Fatal("expected nonexistent session to not be found")
	}
}

func TestSessionManager_OpenWithPriorID(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute, 2, nil)

	sess := sm.Open(SessionClientInfo{}, "old-session-123")
	if sess.PriorID != "old-session-123" {
		t.Fatalf("expected PriorID 'old-session-123', got %q", sess.PriorID)
	}
	if sess.DaemonEpoch != 2 {
		t.Fatalf("expected epoch 2, got %d", sess.DaemonEpoch)
	}
}

func TestSessionManager_HeartbeatSuccess(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute, 1, nil)

	sess := sm.Open(SessionClientInfo{}, "")
	origLease := sess.LeaseExpires

	// Small delay to ensure time progresses.
	time.Sleep(1 * time.Millisecond)

	updated, err := sm.Heartbeat(sess.ID, 1)
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	if !updated.LeaseExpires.After(origLease) {
		t.Fatal("expected lease to be extended")
	}
}

func TestSessionManager_HeartbeatEpochMismatch(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute, 1, nil)

	sess := sm.Open(SessionClientInfo{}, "")

	_, err := sm.Heartbeat(sess.ID, 999)
	if err == nil {
		t.Fatal("expected epoch mismatch error")
	}
}

func TestSessionManager_HeartbeatNotFound(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute, 1, nil)

	_, err := sm.Heartbeat("nonexistent", 1)
	if err == nil {
		t.Fatal("expected not found error")
	}
}

func TestSessionManager_Close(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute, 1, nil)

	sess := sm.Open(SessionClientInfo{}, "")

	closed := sm.Close(sess.ID)
	if !closed {
		t.Fatal("expected Close to return true")
	}

	_, ok := sm.Get(sess.ID)
	if ok {
		t.Fatal("expected session to be gone after close")
	}

	// Close nonexistent
	closed = sm.Close("nonexistent")
	if closed {
		t.Fatal("expected Close to return false for nonexistent")
	}
}

func TestSessionManager_ReapExpired(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 1*time.Millisecond, 1, nil)

	// Open 3 sessions that will expire immediately.
	for i := 0; i < 3; i++ {
		sm.Open(SessionClientInfo{}, "")
	}

	// Wait for leases to expire.
	time.Sleep(5 * time.Millisecond)

	// Open 1 fresh session.
	fresh := sm.Open(SessionClientInfo{}, "")

	reaped := sm.ReapExpired()
	if reaped != 3 {
		t.Fatalf("expected 3 reaped, got %d", reaped)
	}

	if sm.Count() != 1 {
		t.Fatalf("expected 1 remaining, got %d", sm.Count())
	}

	_, ok := sm.Get(fresh.ID)
	if !ok {
		t.Fatal("expected fresh session to survive reaping")
	}
}

func TestSessionManager_LRUEviction(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(3, 10*time.Minute, 1, nil)

	s1 := sm.Open(SessionClientInfo{AgentHint: "a"}, "")
	time.Sleep(1 * time.Millisecond)
	_ = sm.Open(SessionClientInfo{AgentHint: "b"}, "")
	time.Sleep(1 * time.Millisecond)
	_ = sm.Open(SessionClientInfo{AgentHint: "c"}, "")

	// At capacity (3). Opening a 4th should evict s1 (oldest LastSeenAt).
	_ = sm.Open(SessionClientInfo{AgentHint: "d"}, "")

	if sm.Count() != 3 {
		t.Fatalf("expected count 3 after eviction, got %d", sm.Count())
	}

	_, ok := sm.Get(s1.ID)
	if ok {
		t.Fatal("expected oldest session (s1) to be evicted")
	}
}

func TestSessionManager_DrainAll(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute, 1, nil)

	sm.Open(SessionClientInfo{}, "")
	sm.Open(SessionClientInfo{}, "")
	sm.Open(SessionClientInfo{}, "")

	drained := sm.DrainAll()
	if drained != 3 {
		t.Fatalf("expected 3 drained, got %d", drained)
	}

	if sm.ActiveCount() != 0 {
		t.Fatalf("expected 0 active after drain, got %d", sm.ActiveCount())
	}

	// Total count should still be 3 (draining, not removed).
	if sm.Count() != 3 {
		t.Fatalf("expected 3 total after drain, got %d", sm.Count())
	}

	// Drain again should return 0 (already draining).
	if sm.DrainAll() != 0 {
		t.Fatal("expected 0 on second DrainAll")
	}
}

func TestSessionManager_Touch(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute, 1, nil)

	sess := sm.Open(SessionClientInfo{}, "")
	origLease := sess.LeaseExpires

	time.Sleep(1 * time.Millisecond)

	sm.Touch(sess.ID)

	got, _ := sm.Get(sess.ID)
	if !got.LeaseExpires.After(origLease) {
		t.Fatal("expected Touch to extend lease")
	}

	// Touch nonexistent is a no-op (no panic).
	sm.Touch("nonexistent")
}

func TestSessionManager_CountAndActiveCount(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute, 1, nil)

	if sm.Count() != 0 {
		t.Fatalf("expected 0, got %d", sm.Count())
	}
	if sm.ActiveCount() != 0 {
		t.Fatalf("expected 0 active, got %d", sm.ActiveCount())
	}

	sm.Open(SessionClientInfo{}, "")
	sm.Open(SessionClientInfo{}, "")

	if sm.Count() != 2 {
		t.Fatalf("expected 2, got %d", sm.Count())
	}
	if sm.ActiveCount() != 2 {
		t.Fatalf("expected 2 active, got %d", sm.ActiveCount())
	}

	sm.DrainAll()
	if sm.ActiveCount() != 0 {
		t.Fatalf("expected 0 active after drain, got %d", sm.ActiveCount())
	}
}

func TestSessionManager_Epoch(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute, 42, nil)
	if sm.Epoch() != 42 {
		t.Fatalf("expected epoch 42, got %d", sm.Epoch())
	}
}

func TestSessionManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(1000, 10*time.Minute, 1, nil)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sess := sm.Open(SessionClientInfo{AgentHint: "concurrent"}, "")
			sm.Get(sess.ID)
			sm.Touch(sess.ID)
			sm.Count()
			sm.ActiveCount()
			if id%3 == 0 {
				sm.Close(sess.ID)
			}
			if id%5 == 0 {
				sm.Heartbeat(sess.ID, 1)
			}
		}(i)
	}
	wg.Wait()

	count := sm.Count()
	if count < 0 || count > 20 {
		t.Fatalf("unexpected count after concurrent access: %d", count)
	}
}

func TestSessionManager_Defaults(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager(0, 0, 1, nil)

	sess := sm.Open(SessionClientInfo{}, "")

	// Session is fresh, reaping should not remove it.
	reaped := sm.ReapExpired()
	if reaped != 0 {
		t.Fatalf("expected 0 reaped with default timeout, got %d", reaped)
	}

	_, ok := sm.Get(sess.ID)
	if !ok {
		t.Fatal("session should survive with default timeout")
	}
}
