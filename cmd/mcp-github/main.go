// mcp-github is a fast GitHub MCP server written in Go.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var version = "1.0.0"

type githubServer struct {
	token      string
	httpClient *http.Client
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	token := os.Getenv("GITHUB_PERSONAL_ACCESS_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}

	gh := &githubServer{
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	server := mcp.NewServer("mcp-github", version)
	server.SetInstructions("Fast Go-native GitHub MCP server. Supports repos, issues, PRs, and more.")

	// list_repos
	server.AddTool(mcp.Tool{
		Name:        "list_repos",
		Description: "List repositories for a user or organization",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{
					"type":        "string",
					"description": "Username or organization name. Defaults to authenticated user.",
				},
				"type": map[string]any{
					"type":        "string",
					"description": "Type of repos: all, owner, member. Defaults to 'owner'.",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100). Defaults to 30.",
				},
			},
		},
	}, gh.handleListRepos)

	// get_repo
	server.AddTool(mcp.Tool{
		Name:        "get_repo",
		Description: "Get repository information",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{
					"type":        "string",
					"description": "Repository owner",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name",
				},
			},
			Required: []string{"owner", "repo"},
		},
	}, gh.handleGetRepo)

	// list_issues
	server.AddTool(mcp.Tool{
		Name:        "list_issues",
		Description: "List issues for a repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{
					"type":        "string",
					"description": "Repository owner",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name",
				},
				"state": map[string]any{
					"type":        "string",
					"description": "State: open, closed, all. Defaults to 'open'.",
				},
				"labels": map[string]any{
					"type":        "string",
					"description": "Comma-separated list of label names",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100)",
				},
			},
			Required: []string{"owner", "repo"},
		},
	}, gh.handleListIssues)

	// get_issue
	server.AddTool(mcp.Tool{
		Name:        "get_issue",
		Description: "Get issue details",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{
					"type":        "string",
					"description": "Repository owner",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name",
				},
				"number": map[string]any{
					"type":        "integer",
					"description": "Issue number",
				},
			},
			Required: []string{"owner", "repo", "number"},
		},
	}, gh.handleGetIssue)

	// create_issue
	server.AddTool(mcp.Tool{
		Name:        "create_issue",
		Description: "Create a new issue",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{
					"type":        "string",
					"description": "Repository owner",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Issue title",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Issue body",
				},
				"labels": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Labels to add",
				},
				"assignees": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Usernames to assign",
				},
			},
			Required: []string{"owner", "repo", "title"},
		},
	}, gh.handleCreateIssue)

	// list_prs
	server.AddTool(mcp.Tool{
		Name:        "list_prs",
		Description: "List pull requests for a repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{
					"type":        "string",
					"description": "Repository owner",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name",
				},
				"state": map[string]any{
					"type":        "string",
					"description": "State: open, closed, all. Defaults to 'open'.",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100)",
				},
			},
			Required: []string{"owner", "repo"},
		},
	}, gh.handleListPRs)

	// get_pr
	server.AddTool(mcp.Tool{
		Name:        "get_pr",
		Description: "Get pull request details",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{
					"type":        "string",
					"description": "Repository owner",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name",
				},
				"number": map[string]any{
					"type":        "integer",
					"description": "PR number",
				},
			},
			Required: []string{"owner", "repo", "number"},
		},
	}, gh.handleGetPR)

	// list_commits
	server.AddTool(mcp.Tool{
		Name:        "list_commits",
		Description: "List commits for a repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{
					"type":        "string",
					"description": "Repository owner",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name",
				},
				"sha": map[string]any{
					"type":        "string",
					"description": "SHA or branch name",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100)",
				},
			},
			Required: []string{"owner", "repo"},
		},
	}, gh.handleListCommits)

	// search_repos
	server.AddTool(mcp.Tool{
		Name:        "search_repos",
		Description: "Search repositories",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100)",
				},
			},
			Required: []string{"query"},
		},
	}, gh.handleSearchRepos)

	// search_code
	server.AddTool(mcp.Tool{
		Name:        "search_code",
		Description: "Search code in repositories",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query (can include qualifiers like 'repo:owner/name')",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100)",
				},
			},
			Required: []string{"query"},
		},
	}, gh.handleSearchCode)

	// get_file_contents
	server.AddTool(mcp.Tool{
		Name:        "get_file_contents",
		Description: "Get contents of a file from a repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{
					"type":        "string",
					"description": "Repository owner",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File path",
				},
				"ref": map[string]any{
					"type":        "string",
					"description": "Branch, tag, or commit SHA",
				},
			},
			Required: []string{"owner", "repo", "path"},
		},
	}, gh.handleGetFileContents)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (g *githubServer) request(ctx context.Context, method, path string, body any) (map[string]any, error) {
	url := "https://api.github.com" + path

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = strings.NewReader(string(b))
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GitHub API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Try array
		var arr []any
		if err := json.Unmarshal(respBody, &arr); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
		return map[string]any{"items": arr}, nil
	}

	return result, nil
}

func (g *githubServer) requestList(ctx context.Context, path string) ([]any, error) {
	url := "https://api.github.com" + path

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GitHub API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result []any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return result, nil
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

func (g *githubServer) handleListRepos(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	owner := getStringArg(args, "owner", "")
	repoType := getStringArg(args, "type", "owner")
	perPage := getIntArg(args, "per_page", 30)

	var path string
	if owner != "" {
		path = fmt.Sprintf("/users/%s/repos?type=%s&per_page=%d", owner, repoType, perPage)
	} else {
		path = fmt.Sprintf("/user/repos?type=%s&per_page=%d", repoType, perPage)
	}

	result, err := g.requestList(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"repositories": result, "count": len(result)})
}

func (g *githubServer) handleGetRepo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	owner := getStringArg(args, "owner", "")
	repo := getStringArg(args, "repo", "")

	if owner == "" || repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}

	result, err := g.request(ctx, "GET", fmt.Sprintf("/repos/%s/%s", owner, repo), nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleListIssues(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	owner := getStringArg(args, "owner", "")
	repo := getStringArg(args, "repo", "")
	state := getStringArg(args, "state", "open")
	labels := getStringArg(args, "labels", "")
	perPage := getIntArg(args, "per_page", 30)

	if owner == "" || repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}

	path := fmt.Sprintf("/repos/%s/%s/issues?state=%s&per_page=%d", owner, repo, state, perPage)
	if labels != "" {
		path += "&labels=" + labels
	}

	result, err := g.requestList(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"issues": result, "count": len(result)})
}

func (g *githubServer) handleGetIssue(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	owner := getStringArg(args, "owner", "")
	repo := getStringArg(args, "repo", "")
	number := getIntArg(args, "number", 0)

	if owner == "" || repo == "" || number == 0 {
		return nil, fmt.Errorf("owner, repo, and number are required")
	}

	result, err := g.request(ctx, "GET", fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number), nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleCreateIssue(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	owner := getStringArg(args, "owner", "")
	repo := getStringArg(args, "repo", "")
	title := getStringArg(args, "title", "")
	body := getStringArg(args, "body", "")

	if owner == "" || repo == "" || title == "" {
		return nil, fmt.Errorf("owner, repo, and title are required")
	}

	payload := map[string]any{"title": title}
	if body != "" {
		payload["body"] = body
	}
	if labels, ok := args["labels"].([]any); ok {
		payload["labels"] = labels
	}
	if assignees, ok := args["assignees"].([]any); ok {
		payload["assignees"] = assignees
	}

	result, err := g.request(ctx, "POST", fmt.Sprintf("/repos/%s/%s/issues", owner, repo), payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleListPRs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	owner := getStringArg(args, "owner", "")
	repo := getStringArg(args, "repo", "")
	state := getStringArg(args, "state", "open")
	perPage := getIntArg(args, "per_page", 30)

	if owner == "" || repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls?state=%s&per_page=%d", owner, repo, state, perPage)

	result, err := g.requestList(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"pull_requests": result, "count": len(result)})
}

func (g *githubServer) handleGetPR(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	owner := getStringArg(args, "owner", "")
	repo := getStringArg(args, "repo", "")
	number := getIntArg(args, "number", 0)

	if owner == "" || repo == "" || number == 0 {
		return nil, fmt.Errorf("owner, repo, and number are required")
	}

	result, err := g.request(ctx, "GET", fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number), nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleListCommits(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	owner := getStringArg(args, "owner", "")
	repo := getStringArg(args, "repo", "")
	sha := getStringArg(args, "sha", "")
	perPage := getIntArg(args, "per_page", 30)

	if owner == "" || repo == "" {
		return nil, fmt.Errorf("owner and repo are required")
	}

	path := fmt.Sprintf("/repos/%s/%s/commits?per_page=%d", owner, repo, perPage)
	if sha != "" {
		path += "&sha=" + sha
	}

	result, err := g.requestList(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"commits": result, "count": len(result)})
}

func (g *githubServer) handleSearchRepos(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := getStringArg(args, "query", "")
	perPage := getIntArg(args, "per_page", 30)

	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	path := fmt.Sprintf("/search/repositories?q=%s&per_page=%d", query, perPage)

	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleSearchCode(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := getStringArg(args, "query", "")
	perPage := getIntArg(args, "per_page", 30)

	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	path := fmt.Sprintf("/search/code?q=%s&per_page=%d", query, perPage)

	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleGetFileContents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	owner := getStringArg(args, "owner", "")
	repo := getStringArg(args, "repo", "")
	filePath := getStringArg(args, "path", "")
	ref := getStringArg(args, "ref", "")

	if owner == "" || repo == "" || filePath == "" {
		return nil, fmt.Errorf("owner, repo, and path are required")
	}

	path := fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, filePath)
	if ref != "" {
		path += "?ref=" + ref
	}

	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}
