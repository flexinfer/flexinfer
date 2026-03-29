// ops.go — Shared discovery helpers used by sync operations.
// Platform-specific logic lives in ops_gemini.go, ops_claude.go, ops_codex.go.
// Core sync entry points live in ops_sync.go. Regeneration lives in ops_regen.go.
// Validation and backup live in ops_validate.go. Shared utilities live in ops_helpers.go.
package sync

import (
	"path/filepath"

	"github.com/crb2nu/loom/pkg/registry"
)

func ancestorRoots(start string) []string {
	var roots []string
	current := filepath.Clean(start)
	for {
		roots = append(roots, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return roots
}

func discoverWorkspaceContextFile(repoRoot, filename string) string {
	seen := map[string]struct{}{}
	for _, root := range ancestorRoots(repoRoot) {
		candidates := []string{
			filepath.Join(root, "mcp", "context", filename),
			filepath.Join(root, "services", "loom-core", "mcp", "context", filename),
			filepath.Join(root, "platform", "gitops", "mcp", "context", filename),
		}
		for _, candidate := range candidates {
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			if Exists(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func discoverRegistryPath(repoRoot string) string {
	// Prefer workspace-local registries first (repo overrides + ancestor workspace
	// roots) before falling back to user defaults.
	if local := discoverWorkspaceContextFile(repoRoot, "registry.yaml"); local != "" {
		return local
	}
	return registry.FindRegistryOrDefault(filepath.Join(repoRoot, "mcp", "context", "registry.yaml"))
}
