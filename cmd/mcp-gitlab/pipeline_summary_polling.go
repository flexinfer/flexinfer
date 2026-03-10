// Pipeline summary, test report, artifacts, and polling handlers for mcp-gitlab
package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/validate"
)

const (
	defaultPollPipelineTimeoutSeconds = 55
	maxPollPipelineTimeoutSeconds     = 600
	pollPipelineDeadlineBufferSeconds = 2
)

func (g *gitlabServer) handlePipelineSummary(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	pipelineID := v.RequiredInt("pipeline_id")
	includeTestReport := v.Bool("include_test_report", true)
	includeFailedJobLogs := v.Bool("include_failed_job_logs", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("pipeline_id", pipelineID); errResult != nil {
		return errResult, nil
	}

	// Fetch pipeline and jobs concurrently.
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

	jobSummary := g.summarizeJobs(jobsRes.data, includeFailedJobLogs, ctx, project)

	result := map[string]any{
		"ok":       true,
		"pipeline": pipelineRes.data,
		"jobs":     jobSummary,
	}

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
	if errResult := validatePositiveIntParam("pipeline_id", pipelineID); errResult != nil {
		return errResult, nil
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
	if errResult := validatePositiveIntParam("job_id", jobID); errResult != nil {
		return errResult, nil
	}

	// Cap at 10MB.
	if maxSizeBytes <= 0 {
		maxSizeBytes = 1024 * 1024
	}
	if maxSizeBytes > 10*1024*1024 {
		maxSizeBytes = 10 * 1024 * 1024
	}

	if artifactPath != "" {
		encodedArtifactPath := encodeArtifactPath(artifactPath)
		path := fmt.Sprintf("/projects/%s/jobs/%d/artifacts/%s", encodeProject(project), jobID, encodedArtifactPath)
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
			result["download_url"] = fmt.Sprintf("%s/projects/%s/jobs/%d/artifacts/%s", g.apiURL, encodeProject(project), jobID, encodedArtifactPath)
		} else {
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
	pollIntervalSeconds := v.Int("poll_interval_seconds", 5)
	includeJobLogs := v.Bool("include_job_logs", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("pipeline_id", pipelineID); errResult != nil {
		return errResult, nil
	}

	timeoutSeconds := resolvePollPipelineTimeoutSeconds(ctx, args, v)
	if pollIntervalSeconds < 2 {
		pollIntervalSeconds = 2
	}

	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	pollCount := 0

	var lastStatus string
	var lastPipeline map[string]any

	// Reusable timer to avoid time.After allocations in loops.
	pollTimer := time.NewTimer(0)
	if !pollTimer.Stop() {
		<-pollTimer.C
	}
	defer pollTimer.Stop()

	for {
		pollCount++

		if time.Now().After(deadline) {
			result := g.buildPipelineResult(project, pipelineID, lastPipeline, lastStatus, pollCount, true)
			if includeJobLogs && (lastStatus == "failed" || lastStatus == "canceled") {
				result["failed_job_logs"] = g.getFailedJobLogs(ctx, project, pipelineID, 50)
			}
			return mcp.JSONResult(result)
		}

		path := fmt.Sprintf("/projects/%s/pipelines/%d", encodeProject(project), pipelineID)
		pipeline, err := g.request(ctx, "GET", path, nil)
		if err != nil {
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

		if isTerminalPipelineStatus(status) {
			result := g.buildPipelineResult(project, pipelineID, pipeline, status, pollCount, false)
			if includeJobLogs && (status == "failed" || status == "canceled") {
				result["failed_job_logs"] = g.getFailedJobLogs(ctx, project, pipelineID, 50)
			}
			return mcp.JSONResult(result)
		}

		interval := pollIntervalSeconds
		if status == "pending" || status == "waiting_for_resource" {
			interval = 10
		}

		pollTimer.Reset(time.Duration(interval) * time.Second)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pollTimer.C:
		}
	}
}

func resolvePollPipelineTimeoutSeconds(ctx context.Context, args map[string]any, v *validate.Args) int {
	timeoutSeconds := v.Int("timeout_seconds", defaultPollPipelineTimeoutSeconds)
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultPollPipelineTimeoutSeconds
	}
	if timeoutSeconds > maxPollPipelineTimeoutSeconds {
		timeoutSeconds = maxPollPipelineTimeoutSeconds
	}

	if _, ok := args["timeout_seconds"]; ok {
		return timeoutSeconds
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		return timeoutSeconds
	}

	remainingSeconds := int(math.Ceil(time.Until(deadline).Seconds())) - pollPipelineDeadlineBufferSeconds
	if remainingSeconds < 1 {
		remainingSeconds = 1
	}
	if remainingSeconds < timeoutSeconds {
		timeoutSeconds = remainingSeconds
	}

	return timeoutSeconds
}

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
