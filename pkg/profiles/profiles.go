// Package profiles provides tool filtering based on predefined and custom profiles.
package profiles

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/crb2nu/loom/pkg/mcp"
	"gopkg.in/yaml.v3"
)

// Profile defines a focused tool subset for specific use cases.
type Profile struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Include     IncludeSpec `yaml:"include"`
	MaxTools    int         `yaml:"maxTools,omitempty"`
	Priorities  map[string]int `yaml:"priorities,omitempty"`
}

// IncludeSpec specifies which tools/servers to include in a profile.
type IncludeSpec struct {
	Servers    []string `yaml:"servers,omitempty"`
	Categories []string `yaml:"categories,omitempty"`
	Tools      []string `yaml:"tools,omitempty"`
	Tags       []string `yaml:"tags,omitempty"`
}

// ProfileSet contains multiple profile definitions.
type ProfileSet struct {
	Profiles map[string]*Profile `yaml:"profiles"`
}

// Manager manages profile loading and tool filtering.
type Manager struct {
	profiles map[string]*Profile
}

// NewManager creates a new profile manager with default profiles.
func NewManager() *Manager {
	m := &Manager{
		profiles: make(map[string]*Profile),
	}
	m.loadDefaults()
	return m
}

// loadDefaults loads the built-in profile definitions.
func (m *Manager) loadDefaults() {
	m.profiles["dev"] = &Profile{
		Name:        "dev",
		Description: "Core development tools for coding workflows",
		MaxTools:    50,
		Include: IncludeSpec{
			Servers:    []string{"mcp-git", "mcp-git-worktree", "mcp-github", "mcp-gitlab"},
			Categories: []string{"version-control", "search"},
		},
		Priorities: map[string]int{
			"git_status": 10,
			"git_diff":   9,
			"git_log":    8,
			"git_commit": 7,
		},
	}

	m.profiles["k8s-ops"] = &Profile{
		Name:        "k8s-ops",
		Description: "Kubernetes cluster management and debugging",
		MaxTools:    40,
		Include: IncludeSpec{
			Servers:    []string{"mcp-k8s", "mcp-k8s-ops", "mcp-prometheus", "mcp-loki", "mcp-grafana"},
			Categories: []string{"kubernetes", "monitoring"},
		},
		Priorities: map[string]int{
			"list_pods":        10,
			"get_logs":         9,
			"list_deployments": 8,
		},
	}

	m.profiles["research"] = &Profile{
		Name:        "research",
		Description: "Web search and content extraction",
		MaxTools:    20,
		Include: IncludeSpec{
			Servers: []string{"mcp-tavily", "mcp-youtube"},
			Tags:    []string{"search", "web", "research"},
		},
	}

	m.profiles["infra"] = &Profile{
		Name:        "infra",
		Description: "Infrastructure and cloud management",
		MaxTools:    60,
		Include: IncludeSpec{
			Servers:    []string{"mcp-cloudflare", "mcp-k8s", "mcp-minio", "mcp-prometheus", "mcp-grafana"},
			Categories: []string{"cloud", "monitoring", "storage"},
		},
	}

	m.profiles["full"] = &Profile{
		Name:        "full",
		Description: "All available tools with smart prioritization",
		MaxTools:    100,
		Include: IncludeSpec{
			Servers: []string{"*"}, // All servers
		},
	}
}

// LoadFromFile loads additional profiles from a YAML file.
func (m *Manager) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No custom profiles, that's OK
		}
		return err
	}

	var ps ProfileSet
	if err := yaml.Unmarshal(data, &ps); err != nil {
		return err
	}

	// Merge custom profiles (overrides defaults)
	for name, profile := range ps.Profiles {
		m.profiles[name] = profile
	}

	return nil
}

// Get returns a profile by name, or nil if not found.
func (m *Manager) Get(name string) *Profile {
	return m.profiles[name]
}

// List returns all available profile names.
func (m *Manager) List() []string {
	names := make([]string, 0, len(m.profiles))
	for name := range m.profiles {
		names = append(names, name)
	}
	return names
}

// FilterResult contains the result of filtering tools.
type FilterResult struct {
	Tools       []mcp.Tool
	Profile     string
	TotalBefore int
	TotalAfter  int
	Truncated   bool
}

// Filter applies profile-based filtering to tools.
func (m *Manager) Filter(tools []mcp.Tool, profileName string) *FilterResult {
	profile := m.profiles[profileName]
	if profile == nil {
		profile = m.profiles["full"]
		profileName = "full"
	}

	result := &FilterResult{
		Profile:     profileName,
		TotalBefore: len(tools),
	}

	// "full" profile with * servers means all tools
	if len(profile.Include.Servers) == 1 && profile.Include.Servers[0] == "*" {
		result.Tools = tools
		if len(tools) > profile.MaxTools && profile.MaxTools > 0 {
			result.Truncated = true
			result.Tools = tools[:profile.MaxTools]
		}
		result.TotalAfter = len(result.Tools)
		return result
	}

	// Build server set for fast lookup
	serverSet := make(map[string]bool)
	for _, s := range profile.Include.Servers {
		serverSet[s] = true
	}

	// Filter tools by server
	var filtered []mcp.Tool
	for _, tool := range tools {
		// Extract server name from namespaced tool (server__toolname)
		serverName := extractServerName(tool.Name)
		if serverSet[serverName] {
			filtered = append(filtered, tool)
		}
	}

	// Sort by priority if priorities are defined
	if len(profile.Priorities) > 0 {
		sortByPriority(filtered, profile.Priorities)
	}

	// Apply limit
	if len(filtered) > profile.MaxTools && profile.MaxTools > 0 {
		result.Truncated = true
		filtered = filtered[:profile.MaxTools]
	}

	result.Tools = filtered
	result.TotalAfter = len(filtered)
	return result
}

// extractServerName extracts the server name from a namespaced tool name.
// Input: "mcp-git__git_status" -> Output: "mcp-git"
func extractServerName(toolName string) string {
	idx := strings.Index(toolName, "__")
	if idx == -1 {
		return ""
	}
	return toolName[:idx]
}

// sortByPriority sorts tools by their priority (higher priority first).
func sortByPriority(tools []mcp.Tool, priorities map[string]int) {
	// Simple bubble sort (small lists, priorities are optional)
	n := len(tools)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			p1 := getToolPriority(tools[j].Name, priorities)
			p2 := getToolPriority(tools[j+1].Name, priorities)
			if p1 < p2 {
				tools[j], tools[j+1] = tools[j+1], tools[j]
			}
		}
	}
}

// getToolPriority returns the priority for a tool, checking both full and base names.
func getToolPriority(toolName string, priorities map[string]int) int {
	// Check full name first
	if p, ok := priorities[toolName]; ok {
		return p
	}
	// Check base name (without server prefix)
	if idx := strings.Index(toolName, "__"); idx != -1 {
		baseName := toolName[idx+2:]
		if p, ok := priorities[baseName]; ok {
			return p
		}
	}
	return 0
}

// DefaultProfilePath returns the path to custom profiles file.
func DefaultProfilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "loom", "profiles.yaml")
}
