// mcp-git is a fast Git MCP server written in Go.
package main

import (
	"bufio"
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
	version     = "1.0.0"
	defaultRepo string
)

func main() {
	var err error
	defaultRepo, err = filepath.Abs(env.String("GIT_REPO_PATH", "."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving GIT_REPO_PATH: %v\n", err)
		os.Exit(1)
	}

	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-git", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-git")

	logger.Info("starting server", "name", "mcp-git", "version", version, "repo", defaultRepo)

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
	}, mcpotel.TracedToolHandler(tracer, "git_status", handleGitStatus))

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
	}, mcpotel.TracedToolHandler(tracer, "git_diff", handleGitDiff))

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
	}, mcpotel.TracedToolHandler(tracer, "git_log", handleGitLog))

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
	}, mcpotel.TracedToolHandler(tracer, "git_branch", handleGitBranch))

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
	}, mcpotel.TracedToolHandler(tracer, "git_checkout", handleGitCheckout))

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
	}, mcpotel.TracedToolHandler(tracer, "git_add", handleGitAdd))

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
	}, mcpotel.TracedToolHandler(tracer, "git_commit", handleGitCommit))

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
	}, mcpotel.TracedToolHandler(tracer, "git_push", handleGitPush))

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
	}, mcpotel.TracedToolHandler(tracer, "git_pull", handleGitPull))

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
	}, mcpotel.TracedToolHandler(tracer, "git_stash", handleGitStash))

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
	}, mcpotel.TracedToolHandler(tracer, "git_show", handleGitShow))

	return server.Run(ctx)
}

func runGit(ctx context.Context, repoPath string, args ...string) (string, error) {
	if repoPath == "" {
		repoPath = defaultRepo
	} else if !filepath.IsAbs(repoPath) {
		repoPath = filepath.Join(defaultRepo, repoPath)
	}
	// Prevent path traversal outside defaultRepo
	if err := pathsec.ValidatePath(repoPath, defaultRepo); err != nil {
		return "", fmt.Errorf("path not allowed: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	cmd.Env = sanitizedGitEnv(os.Environ())

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("git %s: %s", strings.Join(args, " "), string(output))
	}
	return string(output), nil
}

func sanitizedGitEnv(envVars []string) []string {
	// Strip inherited repository-routing variables (e.g. from git hooks) so
	// each command targets cmd.Dir/repoPath instead of caller context.
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

func handleGitStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.String("path", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	output, err := runGit(ctx, path, "status", "--porcelain", "-b")
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
	v := validate.NewArgs(args)
	path := v.String("path", "")
	staged := v.Bool("staged", false)
	commit := v.String("commit", "")
	file := v.String("file", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

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

	output, err := runGit(ctx, path, gitArgs...)
	if err != nil {
		return nil, err
	}

	if output == "" {
		output = "No changes"
	}

	return mcp.TextResult(output), nil
}

func handleGitLog(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.String("path", "")
	count := v.Int("count", 10)
	oneline := v.Bool("oneline", false)
	author := v.String("author", "")
	since := v.String("since", "")
	file := v.String("file", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

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

	output, err := runGit(ctx, path, gitArgs...)
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
	v := validate.NewArgs(args)
	path := v.String("path", "")
	all := v.Bool("all", false)
	create := v.String("create", "")
	delete := v.String("delete", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if create != "" {
		output, err := runGit(ctx, path, "branch", create)
		if err != nil {
			return nil, err
		}
		return mcp.JSONResult(map[string]any{"created": create, "output": output})
	}

	if delete != "" {
		output, err := runGit(ctx, path, "branch", "-d", delete)
		if err != nil {
			return nil, err
		}
		return mcp.JSONResult(map[string]any{"deleted": delete, "output": output})
	}

	gitArgs := []string{"branch", "--format=%(HEAD) %(refname:short) %(upstream:short)"}
	if all {
		gitArgs = append(gitArgs, "-a")
	}

	output, err := runGit(ctx, path, gitArgs...)
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
	v := validate.NewArgs(args)
	path := v.String("path", "")
	branch := v.String("branch", "")
	create := v.Bool("create", false)
	file := v.String("file", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if branch == "" && file == "" {
		return mcp.ErrorResult(fmt.Errorf("branch or file is required")), nil
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

	output, err := runGit(ctx, path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"status": "success", "output": output})
}

func handleGitAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.String("path", "")
	files := v.RequiredStringSlice("files")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	gitArgs := append([]string{"add"}, files...)
	output, err := runGit(ctx, path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"status": "staged", "files": files, "output": output})
}

func handleGitCommit(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.String("path", "")
	message := v.Required("message")
	all := v.Bool("all", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	gitArgs := []string{"commit", "-m", message}
	if all {
		gitArgs = append(gitArgs, "-a")
	}

	output, err := runGit(ctx, path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"status": "committed", "message": message, "output": output})
}

func handleGitPush(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.String("path", "")
	remote := v.String("remote", "origin")
	branch := v.String("branch", "")
	setUpstream := v.Bool("set_upstream", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	gitArgs := []string{"push"}
	if setUpstream {
		gitArgs = append(gitArgs, "-u")
	}
	gitArgs = append(gitArgs, remote)
	if branch != "" {
		gitArgs = append(gitArgs, branch)
	}

	output, err := runGit(ctx, path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"status": "pushed", "remote": remote, "output": output})
}

func handleGitPull(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.String("path", "")
	remote := v.String("remote", "origin")
	branch := v.String("branch", "")
	rebase := v.Bool("rebase", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	gitArgs := []string{"pull"}
	if rebase {
		gitArgs = append(gitArgs, "--rebase")
	}
	gitArgs = append(gitArgs, remote)
	if branch != "" {
		gitArgs = append(gitArgs, branch)
	}

	output, err := runGit(ctx, path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"status": "pulled", "remote": remote, "output": output})
}

func handleGitStash(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.String("path", "")
	action := v.String("action", "push")
	message := v.String("message", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	gitArgs := []string{"stash", action}
	if action == "push" && message != "" {
		gitArgs = append(gitArgs, "-m", message)
	}

	output, err := runGit(ctx, path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"action": action, "output": output})
}

func handleGitShow(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.String("path", "")
	commit := v.String("commit", "HEAD")
	stat := v.Bool("stat", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	gitArgs := []string{"show", "--color=never"}
	if stat {
		gitArgs = append(gitArgs, "--stat")
	}
	gitArgs = append(gitArgs, commit)

	output, err := runGit(ctx, path, gitArgs...)
	if err != nil {
		return nil, err
	}

	return mcp.TextResult(output), nil
}
