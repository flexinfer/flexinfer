package agentcontext

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates an empty git repo in a fresh temp dir with one commit
// so `git worktree add` can branch from HEAD. Skips the whole test when git is
// not available on PATH (e.g. sandboxed CI without git).
func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("commit", "--allow-empty", "-m", "init")
	return dir
}

func TestWorktreeAllocate_ExplicitRepoPathOverridesConfig(t *testing.T) {
	svc := newTestService()
	repo := initTestRepo(t)
	// cfg.GitRepoPath deliberately points somewhere invalid to prove the
	// explicit arg wins instead of silently using the config value.
	svc.worktrees.cfg.GitRepoPath = filepath.Join(t.TempDir(), "does-not-exist")

	branch := "feat/explicit-repo-path"
	result, err := svc.worktrees.Allocate(context.Background(), map[string]any{
		"agent_id":      "agent-a",
		"session_id":    "session-a",
		"branch_name":   branch,
		"repo_path":     repo,
		"worktree_path": filepath.Join(t.TempDir(), branch),
	})
	if err != nil {
		t.Fatalf("Allocate returned transport error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got IsError result: %+v", result)
	}

	svc.worktrees.mu.RLock()
	defer svc.worktrees.mu.RUnlock()
	if len(svc.worktrees.assns) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(svc.worktrees.assns))
	}
	for _, a := range svc.worktrees.assns {
		if a.Branch != branch {
			t.Errorf("assignment branch = %q, want %q", a.Branch, branch)
		}
	}
}

func TestWorktreeAllocate_FallsBackToSessionWorkingDir(t *testing.T) {
	svc := newTestService()
	repo := initTestRepo(t)

	// No cfg.GitRepoPath, no repo_path arg — the only signal is the session's
	// working_dir. This is the exact scenario from services/loom-core#83.
	svc.worktrees.cfg.GitRepoPath = ""
	svc.worktrees.getSession = func(ctx context.Context, sessionID string) (*Session, error) {
		return &Session{ID: sessionID, WorkingDir: repo}, nil
	}

	branch := "feat/session-workdir-fallback"
	result, err := svc.worktrees.Allocate(context.Background(), map[string]any{
		"agent_id":      "agent-b",
		"session_id":    "session-b",
		"branch_name":   branch,
		"worktree_path": filepath.Join(t.TempDir(), branch),
	})
	if err != nil {
		t.Fatalf("Allocate returned transport error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success, got IsError result: %+v", result)
	}

	svc.worktrees.mu.RLock()
	defer svc.worktrees.mu.RUnlock()
	if len(svc.worktrees.assns) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(svc.worktrees.assns))
	}
}

func TestWorktreeAllocate_ActionableErrorWhenUnresolvable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
	svc := newTestService()
	svc.worktrees.cfg.GitRepoPath = ""
	// Session exists but has no working_dir — mirrors the deployed
	// agent-context path where the caller never passed one.
	svc.worktrees.getSession = func(ctx context.Context, sessionID string) (*Session, error) {
		return &Session{ID: sessionID}, nil
	}

	result, err := svc.worktrees.Allocate(context.Background(), map[string]any{
		"agent_id":    "agent-c",
		"session_id":  "session-c",
		"branch_name": "feat/unresolvable",
	})
	if err != nil {
		t.Fatalf("Allocate returned transport error: %v", err)
	}
	if result == nil || !result.IsError || len(result.Content) == 0 {
		t.Fatalf("expected an IsError CallToolResult, got %+v", result)
	}
	msg := result.Content[0].Text
	// Operator-friendly: explain what was checked + how to fix.
	for _, want := range []string{"repo_path arg", "session.working_dir", "AGENT_CONTEXT_GIT_REPO_PATH"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q; full message:\n%s", want, msg)
		}
	}
	if !strings.Contains(strings.ToLower(msg), "could not resolve") {
		t.Errorf("expected operator-facing resolution failure message, got:\n%s", msg)
	}
	if len(svc.worktrees.assns) != 0 {
		t.Errorf("no worktree assignment should be recorded on failure, got %d", len(svc.worktrees.assns))
	}
}
