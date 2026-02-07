package bridge

import (
	"sync"
	"testing"
	"time"
)

func TestCache_SetAndGet(t *testing.T) {
	c := NewCache()

	c.Set("key1", "value1", 5*time.Second)

	val, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected key1 to exist")
	}
	if val != "value1" {
		t.Fatalf("expected value1, got %v", val)
	}
}

func TestCache_GetMissingKey(t *testing.T) {
	c := NewCache()

	_, ok := c.Get("nonexistent")
	if ok {
		t.Fatal("expected missing key to return false")
	}
}

func TestCache_Expiration(t *testing.T) {
	c := NewCache()

	c.Set("expire-me", "data", 10*time.Millisecond)

	// Should exist immediately.
	if _, ok := c.Get("expire-me"); !ok {
		t.Fatal("expected key to exist before expiration")
	}

	// Wait for expiry.
	time.Sleep(20 * time.Millisecond)

	_, ok := c.Get("expire-me")
	if ok {
		t.Fatal("expected key to be expired")
	}
}

func TestCache_Invalidate(t *testing.T) {
	c := NewCache()

	c.Set("key1", "val1", 5*time.Second)
	c.Set("key2", "val2", 5*time.Second)

	c.Invalidate("key1")

	if _, ok := c.Get("key1"); ok {
		t.Fatal("expected key1 to be invalidated")
	}
	if _, ok := c.Get("key2"); !ok {
		t.Fatal("expected key2 to still exist")
	}
}

func TestCache_InvalidateAll(t *testing.T) {
	c := NewCache()

	c.Set("a", 1, 5*time.Second)
	c.Set("b", 2, 5*time.Second)
	c.Set("c", 3, 5*time.Second)

	c.InvalidateAll()

	for _, key := range []string{"a", "b", "c"} {
		if _, ok := c.Get(key); ok {
			t.Fatalf("expected key %s to be invalidated", key)
		}
	}
}

func TestCache_OverwriteKey(t *testing.T) {
	c := NewCache()

	c.Set("key", "old", 5*time.Second)
	c.Set("key", "new", 5*time.Second)

	val, ok := c.Get("key")
	if !ok {
		t.Fatal("expected key to exist")
	}
	if val != "new" {
		t.Fatalf("expected new, got %v", val)
	}
}

func TestCache_ConcurrentAccess(t *testing.T) {
	c := NewCache()
	var wg sync.WaitGroup

	// Concurrent writes.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "key"
			c.Set(key, n, 5*time.Second)
		}(i)
	}

	// Concurrent reads.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Get("key")
		}()
	}

	wg.Wait()

	// After all writes, key should exist with some value.
	if _, ok := c.Get("key"); !ok {
		t.Fatal("expected key to exist after concurrent writes")
	}
}
