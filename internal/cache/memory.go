package cache

import (
	"sync"
	"time"
)

// memoryEntry holds a cached value and its expiration time.
type memoryEntry struct {
	data      any
	expiresAt time.Time
}

// MemoryStore is a simple in-memory TTL cache backed by sync.Map.
// Expired entries are lazily deleted on access.
type MemoryStore struct {
	entries sync.Map
}

// NewMemoryStore creates a new empty in-memory cache.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Get retrieves a cached value. Expired entries are lazily deleted.
func (m *MemoryStore) Get(key string) (any, bool) {
	raw, ok := m.entries.Load(key)
	if !ok {
		return nil, false
	}
	entry := raw.(*memoryEntry)
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		m.entries.Delete(key)
		return nil, false
	}
	return entry.data, true
}

// Set stores a value with the given TTL. A zero or negative TTL means no
// expiration.
func (m *MemoryStore) Set(key string, value any, ttl time.Duration) {
	var exp time.Time
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	m.entries.Store(key, &memoryEntry{
		data:      value,
		expiresAt: exp,
	})
}

// Invalidate removes a single key.
func (m *MemoryStore) Invalidate(key string) {
	m.entries.Delete(key)
}

// InvalidateAll removes all entries.
func (m *MemoryStore) InvalidateAll() {
	m.entries.Range(func(key, _ any) bool {
		m.entries.Delete(key)
		return true
	})
}

// Len returns the number of entries (including expired ones that have not
// been lazily cleaned yet).
func (m *MemoryStore) Len() int {
	count := 0
	m.entries.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// Close is a no-op for MemoryStore.
func (m *MemoryStore) Close() error { return nil }
