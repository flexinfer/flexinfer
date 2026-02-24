package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShouldCopy_Missing(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.bin")
	dst := filepath.Join(t.TempDir(), "dst.bin")

	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !shouldCopy(src, dst) {
		t.Fatal("expected shouldCopy=true when destination is missing")
	}
}

func TestShouldCopy_SameSize(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	data := []byte("matching content")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if shouldCopy(src, dst) {
		t.Fatal("expected shouldCopy=false when sizes match")
	}
}

func TestShouldCopy_DifferentSize(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	if err := os.WriteFile(src, []byte("longer content here"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !shouldCopy(src, dst) {
		t.Fatal("expected shouldCopy=true when sizes differ")
	}
}

func TestCopyFileAtomic_LargeBuffer(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	// Create a test file with known content
	content := make([]byte, 64*1024) // 64KB
	for i := range content {
		content[i] = byte(i % 256)
	}
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 4*1024*1024) // 4MB buffer
	n, err := copyFileAtomic(src, dst, buf)
	if err != nil {
		t.Fatalf("copyFileAtomic failed: %v", err)
	}
	if n != int64(len(content)) {
		t.Fatalf("copied %d bytes, want %d", n, len(content))
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if len(got) != len(content) {
		t.Fatalf("destination size %d, want %d", len(got), len(content))
	}
	for i := range content {
		if got[i] != content[i] {
			t.Fatalf("byte mismatch at offset %d: got %d, want %d", i, got[i], content[i])
		}
	}
}

func TestCopyFileAtomic_NoPartialFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")

	if err := os.WriteFile(src, []byte("complete content"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 4096)
	_, err := copyFileAtomic(src, dst, buf)
	if err != nil {
		t.Fatalf("copyFileAtomic failed: %v", err)
	}

	// Verify no .flash-tmp file remains
	tmpPath := dst + ".flash-tmp"
	if _, err := os.Stat(tmpPath); err == nil {
		t.Fatal("expected .flash-tmp to not exist after successful copy")
	}
}

func TestCleanStaleTmpFiles(t *testing.T) {
	dir := t.TempDir()

	// Create some normal files and some .flash-tmp files
	if err := os.WriteFile(filepath.Join(dir, "model.bin"), []byte("model"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.bin.flash-tmp"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "weights.flash-tmp"), []byte("stale2"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanStaleTmpFiles(dir)

	// Normal file should remain
	if _, err := os.Stat(filepath.Join(dir, "model.bin")); err != nil {
		t.Fatal("expected model.bin to remain")
	}
	// Stale tmp files should be removed
	if _, err := os.Stat(filepath.Join(dir, "model.bin.flash-tmp")); err == nil {
		t.Fatal("expected model.bin.flash-tmp to be removed")
	}
	if _, err := os.Stat(filepath.Join(sub, "weights.flash-tmp")); err == nil {
		t.Fatal("expected subdir/weights.flash-tmp to be removed")
	}
}

func TestVerifyIntegrity_Pass(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create matching files
	sub := filepath.Join(srcDir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dstDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string][]byte{
		"model.bin":       make([]byte, 100),
		"sub/config.json": []byte(`{"key": "value"}`),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(srcDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := verifyIntegrity(srcDir, dstDir); err != nil {
		t.Fatalf("expected verification to pass: %v", err)
	}
}

func TestVerifyIntegrity_SizeMismatch(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(srcDir, "model.bin"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "model.bin"), make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyIntegrity(srcDir, dstDir); err == nil {
		t.Fatal("expected verification to fail on size mismatch")
	}
}

func TestVerifyIntegrity_MissingFile(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(srcDir, "model.bin"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Don't create the file in dstDir

	if err := verifyIntegrity(srcDir, dstDir); err == nil {
		t.Fatal("expected verification to fail on missing file")
	}
}
