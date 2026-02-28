package backend

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// SyncDir describes a local directory to sync into a pod.
type SyncDir struct {
	LocalPath  string // absolute path on host
	RemotePath string // absolute path inside pod (e.g., "/workspace/services/loom-core")
}

// DiscoverDeps finds the project directory and any sibling dependencies
// referenced via Go replace directives. Returns SyncDir entries that preserve
// the workspace-relative directory structure so replace directives resolve
// correctly inside the pod.
//
// For non-Go projects (no go.mod), returns only the project directory itself.
func DiscoverDeps(projectDir, workspaceRoot string) ([]SyncDir, error) {
	// Always include the project itself.
	projectRel, err := filepath.Rel(workspaceRoot, projectDir)
	if err != nil || strings.HasPrefix(projectRel, "..") {
		// Project outside workspace root — sync project dir to /workspace.
		return []SyncDir{{LocalPath: projectDir, RemotePath: "/workspace"}}, nil
	}

	dirs := []SyncDir{
		{LocalPath: projectDir, RemotePath: filepath.Join("/workspace", projectRel)},
	}

	// Parse go.mod for relative replace directives.
	replaceDirs, err := parseGoModReplaceDirs(projectDir)
	if err != nil {
		// go.mod missing or unparseable — just sync the project.
		return dirs, nil
	}

	seen := map[string]bool{projectDir: true}
	for _, relPath := range replaceDirs {
		absPath := filepath.Join(projectDir, relPath)
		absPath = filepath.Clean(absPath)

		// Verify the directory exists.
		if info, err := os.Stat(absPath); err != nil || !info.IsDir() {
			continue
		}

		if seen[absPath] {
			continue
		}
		seen[absPath] = true

		depRel, err := filepath.Rel(workspaceRoot, absPath)
		if err != nil || strings.HasPrefix(depRel, "..") {
			continue // dep is outside workspace root — skip
		}

		dirs = append(dirs, SyncDir{
			LocalPath:  absPath,
			RemotePath: filepath.Join("/workspace", depRel),
		})
	}

	return dirs, nil
}

// parseGoModReplaceDirs reads go.mod and returns relative paths from replace
// directives (e.g., "../../libs/mcp-go").
func parseGoModReplaceDirs(projectDir string) ([]string, error) {
	f, err := os.Open(filepath.Join(projectDir, "go.mod"))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var dirs []string
	inReplaceBlock := false
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Track multi-line replace blocks: replace ( ... )
		if strings.HasPrefix(line, "replace (") || strings.HasPrefix(line, "replace(") {
			inReplaceBlock = true
			continue
		}
		if inReplaceBlock && line == ")" {
			inReplaceBlock = false
			continue
		}

		// Single-line: replace module => path
		if strings.HasPrefix(line, "replace ") && !strings.Contains(line, "(") {
			if dir := extractReplacePath(line[len("replace "):]); dir != "" {
				dirs = append(dirs, dir)
			}
			continue
		}

		// Inside replace block: module => path
		if inReplaceBlock {
			if dir := extractReplacePath(line); dir != "" {
				dirs = append(dirs, dir)
			}
		}
	}

	return dirs, scanner.Err()
}

// extractReplacePath extracts the local directory from a replace line like:
//
//	module/path => ../../libs/mcp-go
//	module/path v1.0.0 => ../../libs/mcp-go
//
// Returns "" for non-local replacements (those with version specifiers on the
// right side like "module/path v1.2.3").
func extractReplacePath(line string) string {
	parts := strings.SplitN(line, "=>", 2)
	if len(parts) != 2 {
		return ""
	}

	target := strings.TrimSpace(parts[1])
	if target == "" {
		return ""
	}

	// If the target starts with "." or "/" it's a local path.
	// Remote replacements have a module path + version like "github.com/foo v1.2.3".
	if strings.HasPrefix(target, ".") || strings.HasPrefix(target, "/") {
		// Strip any trailing version comment.
		if idx := strings.Index(target, " "); idx > 0 {
			// Only if it looks like a version (starts with "v" or "//").
			suffix := strings.TrimSpace(target[idx:])
			if strings.HasPrefix(suffix, "//") {
				target = target[:idx]
			}
		}
		return target
	}

	return ""
}
