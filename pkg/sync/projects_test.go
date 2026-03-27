package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func setupProjectTree(t *testing.T, root string, paths []string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscoverProjects(t *testing.T) {
	root := t.TempDir()

	// Create several project structures with .claude/settings.json
	setupProjectTree(t, root, []string{
		"project-a/.claude/settings.json",
		"project-b/.claude/settings.json",
		"services/project-c/.claude/settings.json",
		"deep/nested/project-d/.claude/settings.json",
	})

	projects, err := DiscoverProjects(root, ".claude", false)
	if err != nil {
		t.Fatal(err)
	}

	if len(projects) != 4 {
		t.Fatalf("expected 4 projects, got %d: %v", len(projects), projects)
	}

	// Check that all expected project roots are found
	expected := map[string]bool{
		filepath.Join(root, "project-a"):             false,
		filepath.Join(root, "project-b"):             false,
		filepath.Join(root, "services/project-c"):    false,
		filepath.Join(root, "deep/nested/project-d"): false,
	}
	for _, p := range projects {
		if _, ok := expected[p]; ok {
			expected[p] = true
		} else {
			t.Errorf("unexpected project: %s", p)
		}
	}
	for p, found := range expected {
		if !found {
			t.Errorf("expected project not found: %s", p)
		}
	}
}

func TestDiscoverProjectsSkipsNoiseDirs(t *testing.T) {
	root := t.TempDir()

	setupProjectTree(t, root, []string{
		"project-a/.claude/settings.json",
		"node_modules/pkg/.claude/settings.json",
		"vendor/dep/.claude/settings.json",
		".git/hooks/.claude/settings.json",
	})

	projects, err := DiscoverProjects(root, ".claude", false)
	if err != nil {
		t.Fatal(err)
	}

	if len(projects) != 1 {
		t.Fatalf("expected 1 project (noise should be skipped), got %d: %v", len(projects), projects)
	}
}

func TestDiscoverProjectsSkipsWorktrees(t *testing.T) {
	root := t.TempDir()

	setupProjectTree(t, root, []string{
		"project-a/.claude/settings.json",
		".worktrees/wt1/.claude/settings.json",
	})

	// Without skip
	projects, err := DiscoverProjects(root, ".claude", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects (worktrees included), got %d", len(projects))
	}

	// With skip
	projects, err = DiscoverProjects(root, ".claude", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project (worktrees skipped), got %d", len(projects))
	}
}

func TestDiscoverProjectsMaxDepth(t *testing.T) {
	root := t.TempDir()

	// Create a project deeper than maxDiscoverDepth (6)
	setupProjectTree(t, root, []string{
		"a/b/c/d/e/f/g/.claude/settings.json", // depth 7 from root
		"a/b/.claude/settings.json",           // depth 2
	})

	projects, err := DiscoverProjects(root, ".claude", false)
	if err != nil {
		t.Fatal(err)
	}

	if len(projects) != 1 {
		t.Fatalf("expected 1 project (deep one skipped), got %d: %v", len(projects), projects)
	}
}

func TestDiscoverProjectsEmptyWorkspace(t *testing.T) {
	root := t.TempDir()

	projects, err := DiscoverProjects(root, ".claude", false)
	if err != nil {
		t.Fatal(err)
	}

	if len(projects) != 0 {
		t.Fatalf("expected 0 projects in empty workspace, got %d", len(projects))
	}
}

func TestDiscoverProjectsWithFile(t *testing.T) {
	root := t.TempDir()

	setupProjectTree(t, root, []string{
		"project-a/.codex/config.toml",
		"project-b/.codex/config.toml",
		"deep/nested/project-c/.codex/config.toml",
	})

	projects, err := DiscoverProjectsWithFile(root, ".codex", "config.toml", false)
	if err != nil {
		t.Fatal(err)
	}

	if len(projects) != 3 {
		t.Fatalf("expected 3 projects, got %d: %v", len(projects), projects)
	}
}
