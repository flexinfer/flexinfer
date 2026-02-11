package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeHash_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	fp := &EnvFingerprint{}
	hash, err := computeHash(dir, fp)
	if err != nil {
		t.Fatalf("computeHash() returned error: %v", err)
	}
	if hash == "" {
		t.Fatal("computeHash() returned empty hash")
	}
	if len(hash) != 12 {
		t.Errorf("hash length = %d, want 12", len(hash))
	}
}

func TestComputeHash_EmptyDir_Consistent(t *testing.T) {
	dir := t.TempDir()

	fp := &EnvFingerprint{}
	hash1, err := computeHash(dir, fp)
	if err != nil {
		t.Fatalf("computeHash() first call returned error: %v", err)
	}

	hash2, err := computeHash(dir, fp)
	if err != nil {
		t.Fatalf("computeHash() second call returned error: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("hashes differ for same empty dir: %q vs %q", hash1, hash2)
	}
}

func TestComputeHash_SameFiles_SameHash(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	goMod := "module test\n\ngo 1.25\n"
	if err := os.WriteFile(filepath.Join(dir1, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("failed to write go.mod in dir1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("failed to write go.mod in dir2: %v", err)
	}

	fp := &EnvFingerprint{}
	hash1, err := computeHash(dir1, fp)
	if err != nil {
		t.Fatalf("computeHash(dir1) returned error: %v", err)
	}

	hash2, err := computeHash(dir2, fp)
	if err != nil {
		t.Fatalf("computeHash(dir2) returned error: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("identical files produced different hashes: %q vs %q", hash1, hash2)
	}
}

func TestComputeHash_DifferentFiles_DifferentHash(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir1, "go.mod"), []byte("module a\n\ngo 1.25\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod in dir1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "go.mod"), []byte("module b\n\ngo 1.24\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod in dir2: %v", err)
	}

	fp := &EnvFingerprint{}
	hash1, err := computeHash(dir1, fp)
	if err != nil {
		t.Fatalf("computeHash(dir1) returned error: %v", err)
	}

	hash2, err := computeHash(dir2, fp)
	if err != nil {
		t.Fatalf("computeHash(dir2) returned error: %v", err)
	}

	if hash1 == hash2 {
		t.Errorf("different files produced same hash: %q", hash1)
	}
}

func TestComputeHash_DifferentDepFiles_DifferentHash(t *testing.T) {
	// One dir has go.mod, the other has package.json with the same content.
	// The hash should differ because the file name is part of the hash.
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	content := "same content\n"
	if err := os.WriteFile(filepath.Join(dir1, "go.mod"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "package.json"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}

	fp := &EnvFingerprint{}
	hash1, err := computeHash(dir1, fp)
	if err != nil {
		t.Fatalf("computeHash(dir1) returned error: %v", err)
	}

	hash2, err := computeHash(dir2, fp)
	if err != nil {
		t.Fatalf("computeHash(dir2) returned error: %v", err)
	}

	if hash1 == hash2 {
		t.Error("different dep files with same content should produce different hashes (file name is part of hash)")
	}
}

func TestComputeHash_AdditionalFile_ChangesHash(t *testing.T) {
	dir := t.TempDir()

	goMod := "module test\n\ngo 1.25\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	fp := &EnvFingerprint{}
	hash1, err := computeHash(dir, fp)
	if err != nil {
		t.Fatalf("computeHash() before adding go.sum returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte("h1:abc123\n"), 0644); err != nil {
		t.Fatalf("failed to write go.sum: %v", err)
	}

	hash2, err := computeHash(dir, fp)
	if err != nil {
		t.Fatalf("computeHash() after adding go.sum returned error: %v", err)
	}

	if hash1 == hash2 {
		t.Error("adding go.sum should change the hash")
	}
}

func TestComputeHash_IgnoresNonDepFiles(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	goMod := "module test\n\ngo 1.25\n"
	if err := os.WriteFile(filepath.Join(dir1, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("failed to write go.mod in dir1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("failed to write go.mod in dir2: %v", err)
	}

	// Add a non-dep file to dir2 only
	if err := os.WriteFile(filepath.Join(dir2, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatalf("failed to write main.go: %v", err)
	}

	fp := &EnvFingerprint{}
	hash1, err := computeHash(dir1, fp)
	if err != nil {
		t.Fatalf("computeHash(dir1) returned error: %v", err)
	}

	hash2, err := computeHash(dir2, fp)
	if err != nil {
		t.Fatalf("computeHash(dir2) returned error: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("non-dep files should not affect hash: %q vs %q", hash1, hash2)
	}
}

func TestComputeHash_HashLength(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.25\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	fp := &EnvFingerprint{}
	hash, err := computeHash(dir, fp)
	if err != nil {
		t.Fatalf("computeHash() returned error: %v", err)
	}

	if len(hash) != 12 {
		t.Errorf("hash length = %d, want 12 (truncated SHA-256 hex)", len(hash))
	}

	// Verify it's valid hex
	for _, c := range hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("hash contains non-hex character: %c", c)
		}
	}
}
