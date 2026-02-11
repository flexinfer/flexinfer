package dockerfile

import (
	"strings"
	"testing"

	"github.com/crb2nu/loom/internal/devbox/detect"
)

func TestGenerate_GoProject(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mygoproject",
		ProjectName: "mygoproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: "1.22"},
		},
		Hash: "abc123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	checks := []struct {
		name     string
		contains string
	}{
		{"base image", "golang:1.22"},
		{"go mod download", "go mod download"},
		{"CMD sleep", `CMD ["sleep"`},
		{"hash comment", "abc123"},
		{"workdir", "WORKDIR /workspace"},
	}

	for _, c := range checks {
		if !strings.Contains(dockerfile, c.contains) {
			t.Errorf("Go Dockerfile missing %s: expected to contain %q\nGot:\n%s", c.name, c.contains, dockerfile)
		}
	}
}

func TestGenerate_GoProject_DefaultVersion(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mygoproject",
		ProjectName: "mygoproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: ""},
		},
		Hash: "def456",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "golang:1.25") {
		t.Errorf("Go Dockerfile should default to golang:1.25, got:\n%s", dockerfile)
	}
}

func TestGenerate_GoProject_WithTools(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mygoproject",
		ProjectName: "mygoproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: "1.22", Tools: []string{"golangci-lint", "goimports"}},
		},
		Hash: "tools123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "golangci-lint/cmd/golangci-lint") {
		t.Errorf("Go Dockerfile should install golangci-lint, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "golang.org/x/tools/cmd/goimports") {
		t.Errorf("Go Dockerfile should install goimports, got:\n%s", dockerfile)
	}
}

func TestGenerate_PythonProject(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mypyproject",
		ProjectName: "mypyproject",
		Languages: []detect.LanguageSpec{
			{Language: "python", Version: "3.12", DepManager: "pip"},
		},
		Hash: "py123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	checks := []struct {
		name     string
		contains string
	}{
		{"base image", "python:3.12"},
		{"pip install", "pip install"},
		{"CMD sleep", `CMD [`},
		{"hash comment", "py123"},
		{"requirements copy", "requirements.txt"},
	}

	for _, c := range checks {
		if !strings.Contains(dockerfile, c.contains) {
			t.Errorf("Python Dockerfile missing %s: expected to contain %q\nGot:\n%s", c.name, c.contains, dockerfile)
		}
	}
}

func TestGenerate_PythonProject_UV(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mypyproject",
		ProjectName: "mypyproject",
		Languages: []detect.LanguageSpec{
			{Language: "python", Version: "3.11", DepManager: "uv"},
		},
		Hash: "uvpy",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "pip install --no-cache-dir uv") {
		t.Errorf("Python/uv Dockerfile should install uv, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "uv sync") {
		t.Errorf("Python/uv Dockerfile should run uv sync, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "pyproject.toml") {
		t.Errorf("Python/uv Dockerfile should copy pyproject.toml, got:\n%s", dockerfile)
	}
}

func TestGenerate_PythonProject_Poetry(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mypyproject",
		ProjectName: "mypyproject",
		Languages: []detect.LanguageSpec{
			{Language: "python", Version: "3.11", DepManager: "poetry"},
		},
		Hash: "poetrypy",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "pip install --no-cache-dir poetry") {
		t.Errorf("Python/poetry Dockerfile should install poetry, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "poetry install") {
		t.Errorf("Python/poetry Dockerfile should run poetry install, got:\n%s", dockerfile)
	}
}

func TestGenerate_PythonProject_DefaultVersion(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mypyproject",
		ProjectName: "mypyproject",
		Languages: []detect.LanguageSpec{
			{Language: "python", Version: ""},
		},
		Hash: "pydef",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "python:3.11") {
		t.Errorf("Python Dockerfile should default to python:3.11, got:\n%s", dockerfile)
	}
}

func TestGenerate_NodeProject_NPM(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mynodeproject",
		ProjectName: "mynodeproject",
		Languages: []detect.LanguageSpec{
			{Language: "node", Version: "20", DepManager: "npm"},
		},
		Hash: "node123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	checks := []struct {
		name     string
		contains string
	}{
		{"base image", "node:20"},
		{"npm ci", "npm ci"},
		{"CMD sleep", `CMD [`},
		{"hash comment", "node123"},
		{"package.json copy", "package.json"},
	}

	for _, c := range checks {
		if !strings.Contains(dockerfile, c.contains) {
			t.Errorf("Node/npm Dockerfile missing %s: expected to contain %q\nGot:\n%s", c.name, c.contains, dockerfile)
		}
	}
}

func TestGenerate_NodeProject_PNPM(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mynodeproject",
		ProjectName: "mynodeproject",
		Languages: []detect.LanguageSpec{
			{Language: "node", Version: "18", DepManager: "pnpm"},
		},
		Hash: "pnpm123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "corepack enable") {
		t.Errorf("Node/pnpm Dockerfile should enable corepack, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "pnpm install") {
		t.Errorf("Node/pnpm Dockerfile should run pnpm install, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "pnpm-lock.yaml") {
		t.Errorf("Node/pnpm Dockerfile should copy pnpm-lock.yaml, got:\n%s", dockerfile)
	}
}

func TestGenerate_NodeProject_Yarn(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mynodeproject",
		ProjectName: "mynodeproject",
		Languages: []detect.LanguageSpec{
			{Language: "node", Version: "20", DepManager: "yarn"},
		},
		Hash: "yarn123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "yarn install") {
		t.Errorf("Node/yarn Dockerfile should run yarn install, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "yarn.lock") {
		t.Errorf("Node/yarn Dockerfile should copy yarn.lock, got:\n%s", dockerfile)
	}
}

func TestGenerate_NodeProject_DefaultVersion(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mynodeproject",
		ProjectName: "mynodeproject",
		Languages: []detect.LanguageSpec{
			{Language: "node", Version: ""},
		},
		Hash: "nodedef",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "node:20") {
		t.Errorf("Node Dockerfile should default to node:20, got:\n%s", dockerfile)
	}
}

func TestGenerate_RustProject(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/myrustproject",
		ProjectName: "myrustproject",
		Languages: []detect.LanguageSpec{
			{Language: "rust", Version: ""},
		},
		Hash: "rust123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	checks := []struct {
		name     string
		contains string
	}{
		{"base image", "rust:"},
		{"cargo fetch", "cargo fetch"},
		{"CMD sleep", `CMD [`},
		{"hash comment", "rust123"},
		{"Cargo.toml copy", "Cargo.toml"},
	}

	for _, c := range checks {
		if !strings.Contains(dockerfile, c.contains) {
			t.Errorf("Rust Dockerfile missing %s: expected to contain %q\nGot:\n%s", c.name, c.contains, dockerfile)
		}
	}
}

func TestGenerate_MultiLanguage_GoAndNode(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/multiproject",
		ProjectName: "multiproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: "1.22"},
			{Language: "node", Version: "20", DepManager: "npm"},
		},
		Hash: "multi123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	checks := []struct {
		name     string
		contains string
	}{
		{"go base image", "golang:1.22"},
		{"go mod download", "go mod download"},
		{"nodejs package", "nodejs"},
		{"npm install", "npm"},
		{"CMD sleep", `CMD [`},
		{"hash comment", "multi123"},
	}

	for _, c := range checks {
		if !strings.Contains(dockerfile, c.contains) {
			t.Errorf("Multi-lang Dockerfile missing %s: expected to contain %q\nGot:\n%s", c.name, c.contains, dockerfile)
		}
	}
}

func TestGenerate_MultiLanguage_GoAndPython(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/multiproject",
		ProjectName: "multiproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: "1.22"},
			{Language: "python", Version: "3.12", DepManager: "uv"},
		},
		Hash: "gopy123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "golang:1.22") {
		t.Errorf("Multi-lang Dockerfile should use Go as base image, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "go mod download") {
		t.Errorf("Multi-lang Dockerfile should include go mod download, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "python3") {
		t.Errorf("Multi-lang Dockerfile should install python3, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "uv") {
		t.Errorf("Multi-lang Dockerfile should install uv for Python, got:\n%s", dockerfile)
	}
}

func TestGenerate_MultiLanguage_NodeAndPython(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/multiproject",
		ProjectName: "multiproject",
		Languages: []detect.LanguageSpec{
			{Language: "node", Version: "20", DepManager: "pnpm"},
			{Language: "python", Version: "3.11", DepManager: "poetry"},
		},
		Hash: "nodepy123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	// Without Go, the multi-template uses debian:bookworm-slim as base
	if !strings.Contains(dockerfile, "debian:bookworm-slim") {
		t.Errorf("Multi-lang without Go should use debian base, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "nodejs") {
		t.Errorf("Multi-lang Dockerfile should install nodejs, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "python3") {
		t.Errorf("Multi-lang Dockerfile should install python3, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "pnpm install") {
		t.Errorf("Multi-lang Dockerfile should run pnpm install, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "poetry install") {
		t.Errorf("Multi-lang Dockerfile should run poetry install, got:\n%s", dockerfile)
	}
}

func TestGenerate_MultiLanguage_GoNodePnpm(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/multiproject",
		ProjectName: "multiproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: "1.22"},
			{Language: "node", Version: "20", DepManager: "pnpm"},
		},
		Hash: "gopnpm",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "corepack enable") {
		t.Errorf("Multi-lang Go+pnpm should enable corepack, got:\n%s", dockerfile)
	}
}

func TestGenerate_EmptyLanguages(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/emptyproject",
		ProjectName: "emptyproject",
		Languages:   []detect.LanguageSpec{},
		Hash:        "empty123",
	}

	_, err := Generate(fp)
	if err == nil {
		t.Fatal("Generate() should return error for empty languages, got nil")
	}

	if !strings.Contains(err.Error(), "no languages detected") {
		t.Errorf("error should mention 'no languages detected', got: %v", err)
	}
}

func TestGenerate_NilLanguages(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/emptyproject",
		ProjectName: "emptyproject",
		Languages:   nil,
		Hash:        "nil123",
	}

	_, err := Generate(fp)
	if err == nil {
		t.Fatal("Generate() should return error for nil languages, got nil")
	}

	if !strings.Contains(err.Error(), "no languages detected") {
		t.Errorf("error should mention 'no languages detected', got: %v", err)
	}
}

func TestGenerate_UnsupportedLanguage(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/javaproject",
		ProjectName: "javaproject",
		Languages: []detect.LanguageSpec{
			{Language: "java", Version: "17"},
		},
		Hash: "java123",
	}

	_, err := Generate(fp)
	if err == nil {
		t.Fatal("Generate() should return error for unsupported language, got nil")
	}

	if !strings.Contains(err.Error(), "unsupported language") {
		t.Errorf("error should mention 'unsupported language', got: %v", err)
	}
}

func TestGenerate_WithSystemDeps(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mygoproject",
		ProjectName: "mygoproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: "1.22"},
		},
		SystemDeps: []string{"libssl-dev", "zlib1g-dev"},
		Hash:       "sysdeps123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "libssl-dev") {
		t.Errorf("Dockerfile should contain system dep libssl-dev, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "zlib1g-dev") {
		t.Errorf("Dockerfile should contain system dep zlib1g-dev, got:\n%s", dockerfile)
	}
}

func TestGenerate_WithEnvVars(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mygoproject",
		ProjectName: "mygoproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: "1.22"},
		},
		EnvVars: map[string]string{
			"CGO_ENABLED": "0",
			"GOFLAGS":     "-mod=vendor",
		},
		Hash: "env123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "CGO_ENABLED=0") {
		t.Errorf("Dockerfile should contain ENV CGO_ENABLED=0, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "GOFLAGS=-mod=vendor") {
		t.Errorf("Dockerfile should contain ENV GOFLAGS=-mod=vendor, got:\n%s", dockerfile)
	}
}

func TestGenerate_WithSetupCommands(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/mygoproject",
		ProjectName: "mygoproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: "1.22"},
		},
		Overrides: &detect.ManifestOverride{
			Setup: []string{
				"echo hello",
				"mkdir -p /data",
			},
		},
		Hash: "setup123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "echo hello") {
		t.Errorf("Dockerfile should contain setup command 'echo hello', got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "mkdir -p /data") {
		t.Errorf("Dockerfile should contain setup command 'mkdir -p /data', got:\n%s", dockerfile)
	}
}

func TestGenerate_CustomBaseImage(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/customproject",
		ProjectName: "customproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: "1.22"},
		},
		Overrides: &detect.ManifestOverride{
			BaseImage: "ubuntu:22.04",
		},
		Hash: "custom123",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "ubuntu:22.04") {
		t.Errorf("Dockerfile should use custom base image ubuntu:22.04, got:\n%s", dockerfile)
	}
	// Non-alpine base should use apt-get
	if !strings.Contains(dockerfile, "apt-get") {
		t.Errorf("Non-alpine custom base should use apt-get, got:\n%s", dockerfile)
	}
}

func TestGenerate_CustomBaseImage_Alpine(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/customproject",
		ProjectName: "customproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: "1.22"},
		},
		Overrides: &detect.ManifestOverride{
			BaseImage: "alpine:3.19",
		},
		Hash: "alpinecustom",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "alpine:3.19") {
		t.Errorf("Dockerfile should use custom base image alpine:3.19, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "apk add") {
		t.Errorf("Alpine custom base should use apk add, got:\n%s", dockerfile)
	}
}

func TestGenerate_HashInComment(t *testing.T) {
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/tmp/myproject",
		ProjectName: "myproject",
		Languages: []detect.LanguageSpec{
			{Language: "go", Version: "1.22"},
		},
		Hash: "sha256abcdef1234567890",
	}

	out, err := Generate(fp)
	if err != nil {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}

	dockerfile := string(out)

	if !strings.Contains(dockerfile, "sha256abcdef1234567890") {
		t.Errorf("Dockerfile should contain hash in comment, got:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile, "# Auto-generated by mcp-devbox") {
		t.Errorf("Dockerfile should contain auto-generated comment, got:\n%s", dockerfile)
	}
}
