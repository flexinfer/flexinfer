package daemon

import (
	"sync"
	"testing"
	"time"
)

func TestSessionManager_StoreAndGet(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute)

	sess := &HTTPSession{
		ID:         "sess-1",
		CreatedAt:  time.Now(),
		LastAccess: time.Now(),
		ClientInfo: "test-client",
	}
	sm.Store(sess)

	got, ok := sm.Get("sess-1")
	if !ok {
		t.Fatal("expected session to be found")
	}
	if got.ID != "sess-1" {
		t.Fatalf("expected ID 'sess-1', got %q", got.ID)
	}
	if got.ClientInfo != "test-client" {
		t.Fatalf("expected ClientInfo 'test-client', got %q", got.ClientInfo)
	}

	// Nonexistent
	_, ok = sm.Get("nonexistent")
	if ok {
		t.Fatal("expected nonexistent session to not be found")
	}
}

func TestSessionManager_Delete(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute)

	sess := &HTTPSession{
		ID:         "sess-del",
		CreatedAt:  time.Now(),
		LastAccess: time.Now(),
	}
	sm.Store(sess)

	// Delete existing
	deleted := sm.Delete("sess-del")
	if !deleted {
		t.Fatal("expected Delete to return true for existing session")
	}

	// Verify gone
	_, ok := sm.Get("sess-del")
	if ok {
		t.Fatal("expected session to be gone after delete")
	}

	// Delete nonexistent
	deleted = sm.Delete("nonexistent")
	if deleted {
		t.Fatal("expected Delete to return false for nonexistent session")
	}
}

func TestSessionManager_Count(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute)

	if sm.Count() != 0 {
		t.Fatalf("expected count 0, got %d", sm.Count())
	}

	now := time.Now()
	for i := 0; i < 5; i++ {
		sm.Store(&HTTPSession{
			ID:         "sess-" + string(rune('a'+i)),
			CreatedAt:  now,
			LastAccess: now,
		})
	}

	if sm.Count() != 5 {
		t.Fatalf("expected count 5, got %d", sm.Count())
	}

	sm.Delete("sess-a")
	sm.Delete("sess-b")

	if sm.Count() != 3 {
		t.Fatalf("expected count 3 after deletes, got %d", sm.Count())
	}
}

func TestSessionManager_ReapExpired(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 1*time.Minute)

	now := time.Now()
	staleTime := now.Add(-2 * time.Minute) // older than 1min timeout

	// 3 stale sessions
	for i := 0; i < 3; i++ {
		sm.Store(&HTTPSession{
			ID:         "stale-" + string(rune('a'+i)),
			CreatedAt:  staleTime,
			LastAccess: staleTime,
		})
	}

	// 1 fresh session
	sm.Store(&HTTPSession{
		ID:         "fresh-1",
		CreatedAt:  now,
		LastAccess: now,
	})

	reaped := sm.ReapExpired()
	if reaped != 3 {
		t.Fatalf("expected 3 reaped, got %d", reaped)
	}

	if sm.Count() != 1 {
		t.Fatalf("expected 1 remaining, got %d", sm.Count())
	}

	// Verify fresh session survived
	_, ok := sm.Get("fresh-1")
	if !ok {
		t.Fatal("expected fresh session to survive reaping")
	}
}

func TestSessionManager_ReapExpired_None(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(100, 10*time.Minute)

	now := time.Now()
	for i := 0; i < 3; i++ {
		sm.Store(&HTTPSession{
			ID:         "fresh-" + string(rune('a'+i)),
			CreatedAt:  now,
			LastAccess: now,
		})
	}

	reaped := sm.ReapExpired()
	if reaped != 0 {
		t.Fatalf("expected 0 reaped (all fresh), got %d", reaped)
	}

	if sm.Count() != 3 {
		t.Fatalf("expected all 3 sessions to remain, got %d", sm.Count())
	}
}

func TestSessionManager_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	sm := NewSessionManager(1000, 10*time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			now := time.Now()
			sessID := "concurrent-" + string(rune('a'+id))
			sm.Store(&HTTPSession{
				ID:         sessID,
				CreatedAt:  now,
				LastAccess: now,
			})
			sm.Get(sessID)
			sm.Count()
			if id%2 == 0 {
				sm.Delete(sessID)
			}
		}(i)
	}
	wg.Wait()

	// Verify no panic and count is reasonable
	count := sm.Count()
	if count < 0 || count > 10 {
		t.Fatalf("unexpected count after concurrent access: %d", count)
	}
}

func TestSessionManager_Defaults(t *testing.T) {
	t.Parallel()

	// Zero timeout defaults to 30min
	sm := NewSessionManager(0, 0)

	now := time.Now()
	sm.Store(&HTTPSession{
		ID:         "default-test",
		CreatedAt:  now,
		LastAccess: now,
	})

	// Session is fresh, so reaping should not remove it
	reaped := sm.ReapExpired()
	if reaped != 0 {
		t.Fatalf("expected 0 reaped with default timeout, got %d", reaped)
	}

	// Verify the session is still there
	_, ok := sm.Get("default-test")
	if !ok {
		t.Fatal("session should survive with default timeout")
	}
}
