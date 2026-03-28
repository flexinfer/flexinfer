package daemon

import (
	"os"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/templatevars"
)

// toolsResult holds the aggregated tools response.
type toolsResult struct {
	Tools       []mcp.Tool `json:"tools"`
	CachedAt    time.Time  `json:"cachedAt"`
	ServerCount int        `json:"serverCount"`
}

func normalizeServerFilters(servers []string) []string {
	if len(servers) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(servers))
	out := make([]string, 0, len(servers))
	for _, raw := range servers {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func splitNamespacedToolName(name string) (server, tool string) {
	parts := strings.SplitN(strings.TrimSpace(name), "__", 2)
	if len(parts) != 2 {
		return "", strings.TrimSpace(name)
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func containsServerFilter(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

// toolNamesChanged reports whether the set of tool names differs between old and new.
func toolNamesChanged(oldTools, newTools []mcp.Tool) bool {
	if len(oldTools) != len(newTools) {
		return true
	}
	old := make(map[string]bool, len(oldTools))
	for _, t := range oldTools {
		old[t.Name] = true
	}
	for _, t := range newTools {
		if !old[t.Name] {
			return true
		}
	}
	return false
}

// resourceNamesChanged reports whether the set of resource URIs differs between old and new.
func resourceNamesChanged(oldRes, newRes []mcp.Resource) bool {
	if len(oldRes) != len(newRes) {
		return true
	}
	old := make(map[string]bool, len(oldRes))
	for _, r := range oldRes {
		old[r.URI] = true
	}
	for _, r := range newRes {
		if !old[r.URI] {
			return true
		}
	}
	return false
}

// expandVarsWithRegistry expands variable patterns with registry-based env aliases.
// - ${repo}: Repository root
// - ${HOME}: User home directory
// - ${env:VAR}: Environment variable (with fallback alias support)
// - ${keychain:VAR}: Keychain reference (treated as env var for now)
func expandVarsWithRegistry(s string, repoRoot string, reg *registry.Registry) string {
	// Expand ${HOME}
	if home, err := os.UserHomeDir(); err == nil {
		s = strings.ReplaceAll(s, "${HOME}", home)
	}

	// Expand ${repo}
	if repoRoot != "" {
		s = strings.ReplaceAll(s, "${repo}", repoRoot)
	}

	// Delegate ${env:}, ${keychain:}, ${secret:} to the shared expander
	exp := templatevars.New(
		templatevars.WithRegistry(reg),
		templatevars.WithLazySecrets(),
	)
	return exp.Expand(s)
}

// expandVars expands variable patterns in strings (uses daemon's repoRoot and registry).
func (d *Daemon) expandVars(s string) string {
	return expandVarsWithRegistry(s, d.repoRoot, d.registry)
}
