package agentcontext

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirDiskUsage(t *testing.T) {
	dir := t.TempDir()

	// Create some files
	writeFile(t, filepath.Join(dir, "a.txt"), 100)
	writeFile(t, filepath.Join(dir, "b.txt"), 200)

	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sub, "c.txt"), 50)

	size, err := dirDiskUsage(dir)
	if err != nil {
		t.Fatal(err)
	}

	if size != 350 {
		t.Errorf("dirDiskUsage = %d, want 350", size)
	}
}

func TestDirDiskUsage_NonExistentDir(t *testing.T) {
	size, _ := dirDiskUsage("/nonexistent/path/should/not/exist")
	// filepath.Walk returns the error via the callback; dirDiskUsage skips inaccessible entries
	// so for a non-existent root the Walk itself returns an error, yielding 0 bytes
	if size != 0 {
		t.Errorf("dirDiskUsage(nonexistent) = %d, want 0", size)
	}
}

func TestDirDiskUsage_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	size, err := dirDiskUsage(dir)
	if err != nil {
		t.Fatal(err)
	}
	if size != 0 {
		t.Errorf("dirDiskUsage(empty) = %d, want 0", size)
	}
}

func TestParseArtifactPatterns(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{".next,node_modules,target", 3},
		{" .next , node_modules , target ", 3},
		{"", 0},
		{",,,", 0},
		{"single", 1},
	}

	for _, tt := range tests {
		got := parseArtifactPatterns(tt.input)
		if len(got) != tt.want {
			t.Errorf("parseArtifactPatterns(%q) = %d patterns, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestFindArtifactDirs(t *testing.T) {
	dir := t.TempDir()

	// Create artifact dirs
	for _, name := range []string{"node_modules", ".next", "src"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Add some content to node_modules
	writeFile(t, filepath.Join(dir, "node_modules", "pkg.json"), 1000)

	artifacts, err := findArtifactDirs(dir, []string{"node_modules", ".next", "target"})
	if err != nil {
		t.Fatal(err)
	}

	if len(artifacts) != 2 {
		t.Fatalf("findArtifactDirs found %d, want 2", len(artifacts))
	}

	// Verify node_modules has size
	for _, a := range artifacts {
		if a.Pattern == "node_modules" && a.SizeBytes != 1000 {
			t.Errorf("node_modules size = %d, want 1000", a.SizeBytes)
		}
	}
}

func TestFindArtifactDirs_SecondLevel(t *testing.T) {
	dir := t.TempDir()

	// Create nested artifact: packages/foo/node_modules
	nested := filepath.Join(dir, "packages", "node_modules")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(nested, "dep.js"), 500)

	artifacts, err := findArtifactDirs(dir, []string{"node_modules"})
	if err != nil {
		t.Fatal(err)
	}

	if len(artifacts) != 1 {
		t.Fatalf("findArtifactDirs found %d, want 1", len(artifacts))
	}
	if artifacts[0].SizeBytes != 500 {
		t.Errorf("nested node_modules size = %d, want 500", artifacts[0].SizeBytes)
	}
}

func TestFindArtifactDirs_EmptyPatterns(t *testing.T) {
	dir := t.TempDir()
	artifacts, err := findArtifactDirs(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 0 {
		t.Errorf("expected 0 artifacts with nil patterns, got %d", len(artifacts))
	}
}

func TestRemoveArtifactDirs_DryRun(t *testing.T) {
	dir := t.TempDir()

	nm := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(nm, "pkg.json"), 500)

	freed, paths, err := removeArtifactDirs(dir, []string{"node_modules"}, true)
	if err != nil {
		t.Fatal(err)
	}

	if freed != 500 {
		t.Errorf("freed = %d, want 500", freed)
	}
	if len(paths) != 1 {
		t.Errorf("paths = %d, want 1", len(paths))
	}

	// Directory should still exist (dry run)
	if _, err := os.Stat(nm); os.IsNotExist(err) {
		t.Error("node_modules should still exist in dry run")
	}
}

func TestRemoveArtifactDirs_Actual(t *testing.T) {
	dir := t.TempDir()

	nm := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(nm, "pkg.json"), 500)

	freed, paths, err := removeArtifactDirs(dir, []string{"node_modules"}, false)
	if err != nil {
		t.Fatal(err)
	}

	if freed != 500 {
		t.Errorf("freed = %d, want 500", freed)
	}
	if len(paths) != 1 {
		t.Errorf("paths = %d, want 1", len(paths))
	}

	// Directory should be gone
	if _, err := os.Stat(nm); !os.IsNotExist(err) {
		t.Error("node_modules should have been removed")
	}
}

func TestHumanizeBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
		{1099511627776, "1.0 TiB"},
	}

	for _, tt := range tests {
		got := humanizeBytes(tt.input)
		if got != tt.want {
			t.Errorf("humanizeBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// writeFile creates a file with exactly the specified number of bytes.
func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	data := make([]byte, size)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
