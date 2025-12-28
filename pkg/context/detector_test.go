package context

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewDetector(t *testing.T) {
	d := NewDetector("/tmp/test")
	if d == nil {
		t.Fatal("NewDetector returned nil")
		return // unreachable, but satisfies staticcheck
	}
	if d.cwd != "/tmp/test" {
		t.Errorf("expected cwd '/tmp/test', got '%s'", d.cwd)
	}
}

func TestDetector_Detect_EmptyDir(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	d := NewDetector(tmpDir)
	ctx := d.Detect()

	if ctx.CWD != tmpDir {
		t.Errorf("expected CWD '%s', got '%s'", tmpDir, ctx.CWD)
	}
	if ctx.IsGitRepo {
		t.Error("empty dir should not be detected as git repo")
	}
	if ctx.HasDockerfile {
		t.Error("empty dir should not have Dockerfile")
	}
	if ctx.ProjectType != "unknown" {
		t.Errorf("expected ProjectType 'unknown', got '%s'", ctx.ProjectType)
	}
}

func TestDetector_Detect_GitRepo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .git directory
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}

	d := NewDetector(tmpDir)
	ctx := d.Detect()

	if !ctx.IsGitRepo {
		t.Error("directory with .git should be detected as git repo")
	}
}

func TestDetector_Detect_GoProject(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create go.mod file
	goMod := filepath.Join(tmpDir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDetector(tmpDir)
	ctx := d.Detect()

	if ctx.ProjectType != "go" {
		t.Errorf("expected ProjectType 'go', got '%s'", ctx.ProjectType)
	}
}

func TestDetector_Detect_NodeProject(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create package.json file
	pkgJson := filepath.Join(tmpDir, "package.json")
	if err := os.WriteFile(pkgJson, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDetector(tmpDir)
	ctx := d.Detect()

	if ctx.ProjectType != "node" {
		t.Errorf("expected ProjectType 'node', got '%s'", ctx.ProjectType)
	}
}

func TestDetector_Detect_PythonProject(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create pyproject.toml file
	pyProject := filepath.Join(tmpDir, "pyproject.toml")
	if err := os.WriteFile(pyProject, []byte("[project]"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDetector(tmpDir)
	ctx := d.Detect()

	if ctx.ProjectType != "python" {
		t.Errorf("expected ProjectType 'python', got '%s'", ctx.ProjectType)
	}
}

func TestDetector_Detect_RustProject(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create Cargo.toml file
	cargoToml := filepath.Join(tmpDir, "Cargo.toml")
	if err := os.WriteFile(cargoToml, []byte("[package]"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDetector(tmpDir)
	ctx := d.Detect()

	if ctx.ProjectType != "rust" {
		t.Errorf("expected ProjectType 'rust', got '%s'", ctx.ProjectType)
	}
}

func TestDetector_Detect_Dockerfile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create Dockerfile
	dockerfile := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM alpine"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDetector(tmpDir)
	ctx := d.Detect()

	if !ctx.HasDockerfile {
		t.Error("directory with Dockerfile should have HasDockerfile=true")
	}
}

func TestDetector_Detect_Kubeconfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .kube directory with config
	kubeDir := filepath.Join(tmpDir, ".kube")
	if err := os.Mkdir(kubeDir, 0755); err != nil {
		t.Fatal(err)
	}
	kubeConfig := filepath.Join(kubeDir, "config")
	if err := os.WriteFile(kubeConfig, []byte("clusters:"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDetector(tmpDir)
	ctx := d.Detect()

	if !ctx.HasKubeConfig {
		t.Error("directory with .kube/config should have HasKubeConfig=true")
	}
}

func TestDetector_SuggestedProfile_Dev(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create git repo with go.mod (typical dev scenario)
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	goMod := filepath.Join(tmpDir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDetector(tmpDir)
	ctx := d.Detect()

	if ctx.SuggestedProfile != "dev" {
		t.Errorf("expected SuggestedProfile 'dev', got '%s'", ctx.SuggestedProfile)
	}
}

func TestDetector_SuggestedProfile_WithKube(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create kubeconfig and Dockerfile (infra scenario)
	kubeDir := filepath.Join(tmpDir, ".kube")
	if err := os.Mkdir(kubeDir, 0755); err != nil {
		t.Fatal(err)
	}
	kubeConfig := filepath.Join(kubeDir, "config")
	if err := os.WriteFile(kubeConfig, []byte("clusters:"), 0644); err != nil {
		t.Fatal(err)
	}
	dockerfile := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM alpine"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDetector(tmpDir)
	ctx := d.Detect()

	// Should detect kubernetes and docker tags
	if !ctx.HasKubeConfig {
		t.Error("expected HasKubeConfig to be true")
	}
	if !ctx.HasDockerfile {
		t.Error("expected HasDockerfile to be true")
	}
	// SuggestedProfile depends on implementation, just check it's not empty
	if ctx.SuggestedProfile == "" {
		t.Error("expected SuggestedProfile to be set")
	}
}

func TestDetector_DetectedTags(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "loom-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create git repo with Dockerfile
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatal(err)
	}
	dockerfile := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM alpine"), 0644); err != nil {
		t.Fatal(err)
	}

	d := NewDetector(tmpDir)
	ctx := d.Detect()

	// Should have git and docker tags
	hasGit := false
	hasDocker := false
	for _, tag := range ctx.DetectedTags {
		if tag == "git" {
			hasGit = true
		}
		if tag == "docker" {
			hasDocker = true
		}
	}

	if !hasGit {
		t.Error("expected 'git' in DetectedTags")
	}
	if !hasDocker {
		t.Error("expected 'docker' in DetectedTags")
	}
}
