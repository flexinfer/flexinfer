package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFingerprint_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}
	if fp == nil {
		t.Fatal("Fingerprint() returned nil")
	}
	if fp.ProjectDir != dir {
		t.Errorf("ProjectDir = %q, want %q", fp.ProjectDir, dir)
	}
	if fp.ProjectName != filepath.Base(dir) {
		t.Errorf("ProjectName = %q, want %q", fp.ProjectName, filepath.Base(dir))
	}
	if len(fp.Languages) != 0 {
		t.Errorf("Languages = %v, want empty slice", fp.Languages)
	}
	if fp.Hash == "" {
		t.Error("Hash is empty, want non-empty hash")
	}
}

func TestFingerprint_GoProject(t *testing.T) {
	dir := t.TempDir()

	goMod := "module test\n\ngo 1.25\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}
	if len(fp.Languages) != 1 {
		t.Fatalf("len(Languages) = %d, want 1", len(fp.Languages))
	}

	lang := fp.Languages[0]
	if lang.Language != "go" {
		t.Errorf("Language = %q, want %q", lang.Language, "go")
	}
	if lang.Version != "1.25" {
		t.Errorf("Version = %q, want %q", lang.Version, "1.25")
	}
	if lang.DepFile != "go.mod" {
		t.Errorf("DepFile = %q, want %q", lang.DepFile, "go.mod")
	}
	if lang.DepManager != "go" {
		t.Errorf("DepManager = %q, want %q", lang.DepManager, "go")
	}
	if lang.LockFile != "" {
		t.Errorf("LockFile = %q, want empty (no go.sum)", lang.LockFile)
	}
}

func TestFingerprint_GoProject_WithSum(t *testing.T) {
	dir := t.TempDir()

	goMod := "module test\n\ngo 1.25\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), []byte(""), 0644); err != nil {
		t.Fatalf("failed to write go.sum: %v", err)
	}

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}
	if len(fp.Languages) != 1 {
		t.Fatalf("len(Languages) = %d, want 1", len(fp.Languages))
	}
	if fp.Languages[0].LockFile != "go.sum" {
		t.Errorf("LockFile = %q, want %q", fp.Languages[0].LockFile, "go.sum")
	}
}

func TestFingerprint_PythonProject_Pyproject(t *testing.T) {
	dir := t.TempDir()

	pyproject := `[project]
name = "myapp"
requires-python = ">=3.11"
`
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproject), 0644); err != nil {
		t.Fatalf("failed to write pyproject.toml: %v", err)
	}

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}
	if len(fp.Languages) != 1 {
		t.Fatalf("len(Languages) = %d, want 1", len(fp.Languages))
	}

	lang := fp.Languages[0]
	if lang.Language != "python" {
		t.Errorf("Language = %q, want %q", lang.Language, "python")
	}
	if lang.Version != "3.11" {
		t.Errorf("Version = %q, want %q", lang.Version, "3.11")
	}
	if lang.DepFile != "pyproject.toml" {
		t.Errorf("DepFile = %q, want %q", lang.DepFile, "pyproject.toml")
	}
	// Default dep manager is uv when no lock files exist
	if lang.DepManager != "uv" {
		t.Errorf("DepManager = %q, want %q", lang.DepManager, "uv")
	}
}

func TestFingerprint_PythonProject_RequirementsTxt(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask==3.0\n"), 0644); err != nil {
		t.Fatalf("failed to write requirements.txt: %v", err)
	}

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}
	if len(fp.Languages) != 1 {
		t.Fatalf("len(Languages) = %d, want 1", len(fp.Languages))
	}

	lang := fp.Languages[0]
	if lang.Language != "python" {
		t.Errorf("Language = %q, want %q", lang.Language, "python")
	}
	if lang.DepFile != "requirements.txt" {
		t.Errorf("DepFile = %q, want %q", lang.DepFile, "requirements.txt")
	}
	if lang.DepManager != "pip" {
		t.Errorf("DepManager = %q, want %q", lang.DepManager, "pip")
	}
}

func TestFingerprint_PythonProject_Poetry(t *testing.T) {
	dir := t.TempDir()

	pyproject := `[tool.poetry]
name = "myapp"

[project]
requires-python = ">=3.12"
`
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(pyproject), 0644); err != nil {
		t.Fatalf("failed to write pyproject.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "poetry.lock"), []byte(""), 0644); err != nil {
		t.Fatalf("failed to write poetry.lock: %v", err)
	}

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}
	if len(fp.Languages) != 1 {
		t.Fatalf("len(Languages) = %d, want 1", len(fp.Languages))
	}

	lang := fp.Languages[0]
	if lang.DepManager != "poetry" {
		t.Errorf("DepManager = %q, want %q", lang.DepManager, "poetry")
	}
	if lang.LockFile != "poetry.lock" {
		t.Errorf("LockFile = %q, want %q", lang.LockFile, "poetry.lock")
	}
}

func TestFingerprint_NodeProject(t *testing.T) {
	dir := t.TempDir()

	pkgJSON := `{
  "name": "myapp",
  "engines": {
    "node": ">=20.0.0"
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}
	if len(fp.Languages) != 1 {
		t.Fatalf("len(Languages) = %d, want 1", len(fp.Languages))
	}

	lang := fp.Languages[0]
	if lang.Language != "node" {
		t.Errorf("Language = %q, want %q", lang.Language, "node")
	}
	if lang.Version != "20.0.0" {
		t.Errorf("Version = %q, want %q", lang.Version, "20.0.0")
	}
	if lang.DepFile != "package.json" {
		t.Errorf("DepFile = %q, want %q", lang.DepFile, "package.json")
	}
	// Default dep manager is npm when no lock files exist
	if lang.DepManager != "npm" {
		t.Errorf("DepManager = %q, want %q", lang.DepManager, "npm")
	}
}

func TestFingerprint_NodeProject_Pnpm(t *testing.T) {
	dir := t.TempDir()

	pkgJSON := `{"name": "myapp"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0644); err != nil {
		t.Fatalf("failed to write pnpm-lock.yaml: %v", err)
	}

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}
	if len(fp.Languages) != 1 {
		t.Fatalf("len(Languages) = %d, want 1", len(fp.Languages))
	}

	lang := fp.Languages[0]
	if lang.DepManager != "pnpm" {
		t.Errorf("DepManager = %q, want %q", lang.DepManager, "pnpm")
	}
	if lang.LockFile != "pnpm-lock.yaml" {
		t.Errorf("LockFile = %q, want %q", lang.LockFile, "pnpm-lock.yaml")
	}
}

func TestFingerprint_MultiLanguage_GoAndNode(t *testing.T) {
	dir := t.TempDir()

	goMod := "module test\n\ngo 1.25\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	pkgJSON := `{"name": "frontend"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}
	if len(fp.Languages) != 2 {
		t.Fatalf("len(Languages) = %d, want 2", len(fp.Languages))
	}

	langMap := make(map[string]LanguageSpec)
	for _, l := range fp.Languages {
		langMap[l.Language] = l
	}

	if _, ok := langMap["go"]; !ok {
		t.Error("expected Go language to be detected")
	}
	if _, ok := langMap["node"]; !ok {
		t.Error("expected Node language to be detected")
	}
}

func TestFingerprint_BuildTargets(t *testing.T) {
	dir := t.TempDir()

	makefile := `build:
	go build ./...

test:
	go test ./...

.PHONY: build test
`
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0644); err != nil {
		t.Fatalf("failed to write Makefile: %v", err)
	}

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}

	targetSet := make(map[string]bool)
	for _, t := range fp.BuildTargets {
		targetSet[t] = true
	}

	if !targetSet["build"] {
		t.Error("expected 'build' target to be detected")
	}
	if !targetSet["test"] {
		t.Error("expected 'test' target to be detected")
	}
}

func TestFingerprint_SystemDeps_Dockerfile(t *testing.T) {
	dir := t.TempDir()

	dockerfile := `FROM golang:1.25
RUN apt-get update && apt-get install -y git curl
`
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		t.Fatalf("failed to write Dockerfile: %v", err)
	}

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}

	depSet := make(map[string]bool)
	for _, d := range fp.SystemDeps {
		depSet[d] = true
	}

	if !depSet["git"] {
		t.Error("expected 'git' system dep to be detected")
	}
	if !depSet["curl"] {
		t.Error("expected 'curl' system dep to be detected")
	}
}

func TestFingerprint_ManifestOverrides(t *testing.T) {
	dir := t.TempDir()

	manifest := `base_image: ubuntu:24.04
system_deps:
  - ffmpeg
  - imagemagick
env:
  MY_VAR: hello
`
	if err := os.WriteFile(filepath.Join(dir, ".devbox.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("failed to write .devbox.yaml: %v", err)
	}

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}
	if fp.Overrides == nil {
		t.Fatal("Overrides is nil, want non-nil")
	}
	if fp.Overrides.BaseImage != "ubuntu:24.04" {
		t.Errorf("BaseImage = %q, want %q", fp.Overrides.BaseImage, "ubuntu:24.04")
	}

	depSet := make(map[string]bool)
	for _, d := range fp.SystemDeps {
		depSet[d] = true
	}
	if !depSet["ffmpeg"] {
		t.Error("expected 'ffmpeg' in SystemDeps from manifest")
	}
	if !depSet["imagemagick"] {
		t.Error("expected 'imagemagick' in SystemDeps from manifest")
	}

	if fp.EnvVars["MY_VAR"] != "hello" {
		t.Errorf("EnvVars[MY_VAR] = %q, want %q", fp.EnvVars["MY_VAR"], "hello")
	}
}

func TestFingerprint_HashIsNonEmpty(t *testing.T) {
	dir := t.TempDir()

	fp, err := Fingerprint(dir)
	if err != nil {
		t.Fatalf("Fingerprint() returned error: %v", err)
	}
	if fp.Hash == "" {
		t.Error("Hash is empty, want non-empty deterministic hash")
	}
}

func TestIsSimpleTarget(t *testing.T) {
	tests := []struct {
		target string
		want   bool
	}{
		{"build", true},
		{"test", true},
		{"clean", true},
		{"", false},
		{"$(VAR)", false},
		{"foo%bar", false},
		{"path/to/file", false},
	}

	for _, tt := range tests {
		got := isSimpleTarget(tt.target)
		if got != tt.want {
			t.Errorf("isSimpleTarget(%q) = %v, want %v", tt.target, got, tt.want)
		}
	}
}

func TestAppendUnique(t *testing.T) {
	result := appendUnique([]string{"a", "b"}, "b", "c", "a", "d")
	if len(result) != 4 {
		t.Fatalf("len(result) = %d, want 4", len(result))
	}

	expected := map[string]bool{"a": true, "b": true, "c": true, "d": true}
	for _, v := range result {
		if !expected[v] {
			t.Errorf("unexpected value %q in result", v)
		}
	}
}

func TestAppendUnique_EmptySlice(t *testing.T) {
	result := appendUnique(nil, "a", "b", "a")
	if len(result) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(result))
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()

	// Non-existent file
	if fileExists(filepath.Join(dir, "nope.txt")) {
		t.Error("fileExists returned true for non-existent file")
	}

	// Existing file
	path := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(path, []byte("hi"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if !fileExists(path) {
		t.Error("fileExists returned false for existing file")
	}

	// Directory should return false
	if fileExists(dir) {
		t.Error("fileExists returned true for directory")
	}
}
