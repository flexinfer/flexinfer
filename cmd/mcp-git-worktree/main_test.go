package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(sanitizedGitEnv(os.Environ()),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v, output: %s", strings.Join(args, " "), err, string(out))
		}
	}

	run("init", "-b", "main")
	hooksDir := filepath.Join(dir, ".githooks-test")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	run("config", "core.hooksPath", hooksDir)
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")
	return dir
}

func setDefaultRepo(t *testing.T, p string) {
	t.Helper()
	prev := defaultRepo
	defaultRepo = p
	t.Cleanup(func() { defaultRepo = prev })
}

func TestHandleAddListAndRemove(t *testing.T) {
	dir := initTestRepo(t)
	setDefaultRepo(t, dir)

	addResult, err := handleAdd(context.Background(), map[string]any{
		"path":          "wt-feature",
		"create_branch": true,
		"branch":        "feature/test",
	})
	if err != nil {
		t.Fatalf("unexpected add error: %v", err)
	}
	if addResult.IsError {
		t.Fatalf("expected add success, got: %s", addResult.Content[0].Text)
	}

	wtPath := filepath.Join(dir, "wt-feature")
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("expected worktree dir to exist: %v", err)
	}

	listResult, err := handleList(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected list error: %v", err)
	}
	if listResult.IsError {
		t.Fatalf("expected list success, got: %s", listResult.Content[0].Text)
	}
	listText := listResult.Content[0].Text
	if !strings.Contains(listText, wtPath) {
		t.Fatalf("expected worktree path %q in list output: %s", wtPath, listText)
	}
	if !strings.Contains(listText, "feature/test") {
		t.Fatalf("expected branch feature/test in list output: %s", listText)
	}

	removeResult, err := handleRemove(context.Background(), map[string]any{
		"path": "wt-feature",
	})
	if err != nil {
		t.Fatalf("unexpected remove error: %v", err)
	}
	if removeResult.IsError {
		t.Fatalf("expected remove success, got: %s", removeResult.Content[0].Text)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("expected worktree dir to be removed, got err: %v", err)
	}
}

func TestHandleAddRejectsTraversal(t *testing.T) {
	dir := initTestRepo(t)
	setDefaultRepo(t, dir)

	addResult, err := handleAdd(context.Background(), map[string]any{
		"path":          "../escape",
		"create_branch": true,
		"branch":        "escape",
	})
	if err != nil {
		t.Fatalf("unexpected add error: %v", err)
	}
	if !addResult.IsError {
		t.Fatalf("expected traversal path to be rejected")
	}
	if !strings.Contains(addResult.Content[0].Text, "path must be inside repository") {
		t.Fatalf("expected path validation error, got: %s", addResult.Content[0].Text)
	}

	removeResult, err := handleRemove(context.Background(), map[string]any{
		"path": "../escape",
	})
	if err != nil {
		t.Fatalf("unexpected remove error: %v", err)
	}
	if !removeResult.IsError {
		t.Fatalf("expected traversal path to be rejected for remove")
	}
}

func TestHandleAddRejectsRootPath(t *testing.T) {
	dir := initTestRepo(t)
	setDefaultRepo(t, dir)

	result, err := handleAdd(context.Background(), map[string]any{
		"path":          ".",
		"create_branch": true,
		"branch":        "feature/root",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected root path rejection")
	}
	if !strings.Contains(result.Content[0].Text, "cannot add worktree at repository root") {
		t.Fatalf("unexpected error text: %s", result.Content[0].Text)
	}
}

func TestHandleListIgnoresInheritedGitRoutingEnv(t *testing.T) {
	dir := initTestRepo(t)
	setDefaultRepo(t, dir)

	t.Setenv("GIT_DIR", "/tmp/not-a-repo")
	t.Setenv("GIT_WORK_TREE", "/tmp/not-a-worktree")
	t.Setenv("GIT_COMMON_DIR", "/tmp/not-a-common-dir")

	result, err := handleList(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
	}
}

func TestHandleListSupportsRepoPathOverride(t *testing.T) {
	dir := initTestRepo(t)
	setDefaultRepo(t, filepath.Dir(dir)) // default is intentionally not a git repo

	result, err := handleList(context.Background(), map[string]any{
		"repo_path": dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success with repo_path override, got: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, dir) {
		t.Fatalf("expected repo path %q in output: %s", dir, result.Content[0].Text)
	}
}

func TestResolveDefaultRepoFallsBackToGitRepoPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REPO_PATH", "")
	t.Setenv("GIT_REPO_PATH", dir)

	got, err := resolveDefaultRepo()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolve want abs path: %v", err)
	}
	if got != want {
		t.Fatalf("resolveDefaultRepo() = %q, want %q", got, want)
	}
}
