// mcp-github is a fast GitHub MCP server written in Go.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/poll"
	"github.com/crb2nu/loom/pkg/strutil"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.0.0"

type githubServer struct {
	token      string
	httpClient *httpclient.Client
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	token := env.StringWithFallbacks("GITHUB_PERSONAL_ACCESS_TOKEN", "GITHUB_TOKEN")

	gh := &githubServer{
		token:      token,
		httpClient: httpclient.NewDefault(),
	}

	logger.Info("starting server", "name", "mcp-github", "version", version)

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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
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

	return server.Run(ctx)
}

func (g *githubServer) request(ctx context.Context, method, path string, body any) (map[string]any, error) {
	reqURL := "https://api.github.com" + path

	var reqBodyBytes []byte
	if body != nil {
		var err error
		reqBodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	headers := map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}
	if len(reqBodyBytes) > 0 {
		headers["Content-Type"] = "application/json"
	}

	respBody, _, err := g.doRequest(ctx, method, reqURL, reqBodyBytes, headers)
	if err != nil {
		return nil, err
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

func (g *githubServer) requestListWithMeta(ctx context.Context, path string) ([]any, map[string]any, error) {
	reqURL := "https://api.github.com" + path
	respBody, respHeaders, err := g.doRequest(ctx, "GET", reqURL, nil, map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	})
	if err != nil {
		return nil, nil, err
	}

	var result []any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, nil, fmt.Errorf("parse response: %w", err)
	}

	return result, parseGitHubPagination(respHeaders), nil
}

func normalizePerPage(perPage int, defaultVal int) int {
	return validate.NormalizePerPage(perPage, defaultVal, 100)
}

func normalizePage(page int) int {
	return validate.NormalizePage(page)
}

func (g *githubServer) doRequest(ctx context.Context, method, reqURL string, body []byte, headers map[string]string) ([]byte, http.Header, error) {
	const (
		maxAttempts       = 3
		maxErrorBodyBytes = 8192
		maxRetryDelay     = 10 * time.Second
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var reqBody io.Reader
		if len(body) > 0 {
			reqBody = bytes.NewReader(body)
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
		if err != nil {
			return nil, nil, err
		}

		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "mcp-github/"+version)

		for k, v := range headers {
			if k != "" && v != "" {
				req.Header.Set(k, v)
			}
		}
		if g.token != "" {
			req.Header.Set("Authorization", "Bearer "+g.token)
		}

		resp, err := g.httpClient.HTTP().Do(req)
		if err != nil {
			if attempt < maxAttempts-1 && isTransientError(err) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, nil, sleepErr
				}
				continue
			}
			return nil, nil, err
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		respHeaders := resp.Header.Clone()
		if readErr != nil {
			if attempt < maxAttempts-1 && isTransientError(readErr) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, respHeaders, sleepErr
				}
				continue
			}
			return nil, respHeaders, readErr
		}

		if resp.StatusCode == 429 && attempt < maxAttempts-1 {
			delay := parseRetryAfter(resp.Header.Get("Retry-After"))
			if delay <= 0 {
				delay = backoffDelay(attempt, maxRetryDelay)
			}
			if delay > maxRetryDelay {
				delay = maxRetryDelay
			}
			if sleepErr := poll.WaitWithContext(ctx, delay); sleepErr != nil {
				return nil, respHeaders, sleepErr
			}
			continue
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 && attempt < maxAttempts-1 {
			if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
				return nil, respHeaders, sleepErr
			}
			continue
		}

		if resp.StatusCode >= 400 {
			bodyText := trimTo(respBody, maxErrorBodyBytes)
			// Add helpful rate limit context when possible.
			if resp.StatusCode == 403 && strings.Contains(strings.ToLower(bodyText), "rate limit") {
				bodyText = fmt.Sprintf("%s (rate_limit_remaining=%s reset=%s)", bodyText, respHeaders.Get("X-RateLimit-Remaining"), respHeaders.Get("X-RateLimit-Reset"))
			}
			return nil, respHeaders, mcperror.APIError("GitHub", resp.StatusCode, bodyText)
		}

		return respBody, respHeaders, nil
	}

	return nil, nil, fmt.Errorf("request failed after retries")
}

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

func backoffDelay(attempt int, max time.Duration) time.Duration {
	delay := time.Duration(1<<attempt) * time.Second
	if delay > max {
		return max
	}
	return delay
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "temporary failure") ||
		strings.Contains(errStr, "EOF")
}

func trimTo(b []byte, max int) string {
	return strutil.TruncateNoEllipsis(string(b), max)
}

func parseGitHubPagination(headers http.Header) map[string]any {
	if headers == nil {
		return nil
	}
	link := strings.TrimSpace(headers.Get("Link"))
	if link == "" {
		return nil
	}

	out := map[string]any{}
	for _, part := range strings.Split(link, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Format: <url>; rel="next"
		semi := strings.Index(part, ";")
		if semi <= 0 {
			continue
		}
		urlPart := strings.TrimSpace(part[:semi])
		metaPart := part[semi+1:]

		if !strings.HasPrefix(urlPart, "<") || !strings.HasSuffix(urlPart, ">") {
			continue
		}
		u := strings.TrimSuffix(strings.TrimPrefix(urlPart, "<"), ">")

		relIdx := strings.Index(metaPart, "rel=\"")
		if relIdx < 0 {
			continue
		}
		relPart := metaPart[relIdx+5:]
		end := strings.Index(relPart, "\"")
		if end < 0 {
			continue
		}
		rel := relPart[:end]

		key := rel + "_url"
		out[key] = u
		if page, ok := extractPage(u); ok {
			out[rel+"_page"] = page
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func extractPage(rawURL string) (int, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, false
	}
	pageStr := u.Query().Get("page")
	if pageStr == "" {
		return 0, false
	}
	page, err := strconv.Atoi(pageStr)
	if err != nil || page <= 0 {
		return 0, false
	}
	return page, true
}

func (g *githubServer) handleListRepos(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.String("owner", "")
	repoType := v.String("type", "owner")
	perPage := normalizePerPage(v.Int("per_page", 30), 30)
	page := normalizePage(v.Int("page", 1))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var path string
	if owner != "" {
		path = fmt.Sprintf("/users/%s/repos?type=%s&per_page=%d&page=%d", owner, repoType, perPage, page)
	} else {
		path = fmt.Sprintf("/user/repos?type=%s&per_page=%d&page=%d", repoType, perPage, page)
	}

	result, meta, err := g.requestListWithMeta(ctx, path)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"repositories": result, "count": len(result), "pagination": meta})
}

func (g *githubServer) handleGetRepo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := g.request(ctx, "GET", fmt.Sprintf("/repos/%s/%s", owner, repo), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleListIssues(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	state := v.String("state", "open")
	labels := v.String("labels", "")
	perPage := normalizePerPage(v.Int("per_page", 30), 30)
	page := normalizePage(v.Int("page", 1))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/repos/%s/%s/issues?state=%s&per_page=%d&page=%d", owner, repo, state, perPage, page)
	if labels != "" {
		path += "&labels=" + url.QueryEscape(labels)
	}

	result, meta, err := g.requestListWithMeta(ctx, path)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"issues": result, "count": len(result), "pagination": meta})
}

func (g *githubServer) handleGetIssue(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	number := v.RequiredInt("number")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := g.request(ctx, "GET", fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleCreateIssue(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	title := v.Required("title")
	body := v.String("body", "")
	labels := v.StringSlice("labels")
	assignees := v.StringSlice("assignees")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{"title": title}
	if body != "" {
		payload["body"] = body
	}
	if len(labels) > 0 {
		payload["labels"] = labels
	}
	if len(assignees) > 0 {
		payload["assignees"] = assignees
	}

	result, err := g.request(ctx, "POST", fmt.Sprintf("/repos/%s/%s/issues", owner, repo), payload)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleListPRs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	state := v.String("state", "open")
	perPage := normalizePerPage(v.Int("per_page", 30), 30)
	page := normalizePage(v.Int("page", 1))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls?state=%s&per_page=%d&page=%d", owner, repo, state, perPage, page)

	result, meta, err := g.requestListWithMeta(ctx, path)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"pull_requests": result, "count": len(result), "pagination": meta})
}

func (g *githubServer) handleGetPR(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	number := v.RequiredInt("number")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := g.request(ctx, "GET", fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleListCommits(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	sha := v.String("sha", "")
	perPage := normalizePerPage(v.Int("per_page", 30), 30)
	page := normalizePage(v.Int("page", 1))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/repos/%s/%s/commits?per_page=%d&page=%d", owner, repo, perPage, page)
	if sha != "" {
		path += "&sha=" + url.QueryEscape(sha)
	}

	result, meta, err := g.requestListWithMeta(ctx, path)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"commits": result, "count": len(result), "pagination": meta})
}

func (g *githubServer) handleSearchRepos(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	perPage := normalizePerPage(v.Int("per_page", 30), 30)
	page := normalizePage(v.Int("page", 1))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/search/repositories?q=%s&per_page=%d&page=%d", url.QueryEscape(query), perPage, page)

	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleSearchCode(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	perPage := normalizePerPage(v.Int("per_page", 30), 30)
	page := normalizePage(v.Int("page", 1))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/search/code?q=%s&per_page=%d&page=%d", url.QueryEscape(query), perPage, page)

	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func (g *githubServer) handleGetFileContents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	filePath := v.Required("path")
	ref := v.String("ref", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, filePath)
	if ref != "" {
		path += "?ref=" + ref
	}

	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}
