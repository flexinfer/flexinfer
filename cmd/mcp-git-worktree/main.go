package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/crb2nu/loom/pkg/mcp"
)

var (
	version  = "0.1.0"
	repoPath string
)

func main() {
	var err error
	repoPath, err = filepath.Abs(getEnv("REPO_PATH", "."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving REPO_PATH: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	server := mcp.NewServer("mcp-git-worktree", version)
	server.SetInstructions("Git worktree management")

	// Tools
	server.AddTool(mcp.Tool{
		Name:        "git_worktree_list",
		Description: "List git worktrees with branch, lock, and prune flags",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleList)

	server.AddTool(mcp.Tool{
		Name:        "git_worktree_add",
		Description: "Add a new git worktree",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path":          map[string]any{"type": "string", "description": "Relative path for worktree"},
				"branch":        map[string]any{"type": "string", "description": "Branch to check out"},
				"create_branch": map[string]any{"type": "boolean", "description": "Create branch from start_point"},
				"start_point":   map[string]any{"type": "string", "description": "Commit/branch/tag to base new branch on"},
				"detach":        map[string]any{"type": "boolean", "description": "Create a detached worktree"},
			},
			Required: []string{"path"},
		},
	}, handleAdd)

	server.AddTool(mcp.Tool{
		Name:        "git_worktree_remove",
		Description: "Remove a git worktree",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path":  map[string]any{"type": "string", "description": "Relative path to remove"},
				"force": map[string]any{"type": "boolean", "description": "Force removal"},
			},
			Required: []string{"path"},
		},
	}, handleRemove)

	server.AddTool(mcp.Tool{
		Name:        "git_worktree_prune",
		Description: "Prune stale git worktree metadata",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"dry_run": map[string]any{"type": "boolean", "description": "Show what would be pruned"},
			},
		},
	}, handlePrune)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoPath}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %v, output: %s", args[0], err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// Handlers

func handleList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	out, err := runGit("worktree", "list", "--porcelain")
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
	path, _ := args["path"].(string)
	branch, _ := args["branch"].(string)
	createBranch, _ := args["create_branch"].(bool)
	startPoint, _ := args["start_point"].(string)
	detach, _ := args["detach"].(bool)

	if path == "" {
		return mcp.ErrorResult(fmt.Errorf("path is required")), nil
	}

	// Validate path is inside repo
	absPath := filepath.Join(repoPath, path)
	if !strings.HasPrefix(absPath, repoPath) {
		return mcp.ErrorResult(fmt.Errorf("path must be inside repository")), nil
	}
	if absPath == repoPath {
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

	_, err := runGit(gitArgs...)
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
	path, _ := args["path"].(string)
	force, _ := args["force"].(bool)

	if path == "" {
		return mcp.ErrorResult(fmt.Errorf("path is required")), nil
	}

	absPath := filepath.Join(repoPath, path)
	if !strings.HasPrefix(absPath, repoPath) {
		return mcp.ErrorResult(fmt.Errorf("path must be inside repository")), nil
	}
	if absPath == repoPath {
		return mcp.ErrorResult(fmt.Errorf("cannot remove main repository worktree")), nil
	}

	gitArgs := []string{"worktree", "remove"}
	if force {
		gitArgs = append(gitArgs, "--force")
	}
	gitArgs = append(gitArgs, absPath)

	_, err := runGit(gitArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"ok": true, "path": absPath})
}

func handlePrune(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	dryRun, _ := args["dry_run"].(bool)
	gitArgs := []string{"worktree", "prune"}
	if dryRun {
		gitArgs = append(gitArgs, "--dry-run")
	}

	out, err := runGit(gitArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"ok": true, "output": out})
}
