// Package context provides workspace detection and context-aware profile selection.
package context

import (
	"os"
	"path/filepath"
)

// WorkspaceContext describes the current working environment.
type WorkspaceContext struct {
	CWD              string
	IsGitRepo        bool
	HasKubeConfig    bool
	HasDockerfile    bool
	HasGoMod         bool
	HasPackageJSON   bool
	HasPyProject     bool
	HasCargoToml     bool
	ProjectType      string // "go", "python", "node", "rust", "unknown"
	DetectedTags     []string
	SuggestedProfile string
}

// Detector analyzes the current working directory.
type Detector struct {
	cwd string
}

// NewDetector creates a new context detector for the given directory.
func NewDetector(cwd string) *Detector {
	return &Detector{cwd: cwd}
}

// Detect analyzes the workspace and returns context information.
func (d *Detector) Detect() *WorkspaceContext {
	ctx := &WorkspaceContext{
		CWD:          d.cwd,
		ProjectType:  "unknown",
		DetectedTags: make([]string, 0),
	}

	// Check for git repository
	if d.exists(".git") || d.exists("../.git") {
		ctx.IsGitRepo = true
		ctx.DetectedTags = append(ctx.DetectedTags, "git", "version-control")
	}

	// Check for Kubernetes configuration (local .kube/config or global ~/.kube/config)
	if d.exists(filepath.Join(".kube", "config")) {
		ctx.HasKubeConfig = true
		ctx.DetectedTags = append(ctx.DetectedTags, "kubernetes", "k8s")
	} else if home, err := os.UserHomeDir(); err == nil && d.fileExists(filepath.Join(home, ".kube", "config")) {
		ctx.HasKubeConfig = true
		ctx.DetectedTags = append(ctx.DetectedTags, "kubernetes", "k8s")
	}

	// Check for Docker
	if d.exists("Dockerfile") || d.exists("docker-compose.yml") || d.exists("docker-compose.yaml") {
		ctx.HasDockerfile = true
		ctx.DetectedTags = append(ctx.DetectedTags, "docker", "containers")
	}

	// Detect project type
	if d.exists("go.mod") {
		ctx.HasGoMod = true
		ctx.ProjectType = "go"
		ctx.DetectedTags = append(ctx.DetectedTags, "go", "golang")
	} else if d.exists("package.json") {
		ctx.HasPackageJSON = true
		ctx.ProjectType = "node"
		ctx.DetectedTags = append(ctx.DetectedTags, "javascript", "node", "typescript")
	} else if d.exists("pyproject.toml") || d.exists("setup.py") || d.exists("requirements.txt") {
		ctx.HasPyProject = true
		ctx.ProjectType = "python"
		ctx.DetectedTags = append(ctx.DetectedTags, "python")
	} else if d.exists("Cargo.toml") {
		ctx.HasCargoToml = true
		ctx.ProjectType = "rust"
		ctx.DetectedTags = append(ctx.DetectedTags, "rust")
	}

	// Suggest profile based on context
	ctx.SuggestedProfile = d.suggestProfile(ctx)

	return ctx
}

// suggestProfile returns the best profile for the detected context.
func (d *Detector) suggestProfile(ctx *WorkspaceContext) string {
	// Kubernetes context takes priority if kubeconfig exists and we're in k8s-related dir
	if ctx.HasKubeConfig {
		// Check if we're in a k8s/infra directory
		if d.exists("k8s") || d.exists("kubernetes") || d.exists("helm") || d.exists("manifests") {
			return "k8s-ops"
		}
	}

	// Git repo suggests dev profile
	if ctx.IsGitRepo {
		return "dev"
	}

	// Default to full
	return "full"
}

// exists checks if a path exists relative to cwd.
func (d *Detector) exists(path string) bool {
	_, err := os.Stat(filepath.Join(d.cwd, path))
	return err == nil
}

// fileExists checks if a file exists at an absolute path.
func (d *Detector) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// GetCurrentWorkingDirectory returns the current working directory.
func GetCurrentWorkingDirectory() (string, error) {
	return os.Getwd()
}

// AutoDetectProfile detects context and returns the suggested profile.
func AutoDetectProfile() string {
	cwd, err := GetCurrentWorkingDirectory()
	if err != nil {
		return "full"
	}
	detector := NewDetector(cwd)
	ctx := detector.Detect()
	return ctx.SuggestedProfile
}
