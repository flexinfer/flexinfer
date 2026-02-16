package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndReadManifest(t *testing.T) {
	dir := t.TempDir()

	files := []string{"commands/deploy.md", "rules/security.md"}
	if err := WriteManifest(dir, "claude", files); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil manifest")
	}

	if m.Platform != "claude" {
		t.Errorf("expected platform 'claude', got %q", m.Platform)
	}
	if len(m.Generated) != 2 {
		t.Fatalf("expected 2 generated files, got %d", len(m.Generated))
	}
	if m.Generated[0] != "commands/deploy.md" {
		t.Errorf("expected first file 'commands/deploy.md', got %q", m.Generated[0])
	}
	if m.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
}

func TestReadManifest_NotFound(t *testing.T) {
	dir := t.TempDir()

	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("expected no error for missing manifest, got: %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil manifest for missing file, got: %+v", m)
	}
}

func TestReadManifest_InvalidJSON(t *testing.T) {
	dir := t.TempDir()

	// Write invalid JSON.
	badPath := filepath.Join(dir, ManifestFilename)
	if err := writeTestFile(badPath, "not json"); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := ReadManifest(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWriteManifest_EmptyFiles(t *testing.T) {
	dir := t.TempDir()

	if err := WriteManifest(dir, "codex", []string{}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil manifest")
	}
	if len(m.Generated) != 0 {
		t.Errorf("expected 0 generated files, got %d", len(m.Generated))
	}
}

func TestManifestFilename_Constant(t *testing.T) {
	if ManifestFilename != ".loom-skills-manifest.json" {
		t.Errorf("unexpected manifest filename: %q", ManifestFilename)
	}
}

// writeTestFile is a helper to write a string to a file path.
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
