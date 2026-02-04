// Pipeline and job operation handlers for mcp-gitlab
package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/validate"
)

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

	path := fmt.Sprintf("/projects/%s/jobs/%d/cancel", encodeProject(project), jobID)
	job, err := g.request(ctx, "POST", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(job)
}

// Pipeline polling & summary handlers

func (g *gitlabServer) handlePipelineSummary(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	pipelineID := v.RequiredInt("pipeline_id")
	includeTestReport := v.Bool("include_test_report", true)
	includeFailedJobLogs := v.Bool("include_failed_job_logs", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
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
	v := validate.NewArgs(args)
	project := v.Required("project")
	pipelineID := v.RequiredInt("pipeline_id")
	includePassed := v.Bool("include_passed", false)
	maxFailures := v.Int("max_failures", 50)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
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
	v := validate.NewArgs(args)
	project := v.Required("project")
	jobID := v.RequiredInt("job_id")
	artifactPath := v.String("artifact_path", "")
	maxSizeBytes := v.Int("max_size_bytes", 1024*1024) // 1MB default

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
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
		data, resp, truncated, err := g.doRequestLimited(ctx, "GET", g.apiURL+path, nil, nil, maxSizeBytes) //nolint:bodyclose // body closed inside doRequestLimited
		if err != nil {
			return nil, err
		}

		result := map[string]any{
			"ok":            true,
			"project":       project,
			"job_id":        jobID,
			"artifact_path": artifactPath,
			"size_bytes":    len(data),
			"truncated":     truncated,
		}

		contentType := ""
		if resp != nil {
			contentType = resp.Header.Get("Content-Type")
		}
		result["content_type"] = contentType

		if truncated {
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
	v := validate.NewArgs(args)
	project := v.Required("project")
	pipelineID := v.RequiredInt("pipeline_id")
	timeoutSeconds := v.Int("timeout_seconds", 300)
	pollIntervalSeconds := v.Int("poll_interval_seconds", 5)
	includeJobLogs := v.Bool("include_job_logs", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
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

	// Create a reusable timer to avoid memory leaks from time.After in loops
	pollTimer := time.NewTimer(0)
	if !pollTimer.Stop() {
		<-pollTimer.C // Drain the channel if timer already fired
	}
	defer pollTimer.Stop()

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
			// Transient error - retry with context-aware sleep
			if isTransientError(err) && time.Now().Before(deadline) {
				pollTimer.Reset(time.Duration(pollIntervalSeconds) * time.Second)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-pollTimer.C:
					continue
				}
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

		// Use timer.Reset instead of time.After to avoid memory leaks
		pollTimer.Reset(time.Duration(interval) * time.Second)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pollTimer.C:
			// Continue polling
		}
	}
}

// Helper functions for pipeline operations

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
			trace, _, _, err := g.fetchJobTraceTail(ctx, project, int(jobID), 50, 200_000)
			if err != nil {
				continue
			}
			logsForJobs = append(logsForJobs, map[string]any{
				"job_id":     int(jobID),
				"job_name":   fj["name"],
				"tail_lines": trace,
			})
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
		if mcperror.IsNotFound(err) {
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

		trace, _, _, err := g.fetchJobTraceTail(ctx, project, int(jobID), tailLines, 200_000)
		if err != nil {
			continue
		}

		logs = append(logs, map[string]any{
			"job_id":         int(jobID),
			"job_name":       jobMap["name"],
			"stage":          jobMap["stage"],
			"failure_reason": jobMap["failure_reason"],
			"tail_lines":     trace,
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
