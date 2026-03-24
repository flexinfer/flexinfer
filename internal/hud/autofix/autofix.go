// Package autofix provides LLM-powered pipeline failure diagnosis and
// automated fix orchestration for the HUD.
package autofix

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/coordinator"
)

// --- Types ---

// Diagnosis is the result of LLM analysis of a pipeline failure.
type Diagnosis struct {
	PipelineID   int      `json:"pipeline_id"`
	Project      string   `json:"project"`
	RootCause    string   `json:"root_cause"`
	Category     string   `json:"category"` // "test_failure", "build_error", "lint", "dependency", "infra"
	SuggestedFix string   `json:"suggested_fix"`
	Confidence   float64  `json:"confidence"` // 0.0-1.0
	FailedJobs   []string `json:"failed_jobs"`
	LogSnippets  []string `json:"log_snippets"`
}

// AutoFixProposal is a proposed fix generated from a diagnosis.
type AutoFixProposal struct {
	ID               string    `json:"id"`
	DiagnosisID      string    `json:"diagnosis_id"`
	Description      string    `json:"description"`
	Strategy         string    `json:"strategy"` // "agent_fix", "retry", "manual"
	Files            []string  `json:"estimated_files"`
	Confidence       float64   `json:"confidence"`
	RequiresApproval bool      `json:"requires_approval"`
	CreatedAt        time.Time `json:"created_at"`
}

// AutoFixExecution tracks the execution of an approved proposal.
type AutoFixExecution struct {
	ID          string     `json:"id"`
	ProposalID  string     `json:"proposal_id"`
	Status      string     `json:"status"` // "pending_approval", "running", "succeeded", "failed", "rejected"
	AgentID     string     `json:"agent_id,omitempty"`
	SpawnID     string     `json:"spawn_id,omitempty"`
	Result      string     `json:"result,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// --- Bridge interfaces ---

// PipelineBridge fetches pipeline details and job logs.
type PipelineBridge interface {
	GetPipelineDetail(project string, pipelineID int) (*bridge.PipelineDetail, error)
}

// JobTraceBridge fetches job trace logs.
type JobTraceBridge interface {
	GetJobTrace(project string, jobID int) (string, error)
}

// SpawnerOps spawns headless agents for auto-fix execution.
type SpawnerOps interface {
	Spawn(ctx context.Context, req SpawnRequest) (string, error)
}

// SpawnRequest is a minimal request type for spawning an agent.
// This avoids importing the spawn package directly.
type SpawnRequest struct {
	AgentType       string `json:"agent_type"`
	Project         string `json:"project"`
	TaskDescription string `json:"task_description"`
	Branch          string `json:"branch,omitempty"`
	BaseBranch      string `json:"base_branch,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
}

// --- Auto-fix engine ---

// AutoFixEngine coordinates diagnosis, proposal, and execution of fixes.
type AutoFixEngine struct {
	mu         sync.RWMutex
	llm        *coordinator.FlexInferClient
	pipeline   PipelineBridge
	jobTrace   JobTraceBridge
	spawner    SpawnerOps
	logger     *slog.Logger
	model      string
	proposals  []AutoFixProposal
	executions []AutoFixExecution
	diagnoses  map[string]*Diagnosis // keyed by "project:pipelineID"
}

// NewAutoFixEngine creates an AutoFixEngine.
func NewAutoFixEngine(
	llm *coordinator.FlexInferClient,
	pipeline PipelineBridge,
	jobTrace JobTraceBridge,
	spawner SpawnerOps,
	model string,
	logger *slog.Logger,
) *AutoFixEngine {
	if logger == nil {
		logger = slog.Default()
	}
	if model == "" {
		model = "qwen3-8b"
	}
	return &AutoFixEngine{
		llm:        llm,
		pipeline:   pipeline,
		jobTrace:   jobTrace,
		spawner:    spawner,
		logger:     logger.With("component", "autofix-engine"),
		model:      model,
		proposals:  make([]AutoFixProposal, 0),
		executions: make([]AutoFixExecution, 0),
		diagnoses:  make(map[string]*Diagnosis),
	}
}

// DiagnoseFailure analyzes a failed pipeline using the LLM.
func (e *AutoFixEngine) DiagnoseFailure(ctx context.Context, project string, pipelineID int) (*Diagnosis, error) {
	if e.llm == nil {
		return nil, fmt.Errorf("autofix: LLM client not configured")
	}
	if e.pipeline == nil {
		return nil, fmt.Errorf("autofix: pipeline bridge not configured")
	}

	// Fetch pipeline detail.
	detail, err := e.pipeline.GetPipelineDetail(project, pipelineID)
	if err != nil {
		return nil, fmt.Errorf("autofix: fetch pipeline detail: %w", err)
	}

	// Collect failed job names and log snippets.
	var failedJobs []string
	var logSnippets []string
	for _, stage := range detail.Stages {
		for _, job := range stage.Jobs {
			if job.Status == "failed" {
				failedJobs = append(failedJobs, job.Name)
				if e.jobTrace != nil {
					trace, traceErr := e.jobTrace.GetJobTrace(project, job.ID)
					if traceErr == nil && trace != "" {
						// Truncate log to last 2000 chars.
						if len(trace) > 2000 {
							trace = trace[len(trace)-2000:]
						}
						logSnippets = append(logSnippets, fmt.Sprintf("=== %s ===\n%s", job.Name, trace))
					}
				}
			}
		}
	}

	// Build LLM prompt.
	userMsg := buildDiagnosticPrompt(project, pipelineID, detail.Ref, failedJobs, logSnippets)

	response, err := e.llm.CompleteSimple(ctx, e.model, promptDiagnosis, userMsg, 2048)
	if err != nil {
		return nil, fmt.Errorf("autofix: LLM diagnosis failed: %w", err)
	}

	// Parse structured response.
	diag, err := parseDiagnosis(response, project, pipelineID, failedJobs, logSnippets)
	if err != nil {
		return nil, fmt.Errorf("autofix: parse diagnosis: %w", err)
	}

	// Cache the diagnosis.
	e.mu.Lock()
	key := fmt.Sprintf("%s:%d", project, pipelineID)
	e.diagnoses[key] = diag
	e.mu.Unlock()

	e.logger.Info("pipeline diagnosed",
		"project", project,
		"pipeline_id", pipelineID,
		"root_cause", diag.RootCause,
		"category", diag.Category,
		"confidence", diag.Confidence,
	)

	return diag, nil
}

// ProposeAutoFix generates a fix proposal from a diagnosis.
func (e *AutoFixEngine) ProposeAutoFix(diag Diagnosis) (*AutoFixProposal, error) {
	strategy := "manual"
	requiresApproval := true

	// Determine strategy based on category and confidence.
	switch {
	case diag.Confidence >= 0.8 && (diag.Category == "test_failure" || diag.Category == "lint"):
		strategy = "agent_fix"
	case diag.Category == "infra":
		strategy = "retry"
		requiresApproval = false
	case diag.Confidence >= 0.6:
		strategy = "agent_fix"
	}

	proposal := AutoFixProposal{
		ID:               fmt.Sprintf("proposal-%d", time.Now().UnixNano()),
		DiagnosisID:      fmt.Sprintf("%s:%d", diag.Project, diag.PipelineID),
		Description:      diag.SuggestedFix,
		Strategy:         strategy,
		Confidence:       diag.Confidence,
		RequiresApproval: requiresApproval,
		CreatedAt:        time.Now(),
	}

	e.mu.Lock()
	e.proposals = append(e.proposals, proposal)
	e.mu.Unlock()

	e.logger.Info("auto-fix proposed",
		"proposal_id", proposal.ID,
		"strategy", proposal.Strategy,
		"confidence", proposal.Confidence,
	)

	return &proposal, nil
}

// ExecuteAutoFix starts execution of an approved proposal.
func (e *AutoFixEngine) ExecuteAutoFix(ctx context.Context, proposal AutoFixProposal) (*AutoFixExecution, error) {
	exec := AutoFixExecution{
		ID:         fmt.Sprintf("exec-%d", time.Now().UnixNano()),
		ProposalID: proposal.ID,
		Status:     "running",
		StartedAt:  time.Now(),
	}

	// Look up the diagnosis for context.
	e.mu.RLock()
	diag := e.diagnoses[proposal.DiagnosisID]
	e.mu.RUnlock()

	switch proposal.Strategy {
	case "agent_fix":
		if e.spawner != nil && diag != nil {
			taskDesc := fmt.Sprintf("Fix pipeline failure: %s\n\nDiagnosis: %s\n\nSuggested fix: %s",
				proposal.Description, diag.RootCause, diag.SuggestedFix)
			spawnID, err := e.spawner.Spawn(ctx, SpawnRequest{
				AgentType:       "claude-code",
				Project:         diag.Project,
				TaskDescription: taskDesc,
				BaseBranch:      diag.Project,
			})
			if err != nil {
				exec.Status = "failed"
				exec.Result = fmt.Sprintf("spawn failed: %v", err)
				now := time.Now()
				exec.CompletedAt = &now
			} else {
				exec.SpawnID = spawnID
				exec.AgentID = "spawn-" + spawnID
			}
		} else {
			// No spawner available, mark as needing manual intervention.
			exec.Status = "failed"
			exec.Result = "no spawn orchestrator available"
			now := time.Now()
			exec.CompletedAt = &now
		}

	case "retry":
		// Retry strategy is a no-op placeholder; the actual retry would
		// trigger a pipeline re-run via the GitLab bridge.
		exec.Status = "succeeded"
		exec.Result = "pipeline retry requested"
		now := time.Now()
		exec.CompletedAt = &now

	default:
		exec.Status = "failed"
		exec.Result = "manual intervention required"
		now := time.Now()
		exec.CompletedAt = &now
	}

	e.mu.Lock()
	e.executions = append(e.executions, exec)
	e.mu.Unlock()

	e.logger.Info("auto-fix execution started",
		"exec_id", exec.ID,
		"proposal_id", proposal.ID,
		"status", exec.Status,
	)

	return &exec, nil
}

// GetExecution returns an execution by ID.
func (e *AutoFixEngine) GetExecution(id string) (*AutoFixExecution, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, exec := range e.executions {
		if exec.ID == id {
			return &exec, nil
		}
	}
	return nil, fmt.Errorf("autofix: execution %q not found", id)
}

// ListProposals returns all proposals, newest first.
func (e *AutoFixEngine) ListProposals() []AutoFixProposal {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]AutoFixProposal, len(e.proposals))
	for i, p := range e.proposals {
		out[len(e.proposals)-1-i] = p
	}
	return out
}

// ListExecutions returns all executions, newest first.
func (e *AutoFixEngine) ListExecutions() []AutoFixExecution {
	e.mu.RLock()
	defer e.mu.RUnlock()

	out := make([]AutoFixExecution, len(e.executions))
	for i, ex := range e.executions {
		out[len(e.executions)-1-i] = ex
	}
	return out
}

// GetProposal returns a proposal by ID.
func (e *AutoFixEngine) GetProposal(id string) (*AutoFixProposal, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, p := range e.proposals {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("autofix: proposal %q not found", id)
}

// RejectProposal marks a proposal's execution as rejected.
func (e *AutoFixEngine) RejectProposal(proposalID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Create a rejected execution record.
	now := time.Now()
	exec := AutoFixExecution{
		ID:          fmt.Sprintf("exec-%d", now.UnixNano()),
		ProposalID:  proposalID,
		Status:      "rejected",
		StartedAt:   now,
		CompletedAt: &now,
	}
	e.executions = append(e.executions, exec)
	return nil
}

// --- Prompts and parsing ---

const promptDiagnosis = `You are a CI/CD pipeline diagnostic engine. Analyze the provided pipeline failure information and produce a structured diagnosis.

Respond with ONLY valid JSON in this exact format:
{
  "root_cause": "Brief description of the root cause",
  "category": "test_failure|build_error|lint|dependency|infra",
  "suggested_fix": "Specific actionable fix suggestion",
  "confidence": 0.85
}

Categories:
- test_failure: Unit/integration test failures
- build_error: Compilation or build errors
- lint: Linting or formatting failures
- dependency: Missing or incompatible dependencies
- infra: Infrastructure issues (network, runner, Docker, etc.)`

func buildDiagnosticPrompt(project string, pipelineID int, ref string, failedJobs, logSnippets []string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Project: %s\n", project)
	fmt.Fprintf(&sb, "Pipeline ID: %d\n", pipelineID)
	fmt.Fprintf(&sb, "Branch/Ref: %s\n", ref)
	fmt.Fprintf(&sb, "Failed Jobs: %s\n\n", strings.Join(failedJobs, ", "))

	if len(logSnippets) > 0 {
		sb.WriteString("Job Logs:\n")
		for _, snippet := range logSnippets {
			sb.WriteString(snippet)
			sb.WriteString("\n\n")
		}
	}
	return sb.String()
}

// diagnosisJSON is the parsed LLM response structure.
type diagnosisJSON struct {
	RootCause    string  `json:"root_cause"`
	Category     string  `json:"category"`
	SuggestedFix string  `json:"suggested_fix"`
	Confidence   float64 `json:"confidence"`
}

func parseDiagnosis(response, project string, pipelineID int, failedJobs, logSnippets []string) (*Diagnosis, error) {
	// Extract JSON from response (strip any markdown fences).
	cleaned := strings.TrimSpace(response)
	if idx := strings.Index(cleaned, "{"); idx >= 0 {
		cleaned = cleaned[idx:]
	}
	if idx := strings.LastIndex(cleaned, "}"); idx >= 0 {
		cleaned = cleaned[:idx+1]
	}

	var parsed diagnosisJSON
	if err := json.Unmarshal([]byte(cleaned), &parsed); err != nil {
		// Fallback: return a basic diagnosis from the raw text.
		return &Diagnosis{
			PipelineID:   pipelineID,
			Project:      project,
			RootCause:    response,
			Category:     "build_error",
			SuggestedFix: "Manual investigation required",
			Confidence:   0.3,
			FailedJobs:   failedJobs,
			LogSnippets:  logSnippets,
		}, nil
	}

	// Clamp confidence to valid range.
	if parsed.Confidence < 0 {
		parsed.Confidence = 0
	}
	if parsed.Confidence > 1.0 {
		parsed.Confidence = 1.0
	}

	return &Diagnosis{
		PipelineID:   pipelineID,
		Project:      project,
		RootCause:    parsed.RootCause,
		Category:     parsed.Category,
		SuggestedFix: parsed.SuggestedFix,
		Confidence:   parsed.Confidence,
		FailedJobs:   failedJobs,
		LogSnippets:  logSnippets,
	}, nil
}
