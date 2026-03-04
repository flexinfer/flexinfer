package sync

import (
	"os"
	"path/filepath"
	"testing"
)

// =============================================================================
// DriftStatus Tests
// =============================================================================

func TestDriftStatus_String(t *testing.T) {
	tests := []struct {
		status DriftStatus
		want   string
	}{
		{DriftInSync, "in-sync"},
		{DriftOutOfSync, "out-of-sync"},
		{DriftMissing, "missing"},
		{DriftExtra, "extra"},
		{DriftStatus(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.status.String()
		if got != tt.want {
			t.Errorf("DriftStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

// =============================================================================
// hashFile Tests
// =============================================================================

func TestHashFile_ConsistentHash(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	hash1, err := hashFile(testFile)
	if err != nil {
		t.Fatalf("hashFile failed: %v", err)
	}

	hash2, err := hashFile(testFile)
	if err != nil {
		t.Fatalf("hashFile second call failed: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("hash not consistent: %q != %q", hash1, hash2)
	}

	// Hash should be 16 hex characters (8 bytes)
	if len(hash1) != 16 {
		t.Errorf("hash length = %d, want 16", len(hash1))
	}
}

func TestHashFile_DifferentContent(t *testing.T) {
	tmpDir := t.TempDir()
	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")

	os.WriteFile(file1, []byte("content one"), 0644)
	os.WriteFile(file2, []byte("content two"), 0644)

	hash1, _ := hashFile(file1)
	hash2, _ := hashFile(file2)

	if hash1 == hash2 {
		t.Error("different content should produce different hashes")
	}
}

func TestHashFile_NonExistent(t *testing.T) {
	_, err := hashFile("/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// =============================================================================
// shouldExclude Tests
// =============================================================================

func TestShouldExclude_ExactMatch(t *testing.T) {
	m, _ := NewManager("/tmp/repo")
	profile := &Profile{
		Excludes: []string{"auth.json", "sessions"},
	}

	if !m.shouldExclude("auth.json", profile) {
		t.Error("should exclude auth.json")
	}
	if !m.shouldExclude("sessions", profile) {
		t.Error("should exclude sessions")
	}
}

func TestShouldExclude_BaseNameMatch(t *testing.T) {
	m, _ := NewManager("/tmp/repo")
	profile := &Profile{
		SecretFiles: []string{"auth.json"},
	}

	// Should match even in subdirectory
	if !m.shouldExclude("subdir/auth.json", profile) {
		t.Error("should exclude subdir/auth.json by basename (secret file)")
	}
}

func TestShouldExclude_SecretFiles(t *testing.T) {
	m, _ := NewManager("/tmp/repo")
	profile := &Profile{
		SecretFiles: []string{"credentials.json"},
	}

	if !m.shouldExclude("credentials.json", profile) {
		t.Error("should exclude secret file")
	}
	if !m.shouldExclude("subdir/credentials.json", profile) {
		t.Error("should exclude secret file by basename")
	}
}

func TestShouldExclude_NoMatch(t *testing.T) {
	m, _ := NewManager("/tmp/repo")
	profile := &Profile{
		Excludes:    []string{"auth.json"},
		SecretFiles: []string{"secret.key"},
	}

	if m.shouldExclude("config.toml", profile) {
		t.Error("should not exclude config.toml")
	}
	if m.shouldExclude("settings.json", profile) {
		t.Error("should not exclude settings.json")
	}
}

// =============================================================================
// compareDirectories Tests
// =============================================================================

func TestCompareDirectories_IdenticalDirs(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create identical files
	os.WriteFile(filepath.Join(repoDir, "config.toml"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(homeDir, "config.toml"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	profile := &Profile{}

	items := m.compareDirectories(repoDir, homeDir, profile)

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Status != DriftInSync {
		t.Errorf("expected DriftInSync, got %s", items[0].Status)
	}
}

func TestCompareDirectories_DifferentContent(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	os.WriteFile(filepath.Join(repoDir, "config.toml"), []byte("repo content"), 0644)
	os.WriteFile(filepath.Join(homeDir, "config.toml"), []byte("home content"), 0644)

	m, _ := NewManager(repoDir)
	profile := &Profile{}

	items := m.compareDirectories(repoDir, homeDir, profile)

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Status != DriftOutOfSync {
		t.Errorf("expected DriftOutOfSync, got %s", items[0].Status)
	}
	if items[0].File != "config.toml" {
		t.Errorf("expected file config.toml, got %s", items[0].File)
	}
}

func TestCompareDirectories_MissingInHome(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	os.WriteFile(filepath.Join(repoDir, "config.toml"), []byte("content"), 0644)
	// No file in homeDir

	m, _ := NewManager(repoDir)
	profile := &Profile{}

	items := m.compareDirectories(repoDir, homeDir, profile)

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Status != DriftMissing {
		t.Errorf("expected DriftMissing, got %s", items[0].Status)
	}
}

func TestCompareDirectories_ExtraInHome(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// No file in repoDir
	os.WriteFile(filepath.Join(homeDir, "extra.txt"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	profile := &Profile{}

	items := m.compareDirectories(repoDir, homeDir, profile)

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Status != DriftExtra {
		t.Errorf("expected DriftExtra, got %s", items[0].Status)
	}
}

func TestCompareDirectories_ExcludedFiles(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create files that should be excluded
	os.WriteFile(filepath.Join(repoDir, "auth.json"), []byte("secret"), 0644)
	os.WriteFile(filepath.Join(homeDir, "auth.json"), []byte("different secret"), 0644)

	// Create file that should be compared
	os.WriteFile(filepath.Join(repoDir, "config.toml"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(homeDir, "config.toml"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	profile := &Profile{
		Excludes: []string{"auth.json"},
	}

	items := m.compareDirectories(repoDir, homeDir, profile)

	// Should only have config.toml, not auth.json
	if len(items) != 1 {
		t.Fatalf("expected 1 item (excluding auth.json), got %d", len(items))
	}
	if items[0].File != "config.toml" {
		t.Errorf("expected config.toml, got %s", items[0].File)
	}
}

func TestCompareDirectories_NestedFiles(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create nested structure
	os.MkdirAll(filepath.Join(repoDir, "subdir"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(repoDir, "subdir", "nested.txt"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(homeDir, "subdir", "nested.txt"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	profile := &Profile{}

	items := m.compareDirectories(repoDir, homeDir, profile)

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].File != filepath.Join("subdir", "nested.txt") {
		t.Errorf("expected nested path, got %s", items[0].File)
	}
	if items[0].Status != DriftInSync {
		t.Errorf("expected DriftInSync, got %s", items[0].Status)
	}
}

// =============================================================================
// GetSyncStatus Tests
// =============================================================================

func TestGetSyncStatus_UnknownProfile(t *testing.T) {
	m, _ := NewManager("/tmp/repo")

	status, err := m.GetSyncStatus("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != nil {
		t.Error("expected nil status for unknown profile")
	}
}

func TestGetSyncStatus_RepoNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	// Add a test profile with non-existent repo dir
	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "nonexistent",
		HomeDir: tmpDir,
	}

	status, err := m.GetSyncStatus("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.RepoExists {
		t.Error("expected RepoExists = false")
	}
	if status.InSync {
		t.Error("expected InSync = false when repo doesn't exist")
	}
}

func TestGetSyncStatus_HomeNotExists(t *testing.T) {
	repoDir := t.TempDir()
	m, _ := NewManager(repoDir)

	// Create repo directory but not home
	os.MkdirAll(filepath.Join(repoDir, "test-profile"), 0755)

	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-profile",
		HomeDir: "/nonexistent/path",
	}

	status, err := m.GetSyncStatus("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !status.RepoExists {
		t.Error("expected RepoExists = true")
	}
	if status.HomeExists {
		t.Error("expected HomeExists = false")
	}
	if status.InSync {
		t.Error("expected InSync = false when home doesn't exist")
	}
}

func TestGetSyncStatus_InSync(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create matching files
	profileDir := filepath.Join(repoDir, "test-profile")
	os.MkdirAll(profileDir, 0755)
	os.WriteFile(filepath.Join(profileDir, "config.toml"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(homeDir, "config.toml"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-profile",
		HomeDir: homeDir,
	}

	status, err := m.GetSyncStatus("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !status.RepoExists {
		t.Error("expected RepoExists = true")
	}
	if !status.HomeExists {
		t.Error("expected HomeExists = true")
	}
	if !status.InSync {
		t.Error("expected InSync = true")
	}
	if status.Profile != "test" {
		t.Errorf("expected Profile = test, got %s", status.Profile)
	}
}

func TestGetSyncStatus_ResolvesRelativeHomeDir(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Ensure we don't accidentally resolve relative paths against the test CWD.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	defer func() { _ = os.Chdir(origWD) }()
	_ = os.Chdir(t.TempDir())

	// Create matching files
	repoProfileDir := filepath.Join(repoDir, "test-profile")
	homeProfileDir := filepath.Join(homeDir, ".test-home")
	os.MkdirAll(repoProfileDir, 0755)
	os.MkdirAll(homeProfileDir, 0755)
	os.WriteFile(filepath.Join(repoProfileDir, "mcp.json"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(homeProfileDir, "mcp.json"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir
	m.Profiles["test"] = &Profile{
		Name:              "test",
		RepoDir:           "test-profile",
		HomeDir:           ".test-home",
		GeneratedFile:     "mcp.json",
		SyncGeneratedOnly: true,
	}

	status, err := m.GetSyncStatus("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.HomeExists {
		t.Fatalf("expected HomeExists = true, got false (homePath=%s)", status.HomePath)
	}
	if !status.InSync {
		t.Fatalf("expected InSync = true, got false (drift=%v)", status.DriftDetails)
	}
	if len(status.DriftDetails) != 1 || status.DriftDetails[0].File != "mcp.json" {
		t.Fatalf("expected single drift item for mcp.json, got %v", status.DriftDetails)
	}
}

func TestGetSyncStatus_UsesHomeGeneratedFileOverride(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	repoProfileDir := filepath.Join(repoDir, "test-profile")
	homeProfileDir := filepath.Join(homeDir, ".test-home")
	os.MkdirAll(repoProfileDir, 0755)
	os.MkdirAll(homeProfileDir, 0755)
	os.WriteFile(filepath.Join(repoProfileDir, "mcp.json"), []byte("content"), 0644)
	os.WriteFile(filepath.Join(homeProfileDir, "mcp_config.json"), []byte("content"), 0644)

	m, _ := NewManager(repoDir)
	m.HomeDir = homeDir
	m.Profiles["test"] = &Profile{
		Name:              "test",
		RepoDir:           "test-profile",
		HomeDir:           ".test-home",
		GeneratedFile:     "mcp.json",
		HomeGeneratedFile: "mcp_config.json",
		SyncGeneratedOnly: true,
	}

	status, err := m.GetSyncStatus("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.InSync {
		t.Fatalf("expected InSync = true, got false (drift=%v)", status.DriftDetails)
	}
	if len(status.DriftDetails) != 1 || status.DriftDetails[0].Status != DriftInSync {
		t.Fatalf("expected single in-sync item, got %v", status.DriftDetails)
	}
}

func TestGetSyncStatus_OutOfSync(t *testing.T) {
	repoDir := t.TempDir()
	homeDir := t.TempDir()

	// Create different files
	profileDir := filepath.Join(repoDir, "test-profile")
	os.MkdirAll(profileDir, 0755)
	os.WriteFile(filepath.Join(profileDir, "config.toml"), []byte("repo content"), 0644)
	os.WriteFile(filepath.Join(homeDir, "config.toml"), []byte("home content"), 0644)

	m, _ := NewManager(repoDir)
	m.Profiles["test"] = &Profile{
		Name:    "test",
		RepoDir: "test-profile",
		HomeDir: homeDir,
	}

	status, err := m.GetSyncStatus("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.InSync {
		t.Error("expected InSync = false when content differs")
	}
	if len(status.DriftDetails) == 0 {
		t.Error("expected DriftDetails to contain items")
	}

	// Find the out-of-sync item
	found := false
	for _, item := range status.DriftDetails {
		if item.File == "config.toml" && item.Status == DriftOutOfSync {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find out-of-sync config.toml in DriftDetails")
	}
}

// =============================================================================
// GetAllSyncStatus Tests
// =============================================================================

func TestGetAllSyncStatus_ReturnsAllProfiles(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	// Create directories for some profiles
	for _, name := range []string{"claude", "codex"} {
		profile := m.Profiles[name]
		if profile != nil {
			os.MkdirAll(filepath.Join(tmpDir, profile.RepoDir), 0755)
		}
	}

	statuses, err := m.GetAllSyncStatus()
	if err != nil {
		t.Fatalf("GetAllSyncStatus failed: %v", err)
	}

	// Should have statuses for all profiles
	if len(statuses) != len(m.Profiles) {
		t.Errorf("expected %d statuses, got %d", len(m.Profiles), len(statuses))
	}
}
