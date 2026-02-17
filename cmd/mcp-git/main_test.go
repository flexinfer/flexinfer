package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates a temp git repo and sets defaultRepo.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prevDefaultRepo := defaultRepo
	defaultRepo = dir
	t.Cleanup(func() { defaultRepo = prevDefaultRepo })

	// Initialize git repo with a commit.
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
			t.Fatalf("git %s: %s\n%s", strings.Join(args, " "), err, out)
		}
	}

	run("init", "-b", "main")
	// Force no local hooks in test repos to avoid global hook side effects.
	hooksDir := filepath.Join(dir, ".githooks-test")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	run("config", "core.hooksPath", hooksDir)
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")

	return dir
}

func withPath(path string, args map[string]any) map[string]any {
	out := make(map[string]any, len(args)+1)
	for k, v := range args {
		out[k] = v
	}
	out["path"] = path
	return out
}

func TestHandleGitStatus(t *testing.T) {
	t.Run("happy path clean repo", func(t *testing.T) {
		dir := initTestRepo(t)
		result, err := handleGitStatus(context.Background(), withPath(dir, map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "branch") {
			t.Errorf("expected branch field, got: %s", text)
		}
		if !strings.Contains(text, "clean: true") && !strings.Contains(text, `"clean":true`) && !strings.Contains(text, `"clean": true`) {
			t.Errorf("expected clean=true, got: %s", text)
		}
	})

	t.Run("dirty repo", func(t *testing.T) {
		dir := initTestRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new file"), 0644); err != nil {
			t.Fatalf("write new.txt: %v", err)
		}

		result, err := handleGitStatus(context.Background(), withPath(dir, map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "new.txt") {
			t.Errorf("expected new.txt in untracked, got: %s", text)
		}
	})

	t.Run("ignores inherited git env routing vars", func(t *testing.T) {
		dir := initTestRepo(t)
		t.Setenv("GIT_DIR", "/tmp/not-a-repo")
		t.Setenv("GIT_WORK_TREE", "/tmp/not-a-worktree")
		t.Setenv("GIT_COMMON_DIR", "/tmp/not-a-common-dir")

		result, err := handleGitStatus(context.Background(), withPath(dir, map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "clean: true") && !strings.Contains(text, `"clean":true`) && !strings.Contains(text, `"clean": true`) {
			t.Errorf("expected clean=true, got: %s", text)
		}
	})

	t.Run("with explicit path", func(t *testing.T) {
		dir := initTestRepo(t)
		result, err := handleGitStatus(context.Background(), map[string]any{
			"path": dir,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
	})
}

func TestHandleGitLog(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		dir := initTestRepo(t)
		result, err := handleGitLog(context.Background(), withPath(dir, map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "initial commit") {
			t.Errorf("expected commit message, got: %s", text)
		}
	})

	t.Run("oneline format", func(t *testing.T) {
		dir := initTestRepo(t)
		result, err := handleGitLog(context.Background(), withPath(dir, map[string]any{
			"oneline": true,
			"count":   float64(5), // JSON numbers come as float64
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
	})
}

func TestHandleGitDiff(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		dir := initTestRepo(t)
		result, err := handleGitDiff(context.Background(), withPath(dir, map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "No changes") {
			t.Errorf("expected 'No changes', got: %s", text)
		}
	})

	t.Run("with changes", func(t *testing.T) {
		dir := initTestRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Updated\n"), 0644); err != nil {
			t.Fatalf("write README: %v", err)
		}

		result, err := handleGitDiff(context.Background(), withPath(dir, map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "Updated") {
			t.Errorf("expected diff content, got: %s", text)
		}
	})
}

func TestHandleGitBranch(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		dir := initTestRepo(t)
		result, err := handleGitBranch(context.Background(), withPath(dir, map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "main") {
			t.Errorf("expected main branch, got: %s", text)
		}
	})
}

func TestHandleGitShow(t *testing.T) {
	t.Run("happy path HEAD", func(t *testing.T) {
		dir := initTestRepo(t)
		result, err := handleGitShow(context.Background(), withPath(dir, map[string]any{
			"ref": "HEAD",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "initial commit") {
			t.Errorf("expected commit in show output, got: %s", text)
		}
	})

	t.Run("defaults to HEAD when no commit specified", func(t *testing.T) {
		dir := initTestRepo(t)
		result, err := handleGitShow(context.Background(), withPath(dir, map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success (defaults to HEAD), got error: %v", result.Content)
		}
	})

	t.Run("stat mode", func(t *testing.T) {
		dir := initTestRepo(t)
		result, err := handleGitShow(context.Background(), withPath(dir, map[string]any{
			"commit": "HEAD",
			"stat":   true,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
	})
}

func TestHandleGitAdd(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		dir := initTestRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("content"), 0644); err != nil {
			t.Fatalf("write new.txt: %v", err)
		}

		result, err := handleGitAdd(context.Background(), withPath(dir, map[string]any{
			"files": []any{"new.txt"},
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
	})

	t.Run("missing files", func(t *testing.T) {
		dir := initTestRepo(t)
		result, err := handleGitAdd(context.Background(), withPath(dir, map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing files")
		}
	})
}

func TestHandleGitCommit(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		dir := initTestRepo(t)
		if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("content"), 0644); err != nil {
			t.Fatalf("write new.txt: %v", err)
		}
		if _, err := handleGitAdd(context.Background(), withPath(dir, map[string]any{"files": []any{"new.txt"}})); err != nil {
			t.Fatalf("add failed: %v", err)
		}

		result, err := handleGitCommit(context.Background(), withPath(dir, map[string]any{
			"message": "add new file",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
	})

	t.Run("missing message", func(t *testing.T) {
		dir := initTestRepo(t)
		result, err := handleGitCommit(context.Background(), withPath(dir, map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing message")
		}
	})
}
