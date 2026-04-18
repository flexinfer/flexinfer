package mentatlab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateAutonomousFlow_Template ensures the shipped seed template passes
// validation. If this test fails after editing the template, the edit probably
// introduces a write-op node that bypasses the review gate.
func TestValidateAutonomousFlow_Template(t *testing.T) {
	// Template lives in cmd/mcp-mentatlab/templates/autonomous-refactor.yaml
	// relative to the repo root. Walk up from this test file to find it.
	path := findRepoFile(t, "cmd/mcp-mentatlab/templates/autonomous-refactor.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	if err := ValidateAutonomousFlow(data); err != nil {
		t.Fatalf("template should validate, got: %v", err)
	}
}

func TestValidateAutonomousFlow_WriteEdgeBypassesGate(t *testing.T) {
	// `commit` (shell, write op) wires directly from `plan` with no gate.
	yml := []byte(`
id: bad
version: 1
nodes:
  - id: plan
    type: llm
  - id: commit
    type: shell
edges:
  - { from: plan, to: commit }
`)
	err := ValidateAutonomousFlow(yml)
	if err == nil {
		t.Fatal("expected error for ungated write edge, got nil")
	}
	if !strings.Contains(err.Error(), "commit") || !strings.Contains(err.Error(), "human_gate") {
		t.Fatalf("error should mention offending node + gate, got: %v", err)
	}
}

func TestValidateAutonomousFlow_MissingGateNode(t *testing.T) {
	// No gate anywhere; shell write op has no gate upstream.
	yml := []byte(`
id: no-gate
version: 1
nodes:
  - id: plan
    type: llm
  - id: spawn
    type: agent_spawn
  - id: push
    type: shell
edges:
  - { from: plan, to: spawn }
  - { from: spawn, to: push }
`)
	err := ValidateAutonomousFlow(yml)
	if err == nil {
		t.Fatal("expected error for flow with no gate, got nil")
	}
	if !strings.Contains(err.Error(), "no upstream human_gate") {
		t.Fatalf("error should name the invariant clearly, got: %v", err)
	}
}

// TestValidateAutonomousFlow_ParallelGatedPaths covers a flow with two
// parallel write paths that each terminate through a gate. Both branches
// must be checked; either ungated branch fails.
func TestValidateAutonomousFlow_ParallelGatedPaths(t *testing.T) {
	yml := []byte(`
id: parallel
version: 1
nodes:
  - id: plan
    type: llm
  - id: spawn_a
    type: agent_spawn
  - id: spawn_b
    type: agent_spawn
  - id: gate_a
    type: human_gate
  - id: gate_b
    type: review_gate
  - id: commit_a
    type: shell
  - id: commit_b
    type: shell
edges:
  - { from: plan, to: spawn_a }
  - { from: plan, to: spawn_b }
  - { from: spawn_a, to: gate_a }
  - { from: spawn_b, to: gate_b }
  - { from: gate_a, to: commit_a }
  - { from: gate_b, to: commit_b }
`)
	if err := ValidateAutonomousFlow(yml); err != nil {
		t.Fatalf("parallel gated flow should validate, got: %v", err)
	}
}

func TestValidateAutonomousFlow_EmptyYAML(t *testing.T) {
	if err := ValidateAutonomousFlow(nil); err == nil {
		t.Fatal("expected error for nil yaml")
	}
	if err := ValidateAutonomousFlow([]byte("")); err == nil {
		t.Fatal("expected error for empty yaml")
	}
}

func TestValidateAutonomousFlow_MalformedYAML(t *testing.T) {
	err := ValidateAutonomousFlow([]byte("::not yaml:::\n  - ["))
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse flow yaml") {
		t.Fatalf("error should mention parse failure, got: %v", err)
	}
}

// TestValidateAutonomousFlow_GitPrefixIsWriteOp verifies node types beginning
// with `git_` are treated as write ops and therefore require a gate upstream.
func TestValidateAutonomousFlow_GitPrefixIsWriteOp(t *testing.T) {
	yml := []byte(`
id: git-prefix
version: 1
nodes:
  - id: plan
    type: llm
  - id: merge
    type: git_merge
edges:
  - { from: plan, to: merge }
`)
	err := ValidateAutonomousFlow(yml)
	if err == nil {
		t.Fatal("expected error for ungated git_* node")
	}
}

// TestIsWriteOpType exercises the exported classifier directly.
func TestIsWriteOpType(t *testing.T) {
	cases := map[string]bool{
		"shell":       true,
		"agent_spawn": true,
		"git_commit":  true,
		"git_push":    true,
		"llm":         false,
		"human_gate":  false,
		"":            false,
	}
	for typ, want := range cases {
		if got := IsWriteOpType(typ); got != want {
			t.Errorf("IsWriteOpType(%q) = %v, want %v", typ, got, want)
		}
	}
}

// findRepoFile walks up from the test's working directory until it locates
// the given repo-relative path. Keeps the test robust to `go test` being run
// from any subdirectory.
func findRepoFile(t *testing.T, rel string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for i := 0; i < 10; i++ {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find %s walking up from %s", rel, wd)
	return ""
}
