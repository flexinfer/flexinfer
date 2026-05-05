// Pipeline and job operation handlers for mcp-gitlab
package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"golang.org/x/sync/errgroup"

	"github.com/crb2nu/loom/pkg/mcpscaffold"
	"github.com/crb2nu/loom/pkg/validate"
)

// listActivePipelinesMaxProjects caps the projects list to keep one batched
// call bounded; HUD callers cap at 20, so 100 leaves headroom for ad-hoc use.
const listActivePipelinesMaxProjects = 100

// listActivePipelinesConcurrency caps in-flight per-project HTTP requests
// inside one batched tool call. Replaces the prior client-side cap of 4 with
// a server-side cap of 8 (now serving all projects in one daemon lock).
const listActivePipelinesConcurrency = 8

func registerPipelineTools(srv *mcpscaffold.Server, gl *gitlabServer) {
	// list_pipelines
	srv.AddTracedTool(mcp.Tool{
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
	// list_active_pipelines (batched across N projects in a single tool call so
	// callers like the HUD pipeline monitor only acquire the daemon's per-server
	// call-lock once instead of N times)
	srv.AddTracedTool(mcp.Tool{
		Name: "list_active_pipelines",
		Description: "Batch-list active (running/pending by default) pipelines " +
			"across multiple projects in a single tool call. Each result is " +
			"tagged with its source project. Errors are returned per-project so " +
			"a slow or failing project does not abort the whole batch.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"projects": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Project IDs or URL-encoded paths (max 100).",
				},
				"statuses": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Pipeline statuses to fetch. Defaults to [\"running\",\"pending\"].",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per (project,status) page (max 100). Defaults to 20.",
				},
			},
			Required: []string{"projects"},
		},
	}, gl.handleListActivePipelines)
	// get_pipeline
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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
	}, gl.handlePollPipeline)
}

// handleListActivePipelines fans out per-project list_pipelines requests
// inside a single tool invocation. The HUD pipeline monitor previously made
// N×M calls (N projects × M statuses) which serialized through the daemon's
// per-server call lock and saturated it under typical fleets (20×2 = 40
// calls / 10s window vs ~2s/call → 80s of work in 10s, deadline-exceeded
// flap at 5s wait). This batched form replaces N×M lock acquisitions with 1.
func (g *gitlabServer) handleListActivePipelines(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	rawProjects, _ := args["projects"].([]any)
	if len(rawProjects) == 0 {
		return mcp.ErrorResult(fmt.Errorf("projects required (non-empty array)")), nil
	}
	if len(rawProjects) > listActivePipelinesMaxProjects {
		return mcp.ErrorResult(fmt.Errorf("projects cannot exceed %d entries (got %d)", listActivePipelinesMaxProjects, len(rawProjects))), nil
	}
	projects := make([]string, 0, len(rawProjects))
	seen := make(map[string]struct{}, len(rawProjects))
	for _, p := range rawProjects {
		s, ok := p.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		projects = append(projects, s)
	}
	if len(projects) == 0 {
		return mcp.ErrorResult(fmt.Errorf("projects must contain at least one non-empty string")), nil
	}

	statuses := []string{"running", "pending"}
	if rawStatuses, ok := args["statuses"].([]any); ok && len(rawStatuses) > 0 {
		statuses = statuses[:0]
		for _, s := range rawStatuses {
			if str, ok := s.(string); ok {
				str = strings.TrimSpace(str)
				if str != "" {
					statuses = append(statuses, str)
				}
			}
		}
		if len(statuses) == 0 {
			return mcp.ErrorResult(fmt.Errorf("statuses must contain at least one non-empty string when provided")), nil
		}
	}

	perPage := 20
	if rp, ok := args["per_page"].(float64); ok && rp > 0 {
		perPage = int(rp)
	}
	perPage = normalizePerPage(perPage, 20)

	g_, gctx := errgroup.WithContext(ctx)
	g_.SetLimit(listActivePipelinesConcurrency)

	var (
		mu        sync.Mutex
		pipelines = make([]any, 0, len(projects)*len(statuses)*perPage)
		errs      = make(map[string]string)
	)

	for _, project := range projects {
		for _, status := range statuses {
			project, status := project, status
			g_.Go(func() error {
				q := url.Values{}
				q.Set("per_page", fmt.Sprintf("%d", perPage))
				q.Set("status", status)
				path := fmt.Sprintf("/projects/%s/pipelines?%s", encodeProject(project), q.Encode())
				items, _, err := g.requestListWithMeta(gctx, path)
				if err != nil {
					mu.Lock()
					// Keep first error per project; downstream may want richer
					// detail later, but a single line is enough for triage.
					if _, exists := errs[project]; !exists {
						errs[project] = err.Error()
					}
					mu.Unlock()
					return nil
				}
				tagged := make([]any, 0, len(items))
				for _, item := range items {
					m, ok := item.(map[string]any)
					if !ok {
						continue
					}
					m["project"] = project
					tagged = append(tagged, m)
				}
				mu.Lock()
				pipelines = append(pipelines, tagged...)
				mu.Unlock()
				return nil
			})
		}
	}
	_ = g_.Wait()

	return mcp.JSONResult(map[string]any{
		"pipelines": pipelines,
		"count":     len(pipelines),
		"projects":  len(projects),
		"statuses":  statuses,
		"errors":    errs,
	})
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
