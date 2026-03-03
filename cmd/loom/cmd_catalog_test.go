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

	got := buildCatalogEntries(reg, "codex", "")
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

func TestBuildCatalogEntries_CategoryFilter(t *testing.T) {
	reg := &registry.Registry{
		Servers: []*registry.Server{
			{Name: "a", Categories: []string{"ops"}},
			{Name: "b", Categories: []string{"dev", "test"}},
			{Name: "c", Categories: []string{"security"}},
		},
	}

	got := buildCatalogEntries(reg, "codex", "DEV")
	if len(got) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(got))
	}
	if got[0].Name != "b" {
		t.Fatalf("entry name = %q, want b", got[0].Name)
	}
}
