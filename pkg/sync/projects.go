package sync

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// skipDirs contains directory names to skip during project discovery.
var skipDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"vendor":       {},
	"__pycache__":  {},
	".venv":        {},
	".tox":         {},
	".cache":       {},
	"bin":          {},
	"dist":         {},
	"build":        {},
	".terraform":   {},
	".gradle":      {},
	"target":       {},
	".next":        {},
	".nuxt":        {},
}

const maxDiscoverDepth = 6

// DiscoverProjectsWithFile walks workspaceRoot to find directories containing
// <profileRepoDir>/<relativeFile>. Returns absolute paths to project roots
// (the parent of the profile directory). If skipWorktrees is true, directories
// named ".worktrees" are skipped.
func DiscoverProjectsWithFile(workspaceRoot, profileRepoDir, relativeFile string, skipWorktrees bool) ([]string, error) {
	workspaceRoot = filepath.Clean(workspaceRoot)
	targetRel := filepath.Join(profileRepoDir, relativeFile)

	var projects []string

	err := filepath.WalkDir(workspaceRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip directories we can't read
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if !d.IsDir() {
			return nil
		}

		// Skip the workspace root itself (we check children)
		if path == workspaceRoot {
			return nil
		}

		name := d.Name()

		// Check depth relative to workspace root
		rel, _ := filepath.Rel(workspaceRoot, path)
		depth := strings.Count(rel, string(os.PathSeparator)) + 1
		if depth > maxDiscoverDepth {
			return fs.SkipDir
		}

		// Skip noise directories
		if _, skip := skipDirs[name]; skip {
			return fs.SkipDir
		}

		// Skip worktrees if requested
		if skipWorktrees && name == ".worktrees" {
			return fs.SkipDir
		}

		// Skip hidden dirs that are profile dirs themselves (don't recurse into .claude etc)
		if strings.HasPrefix(name, ".") && name == profileRepoDir {
			return fs.SkipDir
		}

		// Check if this directory contains <profileRepoDir>/<relativeFile>.
		candidate := filepath.Join(path, targetRel)
		if _, err := os.Stat(candidate); err == nil {
			projects = append(projects, path)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return projects, nil
}

// DiscoverProjects walks workspaceRoot to find directories containing
// <profileRepoDir>/settings.json (e.g. .claude/settings.json). Returns
// absolute paths to project roots (the parent of the profile directory).
// If skipWorktrees is true, directories named ".worktrees" are skipped.
func DiscoverProjects(workspaceRoot, profileRepoDir string, skipWorktrees bool) ([]string, error) {
	return DiscoverProjectsWithFile(workspaceRoot, profileRepoDir, "settings.json", skipWorktrees)
}
