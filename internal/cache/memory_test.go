package cache

import (
	"testing"
	"time"
)

// storeTests runs the shared Store contract tests against any implementation.
func storeTests(t *testing.T, s Store) {
	t.Helper()

	t.Run("GetMiss", func(t *testing.T) {
		_, ok := s.Get("nonexistent")
		if ok {
			t.Fatal("expected miss on nonexistent key")
		}
	})

	t.Run("SetAndGet", func(t *testing.T) {
		s.Set("key1", "value1", 10*time.Second)
		v, ok := s.Get("key1")
		if !ok {
			t.Fatal("expected hit")
		}
		str, _ := v.(string)
		if str != "value1" {
			t.Fatalf("expected 'value1', got %v", v)
		}
	})

	t.Run("SetOverwrite", func(t *testing.T) {
		s.Set("overwrite", "old", 10*time.Second)
		s.Set("overwrite", "new", 10*time.Second)
		v, ok := s.Get("overwrite")
		if !ok {
			t.Fatal("expected hit")
		}
		str, _ := v.(string)
		if str != "new" {
			t.Fatalf("expected 'new', got %v", v)
		}
	})

	t.Run("Invalidate", func(t *testing.T) {
		s.Set("del-me", "val", 10*time.Second)
		s.Invalidate("del-me")
		_, ok := s.Get("del-me")
		if ok {
			t.Fatal("expected miss after invalidation")
		}
	})

	t.Run("InvalidateAll", func(t *testing.T) {
		s.Set("a", 1, 10*time.Second)
		s.Set("b", 2, 10*time.Second)
		s.InvalidateAll()
		if s.Len() != 0 {
			t.Fatalf("expected 0 entries after InvalidateAll, got %d", s.Len())
		}
	})

	t.Run("Len", func(t *testing.T) {
		s.InvalidateAll()
		s.Set("x", 1, 10*time.Second)
		s.Set("y", 2, 10*time.Second)
		if s.Len() < 2 {
			t.Fatalf("expected at least 2 entries, got %d", s.Len())
		}
	})
}

func TestMemoryStore(t *testing.T) {
	s := NewMemoryStore()
	defer s.Close()
	storeTests(t, s)
}

func TestMemoryStore_TTLExpiry(t *testing.T) {
	s := NewMemoryStore()
	defer s.Close()

	s.Set("ephemeral", "data", 50*time.Millisecond)
	v, ok := s.Get("ephemeral")
	if !ok {
		t.Fatal("expected hit before TTL expires")
	}
	if v != "data" {
		t.Fatalf("expected 'data', got %v", v)
	}

	time.Sleep(60 * time.Millisecond)
	_, ok = s.Get("ephemeral")
	if ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestMemoryStore_StructValues(t *testing.T) {
	type session struct {
		ID     string
		Status string
	}
	s := NewMemoryStore()
	defer s.Close()

	orig := &session{ID: "s1", Status: "active"}
	s.Set("session:agent1", orig, 10*time.Second)

	v, ok := s.Get("session:agent1")
	if !ok {
		t.Fatal("expected hit")
	}
	got, ok := v.(*session)
	if !ok || got.ID != "s1" || got.Status != "active" {
		t.Fatalf("unexpected value: %+v", v)
	}
}

func TestMemoryStore_NilValue(t *testing.T) {
	s := NewMemoryStore()
	defer s.Close()

	// Storing nil should be retrievable as a cache hit.
	s.Set("nil-val", nil, 10*time.Second)
	v, ok := s.Get("nil-val")
	if !ok {
		t.Fatal("expected hit for nil value")
	}
	if v != nil {
		t.Fatalf("expected nil, got %v", v)
	}
}
