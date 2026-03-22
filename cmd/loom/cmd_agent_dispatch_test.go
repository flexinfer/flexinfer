package main

import (
	"path/filepath"
	"testing"
)

func TestLoadWorkflowFile_FeatureDevIncludesManagedWorktreeContract(t *testing.T) {
	path := filepath.Join("..", "..", ".agents", "workflows", "feature-dev.yaml")

	body, err := loadWorkflowFile(path, "", "codex")
	if err != nil {
		t.Fatalf("loadWorkflowFile(feature-dev): %v", err)
	}

	steps, ok := body["steps"].([]map[string]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("expected workflow steps, got %#v", body["steps"])
	}

	var (
		sawWorktreeAllocate bool
		sawWorktreeRelease  bool
		sawDevboxProject    bool
	)
	for _, step := range steps {
		id, _ := step["id"].(string)
		toolName, _ := step["tool_name"].(string)
		toolArgs, _ := step["tool_args"].(map[string]any)

		switch id {
		case "worktree":
			if toolName != "agent_worktree_allocate" {
				t.Fatalf("worktree step tool_name = %q, want agent_worktree_allocate", toolName)
			}
			if got, _ := toolArgs["branch_name"].(string); got != "${input.branch_name}" {
				t.Fatalf("worktree step branch_name = %q, want ${input.branch_name}", got)
			}
			sawWorktreeAllocate = true
		case "sandbox-test":
			if got, _ := toolArgs["project"].(string); got == "${input.project}" {
				sawDevboxProject = true
			}
		case "release-worktree", "cleanup":
			if toolName != "agent_worktree_release" {
				t.Fatalf("release-worktree tool_name = %q, want agent_worktree_release", toolName)
			}
			if got, _ := toolArgs["assignment_id"].(string); got != "${worktree.assignment_id}" {
				t.Fatalf("release-worktree assignment_id = %q, want ${worktree.assignment_id}", got)
			}
			sawWorktreeRelease = true
		}
	}

	if !sawWorktreeAllocate {
		t.Fatal("expected feature-dev workflow to allocate a managed worktree")
	}
	if !sawDevboxProject {
		t.Fatal("expected feature-dev workflow sandbox-test step to use input.project")
	}
	if !sawWorktreeRelease {
		t.Fatal("expected feature-dev workflow to release the managed worktree")
	}

	inputSchema, ok := body["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("expected input_schema map, got %#v", body["input_schema"])
	}

	for _, key := range []string{"branch_name", "project"} {
		raw, ok := inputSchema[key].(map[string]any)
		if !ok {
			t.Fatalf("expected input_schema[%q] map, got %#v", key, inputSchema[key])
		}
		required, _ := raw["required"].(bool)
		if !required {
			t.Fatalf("expected %s to be marked required in feature-dev workflow", key)
		}
	}
}
