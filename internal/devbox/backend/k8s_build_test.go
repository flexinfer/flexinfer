package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadDepFiles_Empty(t *testing.T) {
	dir := t.TempDir()
	files := readDepFiles(dir)
	if len(files) != 0 {
		t.Errorf("expected empty map for empty dir, got %d files", len(files))
	}
}

func TestReadDepFiles_GoProject(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.25"), 0644)
	os.WriteFile(filepath.Join(dir, "go.sum"), []byte("hash1\nhash2"), 0644)

	files := readDepFiles(dir)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if _, ok := files["go.mod"]; !ok {
		t.Error("expected go.mod in result")
	}
	if _, ok := files["go.sum"]; !ok {
		t.Error("expected go.sum in result")
	}
}

func TestReadDepFiles_NodeProject(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"test"}`), 0644)
	os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfile"), 0644)

	files := readDepFiles(dir)
	if _, ok := files["package.json"]; !ok {
		t.Error("expected package.json")
	}
	if _, ok := files["pnpm-lock.yaml"]; !ok {
		t.Error("expected pnpm-lock.yaml")
	}
}

func TestReadDepFiles_MixedProject(t *testing.T) {
	dir := t.TempDir()
	// Simulate a project with both Go and Python deps.
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)
	os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]"), 0644)
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask==3.0"), 0644)

	files := readDepFiles(dir)
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d: %v", len(files), files)
	}
}

func TestReadDepFiles_NonExistentDir(t *testing.T) {
	files := readDepFiles("/nonexistent/path/for/testing")
	if len(files) != 0 {
		t.Errorf("expected empty map for nonexistent dir, got %d", len(files))
	}
}

func TestReadDepFiles_IgnoresNonDepFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0644)

	files := readDepFiles(dir)
	if len(files) != 0 {
		t.Errorf("expected 0 dep files, got %d: %v", len(files), files)
	}
}
