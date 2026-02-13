package agentcontext

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ArtifactDir describes a build artifact directory found inside a worktree.
type ArtifactDir struct {
	Path      string `json:"path"`
	Pattern   string `json:"pattern"`
	SizeBytes int64  `json:"size_bytes"`
}

// dirDiskUsage walks a directory tree and returns the total size in bytes.
// Symlinks are not followed.
func dirDiskUsage(path string) (int64, error) {
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if !info.IsDir() && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// parseArtifactPatterns splits a comma-separated pattern string into trimmed
// directory names. Empty entries are discarded.
func parseArtifactPatterns(csv string) []string {
	raw := strings.Split(csv, ",")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// findArtifactDirs locates artifact directories matching any of the given
// patterns within the root path. Only top-level and second-level matches are
// returned (we don't recurse deep inside node_modules looking for more
// node_modules).
func findArtifactDirs(root string, patterns []string) ([]ArtifactDir, error) {
	if len(patterns) == 0 {
		return nil, nil
	}

	patternSet := make(map[string]struct{}, len(patterns))
	for _, p := range patterns {
		patternSet[p] = struct{}{}
	}

	var results []ArtifactDir

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", root, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()

		// Check top-level match
		if _, ok := patternSet[name]; ok {
			full := filepath.Join(root, name)
			size, _ := dirDiskUsage(full)
			results = append(results, ArtifactDir{
				Path:      full,
				Pattern:   name,
				SizeBytes: size,
			})
			continue
		}

		// Check second-level (e.g., packages/foo/node_modules)
		subDir := filepath.Join(root, name)
		subEntries, err := os.ReadDir(subDir)
		if err != nil {
			continue
		}
		for _, se := range subEntries {
			if !se.IsDir() {
				continue
			}
			if _, ok := patternSet[se.Name()]; ok {
				full := filepath.Join(subDir, se.Name())
				size, _ := dirDiskUsage(full)
				results = append(results, ArtifactDir{
					Path:      full,
					Pattern:   se.Name(),
					SizeBytes: size,
				})
			}
		}
	}

	return results, nil
}

// removeArtifactDirs removes build artifact directories from a worktree path.
// When dryRun is true, directories are enumerated but not deleted.
// Returns total bytes freed, list of removed paths, and any error.
func removeArtifactDirs(root string, patterns []string, dryRun bool) (int64, []string, error) {
	artifacts, err := findArtifactDirs(root, patterns)
	if err != nil {
		return 0, nil, err
	}

	var totalFreed int64
	var removed []string

	for _, a := range artifacts {
		totalFreed += a.SizeBytes
		removed = append(removed, a.Path)
		if !dryRun {
			if err := os.RemoveAll(a.Path); err != nil {
				return totalFreed, removed, fmt.Errorf("remove %s: %w", a.Path, err)
			}
		}
	}

	return totalFreed, removed, nil
}

// humanizeBytes returns a human-readable representation of a byte count.
func humanizeBytes(b int64) string {
	const (
		_          = iota
		kB float64 = 1 << (10 * iota)
		mB
		gB
		tB
	)
	fb := float64(b)
	switch {
	case fb >= tB:
		return fmt.Sprintf("%.1f TiB", fb/tB)
	case fb >= gB:
		return fmt.Sprintf("%.1f GiB", fb/gB)
	case fb >= mB:
		return fmt.Sprintf("%.1f MiB", fb/mB)
	case fb >= kB:
		return fmt.Sprintf("%.1f KiB", fb/kB)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
