package clients

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/crb2nu/loom/pkg/hive/pipeline"
)

// CommandRunner is the seam between GitBranchMerger and `os/exec`. It
// lets tests inject a fake that records invocations and returns canned
// stdout/stderr/exit. Production uses execCommandRunner which shells
// out to the real `git` binary.
type CommandRunner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (stdout, stderr string, exitCode int, err error)
}

// execCommandRunner is the production CommandRunner backed by os/exec.
type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			// Non-zero exit isn't necessarily an err for us — git merge
			// returns 1 on conflict. Surface stdout/stderr to caller and
			// return nil err so the caller can decide.
			err = nil
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// GitBranchMerger satisfies pipeline.BranchMerger by shelling out to
// git. It's the production implementation of the integrator's branch-
// combination step: each parallel slice produced its own branch in its
// own worktree; we fast-forward-or-merge them onto a fresh integration
// branch off main, detecting conflicts via `git status --porcelain`.
//
// Conflict policy: any unresolved hunk in the working tree after the
// final merge is a conflict, regardless of whether earlier branches
// merged cleanly. We don't attempt clever resolution — the integrator
// escalates so a human picks up.
type GitBranchMerger struct {
	RepoRoot string
	Runner   CommandRunner
	// IntegrationPrefix is prepended to the integration branch name.
	// Defaults to "integrate/".
	IntegrationPrefix string
}

// NewGitBranchMerger returns a merger bound to repoRoot. RepoRoot must
// be the absolute path to the operator's loom-core checkout. The
// integration branch is created INSIDE this worktree, so the operator
// pod must have write access to refs/heads.
func NewGitBranchMerger(repoRoot string) *GitBranchMerger {
	return &GitBranchMerger{
		RepoRoot:          repoRoot,
		Runner:            execCommandRunner{},
		IntegrationPrefix: "integrate/",
	}
}

// Merge satisfies pipeline.BranchMerger.
//
// Sequence:
//  1. fetch origin to make sure local refs are current.
//  2. delete any pre-existing integration branch (we always start clean).
//  3. checkout new integration branch from base.
//  4. for each slice branch, run `git merge --no-ff <slice>`.
//     - clean merge → continue to next slice.
//     - conflict → abort merge, collect conflicted files, return Conflict=true.
//  5. on success, return the integration HEAD sha as IntegratedSHA.
func (m *GitBranchMerger) Merge(ctx context.Context, req pipeline.MergeBranchesRequest) (pipeline.MergeBranchesResponse, error) {
	if m == nil || m.Runner == nil {
		return pipeline.MergeBranchesResponse{}, errors.New("git_merger: not configured")
	}
	if m.RepoRoot == "" {
		return pipeline.MergeBranchesResponse{}, errors.New("git_merger: RepoRoot required")
	}
	if req.IntegrationBranch == "" {
		req.IntegrationBranch = m.defaultIntegrationBranch(req.BacklogID)
	}
	if req.BaseBranch == "" {
		req.BaseBranch = "main"
	}
	if len(req.SliceBranches) == 0 {
		return pipeline.MergeBranchesResponse{}, errors.New("git_merger: no SliceBranches to merge")
	}

	logTail := strings.Builder{}
	cmd := func(args ...string) (string, int, error) {
		stdout, stderr, code, err := m.Runner.Run(ctx, m.RepoRoot, "git", args...)
		fmt.Fprintf(&logTail, "$ git %s (exit %d)\n", strings.Join(args, " "), code)
		if stdout != "" {
			fmt.Fprintf(&logTail, "%s\n", strings.TrimRight(stdout, "\n"))
		}
		if stderr != "" {
			fmt.Fprintf(&logTail, "stderr: %s\n", strings.TrimRight(stderr, "\n"))
		}
		if err != nil {
			return "", code, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return stdout, code, nil
	}

	// 1. Fetch.
	if _, _, err := cmd("fetch", "--prune", "origin"); err != nil {
		return pipeline.MergeBranchesResponse{LogTail: logTail.String()}, err
	}

	// 2. Best-effort delete of any pre-existing integration branch.
	// We ignore exit-1 "branch not found" — that's the happy path.
	_, _, _ = cmd("branch", "-D", req.IntegrationBranch)

	// 3. Check out a fresh integration branch off the base.
	if _, code, err := cmd("checkout", "-b", req.IntegrationBranch, "origin/"+req.BaseBranch); err != nil || code != 0 {
		return pipeline.MergeBranchesResponse{
			LogTail: logTail.String(),
		}, fmt.Errorf("create integration branch failed (exit %d)", code)
	}

	// 4. Merge each slice branch in order.
	for _, branch := range req.SliceBranches {
		stdout, code, err := cmd("merge", "--no-ff", "--no-edit", branch)
		if err != nil {
			return pipeline.MergeBranchesResponse{LogTail: logTail.String()}, err
		}
		if code != 0 {
			conflicts := m.detectConflicts(ctx, &logTail)
			// Abort the failed merge so the working tree is clean for
			// caller cleanup. Best-effort.
			_, _, _ = cmd("merge", "--abort")
			return pipeline.MergeBranchesResponse{
				Conflict:        true,
				ConflictedFiles: conflicts,
				LogTail:         logTail.String(),
			}, nil
		}
		_ = stdout
	}

	// 5. Success: read HEAD sha as the integrated sha.
	sha, _, err := cmd("rev-parse", "HEAD")
	if err != nil {
		return pipeline.MergeBranchesResponse{LogTail: logTail.String()}, err
	}
	return pipeline.MergeBranchesResponse{
		IntegratedSHA: strings.TrimSpace(sha),
		LogTail:       logTail.String(),
	}, nil
}

// detectConflicts parses `git status --porcelain` for files in unmerged
// state. Each conflicted file shows up with status code "UU", "AA",
// "DD", or "U?"/"?U" depending on conflict type. We treat any of those
// as a conflict.
func (m *GitBranchMerger) detectConflicts(ctx context.Context, logTail *strings.Builder) []string {
	stdout, stderr, _, err := m.Runner.Run(ctx, m.RepoRoot, "git", "status", "--porcelain")
	if logTail != nil {
		fmt.Fprintf(logTail, "$ git status --porcelain\n%s%s\n",
			strings.TrimRight(stdout, "\n"),
			tagStderr(stderr))
	}
	if err != nil || stdout == "" {
		return nil
	}
	var conflicts []string
	for _, line := range strings.Split(stdout, "\n") {
		if len(line) < 3 {
			continue
		}
		code := line[:2]
		if isConflictCode(code) {
			conflicts = append(conflicts, strings.TrimSpace(line[3:]))
		}
	}
	return conflicts
}

func isConflictCode(c string) bool {
	switch c {
	case "UU", "AA", "DD", "AU", "UA", "UD", "DU":
		return true
	}
	return false
}

func tagStderr(s string) string {
	if s == "" {
		return ""
	}
	return "\nstderr: " + strings.TrimRight(s, "\n")
}

func (m *GitBranchMerger) defaultIntegrationBranch(backlogID string) string {
	prefix := m.IntegrationPrefix
	if prefix == "" {
		prefix = "integrate/"
	}
	return prefix + backlogID
}

// Compile-time interface assertion.
var _ pipeline.BranchMerger = (*GitBranchMerger)(nil)
