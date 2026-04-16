package main

import (
	"testing"

	"github.com/crb2nu/loom/pkg/registry"
)

func TestBuildCatalogEntries_SortsAndResolvesSpec(t *testing.T) {
	reg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name:       "zeta",
				Categories: []string{"ops"},
				Common: &registry.TargetSpec{
					Description: "zeta common",
				},
				Targets: map[string]*registry.TargetSpec{
					"codex": {
						Description: "zeta codex",
						Command:     "mcp-zeta",
					},
				},
			},
			{
				Name:       "alpha",
				Categories: []string{"dev"},
				Common: &registry.TargetSpec{
					Description: "alpha common",
					Command:     "mcp-alpha-common",
				},
				Targets: map[string]*registry.TargetSpec{
					"codex": {
						Command: "mcp-alpha",
					},
				},
			},
		},
	}

	got := buildCatalogEntries(reg, nil, "codex", "", "")
	if len(got) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(got))
	}
	if got[0].Name != "alpha" || got[1].Name != "zeta" {
		t.Fatalf("entries not sorted by name: %#v", got)
	}
	if got[0].Description != "alpha common" {
		t.Fatalf("alpha description = %q, want common fallback", got[0].Description)
	}
	if got[0].Command != "mcp-alpha" {
		t.Fatalf("alpha command = %q, want codex command", got[0].Command)
	}
	if got[1].Description != "zeta codex" {
		t.Fatalf("zeta description = %q, want codex description", got[1].Description)
	}
	if got[1].Command != "mcp-zeta" {
		t.Fatalf("zeta command = %q, want mcp-zeta", got[1].Command)
	}
}

func TestBuildCatalogEntries_EnabledStatus(t *testing.T) {
	reg := &registry.Registry{
		Servers: []*registry.Server{
			{Name: "a"},
			{Name: "b"},
			{Name: "c"},
		},
	}

	cs := &registry.CatalogState{}
	cs.Disable("b")

	got := buildCatalogEntries(reg, cs, "codex", "", "")
	if len(got) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(got))
	}
	if !got[0].Enabled {
		t.Error("expected a to be enabled")
	}
	if got[1].Enabled {
		t.Error("expected b to be disabled")
	}
	if !got[2].Enabled {
		t.Error("expected c to be enabled")
	}
}

func TestBuildCatalogEntries_CategoryFilter(t *testing.T) {
	reg := &registry.Registry{
		Servers: []*registry.Server{
			{Name: "a", Categories: []string{"ops"}},
			{Name: "b", Categories: []string{"dev", "test"}},
			{Name: "c", Categories: []string{"security"}},
		},
	}

	got := buildCatalogEntries(reg, nil, "codex", "DEV", "")
	if len(got) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(got))
	}
	if got[0].Name != "b" {
		t.Fatalf("entry name = %q, want b", got[0].Name)
	}
}

func TestBuildCatalogEntries_EnrichedFieldsAndSearch(t *testing.T) {
	reg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "searchable",
				Common: &registry.TargetSpec{
					Description: "search target",
					Command:     "mcp-search",
					Env: map[string]string{
						"SEARCH_TOKEN": "value",
						"SEARCH_URL":   "https://example.test",
					},
					Hint:        "network",
					Timeout:     45,
					AlwaysAllow: []string{"*"},
					SSH: &registry.SSHSpec{
						User: "loom",
						Host: "example.test",
					},
					Tools: []registry.ToolSchema{
						{Name: "one"},
						{Name: "two"},
					},
				},
			},
			{Name: "other"},
		},
	}

	got := buildCatalogEntries(reg, nil, "codex", "", "network")
	if len(got) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(got))
	}
	if got[0].Name != "searchable" {
		t.Fatalf("entry name = %q, want searchable", got[0].Name)
	}
	if got[0].ToolCount != 2 {
		t.Fatalf("tool count = %d, want 2", got[0].ToolCount)
	}
	if got[0].Command != "mcp-search" {
		t.Fatalf("command = %q, want mcp-search", got[0].Command)
	}
	if len(got[0].EnvHints) != 2 {
		t.Fatalf("env hints = %v, want 2 keys", got[0].EnvHints)
	}
	if len(got[0].ConfigHints) == 0 {
		t.Fatal("expected config hints to be populated")
	}
}
