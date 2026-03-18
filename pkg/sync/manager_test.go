package sync

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestNewManager_CreatesWithRepoRoot(t *testing.T) {
	m, err := NewManager("/tmp/test-repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	if m.RepoRoot != "/tmp/test-repo" {
		t.Errorf("RepoRoot = %q, want %q", m.RepoRoot, "/tmp/test-repo")
	}
	if m.HomeDir == "" {
		t.Error("HomeDir should not be empty")
	}
	if m.Profiles == nil {
		t.Error("Profiles should not be nil")
	}
}

func TestNewManager_RegistersAllProfiles(t *testing.T) {
	m, err := NewManager("/tmp/test-repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	expectedProfiles := []string{
		"codex",
		"kilocode",
		"claude",
		"claude_desktop",
		"gemini",
		"antigravity",
		"vscode",
		"opencode",
	}

	for _, name := range expectedProfiles {
		if _, ok := m.Profiles[name]; !ok {
			t.Errorf("expected profile %q to be registered", name)
		}
	}
}

func TestNewManager_ProfileHasCorrectFields(t *testing.T) {
	m, err := NewManager("/tmp/test-repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Test claude profile specifically
	p := m.Profiles["claude"]
	if p == nil {
		t.Fatal("claude profile not found")
	}

	if p.Name != "claude" {
		t.Errorf("Name = %q, want %q", p.Name, "claude")
	}
	if p.RepoDir != ".claude" {
		t.Errorf("RepoDir = %q, want %q", p.RepoDir, ".claude")
	}
	if p.HomeDir != ".claude" {
		t.Errorf("HomeDir = %q, want %q", p.HomeDir, ".claude")
	}
	if p.GeneratorTarget != "claude" {
		t.Errorf("GeneratorTarget = %q, want %q", p.GeneratorTarget, "claude")
	}
	if p.GeneratedFile != "mcp.json" {
		t.Errorf("GeneratedFile = %q, want %q", p.GeneratedFile, "mcp.json")
	}
	if !p.SyncGeneratedOnly {
		t.Errorf("SyncGeneratedOnly = %v, want %v", p.SyncGeneratedOnly, true)
	}
}

func TestNewManager_CliProfilesSyncGeneratedOnly(t *testing.T) {
	m, err := NewManager("/tmp/test-repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	for _, name := range []string{"codex", "gemini", "kilocode"} {
		p := m.Profiles[name]
		if p == nil {
			t.Fatalf("profile %q not found", name)
		}
		if !p.SyncGeneratedOnly {
			t.Fatalf("profile %q SyncGeneratedOnly=%v, want true", name, p.SyncGeneratedOnly)
		}
		if p.GeneratedFile != "config.toml" {
			t.Fatalf("profile %q GeneratedFile=%q, want %q", name, p.GeneratedFile, "config.toml")
		}
	}
}

func TestGetProfile_ReturnsCorrectProfile(t *testing.T) {
	m, err := NewManager("/tmp/test-repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	p, err := m.GetProfile("codex")
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}
	if p.Name != "codex" {
		t.Errorf("got profile %q, want %q", p.Name, "codex")
	}
}

func TestGetProfile_ReturnsErrorForUnknown(t *testing.T) {
	m, err := NewManager("/tmp/test-repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	_, err = m.GetProfile("nonexistent")
	if err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestGet_ReturnsProfile(t *testing.T) {
	m, err := NewManager("/tmp/test-repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	p := m.Get("vscode")
	if p == nil {
		t.Fatal("Get should return vscode profile")
	}
	if p.Name != "vscode" {
		t.Errorf("got %q, want %q", p.Name, "vscode")
	}
}

func TestGet_ReturnsNilForUnknown(t *testing.T) {
	m, err := NewManager("/tmp/test-repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	p := m.Get("nonexistent")
	if p != nil {
		t.Error("Get should return nil for unknown profile")
	}
}

func TestList_ReturnsAllProfileNames(t *testing.T) {
	m, err := NewManager("/tmp/test-repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	names := m.List()

	// Sort for consistent comparison
	sort.Strings(names)

	expected := []string{
		"antigravity",
		"claude",
		"claude_desktop",
		"codex",
		"gemini",
		"kilocode",
		"opencode",
		"vscode",
		"zed",
	}
	sort.Strings(expected)

	if len(names) != len(expected) {
		t.Errorf("got %d profiles, want %d", len(names), len(expected))
	}

	for i, name := range names {
		if name != expected[i] {
			t.Errorf("names[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestResolveRepoPath_JoinsCorrectly(t *testing.T) {
	m, err := NewManager("/workspace/loom")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	p := &Profile{RepoDir: ".claude"}
	resolved := m.ResolveRepoPath(p)

	expected := filepath.Join("/workspace/loom", ".claude")
	if resolved != expected {
		t.Errorf("got %q, want %q", resolved, expected)
	}
}

func TestResolveHomePath_JoinsRelativePath(t *testing.T) {
	m, err := NewManager("/tmp/repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	p := &Profile{HomeDir: ".claude"}
	resolved := m.ResolveHomePath(p)

	expected := filepath.Join(m.HomeDir, ".claude")
	if resolved != expected {
		t.Errorf("got %q, want %q", resolved, expected)
	}
}

func TestResolveHomePath_ReturnsAbsolutePath(t *testing.T) {
	m, err := NewManager("/tmp/repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	absPath := "/opt/custom/config"
	p := &Profile{HomeDir: absPath}
	resolved := m.ResolveHomePath(p)

	if resolved != absPath {
		t.Errorf("got %q, want absolute path %q", resolved, absPath)
	}
}

func TestResolveHomePath_HandlesNestedPath(t *testing.T) {
	m, err := NewManager("/tmp/repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// Test claude_desktop which uses nested path
	p := m.Profiles["claude_desktop"]
	if p == nil {
		t.Fatal("claude_desktop profile not found")
	}

	resolved := m.ResolveHomePath(p)

	// Should be joined with home
	expected := filepath.Join(m.HomeDir, "Library/Application Support/Claude")
	if resolved != expected {
		t.Errorf("got %q, want %q", resolved, expected)
	}
}

func TestResolveHomePath_VscodeProfile(t *testing.T) {
	m, err := NewManager("/tmp/repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	p := m.Profiles["vscode"]
	if p == nil {
		t.Fatal("vscode profile not found")
	}

	resolved := m.ResolveHomePath(p)

	expected := filepath.Join(m.HomeDir, "Library/Application Support/Code/User")
	if resolved != expected {
		t.Errorf("got %q, want %q", resolved, expected)
	}

	// Verify WorkspaceDir is also set
	if p.WorkspaceDir != ".vscode" {
		t.Errorf("WorkspaceDir = %q, want %q", p.WorkspaceDir, ".vscode")
	}
}

func TestProfile_ExcludesAreSet(t *testing.T) {
	m, err := NewManager("/tmp/repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	p := m.Profiles["claude"]
	if len(p.Excludes) == 0 {
		t.Error("expected excludes to be set")
	}

	// Check specific excludes
	hasAuthJson := false
	for _, e := range p.Excludes {
		if e == "auth.json" {
			hasAuthJson = true
			break
		}
	}
	if !hasAuthJson {
		t.Error("expected auth.json in excludes")
	}
}

func TestProfile_SecretFilesAreSet(t *testing.T) {
	m, err := NewManager("/tmp/repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	p := m.Profiles["codex"]
	if len(p.SecretFiles) == 0 {
		t.Error("expected secret files to be set")
	}

	if p.SecretFiles[0] != "auth.json" {
		t.Errorf("SecretFiles[0] = %q, want %q", p.SecretFiles[0], "auth.json")
	}
}

func TestAntigravityProfile_HomePathAndFilenameOverride(t *testing.T) {
	m, err := NewManager("/tmp/test-repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	p := m.Get("antigravity")
	if p == nil {
		t.Fatal("antigravity profile not found")
	}
	if p.HomeDir != ".gemini/antigravity" {
		t.Fatalf("HomeDir = %q, want %q", p.HomeDir, ".gemini/antigravity")
	}
	if p.GeneratedFile != "mcp.json" {
		t.Fatalf("GeneratedFile = %q, want %q", p.GeneratedFile, "mcp.json")
	}
	if p.HomeGeneratedFile != "mcp_config.json" {
		t.Fatalf("HomeGeneratedFile = %q, want %q", p.HomeGeneratedFile, "mcp_config.json")
	}
	if p.SkillsTarget != "gemini" {
		t.Fatalf("SkillsTarget = %q, want %q", p.SkillsTarget, "gemini")
	}
	if !p.SkillsDirectToHome {
		t.Fatal("expected SkillsDirectToHome=true for antigravity")
	}
	if p.SkillsHomePath != "$HOME/.gemini/antigravity/skills" {
		t.Fatalf("SkillsHomePath = %q, want %q", p.SkillsHomePath, "$HOME/.gemini/antigravity/skills")
	}
}

func TestCodexProfile_GeneratesDirectlyToHome(t *testing.T) {
	m, err := NewManager("/tmp/test-repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	p := m.Get("codex")
	if p == nil {
		t.Fatal("codex profile not found")
	}
	if !p.GeneratedDirectToHome {
		t.Fatal("expected GeneratedDirectToHome=true for codex")
	}
	if !p.SkillsDirectToHome {
		t.Fatal("expected SkillsDirectToHome=true for codex")
	}
	if p.SkillsHomePath != "$HOME/.codex/skills" {
		t.Fatalf("SkillsHomePath = %q, want %q", p.SkillsHomePath, "$HOME/.codex/skills")
	}
}

// Test that the manager uses the actual user home directory
func TestNewManager_UsesRealHomeDir(t *testing.T) {
	m, err := NewManager("/tmp/repo")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	expectedHome, _ := os.UserHomeDir()
	if m.HomeDir != expectedHome {
		t.Errorf("HomeDir = %q, want %q", m.HomeDir, expectedHome)
	}
}
