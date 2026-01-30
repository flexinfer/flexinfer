package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// =============================================================================
// FileConfig Tests
// =============================================================================

func TestDefaultFileConfig(t *testing.T) {
	cfg := DefaultFileConfig()

	// Hub defaults
	if cfg.Hub.URL != "wss://mcp.flexinfer.ai/ws" {
		t.Errorf("Hub.URL = %q, want default", cfg.Hub.URL)
	}
	if !cfg.Hub.Enabled {
		t.Error("Hub.Enabled should be true by default")
	}
	if cfg.Hub.Profile != "codex" {
		t.Errorf("Hub.Profile = %q, want codex", cfg.Hub.Profile)
	}
	if cfg.Hub.ReconnectIntervalSeconds != 5 {
		t.Errorf("Hub.ReconnectIntervalSeconds = %d, want 5", cfg.Hub.ReconnectIntervalSeconds)
	}
	if cfg.Hub.PingIntervalSeconds != 30 {
		t.Errorf("Hub.PingIntervalSeconds = %d, want 30", cfg.Hub.PingIntervalSeconds)
	}
	if cfg.Hub.MaxRetries != 3 {
		t.Errorf("Hub.MaxRetries = %d, want 3", cfg.Hub.MaxRetries)
	}

	// Resources defaults
	if cfg.Resources.MaxProcesses != 0 {
		t.Errorf("Resources.MaxProcesses = %d, want 0 (unlimited)", cfg.Resources.MaxProcesses)
	}
	if cfg.Resources.IdleTimeoutMinutes != 5 {
		t.Errorf("Resources.IdleTimeoutMinutes = %d, want 5", cfg.Resources.IdleTimeoutMinutes)
	}
	if cfg.Resources.ManifestTTLMinutes != 5 {
		t.Errorf("Resources.ManifestTTLMinutes = %d, want 5", cfg.Resources.ManifestTTLMinutes)
	}

	// Context defaults
	if cfg.Context.ActiveProfile != "full" {
		t.Errorf("Context.ActiveProfile = %q, want full", cfg.Context.ActiveProfile)
	}
	if cfg.Context.AutoDetect {
		t.Error("Context.AutoDetect should be false by default")
	}
	if cfg.Context.EnrichDescriptions {
		t.Error("Context.EnrichDescriptions should be false by default")
	}

	// Debug
	if cfg.Debug {
		t.Error("Debug should be false by default")
	}
}

func TestResourceConfig_GetIdleTimeout_Default(t *testing.T) {
	cfg := ResourceConfig{}
	timeout := cfg.GetIdleTimeout()

	if timeout != 5*time.Minute {
		t.Errorf("GetIdleTimeout() = %v, want 5m", timeout)
	}
}

func TestResourceConfig_GetIdleTimeout_Custom(t *testing.T) {
	cfg := ResourceConfig{IdleTimeoutMinutes: 10}
	timeout := cfg.GetIdleTimeout()

	if timeout != 10*time.Minute {
		t.Errorf("GetIdleTimeout() = %v, want 10m", timeout)
	}
}

func TestResourceConfig_GetIdleTimeout_Zero(t *testing.T) {
	cfg := ResourceConfig{IdleTimeoutMinutes: 0}
	timeout := cfg.GetIdleTimeout()

	// Zero should use default
	if timeout != 5*time.Minute {
		t.Errorf("GetIdleTimeout() = %v, want 5m (default)", timeout)
	}
}

func TestResourceConfig_GetIdleTimeout_Negative(t *testing.T) {
	cfg := ResourceConfig{IdleTimeoutMinutes: -1}
	timeout := cfg.GetIdleTimeout()

	// Negative should use default
	if timeout != 5*time.Minute {
		t.Errorf("GetIdleTimeout() = %v, want 5m (default)", timeout)
	}
}

func TestResourceConfig_GetManifestTTL_Default(t *testing.T) {
	cfg := ResourceConfig{}
	ttl := cfg.GetManifestTTL()

	if ttl != 5*time.Minute {
		t.Errorf("GetManifestTTL() = %v, want 5m", ttl)
	}
}

func TestResourceConfig_GetManifestTTL_Custom(t *testing.T) {
	cfg := ResourceConfig{ManifestTTLMinutes: 15}
	ttl := cfg.GetManifestTTL()

	if ttl != 15*time.Minute {
		t.Errorf("GetManifestTTL() = %v, want 15m", ttl)
	}
}

// =============================================================================
// Config Tests
// =============================================================================

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Target != "codex" {
		t.Errorf("Target = %q, want codex", cfg.Target)
	}
	if cfg.HubURL != "wss://mcp.flexinfer.ai/ws" {
		t.Errorf("HubURL = %q, want default", cfg.HubURL)
	}
	if !cfg.HubFallback {
		t.Error("HubFallback should be true by default")
	}
	if cfg.Debug {
		t.Error("Debug should be false by default")
	}
	if cfg.SocketPath == "" {
		t.Error("SocketPath should not be empty")
	}
}

// =============================================================================
// ManifestManager Tests
// =============================================================================

func TestNewManifestManager(t *testing.T) {
	m := NewManifestManager()

	if m == nil {
		t.Fatal("NewManifestManager returned nil")
	}
	if m.manifest == nil {
		t.Error("manifest should be initialized")
	}
	if m.manifest.Version != 1 {
		t.Errorf("manifest.Version = %d, want 1", m.manifest.Version)
	}
	if m.manifest.Servers == nil {
		t.Error("manifest.Servers should be initialized")
	}
	if m.path == "" {
		t.Error("path should not be empty")
	}
}

func TestManifestManager_GetAllTools_Empty(t *testing.T) {
	m := NewManifestManager()

	tools := m.GetAllTools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestManifestManager_UpdateServerTools(t *testing.T) {
	m := NewManifestManager()

	tools := []mcp.Tool{
		{Name: "test-tool", Description: "A test tool"},
	}
	m.UpdateServerTools("test-server", tools)

	// Check it was added
	got, ok := m.GetServerTools("test-server")
	if !ok {
		t.Fatal("expected tools to be found")
	}
	if len(got) != 1 {
		t.Errorf("expected 1 tool, got %d", len(got))
	}
	if got[0].Name != "test-tool" {
		t.Errorf("tool name = %q, want test-tool", got[0].Name)
	}

	// Check dirty flag
	if !m.dirty {
		t.Error("manifest should be dirty after update")
	}
}

func TestManifestManager_GetAllTools(t *testing.T) {
	m := NewManifestManager()

	// Add tools to multiple servers
	m.UpdateServerTools("server1", []mcp.Tool{
		{Name: "tool1", Description: "Tool 1"},
		{Name: "tool2", Description: "Tool 2"},
	})
	m.UpdateServerTools("server2", []mcp.Tool{
		{Name: "tool3", Description: "Tool 3"},
	})

	tools := m.GetAllTools()
	if len(tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(tools))
	}
}

func TestManifestManager_GetServerTools_NotFound(t *testing.T) {
	m := NewManifestManager()

	_, ok := m.GetServerTools("nonexistent")
	if ok {
		t.Error("expected ok=false for nonexistent server")
	}
}

func TestManifestManager_RemoveServer(t *testing.T) {
	m := NewManifestManager()

	m.UpdateServerTools("test-server", []mcp.Tool{{Name: "tool1"}})
	m.RemoveServer("test-server")

	_, ok := m.GetServerTools("test-server")
	if ok {
		t.Error("server should be removed")
	}
}

func TestManifestManager_GetServerHash(t *testing.T) {
	m := NewManifestManager()

	// Empty hash for nonexistent server
	hash := m.GetServerHash("nonexistent")
	if hash != "" {
		t.Errorf("expected empty hash, got %q", hash)
	}

	// Add tools and check hash
	m.UpdateServerTools("test-server", []mcp.Tool{{Name: "tool1"}})
	hash = m.GetServerHash("test-server")
	if hash == "" {
		t.Error("expected non-empty hash")
	}

	// Same tools should produce same hash
	m.UpdateServerTools("test-server2", []mcp.Tool{{Name: "tool1"}})
	hash2 := m.GetServerHash("test-server2")
	if hash != hash2 {
		t.Error("same tools should produce same hash")
	}
}

func TestManifestManager_IsStale(t *testing.T) {
	m := NewManifestManager()

	// Nonexistent server is always stale
	if !m.IsStale("nonexistent", time.Hour) {
		t.Error("nonexistent server should be stale")
	}

	// Fresh server
	m.UpdateServerTools("test-server", []mcp.Tool{{Name: "tool1"}})
	if m.IsStale("test-server", time.Hour) {
		t.Error("just-updated server should not be stale")
	}

	// Use very short TTL to make it stale
	time.Sleep(10 * time.Millisecond)
	if !m.IsStale("test-server", time.Millisecond) {
		t.Error("server should be stale with very short TTL")
	}
}

func TestManifestManager_ServerCount(t *testing.T) {
	m := NewManifestManager()

	if m.ServerCount() != 0 {
		t.Errorf("expected 0, got %d", m.ServerCount())
	}

	m.UpdateServerTools("server1", []mcp.Tool{})
	m.UpdateServerTools("server2", []mcp.Tool{})

	if m.ServerCount() != 2 {
		t.Errorf("expected 2, got %d", m.ServerCount())
	}

	m.RemoveServer("server1")
	if m.ServerCount() != 1 {
		t.Errorf("expected 1 after remove, got %d", m.ServerCount())
	}
}

func TestManifestManager_LastUpdated(t *testing.T) {
	m := NewManifestManager()

	// Initially zero
	if !m.LastUpdated().IsZero() {
		t.Error("expected zero time initially")
	}

	before := time.Now()
	m.UpdateServerTools("test", []mcp.Tool{})
	after := time.Now()

	lastUpdated := m.LastUpdated()
	if lastUpdated.Before(before) || lastUpdated.After(after) {
		t.Error("LastUpdated should be between before and after")
	}
}

func TestManifestManager_SaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.yaml")

	// Create and save
	m := &ManifestManager{
		path: manifestPath,
		manifest: &ToolManifest{
			Version: 1,
			Servers: make(map[string]ServerManifest),
		},
	}

	m.UpdateServerTools("test-server", []mcp.Tool{
		{Name: "tool1", Description: "Test tool"},
	})

	if err := m.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load into new manager
	m2 := &ManifestManager{
		path: manifestPath,
		manifest: &ToolManifest{
			Version: 1,
			Servers: make(map[string]ServerManifest),
		},
	}

	if err := m2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	tools, ok := m2.GetServerTools("test-server")
	if !ok {
		t.Fatal("expected to find test-server")
	}
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "tool1" {
		t.Errorf("tool name = %q, want tool1", tools[0].Name)
	}
}

func TestManifestManager_Load_NonExistent(t *testing.T) {
	m := &ManifestManager{
		path: "/nonexistent/path/manifest.yaml",
		manifest: &ToolManifest{
			Version: 1,
			Servers: make(map[string]ServerManifest),
		},
	}

	// Should not error on non-existent file
	if err := m.Load(); err != nil {
		t.Errorf("Load should not error on non-existent file: %v", err)
	}
}

func TestManifestManager_Save_NotDirty(t *testing.T) {
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.yaml")

	m := &ManifestManager{
		path:  manifestPath,
		dirty: false,
		manifest: &ToolManifest{
			Version: 1,
			Servers: make(map[string]ServerManifest),
		},
	}

	// Save when not dirty should be no-op
	if err := m.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// File should not exist
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Error("file should not be created when not dirty")
	}
}

// =============================================================================
// hashTools Tests
// =============================================================================

func TestHashTools_Empty(t *testing.T) {
	hash := hashTools(nil)
	if hash != "" {
		t.Errorf("expected empty hash for nil tools, got %q", hash)
	}

	hash = hashTools([]mcp.Tool{})
	if hash != "" {
		t.Errorf("expected empty hash for empty tools, got %q", hash)
	}
}

func TestHashTools_Consistent(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "tool1", Description: "First tool"},
		{Name: "tool2", Description: "Second tool"},
	}

	hash1 := hashTools(tools)
	hash2 := hashTools(tools)

	if hash1 != hash2 {
		t.Errorf("hash not consistent: %q != %q", hash1, hash2)
	}
}

func TestHashTools_DifferentContent(t *testing.T) {
	tools1 := []mcp.Tool{{Name: "tool1"}}
	tools2 := []mcp.Tool{{Name: "tool2"}}

	hash1 := hashTools(tools1)
	hash2 := hashTools(tools2)

	if hash1 == hash2 {
		t.Error("different tools should produce different hashes")
	}
}

func TestHashTools_Length(t *testing.T) {
	tools := []mcp.Tool{{Name: "tool1"}}
	hash := hashTools(tools)

	// Should be 16 hex characters (8 bytes)
	if len(hash) != 16 {
		t.Errorf("hash length = %d, want 16", len(hash))
	}
}

// =============================================================================
// ToolCache Tests
// =============================================================================

func TestToolCache_Empty(t *testing.T) {
	cache := &ToolCache{
		ttl: time.Minute,
	}

	cache.mu.RLock()
	tools := cache.tools
	cache.mu.RUnlock()

	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestToolCache_UpdateAndRead(t *testing.T) {
	cache := &ToolCache{
		ttl: time.Minute,
	}

	tools := []mcp.Tool{
		{Name: "test-tool", Description: "Test"},
	}

	cache.mu.Lock()
	cache.tools = tools
	cache.updatedAt = time.Now()
	cache.mu.Unlock()

	cache.mu.RLock()
	got := cache.tools
	cache.mu.RUnlock()

	if len(got) != 1 {
		t.Errorf("expected 1 tool, got %d", len(got))
	}
}

func TestToolCache_ConcurrentAccess(t *testing.T) {
	cache := &ToolCache{
		tools: []mcp.Tool{{Name: "initial"}},
		ttl:   time.Minute,
	}

	var wg sync.WaitGroup

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cache.mu.RLock()
				_ = len(cache.tools)
				cache.mu.RUnlock()
			}
		}()
	}

	// Concurrent writers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				cache.mu.Lock()
				cache.tools = []mcp.Tool{{Name: "updated"}}
				cache.updatedAt = time.Now()
				cache.mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
}

// =============================================================================
// callLock Tests
// =============================================================================

func TestCallLock_ReturnsLockForServer(t *testing.T) {
	d := &Daemon{}

	lock1 := d.callLock("server1")
	lock2 := d.callLock("server1")

	// Same server should return the same lock
	if lock1 != lock2 {
		t.Error("expected same lock for same server name")
	}
}

func TestCallLock_DifferentServers(t *testing.T) {
	d := &Daemon{}

	lock1 := d.callLock("server1")
	lock2 := d.callLock("server2")

	// Different servers should return different locks
	if lock1 == lock2 {
		t.Error("expected different locks for different servers")
	}
}

func TestCallLock_EmptyServerName(t *testing.T) {
	d := &Daemon{}

	lock := d.callLock("")

	// Empty server name should return a new lock each time
	if lock == nil {
		t.Error("expected non-nil lock for empty server name")
	}

	lock2 := d.callLock("")
	if lock == lock2 {
		t.Error("empty server name should return different locks")
	}
}

func TestCallLock_WhitespaceServerName(t *testing.T) {
	d := &Daemon{}

	lock := d.callLock("   ")

	// Whitespace-only should be treated like empty
	if lock == nil {
		t.Error("expected non-nil lock")
	}
}

func TestCallLock_ConcurrentAccess(t *testing.T) {
	d := &Daemon{}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			serverName := "server"
			if id%2 == 0 {
				serverName = "server1"
			} else {
				serverName = "server2"
			}
			lock := d.callLock(serverName)
			lock.Lock()
			// Simulate some work
			lock.Unlock()
		}(i)
	}
	wg.Wait()
}

// =============================================================================
// expandVarsWithRegistry Tests
// =============================================================================

func TestExpandVarsWithRegistry_HOME(t *testing.T) {
	home, _ := os.UserHomeDir()

	result := expandVarsWithRegistry("${HOME}/config", "/repo", nil)
	expected := home + "/config"

	if result != expected {
		t.Errorf("expandVarsWithRegistry() = %q, want %q", result, expected)
	}
}

func TestExpandVarsWithRegistry_Repo(t *testing.T) {
	result := expandVarsWithRegistry("${repo}/scripts", "/workspace/myproject", nil)
	expected := "/workspace/myproject/scripts"

	if result != expected {
		t.Errorf("expandVarsWithRegistry() = %q, want %q", result, expected)
	}
}

func TestExpandVarsWithRegistry_EmptyRepo(t *testing.T) {
	result := expandVarsWithRegistry("${repo}/scripts", "", nil)

	// When repoRoot is empty, ${repo} should not be replaced
	if result != "${repo}/scripts" {
		t.Errorf("expandVarsWithRegistry() = %q, want ${repo}/scripts", result)
	}
}

func TestExpandVarsWithRegistry_EnvVar(t *testing.T) {
	os.Setenv("TEST_VAR_EXPAND", "test-value")
	defer os.Unsetenv("TEST_VAR_EXPAND")

	result := expandVarsWithRegistry("prefix-${env:TEST_VAR_EXPAND}-suffix", "", nil)
	expected := "prefix-test-value-suffix"

	if result != expected {
		t.Errorf("expandVarsWithRegistry() = %q, want %q", result, expected)
	}
}

func TestExpandVarsWithRegistry_EnvVarMissing(t *testing.T) {
	os.Unsetenv("MISSING_VAR_12345")

	result := expandVarsWithRegistry("${env:MISSING_VAR_12345}", "", nil)

	// Missing env var should be replaced with empty string
	if result != "" {
		t.Errorf("expandVarsWithRegistry() = %q, want empty", result)
	}
}

func TestExpandVarsWithRegistry_EnvVarDefault(t *testing.T) {
	os.Unsetenv("MISSING_VAR_WITH_DEFAULT")

	result := expandVarsWithRegistry("${env:MISSING_VAR_WITH_DEFAULT:-default-value}", "", nil)
	expected := "default-value"

	if result != expected {
		t.Errorf("expandVarsWithRegistry() = %q, want %q", result, expected)
	}
}

func TestExpandVarsWithRegistry_EnvVarDefaultOverride(t *testing.T) {
	os.Setenv("VAR_WITH_DEFAULT", "actual-value")
	defer os.Unsetenv("VAR_WITH_DEFAULT")

	result := expandVarsWithRegistry("${env:VAR_WITH_DEFAULT:-default-value}", "", nil)
	expected := "actual-value"

	if result != expected {
		t.Errorf("expandVarsWithRegistry() = %q, want %q", result, expected)
	}
}

func TestExpandVarsWithRegistry_MultipleVars(t *testing.T) {
	home, _ := os.UserHomeDir()
	os.Setenv("TEST_MULTI", "multi")
	defer os.Unsetenv("TEST_MULTI")

	result := expandVarsWithRegistry("${HOME}/config:${repo}/bin:${env:TEST_MULTI}", "/workspace", nil)
	expected := home + "/config:/workspace/bin:multi"

	if result != expected {
		t.Errorf("expandVarsWithRegistry() = %q, want %q", result, expected)
	}
}

func TestExpandVarsWithRegistry_Keychain(t *testing.T) {
	// Keychain falls back to env when no keychain manager
	os.Setenv("KEYCHAIN_TEST", "keychain-value")
	defer os.Unsetenv("KEYCHAIN_TEST")

	result := expandVarsWithRegistry("${keychain:KEYCHAIN_TEST}", "", nil)

	// Should fall back to env var
	if result != "keychain-value" {
		t.Errorf("expandVarsWithRegistry() = %q, want keychain-value", result)
	}
}

func TestExpandVarsWithRegistry_Secret(t *testing.T) {
	// Secret resolution uses the secrets manager
	// When not found, it will be replaced with empty string
	result := expandVarsWithRegistry("${secret:NONEXISTENT_SECRET}", "", nil)

	// Secret not found should result in empty string
	// (actual behavior depends on secrets manager)
	if result != "" {
		t.Logf("secret resolved to: %q (may vary by environment)", result)
	}
}

func TestExpandVarsWithRegistry_NoVars(t *testing.T) {
	result := expandVarsWithRegistry("plain string", "/workspace", nil)

	if result != "plain string" {
		t.Errorf("expandVarsWithRegistry() = %q, want 'plain string'", result)
	}
}

func TestExpandVarsWithRegistry_UnterminatedVar(t *testing.T) {
	// Unterminated variable pattern should be left as-is
	result := expandVarsWithRegistry("${env:UNTERMINATED", "", nil)

	// Loop should break without infinite loop
	if result != "${env:UNTERMINATED" {
		t.Errorf("expandVarsWithRegistry() = %q, want original", result)
	}
}
