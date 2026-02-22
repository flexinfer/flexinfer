// Package cache provides a pluggable caching layer with in-memory and Redis
// backends. The Store interface is the common abstraction used throughout the
// HUD and agent bridge for TTL-based caching.
package cache

import "time"

// Store is the common caching interface. Implementations must be safe for
// concurrent use from multiple goroutines.
type Store interface {
	// Get retrieves a cached value by key. Returns (value, true) on hit,
	// (nil, false) on miss or expiration.
	Get(key string) (any, bool)

	// Set stores a value with the given TTL. A zero or negative TTL is
	// treated as "no expiration" by memory stores and a very long TTL by
	// Redis stores.
	Set(key string, value any, ttl time.Duration)

	// Invalidate removes a single key.
	Invalidate(key string)

	// InvalidateAll removes all entries.
	InvalidateAll()

	// Len returns the approximate number of entries.
	Len() int

	// Close releases any resources held by the store.
	Close() error
}
