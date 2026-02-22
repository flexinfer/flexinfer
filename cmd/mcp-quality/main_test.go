package main

import (
	"context"
	"testing"
)

func TestParseQualityParams_Defaults(t *testing.T) {
	args := map[string]any{}
	p := parseQualityParams(args)

	if p.Scope != "changed" {
		t.Errorf("expected scope 'changed', got %q", p.Scope)
	}
	if p.BaseRef != "HEAD~1" {
		t.Errorf("expected base_ref 'HEAD~1', got %q", p.BaseRef)
	}
	if len(p.Packages) != 0 {
		t.Errorf("expected no packages, got %v", p.Packages)
	}
}

func TestParseQualityParams_Explicit(t *testing.T) {
	args := map[string]any{
		"scope":    "package",
		"base_ref": "main",
		"packages": []any{"./pkg/...", "./cmd/loom"},
	}
	p := parseQualityParams(args)

	if p.Scope != "package" {
		t.Errorf("expected scope 'package', got %q", p.Scope)
	}
	if p.BaseRef != "main" {
		t.Errorf("expected base_ref 'main', got %q", p.BaseRef)
	}
	if len(p.Packages) != 2 {
		t.Errorf("expected 2 packages, got %d", len(p.Packages))
	}
}

func TestResolvePackages_All(t *testing.T) {
	ctx := context.Background()
	p := qualityParams{Scope: "all"}
	pkgs, err := resolvePackages(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0] != "./..." {
		t.Errorf("expected [./...], got %v", pkgs)
	}
}

func TestResolvePackages_ExplicitPackages(t *testing.T) {
	ctx := context.Background()
	p := qualityParams{
		Scope:    "package",
		Packages: []string{"./pkg/validate", "./cmd/loom"},
	}
	pkgs, err := resolvePackages(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Errorf("expected 2 packages, got %d", len(pkgs))
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		path    string
		pattern string
		want    bool
	}{
		{"cmd/mcp-quality/main.go", "cmd/mcp-*/**", true},
		{"cmd/mcp-docker/tools.go", "cmd/mcp-*/**", true},
		{"pkg/validate/args.go", "cmd/mcp-*/**", false},
		{"pkg/validate", "pkg/**", true},
		{"pkg/validate/sub", "pkg/**", true},
		{"cmd/loom", "pkg/**", false},
		{"internal/hud/app.go", "internal/hud/**", true},
		{"internal/daemon/daemon.go", "internal/hud/**", false},
	}

	for _, tt := range tests {
		got := matchPattern(tt.path, tt.pattern)
		if got != tt.want {
			t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.path, tt.pattern, got, tt.want)
		}
	}
}

func TestToolAvailable(t *testing.T) {
	// "go" should always be available in test environment
	if !toolAvailable("go") {
		t.Error("expected 'go' to be available")
	}
	if toolAvailable("nonexistent-tool-12345") {
		t.Error("expected nonexistent tool to not be available")
	}
}
