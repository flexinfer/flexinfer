package profiles

import (
	"sort"
	"testing"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// ---------------------------------------------------------------------------
// NewManager
// ---------------------------------------------------------------------------

func TestNewManager(t *testing.T) {
	t.Parallel()

	m := NewManager()
	if m == nil {
		t.Fatal("NewManager returned nil")
	}

	wantProfiles := []string{"dev", "full", "infra", "k8s-ops", "research"}
	got := m.List()
	sort.Strings(got)

	if len(got) != len(wantProfiles) {
		t.Fatalf("List() returned %d profiles, want %d: %v", len(got), len(wantProfiles), got)
	}
	for i, name := range wantProfiles {
		if got[i] != name {
			t.Errorf("List()[%d] = %q, want %q", i, got[i], name)
		}
	}
}

// ---------------------------------------------------------------------------
// Manager.Get
// ---------------------------------------------------------------------------

func TestManager_Get(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		profile   string
		wantNil   bool
		wantName  string
		wantDesc  string
		wantMaxGT int // MaxTools should be > this value
	}{
		{
			name:      "existing dev profile",
			profile:   "dev",
			wantNil:   false,
			wantName:  "dev",
			wantDesc:  "Core development tools for coding workflows",
			wantMaxGT: 0,
		},
		{
			name:      "existing full profile",
			profile:   "full",
			wantNil:   false,
			wantName:  "full",
			wantDesc:  "All available tools with smart prioritization",
			wantMaxGT: 0,
		},
		{
			name:      "existing k8s-ops profile",
			profile:   "k8s-ops",
			wantNil:   false,
			wantName:  "k8s-ops",
			wantDesc:  "Kubernetes cluster management and debugging",
			wantMaxGT: 0,
		},
		{
			name:      "existing research profile",
			profile:   "research",
			wantNil:   false,
			wantName:  "research",
			wantDesc:  "Web search and content extraction",
			wantMaxGT: 0,
		},
		{
			name:      "existing infra profile",
			profile:   "infra",
			wantNil:   false,
			wantName:  "infra",
			wantDesc:  "Infrastructure and cloud management",
			wantMaxGT: 0,
		},
		{
			name:    "non-existent profile",
			profile: "nonexistent",
			wantNil: true,
		},
		{
			name:    "empty string",
			profile: "",
			wantNil: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := NewManager()
			p := m.Get(tt.profile)

			if tt.wantNil {
				if p != nil {
					t.Errorf("Get(%q) = %v, want nil", tt.profile, p)
				}
				return
			}
			if p == nil {
				t.Fatalf("Get(%q) returned nil, want non-nil", tt.profile)
			}
			if p.Name != tt.wantName {
				t.Errorf("Get(%q).Name = %q, want %q", tt.profile, p.Name, tt.wantName)
			}
			if p.Description != tt.wantDesc {
				t.Errorf("Get(%q).Description = %q, want %q", tt.profile, p.Description, tt.wantDesc)
			}
			if p.MaxTools <= tt.wantMaxGT {
				t.Errorf("Get(%q).MaxTools = %d, want > %d", tt.profile, p.MaxTools, tt.wantMaxGT)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Manager.List
// ---------------------------------------------------------------------------

func TestManager_List(t *testing.T) {
	t.Parallel()

	m := NewManager()
	names := m.List()

	if len(names) != 5 {
		t.Fatalf("List() returned %d names, want 5", len(names))
	}

	// Verify all expected names are present.
	expected := map[string]bool{
		"dev": true, "k8s-ops": true, "research": true,
		"infra": true, "full": true,
	}
	for _, n := range names {
		if !expected[n] {
			t.Errorf("List() contains unexpected profile %q", n)
		}
		delete(expected, n)
	}
	for n := range expected {
		t.Errorf("List() missing expected profile %q", n)
	}
}

// ---------------------------------------------------------------------------
// Manager.Filter — full profile returns all tools
// ---------------------------------------------------------------------------

func TestManager_Filter_Full(t *testing.T) {
	t.Parallel()

	m := NewManager()
	tools := makeMockTools(
		"mcp-git__git_status",
		"mcp-git__git_diff",
		"mcp-k8s__list_pods",
		"mcp-tavily__search",
	)

	result := m.Filter(tools, "full")

	if result == nil {
		t.Fatal("Filter returned nil")
	}
	if result.Profile != "full" {
		t.Errorf("Profile = %q, want %q", result.Profile, "full")
	}
	if result.TotalBefore != 4 {
		t.Errorf("TotalBefore = %d, want 4", result.TotalBefore)
	}
	if result.TotalAfter != 4 {
		t.Errorf("TotalAfter = %d, want 4", result.TotalAfter)
	}
	if result.Truncated {
		t.Error("Truncated = true, want false")
	}
	if len(result.Tools) != 4 {
		t.Errorf("len(Tools) = %d, want 4", len(result.Tools))
	}
}

// ---------------------------------------------------------------------------
// Manager.Filter — dev profile filters by server
// ---------------------------------------------------------------------------

func TestManager_Filter_Dev(t *testing.T) {
	t.Parallel()

	m := NewManager()
	tools := makeMockTools(
		"mcp-git__git_status",
		"mcp-git__git_diff",
		"mcp-k8s__list_pods",
		"mcp-tavily__search",
		"mcp-github__list_repos",
	)

	result := m.Filter(tools, "dev")

	if result == nil {
		t.Fatal("Filter returned nil")
	}
	if result.Profile != "dev" {
		t.Errorf("Profile = %q, want %q", result.Profile, "dev")
	}
	if result.TotalBefore != 5 {
		t.Errorf("TotalBefore = %d, want 5", result.TotalBefore)
	}

	// dev includes mcp-git and mcp-github servers; should exclude mcp-k8s and mcp-tavily.
	for _, tool := range result.Tools {
		if tool.Name == "mcp-k8s__list_pods" || tool.Name == "mcp-tavily__search" {
			t.Errorf("dev filter should not include %q", tool.Name)
		}
	}
}

// ---------------------------------------------------------------------------
// Manager.Filter — unknown profile falls back to full
// ---------------------------------------------------------------------------

func TestManager_Filter_UnknownFallsBackToFull(t *testing.T) {
	t.Parallel()

	m := NewManager()
	tools := makeMockTools("mcp-git__git_status", "mcp-k8s__list_pods")

	result := m.Filter(tools, "nonexistent")

	if result.Profile != "full" {
		t.Errorf("Profile = %q, want %q (fallback)", result.Profile, "full")
	}
	if result.TotalAfter != 2 {
		t.Errorf("TotalAfter = %d, want 2", result.TotalAfter)
	}
}

// ---------------------------------------------------------------------------
// DefaultProfilePath
// ---------------------------------------------------------------------------

func TestDefaultProfilePath(t *testing.T) {
	t.Parallel()

	path := DefaultProfilePath()
	if path == "" {
		t.Fatal("DefaultProfilePath() returned empty string")
	}

	// Should contain the config directory and filename.
	wantSuffix := ".config/fi-mcp/profiles.yaml"
	if len(path) < len(wantSuffix) {
		t.Fatalf("DefaultProfilePath() = %q, too short to contain %q", path, wantSuffix)
	}
	gotSuffix := path[len(path)-len(wantSuffix):]
	if gotSuffix != wantSuffix {
		t.Errorf("DefaultProfilePath() suffix = %q, want %q", gotSuffix, wantSuffix)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeMockTools(names ...string) []mcp.Tool {
	tools := make([]mcp.Tool, len(names))
	for i, name := range names {
		tools[i] = mcp.Tool{
			Name:        name,
			Description: "mock tool " + name,
		}
	}
	return tools
}
