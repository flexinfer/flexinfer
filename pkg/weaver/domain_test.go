package weaver

import (
	"sort"
	"strings"
	"testing"
)

func TestDomainRegistry_CRUD(t *testing.T) {
	reg := NewDomainRegistry()

	// Empty registry.
	if got := reg.List(); len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}

	// Register.
	agent := SubAgent{
		Name:        "test-domain",
		Description: "Test domain",
		Tools:       []string{"tool1", "tool2"},
	}
	reg.Register(agent)

	got, ok := reg.Get("test-domain")
	if !ok {
		t.Fatal("expected domain to exist")
	}
	if got.Description != "Test domain" {
		t.Errorf("expected description 'Test domain', got %q", got.Description)
	}

	// List.
	all := reg.List()
	if len(all) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(all))
	}

	// Override.
	agent.Description = "Updated"
	reg.Register(agent)
	got, _ = reg.Get("test-domain")
	if got.Description != "Updated" {
		t.Errorf("expected 'Updated', got %q", got.Description)
	}

	// Names.
	names := reg.Names()
	if len(names) != 1 || names[0] != "test-domain" {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestDomainRegistry_ToolToDomains(t *testing.T) {
	reg := NewDomainRegistry()

	reg.Register(SubAgent{
		Name:  "domain-a",
		Tools: []string{"shared-tool", "tool-a"},
	})
	reg.Register(SubAgent{
		Name:  "domain-b",
		Tools: []string{"shared-tool", "tool-b"},
	})

	m := reg.ToolToDomains()

	if domains, ok := m["shared-tool"]; !ok || len(domains) != 2 {
		t.Errorf("expected shared-tool in 2 domains, got %v", m["shared-tool"])
	}
	if domains, ok := m["tool-a"]; !ok || len(domains) != 1 {
		t.Errorf("expected tool-a in 1 domain, got %v", domains)
	}
}

func TestDefaultDomains(t *testing.T) {
	domains := DefaultDomains()
	if len(domains) != 6 {
		t.Fatalf("expected 6 default domains, got %d", len(domains))
	}

	names := make([]string, len(domains))
	for i, d := range domains {
		names[i] = d.Name
		if len(d.Tools) == 0 {
			t.Errorf("domain %q has no tools", d.Name)
		}
		if d.Description == "" {
			t.Errorf("domain %q has no description", d.Name)
		}
	}

	sort.Strings(names)
	expected := []string{"agent-fleet", "ci-pipeline", "cluster-ops", "codebase", "infra-ops", "observability"}
	for i, n := range names {
		if n != expected[i] {
			t.Errorf("expected domain %q, got %q", expected[i], n)
		}
	}
}

func TestDefaultDomains_AllHaveSystemPrompts(t *testing.T) {
	for _, d := range DefaultDomains() {
		if d.SystemPrompt == "" {
			t.Errorf("domain %q has empty SystemPrompt", d.Name)
		}
	}
}

func TestDefaultDomains_ToolCounts(t *testing.T) {
	expected := map[string]int{
		"agent-fleet":   4,
		"ci-pipeline":   6,
		"cluster-ops":   6,
		"codebase":      8,
		"infra-ops":     5,
		"observability": 6,
	}

	for _, d := range DefaultDomains() {
		want, ok := expected[d.Name]
		if !ok {
			t.Errorf("unexpected domain %q", d.Name)
			continue
		}
		if len(d.Tools) != want {
			t.Errorf("domain %q: expected %d tools, got %d", d.Name, want, len(d.Tools))
		}
	}
}

func TestDomainRegistry_GetMissing(t *testing.T) {
	reg := NewDomainRegistry()
	_, ok := reg.Get("nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

func TestDomainRegistry_ValidateTools(t *testing.T) {
	reg := NewDomainRegistry()
	reg.Register(SubAgent{
		Name:  "test-domain",
		Tools: []string{"existing_tool", "missing_tool"},
	})

	lister := &fakeToolLister{
		tools: []ToolInfo{{Name: "existing_tool"}},
	}

	warnings := reg.ValidateTools(lister)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "missing_tool") {
		t.Errorf("expected warning about missing_tool, got: %s", warnings[0])
	}
}

func TestDomainRegistry_ValidateTools_AllPresent(t *testing.T) {
	reg := NewDomainRegistry()
	reg.Register(SubAgent{
		Name:  "test-domain",
		Tools: []string{"tool_a", "tool_b"},
	})

	lister := &fakeToolLister{
		tools: []ToolInfo{{Name: "tool_a"}, {Name: "tool_b"}},
	}

	warnings := reg.ValidateTools(lister)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %d: %v", len(warnings), warnings)
	}
}
