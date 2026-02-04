// mcp-gitlab is a fast GitLab MCP server written in Go.
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

type gitlabServer struct {
	token      string
	apiURL     string
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

	token := env.StringWithFallbacks("GITLAB_PERSONAL_ACCESS_TOKEN", "GITLAB_TOKEN")
	apiURL := strings.TrimSuffix(env.String("GITLAB_API_URL", "https://gitlab.com/api/v4"), "/")

	gl := &gitlabServer{
		token:      token,
		apiURL:     apiURL,
		httpClient: httpclient.NewDefault(),
	}

	logger.Info("starting server", "name", "mcp-gitlab", "version", version, "api_url", apiURL)

	server := mcp.NewServer("mcp-gitlab", version)
	server.SetInstructions("Fast Go-native GitLab MCP server. Supports projects, issues, merge requests, and more.")

	// Register all tools
	registerRepositoryTools(server, gl)
	registerIssueTools(server, gl)
	registerMergeRequestTools(server, gl)
	registerPipelineTools(server, gl)

	// verify_token
	server.AddTool(mcp.Tool{
		Name:        "verify_token",
		Description: "Verify GitLab API token status and scopes",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, gl.handleVerifyToken)

	return server.Run(ctx)
}

// Tool registration functions

func registerRepositoryTools(server *mcp.Server, gl *gitlabServer) {
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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
				},
			},
		},
	}, gl.handleListProjects)
}

func registerIssueTools(server *mcp.Server, gl *gitlabServer) {
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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
				},
			},
			Required: []string{"project"},
		},
	}, gl.handleListIssues)
}

func registerMergeRequestTools(server *mcp.Server, gl *gitlabServer) {
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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
				},
			},
			Required: []string{"project"},
		},
	}, gl.handleListMergeRequests)
}

func registerPipelineTools(server *mcp.Server, gl *gitlabServer) {
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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
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
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
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
}

// Token verification handler

func (g *gitlabServer) handleVerifyToken(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(g.token) == "" {
		return nil, mcperror.NotConfigured("GITLAB_PERSONAL_ACCESS_TOKEN", "set via environment variable or GITLAB_TOKEN")
	}
	if strings.Contains(g.token, "${") {
		return nil, mcperror.InvalidParam("token", "appears to be unexpanded - check your Loom secrets/keychain resolution")
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
		if mcpErr, ok := err.(*mcperror.Error); ok && mcpErr.Code == mcperror.CodeNotFound {
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

// HTTP request helpers

func (g *gitlabServer) request(ctx context.Context, method, path string, body any) (map[string]any, error) {
	reqURL := g.apiURL + path

	var reqBodyBytes []byte
	if body != nil {
		var err error
		reqBodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	headers := map[string]string{
		"Accept": "application/json",
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

func (g *gitlabServer) requestList(ctx context.Context, path string) ([]any, error) {
	items, _, err := g.requestListWithMeta(ctx, path)
	return items, err
}

func (g *gitlabServer) requestListWithMeta(ctx context.Context, path string) ([]any, map[string]any, error) {
	reqURL := g.apiURL + path

	respBody, respHeaders, err := g.doRequest(ctx, "GET", reqURL, nil, map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, nil, err
	}

	var result []any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, nil, fmt.Errorf("parse response: %w", err)
	}

	return result, parsePaginationHeaders(respHeaders), nil
}

func (g *gitlabServer) doRequest(ctx context.Context, method, reqURL string, body []byte, headers map[string]string) ([]byte, http.Header, error) {
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

		req.Header.Set("Accept", "*/*")
		req.Header.Set("User-Agent", "mcp-gitlab/"+version)
		for k, v := range headers {
			if k != "" && v != "" {
				req.Header.Set(k, v)
			}
		}
		if g.token != "" {
			req.Header.Set("PRIVATE-TOKEN", g.token)
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
			delay := parseRetryAfter(respHeaders.Get("Retry-After"))
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
			return nil, respHeaders, mcperror.APIError("GitLab", resp.StatusCode, strutil.TruncateNoEllipsis(string(respBody), maxErrorBodyBytes))
		}

		return respBody, respHeaders, nil
	}

	return nil, nil, fmt.Errorf("request failed after retries")
}

func (g *gitlabServer) doRequestLimited(ctx context.Context, method, reqURL string, body []byte, headers map[string]string, maxBytes int) ([]byte, *http.Response, bool, error) {
	const (
		maxAttempts       = 3
		maxErrorBodyBytes = 8192
		maxRetryDelay     = 10 * time.Second
	)

	if maxBytes <= 0 {
		return nil, nil, false, fmt.Errorf("maxBytes must be > 0")
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var reqBody io.Reader
		if len(body) > 0 {
			reqBody = bytes.NewReader(body)
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
		if err != nil {
			return nil, nil, false, err
		}

		req.Header.Set("Accept", "*/*")
		req.Header.Set("User-Agent", "mcp-gitlab/"+version)
		for k, v := range headers {
			if k != "" && v != "" {
				req.Header.Set(k, v)
			}
		}
		if g.token != "" {
			req.Header.Set("PRIVATE-TOKEN", g.token)
		}

		resp, err := g.httpClient.HTTP().Do(req)
		if err != nil {
			if attempt < maxAttempts-1 && isTransientError(err) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, nil, false, sleepErr
				}
				continue
			}
			return nil, nil, false, err
		}

		limited, readErr := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes+1)))
		_ = resp.Body.Close()
		if readErr != nil {
			if attempt < maxAttempts-1 && isTransientError(readErr) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, resp, false, sleepErr
				}
				continue
			}
			return nil, resp, false, readErr
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
				return nil, resp, false, sleepErr
			}
			continue
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 && attempt < maxAttempts-1 {
			if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
				return nil, resp, false, sleepErr
			}
			continue
		}

		truncated := len(limited) > maxBytes
		if truncated {
			limited = limited[:maxBytes]
		}

		if resp.StatusCode >= 400 {
			return nil, resp, truncated, mcperror.APIError("GitLab", resp.StatusCode, strutil.TruncateNoEllipsis(string(limited), maxErrorBodyBytes))
		}

		return limited, resp, truncated, nil
	}

	return nil, nil, false, fmt.Errorf("request failed after retries")
}

func (g *gitlabServer) doRequestTail(ctx context.Context, method, reqURL string, headers map[string]string, maxBytes int) ([]byte, *http.Response, int, error) {
	const (
		maxAttempts   = 3
		maxRetryDelay = 10 * time.Second
	)

	if maxBytes <= 0 {
		maxBytes = 200_000
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, reqURL, nil)
		if err != nil {
			return nil, nil, 0, err
		}

		req.Header.Set("Accept", "*/*")
		req.Header.Set("User-Agent", "mcp-gitlab/"+version)
		for k, v := range headers {
			if k != "" && v != "" {
				req.Header.Set(k, v)
			}
		}
		if g.token != "" {
			req.Header.Set("PRIVATE-TOKEN", g.token)
		}

		resp, err := g.httpClient.HTTP().Do(req)
		if err != nil {
			if attempt < maxAttempts-1 && isTransientError(err) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, nil, 0, sleepErr
				}
				continue
			}
			return nil, nil, 0, err
		}

		if resp.StatusCode == 429 && attempt < maxAttempts-1 {
			_ = resp.Body.Close()
			delay := parseRetryAfter(resp.Header.Get("Retry-After"))
			if delay <= 0 {
				delay = backoffDelay(attempt, maxRetryDelay)
			}
			if delay > maxRetryDelay {
				delay = maxRetryDelay
			}
			if sleepErr := poll.WaitWithContext(ctx, delay); sleepErr != nil {
				return nil, nil, 0, sleepErr
			}
			continue
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 && attempt < maxAttempts-1 {
			_ = resp.Body.Close()
			if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
				return nil, nil, 0, sleepErr
			}
			continue
		}

		tail, totalRead, readErr := readTail(resp.Body, maxBytes)
		_ = resp.Body.Close()
		if readErr != nil {
			if attempt < maxAttempts-1 && isTransientError(readErr) {
				if sleepErr := poll.WaitWithContext(ctx, backoffDelay(attempt, maxRetryDelay)); sleepErr != nil {
					return nil, resp, totalRead, sleepErr
				}
				continue
			}
			return nil, resp, totalRead, readErr
		}

		if resp.StatusCode >= 400 {
			return nil, resp, totalRead, mcperror.APIError("GitLab", resp.StatusCode, strutil.TruncateNoEllipsis(string(tail), 8192))
		}

		return tail, resp, totalRead, nil
	}

	return nil, nil, 0, fmt.Errorf("request failed after retries")
}

func (g *gitlabServer) fetchJobTraceTail(ctx context.Context, project string, jobID int, tailLines int, maxBytes int) (string, string, bool, error) {
	if tailLines <= 0 {
		tailLines = 200
	}
	if maxBytes <= 0 {
		maxBytes = 200_000
	}

	path := fmt.Sprintf("/projects/%s/jobs/%d/trace", encodeProject(project), jobID)
	reqURL := g.apiURL + path

	b, resp, totalRead, err := g.doRequestTail(ctx, "GET", reqURL, map[string]string{"Accept": "text/plain"}, maxBytes) //nolint:bodyclose // body closed inside doRequestTail
	if err != nil {
		return "", "", false, err
	}

	contentType := ""
	if resp != nil {
		contentType = resp.Header.Get("Content-Type")
	}

	truncated := totalRead > maxBytes
	trace := string(b)
	lines := strings.Split(trace, "\n")
	if tailLines > 0 && len(lines) > tailLines {
		truncated = true
		lines = lines[len(lines)-tailLines:]
		trace = strings.Join(lines, "\n")
	}

	return trace, contentType, truncated, nil
}

// Utility functions

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

func parsePaginationHeaders(headers http.Header) map[string]any {
	if headers == nil {
		return nil
	}
	out := map[string]any{}
	for _, kv := range []struct {
		key string
		dst string
	}{
		{"X-Page", "page"},
		{"X-Per-Page", "per_page"},
		{"X-Next-Page", "next_page"},
		{"X-Prev-Page", "prev_page"},
		{"X-Total-Pages", "total_pages"},
		{"X-Total", "total"},
	} {
		v := strings.TrimSpace(headers.Get(kv.key))
		if v == "" {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil {
			out[kv.dst] = n
		} else {
			out[kv.dst] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func readTail(r io.Reader, maxBytes int) ([]byte, int, error) {
	if maxBytes <= 0 {
		return nil, 0, fmt.Errorf("maxBytes must be > 0")
	}

	ring := make([]byte, maxBytes)
	buf := make([]byte, 32*1024)
	pos := 0
	filled := 0
	total := 0

	for {
		n, err := r.Read(buf)
		if n > 0 {
			total += n
			if n >= maxBytes {
				copy(ring, buf[n-maxBytes:n])
				pos = 0
				filled = maxBytes
			} else {
				end := pos + n
				if end <= maxBytes {
					copy(ring[pos:end], buf[:n])
				} else {
					first := maxBytes - pos
					copy(ring[pos:], buf[:first])
					copy(ring[:end-maxBytes], buf[first:n])
				}
				pos = end % maxBytes
				if filled < maxBytes {
					filled += n
					if filled > maxBytes {
						filled = maxBytes
					}
				}
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, total, err
		}
	}

	if filled == 0 {
		return []byte{}, total, nil
	}

	if filled < maxBytes {
		return ring[:filled], total, nil
	}

	// pos is the start of the oldest data.
	out := make([]byte, 0, maxBytes)
	out = append(out, ring[pos:]...)
	out = append(out, ring[:pos]...)
	return out, total, nil
}

func encodeProject(project string) string {
	return url.PathEscape(project)
}

func normalizePerPage(perPage int, defaultVal int) int {
	return validate.NormalizePerPage(perPage, defaultVal, 100)
}

func normalizePage(page int) int {
	return validate.NormalizePage(page)
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	// Server errors (5xx) are transient
	if mcperror.IsServerError(err) {
		return true
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

// Ensure validate package is used (referenced by handler files)
var _ = validate.NewArgs
