package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/registry"
)

func TestSyncAgentsSafetyPolicy_AppendsManagedBlock(t *testing.T) {
	dir := t.TempDir()
	agentsPath := filepath.Join(dir, "AGENTS.md")
	original := "# AGENTS.md\n\nRepo instructions.\n"
	if err := os.WriteFile(agentsPath, []byte(original), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"agents": {
				Settings: map[string]any{
					"dirty_worktree_mode": "continue_scoped_commits",
				},
			},
		},
	}

	if err := syncAgentsSafetyPolicy(dir, reg); err != nil {
		t.Fatalf("syncAgentsSafetyPolicy failed: %v", err)
	}

	updatedBytes, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	updated := string(updatedBytes)
	if !strings.Contains(updated, agentsSafetyBegin) || !strings.Contains(updated, agentsSafetyEnd) {
		t.Fatalf("expected managed block markers in AGENTS.md, got:\n%s", updated)
	}
	if !strings.Contains(updated, "Pre-existing uncommitted/untracked files are baseline context") {
		t.Fatalf("expected safety guidance in AGENTS.md, got:\n%s", updated)
	}
}

func TestSyncAgentsSafetyPolicy_ReplacesExistingManagedBlock(t *testing.T) {
	dir := t.TempDir()
	agentsPath := filepath.Join(dir, "AGENTS.md")
	initial := "# AGENTS.md\n\n" + agentsSafetyBegin + "\nold\n" + agentsSafetyEnd + "\n"
	if err := os.WriteFile(agentsPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	reg := &registry.Registry{
		PlatformPermissions: map[string]*registry.PlatformPermission{
			"agents": {
				Settings: map[string]any{
					"dirty_worktree_mode":          "continue_scoped_commits",
					"dirty_worktree_nudge_message": "Use scoped commits and continue on current branch.",
				},
			},
		},
	}

	if err := syncAgentsSafetyPolicy(dir, reg); err != nil {
		t.Fatalf("syncAgentsSafetyPolicy failed: %v", err)
	}

	updatedBytes, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	updated := string(updatedBytes)
	if strings.Contains(updated, "\nold\n") {
		t.Fatalf("expected managed block replacement, got:\n%s", updated)
	}
	if !strings.Contains(updated, "Use scoped commits and continue on current branch.") {
		t.Fatalf("expected updated message in managed block, got:\n%s", updated)
	}
}
