package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src.txt")
	dst := filepath.Join(tmpDir, "dst.txt")

	content := []byte("hello world")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatalf("failed to write src file: %v", err)
	}

	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}

	if !Exists(dst) {
		t.Errorf("dst file does not exist")
	}

	readContent, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read dst file: %v", err)
	}

	if string(readContent) != string(content) {
		t.Errorf("content mismatch: got %s, want %s", string(readContent), string(content))
	}
}

func TestExists(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "test.txt")

	if Exists(file) {
		t.Errorf("Exists returned true for non-existent file")
	}

	if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	if !Exists(file) {
		t.Errorf("Exists returned false for existing file")
	}
}
