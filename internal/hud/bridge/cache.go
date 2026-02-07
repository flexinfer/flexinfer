package bridge

import (
	"sync"
	"time"
)

// cacheEntry holds a cached value and its expiration time.
type cacheEntry struct {
	data      any
	expiresAt time.Time
}

// Cache is a simple in-memory TTL cache. It is safe for concurrent use.
type Cache struct {
	entries sync.Map
}

// NewCache creates a new empty Cache.
func NewCache() *Cache {
	return &Cache{}
}

// Get retrieves a cached value by key. It returns the value and true if the
// entry exists and has not expired. Expired entries are lazily deleted.
func (c *Cache) Get(key string) (any, bool) {
	raw, ok := c.entries.Load(key)
	if !ok {
		return nil, false
	}
	entry := raw.(*cacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.entries.Delete(key)
		return nil, false
	}
	return entry.data, true
}

// Set stores a value in the cache with the given TTL.
func (c *Cache) Set(key string, data any, ttl time.Duration) {
	c.entries.Store(key, &cacheEntry{
		data:      data,
		expiresAt: time.Now().Add(ttl),
	})
}

// Invalidate removes a single key from the cache.
func (c *Cache) Invalidate(key string) {
	c.entries.Delete(key)
}

// InvalidateAll removes all entries from the cache.
func (c *Cache) InvalidateAll() {
	c.entries.Range(func(key, _ any) bool {
		c.entries.Delete(key)
		return true
	})
}
