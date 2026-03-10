// Pipeline and job operation handlers for mcp-gitlab
package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"

	"go.opentelemetry.io/otel/trace"
)

func registerPipelineTools(server *mcp.Server, gl *gitlabServer, tracer trace.Tracer) {
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
	}, mcpotel.TracedToolHandler(tracer, "list_pipelines", gl.handleListPipelines))

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
	}, mcpotel.TracedToolHandler(tracer, "get_pipeline", gl.handleGetPipeline))

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
	}, mcpotel.TracedToolHandler(tracer, "create_pipeline", gl.handleCreatePipeline))

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
	}, mcpotel.TracedToolHandler(tracer, "cancel_pipeline", gl.handleCancelPipeline))

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
	}, mcpotel.TracedToolHandler(tracer, "retry_pipeline", gl.handleRetryPipeline))

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
	}, mcpotel.TracedToolHandler(tracer, "list_pipeline_jobs", gl.handleListPipelineJobs))

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
	}, mcpotel.TracedToolHandler(tracer, "get_job", gl.handleGetJob))

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
	}, mcpotel.TracedToolHandler(tracer, "get_job_trace", gl.handleGetJobTrace))

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
	}, mcpotel.TracedToolHandler(tracer, "retry_job", gl.handleRetryJob))

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
	}, mcpotel.TracedToolHandler(tracer, "play_job", gl.handlePlayJob))

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
	}, mcpotel.TracedToolHandler(tracer, "cancel_job", gl.handleCancelJob))

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
	}, mcpotel.TracedToolHandler(tracer, "pipeline_summary", gl.handlePipelineSummary))

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
	}, mcpotel.TracedToolHandler(tracer, "get_test_report", gl.handleGetTestReport))

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
	}, mcpotel.TracedToolHandler(tracer, "get_artifacts", gl.handleGetArtifacts))

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
					"description": "Maximum time to wait (default: 55, max: 600). When omitted, the handler also trims to the caller's remaining deadline budget.",
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
	}, mcpotel.TracedToolHandler(tracer, "poll_pipeline", gl.handlePollPipeline))
}

func (g *gitlabServer) handleListPipelines(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	ref := v.String("ref", "")
	status := v.String("status", "")
	perPage := normalizePerPage(v.Int("per_page", 20), 20)
	page := normalizePage(v.Int("page", 1))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	q := url.Values{}
	q.Set("per_page", fmt.Sprintf("%d", perPage))
	q.Set("page", fmt.Sprintf("%d", page))
	if ref != "" {
		q.Set("ref", ref)
	}
	if status != "" {
		q.Set("status", status)
	}
	path := fmt.Sprintf("/projects/%s/pipelines?%s", encodeProject(project), q.Encode())

	pipelines, meta, err := g.requestListWithMeta(ctx, path)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(map[string]any{"pipelines": pipelines, "count": len(pipelines), "pagination": meta})
}

func (g *gitlabServer) handleGetPipeline(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	pipelineID := v.RequiredInt("pipeline_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("pipeline_id", pipelineID); errResult != nil {
		return errResult, nil
	}

	path := fmt.Sprintf("/projects/%s/pipelines/%d", encodeProject(project), pipelineID)
	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleCreatePipeline(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	ref := v.Required("ref")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
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
	v := validate.NewArgs(args)
	project := v.Required("project")
	pipelineID := v.RequiredInt("pipeline_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("pipeline_id", pipelineID); errResult != nil {
		return errResult, nil
	}

	path := fmt.Sprintf("/projects/%s/pipelines/%d/cancel", encodeProject(project), pipelineID)
	result, err := g.request(ctx, "POST", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleRetryPipeline(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	pipelineID := v.RequiredInt("pipeline_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("pipeline_id", pipelineID); errResult != nil {
		return errResult, nil
	}

	path := fmt.Sprintf("/projects/%s/pipelines/%d/retry", encodeProject(project), pipelineID)
	result, err := g.request(ctx, "POST", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleListPipelineJobs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	pipelineID := v.RequiredInt("pipeline_id")
	scope := v.String("scope", "")
	perPage := normalizePerPage(v.Int("per_page", 100), 100)
	page := normalizePage(v.Int("page", 1))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("pipeline_id", pipelineID); errResult != nil {
		return errResult, nil
	}

	q := url.Values{}
	q.Set("per_page", fmt.Sprintf("%d", perPage))
	q.Set("page", fmt.Sprintf("%d", page))
	if scope != "" {
		q.Set("scope", scope)
	}

	path := fmt.Sprintf("/projects/%s/pipelines/%d/jobs?%s", encodeProject(project), pipelineID, q.Encode())
	jobs, meta, err := g.requestListWithMeta(ctx, path)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(map[string]any{"jobs": jobs, "count": len(jobs), "pagination": meta})
}

func (g *gitlabServer) handleGetJob(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	jobID := v.RequiredInt("job_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("job_id", jobID); errResult != nil {
		return errResult, nil
	}

	path := fmt.Sprintf("/projects/%s/jobs/%d", encodeProject(project), jobID)
	job, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(job)
}

func (g *gitlabServer) handleGetJobTrace(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	jobID := v.RequiredInt("job_id")
	tailLines := v.Int("tail_lines", 200)
	maxBytes := v.Int("max_bytes", 200_000)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("job_id", jobID); errResult != nil {
		return errResult, nil
	}

	if tailLines <= 0 {
		tailLines = 200
	}
	if maxBytes <= 0 {
		maxBytes = 200_000
	}

	trace, contentType, truncated, err := g.fetchJobTraceTail(ctx, project, jobID, tailLines, maxBytes)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"project":      project,
		"job_id":       jobID,
		"content_type": contentType,
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
	v := validate.NewArgs(args)
	project := v.Required("project")
	jobID := v.RequiredInt("job_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("job_id", jobID); errResult != nil {
		return errResult, nil
	}

	path := fmt.Sprintf("/projects/%s/jobs/%d/retry", encodeProject(project), jobID)
	job, err := g.request(ctx, "POST", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(job)
}

func (g *gitlabServer) handlePlayJob(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	jobID := v.RequiredInt("job_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("job_id", jobID); errResult != nil {
		return errResult, nil
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
	v := validate.NewArgs(args)
	project := v.Required("project")
	jobID := v.RequiredInt("job_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("job_id", jobID); errResult != nil {
		return errResult, nil
	}

	path := fmt.Sprintf("/projects/%s/jobs/%d/cancel", encodeProject(project), jobID)
	job, err := g.request(ctx, "POST", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(job)
}
