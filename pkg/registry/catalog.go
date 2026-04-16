package registry

import (
	"fmt"
	"sort"
	"strings"
)

// CatalogEntry is the shared discovery shape used by the CLI and HUD.
type CatalogEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Command     string   `json:"command,omitempty"`
	Enabled     bool     `json:"enabled"`
	Running     bool     `json:"running"`
	ToolCount   int      `json:"tool_count"`
	EnvHints    []string `json:"env_hints,omitempty"`
	ConfigHints []string `json:"config_hints,omitempty"`
}

// BuildCatalogEntries returns registry servers enriched with derived discovery
// metadata and optional search/category filters.
func BuildCatalogEntries(reg *Registry, cs *CatalogState, targetProfile, categoryFilter, query string, runningByName map[string]bool) []CatalogEntry {
	if reg == nil {
		return nil
	}

	categoryNeedle := strings.TrimSpace(strings.ToLower(categoryFilter))
	queryNeedle := strings.TrimSpace(strings.ToLower(query))
	entries := make([]CatalogEntry, 0, len(reg.Servers))

	for _, srv := range reg.Servers {
		if srv == nil {
			continue
		}
		if categoryNeedle != "" && !serverHasCategory(srv, categoryNeedle) {
			continue
		}

		entry := CatalogEntry{
			Name:       srv.Name,
			Categories: sortedCopyStrings(srv.Categories),
			Enabled:    cs == nil || !cs.IsDisabled(srv.Name),
		}
		if runningByName != nil {
			entry.Running = runningByName[srv.Name]
		}

		if spec, err := reg.GetServerSpec(srv.Name, targetProfile); err == nil && spec != nil {
			entry.Description = strings.TrimSpace(spec.Description)
			entry.Command = strings.TrimSpace(spec.Command)
			entry.ToolCount = len(spec.Tools)
			entry.EnvHints = sortedMapKeys(spec.Env)
			entry.ConfigHints = catalogConfigHints(spec)
		}

		if entry.Description == "" && srv.Common != nil {
			entry.Description = strings.TrimSpace(srv.Common.Description)
		}

		if queryNeedle != "" && !catalogEntryMatchesQuery(entry, queryNeedle) {
			continue
		}

		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func catalogEntryMatchesQuery(entry CatalogEntry, needle string) bool {
	if needle == "" {
		return true
	}

	haystacks := []string{
		entry.Name,
		entry.Description,
		entry.Command,
		strings.Join(entry.Categories, " "),
		strings.Join(entry.EnvHints, " "),
		strings.Join(entry.ConfigHints, " "),
	}
	for _, haystack := range haystacks {
		if strings.Contains(strings.ToLower(haystack), needle) {
			return true
		}
	}
	return false
}

func catalogConfigHints(spec *TargetSpec) []string {
	if spec == nil {
		return nil
	}

	hints := make([]string, 0, 4)
	if spec.Hint != "" {
		hints = append(hints, "hint: "+spec.Hint)
	}
	if spec.Timeout > 0 {
		hints = append(hints, fmt.Sprintf("timeout: %ds", spec.Timeout))
	}
	if len(spec.AlwaysAllow) > 0 {
		hints = append(hints, "allow: "+strings.Join(spec.AlwaysAllow, ", "))
	}
	if ssh := spec.SSH; ssh != nil {
		switch {
		case strings.TrimSpace(ssh.User) != "" && strings.TrimSpace(ssh.Host) != "":
			hints = append(hints, "ssh: "+strings.TrimSpace(ssh.User)+"@"+strings.TrimSpace(ssh.Host))
		case strings.TrimSpace(ssh.Host) != "":
			hints = append(hints, "ssh: "+strings.TrimSpace(ssh.Host))
		}
	}
	if len(spec.Args) > 0 {
		args := make([]string, 0, len(spec.Args))
		for _, arg := range spec.Args {
			args = append(args, fmt.Sprint(arg))
		}
		hints = append(hints, "args: "+strings.Join(args, " "))
	}
	return hints
}

func sortedMapKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func serverHasCategory(srv *Server, categoryLower string) bool {
	for _, c := range srv.Categories {
		if strings.EqualFold(strings.TrimSpace(c), categoryLower) {
			return true
		}
	}
	return false
}

func sortedCopyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
