// mcp-git is a fast Git MCP server written in Go.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var (
	version     = "1.0.0"
	defaultRepo string
)

func main() {
	var err error
	defaultRepo, err = filepath.Abs(getEnv("GIT_REPO_PATH", "."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving GIT_REPO_PATH: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	server := mcp.NewServer("mcp-git", version)
	server.SetInstructions("Fast Go-native Git MCP server. Supports status, diff, log, branch operations.")

	// git_status
	server.AddTool(mcp.Tool{
		Name:        "git_status",
		Description: "Get the status of the Git repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the Git repository. Defaults to current directory.",
				},
			},
		},
	}, handleGitStatus)

	// git_diff
	server.AddTool(mcp.Tool{
		Name:        "git_diff",
		Description: "Show changes in the working directory or between commits",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the Git repository",
				},
				"staged": map[string]any{
					"type":        "boolean",
					"description": "Show staged changes (--cached)",
				},
				"commit": map[string]any{
					"type":        "string",
					"description": "Show diff for specific commit or range (e.g., HEAD~3..HEAD)",
				},
				"file": map[string]any{
					"type":        "string",
					"description": "Show diff for specific file",
				},
			},
		},
	}, handleGitDiff)

	// git_log
	server.AddTool(mcp.Tool{
		Name:        "git_log",
		Description: "Show commit history",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the Git repository",
				},
				"count": map[string]any{
					"type":        "integer",
					"description": "Number of commits to show. Defaults to 10.",
				},
				"oneline": map[string]any{
					"type":        "boolean",
					"description": "Use oneline format",
				},
				"author": map[string]any{
					"type":        "string",
					"description": "Filter by author",
				},
				"since": map[string]any{
					"type":        "string",
					"description": "Show commits since date (e.g., '1 week ago')",
				},
				"file": map[string]any{
					"type":        "string",
					"description": "Show history for specific file",
				},
			},
		},
	}, handleGitLog)

	// git_branch
	server.AddTool(mcp.Tool{
		Name:        "git_branch",
		Description: "List or manage branches",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the Git repository",
				},
				"all": map[string]any{
					"type":        "boolean",
					"description": "List all branches including remote",
				},
				"create": map[string]any{
					"type":        "string",
					"description": "Create a new branch with this name",
				},
				"delete": map[string]any{
					"type":        "string",
					"description": "Delete branch with this name",
				},
			},
		},
	}, handleGitBranch)

	// git_checkout
	server.AddTool(mcp.Tool{
		Name:        "git_checkout",
		Description: "Switch branches or restore files",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the Git repository",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Branch to checkout",
				},
				"create": map[string]any{
					"type":        "boolean",
					"description": "Create branch if it doesn't exist (-b)",
				},
				"file": map[string]any{
					"type":        "string",
					"description": "Restore specific file",
				},
			},
		},
	}, handleGitCheckout)

	// git_add
	server.AddTool(mcp.Tool{
		Name:        "git_add",
		Description: "Stage files for commit",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the Git repository",
				},
				"files": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Files to stage. Use ['.'] for all files.",
				},
			},
			Required: []string{"files"},
		},
	}, handleGitAdd)

	// git_commit
	server.AddTool(mcp.Tool{
		Name:        "git_commit",
		Description: "Create a commit",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the Git repository",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Commit message",
				},
				"all": map[string]any{
					"type":        "boolean",
					"description": "Stage all modified files (-a)",
				},
			},
			Required: []string{"message"},
		},
	}, handleGitCommit)

	// git_push
	server.AddTool(mcp.Tool{
		Name:        "git_push",
		Description: "Push commits to remote",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the Git repository",
				},
				"remote": map[string]any{
					"type":        "string",
					"description": "Remote name. Defaults to 'origin'.",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Branch to push. Defaults to current branch.",
				},
				"set_upstream": map[string]any{
					"type":        "boolean",
					"description": "Set upstream (-u)",
				},
			},
		},
	}, handleGitPush)

	// git_pull
	server.AddTool(mcp.Tool{
		Name:        "git_pull",
		Description: "Pull changes from remote",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the Git repository",
				},
				"remote": map[string]any{
					"type":        "string",
					"description": "Remote name. Defaults to 'origin'.",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Branch to pull. Defaults to current branch.",
				},
				"rebase": map[string]any{
					"type":        "boolean",
					"description": "Rebase instead of merge",
				},
			},
		},
	}, handleGitPull)

	// git_stash
	server.AddTool(mcp.Tool{
		Name:        "git_stash",
		Description: "Stash changes",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the Git repository",
				},
				"action": map[string]any{
					"type":        "string",
					"description": "Action: push, pop, list, drop. Defaults to 'push'.",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Stash message (for push)",
				},
			},
		},
	}, handleGitStash)

	// git_show
	server.AddTool(mcp.Tool{
		Name:        "git_show",
		Description: "Show commit details",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the Git repository",
				},
				"commit": map[string]any{
					"type":        "string",
					"description": "Commit SHA or reference. Defaults to HEAD.",
				},
				"stat": map[string]any{
					"type":        "boolean",
					"description": "Show diffstat only",
				},
			},
		},
	}, handleGitShow)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runGit(repoPath string, args ...string) (string, error) {
	if repoPath == "" {
		repoPath = defaultRepo
	} else if !filepath.IsAbs(repoPath) {
		repoPath = filepath.Join(defaultRepo, repoPath)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("git %s: %s", strings.Join(args, " "), string(output))
	}
	return string(output), nil
}

func getStringArg(args map[string]any, key, defaultVal string) string {
	if v, ok := args[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}

func getIntArg(args map[string]any, key string, defaultVal int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	return defaultVal
}

func getBoolArg(args map[string]any, key string, defaultVal bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return defaultVal
}

func getStringSliceArg(args map[string]any, key string) []string {
	if v, ok := args[key].([]any); ok {
		var result []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func handleGitStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := getStringArg(args, "path", "")

	output, err := runGit(path, "status", "--porcelain", "-b")
	if err != nil {
		return nil, err
	}

	// Parse status
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var branch string
	var staged, modified, untracked []string

	for _, line := range lines {
		if strings.HasPrefix(line, "##") {
			branch = strings.TrimPrefix(line, "## ")
			continue
		}
		if len(line) < 3 {
			continue
		}
		x, y := line[0], line[1]
		file := strings.TrimSpace(line[3:])

		if x == 'A' || x == 'M' || x == 'D' || x == 'R' {
			staged = append(staged, file)
		}
		if y == 'M' || y == 'D' {
			modified = append(modified, file)
		}
		if x == '?' && y == '?' {
			untracked = append(untracked, file)
		}
	}

	result := map[string]any{
		"branch":    branch,
		"staged":    staged,
		"modified":  modified,
		"untracked": untracked,
		"clean":     len(staged) == 0 && len(modified) == 0 && len(untracked) == 0,
	}

	return mcp.JSONResult(result)
}

func handleGitDiff(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := getStringArg(args, "path", "")
	staged := getBoolArg(args, "staged", false)
	commit := getStringArg(args, "commit", "")
	file := getStringArg(args, "file", "")

	gitArgs := []string{"diff", "--color=never"}

	if staged {
		gitArgs = append(gitArgs, "--cached")
	}
	if commit != "" {
		gitArgs = append(gitArgs, commit)
	}
	if file != "" {
		gitArgs = append(gitArgs, "--", file)
	}

	output, err := runGit(path, gitArgs...)
	if err != nil {
		return nil, err
	}

	if output == "" {
		output = "No changes"
	}

	return mcp.TextResult(output), nil
}

func handleGitLog(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := getStringArg(args, "path", "")
	count := getIntArg(args, "count", 10)
	oneline := getBoolArg(args, "oneline", false)
	author := getStringArg(args, "author", "")
	since := getStringArg(args, "since", "")
	file := getStringArg(args, "file", "")

	gitArgs := []string{"log", fmt.Sprintf("-n%d", count)}

	if oneline {
		gitArgs = append(gitArgs, "--oneline")
	} else {
		gitArgs = append(gitArgs, "--format=%H|%an|%ae|%at|%s")
	}

	if author != "" {
		gitArgs = append(gitArgs, "--author="+author)
	}
	if since != "" {
		gitArgs = append(gitArgs, "--since="+since)
	}
	if file != "" {
		gitArgs = append(gitArgs, "--", file)
	}

	output, err := runGit(path, gitArgs...)
	if err != nil {
		return nil, err
	}

	if oneline {
		return mcp.TextResult(output), nil
	}

	// Parse structured output
	var commits []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "|", 5)
		if len(parts) == 5 {
			commits = append(commits, map[string]any{
				"sha":       parts[0],
				"author":    parts[1],
				"email":     parts[2],
				"timestamp": parts[3],
				"message":   parts[4],
			})
		}
	}

	return mcp.JSONResult(map[string]any{"commits": commits, "count": len(commits)})
}

func handleGitBranch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := getStringArg(args, "path", "")
	all := getBoolArg(args, "all", false)
	create := getStringArg(args, "create", "")
	delete := getStringArg(args, "delete", "")

	if create != "" {
		output, err := runGit(path, "branch", create)
		if err != nil {
			return nil, err
		}
		return mcp.JSONResult(map[string]any{"created": create, "output": output})
	}

	if delete != "" {
		output, err := runGit(path, "branch", "-d", delete)
		if err != nil {
			return nil, err
		}
		return mcp.JSONResult(map[string]any{"deleted": delete, "output": output})
	}

	gitArgs := []string{"branch", "--format=%(HEAD) %(refname:short) %(upstream:short)"}
	if all {
		gitArgs = append(gitArgs, "-a")
	}

	output, err := runGit(path, gitArgs...)
	if err != nil {
		return nil, err
	}

	var branches []map[string]any
	var current string
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			isCurrent := parts[0] == "*"
			name := parts[1]
			if isCurrent {
				name = parts[1]
				current = name
			}
			upstream := ""
			if len(parts) >= 3 {
				upstream = parts[2]
			}
			branches = append(branches, map[string]any{
				"name":     name,
				"current":  isCurrent,
				"upstream": upstream,
			})
		}
	}

	return mcp.JSONResult(map[string]any{"branches": branches, "current": current})
}

func handleGitCheckout(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := getStringArg(args, "path", "")
	branch := getStringArg(args, "branch", "")
	create := getBoolArg(args, "create", false)
	file := getStringArg(args, "file", "")

	if branch == "" && file == "" {
		return nil, fmt.Errorf("branch or file is required")
	}

	gitArgs := []string{"checkout"}
	if create && branch != "" {
		gitArgs = append(gitArgs, "-b")
	}
	if branch != "" {
		gitArgs = append(gitArgs, branch)
	}
	if file != "" {
		gitArgs = append(gitArgs, "--", file)
	}

	output, err := runGit(path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"status": "success", "output": output})
}

func handleGitAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := getStringArg(args, "path", "")
	files := getStringSliceArg(args, "files")

	if len(files) == 0 {
		return nil, fmt.Errorf("files is required")
	}

	gitArgs := append([]string{"add"}, files...)
	output, err := runGit(path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"status": "staged", "files": files, "output": output})
}

func handleGitCommit(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := getStringArg(args, "path", "")
	message := getStringArg(args, "message", "")
	all := getBoolArg(args, "all", false)

	if message == "" {
		return nil, fmt.Errorf("message is required")
	}

	gitArgs := []string{"commit", "-m", message}
	if all {
		gitArgs = append(gitArgs, "-a")
	}

	output, err := runGit(path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"status": "committed", "message": message, "output": output})
}

func handleGitPush(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := getStringArg(args, "path", "")
	remote := getStringArg(args, "remote", "origin")
	branch := getStringArg(args, "branch", "")
	setUpstream := getBoolArg(args, "set_upstream", false)

	gitArgs := []string{"push"}
	if setUpstream {
		gitArgs = append(gitArgs, "-u")
	}
	gitArgs = append(gitArgs, remote)
	if branch != "" {
		gitArgs = append(gitArgs, branch)
	}

	output, err := runGit(path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"status": "pushed", "remote": remote, "output": output})
}

func handleGitPull(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := getStringArg(args, "path", "")
	remote := getStringArg(args, "remote", "origin")
	branch := getStringArg(args, "branch", "")
	rebase := getBoolArg(args, "rebase", false)

	gitArgs := []string{"pull"}
	if rebase {
		gitArgs = append(gitArgs, "--rebase")
	}
	gitArgs = append(gitArgs, remote)
	if branch != "" {
		gitArgs = append(gitArgs, branch)
	}

	output, err := runGit(path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"status": "pulled", "remote": remote, "output": output})
}

func handleGitStash(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := getStringArg(args, "path", "")
	action := getStringArg(args, "action", "push")
	message := getStringArg(args, "message", "")

	gitArgs := []string{"stash", action}
	if action == "push" && message != "" {
		gitArgs = append(gitArgs, "-m", message)
	}

	output, err := runGit(path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"action": action, "output": output})
}

func handleGitShow(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path := getStringArg(args, "path", "")
	commit := getStringArg(args, "commit", "HEAD")
	stat := getBoolArg(args, "stat", false)

	gitArgs := []string{"show", "--color=never"}
	if stat {
		gitArgs = append(gitArgs, "--stat")
	}
	gitArgs = append(gitArgs, commit)

	output, err := runGit(path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.TextResult(output), nil
}
