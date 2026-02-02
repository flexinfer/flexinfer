// Package daemon provides the main Loom daemon orchestrator.
package daemon

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// ResponseCache provides TTL-based caching for read-only MCP tool responses.
type ResponseCache struct {
	mu         sync.RWMutex
	entries    map[string]*cacheEntry
	order      *list.List // LRU order tracking
	orderMap   map[string]*list.Element
	maxSize    int64 // max size in bytes
	curSize    int64 // current size in bytes
	defaultTTL time.Duration
	toolTTLs   map[string]time.Duration // server__tool -> TTL
	cacheable  map[string]bool          // server__tool -> cacheable
}

// cacheEntry represents a cached response.
type cacheEntry struct {
	Key       string
	Response  json.RawMessage
	Size      int64
	CreatedAt time.Time
	ExpiresAt time.Time
	Hits      int64
}

// CacheConfig holds response cache configuration.
type CacheConfig struct {
	// Enabled controls whether response caching is active
	Enabled bool `yaml:"enabled"`

	// DefaultTTLSeconds is the default cache TTL in seconds
	DefaultTTLSeconds int `yaml:"default_ttl_seconds"`

	// MaxSizeMB is the maximum cache size in megabytes
	MaxSizeMB int `yaml:"max_size_mb"`

	// ToolTTLs maps server__tool to TTL in seconds
	ToolTTLs map[string]int `yaml:"tool_ttls,omitempty"`
}

// DefaultCacheConfig returns sensible cache defaults.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		Enabled:           false, // Opt-in by default
		DefaultTTLSeconds: 60,
		MaxSizeMB:         100,
		ToolTTLs:          defaultToolTTLs(),
	}
}

// defaultToolTTLs returns the default per-tool TTLs in seconds.
func defaultToolTTLs() map[string]int {
	return map[string]int{
		// Prometheus - fast-changing metrics
		"prometheus__query":        30,
		"prometheus__query_range":  30,
		"prometheus__list_metrics": 60,
		"prometheus__list_labels":  60,
		"prometheus__label_values": 60,
		"prometheus__list_targets": 60,
		"prometheus__list_alerts":  30,
		"prometheus__list_rules":   60,
		"prometheus__runtime_info": 300,

		// GitHub - slower-changing data
		"github__list_repos":        300,
		"github__get_repo":          300,
		"github__list_issues":       60,
		"github__get_issue":         60,
		"github__list_prs":          60,
		"github__get_pr":            60,
		"github__list_commits":      60,
		"github__get_file_contents": 300,
		"github__search_repos":      60,
		"github__search_code":       60,

		// Loki - log queries
		"loki__query":           30,
		"loki__query_range":     30,
		"loki__labels":          60,
		"loki__label_values":    60,
		"loki__series":          30,
		"loki__stats":           60,
		"loki__index_stats":     60,
		"loki__detected_fields": 30,
		"loki__ready":           60,

		// Docker - container state
		"docker__version": 300,
		"docker__info":    60,
		"docker__ps":      15,
		"docker__images":  60,
		"docker__inspect": 30,

		// Grafana - dashboard queries
		"grafana__search":           60,
		"grafana__get_dashboard":    60,
		"grafana__list_datasources": 300,
		"grafana__get_datasource":   300,
		"grafana__list_alerts":      30,
		"grafana__list_folders":     300,
	}
}

// cacheableTools returns the set of tools that are safe to cache.
func cacheableTools() map[string]bool {
	tools := make(map[string]bool)
	for tool := range defaultToolTTLs() {
		tools[tool] = true
	}
	return tools
}

// NewResponseCache creates a new response cache with the given configuration.
func NewResponseCache(cfg CacheConfig) *ResponseCache {
	if !cfg.Enabled {
		return nil
	}

	defaultTTL := time.Duration(cfg.DefaultTTLSeconds) * time.Second
	if defaultTTL <= 0 {
		defaultTTL = 60 * time.Second
	}

	maxSize := int64(cfg.MaxSizeMB) * 1024 * 1024
	if maxSize <= 0 {
		maxSize = 100 * 1024 * 1024 // 100MB default
	}

	toolTTLs := make(map[string]time.Duration)
	for tool, seconds := range cfg.ToolTTLs {
		toolTTLs[tool] = time.Duration(seconds) * time.Second
	}

	// Add defaults for tools not explicitly configured
	for tool, seconds := range defaultToolTTLs() {
		if _, ok := toolTTLs[tool]; !ok {
			toolTTLs[tool] = time.Duration(seconds) * time.Second
		}
	}

	return &ResponseCache{
		entries:    make(map[string]*cacheEntry),
		order:      list.New(),
		orderMap:   make(map[string]*list.Element),
		maxSize:    maxSize,
		defaultTTL: defaultTTL,
		toolTTLs:   toolTTLs,
		cacheable:  cacheableTools(),
	}
}

// IsCacheable returns true if the given server/tool combination is cacheable.
func (c *ResponseCache) IsCacheable(server, tool string) bool {
	if c == nil {
		return false
	}
	key := server + "__" + tool
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cacheable[key]
}

// Key generates a cache key from server, tool, and params.
func (c *ResponseCache) Key(server, tool string, params json.RawMessage) string {
	h := sha256.New()
	h.Write([]byte(server))
	h.Write([]byte("__"))
	h.Write([]byte(tool))
	h.Write([]byte("__"))
	h.Write(params)
	return hex.EncodeToString(h.Sum(nil))
}

// Get retrieves a cached response if it exists and is not expired.
func (c *ResponseCache) Get(key string) (json.RawMessage, bool) {
	if c == nil {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	// Check expiration
	if time.Now().After(entry.ExpiresAt) {
		c.removeEntryLocked(key)
		return nil, false
	}

	// Update LRU order
	if elem, ok := c.orderMap[key]; ok {
		c.order.MoveToFront(elem)
	}

	entry.Hits++
	return entry.Response, true
}

// Set stores a response in the cache.
func (c *ResponseCache) Set(key string, response json.RawMessage, server, tool string) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Calculate size
	size := int64(len(response))

	// Get TTL for this tool
	ttl := c.defaultTTL
	toolKey := server + "__" + tool
	if t, ok := c.toolTTLs[toolKey]; ok {
		ttl = t
	}

	now := time.Now()
	entry := &cacheEntry{
		Key:       key,
		Response:  response,
		Size:      size,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		Hits:      0,
	}

	// Remove old entry if exists
	if _, ok := c.entries[key]; ok {
		c.removeEntryLocked(key)
	}

	// Evict entries if necessary to make room
	for c.curSize+size > c.maxSize && c.order.Len() > 0 {
		c.evictOldestLocked()
	}

	// Add new entry
	c.entries[key] = entry
	c.curSize += size
	elem := c.order.PushFront(key)
	c.orderMap[key] = elem
}

// Clear removes all entries from the cache.
func (c *ResponseCache) Clear() {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*cacheEntry)
	c.order = list.New()
	c.orderMap = make(map[string]*list.Element)
	c.curSize = 0
}

// ClearServer removes all entries for a specific server.
func (c *ResponseCache) ClearServer(server string) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// We need to iterate and find entries for this server
	// Since cache keys are sha256 hashes, we can't easily filter by server
	// For now, we'll just clear the entire cache when clearing by server
	// A more sophisticated approach would store server info in the entry
	c.entries = make(map[string]*cacheEntry)
	c.order = list.New()
	c.orderMap = make(map[string]*list.Element)
	c.curSize = 0
}

// Stats returns cache statistics.
func (c *ResponseCache) Stats() CacheStats {
	if c == nil {
		return CacheStats{}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	var totalHits int64
	for _, entry := range c.entries {
		totalHits += entry.Hits
	}

	return CacheStats{
		Entries:   len(c.entries),
		SizeBytes: c.curSize,
		MaxBytes:  c.maxSize,
		TotalHits: totalHits,
	}
}

// CacheStats holds cache statistics.
type CacheStats struct {
	Entries   int   `json:"entries"`
	SizeBytes int64 `json:"size_bytes"`
	MaxBytes  int64 `json:"max_bytes"`
	TotalHits int64 `json:"total_hits"`
}

// removeEntryLocked removes an entry (caller must hold lock).
func (c *ResponseCache) removeEntryLocked(key string) {
	entry, ok := c.entries[key]
	if !ok {
		return
	}

	delete(c.entries, key)
	c.curSize -= entry.Size

	if elem, ok := c.orderMap[key]; ok {
		c.order.Remove(elem)
		delete(c.orderMap, key)
	}
}

// evictOldestLocked removes the oldest (least recently used) entry.
func (c *ResponseCache) evictOldestLocked() {
	elem := c.order.Back()
	if elem == nil {
		return
	}

	key := elem.Value.(string)
	c.removeEntryLocked(key)
}

// Prune removes expired entries from the cache.
func (c *ResponseCache) Prune() int {
	if c == nil {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	var pruned int

	for key, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			c.removeEntryLocked(key)
			pruned++
		}
	}

	return pruned
}
