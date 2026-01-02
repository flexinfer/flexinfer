package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// Backup Tests
// =============================================================================

func TestBackup_RepoSource(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create test profile directory in repo with content
	profileDir := filepath.Join(repoDir, "test-profile")
	os.MkdirAll(profileDir, 0755)
	os.WriteFile(filepath.Join(profileDir, "config.toml"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir // Override home for testing

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-profile",
		HomeDir: filepath.Join(homeDir, "test-profile"),
	}

	// Create home dir so backups go there
	os.MkdirAll(filepath.Join(homeDir, "test-profile"), 0755)

	err := m.Backup("test", "repo")
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Check backup was created
	backupRoot := filepath.Join(homeDir, "test-profile", "backups")
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		t.Fatalf("failed to read backup dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected backup directory to be created")
	}

	// Verify backup contains the file
	backupDir := filepath.Join(backupRoot, entries[0].Name())
	backupFile := filepath.Join(backupDir, "config.toml")
	if !Exists(backupFile) {
		t.Error("expected config.toml to be in backup")
	}
}

func TestBackup_HomeSource(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create test profile directory in home with content
	homeProfDir := filepath.Join(homeDir, "test-profile")
	os.MkdirAll(homeProfDir, 0755)
	os.WriteFile(filepath.Join(homeProfDir, "config.toml"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-profile",
		HomeDir: filepath.Join(homeDir, "test-profile"),
	}

	err := m.Backup("test", "home")
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Check backup was created in home profile backups dir
	backupRoot := filepath.Join(homeProfDir, "backups")
	entries, _ := os.ReadDir(backupRoot)
	if len(entries) == 0 {
		t.Error("expected backup directory to be created")
	}
}

func TestBackup_UnknownProfile(t *testing.T) {
	m, _ := NewManager(t.TempDir())

	err := m.Backup("nonexistent", "repo")
	if err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestBackup_SourceNotFound(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "nonexistent",
		HomeDir: filepath.Join(homeDir, "nonexistent"),
	}

	err := m.Backup("test", "repo")
	if err == nil {
		t.Error("expected error when source doesn't exist")
	}
}

func TestBackup_ExcludesBackupsDir(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create test profile with nested backups folder
	profileDir := filepath.Join(repoDir, "test-profile")
	os.MkdirAll(filepath.Join(profileDir, "backups"), 0755)
	os.WriteFile(filepath.Join(profileDir, "config.toml"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(profileDir, "backups", "old.txt"), []byte("old"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-profile",
		HomeDir: filepath.Join(homeDir, "test-profile"),
	}

	os.MkdirAll(filepath.Join(homeDir, "test-profile"), 0755)

	err := m.Backup("test", "repo")
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Check backup doesn't contain nested backups folder
	backupRoot := filepath.Join(homeDir, "test-profile", "backups")
	entries, _ := os.ReadDir(backupRoot)
	if len(entries) == 0 {
		t.Fatal("expected backup to be created")
	}

	// The backup should NOT contain its own "backups" subfolder
	backupPath := filepath.Join(backupRoot, entries[0].Name())
	nestedBackups := filepath.Join(backupPath, "backups")
	if Exists(nestedBackups) {
		t.Error("backup should exclude nested 'backups' directory")
	}
}

func TestBackup_TimestampFormat(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	profileDir := filepath.Join(repoDir, "test-profile")
	os.MkdirAll(profileDir, 0755)
	os.WriteFile(filepath.Join(profileDir, "config.toml"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-profile",
		HomeDir: filepath.Join(homeDir, "test-profile"),
	}

	os.MkdirAll(filepath.Join(homeDir, "test-profile"), 0755)

	_ = m.Backup("test", "repo")

	backupRoot := filepath.Join(homeDir, "test-profile", "backups")
	entries, _ := os.ReadDir(backupRoot)
	if len(entries) == 0 {
		t.Fatal("expected backup directory")
	}

	// Check timestamp format: test_repo_YYYYMMDD_HHMMSS
	name := entries[0].Name()
	if !strings.HasPrefix(name, "test_repo_") {
		t.Errorf("backup name %q should start with 'test_repo_'", name)
	}
}

// =============================================================================
// Validate Tests
// =============================================================================

func TestValidate_ConfigToml(t *testing.T) {
	homeDir := t.TempDir()
	profileDir := filepath.Join(homeDir, "test-profile")
	os.MkdirAll(profileDir, 0755)
	// Create valid TOML config content
	validToml := `[mcp_servers.test]
command = "node"
args = ["server.js"]
`
	os.WriteFile(filepath.Join(profileDir, "config.toml"), []byte(validToml), 0644)

	m, _ := NewManager(t.TempDir())
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:            "test",
		RepoDir:         "test-repo",
		HomeDir:         filepath.Join(homeDir, "test-profile"),
		GeneratorTarget: "codex",
	}

	err := m.Validate("test")
	if err != nil {
		t.Errorf("Validate should pass when config.toml exists: %v", err)
	}
}

func TestValidate_McpJson(t *testing.T) {
	homeDir := t.TempDir()
	profileDir := filepath.Join(homeDir, "test-profile")
	os.MkdirAll(profileDir, 0755)
	// Create valid JSON config content
	validJson := `{"mcpServers": {"test": {"command": "node", "args": ["server.js"]}}}`
	os.WriteFile(filepath.Join(profileDir, "mcp.json"), []byte(validJson), 0644)

	m, _ := NewManager(t.TempDir())
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:            "test",
		RepoDir:         "test-repo",
		HomeDir:         filepath.Join(homeDir, "test-profile"),
		GeneratorTarget: "claude",
	}

	err := m.Validate("test")
	if err != nil {
		t.Errorf("Validate should pass when mcp.json exists: %v", err)
	}
}

func TestValidate_ClaudeDesktopConfig(t *testing.T) {
	homeDir := t.TempDir()
	profileDir := filepath.Join(homeDir, "test-profile")
	os.MkdirAll(profileDir, 0755)
	// Create valid JSON config content
	validJson := `{"mcpServers": {"test": {"command": "node", "args": ["server.js"]}}}`
	os.WriteFile(filepath.Join(profileDir, "claude_desktop_config.json"), []byte(validJson), 0644)

	m, _ := NewManager(t.TempDir())
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:            "test",
		RepoDir:         "test-repo",
		HomeDir:         filepath.Join(homeDir, "test-profile"),
		GeneratorTarget: "claude_desktop",
	}

	err := m.Validate("test")
	if err != nil {
		t.Errorf("Validate should pass when claude_desktop_config.json exists: %v", err)
	}
}

func TestValidate_NoConfigFiles(t *testing.T) {
	homeDir := t.TempDir()
	profileDir := filepath.Join(homeDir, "test-profile")
	os.MkdirAll(profileDir, 0755)
	// Don't create any config files

	m, _ := NewManager(t.TempDir())
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-repo",
		HomeDir: filepath.Join(homeDir, "test-profile"),
	}

	err := m.Validate("test")
	if err == nil {
		t.Error("Validate should fail when no config files exist")
	}
}

func TestValidate_ProfileNotFound(t *testing.T) {
	m, _ := NewManager(t.TempDir())

	err := m.Validate("nonexistent")
	if err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestValidate_HomeDirNotExists(t *testing.T) {
	m, _ := NewManager(t.TempDir())
	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-repo",
		HomeDir: "/nonexistent/path",
	}

	err := m.Validate("test")
	if err == nil {
		t.Error("Validate should fail when home dir doesn't exist")
	}
}

// =============================================================================
// PullFromHome Tests
// =============================================================================

func TestPullFromHome_Basic(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create content in home
	homeProfDir := filepath.Join(homeDir, "test-profile")
	os.MkdirAll(homeProfDir, 0755)
	os.WriteFile(filepath.Join(homeProfDir, "config.toml"), []byte("from home"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-profile",
		HomeDir: filepath.Join(homeDir, "test-profile"),
	}

	err := m.PullFromHome("test", false)
	if err != nil {
		t.Fatalf("PullFromHome failed: %v", err)
	}

	// Check file was pulled to repo
	repoFile := filepath.Join(repoDir, "test-profile", "config.toml")
	content, err := os.ReadFile(repoFile)
	if err != nil {
		t.Fatalf("failed to read pulled file: %v", err)
	}
	if string(content) != "from home" {
		t.Errorf("content = %q, want 'from home'", string(content))
	}
}

func TestPullFromHome_HomeNotExists(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-profile",
		HomeDir: filepath.Join(homeDir, "nonexistent"),
	}

	err := m.PullFromHome("test", false)
	if err == nil {
		t.Error("expected error when home doesn't exist")
	}
}

func TestPullFromHome_UnknownProfile(t *testing.T) {
	m, _ := NewManager(t.TempDir())

	err := m.PullFromHome("nonexistent", false)
	if err == nil {
		t.Error("expected error for unknown profile")
	}
}

// =============================================================================
// SyncToHome Tests
// =============================================================================

func TestSyncToHome_Basic(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create content in repo
	repoProfDir := filepath.Join(repoDir, "test-profile")
	os.MkdirAll(repoProfDir, 0755)
	os.WriteFile(filepath.Join(repoProfDir, "config.toml"), []byte("from repo"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-profile",
		HomeDir: filepath.Join(homeDir, "test-profile"),
	}

	err := m.SyncToHome("test", false, false, false, false, "", false, "")
	if err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	// Check file was synced to home
	homeFile := filepath.Join(homeDir, "test-profile", "config.toml")
	content, err := os.ReadFile(homeFile)
	if err != nil {
		t.Fatalf("failed to read synced file: %v", err)
	}
	if string(content) != "from repo" {
		t.Errorf("content = %q, want 'from repo'", string(content))
	}
}

func TestSyncToHome_RepoNotExists(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "nonexistent",
		HomeDir: filepath.Join(homeDir, "test-profile"),
	}

	err := m.SyncToHome("test", false, false, false, false, "", false, "")
	if err == nil {
		t.Error("expected error when repo doesn't exist")
	}
}

func TestSyncToHome_RepoOnly(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	repoProfDir := filepath.Join(repoDir, "test-profile")
	os.MkdirAll(repoProfDir, 0755)
	os.WriteFile(filepath.Join(repoProfDir, "config.toml"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-profile",
		HomeDir: filepath.Join(homeDir, "test-profile"),
	}

	// With repoOnly=true, should not sync to home
	err := m.SyncToHome("test", false, false, true, false, "", false, "")
	if err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	// Home should not have the file
	homeFile := filepath.Join(homeDir, "test-profile", "config.toml")
	if Exists(homeFile) {
		t.Error("file should not be synced when repoOnly=true")
	}
}

func TestSyncToHome_UnknownProfile(t *testing.T) {
	m, _ := NewManager(t.TempDir())

	err := m.SyncToHome("nonexistent", false, false, false, false, "", false, "")
	if err == nil {
		t.Error("expected error for unknown profile")
	}
}

func TestSyncToHome_WithWorkspaceDir(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create repo profile with generated file
	repoProfDir := filepath.Join(repoDir, ".vscode-mcp")
	os.MkdirAll(repoProfDir, 0755)
	os.WriteFile(filepath.Join(repoProfDir, "mcp.json"), []byte("{}"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	m.Profiles["vscode"] = &Profile{
		Name:          "vscode",
		RepoDir:       ".vscode-mcp",
		HomeDir:       filepath.Join(homeDir, ".vscode-mcp"),
		WorkspaceDir:  ".vscode",
		GeneratedFile: "mcp.json",
	}

	err := m.SyncToHome("vscode", false, false, false, false, "", false, "")
	if err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	// Check file was also copied to workspace dir
	workspaceFile := filepath.Join(repoDir, ".vscode", "mcp.json")
	if !Exists(workspaceFile) {
		t.Error("expected mcp.json to be copied to .vscode workspace dir")
	}
}

// =============================================================================
// SyncAll Tests
// =============================================================================

func TestSyncAll_MultiplePreparedProfiles(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create repo directories for two profiles
	for _, profile := range []string{"profile1", "profile2"} {
		profDir := filepath.Join(repoDir, profile)
		os.MkdirAll(profDir, 0755)
		os.WriteFile(filepath.Join(profDir, "config.toml"), []byte(profile), 0644)
	}

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	// Clear default profiles and add test profiles
	m.Profiles = map[string]*Profile{
		"profile1": {
			Name:    "profile1",
			RepoDir: "profile1",
			HomeDir: filepath.Join(homeDir, "profile1"),
		},
		"profile2": {
			Name:    "profile2",
			RepoDir: "profile2",
			HomeDir: filepath.Join(homeDir, "profile2"),
		},
	}

	err := m.SyncAll(false, false, false, false, "", false, "")
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	// Check both profiles were synced
	for _, profile := range []string{"profile1", "profile2"} {
		homeFile := filepath.Join(homeDir, profile, "config.toml")
		if !Exists(homeFile) {
			t.Errorf("expected %s to be synced", homeFile)
		}
	}
}
