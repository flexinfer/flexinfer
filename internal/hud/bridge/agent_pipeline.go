package bridge

import (
	"encoding/json"
	"fmt"
)

// --- Pipeline DTOs ---

// PipelineInfo describes a GitLab CI pipeline summary.
type PipelineInfo struct {
	ID        int    `json:"id"`
	ProjectID int    `json:"project_id"`
	Project   string `json:"project"`
	Ref       string `json:"ref"`
	Status    string `json:"status"`
	Source    string `json:"source,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at,omitempty"`
	WebURL    string `json:"web_url,omitempty"`
}

// PipelineStage describes a pipeline stage with its jobs.
type PipelineStage struct {
	Name   string        `json:"name"`
	Status string        `json:"status"`
	Jobs   []PipelineJob `json:"jobs,omitempty"`
}

// PipelineJob describes a single CI job.
type PipelineJob struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Stage     string `json:"stage"`
	Duration  int    `json:"duration,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	WebURL    string `json:"web_url,omitempty"`
}

// PipelineDetail is the full pipeline status with stage breakdown.
type PipelineDetail struct {
	PipelineInfo
	Stages          []PipelineStage `json:"stages,omitempty"`
	CompletedStages int             `json:"completed_stages"`
	TotalStages     int             `json:"total_stages"`
	CurrentStage    string          `json:"current_stage"`
	FailedJobCount  int             `json:"failed_job_count"`
	Duration        int             `json:"duration,omitempty"`
}

// --- Pipeline bridge methods ---

// ListActivePipelines fetches active pipelines from the mcp-gitlab server
// for the given project paths. Returns pipelines with status running or pending.
func (a *AgentBridge) ListActivePipelines(projects []string) ([]PipelineInfo, error) {
	var allPipelines []PipelineInfo

	for _, project := range projects {
		raw, err := a.client.CallTool("gitlab__list_pipelines", map[string]any{
			"project": project,
			"status":  "running",
		})
		if err != nil {
			// Try pending pipelines too, but don't fail on individual project errors.
			continue
		}

		var listResult struct {
			Pipelines []PipelineInfo `json:"pipelines"`
		}
		if err := unmarshalGitLabResult(raw, &listResult); err != nil {
			continue
		}
		pipelines := listResult.Pipelines
		for i := range pipelines {
			pipelines[i].Project = project
		}
		allPipelines = append(allPipelines, pipelines...)

		// Also fetch pending pipelines.
		raw, err = a.client.CallTool("gitlab__list_pipelines", map[string]any{
			"project": project,
			"status":  "pending",
		})
		if err == nil {
			var pendingResult struct {
				Pipelines []PipelineInfo `json:"pipelines"`
			}
			if err := unmarshalGitLabResult(raw, &pendingResult); err == nil {
				pending := pendingResult.Pipelines
				for i := range pending {
					pending[i].Project = project
				}
				allPipelines = append(allPipelines, pending...)
			}
		}
	}

	return allPipelines, nil
}

// GetPipelineDetail fetches detailed pipeline info including stages and jobs.
func (a *AgentBridge) GetPipelineDetail(project string, pipelineID int) (*PipelineDetail, error) {
	// Get pipeline info.
	raw, err := a.client.CallTool("gitlab__get_pipeline", map[string]any{
		"project":     project,
		"pipeline_id": pipelineID,
	})
	if err != nil {
		return nil, fmt.Errorf("get pipeline %d: %w", pipelineID, err)
	}

	var info PipelineInfo
	if err := unmarshalGitLabResult(raw, &info); err != nil {
		return nil, fmt.Errorf("unmarshal pipeline %d: %w", pipelineID, err)
	}
	info.Project = project

	// Get pipeline jobs to build stage breakdown.
	raw, err = a.client.CallTool("gitlab__list_pipeline_jobs", map[string]any{
		"project":     project,
		"pipeline_id": pipelineID,
	})
	if err != nil {
		// Return basic info without stage breakdown.
		return &PipelineDetail{PipelineInfo: info}, nil
	}

	var jobsResult struct {
		Jobs []PipelineJob `json:"jobs"`
	}
	if err := unmarshalGitLabResult(raw, &jobsResult); err != nil {
		return &PipelineDetail{PipelineInfo: info}, nil
	}
	jobs := jobsResult.Jobs

	// Build stages from jobs.
	stageOrder := []string{}
	stageMap := map[string]*PipelineStage{}
	for _, job := range jobs {
		if _, exists := stageMap[job.Stage]; !exists {
			stageOrder = append(stageOrder, job.Stage)
			stageMap[job.Stage] = &PipelineStage{
				Name:   job.Stage,
				Status: "pending",
			}
		}
		stageMap[job.Stage].Jobs = append(stageMap[job.Stage].Jobs, job)
	}

	// Determine stage statuses and counts.
	stages := make([]PipelineStage, 0, len(stageOrder))
	completedStages := 0
	currentStage := ""
	failedJobs := 0

	for _, name := range stageOrder {
		stage := stageMap[name]
		stage.Status = aggregateJobStatus(stage.Jobs)
		stages = append(stages, *stage)

		switch stage.Status {
		case "success":
			completedStages++
		case "running":
			if currentStage == "" {
				currentStage = name
			}
		case "failed":
			if currentStage == "" {
				currentStage = name
			}
		}

		for _, job := range stage.Jobs {
			if job.Status == "failed" {
				failedJobs++
			}
		}
	}

	if currentStage == "" && len(stageOrder) > 0 {
		// If no running stage, use the first pending one or the last stage.
		for _, name := range stageOrder {
			if stageMap[name].Status == "pending" {
				currentStage = name
				break
			}
		}
		if currentStage == "" {
			currentStage = stageOrder[len(stageOrder)-1]
		}
	}

	return &PipelineDetail{
		PipelineInfo:    info,
		Stages:          stages,
		CompletedStages: completedStages,
		TotalStages:     len(stages),
		CurrentStage:    currentStage,
		FailedJobCount:  failedJobs,
	}, nil
}

// aggregateJobStatus determines the overall status of a stage from its jobs.
func aggregateJobStatus(jobs []PipelineJob) string {
	hasRunning := false
	hasFailed := false
	allSuccess := true

	for _, job := range jobs {
		switch job.Status {
		case "running", "pending":
			hasRunning = true
			allSuccess = false
		case "failed":
			hasFailed = true
			allSuccess = false
		case "success":
			// OK
		default:
			allSuccess = false
		}
	}

	if hasFailed {
		return "failed"
	}
	if hasRunning {
		return "running"
	}
	if allSuccess && len(jobs) > 0 {
		return "success"
	}
	return "pending"
}

// unmarshalGitLabResult extracts the content from an MCP tool result for GitLab tools.
func unmarshalGitLabResult(raw json.RawMessage, target any) error {
	// GitLab MCP tools return results in the standard MCP CallToolResult format.
	return UnmarshalToolResult(raw, target)
}
