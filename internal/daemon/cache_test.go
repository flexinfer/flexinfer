package daemon

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDefaultCacheConfig(t *testing.T) {
	cfg := DefaultCacheConfig()

	if cfg.Enabled {
		t.Error("cache should be disabled by default")
	}
	if cfg.DefaultTTLSeconds != 60 {
		t.Errorf("DefaultTTLSeconds = %d, want 60", cfg.DefaultTTLSeconds)
	}
	if cfg.MaxSizeMB != 100 {
		t.Errorf("MaxSizeMB = %d, want 100", cfg.MaxSizeMB)
	}
	if len(cfg.ToolTTLs) == 0 {
		t.Error("ToolTTLs should have default entries")
	}
}

func TestNewResponseCache_Disabled(t *testing.T) {
	cfg := CacheConfig{Enabled: false}
	cache := NewResponseCache(cfg)
	if cache != nil {
		t.Error("NewResponseCache should return nil when disabled")
	}
}

func TestNewResponseCache_Enabled(t *testing.T) {
	cfg := CacheConfig{
		Enabled:           true,
		DefaultTTLSeconds: 30,
		MaxSizeMB:         50,
	}
	cache := NewResponseCache(cfg)
	if cache == nil {
		t.Fatal("NewResponseCache should return non-nil when enabled")
	}
	if cache.defaultTTL != 30*time.Second {
		t.Errorf("defaultTTL = %v, want 30s", cache.defaultTTL)
	}
	if cache.maxSize != 50*1024*1024 {
		t.Errorf("maxSize = %d, want 50MB", cache.maxSize)
	}
}

func TestNewResponseCache_DefaultValues(t *testing.T) {
	cfg := CacheConfig{
		Enabled:           true,
		DefaultTTLSeconds: 0,  // Should use default
		MaxSizeMB:         -1, // Should use default
	}
	cache := NewResponseCache(cfg)
	if cache == nil {
		t.Fatal("NewResponseCache should return non-nil")
	}
	if cache.defaultTTL != 60*time.Second {
		t.Errorf("defaultTTL = %v, want 60s", cache.defaultTTL)
	}
	if cache.maxSize != 100*1024*1024 {
		t.Errorf("maxSize = %d, want 100MB", cache.maxSize)
	}
}

func TestResponseCache_IsCacheable(t *testing.T) {
	cfg := CacheConfig{Enabled: true, DefaultTTLSeconds: 60}
	cache := NewResponseCache(cfg)

	tests := []struct {
		server, tool string
		want         bool
	}{
		{"prometheus", "query", true},
		{"prometheus", "query_range", true},
		{"github", "list_repos", true},
		{"github", "get_repo", true},
		{"docker", "ps", true},
		{"grafana", "search", true},
		{"unknown", "unknown_tool", false},
		{"git", "commit", false},
	}

	for _, tt := range tests {
		t.Run(tt.server+"__"+tt.tool, func(t *testing.T) {
			got := cache.IsCacheable(tt.server, tt.tool)
			if got != tt.want {
				t.Errorf("IsCacheable(%q, %q) = %v, want %v", tt.server, tt.tool, got, tt.want)
			}
		})
	}
}

func TestResponseCache_IsCacheable_NilCache(t *testing.T) {
	var cache *ResponseCache
	if cache.IsCacheable("prometheus", "query") {
		t.Error("nil cache should return false for IsCacheable")
	}
}

func TestResponseCache_Key(t *testing.T) {
	cfg := CacheConfig{Enabled: true}
	cache := NewResponseCache(cfg)

	params1 := json.RawMessage(`{"query": "up"}`)
	params2 := json.RawMessage(`{"query": "down"}`)

	key1 := cache.Key("prometheus", "query", params1)
	key2 := cache.Key("prometheus", "query", params2)
	key3 := cache.Key("prometheus", "query", params1) // Same as key1

	if key1 == key2 {
		t.Error("different params should produce different keys")
	}
	if key1 != key3 {
		t.Error("same params should produce same keys")
	}

	// Keys should be hex-encoded SHA256 (64 chars)
	if len(key1) != 64 {
		t.Errorf("key length = %d, want 64", len(key1))
	}
}

func TestResponseCache_GetSet(t *testing.T) {
	cfg := CacheConfig{Enabled: true, DefaultTTLSeconds: 60}
	cache := NewResponseCache(cfg)

	params := json.RawMessage(`{"query": "up"}`)
	key := cache.Key("prometheus", "query", params)
	response := json.RawMessage(`{"result": "ok"}`)

	// Get before set should miss
	if _, ok := cache.Get(key); ok {
		t.Error("Get should return false for missing key")
	}

	// Set and get
	cache.Set(key, response, "prometheus", "query")
	got, ok := cache.Get(key)
	if !ok {
		t.Error("Get should return true after Set")
	}
	if string(got) != string(response) {
		t.Errorf("Got = %s, want %s", got, response)
	}
}

func TestResponseCache_Get_NilCache(t *testing.T) {
	var cache *ResponseCache
	_, ok := cache.Get("somekey")
	if ok {
		t.Error("nil cache should return false for Get")
	}
}

func TestResponseCache_Set_NilCache(t *testing.T) {
	var cache *ResponseCache
	// Should not panic
	cache.Set("key", json.RawMessage(`{}`), "server", "tool")
}

func TestResponseCache_Expiration(t *testing.T) {
	cfg := CacheConfig{
		Enabled:           true,
		DefaultTTLSeconds: 1, // 1 second TTL for testing
	}
	cache := NewResponseCache(cfg)

	params := json.RawMessage(`{}`)
	key := cache.Key("test", "tool", params)
	response := json.RawMessage(`{"result": "ok"}`)

	cache.Set(key, response, "test", "tool")

	// Should be available immediately
	if _, ok := cache.Get(key); !ok {
		t.Error("entry should be available immediately after set")
	}

	// Wait for expiration
	time.Sleep(1100 * time.Millisecond)

	// Should be expired
	if _, ok := cache.Get(key); ok {
		t.Error("entry should be expired after TTL")
	}
}

func TestResponseCache_LRUEviction(t *testing.T) {
	cfg := CacheConfig{
		Enabled:           true,
		DefaultTTLSeconds: 60,
		MaxSizeMB:         0, // Will use default 100MB, but we'll override
	}
	cache := NewResponseCache(cfg)
	// Override max size to a small value for testing
	cache.maxSize = 100 // 100 bytes

	// Add entries that exceed the limit
	for i := 0; i < 10; i++ {
		params := json.RawMessage(`{"i": ` + string(rune('0'+i)) + `}`)
		key := cache.Key("test", "tool", params)
		response := json.RawMessage(`{"result": "this is a response that takes some bytes"}`)
		cache.Set(key, response, "test", "tool")
	}

	// Cache should have evicted old entries to stay under limit
	stats := cache.Stats()
	if stats.SizeBytes > cache.maxSize {
		t.Errorf("cache size %d exceeds max %d", stats.SizeBytes, cache.maxSize)
	}
}

func TestResponseCache_Clear(t *testing.T) {
	cfg := CacheConfig{Enabled: true, DefaultTTLSeconds: 60}
	cache := NewResponseCache(cfg)

	// Add some entries
	for i := 0; i < 5; i++ {
		params := json.RawMessage(`{"i": ` + string(rune('0'+i)) + `}`)
		key := cache.Key("test", "tool", params)
		cache.Set(key, json.RawMessage(`{}`), "test", "tool")
	}

	stats := cache.Stats()
	if stats.Entries != 5 {
		t.Errorf("Entries = %d, want 5", stats.Entries)
	}

	cache.Clear()

	stats = cache.Stats()
	if stats.Entries != 0 {
		t.Errorf("Entries after clear = %d, want 0", stats.Entries)
	}
	if stats.SizeBytes != 0 {
		t.Errorf("SizeBytes after clear = %d, want 0", stats.SizeBytes)
	}
}

func TestResponseCache_Clear_NilCache(t *testing.T) {
	var cache *ResponseCache
	// Should not panic
	cache.Clear()
}

func TestResponseCache_ClearServer(t *testing.T) {
	cfg := CacheConfig{Enabled: true, DefaultTTLSeconds: 60}
	cache := NewResponseCache(cfg)

	// Add some entries
	for i := 0; i < 5; i++ {
		params := json.RawMessage(`{"i": ` + string(rune('0'+i)) + `}`)
		key := cache.Key("test", "tool", params)
		cache.Set(key, json.RawMessage(`{}`), "test", "tool")
	}

	cache.ClearServer("test")

	stats := cache.Stats()
	if stats.Entries != 0 {
		t.Errorf("Entries after ClearServer = %d, want 0", stats.Entries)
	}
}

func TestResponseCache_ClearServer_NilCache(t *testing.T) {
	var cache *ResponseCache
	// Should not panic
	cache.ClearServer("test")
}

func TestResponseCache_Stats(t *testing.T) {
	cfg := CacheConfig{Enabled: true, DefaultTTLSeconds: 60, MaxSizeMB: 50}
	cache := NewResponseCache(cfg)

	params := json.RawMessage(`{"query": "up"}`)
	key := cache.Key("prometheus", "query", params)
	response := json.RawMessage(`{"result": "ok"}`)

	cache.Set(key, response, "prometheus", "query")

	// Access multiple times to increment hits
	cache.Get(key)
	cache.Get(key)
	cache.Get(key)

	stats := cache.Stats()
	if stats.Entries != 1 {
		t.Errorf("Entries = %d, want 1", stats.Entries)
	}
	if stats.SizeBytes != int64(len(response)) {
		t.Errorf("SizeBytes = %d, want %d", stats.SizeBytes, len(response))
	}
	if stats.MaxBytes != 50*1024*1024 {
		t.Errorf("MaxBytes = %d, want 50MB", stats.MaxBytes)
	}
	if stats.TotalHits != 3 {
		t.Errorf("TotalHits = %d, want 3", stats.TotalHits)
	}
}

func TestResponseCache_Stats_NilCache(t *testing.T) {
	var cache *ResponseCache
	stats := cache.Stats()
	if stats.Entries != 0 || stats.SizeBytes != 0 {
		t.Error("nil cache should return zero stats")
	}
}

func TestResponseCache_Prune(t *testing.T) {
	cfg := CacheConfig{
		Enabled:           true,
		DefaultTTLSeconds: 1, // 1 second TTL
	}
	cache := NewResponseCache(cfg)

	// Add entries
	for i := 0; i < 5; i++ {
		params := json.RawMessage(`{"i": ` + string(rune('0'+i)) + `}`)
		key := cache.Key("test", "tool", params)
		cache.Set(key, json.RawMessage(`{}`), "test", "tool")
	}

	// Prune immediately - nothing should be expired
	pruned := cache.Prune()
	if pruned != 0 {
		t.Errorf("Prune returned %d, want 0", pruned)
	}

	// Wait for expiration
	time.Sleep(1100 * time.Millisecond)

	// Prune should remove all entries
	pruned = cache.Prune()
	if pruned != 5 {
		t.Errorf("Prune returned %d, want 5", pruned)
	}

	stats := cache.Stats()
	if stats.Entries != 0 {
		t.Errorf("Entries after prune = %d, want 0", stats.Entries)
	}
}

func TestResponseCache_Prune_NilCache(t *testing.T) {
	var cache *ResponseCache
	pruned := cache.Prune()
	if pruned != 0 {
		t.Errorf("nil cache Prune returned %d, want 0", pruned)
	}
}

func TestResponseCache_UpdateExisting(t *testing.T) {
	cfg := CacheConfig{Enabled: true, DefaultTTLSeconds: 60}
	cache := NewResponseCache(cfg)

	params := json.RawMessage(`{}`)
	key := cache.Key("test", "tool", params)

	// Set initial value
	cache.Set(key, json.RawMessage(`{"v": 1}`), "test", "tool")

	// Update with new value
	cache.Set(key, json.RawMessage(`{"v": 2}`), "test", "tool")

	got, ok := cache.Get(key)
	if !ok {
		t.Fatal("entry should exist")
	}
	if string(got) != `{"v": 2}` {
		t.Errorf("Got = %s, want {\"v\": 2}", got)
	}

	// Should only have one entry
	stats := cache.Stats()
	if stats.Entries != 1 {
		t.Errorf("Entries = %d, want 1", stats.Entries)
	}
}

func TestResponseCache_CustomToolTTL(t *testing.T) {
	cfg := CacheConfig{
		Enabled:           true,
		DefaultTTLSeconds: 60,
		ToolTTLs: map[string]int{
			"custom__tool": 2,
		},
	}
	cache := NewResponseCache(cfg)

	// Verify custom TTL is set
	if ttl := cache.toolTTLs["custom__tool"]; ttl != 2*time.Second {
		t.Errorf("custom tool TTL = %v, want 2s", ttl)
	}

	// Verify default tools still have their TTLs
	if ttl := cache.toolTTLs["prometheus__query"]; ttl != 30*time.Second {
		t.Errorf("prometheus__query TTL = %v, want 30s", ttl)
	}
}

func TestResponseCache_Concurrent(t *testing.T) {
	cfg := CacheConfig{Enabled: true, DefaultTTLSeconds: 60}
	cache := NewResponseCache(cfg)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				params := json.RawMessage(`{}`)
				key := cache.Key("test", "tool", params)
				response := json.RawMessage(`{"id": ` + string(rune('0'+id)) + `}`)
				cache.Set(key, response, "test", "tool")
				cache.Get(key)
				cache.IsCacheable("test", "tool")
				cache.Stats()
			}
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestDefaultToolTTLs(t *testing.T) {
	ttls := defaultToolTTLs()

	// Check some expected entries exist
	expected := map[string]int{
		"prometheus__query":  30,
		"github__list_repos": 300,
		"docker__ps":         15,
		"grafana__search":    60,
		"loki__query":        30,
	}

	for tool, want := range expected {
		if got, ok := ttls[tool]; !ok {
			t.Errorf("missing TTL for %s", tool)
		} else if got != want {
			t.Errorf("TTL for %s = %d, want %d", tool, got, want)
		}
	}
}

func TestCacheableTools(t *testing.T) {
	tools := cacheableTools()

	// All tools with TTLs should be cacheable
	for tool := range defaultToolTTLs() {
		if !tools[tool] {
			t.Errorf("%s should be cacheable", tool)
		}
	}
}
