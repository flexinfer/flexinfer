package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/pathsec"
	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version     = "0.1.0"
	defaultRepo string
)

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	var err error
	defaultRepo, err = resolveDefaultRepo()
	if err != nil {
		return err
	}

	logger := mcplog.NewDefault()
	tp,
		shutdownTracer,

		err :=
		mcpotel.InitTracer(ctx, "mcp-git-worktree",

			logger)
	if err != nil {
		logger.
			Warn("OTel tracer init failed", "error",
				err)
	}
	defer func() {
		_ = shutdownTracer(ctx)
	}()
	tracer := mcpotel.Tracer(tp, "mcp-git-worktree")

	logger.Info("starting server", "name", "mcp-git-worktree", "version", version, "repo", defaultRepo)

	server := mcp.NewServer("mcp-git-worktree", version)
	server.SetInstructions("Git worktree management")

	// Tools
	server.AddTool(mcp.Tool{
		Name:        "git_worktree_list",
		Description: "List git worktrees with branch, lock, and prune flags",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_path": map[string]any{
					"type":        "string",
					"description": "Path to the git repository. Defaults to server default.",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "git_worktree_list", handleList))

	server.AddTool(mcp.Tool{
		Name:        "git_worktree_add",
		Description: "Add a new git worktree",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_path":     map[string]any{"type": "string", "description": "Path to the git repository. Defaults to server default."},
				"path":          map[string]any{"type": "string", "description": "Worktree path relative to the repo root. Sibling paths like ../repo-feature are allowed."},
				"branch":        map[string]any{"type": "string", "description": "Branch to check out"},
				"create_branch": map[string]any{"type": "boolean", "description": "Create branch from start_point"},
				"start_point":   map[string]any{"type": "string", "description": "Commit/branch/tag to base new branch on"},
				"detach":        map[string]any{"type": "boolean", "description": "Create a detached worktree"},
			},
			Required: []string{"path"},
		},
	}, mcpotel.TracedToolHandler(tracer, "git_worktree_add", handleAdd))

	server.AddTool(mcp.Tool{
		Name:        "git_worktree_remove",
		Description: "Remove a git worktree",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_path": map[string]any{"type": "string", "description": "Path to the git repository. Defaults to server default."},
				"path":      map[string]any{"type": "string", "description": "Worktree path relative to the repo root. Sibling paths like ../repo-feature are allowed."},
				"force":     map[string]any{"type": "boolean", "description": "Force removal"},
			},
			Required: []string{"path"},
		},
	}, mcpotel.TracedToolHandler(tracer, "git_worktree_remove", handleRemove))

	server.AddTool(mcp.Tool{
		Name:        "git_worktree_prune",
		Description: "Prune stale git worktree metadata",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo_path": map[string]any{"type": "string", "description": "Path to the git repository. Defaults to server default."},
				"dry_run":   map[string]any{"type": "boolean", "description": "Show what would be pruned"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "git_worktree_prune", handlePrune))

	return server.Run(ctx)
}

func resolveDefaultRepo() (string, error) {
	repoRoot := env.StringWithFallbacks("REPO_PATH", "GIT_REPO_PATH")
	if repoRoot == "" {
		repoRoot = "."
	}
	absPath, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolving default repo path: %w", err)
	}
	return absPath, nil
}

func resolveRepoPath(repoPath string) (string, error) {
	repoRoot := defaultRepo
	if repoPath != "" {
		if filepath.IsAbs(repoPath) {
			repoRoot = repoPath
		} else {
			repoRoot = filepath.Join(defaultRepo, repoPath)
		}
	}
	absPath, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolving repo path: %w", err)
	}
	// Keep repository selection bounded under the default repo root.
	if err := pathsec.ValidatePath(absPath, defaultRepo); err != nil {
		return "", fmt.Errorf("repo_path not allowed: %w", err)
	}
	return absPath, nil
}

func runGit(ctx context.Context, repoPath string, args ...string) (string, error) {
	if repoPath == "" {
		repoPath = defaultRepo
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repoPath}, args...)...)
	cmd.Env = sanitizedGitEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %v, output: %s", args[0], err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func resolveWorktreePath(repoPath, worktreePath string) (string, error) {
	targetPath := worktreePath
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(repoPath, worktreePath)
	}

	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		return "", fmt.Errorf("invalid worktree path: %w", err)
	}

	allowedRoot := filepath.Dir(repoPath)
	if err := pathsec.ValidatePath(absPath, allowedRoot); err != nil {
		return "", fmt.Errorf("path must stay within repository parent %q: %w", allowedRoot, err)
	}

	return absPath, nil
}

func sanitizedGitEnv(envVars []string) []string {
	blocked := map[string]struct{}{
		"GIT_DIR":                          {},
		"GIT_WORK_TREE":                    {},
		"GIT_COMMON_DIR":                   {},
		"GIT_INDEX_FILE":                   {},
		"GIT_OBJECT_DIRECTORY":             {},
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": {},
	}

	sanitized := make([]string, 0, len(envVars))
	for _, kv := range envVars {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if _, found := blocked[key]; found {
			continue
		}
		sanitized = append(sanitized, kv)
	}
	return sanitized
}

// Handlers

func handleList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	repoPath := v.String("repo_path", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resolvedRepoPath, err := resolveRepoPath(repoPath)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	out, err := runGit(ctx, resolvedRepoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var entries []map[string]any
	var current map[string]any

	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "worktree ") {
			if current != nil {
				entries = append(entries, current)
			}
			current = make(map[string]any)
			current["path"] = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "HEAD ") {
			current["head"] = strings.TrimPrefix(line, "HEAD ")
		} else if strings.HasPrefix(line, "branch ") {
			current["branch"] = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		} else if strings.HasPrefix(line, "locked") {
			current["locked"] = true
			if len(line) > 7 {
				current["lock_reason"] = strings.TrimSpace(strings.TrimPrefix(line, "locked"))
			}
		} else if strings.HasPrefix(line, "prunable") {
			current["prunable"] = true
		}
	}
	if current != nil {
		entries = append(entries, current)
	}

	return mcp.JSONResult(map[string]any{"ok": true, "worktrees": entries})
}

func handleAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	repoPath := v.String("repo_path", "")
	path := v.Required("path")
	branch := v.String("branch", "")
	createBranch := v.Bool("create_branch", false)
	startPoint := v.String("start_point", "")
	detach := v.Bool("detach", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resolvedRepoPath, err := resolveRepoPath(repoPath)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	absPath, err := resolveWorktreePath(resolvedRepoPath, path)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	if absPath == resolvedRepoPath {
		return mcp.ErrorResult(fmt.Errorf("cannot add worktree at repository root")), nil
	}

	gitArgs := []string{"worktree", "add"}
	if detach {
		gitArgs = append(gitArgs, "--detach", absPath)
		if startPoint != "" {
			gitArgs = append(gitArgs, startPoint)
		} else {
			gitArgs = append(gitArgs, "HEAD")
		}
	} else if createBranch {
		if branch == "" {
			return mcp.ErrorResult(fmt.Errorf("branch is required when create_branch=true")), nil
		}
		gitArgs = append(gitArgs, "-b", branch, absPath)
		if startPoint != "" {
			gitArgs = append(gitArgs, startPoint)
		} else {
			gitArgs = append(gitArgs, "HEAD")
		}
	} else {
		if branch == "" {
			return mcp.ErrorResult(fmt.Errorf("branch is required unless detach=true")), nil
		}
		gitArgs = append(gitArgs, absPath, branch)
	}

	_, err = runGit(ctx, resolvedRepoPath, gitArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"path":   absPath,
		"branch": branch,
		"detach": detach,
	})
}

func handleRemove(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	repoPath := v.String("repo_path", "")
	path := v.Required("path")
	force := v.Bool("force", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resolvedRepoPath, err := resolveRepoPath(repoPath)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	absPath, err := resolveWorktreePath(resolvedRepoPath, path)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	if absPath == resolvedRepoPath {
		return mcp.ErrorResult(fmt.Errorf("cannot remove main repository worktree")), nil
	}

	gitArgs := []string{"worktree", "remove"}
	if force {
		gitArgs = append(gitArgs, "--force")
	}
	gitArgs = append(gitArgs, absPath)

	_, err = runGit(ctx, resolvedRepoPath, gitArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"ok": true, "path": absPath})
}

func handlePrune(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	repoPath := v.String("repo_path", "")
	dryRun := v.Bool("dry_run", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resolvedRepoPath, err := resolveRepoPath(repoPath)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	gitArgs := []string{"worktree", "prune"}
	if dryRun {
		gitArgs = append(gitArgs, "--dry-run")
	}

	out, err := runGit(ctx, resolvedRepoPath, gitArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"ok": true, "output": out})
}
