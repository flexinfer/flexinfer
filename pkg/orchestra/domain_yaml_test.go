package orchestra

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDomainsFromFile_Valid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "domains.yaml")

	content := `domains:
  - name: custom-ops
    description: Custom operations domain
    tools:
      - custom__tool_a
      - custom__tool_b
  - name: analytics
    description: Analytics domain
    tools:
      - analytics__query
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	agents, err := LoadDomainsFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(agents))
	}

	if agents[0].Name != "custom-ops" {
		t.Errorf("expected name 'custom-ops', got %q", agents[0].Name)
	}
	if agents[0].Description != "Custom operations domain" {
		t.Errorf("expected description 'Custom operations domain', got %q", agents[0].Description)
	}
	if len(agents[0].Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(agents[0].Tools))
	}

	if agents[1].Name != "analytics" {
		t.Errorf("expected name 'analytics', got %q", agents[1].Name)
	}
	if len(agents[1].Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(agents[1].Tools))
	}
}

func TestLoadDomainsFromFile_MissingFile(t *testing.T) {
	t.Parallel()

	agents, err := LoadDomainsFromFile("/nonexistent/path/domains.yaml")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if agents != nil {
		t.Fatalf("expected nil agents for missing file, got %d", len(agents))
	}
}

func TestLoadDomainsFromFile_InvalidYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")

	if err := os.WriteFile(path, []byte("{{not: valid: yaml:::"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := LoadDomainsFromFile(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadDomainsFromFile_MissingName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "no-name.yaml")

	content := `domains:
  - description: No name domain
    tools:
      - some_tool
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := LoadDomainsFromFile(path)
	if err == nil {
		t.Fatal("expected error for domain without name")
	}
}

func TestLoadDomainsFromFile_NoTools(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "no-tools.yaml")

	content := `domains:
  - name: empty-domain
    description: Domain with no tools
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := LoadDomainsFromFile(path)
	if err == nil {
		t.Fatal("expected error for domain without tools")
	}
}

func TestLoadDomainsFromFile_EmptyDomains(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")

	content := `domains: []
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	agents, err := LoadDomainsFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("expected 0 domains, got %d", len(agents))
	}
}

func TestLoadDomainsFromFile_WithOptionalFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "full.yaml")

	content := `domains:
  - name: custom
    description: Custom domain
    system_prompt: You are a custom agent.
    tools:
      - custom__tool
    model: custom-model
    token_budget: 2048
    max_tokens: 512
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	agents, err := LoadDomainsFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(agents))
	}

	a := agents[0]
	if a.SystemPrompt != "You are a custom agent." {
		t.Errorf("expected system_prompt, got %q", a.SystemPrompt)
	}
	if a.Model != "custom-model" {
		t.Errorf("expected model 'custom-model', got %q", a.Model)
	}
	if a.TokenBudget != 2048 {
		t.Errorf("expected token_budget 2048, got %d", a.TokenBudget)
	}
	if a.MaxTokens != 512 {
		t.Errorf("expected max_tokens 512, got %d", a.MaxTokens)
	}
}

func TestMergeDomainsIntoRegistry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "merge.yaml")

	// Create YAML that overrides the "codebase" default and adds a new domain.
	content := `domains:
  - name: codebase
    description: Overridden codebase domain
    tools:
      - git__git_status
      - git__git_log
      - git__git_stash
  - name: new-domain
    description: Brand new domain
    tools:
      - new__tool_a
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	reg := NewDomainRegistry()
	// Register a default codebase domain.
	reg.Register(SubAgent{
		Name:        "codebase",
		Description: "Original codebase",
		Tools:       []string{"git__git_status", "git__git_diff"},
	})

	if err := MergeDomainsIntoRegistry(reg, path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify override.
	got, ok := reg.Get("codebase")
	if !ok {
		t.Fatal("expected codebase domain to exist")
	}
	if got.Description != "Overridden codebase domain" {
		t.Errorf("expected overridden description, got %q", got.Description)
	}
	if len(got.Tools) != 3 {
		t.Errorf("expected 3 tools after override, got %d", len(got.Tools))
	}

	// Verify new domain was added.
	_, ok = reg.Get("new-domain")
	if !ok {
		t.Error("expected new-domain to exist after merge")
	}
}

func TestMergeDomainsIntoRegistry_MissingFile(t *testing.T) {
	t.Parallel()

	reg := NewDomainRegistry()
	reg.Register(SubAgent{
		Name:  "existing",
		Tools: []string{"tool"},
	})

	// Missing file should not error and not change the registry.
	if err := MergeDomainsIntoRegistry(reg, "/nonexistent/file.yaml"); err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}

	if len(reg.List()) != 1 {
		t.Errorf("expected registry unchanged, got %d domains", len(reg.List()))
	}
}

func TestMergeDomainsIntoRegistry_InvalidFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.yaml")

	if err := os.WriteFile(path, []byte("{{bad yaml"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	reg := NewDomainRegistry()
	err := MergeDomainsIntoRegistry(reg, path)
	if err == nil {
		t.Fatal("expected error for invalid YAML file")
	}
}

func TestDefaultDomainsPath(t *testing.T) {
	t.Parallel()

	path := DefaultDomainsPath()
	if path == "" {
		t.Fatal("expected non-empty default path")
	}
	// Should end with the expected filename.
	if filepath.Base(path) != "orchestra-domains.yaml" {
		t.Errorf("expected path to end with 'orchestra-domains.yaml', got %q", filepath.Base(path))
	}
}
