package hud

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// projectBuckets are the workspace subdirectories searched when the caller
// supplies a bare project name (e.g. "loom-core") instead of a
// workspace-relative path (e.g. "services/loom-core").
//
// Kept in sync with cmd/mcp-devbox/manager.go:resolveProject so spawns and
// devbox sandboxes agree on which repos a given name resolves to. Add new
// top-level buckets here rather than forking the list.
var projectBuckets = []string{"services", "libs", "platform", "private", "labs"}

// resolveProjectPath locates a project on disk relative to the workspace
// root. It returns the absolute local path (for Fingerprint/ContextDir) and
// the workspace-relative path (for pod-internal paths like
// "/workspace/<rel>"). When project is already a workspace-relative path
// containing a separator (e.g. "services/loom-core"), it is tried verbatim
// before falling back to the bucketed search.
//
// Previously spawn.go concatenated workspaceRoot and req.Project directly,
// which broke for monorepo-style layouts where repos live under
// services/, libs/, etc. — the detect.Fingerprint call then failed with
// "no languages detected" and the fallback Dockerfile was missing the
// agent CLI's runtime deps.
func resolveProjectPath(workspaceRoot, project string) (absPath, relPath string, err error) {
	if workspaceRoot == "" {
		return "", "", fmt.Errorf("workspace root is empty")
	}
	if project == "" {
		return "", "", fmt.Errorf("project is empty")
	}

	if filepath.IsAbs(project) {
		info, statErr := os.Stat(project)
		if statErr != nil || !info.IsDir() {
			return "", "", fmt.Errorf("project directory not found: %s", project)
		}
		rel, relErr := filepath.Rel(workspaceRoot, project)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			return "", "", fmt.Errorf("project %q is not under workspace root %q", project, workspaceRoot)
		}
		return project, filepath.ToSlash(rel), nil
	}

	cleaned := filepath.Clean(project)
	if strings.HasPrefix(cleaned, "..") || strings.HasPrefix(cleaned, "/") {
		return "", "", fmt.Errorf("invalid project path: %s", project)
	}

	candidates := make([]string, 0, 1+len(projectBuckets))
	// Try the workspace-relative path first so callers passing "services/foo"
	// land in the expected directory without double-prefixing.
	candidates = append(candidates, filepath.Join(workspaceRoot, cleaned))
	if !strings.ContainsRune(cleaned, filepath.Separator) {
		for _, bucket := range projectBuckets {
			candidates = append(candidates, filepath.Join(workspaceRoot, bucket, cleaned))
		}
	}

	for _, path := range candidates {
		info, statErr := os.Stat(path)
		if statErr != nil || !info.IsDir() {
			continue
		}
		rel, relErr := filepath.Rel(workspaceRoot, path)
		if relErr != nil {
			continue
		}
		return path, filepath.ToSlash(rel), nil
	}

	return "", "", fmt.Errorf("project %q not found under %s (searched %s)", project, workspaceRoot, strings.Join(projectBuckets, ", "))
}
