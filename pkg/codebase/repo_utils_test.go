package codebase

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

func TestDeriveRepoID_NoGitRepoFallsBackToRootHash(t *testing.T) {
	tempDir, err := os.MkdirTemp("/tmp", "repo-utils-no-git-*")
	if err != nil {
		t.Fatalf("os.MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })
	abs, err := filepath.Abs(tempDir)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}

	got, err := deriveRepoID(tempDir)
	if err != nil {
		t.Fatalf("deriveRepoID: %v", err)
	}

	want := schema.ShortSHA256Hex(abs)
	if got != want {
		t.Fatalf("deriveRepoID() = %q, want %q", got, want)
	}
}

func TestDeriveRepoID_UsesRemoteOriginHashWhenAvailable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo, err := os.MkdirTemp("/tmp", "repo-utils-git-*")
	if err != nil {
		t.Fatalf("os.MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(repo) })
	runGit(t, repo, "init")
	remote := "https://example.com/org/repo.git"
	runGit(t, repo, "remote", "add", "origin", remote)

	got, err := deriveRepoID(repo)
	if err != nil {
		t.Fatalf("deriveRepoID: %v", err)
	}

	want := schema.ShortSHA256Hex("https://example.com/org/repo")
	if got != want {
		t.Fatalf("deriveRepoID() = %q, want %q", got, want)
	}
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmdArgs := append([]string{"-C", repo}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
