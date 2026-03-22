package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"
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

// PipelineRef is a lightweight reference for linking entities to pipelines.
// This is a bridge-local type (distinct from the schema PipelineRef) following
// the bridge DTO pattern.
type PipelineRef struct {
	ID      int    `json:"id"`
	Project string `json:"project"`
	Ref     string `json:"ref,omitempty"`
	WebURL  string `json:"web_url,omitempty"`
}

// PipelineRefFromInfo creates a PipelineRef from a PipelineInfo.
func PipelineRefFromInfo(p PipelineInfo) *PipelineRef {
	return &PipelineRef{
		ID:      p.ID,
		Project: p.Project,
		Ref:     p.Ref,
		WebURL:  p.WebURL,
	}
}

// --- Pipeline bridge methods ---

// ListActivePipelines fetches active pipelines from the mcp-gitlab server
// for the given project paths concurrently. Returns pipelines with status
// running or pending. Concurrency is capped at 4 to avoid overwhelming GitLab.
func (a *AgentBridge) ListActivePipelines(projects []string) ([]PipelineInfo, error) {
	g, _ := errgroup.WithContext(context.Background())
	g.SetLimit(4) // cap concurrency to avoid overwhelming GitLab
	var mu sync.Mutex
	var allPipelines []PipelineInfo

	for _, project := range projects {
		g.Go(func() error {
			// Fetch running pipelines.
			raw, err := a.client.CallTool("gitlab__list_pipelines", map[string]any{
				"project": project,
				"status":  "running",
			})
			if err == nil {
				var listResult struct {
					Pipelines []PipelineInfo `json:"pipelines"`
				}
				if err := unmarshalGitLabResult(raw, &listResult); err == nil {
					pipelines := listResult.Pipelines
					for i := range pipelines {
						pipelines[i].Project = project
					}
					mu.Lock()
					allPipelines = append(allPipelines, pipelines...)
					mu.Unlock()
				}
			}

			// Fetch pending pipelines.
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
					mu.Lock()
					allPipelines = append(allPipelines, pending...)
					mu.Unlock()
				}
			}
			return nil
		})
	}

	g.Wait()
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

// RecordPipelineEvent creates a pipeline_event context entry in the given session.
// This links CI pipeline lifecycle events to the agent's session context.
func (a *AgentBridge) RecordPipelineEvent(sessionID string, ref PipelineRef, status, stage string) error {
	title := fmt.Sprintf("Pipeline %d %s", ref.ID, status)
	content := fmt.Sprintf("Pipeline %d (%s) on %s: status=%s", ref.ID, ref.Project, ref.Ref, status)
	if stage != "" {
		content += fmt.Sprintf(", stage=%s", stage)
	}

	entries := []map[string]any{{
		"entry_type": "pipeline_event",
		"title":      title,
		"content":    content,
		"metadata": map[string]any{
			"pipeline_id":      ref.ID,
			"pipeline_project": ref.Project,
			"pipeline_ref":     ref.Ref,
			"pipeline_status":  status,
			"pipeline_stage":   stage,
			"pipeline_web_url": ref.WebURL,
		},
	}}

	return a.ContextAdd(sessionID, entries)
}

// FindPipelineForBranch searches active pipelines for one matching the given branch.
// Returns nil if no matching pipeline is found. Used by WorkStart to auto-link
// sessions/tasks to their CI pipeline.
func (a *AgentBridge) FindPipelineForBranch(projects []string, branch string) *PipelineRef {
	if branch == "" || len(projects) == 0 {
		return nil
	}
	pipelines, err := a.ListActivePipelines(projects)
	if err != nil || len(pipelines) == 0 {
		return nil
	}
	for _, p := range pipelines {
		if p.Ref == branch {
			return PipelineRefFromInfo(p)
		}
	}
	return nil
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
