package sync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/skills"
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

func TestSyncToHome_GeminiPreservesTrustedFolders(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	repoGemini := filepath.Join(repoDir, ".gemini")
	homeGemini := filepath.Join(homeDir, ".gemini")
	if err := os.MkdirAll(repoGemini, 0755); err != nil {
		t.Fatalf("mkdir repo gemini: %v", err)
	}
	if err := os.MkdirAll(homeGemini, 0755); err != nil {
		t.Fatalf("mkdir home gemini: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repoGemini, "config.toml"), []byte("[mcp_servers.test]\ncommand = \"echo\"\n"), 0644); err != nil {
		t.Fatalf("write repo config.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoGemini, "settings.json"), []byte("{\"theme\":\"test\"}\n"), 0644); err != nil {
		t.Fatalf("write repo settings.json: %v", err)
	}

	original := []byte("{\n  \"/tmp/workspace\": \"TRUST_PARENT\"\n}\n")
	if err := os.WriteFile(filepath.Join(homeGemini, "trustedFolders.json"), original, 0600); err != nil {
		t.Fatalf("write trustedFolders.json: %v", err)
	}

	if err := m.SyncToHome("gemini", false, false, false, false, "", false, "", false); err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(homeGemini, "trustedFolders.json"))
	if err != nil {
		t.Fatalf("read trustedFolders.json: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("trustedFolders.json changed unexpectedly\nwant: %q\ngot:  %q", string(original), string(got))
	}
}

func TestEnsureGeminiTrustedFolders_RepairsInvalidFile(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")
	if err := os.MkdirAll(homeGemini, 0755); err != nil {
		t.Fatalf("mkdir home gemini: %v", err)
	}

	path := filepath.Join(homeGemini, "trustedFolders.json")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatalf("write empty trustedFolders.json: %v", err)
	}

	if err := ensureGeminiTrustedFolders(homeGemini, nil); err != nil {
		t.Fatalf("ensureGeminiTrustedFolders failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trustedFolders.json: %v", err)
	}
	if !isValidGeminiTrustedFolders(got) {
		t.Fatalf("trustedFolders.json should be valid after repair, got %q", string(got))
	}
}

func TestSyncToHome_GeminiPreservesExtensionEnablement(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	repoGemini := filepath.Join(repoDir, ".gemini")
	homeGemini := filepath.Join(homeDir, ".gemini")
	if err := os.MkdirAll(filepath.Join(repoGemini), 0755); err != nil {
		t.Fatalf("mkdir repo gemini: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(homeGemini, "extensions"), 0755); err != nil {
		t.Fatalf("mkdir home gemini extensions: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repoGemini, "config.toml"), []byte("[mcp_servers.test]\ncommand = \"echo\"\n"), 0644); err != nil {
		t.Fatalf("write repo config.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoGemini, "settings.json"), []byte("{\"theme\":\"test\"}\n"), 0644); err != nil {
		t.Fatalf("write repo settings.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeGemini, "trustedFolders.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatalf("write trustedFolders.json: %v", err)
	}

	original := []byte("{\n  \"foo\": true,\n  \"bar\": false\n}\n")
	enablementPath := filepath.Join(homeGemini, "extensions", "extension-enablement.json")
	if err := os.WriteFile(enablementPath, original, 0600); err != nil {
		t.Fatalf("write extension-enablement.json: %v", err)
	}

	if err := m.SyncToHome("gemini", false, false, false, false, "", false, "", false); err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	got, err := os.ReadFile(enablementPath)
	if err != nil {
		t.Fatalf("read extension-enablement.json: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("extension-enablement.json changed unexpectedly\nwant: %q\ngot:  %q", string(original), string(got))
	}
}

func TestEnsureGeminiExtensionEnablement_RepairsInvalidFile(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")
	if err := os.MkdirAll(filepath.Join(homeGemini, "extensions"), 0755); err != nil {
		t.Fatalf("mkdir home gemini extensions: %v", err)
	}

	path := filepath.Join(homeGemini, "extensions", "extension-enablement.json")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatalf("write empty extension-enablement.json: %v", err)
	}

	if err := ensureGeminiExtensionEnablement(homeGemini, nil); err != nil {
		t.Fatalf("ensureGeminiExtensionEnablement failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read extension-enablement.json: %v", err)
	}
	if !isValidGeminiJSONObject(got) {
		t.Fatalf("extension-enablement.json should be valid after repair, got %q", string(got))
	}
}

func TestEnsureGeminiExtensionEnablement_RepairsFromBackup(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")
	if err := os.MkdirAll(filepath.Join(homeGemini, "extensions"), 0755); err != nil {
		t.Fatalf("mkdir home gemini extensions: %v", err)
	}

	path := filepath.Join(homeGemini, "extensions", "extension-enablement.json")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatalf("write invalid extension-enablement.json: %v", err)
	}

	backupPath := filepath.Join(homeGemini, "backups", "gemini_home_20990101_000000", "extensions", "extension-enablement.json")
	backup := []byte("{\"context7\":true}\n")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0755); err != nil {
		t.Fatalf("mkdir backup path: %v", err)
	}
	if err := os.WriteFile(backupPath, backup, 0600); err != nil {
		t.Fatalf("write backup extension-enablement.json: %v", err)
	}

	if err := ensureGeminiExtensionEnablement(homeGemini, nil); err != nil {
		t.Fatalf("ensureGeminiExtensionEnablement failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read extension-enablement.json: %v", err)
	}
	if string(got) != string(backup) {
		t.Fatalf("expected extension-enablement.json restored from backup\nwant: %q\ngot:  %q", string(backup), string(got))
	}
}

func TestEnsureGeminiExtensionManifests_RepairsInvalidFromBackup(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")
	manifestPath := filepath.Join(homeGemini, "extensions", "context7", "gemini-extension.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err != nil {
		t.Fatalf("mkdir manifest dir: %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte(""), 0600); err != nil {
		t.Fatalf("write invalid gemini-extension.json: %v", err)
	}

	backupPath := filepath.Join(homeGemini, "backups", "gemini_home_20990101_000000", "extensions", "context7", "gemini-extension.json")
	backup := []byte("{\"name\":\"context7\"}\n")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0755); err != nil {
		t.Fatalf("mkdir backup manifest dir: %v", err)
	}
	if err := os.WriteFile(backupPath, backup, 0600); err != nil {
		t.Fatalf("write backup manifest: %v", err)
	}

	if err := ensureGeminiExtensionManifests(homeGemini, nil); err != nil {
		t.Fatalf("ensureGeminiExtensionManifests failed: %v", err)
	}

	got, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read gemini-extension.json: %v", err)
	}
	if string(got) != string(backup) {
		t.Fatalf("expected gemini-extension.json restored from backup\nwant: %q\ngot:  %q", string(backup), string(got))
	}
}

func TestEnsureGeminiExtensionCommands_RepairsInvalidFromBackup(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")
	commandPath := filepath.Join(homeGemini, "extensions", "code-review", "commands", "code-review.toml")
	if err := os.MkdirAll(filepath.Dir(commandPath), 0755); err != nil {
		t.Fatalf("mkdir command dir: %v", err)
	}
	if err := os.WriteFile(commandPath, []byte(""), 0600); err != nil {
		t.Fatalf("write invalid command: %v", err)
	}

	backupPath := filepath.Join(homeGemini, "backups", "gemini_home_20990101_000000", "extensions", "code-review", "commands", "code-review.toml")
	backup := []byte("name = \"code-review\"\nprompt = \"Run a review\"\n")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0755); err != nil {
		t.Fatalf("mkdir backup command dir: %v", err)
	}
	if err := os.WriteFile(backupPath, backup, 0600); err != nil {
		t.Fatalf("write backup command: %v", err)
	}

	if err := ensureGeminiExtensionCommands(homeGemini); err != nil {
		t.Fatalf("ensureGeminiExtensionCommands failed: %v", err)
	}

	got, err := os.ReadFile(commandPath)
	if err != nil {
		t.Fatalf("read command: %v", err)
	}
	if string(got) != string(backup) {
		t.Fatalf("expected command restored from backup\nwant: %q\ngot:  %q", string(backup), string(got))
	}
}

func TestEnsureGeminiExtensionCommands_QuarantinesInvalidWithoutBackup(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")
	commandPath := filepath.Join(homeGemini, "extensions", "code-review", "commands", "code-review.toml")
	if err := os.MkdirAll(filepath.Dir(commandPath), 0755); err != nil {
		t.Fatalf("mkdir command dir: %v", err)
	}
	if err := os.WriteFile(commandPath, []byte(""), 0600); err != nil {
		t.Fatalf("write invalid command: %v", err)
	}

	if err := ensureGeminiExtensionCommands(homeGemini); err != nil {
		t.Fatalf("ensureGeminiExtensionCommands failed: %v", err)
	}

	if _, err := os.Stat(commandPath); !os.IsNotExist(err) {
		t.Fatalf("expected invalid command to be quarantined, stat err=%v", err)
	}
	quarantinePath := commandPath + ".loom-quarantined"
	if _, err := os.Stat(quarantinePath); err != nil {
		t.Fatalf("expected quarantined file at %s: %v", quarantinePath, err)
	}
}

func TestEnsureGeminiAuthFiles_RepairsCorruptedAuthData(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")
	if err := os.MkdirAll(homeGemini, 0755); err != nil {
		t.Fatalf("mkdir home gemini: %v", err)
	}

	originalGoogle := []byte("{\"email\":\"user@example.com\"}\n")
	originalOAuth := []byte("{\"refresh_token\":\"abc\"}\n")
	originalState := []byte("{\"session\":\"ok\"}\n")
	originalInstall := []byte("installation-id-123\n")

	if err := os.WriteFile(filepath.Join(homeGemini, "google_accounts.json"), originalGoogle, 0600); err != nil {
		t.Fatalf("write google_accounts.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeGemini, "oauth_creds.json"), originalOAuth, 0600); err != nil {
		t.Fatalf("write oauth_creds.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeGemini, "state.json"), originalState, 0600); err != nil {
		t.Fatalf("write state.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeGemini, "installation_id"), originalInstall, 0600); err != nil {
		t.Fatalf("write installation_id: %v", err)
	}

	snapshot := readGeminiAuthSnapshot(homeGemini)

	// Simulate corruption during sync or external write.
	if err := os.WriteFile(filepath.Join(homeGemini, "google_accounts.json"), []byte(""), 0600); err != nil {
		t.Fatalf("corrupt google_accounts.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeGemini, "oauth_creds.json"), []byte("not-json"), 0600); err != nil {
		t.Fatalf("corrupt oauth_creds.json: %v", err)
	}
	if err := os.Remove(filepath.Join(homeGemini, "state.json")); err != nil {
		t.Fatalf("remove state.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeGemini, "installation_id"), []byte(" \n"), 0600); err != nil {
		t.Fatalf("corrupt installation_id: %v", err)
	}

	if err := ensureGeminiAuthFiles(homeGemini, snapshot); err != nil {
		t.Fatalf("ensureGeminiAuthFiles failed: %v", err)
	}

	assertFileEquals := func(name string, want []byte) {
		got, err := os.ReadFile(filepath.Join(homeGemini, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s mismatch\nwant: %q\ngot:  %q", name, string(want), string(got))
		}
	}

	assertFileEquals("google_accounts.json", originalGoogle)
	assertFileEquals("oauth_creds.json", originalOAuth)
	assertFileEquals("state.json", originalState)
	assertFileEquals("installation_id", originalInstall)
}

func TestEnsureGeminiAuthFiles_DoesNotOverrideValidCurrentData(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")
	if err := os.MkdirAll(homeGemini, 0755); err != nil {
		t.Fatalf("mkdir home gemini: %v", err)
	}

	// Snapshot with old values.
	if err := os.WriteFile(filepath.Join(homeGemini, "google_accounts.json"), []byte("{\"email\":\"old@example.com\"}\n"), 0600); err != nil {
		t.Fatalf("write old google_accounts.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeGemini, "installation_id"), []byte("old-install-id\n"), 0600); err != nil {
		t.Fatalf("write old installation_id: %v", err)
	}
	snapshot := readGeminiAuthSnapshot(homeGemini)

	// Simulate user re-auth producing new valid files.
	newGoogle := []byte("{\"email\":\"new@example.com\"}\n")
	newInstall := []byte("new-install-id\n")
	if err := os.WriteFile(filepath.Join(homeGemini, "google_accounts.json"), newGoogle, 0600); err != nil {
		t.Fatalf("write new google_accounts.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeGemini, "installation_id"), newInstall, 0600); err != nil {
		t.Fatalf("write new installation_id: %v", err)
	}

	if err := ensureGeminiAuthFiles(homeGemini, snapshot); err != nil {
		t.Fatalf("ensureGeminiAuthFiles failed: %v", err)
	}

	gotGoogle, err := os.ReadFile(filepath.Join(homeGemini, "google_accounts.json"))
	if err != nil {
		t.Fatalf("read google_accounts.json: %v", err)
	}
	if string(gotGoogle) != string(newGoogle) {
		t.Fatalf("expected current valid google_accounts.json preserved\nwant: %q\ngot:  %q", string(newGoogle), string(gotGoogle))
	}

	gotInstall, err := os.ReadFile(filepath.Join(homeGemini, "installation_id"))
	if err != nil {
		t.Fatalf("read installation_id: %v", err)
	}
	if string(gotInstall) != string(newInstall) {
		t.Fatalf("expected current valid installation_id preserved\nwant: %q\ngot:  %q", string(newInstall), string(gotInstall))
	}
}

func TestSyncToHome_ClaudeRepairsInvalidSettingsFromSnapshot(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	repoClaude := filepath.Join(repoDir, ".claude")
	homeClaude := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(repoClaude, 0755); err != nil {
		t.Fatalf("mkdir repo claude: %v", err)
	}
	if err := os.MkdirAll(homeClaude, 0755); err != nil {
		t.Fatalf("mkdir home claude: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repoClaude, "mcp.json"), []byte("{\"mcpServers\":{}}\n"), 0644); err != nil {
		t.Fatalf("write repo mcp.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoClaude, "settings.json"), []byte(""), 0644); err != nil {
		t.Fatalf("write invalid repo settings.json: %v", err)
	}

	original := []byte("{\"customSetting\":true}\n")
	settingsPath := filepath.Join(homeClaude, "settings.json")
	if err := os.WriteFile(settingsPath, original, 0600); err != nil {
		t.Fatalf("write home settings.json: %v", err)
	}

	if err := m.SyncToHome("claude", false, false, false, false, "", false, "", false); err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	got, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read home settings.json: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("expected settings.json repaired from snapshot\nwant: %q\ngot:  %q", string(original), string(got))
	}
}

func TestSyncToHome_CodexRepairsInvalidConfigFromSnapshot(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	repoCodex := filepath.Join(repoDir, ".codex")
	homeCodex := filepath.Join(homeDir, ".codex")
	if err := os.MkdirAll(repoCodex, 0755); err != nil {
		t.Fatalf("mkdir repo codex: %v", err)
	}
	if err := os.MkdirAll(homeCodex, 0755); err != nil {
		t.Fatalf("mkdir home codex: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repoCodex, "config.toml"), []byte(""), 0644); err != nil {
		t.Fatalf("write invalid repo config.toml: %v", err)
	}

	original := []byte("[mcp_servers.test]\ncommand = \"echo\"\n")
	configPath := filepath.Join(homeCodex, "config.toml")
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatalf("write home config.toml: %v", err)
	}

	if err := m.SyncToHome("codex", false, false, false, false, "", false, "", false); err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read home config.toml: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("expected config.toml repaired from snapshot\nwant: %q\ngot:  %q", string(original), string(got))
	}
}

func TestEnsureCodexConfigFiles_RepairsInvalidFromBackup(t *testing.T) {
	homeDir := t.TempDir()
	homeCodex := filepath.Join(homeDir, ".codex")
	if err := os.MkdirAll(homeCodex, 0755); err != nil {
		t.Fatalf("mkdir home codex: %v", err)
	}

	path := filepath.Join(homeCodex, "config.toml")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatalf("write invalid config.toml: %v", err)
	}

	backupPath := filepath.Join(homeCodex, "backups", "codex_home_20990101_000000", "config.toml")
	backup := []byte("[mcp_servers.backup]\ncommand = \"echo\"\n")
	if err := os.MkdirAll(filepath.Dir(backupPath), 0755); err != nil {
		t.Fatalf("mkdir backup path: %v", err)
	}
	if err := os.WriteFile(backupPath, backup, 0600); err != nil {
		t.Fatalf("write backup config.toml: %v", err)
	}

	if err := ensureCodexConfigFiles(homeCodex, codexConfigSnapshot{}); err != nil {
		t.Fatalf("ensureCodexConfigFiles failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if string(got) != string(backup) {
		t.Fatalf("expected config.toml restored from backup\nwant: %q\ngot:  %q", string(backup), string(got))
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

func TestDiscoverRegistryPath_PrefersRepoOverride(t *testing.T) {
	repoRoot := t.TempDir()

	override := filepath.Join(repoRoot, "mcp", "context", "registry.yaml")
	platform := filepath.Join(repoRoot, "platform", "gitops", "mcp", "context", "registry.yaml")

	if err := os.MkdirAll(filepath.Dir(override), 0755); err != nil {
		t.Fatalf("mkdir override dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(platform), 0755); err != nil {
		t.Fatalf("mkdir platform dir: %v", err)
	}
	if err := os.WriteFile(override, []byte("override: true\n"), 0644); err != nil {
		t.Fatalf("write override: %v", err)
	}
	if err := os.WriteFile(platform, []byte("platform: true\n"), 0644); err != nil {
		t.Fatalf("write platform: %v", err)
	}

	got := discoverRegistryPath(repoRoot)
	if got != override {
		t.Fatalf("discoverRegistryPath()=%q, want %q", got, override)
	}
}

func TestDiscoverRegistryPath_FindsPlatformGitopsRegistry(t *testing.T) {
	repoRoot := t.TempDir()

	platform := filepath.Join(repoRoot, "platform", "gitops", "mcp", "context", "registry.yaml")
	if err := os.MkdirAll(filepath.Dir(platform), 0755); err != nil {
		t.Fatalf("mkdir platform dir: %v", err)
	}
	if err := os.WriteFile(platform, []byte("platform: true\n"), 0644); err != nil {
		t.Fatalf("write platform: %v", err)
	}

	got := discoverRegistryPath(repoRoot)
	if got != platform {
		t.Fatalf("discoverRegistryPath()=%q, want %q", got, platform)
	}
}

func TestDiscoverRegistryPath_FindsAncestorWorkspacePlatformRegistry(t *testing.T) {
	workspaceRoot := t.TempDir()
	repoRoot := filepath.Join(workspaceRoot, "services", "loom-core")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}

	platform := filepath.Join(workspaceRoot, "platform", "gitops", "mcp", "context", "registry.yaml")
	if err := os.MkdirAll(filepath.Dir(platform), 0755); err != nil {
		t.Fatalf("mkdir platform dir: %v", err)
	}
	if err := os.WriteFile(platform, []byte("platform: true\n"), 0644); err != nil {
		t.Fatalf("write platform: %v", err)
	}

	got := discoverRegistryPath(repoRoot)
	if got != platform {
		t.Fatalf("discoverRegistryPath()=%q, want %q", got, platform)
	}
}

func TestDiscoverSkillsRegistryPath_FindsAncestorWorkspacePlatformRegistry(t *testing.T) {
	workspaceRoot := t.TempDir()
	repoRoot := filepath.Join(workspaceRoot, "services", "loom-core")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}

	skillsRegistry := filepath.Join(workspaceRoot, "platform", "gitops", "mcp", "context", "skills-registry.yaml")
	if err := os.MkdirAll(filepath.Dir(skillsRegistry), 0755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	if err := os.WriteFile(skillsRegistry, []byte("version: 1\nskills: []\n"), 0644); err != nil {
		t.Fatalf("write skills registry: %v", err)
	}

	got := discoverSkillsRegistryPath(repoRoot)
	if got != skillsRegistry {
		t.Fatalf("discoverSkillsRegistryPath()=%q, want %q", got, skillsRegistry)
	}
}

func TestDiscoverSkillsRegistryPath_FindsAncestorWorkspaceLoomCoreRegistry(t *testing.T) {
	workspaceRoot := t.TempDir()
	repoRoot := filepath.Join(workspaceRoot, "services", "other-repo")
	if err := os.MkdirAll(repoRoot, 0755); err != nil {
		t.Fatalf("mkdir repo root: %v", err)
	}

	skillsRegistry := filepath.Join(workspaceRoot, "services", "loom-core", "mcp", "context", "skills-registry.yaml")
	if err := os.MkdirAll(filepath.Dir(skillsRegistry), 0755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	if err := os.WriteFile(skillsRegistry, []byte("version: 1\nskills: []\n"), 0644); err != nil {
		t.Fatalf("write skills registry: %v", err)
	}

	got := discoverSkillsRegistryPath(repoRoot)
	if got != skillsRegistry {
		t.Fatalf("discoverSkillsRegistryPath()=%q, want %q", got, skillsRegistry)
	}
}

func TestRegenerateSkills_UpdatesRegistryDate(t *testing.T) {
	repoRoot := t.TempDir()
	homeDir := t.TempDir()

	// Create local skills registry with a stale updated date.
	registryPath := filepath.Join(repoRoot, "mcp", "context", "skills-registry.yaml")
	if err := os.MkdirAll(filepath.Dir(registryPath), 0755); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}
	registry := `version: 1
updated: "2026-01-01"
skills:
  - name: test-skill
    categories: [test]
    common:
      description: "test"
      instructions: "Do test work."
    targets:
      codex:
        enabled: true
        type: skill
`
	if err := os.WriteFile(registryPath, []byte(registry), 0644); err != nil {
		t.Fatalf("write skills registry: %v", err)
	}

	m, err := NewManager(repoRoot)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	m.HomeDir = homeDir

	p := &Profile{
		Name:         "codex",
		RepoDir:      ".codex",
		HomeDir:      filepath.Join(homeDir, ".codex"),
		SkillsTarget: "codex",
	}

	if err := m.regenerateSkills(p); err != nil {
		t.Fatalf("regenerateSkills: %v", err)
	}

	updatedBytes, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatalf("read updated registry: %v", err)
	}
	updated := string(updatedBytes)

	today := time.Now().Format("2006-01-02")
	wantQuoted := `updated: "` + today + `"`
	wantBare := "updated: " + today
	if !strings.Contains(updated, wantQuoted) && !strings.Contains(updated, wantBare) {
		t.Fatalf("expected registry to contain %q or %q, got:\n%s", wantQuoted, wantBare, updated)
	}
	if strings.Contains(updated, `updated: "2026-01-01"`) {
		t.Fatalf("expected stale updated date to be replaced, got:\n%s", updated)
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

func TestValidate_McpConfigJsonWithHomeGeneratedFileOverride(t *testing.T) {
	homeDir := t.TempDir()
	profileDir := filepath.Join(homeDir, "test-profile")
	os.MkdirAll(profileDir, 0755)
	validJSON := `{"mcpServers": {"test": {"command": "node", "args": ["server.js"]}}}`
	os.WriteFile(filepath.Join(profileDir, "mcp_config.json"), []byte(validJSON), 0644)

	m, _ := NewManager(t.TempDir())
	m.HomeDir = homeDir
	m.Profiles["test"] = &Profile{
		Name:              "test",
		RepoDir:           "test-repo",
		HomeDir:           filepath.Join(homeDir, "test-profile"),
		GeneratorTarget:   "antigravity",
		GeneratedFile:     "mcp.json",
		HomeGeneratedFile: "mcp_config.json",
	}

	err := m.Validate("test")
	if err != nil {
		t.Errorf("Validate should pass when mcp_config.json exists: %v", err)
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

	err := m.SyncToHome("test", false, false, false, false, "", false, "", false)
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

	err := m.SyncToHome("test", false, false, false, false, "", false, "", false)
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
	err := m.SyncToHome("test", false, false, true, false, "", false, "", false)
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

	err := m.SyncToHome("nonexistent", false, false, false, false, "", false, "", false)
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

	err := m.SyncToHome("vscode", false, false, false, false, "", false, "", false)
	if err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	// Check file was also copied to workspace dir
	workspaceFile := filepath.Join(repoDir, ".vscode", "mcp.json")
	if !Exists(workspaceFile) {
		t.Error("expected mcp.json to be copied to .vscode workspace dir")
	}
}

func TestSyncToHome_GeneratedOnly(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	repoProfDir := filepath.Join(repoDir, "test-profile")
	os.MkdirAll(repoProfDir, 0755)
	os.WriteFile(filepath.Join(repoProfDir, "mcp.json"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(repoProfDir, "extra.txt"), []byte("extra"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir
	m.Profiles["test"] = &Profile{
		Name:              "test",
		RepoDir:           "test-profile",
		HomeDir:           filepath.Join(homeDir, "dest"),
		GeneratedFile:     "mcp.json",
		SyncGeneratedOnly: true,
	}

	err := m.SyncToHome("test", false, false, false, false, "", false, "", false)
	if err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	if !Exists(filepath.Join(homeDir, "dest", "mcp.json")) {
		t.Error("expected mcp.json to be synced")
	}
	if Exists(filepath.Join(homeDir, "dest", "extra.txt")) {
		t.Error("did not expect extra.txt to be synced for generated-only profile")
	}
}

func TestSyncToHome_GeneratedOnly_HomeGeneratedFileOverride(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	repoProfDir := filepath.Join(repoDir, "test-profile")
	os.MkdirAll(repoProfDir, 0755)
	os.WriteFile(filepath.Join(repoProfDir, "mcp.json"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir
	m.Profiles["test"] = &Profile{
		Name:              "test",
		RepoDir:           "test-profile",
		HomeDir:           filepath.Join(homeDir, "dest"),
		GeneratedFile:     "mcp.json",
		HomeGeneratedFile: "mcp_config.json",
		SyncGeneratedOnly: true,
	}

	err := m.SyncToHome("test", false, false, false, false, "", false, "", false)
	if err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	if !Exists(filepath.Join(homeDir, "dest", "mcp_config.json")) {
		t.Error("expected mcp.json to be synced to home as mcp_config.json")
	}
	if Exists(filepath.Join(homeDir, "dest", "mcp.json")) {
		t.Error("did not expect mcp.json in home when HomeGeneratedFile is set")
	}
}

func TestBackup_GeneratedOnly(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	homeProfDir := filepath.Join(homeDir, "app-dir")
	os.MkdirAll(homeProfDir, 0755)
	os.WriteFile(filepath.Join(homeProfDir, "mcp.json"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(homeProfDir, "extra.txt"), []byte("extra"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir
	m.Profiles["test"] = &Profile{
		Name:              "test",
		RepoDir:           "test-profile",
		HomeDir:           homeProfDir,
		GeneratedFile:     "mcp.json",
		SyncGeneratedOnly: true,
	}

	if err := m.Backup("test", "home"); err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	backupRoot := filepath.Join(homeDir, ".config", "loom", "backups", "test")
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		t.Fatalf("failed to read backup dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected backup directory to be created")
	}

	backupDir := filepath.Join(backupRoot, entries[0].Name())
	if !Exists(filepath.Join(backupDir, "mcp.json")) {
		t.Error("expected mcp.json to be in backup")
	}
	if Exists(filepath.Join(backupDir, "extra.txt")) {
		t.Error("did not expect extra.txt to be backed up for generated-only profile")
	}
}

func TestPullFromHome_GeneratedOnly_HomeGeneratedFileOverride(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	homeProfDir := filepath.Join(homeDir, "app-dir")
	os.MkdirAll(homeProfDir, 0755)
	os.WriteFile(filepath.Join(homeProfDir, "mcp_config.json"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir
	m.Profiles["test"] = &Profile{
		Name:              "test",
		RepoDir:           "test-profile",
		HomeDir:           homeProfDir,
		GeneratedFile:     "mcp.json",
		HomeGeneratedFile: "mcp_config.json",
		SyncGeneratedOnly: true,
	}

	err := m.PullFromHome("test", false)
	if err != nil {
		t.Fatalf("PullFromHome failed: %v", err)
	}

	if !Exists(filepath.Join(repoDir, "test-profile", "mcp.json")) {
		t.Error("expected home mcp_config.json to be pulled into repo mcp.json")
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

	err := m.SyncAll(false, false, false, false, "", false, "", nil, false)
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

// =============================================================================
// Profile Default Tests
// =============================================================================

func TestProfile_DefaultLoomMode(t *testing.T) {
	m, _ := NewManager(t.TempDir())

	claude := m.Get("claude")
	if claude == nil {
		t.Fatal("claude profile not found")
	}
	if !claude.DefaultLoomMode {
		t.Error("claude profile should default to loom mode")
	}

	claudeDesktop := m.Get("claude_desktop")
	if claudeDesktop == nil {
		t.Fatal("claude_desktop profile not found")
	}
	if !claudeDesktop.DefaultLoomMode {
		t.Error("claude_desktop profile should default to loom mode")
	}

	codex := m.Get("codex")
	if codex == nil {
		t.Fatal("codex profile not found")
	}
	if !codex.DefaultLoomMode {
		t.Error("codex profile should default to loom mode")
	}

	vscode := m.Get("vscode")
	if vscode == nil {
		t.Fatal("vscode profile not found")
	}
	if !vscode.DefaultLoomMode {
		t.Error("vscode profile should default to loom mode")
	}

	gemini := m.Get("gemini")
	if gemini == nil {
		t.Fatal("gemini profile not found")
	}
	if !gemini.DefaultLoomMode {
		t.Error("gemini profile should default to loom mode")
	}

	antigravity := m.Get("antigravity")
	if antigravity == nil {
		t.Fatal("antigravity profile not found")
	}
	if !antigravity.DefaultLoomMode {
		t.Error("antigravity profile should default to loom mode")
	}
}

func TestProfile_DefaultResolveSecrets(t *testing.T) {
	m, _ := NewManager(t.TempDir())

	codex := m.Get("codex")
	if codex == nil {
		t.Fatal("codex profile not found")
	}
	if !codex.DefaultResolveSecrets {
		t.Error("codex profile should default to resolving secrets")
	}

	kilocode := m.Get("kilocode")
	if kilocode == nil {
		t.Fatal("kilocode profile not found")
	}
	if !kilocode.DefaultResolveSecrets {
		t.Error("kilocode profile should default to resolving secrets")
	}

	claude := m.Get("claude")
	if claude == nil {
		t.Fatal("claude profile not found")
	}
	if claude.DefaultResolveSecrets {
		t.Error("claude profile should NOT default to resolving secrets")
	}

	vscode := m.Get("vscode")
	if vscode == nil {
		t.Fatal("vscode profile not found")
	}
	if vscode.DefaultResolveSecrets {
		t.Error("vscode profile should NOT default to resolving secrets")
	}

	gemini := m.Get("gemini")
	if gemini == nil {
		t.Fatal("gemini profile not found")
	}
	if gemini.DefaultResolveSecrets {
		t.Error("gemini profile should NOT default to resolving secrets")
	}
}

func TestSyncAll_PerProfileDefaults(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create repo directories for profiles with different defaults
	for _, profile := range []string{"profile-loom", "profile-resolve"} {
		profDir := filepath.Join(repoDir, profile)
		os.MkdirAll(profDir, 0755)
		os.WriteFile(filepath.Join(profDir, "config.toml"), []byte(profile), 0644)
	}

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	m.Profiles = map[string]*Profile{
		"profile-loom": {
			Name:            "profile-loom",
			RepoDir:         "profile-loom",
			HomeDir:         filepath.Join(homeDir, "profile-loom"),
			DefaultLoomMode: true,
		},
		"profile-resolve": {
			Name:                  "profile-resolve",
			RepoDir:               "profile-resolve",
			HomeDir:               filepath.Join(homeDir, "profile-resolve"),
			DefaultResolveSecrets: true,
		},
	}

	// SyncAll with nil resolveSecrets (use per-profile defaults)
	err := m.SyncAll(false, false, false, false, "", false, "", nil, false)
	if err != nil {
		t.Fatalf("SyncAll failed: %v", err)
	}

	// Both profiles should be synced
	for _, profile := range []string{"profile-loom", "profile-resolve"} {
		homeFile := filepath.Join(homeDir, profile, "config.toml")
		if !Exists(homeFile) {
			t.Errorf("expected %s to be synced", homeFile)
		}
	}
}

// =============================================================================
// GeneratedDirectToHome Tests
// =============================================================================

func TestCleanRepoGenerated_RemovesStaleFiles(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	p := &Profile{
		Name:                  "codex",
		RepoDir:               ".codex",
		GeneratedFile:         "config.toml",
		ExtraGeneratedFiles:   []string{"settings.json"},
		GeneratedDirectToHome: true,
	}

	repoCodex := filepath.Join(repoDir, ".codex")
	if err := os.MkdirAll(repoCodex, 0o755); err != nil {
		t.Fatalf("mkdir repo codex: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoCodex, "config.toml"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoCodex, "settings.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write stale settings: %v", err)
	}

	m.cleanRepoGenerated(p)

	if Exists(filepath.Join(repoCodex, "config.toml")) {
		t.Error("expected stale config.toml to be removed")
	}
	if Exists(filepath.Join(repoCodex, "settings.json")) {
		t.Error("expected stale settings.json to be removed")
	}
}

func TestSyncToHome_RepoOnlySkipsHomeOnlyCodex(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	homeCodex := filepath.Join(homeDir, ".codex")
	if err := os.MkdirAll(homeCodex, 0o755); err != nil {
		t.Fatalf("mkdir home codex: %v", err)
	}

	if err := m.SyncToHome("codex", false, false, true, false, "", false, "", false); err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	if Exists(filepath.Join(homeCodex, "config.toml")) {
		t.Error("did not expect repo-only sync to write home config")
	}
}

func TestSyncAllProjects_StripsHomeManagedSettingsKeys(t *testing.T) {
	repoDir := t.TempDir()
	workspaceDir := t.TempDir()

	m, _ := NewManager(repoDir)

	projectSettings := filepath.Join(workspaceDir, "project-a", ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(projectSettings), 0o755); err != nil {
		t.Fatalf("mkdir project settings dir: %v", err)
	}
	content := []byte(`{"hooks":{"session":true},"permissions":{"allow":["Bash"]},"theme":"dark"}`)
	if err := os.WriteFile(projectSettings, content, 0o644); err != nil {
		t.Fatalf("write project settings: %v", err)
	}

	updated, err := m.SyncAllProjects("claude", workspaceDir, false, false)
	if err != nil {
		t.Fatalf("SyncAllProjects failed: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}

	got, err := os.ReadFile(projectSettings)
	if err != nil {
		t.Fatalf("read project settings: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("parse stripped settings: %v", err)
	}
	if _, ok := parsed["hooks"]; ok {
		t.Fatal("expected hooks removed from workspace project settings")
	}
	if _, ok := parsed["permissions"]; ok {
		t.Fatal("expected permissions removed from workspace project settings")
	}
	if parsed["theme"] != "dark" {
		t.Fatalf("expected theme preserved, got %#v", parsed["theme"])
	}
}

func TestCleanAllProjectsGenerated_RemovesWorkspaceCopiesForHomeManagedProfiles(t *testing.T) {
	repoDir := t.TempDir()
	workspaceDir := t.TempDir()

	m, _ := NewManager(repoDir)

	projectConfig := filepath.Join(workspaceDir, "project-a", ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	if err := os.WriteFile(projectConfig, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	updated, err := m.CleanAllProjectsGenerated("codex", workspaceDir, false, false)
	if err != nil {
		t.Fatalf("CleanAllProjectsGenerated failed: %v", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}
	if Exists(projectConfig) {
		t.Fatal("expected stale project codex config to be removed")
	}
}

func TestRegenerate_AutoCleansStaleProjectConfigs(t *testing.T) {
	repoDir := t.TempDir()
	workspaceDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.WorkspaceRoot = workspaceDir

	// Create a stale codex config in a workspace sub-project
	projectConfig := filepath.Join(workspaceDir, "project-a", ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatalf("mkdir project config dir: %v", err)
	}
	if err := os.WriteFile(projectConfig, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	// Verify the stale config exists before Regenerate
	if !Exists(projectConfig) {
		t.Fatal("expected stale project codex config to exist before test")
	}

	// Regenerate will fail because there's no real registry, but the cleanup
	// happens before generation — test that cleanRepoGenerated + CleanAllProjectsGenerated
	// are called for GeneratedDirectToHome profiles by calling them directly
	// (matching what Regenerate does).
	p, err := m.GetProfile("codex")
	if err != nil {
		t.Fatalf("GetProfile codex: %v", err)
	}
	if !p.GeneratedDirectToHome {
		t.Fatal("expected codex profile to have GeneratedDirectToHome=true")
	}

	// Simulate what Regenerate does for GeneratedDirectToHome profiles
	m.cleanRepoGenerated(p)
	if m.WorkspaceRoot != "" {
		n, cleanErr := m.CleanAllProjectsGenerated(p.Name, m.WorkspaceRoot, false, false)
		if cleanErr != nil {
			t.Fatalf("CleanAllProjectsGenerated failed: %v", cleanErr)
		}
		if n != 1 {
			t.Fatalf("CleanAllProjectsGenerated cleaned %d, want 1", n)
		}
	}

	if Exists(projectConfig) {
		t.Fatal("expected stale project codex config to be removed during regen cleanup")
	}
}

// =============================================================================
// SkillsDirectToHome Tests
// =============================================================================

func TestCleanRepoSkills_RemovesStaleFiles(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	p := &Profile{
		Name:               "gemini",
		RepoDir:            ".gemini",
		HomeDir:            ".gemini",
		SkillsDirectToHome: true,
	}

	repoGemini := filepath.Join(repoDir, ".gemini")

	// Create stale repo skill files
	skillsDir := filepath.Join(repoGemini, "skills", "test-skill")
	os.MkdirAll(skillsDir, 0755)
	os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte("stale"), 0644)

	// Create stale manifest
	os.WriteFile(filepath.Join(repoGemini, skills.ManifestFilename), []byte("{}"), 0644)

	// Create stale instructions.md
	os.WriteFile(filepath.Join(repoGemini, "instructions.md"), []byte("stale"), 0644)

	m.cleanRepoSkills(p)

	if Exists(filepath.Join(repoGemini, "skills")) {
		t.Error("expected skills directory to be removed")
	}
	if Exists(filepath.Join(repoGemini, skills.ManifestFilename)) {
		t.Error("expected manifest to be removed")
	}
	if Exists(filepath.Join(repoGemini, "instructions.md")) {
		t.Error("expected instructions.md to be removed")
	}
}

func TestCleanRepoSkills_NoopWhenNothingExists(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	p := &Profile{
		Name:               "gemini",
		RepoDir:            ".gemini",
		SkillsDirectToHome: true,
	}

	// Should not panic or error when nothing exists
	m.cleanRepoSkills(p)
}

func TestSyncToHome_SkillsDirectToHome_SkipsRepoCopy(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	repoGemini := filepath.Join(repoDir, ".gemini")
	homeGemini := filepath.Join(homeDir, ".gemini")
	os.MkdirAll(repoGemini, 0755)
	os.MkdirAll(homeGemini, 0755)

	// Create generated config files
	os.WriteFile(filepath.Join(repoGemini, "config.toml"), []byte("[mcp_servers]\n"), 0644)
	os.WriteFile(filepath.Join(repoGemini, "settings.json"), []byte("{}\n"), 0644)
	os.WriteFile(filepath.Join(homeGemini, "trustedFolders.json"), []byte("{}\n"), 0600)

	// Create a skill manifest in the REPO (simulating pre-existing state)
	manifest := skills.Manifest{
		Platform:  "gemini",
		Generated: []string{"skills/test/SKILL.md"},
	}
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	os.WriteFile(filepath.Join(repoGemini, skills.ManifestFilename), manifestData, 0644)

	// Create the skill file in repo
	os.MkdirAll(filepath.Join(repoGemini, "skills", "test"), 0755)
	os.WriteFile(filepath.Join(repoGemini, "skills", "test", "SKILL.md"), []byte("repo skill"), 0644)

	p := m.Get("gemini")
	// Override to use direct-to-home
	p.SkillsDirectToHome = true

	err := m.SyncToHome("gemini", false, false, false, false, "", false, "", false)
	if err != nil {
		t.Fatalf("SyncToHome failed: %v", err)
	}

	// The repo skill should NOT have been copied to home (direct-to-home skips the copy)
	homeSkill := filepath.Join(homeGemini, "skills", "test", "SKILL.md")
	if Exists(homeSkill) {
		content, _ := os.ReadFile(homeSkill)
		if string(content) == "repo skill" {
			t.Error("repo skill should not have been copied to home when SkillsDirectToHome is true")
		}
	}
}

// =============================================================================
// Gemini MCP Extension Pruning Tests
// =============================================================================

func TestPruneGeminiMCPExtensions_RemovesMCPExtensions(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")

	// Create an extension with mcpServers (should be pruned)
	mcpExt := filepath.Join(homeGemini, "extensions", "gemini-cli-security")
	os.MkdirAll(mcpExt, 0755)
	os.WriteFile(filepath.Join(mcpExt, "gemini-extension.json"), []byte(`{
		"name": "gemini-cli-security",
		"mcpServers": {"osvScanner": {"command": "osv-scanner"}}
	}`), 0644)

	// Create an extension without mcpServers (should be preserved)
	noMCPExt := filepath.Join(homeGemini, "extensions", "code-review")
	os.MkdirAll(noMCPExt, 0755)
	os.WriteFile(filepath.Join(noMCPExt, "gemini-extension.json"), []byte(`{
		"name": "code-review",
		"commands": [{"name": "review"}]
	}`), 0644)

	// Create extension-enablement.json
	os.WriteFile(filepath.Join(homeGemini, "extensions", "extension-enablement.json"),
		[]byte(`{"gemini-cli-security": true, "code-review": true}`), 0600)

	pruned, err := pruneGeminiMCPExtensions(homeGemini)
	if err != nil {
		t.Fatalf("pruneGeminiMCPExtensions failed: %v", err)
	}

	if len(pruned) != 1 || pruned[0] != "gemini-cli-security" {
		t.Fatalf("expected [gemini-cli-security] pruned, got %v", pruned)
	}

	if Exists(mcpExt) {
		t.Error("expected gemini-cli-security extension to be removed")
	}
	if !Exists(noMCPExt) {
		t.Error("expected code-review extension to be preserved")
	}

	// Check enablement was updated
	enableData, err := os.ReadFile(filepath.Join(homeGemini, "extensions", "extension-enablement.json"))
	if err != nil {
		t.Fatalf("read enablement: %v", err)
	}
	var enablement map[string]any
	json.Unmarshal(enableData, &enablement)
	if _, ok := enablement["gemini-cli-security"]; ok {
		t.Error("expected gemini-cli-security removed from enablement")
	}
	if _, ok := enablement["code-review"]; !ok {
		t.Error("expected code-review preserved in enablement")
	}
}

func TestPruneGeminiMCPExtensions_NoExtensionsDir(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")
	os.MkdirAll(homeGemini, 0755)

	pruned, err := pruneGeminiMCPExtensions(homeGemini)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pruned) != 0 {
		t.Fatalf("expected no pruned extensions, got %v", pruned)
	}
}

func TestPruneGeminiMCPExtensions_PreservesNonMCPExtensions(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")

	// Extension with only commands (no mcpServers)
	ext := filepath.Join(homeGemini, "extensions", "prompt-library")
	os.MkdirAll(ext, 0755)
	os.WriteFile(filepath.Join(ext, "gemini-extension.json"), []byte(`{"name": "prompt-library"}`), 0644)

	pruned, err := pruneGeminiMCPExtensions(homeGemini)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pruned) != 0 {
		t.Fatalf("expected no pruned extensions, got %v", pruned)
	}
	if !Exists(ext) {
		t.Error("expected prompt-library to be preserved")
	}
}

func TestRemoveFromExtensionEnablement_RemovesEntries(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")
	os.MkdirAll(filepath.Join(homeGemini, "extensions"), 0755)

	path := filepath.Join(homeGemini, "extensions", "extension-enablement.json")
	os.WriteFile(path, []byte(`{"foo": true, "bar": false, "baz": true}`), 0600)

	err := removeFromExtensionEnablement(homeGemini, []string{"foo", "baz"})
	if err != nil {
		t.Fatalf("removeFromExtensionEnablement failed: %v", err)
	}

	data, _ := os.ReadFile(path)
	var obj map[string]any
	json.Unmarshal(data, &obj)

	if _, ok := obj["foo"]; ok {
		t.Error("expected foo removed")
	}
	if _, ok := obj["baz"]; ok {
		t.Error("expected baz removed")
	}
	if _, ok := obj["bar"]; !ok {
		t.Error("expected bar preserved")
	}
}

func TestRemoveFromExtensionEnablement_NoFileIsNoop(t *testing.T) {
	homeDir := t.TempDir()
	homeGemini := filepath.Join(homeDir, ".gemini")
	os.MkdirAll(homeGemini, 0755)

	err := removeFromExtensionEnablement(homeGemini, []string{"foo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFilterPrunedExtensions_RemovesPrunedFromSnapshot(t *testing.T) {
	snapshot := geminiConfigSnapshot{
		extensionManifests: map[string][]byte{
			"extensions/context7/gemini-extension.json":    []byte("{}"),
			"extensions/code-review/gemini-extension.json": []byte("{}"),
			"extensions/redis/gemini-extension.json":       []byte("{}"),
		},
	}

	filtered := filterPrunedExtensions(snapshot, []string{"context7", "redis"})

	if len(filtered.extensionManifests) != 1 {
		t.Fatalf("expected 1 manifest remaining, got %d", len(filtered.extensionManifests))
	}
	if _, ok := filtered.extensionManifests["extensions/code-review/gemini-extension.json"]; !ok {
		t.Error("expected code-review to be preserved")
	}
}

func TestFilterPrunedExtensions_NoPrunedIsNoop(t *testing.T) {
	snapshot := geminiConfigSnapshot{
		extensionManifests: map[string][]byte{
			"extensions/code-review/gemini-extension.json": []byte("{}"),
		},
	}

	filtered := filterPrunedExtensions(snapshot, nil)

	if len(filtered.extensionManifests) != 1 {
		t.Fatalf("expected snapshot unchanged, got %d manifests", len(filtered.extensionManifests))
	}
}

func TestAllSkillProfiles_HaveSkillsDirectToHome(t *testing.T) {
	m, _ := NewManager(t.TempDir())

	tests := []struct {
		name     string
		homePath string
	}{
		{"gemini", "$HOME/.gemini/skills"},
		{"antigravity", "$HOME/.gemini/antigravity/skills"},
		{"claude", "$HOME/.claude/commands"},
		{"kilocode", "$HOME/.kilocode/skills"},
		{"codex", "$HOME/.codex/skills"},
		{"opencode", "$HOME/.config/opencode/skills"},
		{"zed", "$HOME/.config/zed/skills"},
	}

	for _, tt := range tests {
		p := m.Get(tt.name)
		if p == nil {
			t.Fatalf("%s profile not found", tt.name)
		}
		if !p.SkillsDirectToHome {
			t.Errorf("%s profile should have SkillsDirectToHome=true", tt.name)
		}
		if p.SkillsHomePath != tt.homePath {
			t.Errorf("%s SkillsHomePath = %q, want %q", tt.name, p.SkillsHomePath, tt.homePath)
		}
	}

	// Verify antigravity uses gemini skills target
	antigravity := m.Get("antigravity")
	if antigravity.SkillsTarget != "gemini" {
		t.Errorf("antigravity SkillsTarget = %q, want %q", antigravity.SkillsTarget, "gemini")
	}
}

func TestCodexProfile_HasHomeOnlySkills(t *testing.T) {
	m, _ := NewManager(t.TempDir())

	codex := m.Get("codex")
	if codex == nil {
		t.Fatal("codex profile not found")
	}
	if !codex.SkillsDirectToHome {
		t.Error("codex profile should have SkillsDirectToHome=true")
	}
	if codex.SkillsHomePath != "$HOME/.codex/skills" {
		t.Errorf("codex SkillsHomePath = %q, want %q", codex.SkillsHomePath, "$HOME/.codex/skills")
	}
}

// =============================================================================
// Drift Detection Tests
// =============================================================================

func TestConfigInSyncIgnoringKeys_HooksDifferOnly(t *testing.T) {
	tests := []struct {
		name       string
		repo       string
		home       string
		ignoreKeys []string
		wantSync   bool
	}{
		{
			name:       "differ only in hooks — in sync",
			repo:       `{"theme":"dark"}`,
			home:       `{"theme":"dark","hooks":{"preToolUse":"echo hi"}}`,
			ignoreKeys: []string{"hooks"},
			wantSync:   true,
		},
		{
			name:       "differ in other keys — out of sync",
			repo:       `{"theme":"dark"}`,
			home:       `{"theme":"light","hooks":{"preToolUse":"echo hi"}}`,
			ignoreKeys: []string{"hooks"},
			wantSync:   false,
		},
		{
			name:       "identical — in sync",
			repo:       `{"theme":"dark"}`,
			home:       `{"theme":"dark"}`,
			ignoreKeys: []string{"hooks"},
			wantSync:   true,
		},
		{
			name:       "multiple ignored keys",
			repo:       `{"theme":"dark"}`,
			home:       `{"theme":"dark","hooks":{},"notify":{"url":"http://example.com"}}`,
			ignoreKeys: []string{"hooks", "notify"},
			wantSync:   true,
		},
		{
			name:       "invalid JSON repo — not in sync",
			repo:       `not json`,
			home:       `{"theme":"dark"}`,
			ignoreKeys: []string{"hooks"},
			wantSync:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			repoFile := filepath.Join(dir, "repo.json")
			homeFile := filepath.Join(dir, "home.json")

			os.WriteFile(repoFile, []byte(tt.repo), 0644)
			os.WriteFile(homeFile, []byte(tt.home), 0644)

			got := configInSyncIgnoringKeys(repoFile, homeFile, tt.ignoreKeys)
			if got != tt.wantSync {
				t.Errorf("configInSyncIgnoringKeys() = %v, want %v", got, tt.wantSync)
			}
		})
	}
}

func TestTomlInSyncIgnoringKeys_NotifyDiffers(t *testing.T) {
	tests := []struct {
		name       string
		repo       string
		home       string
		ignoreKeys []string
		wantSync   bool
	}{
		{
			name:       "differ only in notify — in sync",
			repo:       "[mcp_servers.test]\ncommand = \"echo\"\n",
			home:       "[mcp_servers.test]\ncommand = \"echo\"\n\n[notify]\nurl = \"http://localhost:3333\"\n",
			ignoreKeys: []string{"notify"},
			wantSync:   true,
		},
		{
			name:       "differ in mcp_servers — out of sync",
			repo:       "[mcp_servers.test]\ncommand = \"echo\"\n",
			home:       "[mcp_servers.other]\ncommand = \"node\"\n\n[notify]\nurl = \"http://localhost:3333\"\n",
			ignoreKeys: []string{"notify"},
			wantSync:   false,
		},
		{
			name:       "identical — in sync",
			repo:       "[mcp_servers.test]\ncommand = \"echo\"\n",
			home:       "[mcp_servers.test]\ncommand = \"echo\"\n",
			ignoreKeys: []string{"notify"},
			wantSync:   true,
		},
		{
			name:       "nested notify section — in sync",
			repo:       "[mcp_servers.test]\ncommand = \"echo\"\n",
			home:       "[mcp_servers.test]\ncommand = \"echo\"\n\n[notify]\nurl = \"http://localhost\"\n\n[notify.hooks]\non_start = true\n",
			ignoreKeys: []string{"notify"},
			wantSync:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			repoFile := filepath.Join(dir, "repo.toml")
			homeFile := filepath.Join(dir, "home.toml")

			os.WriteFile(repoFile, []byte(tt.repo), 0644)
			os.WriteFile(homeFile, []byte(tt.home), 0644)

			got := tomlInSyncIgnoringKeys(repoFile, homeFile, tt.ignoreKeys)
			if got != tt.wantSync {
				t.Errorf("tomlInSyncIgnoringKeys() = %v, want %v", got, tt.wantSync)
			}
		})
	}
}

func TestDriftSummary(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir

	// Clear default profiles.
	m.Profiles = map[string]*Profile{}

	// Profile 1: in-sync (both dirs exist with matching generated file)
	repo1 := filepath.Join(repoDir, "p1")
	home1 := filepath.Join(homeDir, "p1")
	os.MkdirAll(repo1, 0755)
	os.MkdirAll(home1, 0755)
	os.WriteFile(filepath.Join(repo1, "mcp.json"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(home1, "mcp.json"), []byte("content"), 0644)
	m.Profiles["p1"] = &Profile{
		Name:              "p1",
		RepoDir:           "p1",
		HomeDir:           filepath.Join(homeDir, "p1"),
		GeneratedFile:     "mcp.json",
		SyncGeneratedOnly: true,
	}

	// Profile 2: out-of-sync (content differs)
	repo2 := filepath.Join(repoDir, "p2")
	home2 := filepath.Join(homeDir, "p2")
	os.MkdirAll(repo2, 0755)
	os.MkdirAll(home2, 0755)
	os.WriteFile(filepath.Join(repo2, "config.toml"), []byte("repo"), 0644)
	os.WriteFile(filepath.Join(home2, "config.toml"), []byte("home-different"), 0644)
	m.Profiles["p2"] = &Profile{
		Name:              "p2",
		RepoDir:           "p2",
		HomeDir:           filepath.Join(homeDir, "p2"),
		GeneratedFile:     "config.toml",
		SyncGeneratedOnly: true,
	}

	// Profile 3: missing (home dir doesn't exist)
	repo3 := filepath.Join(repoDir, "p3")
	os.MkdirAll(repo3, 0755)
	os.WriteFile(filepath.Join(repo3, "mcp.json"), []byte("content"), 0644)
	m.Profiles["p3"] = &Profile{
		Name:              "p3",
		RepoDir:           "p3",
		HomeDir:           filepath.Join(homeDir, "p3-nonexistent"),
		GeneratedFile:     "mcp.json",
		SyncGeneratedOnly: true,
	}

	inSync, outOfSync, missing, err := m.DriftSummary()
	if err != nil {
		t.Fatalf("DriftSummary failed: %v", err)
	}

	if inSync != 1 {
		t.Errorf("inSync = %d, want 1", inSync)
	}
	if outOfSync != 1 {
		t.Errorf("outOfSync = %d, want 1", outOfSync)
	}
	if missing != 1 {
		t.Errorf("missing = %d, want 1", missing)
	}
}

func TestSkillsDirectToHome_AllProfiles_HaveHomePath(t *testing.T) {
	m, _ := NewManager(t.TempDir())

	for _, name := range m.List() {
		p := m.Get(name)
		if p.SkillsDirectToHome && p.SkillsHomePath == "" {
			t.Errorf("profile %q has SkillsDirectToHome=true but empty SkillsHomePath", name)
		}
	}
}

func TestCompareHomeGeneratedFiles_ReportsMissingExtras(t *testing.T) {
	homeDir := t.TempDir()

	// Create profile with primary and extra generated files.
	profile := &Profile{
		Name:                  "test",
		GeneratedFile:         "config.toml",
		ExtraGeneratedFiles:   []string{"settings.json"},
		GeneratedDirectToHome: true,
	}

	// Only the primary exists, extras are missing — should not report drift.
	homeProfile := filepath.Join(homeDir, "test")
	os.MkdirAll(homeProfile, 0755)
	os.WriteFile(filepath.Join(homeProfile, "config.toml"), []byte("content"), 0644)

	items := compareHomeGeneratedFiles(homeProfile, profile)

	// Should report the primary as in-sync and the missing extra as drift.
	if len(items) != 2 {
		t.Fatalf("expected 2 drift items, got %d: %+v", len(items), items)
	}
	foundPrimary := false
	foundMissingExtra := false
	for _, item := range items {
		switch item.File {
		case "config.toml":
			foundPrimary = item.Status == DriftInSync
		case "settings.json":
			foundMissingExtra = item.Status == DriftMissing
		}
	}
	if !foundPrimary {
		t.Fatal("expected primary config.toml to be in sync")
	}
	if !foundMissingExtra {
		t.Fatal("expected missing settings.json extra artifact to be reported")
	}

	// Now also create the extra file — should report 2 in-sync items.
	os.WriteFile(filepath.Join(homeProfile, "settings.json"), []byte("{}"), 0644)

	items = compareHomeGeneratedFiles(homeProfile, profile)
	if len(items) != 2 {
		t.Fatalf("expected 2 drift items, got %d: %+v", len(items), items)
	}
	for _, item := range items {
		if item.Status != DriftInSync {
			t.Errorf("file %q status = %v, want DriftInSync", item.File, item.Status)
		}
	}
}
