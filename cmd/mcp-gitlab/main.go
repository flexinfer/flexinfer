// mcp-gitlab is a fast GitLab MCP server written in Go.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var version = "1.0.0"

type gitlabServer struct {
	token      string
	apiURL     string
	httpClient *http.Client
}

type apiError struct {
	StatusCode int
	Body       string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("GitLab API error %d: %s", e.StatusCode, e.Body)
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

	token := os.Getenv("GITLAB_PERSONAL_ACCESS_TOKEN")
	if token == "" {
		token = os.Getenv("GITLAB_TOKEN")
	}

	apiURL := os.Getenv("GITLAB_API_URL")
	if apiURL == "" {
		apiURL = "https://gitlab.com/api/v4"
	}
	// Ensure no trailing slash
	apiURL = strings.TrimSuffix(apiURL, "/")

	gl := &gitlabServer{
		token:  token,
		apiURL: apiURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	server := mcp.NewServer("mcp-gitlab", version)
	server.SetInstructions("Fast Go-native GitLab MCP server. Supports projects, issues, merge requests, and more.")

	// search_repositories
	server.AddTool(mcp.Tool{
		Name:        "search_repositories",
		Description: "Search for GitLab projects/repositories",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"search": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100). Defaults to 20.",
				},
			},
			Required: []string{"search"},
		},
	}, gl.handleSearchRepositories)

	// get_file_contents
	server.AddTool(mcp.Tool{
		Name:        "get_file_contents",
		Description: "Get contents of a file from a repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path (e.g., 'namespace/project')",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File path within the repository",
				},
				"ref": map[string]any{
					"type":        "string",
					"description": "Branch, tag, or commit SHA. Defaults to default branch.",
				},
			},
			Required: []string{"project", "path"},
		},
	}, gl.handleGetFileContents)

	// create_or_update_file
	server.AddTool(mcp.Tool{
		Name:        "create_or_update_file",
		Description: "Create or update a file in a repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File path",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Branch name",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "File content",
				},
				"commit_message": map[string]any{
					"type":        "string",
					"description": "Commit message",
				},
			},
			Required: []string{"project", "path", "branch", "content", "commit_message"},
		},
	}, gl.handleCreateOrUpdateFile)

	// push_files
	server.AddTool(mcp.Tool{
		Name:        "push_files",
		Description: "Push multiple files in a single commit",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Branch name",
				},
				"commit_message": map[string]any{
					"type":        "string",
					"description": "Commit message",
				},
				"actions": map[string]any{
					"type":        "array",
					"description": "Array of file actions",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"action":    map[string]any{"type": "string", "description": "create, update, delete, move"},
							"file_path": map[string]any{"type": "string"},
							"content":   map[string]any{"type": "string"},
						},
					},
				},
			},
			Required: []string{"project", "branch", "commit_message", "actions"},
		},
	}, gl.handlePushFiles)

	// create_repository
	server.AddTool(mcp.Tool{
		Name:        "create_repository",
		Description: "Create a new GitLab project/repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Project name",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Project description",
				},
				"visibility": map[string]any{
					"type":        "string",
					"description": "Visibility: private, internal, public. Defaults to private.",
				},
				"namespace_id": map[string]any{
					"type":        "integer",
					"description": "Namespace/group ID to create project in",
				},
				"initialize_with_readme": map[string]any{
					"type":        "boolean",
					"description": "Initialize with README",
				},
			},
			Required: []string{"name"},
		},
	}, gl.handleCreateRepository)

	// create_issue
	server.AddTool(mcp.Tool{
		Name:        "create_issue",
		Description: "Create a new issue in a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Issue title",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Issue description",
				},
				"labels": map[string]any{
					"type":        "string",
					"description": "Comma-separated label names",
				},
				"assignee_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "integer"},
					"description": "User IDs to assign",
				},
			},
			Required: []string{"project", "title"},
		},
	}, gl.handleCreateIssue)

	// list_issues
	server.AddTool(mcp.Tool{
		Name:        "list_issues",
		Description: "List issues for a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"state": map[string]any{
					"type":        "string",
					"description": "State: opened, closed, all. Defaults to 'opened'.",
				},
				"labels": map[string]any{
					"type":        "string",
					"description": "Comma-separated label names",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100)",
				},
			},
			Required: []string{"project"},
		},
	}, gl.handleListIssues)

	// create_merge_request
	server.AddTool(mcp.Tool{
		Name:        "create_merge_request",
		Description: "Create a new merge request",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"source_branch": map[string]any{
					"type":        "string",
					"description": "Source branch",
				},
				"target_branch": map[string]any{
					"type":        "string",
					"description": "Target branch",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "MR title",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "MR description",
				},
				"remove_source_branch": map[string]any{
					"type":        "boolean",
					"description": "Remove source branch after merge",
				},
			},
			Required: []string{"project", "source_branch", "target_branch", "title"},
		},
	}, gl.handleCreateMergeRequest)

	// list_merge_requests
	server.AddTool(mcp.Tool{
		Name:        "list_merge_requests",
		Description: "List merge requests for a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"state": map[string]any{
					"type":        "string",
					"description": "State: opened, closed, merged, all. Defaults to 'opened'.",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100)",
				},
			},
			Required: []string{"project"},
		},
	}, gl.handleListMergeRequests)

	// fork_repository
	server.AddTool(mcp.Tool{
		Name:        "fork_repository",
		Description: "Fork a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path to fork",
				},
				"namespace_id": map[string]any{
					"type":        "integer",
					"description": "Namespace ID to fork into",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "New project name (optional)",
				},
			},
			Required: []string{"project"},
		},
	}, gl.handleForkRepository)

	// create_branch
	server.AddTool(mcp.Tool{
		Name:        "create_branch",
		Description: "Create a new branch",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "New branch name",
				},
				"ref": map[string]any{
					"type":        "string",
					"description": "Source branch or commit SHA",
				},
			},
			Required: []string{"project", "branch", "ref"},
		},
	}, gl.handleCreateBranch)

	// get_project
	server.AddTool(mcp.Tool{
		Name:        "get_project",
		Description: "Get project details",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
			},
			Required: []string{"project"},
		},
	}, gl.handleGetProject)

	// list_projects
	server.AddTool(mcp.Tool{
		Name:        "list_projects",
		Description: "List projects accessible to the authenticated user",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owned": map[string]any{
					"type":        "boolean",
					"description": "Only list owned projects",
				},
				"membership": map[string]any{
					"type":        "boolean",
					"description": "Only list projects user is member of",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100). Defaults to 20.",
				},
			},
		},
	}, gl.handleListProjects)

	// verify_token
	server.AddTool(mcp.Tool{
		Name:        "verify_token",
		Description: "Verify GitLab API token status and scopes",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, gl.handleVerifyToken)

	// list_pipelines
	server.AddTool(mcp.Tool{
		Name:        "list_pipelines",
		Description: "List pipelines for a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"ref": map[string]any{
					"type":        "string",
					"description": "Filter by ref (branch/tag)",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "Filter by status (running, pending, success, failed, canceled, skipped, manual)",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100). Defaults to 20.",
				},
			},
			Required: []string{"project"},
		},
	}, gl.handleListPipelines)

	// get_pipeline
	server.AddTool(mcp.Tool{
		Name:        "get_pipeline",
		Description: "Get a single pipeline by ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"pipeline_id": map[string]any{
					"type":        "integer",
					"description": "Pipeline ID",
				},
			},
			Required: []string{"project", "pipeline_id"},
		},
	}, gl.handleGetPipeline)

	// create_pipeline
	server.AddTool(mcp.Tool{
		Name:        "create_pipeline",
		Description: "Create/run a pipeline for a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"ref": map[string]any{
					"type":        "string",
					"description": "Branch/tag to run the pipeline for",
				},
				"variables": map[string]any{
					"type":        "array",
					"description": "Pipeline variables (array of {key,value})",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"key":   map[string]any{"type": "string"},
							"value": map[string]any{"type": "string"},
						},
						"required": []string{"key", "value"},
					},
				},
			},
			Required: []string{"project", "ref"},
		},
	}, gl.handleCreatePipeline)

	// cancel_pipeline
	server.AddTool(mcp.Tool{
		Name:        "cancel_pipeline",
		Description: "Cancel a running pipeline",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"pipeline_id": map[string]any{
					"type":        "integer",
					"description": "Pipeline ID",
				},
			},
			Required: []string{"project", "pipeline_id"},
		},
	}, gl.handleCancelPipeline)

	// retry_pipeline
	server.AddTool(mcp.Tool{
		Name:        "retry_pipeline",
		Description: "Retry a pipeline",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"pipeline_id": map[string]any{
					"type":        "integer",
					"description": "Pipeline ID",
				},
			},
			Required: []string{"project", "pipeline_id"},
		},
	}, gl.handleRetryPipeline)

	// list_pipeline_jobs
	server.AddTool(mcp.Tool{
		Name:        "list_pipeline_jobs",
		Description: "List jobs for a pipeline",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"pipeline_id": map[string]any{
					"type":        "integer",
					"description": "Pipeline ID",
				},
				"scope": map[string]any{
					"type":        "string",
					"description": "Filter by scope (created, pending, running, failed, success, canceled, skipped, manual)",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100). Defaults to 100.",
				},
			},
			Required: []string{"project", "pipeline_id"},
		},
	}, gl.handleListPipelineJobs)

	// get_job
	server.AddTool(mcp.Tool{
		Name:        "get_job",
		Description: "Get a single job by ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"job_id": map[string]any{
					"type":        "integer",
					"description": "Job ID",
				},
			},
			Required: []string{"project", "job_id"},
		},
	}, gl.handleGetJob)

	// get_job_trace
	server.AddTool(mcp.Tool{
		Name:        "get_job_trace",
		Description: "Fetch job log/trace text",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"job_id": map[string]any{
					"type":        "integer",
					"description": "Job ID",
				},
				"tail_lines": map[string]any{
					"type":        "integer",
					"description": "Return only the last N lines (default 200).",
				},
				"max_bytes": map[string]any{
					"type":        "integer",
					"description": "Limit trace size by keeping only the last N bytes (default 200000).",
				},
			},
			Required: []string{"project", "job_id"},
		},
	}, gl.handleGetJobTrace)

	// retry_job
	server.AddTool(mcp.Tool{
		Name:        "retry_job",
		Description: "Retry a job",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"job_id": map[string]any{
					"type":        "integer",
					"description": "Job ID",
				},
			},
			Required: []string{"project", "job_id"},
		},
	}, gl.handleRetryJob)

	// play_job
	server.AddTool(mcp.Tool{
		Name:        "play_job",
		Description: "Play (trigger) a manual job",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"job_id": map[string]any{
					"type":        "integer",
					"description": "Job ID",
				},
				"job_variables": map[string]any{
					"type":        "array",
					"description": "Job variables (array of {key,value})",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"key":   map[string]any{"type": "string"},
							"value": map[string]any{"type": "string"},
						},
						"required": []string{"key", "value"},
					},
				},
			},
			Required: []string{"project", "job_id"},
		},
	}, gl.handlePlayJob)

	// cancel_job
	server.AddTool(mcp.Tool{
		Name:        "cancel_job",
		Description: "Cancel a job",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"job_id": map[string]any{
					"type":        "integer",
					"description": "Job ID",
				},
			},
			Required: []string{"project", "job_id"},
		},
	}, gl.handleCancelJob)

	// pipeline_summary - comprehensive view in single call
	server.AddTool(mcp.Tool{
		Name:        "pipeline_summary",
		Description: "Get comprehensive pipeline summary including jobs, status counts, and optionally test report. Fetches pipeline + jobs concurrently.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"pipeline_id": map[string]any{
					"type":        "integer",
					"description": "Pipeline ID",
				},
				"include_test_report": map[string]any{
					"type":        "boolean",
					"description": "Include test report summary (default: true)",
				},
				"include_failed_job_logs": map[string]any{
					"type":        "boolean",
					"description": "Include last 50 lines of failed job logs (default: false)",
				},
			},
			Required: []string{"project", "pipeline_id"},
		},
	}, gl.handlePipelineSummary)

	// get_test_report - parse JUnit test report from pipeline
	server.AddTool(mcp.Tool{
		Name:        "get_test_report",
		Description: "Get JUnit test report from a pipeline. Returns summary and failed test details.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"pipeline_id": map[string]any{
					"type":        "integer",
					"description": "Pipeline ID",
				},
				"include_passed": map[string]any{
					"type":        "boolean",
					"description": "Include passed tests in response (default: false)",
				},
				"max_failures": map[string]any{
					"type":        "integer",
					"description": "Maximum number of failed tests to return (default: 50)",
				},
			},
			Required: []string{"project", "pipeline_id"},
		},
	}, gl.handleGetTestReport)

	// get_artifacts - download job artifacts or specific files
	server.AddTool(mcp.Tool{
		Name:        "get_artifacts",
		Description: "Download job artifacts or a specific file within artifacts. Returns content inline (text) or base64 (binary).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"job_id": map[string]any{
					"type":        "integer",
					"description": "Job ID",
				},
				"artifact_path": map[string]any{
					"type":        "string",
					"description": "Specific file within artifacts (optional). If not provided, returns artifact metadata.",
				},
				"max_size_bytes": map[string]any{
					"type":        "integer",
					"description": "Maximum size to return inline (default: 1MB, max: 10MB). Larger artifacts return metadata with download URL.",
				},
			},
			Required: []string{"project", "job_id"},
		},
	}, gl.handleGetArtifacts)

	// poll_pipeline - poll pipeline until terminal state
	server.AddTool(mcp.Tool{
		Name:        "poll_pipeline",
		Description: "Poll pipeline until it reaches a terminal state (success, failed, canceled, skipped, manual). Blocks for up to timeout_seconds.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"pipeline_id": map[string]any{
					"type":        "integer",
					"description": "Pipeline ID",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Maximum time to wait (default: 300, max: 600)",
				},
				"poll_interval_seconds": map[string]any{
					"type":        "integer",
					"description": "Interval between polls (default: 5, min: 2). Adapts to 10s when pending.",
				},
				"include_job_logs": map[string]any{
					"type":        "boolean",
					"description": "Include last 50 lines of failed job logs in result (default: false)",
				},
			},
			Required: []string{"project", "pipeline_id"},
		},
	}, gl.handlePollPipeline)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (g *gitlabServer) request(ctx context.Context, method, path string, body any) (map[string]any, error) {
	reqURL := g.apiURL + path

	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = strings.NewReader(string(b))
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	if g.token != "" {
		req.Header.Set("PRIVATE-TOKEN", g.token)
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
		return nil, &apiError{StatusCode: resp.StatusCode, Body: string(respBody)}
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

func (g *gitlabServer) requestList(ctx context.Context, path string) ([]any, error) {
	reqURL := g.apiURL + path

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	if g.token != "" {
		req.Header.Set("PRIVATE-TOKEN", g.token)
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
		return nil, &apiError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var result []any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return result, nil
}

func (g *gitlabServer) requestRaw(ctx context.Context, method, path string, headers map[string]string) ([]byte, *http.Response, error) {
	reqURL := g.apiURL + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, nil)
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("Accept", "*/*")
	for k, v := range headers {
		if k != "" {
			req.Header.Set(k, v)
		}
	}
	if g.token != "" {
		req.Header.Set("PRIVATE-TOKEN", g.token)
	}

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, nil, &apiError{StatusCode: resp.StatusCode, Body: string(b)}
	}
	return b, resp, nil
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

func encodeProject(project string) string {
	return url.PathEscape(project)
}

func (g *gitlabServer) handleSearchRepositories(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	search := getStringArg(args, "search", "")
	perPage := getIntArg(args, "per_page", 20)

	if search == "" {
		return nil, fmt.Errorf("search is required")
	}

	path := fmt.Sprintf("/projects?search=%s&per_page=%d", url.QueryEscape(search), perPage)

	result, err := g.requestList(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"projects": result, "count": len(result)})
}

func (g *gitlabServer) handleGetFileContents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	filePath := getStringArg(args, "path", "")
	ref := getStringArg(args, "ref", "")

	if project == "" || filePath == "" {
		return nil, fmt.Errorf("project and path are required")
	}

	path := fmt.Sprintf("/projects/%s/repository/files/%s", encodeProject(project), url.PathEscape(filePath))
	if ref != "" {
		path += "?ref=" + url.QueryEscape(ref)
	} else {
		path += "?ref=HEAD"
	}

	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleCreateOrUpdateFile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	filePath := getStringArg(args, "path", "")
	branch := getStringArg(args, "branch", "")
	content := getStringArg(args, "content", "")
	commitMessage := getStringArg(args, "commit_message", "")

	if project == "" || filePath == "" || branch == "" || content == "" || commitMessage == "" {
		return nil, fmt.Errorf("project, path, branch, content, and commit_message are required")
	}

	payload := map[string]any{
		"branch":         branch,
		"content":        content,
		"commit_message": commitMessage,
	}

	// Try PUT first (update), if fails try POST (create)
	path := fmt.Sprintf("/projects/%s/repository/files/%s", encodeProject(project), url.PathEscape(filePath))

	result, err := g.request(ctx, "PUT", path, payload)
	if err != nil {
		// Try create
		result, err = g.request(ctx, "POST", path, payload)
		if err != nil {
			return nil, err
		}
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handlePushFiles(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	branch := getStringArg(args, "branch", "")
	commitMessage := getStringArg(args, "commit_message", "")
	actions, _ := args["actions"].([]any)

	if project == "" || branch == "" || commitMessage == "" || len(actions) == 0 {
		return nil, fmt.Errorf("project, branch, commit_message, and actions are required")
	}

	payload := map[string]any{
		"branch":         branch,
		"commit_message": commitMessage,
		"actions":        actions,
	}

	path := fmt.Sprintf("/projects/%s/repository/commits", encodeProject(project))

	result, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleCreateRepository(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	name := getStringArg(args, "name", "")
	description := getStringArg(args, "description", "")
	visibility := getStringArg(args, "visibility", "private")
	namespaceID := getIntArg(args, "namespace_id", 0)
	initWithReadme := getBoolArg(args, "initialize_with_readme", false)

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	payload := map[string]any{
		"name":                   name,
		"visibility":             visibility,
		"initialize_with_readme": initWithReadme,
	}
	if description != "" {
		payload["description"] = description
	}
	if namespaceID > 0 {
		payload["namespace_id"] = namespaceID
	}

	result, err := g.request(ctx, "POST", "/projects", payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleCreateIssue(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	title := getStringArg(args, "title", "")
	description := getStringArg(args, "description", "")
	labels := getStringArg(args, "labels", "")

	if project == "" || title == "" {
		return nil, fmt.Errorf("project and title are required")
	}

	payload := map[string]any{"title": title}
	if description != "" {
		payload["description"] = description
	}
	if labels != "" {
		payload["labels"] = labels
	}
	if assigneeIDs, ok := args["assignee_ids"].([]any); ok {
		payload["assignee_ids"] = assigneeIDs
	}

	path := fmt.Sprintf("/projects/%s/issues", encodeProject(project))

	result, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleListIssues(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	state := getStringArg(args, "state", "opened")
	labels := getStringArg(args, "labels", "")
	perPage := getIntArg(args, "per_page", 20)

	if project == "" {
		return nil, fmt.Errorf("project is required")
	}

	path := fmt.Sprintf("/projects/%s/issues?state=%s&per_page=%d", encodeProject(project), state, perPage)
	if labels != "" {
		path += "&labels=" + url.QueryEscape(labels)
	}

	result, err := g.requestList(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"issues": result, "count": len(result)})
}

func (g *gitlabServer) handleCreateMergeRequest(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	sourceBranch := getStringArg(args, "source_branch", "")
	targetBranch := getStringArg(args, "target_branch", "")
	title := getStringArg(args, "title", "")
	description := getStringArg(args, "description", "")
	removeSourceBranch := getBoolArg(args, "remove_source_branch", false)

	if project == "" || sourceBranch == "" || targetBranch == "" || title == "" {
		return nil, fmt.Errorf("project, source_branch, target_branch, and title are required")
	}

	payload := map[string]any{
		"source_branch":        sourceBranch,
		"target_branch":        targetBranch,
		"title":                title,
		"remove_source_branch": removeSourceBranch,
	}
	if description != "" {
		payload["description"] = description
	}

	path := fmt.Sprintf("/projects/%s/merge_requests", encodeProject(project))

	result, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleListMergeRequests(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	state := getStringArg(args, "state", "opened")
	perPage := getIntArg(args, "per_page", 20)

	if project == "" {
		return nil, fmt.Errorf("project is required")
	}

	path := fmt.Sprintf("/projects/%s/merge_requests?state=%s&per_page=%d", encodeProject(project), state, perPage)

	result, err := g.requestList(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"merge_requests": result, "count": len(result)})
}

func (g *gitlabServer) handleForkRepository(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	namespaceID := getIntArg(args, "namespace_id", 0)
	name := getStringArg(args, "name", "")

	if project == "" {
		return nil, fmt.Errorf("project is required")
	}

	payload := map[string]any{}
	if namespaceID > 0 {
		payload["namespace_id"] = namespaceID
	}
	if name != "" {
		payload["name"] = name
	}

	path := fmt.Sprintf("/projects/%s/fork", encodeProject(project))

	result, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleCreateBranch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	branch := getStringArg(args, "branch", "")
	ref := getStringArg(args, "ref", "")

	if project == "" || branch == "" || ref == "" {
		return nil, fmt.Errorf("project, branch, and ref are required")
	}

	payload := map[string]any{
		"branch": branch,
		"ref":    ref,
	}

	path := fmt.Sprintf("/projects/%s/repository/branches", encodeProject(project))

	result, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleGetProject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")

	if project == "" {
		return nil, fmt.Errorf("project is required")
	}

	path := fmt.Sprintf("/projects/%s", encodeProject(project))

	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleListProjects(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	owned := getBoolArg(args, "owned", false)
	membership := getBoolArg(args, "membership", false)
	perPage := getIntArg(args, "per_page", 20)

	path := fmt.Sprintf("/projects?per_page=%d", perPage)
	if owned {
		path += "&owned=true"
	}
	if membership {
		path += "&membership=true"
	}

	result, err := g.requestList(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"projects": result, "count": len(result)})
}

func (g *gitlabServer) handleVerifyToken(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(g.token) == "" {
		return nil, fmt.Errorf("GITLAB_PERSONAL_ACCESS_TOKEN is not set")
	}
	if strings.Contains(g.token, "${") {
		return nil, fmt.Errorf("GITLAB_PERSONAL_ACCESS_TOKEN appears to be unexpanded (%q); check your Loom secrets/keychain resolution", g.token)
	}

	result := map[string]any{
		"ok":      false,
		"api_url": g.apiURL,
		"token": map[string]any{
			"present": true,
		},
	}

	// Best-effort: PAT metadata (scopes, expiry). Not all GitLab versions expose this endpoint.
	if tok, err := g.request(ctx, "GET", "/personal_access_tokens/self", nil); err == nil {
		// Never return the actual token; the endpoint doesn't include it anyway, but keep future-proof.
		delete(tok, "token")
		result["personal_access_token"] = tok
	} else if err != nil {
		if ae, ok := err.(*apiError); ok && ae.StatusCode == 404 {
			// Older GitLab instances may not support this endpoint; fall back to /user.
		} else {
			// If it's not a 404, bubble up (401/403/5xx/etc).
			return nil, err
		}
	}

	user, err := g.request(ctx, "GET", "/user", nil)
	if err != nil {
		return nil, err
	}
	result["user"] = user
	result["ok"] = true
	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleListPipelines(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	ref := getStringArg(args, "ref", "")
	status := getStringArg(args, "status", "")
	perPage := getIntArg(args, "per_page", 20)
	if perPage <= 0 {
		perPage = 20
	}

	if project == "" {
		return nil, fmt.Errorf("project is required")
	}

	q := url.Values{}
	q.Set("per_page", fmt.Sprintf("%d", perPage))
	if ref != "" {
		q.Set("ref", ref)
	}
	if status != "" {
		q.Set("status", status)
	}
	path := fmt.Sprintf("/projects/%s/pipelines?%s", encodeProject(project), q.Encode())

	pipelines, err := g.requestList(ctx, path)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(map[string]any{"pipelines": pipelines, "count": len(pipelines)})
}

func (g *gitlabServer) handleGetPipeline(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	pipelineID := getIntArg(args, "pipeline_id", 0)

	if project == "" || pipelineID <= 0 {
		return nil, fmt.Errorf("project and pipeline_id are required")
	}

	path := fmt.Sprintf("/projects/%s/pipelines/%d", encodeProject(project), pipelineID)
	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleCreatePipeline(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	ref := getStringArg(args, "ref", "")

	if project == "" || ref == "" {
		return nil, fmt.Errorf("project and ref are required")
	}

	payload := map[string]any{
		"ref": ref,
	}
	if vars, ok := args["variables"].([]any); ok && len(vars) > 0 {
		payload["variables"] = vars
	}

	path := fmt.Sprintf("/projects/%s/pipeline", encodeProject(project))
	result, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleCancelPipeline(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	pipelineID := getIntArg(args, "pipeline_id", 0)

	if project == "" || pipelineID <= 0 {
		return nil, fmt.Errorf("project and pipeline_id are required")
	}

	path := fmt.Sprintf("/projects/%s/pipelines/%d/cancel", encodeProject(project), pipelineID)
	result, err := g.request(ctx, "POST", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleRetryPipeline(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	pipelineID := getIntArg(args, "pipeline_id", 0)

	if project == "" || pipelineID <= 0 {
		return nil, fmt.Errorf("project and pipeline_id are required")
	}

	path := fmt.Sprintf("/projects/%s/pipelines/%d/retry", encodeProject(project), pipelineID)
	result, err := g.request(ctx, "POST", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleListPipelineJobs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	pipelineID := getIntArg(args, "pipeline_id", 0)
	scope := getStringArg(args, "scope", "")
	perPage := getIntArg(args, "per_page", 100)
	if perPage <= 0 {
		perPage = 100
	}

	if project == "" || pipelineID <= 0 {
		return nil, fmt.Errorf("project and pipeline_id are required")
	}

	q := url.Values{}
	q.Set("per_page", fmt.Sprintf("%d", perPage))
	if scope != "" {
		q.Set("scope", scope)
	}

	path := fmt.Sprintf("/projects/%s/pipelines/%d/jobs?%s", encodeProject(project), pipelineID, q.Encode())
	jobs, err := g.requestList(ctx, path)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(map[string]any{"jobs": jobs, "count": len(jobs)})
}

func (g *gitlabServer) handleGetJob(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	jobID := getIntArg(args, "job_id", 0)

	if project == "" || jobID <= 0 {
		return nil, fmt.Errorf("project and job_id are required")
	}

	path := fmt.Sprintf("/projects/%s/jobs/%d", encodeProject(project), jobID)
	job, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(job)
}

func (g *gitlabServer) handleGetJobTrace(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	jobID := getIntArg(args, "job_id", 0)
	tailLines := getIntArg(args, "tail_lines", 200)
	maxBytes := getIntArg(args, "max_bytes", 200_000)
	if tailLines <= 0 {
		tailLines = 200
	}
	if maxBytes <= 0 {
		maxBytes = 200_000
	}

	if project == "" || jobID <= 0 {
		return nil, fmt.Errorf("project and job_id are required")
	}

	path := fmt.Sprintf("/projects/%s/jobs/%d/trace", encodeProject(project), jobID)
	b, resp, err := g.requestRaw(ctx, "GET", path, map[string]string{"Accept": "text/plain"})
	if err != nil {
		return nil, err
	}

	truncated := false
	if len(b) > maxBytes {
		truncated = true
		b = b[len(b)-maxBytes:]
	}

	trace := string(b)
	lines := strings.Split(trace, "\n")
	if tailLines > 0 && len(lines) > tailLines {
		truncated = true
		lines = lines[len(lines)-tailLines:]
		trace = strings.Join(lines, "\n")
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"project":      project,
		"job_id":       jobID,
		"content_type": resp.Header.Get("Content-Type"),
		"truncated":    truncated,
		"tail_lines":   tailLines,
		"max_bytes":    maxBytes,
		"returned_lines": func() int {
			if trace == "" {
				return 0
			}
			return len(strings.Split(trace, "\n"))
		}(),
		"trace": trace,
	})
}

func (g *gitlabServer) handleRetryJob(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	jobID := getIntArg(args, "job_id", 0)

	if project == "" || jobID <= 0 {
		return nil, fmt.Errorf("project and job_id are required")
	}

	path := fmt.Sprintf("/projects/%s/jobs/%d/retry", encodeProject(project), jobID)
	job, err := g.request(ctx, "POST", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(job)
}

func (g *gitlabServer) handlePlayJob(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	jobID := getIntArg(args, "job_id", 0)

	if project == "" || jobID <= 0 {
		return nil, fmt.Errorf("project and job_id are required")
	}

	var payload any
	if vars, ok := args["job_variables"].([]any); ok && len(vars) > 0 {
		payload = map[string]any{"job_variables": vars}
	}

	path := fmt.Sprintf("/projects/%s/jobs/%d/play", encodeProject(project), jobID)
	job, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(job)
}

func (g *gitlabServer) handleCancelJob(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	jobID := getIntArg(args, "job_id", 0)

	if project == "" || jobID <= 0 {
		return nil, fmt.Errorf("project and job_id are required")
	}

	path := fmt.Sprintf("/projects/%s/jobs/%d/cancel", encodeProject(project), jobID)
	job, err := g.request(ctx, "POST", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(job)
}

// Pipeline polling & summary handlers

func (g *gitlabServer) handlePipelineSummary(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	pipelineID := getIntArg(args, "pipeline_id", 0)
	includeTestReport := getBoolArg(args, "include_test_report", true)
	includeFailedJobLogs := getBoolArg(args, "include_failed_job_logs", false)

	if project == "" || pipelineID <= 0 {
		return nil, fmt.Errorf("project and pipeline_id are required")
	}

	// Fetch pipeline and jobs concurrently
	type pipelineResult struct {
		data map[string]any
		err  error
	}
	type jobsResult struct {
		data []any
		err  error
	}

	pipelineCh := make(chan pipelineResult, 1)
	jobsCh := make(chan jobsResult, 1)

	go func() {
		path := fmt.Sprintf("/projects/%s/pipelines/%d", encodeProject(project), pipelineID)
		result, err := g.request(ctx, "GET", path, nil)
		pipelineCh <- pipelineResult{data: result, err: err}
	}()

	go func() {
		path := fmt.Sprintf("/projects/%s/pipelines/%d/jobs?per_page=100", encodeProject(project), pipelineID)
		result, err := g.requestList(ctx, path)
		jobsCh <- jobsResult{data: result, err: err}
	}()

	pipelineRes := <-pipelineCh
	jobsRes := <-jobsCh

	if pipelineRes.err != nil {
		return nil, pipelineRes.err
	}
	if jobsRes.err != nil {
		return nil, jobsRes.err
	}

	// Build job summary
	jobSummary := g.summarizeJobs(jobsRes.data, includeFailedJobLogs, ctx, project)

	result := map[string]any{
		"ok":       true,
		"pipeline": pipelineRes.data,
		"jobs":     jobSummary,
	}

	// Optionally fetch test report
	if includeTestReport {
		testReport, err := g.fetchTestReport(ctx, project, pipelineID, false, 20)
		if err == nil && testReport != nil {
			result["test_report"] = testReport
		}
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleGetTestReport(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	pipelineID := getIntArg(args, "pipeline_id", 0)
	includePassed := getBoolArg(args, "include_passed", false)
	maxFailures := getIntArg(args, "max_failures", 50)

	if project == "" || pipelineID <= 0 {
		return nil, fmt.Errorf("project and pipeline_id are required")
	}

	if maxFailures <= 0 {
		maxFailures = 50
	}

	testReport, err := g.fetchTestReport(ctx, project, pipelineID, includePassed, maxFailures)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"project":     project,
		"pipeline_id": pipelineID,
		"test_report": testReport,
	})
}

func (g *gitlabServer) handleGetArtifacts(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	jobID := getIntArg(args, "job_id", 0)
	artifactPath := getStringArg(args, "artifact_path", "")
	maxSizeBytes := getIntArg(args, "max_size_bytes", 1024*1024) // 1MB default

	if project == "" || jobID <= 0 {
		return nil, fmt.Errorf("project and job_id are required")
	}

	// Cap at 10MB
	if maxSizeBytes <= 0 {
		maxSizeBytes = 1024 * 1024
	}
	if maxSizeBytes > 10*1024*1024 {
		maxSizeBytes = 10 * 1024 * 1024
	}

	// If specific file requested, fetch that file
	if artifactPath != "" {
		path := fmt.Sprintf("/projects/%s/jobs/%d/artifacts/%s", encodeProject(project), jobID, artifactPath)
		data, resp, err := g.requestRaw(ctx, "GET", path, nil)
		if err != nil {
			return nil, err
		}

		result := map[string]any{
			"ok":            true,
			"project":       project,
			"job_id":        jobID,
			"artifact_path": artifactPath,
			"size_bytes":    len(data),
		}

		contentType := ""
		if resp != nil {
			contentType = resp.Header.Get("Content-Type")
		}
		result["content_type"] = contentType

		if len(data) > maxSizeBytes {
			result["truncated"] = true
			result["download_url"] = fmt.Sprintf("%s/projects/%s/jobs/%d/artifacts/%s", g.apiURL, encodeProject(project), jobID, artifactPath)
		} else {
			// Check if text or binary
			if isTextContent(contentType, data) {
				result["content"] = string(data)
				result["encoding"] = "text"
			} else {
				result["content"] = encodeBase64(data)
				result["encoding"] = "base64"
			}
		}

		return mcp.JSONResult(result)
	}

	// No specific file - return artifact metadata
	path := fmt.Sprintf("/projects/%s/jobs/%d", encodeProject(project), jobID)
	job, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"ok":           true,
		"project":      project,
		"job_id":       jobID,
		"download_url": fmt.Sprintf("%s/projects/%s/jobs/%d/artifacts", g.apiURL, encodeProject(project), jobID),
	}

	// Extract artifact info from job
	if artifacts, ok := job["artifacts"].([]any); ok {
		result["artifacts"] = artifacts
	}
	if artifactFile, ok := job["artifacts_file"].(map[string]any); ok {
		result["artifacts_file"] = artifactFile
	}
	if size, ok := job["artifacts_size"].(float64); ok {
		result["total_size_bytes"] = int(size)
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handlePollPipeline(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	project := getStringArg(args, "project", "")
	pipelineID := getIntArg(args, "pipeline_id", 0)
	timeoutSeconds := getIntArg(args, "timeout_seconds", 300)
	pollIntervalSeconds := getIntArg(args, "poll_interval_seconds", 5)
	includeJobLogs := getBoolArg(args, "include_job_logs", false)

	if project == "" || pipelineID <= 0 {
		return nil, fmt.Errorf("project and pipeline_id are required")
	}

	// Validate and cap values
	if timeoutSeconds <= 0 {
		timeoutSeconds = 300
	}
	if timeoutSeconds > 600 {
		timeoutSeconds = 600
	}
	if pollIntervalSeconds < 2 {
		pollIntervalSeconds = 2
	}

	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	pollCount := 0

	var lastStatus string
	var lastPipeline map[string]any

	for {
		pollCount++

		// Check timeout
		if time.Now().After(deadline) {
			// Return current state with timed_out flag
			result := g.buildPipelineResult(project, pipelineID, lastPipeline, lastStatus, pollCount, true)
			if includeJobLogs && (lastStatus == "failed" || lastStatus == "canceled") {
				result["failed_job_logs"] = g.getFailedJobLogs(ctx, project, pipelineID, 50)
			}
			return mcp.JSONResult(result)
		}

		// Fetch pipeline status
		path := fmt.Sprintf("/projects/%s/pipelines/%d", encodeProject(project), pipelineID)
		pipeline, err := g.request(ctx, "GET", path, nil)
		if err != nil {
			// Transient error - retry
			if isTransientError(err) && time.Now().Before(deadline) {
				time.Sleep(time.Duration(pollIntervalSeconds) * time.Second)
				continue
			}
			return nil, err
		}

		lastPipeline = pipeline
		status, _ := pipeline["status"].(string)
		lastStatus = status

		// Check for terminal state
		if isTerminalPipelineStatus(status) {
			result := g.buildPipelineResult(project, pipelineID, pipeline, status, pollCount, false)
			if includeJobLogs && (status == "failed" || status == "canceled") {
				result["failed_job_logs"] = g.getFailedJobLogs(ctx, project, pipelineID, 50)
			}
			return mcp.JSONResult(result)
		}

		// Adaptive interval - longer for pending
		interval := pollIntervalSeconds
		if status == "pending" || status == "waiting_for_resource" {
			interval = 10
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(interval) * time.Second):
			// Continue polling
		}
	}
}

// Helper functions for pipeline polling

func (g *gitlabServer) summarizeJobs(jobs []any, includeFailedLogs bool, ctx context.Context, project string) map[string]any {
	summary := map[string]any{
		"total":     len(jobs),
		"by_status": map[string]int{},
	}

	statusCounts := make(map[string]int)
	var failedJobs []map[string]any
	var runningJobs []map[string]any

	for _, job := range jobs {
		jobMap, ok := job.(map[string]any)
		if !ok {
			continue
		}

		status, _ := jobMap["status"].(string)
		statusCounts[status]++

		// Capture failed jobs
		if status == "failed" {
			failedJob := map[string]any{
				"id":         jobMap["id"],
				"name":       jobMap["name"],
				"stage":      jobMap["stage"],
				"status":     status,
				"web_url":    jobMap["web_url"],
				"started_at": jobMap["started_at"],
			}
			if fr, ok := jobMap["failure_reason"].(string); ok {
				failedJob["failure_reason"] = fr
			}
			failedJobs = append(failedJobs, failedJob)
		}

		// Capture running jobs
		if status == "running" {
			runningJobs = append(runningJobs, map[string]any{
				"id":         jobMap["id"],
				"name":       jobMap["name"],
				"stage":      jobMap["stage"],
				"started_at": jobMap["started_at"],
			})
		}
	}

	summary["by_status"] = statusCounts
	if len(failedJobs) > 0 {
		summary["failed_jobs"] = failedJobs
	}
	if len(runningJobs) > 0 {
		summary["running_jobs"] = runningJobs
	}

	// Fetch failed job logs if requested
	if includeFailedLogs && len(failedJobs) > 0 {
		var logsForJobs []map[string]any
		for _, fj := range failedJobs {
			jobID, ok := fj["id"].(float64)
			if !ok {
				continue
			}
			logPath := fmt.Sprintf("/projects/%s/jobs/%d/trace", encodeProject(project), int(jobID))
			logData, _, err := g.requestRaw(ctx, "GET", logPath, map[string]string{"Accept": "text/plain"})
			if err == nil {
				lines := strings.Split(string(logData), "\n")
				if len(lines) > 50 {
					lines = lines[len(lines)-50:]
				}
				logsForJobs = append(logsForJobs, map[string]any{
					"job_id":     int(jobID),
					"job_name":   fj["name"],
					"tail_lines": strings.Join(lines, "\n"),
				})
			}
		}
		if len(logsForJobs) > 0 {
			summary["failed_job_logs"] = logsForJobs
		}
	}

	return summary
}

func (g *gitlabServer) fetchTestReport(ctx context.Context, project string, pipelineID int, includePassed bool, maxFailures int) (map[string]any, error) {
	path := fmt.Sprintf("/projects/%s/pipelines/%d/test_report", encodeProject(project), pipelineID)
	report, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		// Test report may not exist
		if ae, ok := err.(*apiError); ok && ae.StatusCode == 404 {
			return nil, nil
		}
		return nil, err
	}

	result := map[string]any{
		"total_time":    report["total_time"],
		"total_count":   report["total_count"],
		"success_count": report["success_count"],
		"failed_count":  report["failed_count"],
		"skipped_count": report["skipped_count"],
		"error_count":   report["error_count"],
	}

	// Parse test suites
	testSuites, _ := report["test_suites"].([]any)
	var failedTests []map[string]any
	var passedTests []map[string]any

	for _, suite := range testSuites {
		suiteMap, ok := suite.(map[string]any)
		if !ok {
			continue
		}

		suiteName, _ := suiteMap["name"].(string)
		testCases, _ := suiteMap["test_cases"].([]any)

		for _, tc := range testCases {
			tcMap, ok := tc.(map[string]any)
			if !ok {
				continue
			}

			status, _ := tcMap["status"].(string)
			testCase := map[string]any{
				"suite":          suiteName,
				"name":           tcMap["name"],
				"classname":      tcMap["classname"],
				"status":         status,
				"execution_time": tcMap["execution_time"],
			}

			if status == "failed" || status == "error" {
				if stackTrace, ok := tcMap["stack_trace"].(string); ok {
					// Truncate stack trace if too long
					if len(stackTrace) > 2000 {
						stackTrace = stackTrace[:2000] + "\n... (truncated)"
					}
					testCase["stack_trace"] = stackTrace
				}
				if sysOut, ok := tcMap["system_output"].(string); ok {
					if len(sysOut) > 1000 {
						sysOut = sysOut[:1000] + "\n... (truncated)"
					}
					testCase["system_output"] = sysOut
				}
				failedTests = append(failedTests, testCase)
			} else if status == "success" && includePassed {
				passedTests = append(passedTests, testCase)
			}
		}
	}

	// Limit failed tests
	if len(failedTests) > maxFailures {
		result["failed_tests_truncated"] = true
		failedTests = failedTests[:maxFailures]
	}
	if len(failedTests) > 0 {
		result["failed_tests"] = failedTests
	}
	if includePassed && len(passedTests) > 0 {
		result["passed_tests"] = passedTests
	}

	return result, nil
}

func (g *gitlabServer) buildPipelineResult(project string, pipelineID int, pipeline map[string]any, status string, pollCount int, timedOut bool) map[string]any {
	result := map[string]any{
		"ok":          !timedOut,
		"project":     project,
		"pipeline_id": pipelineID,
		"status":      status,
		"poll_count":  pollCount,
		"timed_out":   timedOut,
	}

	if pipeline != nil {
		result["pipeline"] = pipeline
	}

	return result
}

func (g *gitlabServer) getFailedJobLogs(ctx context.Context, project string, pipelineID int, tailLines int) []map[string]any {
	// Get failed jobs
	path := fmt.Sprintf("/projects/%s/pipelines/%d/jobs?scope=failed&per_page=10", encodeProject(project), pipelineID)
	jobs, err := g.requestList(ctx, path)
	if err != nil || len(jobs) == 0 {
		return nil
	}

	var logs []map[string]any
	for _, job := range jobs {
		jobMap, ok := job.(map[string]any)
		if !ok {
			continue
		}

		jobID, ok := jobMap["id"].(float64)
		if !ok {
			continue
		}

		logPath := fmt.Sprintf("/projects/%s/jobs/%d/trace", encodeProject(project), int(jobID))
		logData, _, err := g.requestRaw(ctx, "GET", logPath, map[string]string{"Accept": "text/plain"})
		if err != nil {
			continue
		}

		lines := strings.Split(string(logData), "\n")
		if len(lines) > tailLines {
			lines = lines[len(lines)-tailLines:]
		}

		logs = append(logs, map[string]any{
			"job_id":         int(jobID),
			"job_name":       jobMap["name"],
			"stage":          jobMap["stage"],
			"failure_reason": jobMap["failure_reason"],
			"tail_lines":     strings.Join(lines, "\n"),
		})
	}

	return logs
}

func isTerminalPipelineStatus(status string) bool {
	switch status {
	case "success", "failed", "canceled", "skipped", "manual":
		return true
	}
	return false
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	if ae, ok := err.(*apiError); ok {
		// 5xx errors are transient
		return ae.StatusCode >= 500 && ae.StatusCode < 600
	}
	// Network errors could be transient
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "temporary failure")
}

func isTextContent(contentType string, data []byte) bool {
	if strings.HasPrefix(contentType, "text/") {
		return true
	}
	if strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "yaml") {
		return true
	}
	// Check first 512 bytes for text
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	for i := 0; i < checkLen; i++ {
		b := data[i]
		// Allow printable ASCII, tabs, newlines, carriage returns
		if b < 32 && b != '\t' && b != '\n' && b != '\r' {
			return false
		}
		if b > 126 && b < 160 {
			return false
		}
	}
	return true
}

func encodeBase64(data []byte) string {
	const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var result strings.Builder
	result.Grow((len(data)*4 + 2) / 3)

	for i := 0; i < len(data); i += 3 {
		var n uint32
		remaining := len(data) - i

		n = uint32(data[i]) << 16
		if remaining > 1 {
			n |= uint32(data[i+1]) << 8
		}
		if remaining > 2 {
			n |= uint32(data[i+2])
		}

		result.WriteByte(base64Chars[(n>>18)&0x3f])
		result.WriteByte(base64Chars[(n>>12)&0x3f])
		if remaining > 1 {
			result.WriteByte(base64Chars[(n>>6)&0x3f])
		} else {
			result.WriteByte('=')
		}
		if remaining > 2 {
			result.WriteByte(base64Chars[n&0x3f])
		} else {
			result.WriteByte('=')
		}
	}

	return result.String()
}
