// mcp-github-actions is a GitHub Actions MCP server for workflow management.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/strutil"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.0.0"

type actionsServer struct {
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

	srv := &actionsServer{
		token:      token,
		httpClient: httpclient.NewDefault(),
	}

	logger.Info("starting server", "name", "mcp-github-actions", "version", version)

	server := mcp.NewServer("mcp-github-actions", version)
	server.SetInstructions("GitHub Actions MCP server. Manage workflows, runs, and jobs. Requires GITHUB_TOKEN or GITHUB_PERSONAL_ACCESS_TOKEN.")

	// list_workflows
	server.AddTool(mcp.Tool{
		Name:        "list_workflows",
		Description: "List workflows in a repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{
					"type":        "string",
					"description": "Repository owner (user or org)",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100, default 30)",
				},
			},
			Required: []string{"owner", "repo"},
		},
	}, srv.handleListWorkflows)

	// get_workflow
	server.AddTool(mcp.Tool{
		Name:        "get_workflow",
		Description: "Get a specific workflow by ID or filename",
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
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Workflow ID or filename (e.g., 'ci.yml')",
				},
			},
			Required: []string{"owner", "repo", "workflow_id"},
		},
	}, srv.handleGetWorkflow)

	// list_workflow_runs
	server.AddTool(mcp.Tool{
		Name:        "list_workflow_runs",
		Description: "List workflow runs for a repository or specific workflow",
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
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Optional workflow ID or filename to filter by",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Filter by branch name",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "Filter by status: queued, in_progress, completed",
				},
				"conclusion": map[string]any{
					"type":        "string",
					"description": "Filter by conclusion: success, failure, cancelled, skipped",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100, default 30)",
				},
			},
			Required: []string{"owner", "repo"},
		},
	}, srv.handleListWorkflowRuns)

	// get_workflow_run
	server.AddTool(mcp.Tool{
		Name:        "get_workflow_run",
		Description: "Get details of a specific workflow run",
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
				"run_id": map[string]any{
					"type":        "integer",
					"description": "Workflow run ID",
				},
			},
			Required: []string{"owner", "repo", "run_id"},
		},
	}, srv.handleGetWorkflowRun)

	// trigger_workflow
	server.AddTool(mcp.Tool{
		Name:        "trigger_workflow",
		Description: "Trigger a workflow_dispatch event to run a workflow",
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
				"workflow_id": map[string]any{
					"type":        "string",
					"description": "Workflow ID or filename",
				},
				"ref": map[string]any{
					"type":        "string",
					"description": "Git reference (branch or tag) to run the workflow on",
				},
				"inputs": map[string]any{
					"type":        "object",
					"description": "Input parameters for the workflow",
				},
			},
			Required: []string{"owner", "repo", "workflow_id", "ref"},
		},
	}, srv.handleTriggerWorkflow)

	// cancel_workflow_run
	server.AddTool(mcp.Tool{
		Name:        "cancel_workflow_run",
		Description: "Cancel a workflow run",
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
				"run_id": map[string]any{
					"type":        "integer",
					"description": "Workflow run ID to cancel",
				},
			},
			Required: []string{"owner", "repo", "run_id"},
		},
	}, srv.handleCancelWorkflowRun)

	// rerun_workflow
	server.AddTool(mcp.Tool{
		Name:        "rerun_workflow",
		Description: "Re-run a workflow",
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
				"run_id": map[string]any{
					"type":        "integer",
					"description": "Workflow run ID to re-run",
				},
				"failed_only": map[string]any{
					"type":        "boolean",
					"description": "If true, only re-run failed jobs",
				},
			},
			Required: []string{"owner", "repo", "run_id"},
		},
	}, srv.handleRerunWorkflow)

	// list_workflow_jobs
	server.AddTool(mcp.Tool{
		Name:        "list_workflow_jobs",
		Description: "List jobs for a workflow run",
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
				"run_id": map[string]any{
					"type":        "integer",
					"description": "Workflow run ID",
				},
				"filter": map[string]any{
					"type":        "string",
					"description": "Filter jobs: latest (default) or all",
				},
			},
			Required: []string{"owner", "repo", "run_id"},
		},
	}, srv.handleListWorkflowJobs)

	// get_job_logs
	server.AddTool(mcp.Tool{
		Name:        "get_job_logs",
		Description: "Get logs for a workflow job",
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
				"job_id": map[string]any{
					"type":        "integer",
					"description": "Job ID",
				},
				"tail_lines": map[string]any{
					"type":        "integer",
					"description": "Return only the last N lines (default: all)",
				},
			},
			Required: []string{"owner", "repo", "job_id"},
		},
	}, srv.handleGetJobLogs)

	// list_artifacts
	server.AddTool(mcp.Tool{
		Name:        "list_artifacts",
		Description: "List artifacts for a workflow run",
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
				"run_id": map[string]any{
					"type":        "integer",
					"description": "Workflow run ID",
				},
			},
			Required: []string{"owner", "repo", "run_id"},
		},
	}, srv.handleListArtifacts)

	return server.Run(ctx)
}

func (s *actionsServer) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	url := "https://api.github.com" + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.httpClient.HTTP().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, mcperror.APIError("GitHub", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func (s *actionsServer) handleListWorkflows(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	perPage := v.Int("per_page", 30)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/repos/%s/%s/actions/workflows?per_page=%d", owner, repo, perPage)
	data, err := s.request(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		TotalCount int `json:"total_count"`
		Workflows  []struct {
			ID        int    `json:"id"`
			Name      string `json:"name"`
			Path      string `json:"path"`
			State     string `json:"state"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("total_count: %d\n\n", result.TotalCount))
	for _, w := range result.Workflows {
		sb.WriteString(fmt.Sprintf("- %s (ID: %d)\n  path: %s\n  state: %s\n  updated: %s\n\n",
			w.Name, w.ID, w.Path, w.State, w.UpdatedAt))
	}

	return mcp.TextResult(sb.String()), nil
}

func (s *actionsServer) handleGetWorkflow(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	workflowID := v.Required("workflow_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/repos/%s/%s/actions/workflows/%s", owner, repo, workflowID)
	data, err := s.request(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	formatted, _ := json.MarshalIndent(result, "", "  ")
	return mcp.TextResult(string(formatted)), nil
}

func (s *actionsServer) handleListWorkflowRuns(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	workflowID := v.String("workflow_id", "")
	branch := v.String("branch", "")
	status := v.String("status", "")
	conclusion := v.String("conclusion", "")
	perPage := v.Int("per_page", 30)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var path string
	if workflowID != "" {
		path = fmt.Sprintf("/repos/%s/%s/actions/workflows/%s/runs?per_page=%d", owner, repo, workflowID, perPage)
	} else {
		path = fmt.Sprintf("/repos/%s/%s/actions/runs?per_page=%d", owner, repo, perPage)
	}

	if branch != "" {
		path += "&branch=" + branch
	}
	if status != "" {
		path += "&status=" + status
	}
	if conclusion != "" {
		path += "&conclusion=" + conclusion
	}

	data, err := s.request(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		TotalCount   int `json:"total_count"`
		WorkflowRuns []struct {
			ID           int64  `json:"id"`
			Name         string `json:"name"`
			HeadBranch   string `json:"head_branch"`
			HeadSha      string `json:"head_sha"`
			Status       string `json:"status"`
			Conclusion   string `json:"conclusion"`
			Event        string `json:"event"`
			RunNumber    int    `json:"run_number"`
			RunAttempt   int    `json:"run_attempt"`
			CreatedAt    string `json:"created_at"`
			UpdatedAt    string `json:"updated_at"`
			HTMLURL      string `json:"html_url"`
			DisplayTitle string `json:"display_title"`
		} `json:"workflow_runs"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("total_count: %d\n\n", result.TotalCount))
	for _, r := range result.WorkflowRuns {
		conclusionStr := r.Conclusion
		if conclusionStr == "" {
			conclusionStr = r.Status
		}
		sb.WriteString(fmt.Sprintf("- #%d %s [%s]\n  branch: %s | sha: %s\n  event: %s | attempt: %d\n  url: %s\n  updated: %s\n\n",
			r.RunNumber, r.DisplayTitle, conclusionStr,
			r.HeadBranch, r.HeadSha[:7],
			r.Event, r.RunAttempt,
			r.HTMLURL, r.UpdatedAt))
	}

	return mcp.TextResult(sb.String()), nil
}

func (s *actionsServer) handleGetWorkflowRun(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	runID := int64(v.RequiredInt("run_id"))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d", owner, repo, runID)
	data, err := s.request(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	// Format key fields
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Run #%v: %v\n", result["run_number"], result["display_title"]))
	sb.WriteString(fmt.Sprintf("Status: %v | Conclusion: %v\n", result["status"], result["conclusion"]))
	sb.WriteString(fmt.Sprintf("Branch: %v | SHA: %v\n", result["head_branch"], strutil.TruncateNoEllipsis(fmt.Sprintf("%v", result["head_sha"]), 7)))
	sb.WriteString(fmt.Sprintf("Event: %v | Attempt: %v\n", result["event"], result["run_attempt"]))
	sb.WriteString(fmt.Sprintf("Created: %v\n", result["created_at"]))
	sb.WriteString(fmt.Sprintf("Updated: %v\n", result["updated_at"]))
	sb.WriteString(fmt.Sprintf("URL: %v\n", result["html_url"]))

	return mcp.TextResult(sb.String()), nil
}

func (s *actionsServer) handleTriggerWorkflow(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	workflowID := v.Required("workflow_id")
	ref := v.Required("ref")
	inputs, _ := args["inputs"].(map[string]any)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{"ref": ref}
	if inputs != nil {
		body["inputs"] = inputs
	}

	path := fmt.Sprintf("/repos/%s/%s/actions/workflows/%s/dispatches", owner, repo, workflowID)
	_, err := s.request(ctx, "POST", path, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.TextResult(fmt.Sprintf("Workflow %s triggered on %s", workflowID, ref)), nil
}

func (s *actionsServer) handleCancelWorkflowRun(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	runID := int64(v.RequiredInt("run_id"))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/cancel", owner, repo, runID)
	_, err := s.request(ctx, "POST", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.TextResult(fmt.Sprintf("Workflow run %d cancelled", runID)), nil
}

func (s *actionsServer) handleRerunWorkflow(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	runID := int64(v.RequiredInt("run_id"))
	failedOnly := v.Bool("failed_only", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var path string
	if failedOnly {
		path = fmt.Sprintf("/repos/%s/%s/actions/runs/%d/rerun-failed-jobs", owner, repo, runID)
	} else {
		path = fmt.Sprintf("/repos/%s/%s/actions/runs/%d/rerun", owner, repo, runID)
	}

	_, err := s.request(ctx, "POST", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	msg := fmt.Sprintf("Workflow run %d re-triggered", runID)
	if failedOnly {
		msg += " (failed jobs only)"
	}
	return mcp.TextResult(msg), nil
}

func (s *actionsServer) handleListWorkflowJobs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	runID := int64(v.RequiredInt("run_id"))
	filter := v.String("filter", "latest")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/jobs?filter=%s", owner, repo, runID, filter)
	data, err := s.request(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		TotalCount int `json:"total_count"`
		Jobs       []struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Status      string `json:"status"`
			Conclusion  string `json:"conclusion"`
			StartedAt   string `json:"started_at"`
			CompletedAt string `json:"completed_at"`
			HTMLURL     string `json:"html_url"`
			Steps       []struct {
				Name       string `json:"name"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				Number     int    `json:"number"`
			} `json:"steps"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("total_jobs: %d\n\n", result.TotalCount))
	for _, j := range result.Jobs {
		conclusionStr := j.Conclusion
		if conclusionStr == "" {
			conclusionStr = j.Status
		}
		sb.WriteString(fmt.Sprintf("- %s (ID: %d) [%s]\n", j.Name, j.ID, conclusionStr))
		sb.WriteString(fmt.Sprintf("  started: %s | completed: %s\n", j.StartedAt, j.CompletedAt))

		if len(j.Steps) > 0 {
			sb.WriteString("  steps:\n")
			for _, step := range j.Steps {
				stepConclusion := step.Conclusion
				if stepConclusion == "" {
					stepConclusion = step.Status
				}
				sb.WriteString(fmt.Sprintf("    %d. %s [%s]\n", step.Number, step.Name, stepConclusion))
			}
		}
		sb.WriteString("\n")
	}

	return mcp.TextResult(sb.String()), nil
}

func (s *actionsServer) handleGetJobLogs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	jobID := int64(v.RequiredInt("job_id"))
	tailLines := v.Int("tail_lines", 0)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/actions/jobs/%d/logs", owner, repo, jobID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("create request: %w", err)), nil
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.httpClient.HTTP().Do(req)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("request failed: %w", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return mcp.ErrorResult(mcperror.APIError("GitHub Actions", resp.StatusCode, string(body))), nil
	}

	logs, err := io.ReadAll(resp.Body)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("read logs: %w", err)), nil
	}

	logStr := string(logs)
	if tailLines > 0 {
		lines := strings.Split(logStr, "\n")
		if len(lines) > tailLines {
			lines = lines[len(lines)-tailLines:]
		}
		logStr = strings.Join(lines, "\n")
	}

	// Limit response size
	if len(logStr) > 100000 {
		logStr = logStr[len(logStr)-100000:]
		logStr = "[truncated]\n" + logStr
	}

	return mcp.TextResult(logStr), nil
}

func (s *actionsServer) handleListArtifacts(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owner := v.Required("owner")
	repo := v.Required("repo")
	runID := int64(v.RequiredInt("run_id"))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/repos/%s/%s/actions/runs/%d/artifacts", owner, repo, runID)
	data, err := s.request(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		TotalCount int `json:"total_count"`
		Artifacts  []struct {
			ID                 int64  `json:"id"`
			Name               string `json:"name"`
			SizeInBytes        int64  `json:"size_in_bytes"`
			Expired            bool   `json:"expired"`
			CreatedAt          string `json:"created_at"`
			ExpiresAt          string `json:"expires_at"`
			ArchiveDownloadURL string `json:"archive_download_url"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("total_artifacts: %d\n\n", result.TotalCount))
	for _, a := range result.Artifacts {
		expiredStr := ""
		if a.Expired {
			expiredStr = " [EXPIRED]"
		}
		sb.WriteString(fmt.Sprintf("- %s (ID: %d)%s\n  size: %d bytes\n  created: %s\n  expires: %s\n\n",
			a.Name, a.ID, expiredStr, a.SizeInBytes, a.CreatedAt, a.ExpiresAt))
	}

	return mcp.TextResult(sb.String()), nil
}
