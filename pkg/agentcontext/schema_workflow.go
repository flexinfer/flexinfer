package agentcontext

import (
	"time"
)

// =========================================================================
// Workflow Orchestration Types
// =========================================================================

// WorkflowStatus defines the status of a workflow
type WorkflowStatus string

const (
	WorkflowStatusPending    WorkflowStatus = "pending"
	WorkflowStatusRunning    WorkflowStatus = "running"
	WorkflowStatusPaused     WorkflowStatus = "paused"
	WorkflowStatusWaiting    WorkflowStatus = "waiting_approval"
	WorkflowStatusCompleted  WorkflowStatus = "completed"
	WorkflowStatusFailed     WorkflowStatus = "failed"
	WorkflowStatusCancelled  WorkflowStatus = "cancelled"
	WorkflowStatusRolledBack WorkflowStatus = "rolled_back"
)

// StepStatus defines the status of a workflow step
type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
	StepStatusSkipped   StepStatus = "skipped"
	StepStatusWaiting   StepStatus = "waiting_approval"
)

// ApprovalStatus defines approval states
type ApprovalStatus string

const (
	ApprovalStatusPending  ApprovalStatus = "pending"
	ApprovalStatusApproved ApprovalStatus = "approved"
	ApprovalStatusRejected ApprovalStatus = "rejected"
)

// StepType defines what kind of step this is
type StepType string

const (
	StepTypeTool      StepType = "tool"       // Execute an MCP tool
	StepTypeApproval  StepType = "approval"   // Wait for human approval
	StepTypeGate      StepType = "gate"       // Conditional gate
	StepTypeParallel  StepType = "parallel"   // Execute steps in parallel
	StepTypeSubflow   StepType = "subflow"    // Execute a nested workflow
	StepTypeMapReduce StepType = "map_reduce" // Fan-out map + optional reduce
)

// WorkflowStep represents a single step in a workflow
type WorkflowStep struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	StepType    StepType `json:"step_type"`

	// For tool steps
	ToolName   string         `json:"tool_name,omitempty"`
	ToolArgs   map[string]any `json:"tool_args,omitempty"`
	ServerName string         `json:"server_name,omitempty"` // MCP server to use

	// DAG relationships
	DependsOn []string `json:"depends_on,omitempty"` // Step IDs this depends on

	// Approval gate settings
	RequiresApproval       bool   `json:"requires_approval,omitempty"`
	ApprovalMessage        string `json:"approval_message,omitempty"`
	ApprovalTimeoutSeconds int    `json:"approval_timeout_seconds,omitempty"` // Default: 3600 (1 hour)

	// Conditional execution
	Condition string `json:"condition,omitempty"` // JSONPath or simple expression

	// Parallel steps (for StepTypeParallel)
	ParallelSteps []WorkflowStep `json:"parallel_steps,omitempty"`

	// Subflow (for StepTypeSubflow)
	SubflowID string `json:"subflow_id,omitempty"`

	// MapReduce (for StepTypeMapReduce)
	MapInputKey      string         `json:"map_input_key,omitempty"`      // Key in workflow context holding []any items
	MapStepTemplate  *WorkflowStep  `json:"map_step_template,omitempty"`  // Template step instantiated per item
	ReduceToolName   string         `json:"reduce_tool_name,omitempty"`   // Optional reduce tool
	ReduceServerName string         `json:"reduce_server_name,omitempty"` // MCP server for reduce tool
	ReduceToolArgs   map[string]any `json:"reduce_tool_args,omitempty"`   // Args for reduce tool
	MaxConcurrency   int            `json:"max_concurrency,omitempty"`    // 0 = unlimited

	// Retry settings
	MaxRetries int `json:"max_retries,omitempty"`
	RetryDelay int `json:"retry_delay_ms,omitempty"`

	// Timeout in seconds
	Timeout int `json:"timeout_seconds,omitempty"`

	// Rollback step to execute on failure
	RollbackStepID string `json:"rollback_step_id,omitempty"`

	// Runtime state
	Status       StepStatus     `json:"status"`
	StartedAt    *time.Time     `json:"started_at,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
	RetryCount   int            `json:"retry_count"`
	Error        string         `json:"error,omitempty"`
	Result       map[string]any `json:"result,omitempty"`
	ApprovalInfo *ApprovalInfo  `json:"approval_info,omitempty"`
}

// ApprovalInfo tracks approval state for a step
type ApprovalInfo struct {
	Status      ApprovalStatus `json:"status"`
	RequestedAt time.Time      `json:"requested_at"`
	DecidedAt   *time.Time     `json:"decided_at,omitempty"`
	DecidedBy   string         `json:"decided_by,omitempty"`
	Comment     string         `json:"comment,omitempty"`
}

// WorkflowDefinition is the template for a workflow
type WorkflowDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	CreatedBy   string `json:"created_by"`

	// Steps in execution order (respecting DAG)
	Steps []WorkflowStep `json:"steps"`

	// Input schema (JSON Schema)
	InputSchema map[string]any `json:"input_schema,omitempty"`

	// Global timeout
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	// Rollback behavior
	RollbackOnFailure bool `json:"rollback_on_failure"`

	// Timestamps
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Workflow is a running instance of a WorkflowDefinition
type Workflow struct {
	ID           string `json:"id"`
	DefinitionID string `json:"definition_id"`
	SessionID    string `json:"session_id"`
	AgentID      string `json:"agent_id"`
	Namespace    string `json:"namespace,omitempty"`

	// Definition snapshot (for immutability)
	Definition WorkflowDefinition `json:"definition"`

	// Execution state
	Status      WorkflowStatus           `json:"status"`
	CurrentStep string                   `json:"current_step,omitempty"` // Current step ID
	StepStates  map[string]*WorkflowStep `json:"step_states"`

	// Completion signal for subflow waiters (closed when workflow completes)
	done chan struct{} `json:"-"`

	// Input/Output
	Input  map[string]any `json:"input,omitempty"`
	Output map[string]any `json:"output,omitempty"`

	// Execution context passed between steps
	Context map[string]any `json:"context,omitempty"`

	// Error tracking
	Error        string `json:"error,omitempty"`
	FailedStepID string `json:"failed_step_id,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// Metrics
	TotalSteps     int `json:"total_steps"`
	CompletedSteps int `json:"completed_steps"`
	FailedSteps    int `json:"failed_steps"`
}

// clone returns a deep copy of the workflow.
// Used to provide thread-safe snapshots from GetWorkflow.
func (w *Workflow) clone() *Workflow {
	if w == nil {
		return nil
	}
	cp := *w // shallow copy

	// Deep copy time pointers
	if w.StartedAt != nil {
		t := *w.StartedAt
		cp.StartedAt = &t
	}
	if w.CompletedAt != nil {
		t := *w.CompletedAt
		cp.CompletedAt = &t
	}

	// Deep copy maps
	if w.StepStates != nil {
		cp.StepStates = make(map[string]*WorkflowStep, len(w.StepStates))
		for k, v := range w.StepStates {
			if v != nil {
				stepCopy := *v
				// Deep copy nested pointers in WorkflowStep
				if v.StartedAt != nil {
					t := *v.StartedAt
					stepCopy.StartedAt = &t
				}
				if v.CompletedAt != nil {
					t := *v.CompletedAt
					stepCopy.CompletedAt = &t
				}
				if v.ToolArgs != nil {
					stepCopy.ToolArgs = deepCopyMap(v.ToolArgs)
				}
				if v.Result != nil {
					stepCopy.Result = deepCopyMap(v.Result)
				}
				if v.ApprovalInfo != nil {
					ai := *v.ApprovalInfo
					if v.ApprovalInfo.DecidedAt != nil {
						t := *v.ApprovalInfo.DecidedAt
						ai.DecidedAt = &t
					}
					stepCopy.ApprovalInfo = &ai
				}
				if v.MapStepTemplate != nil {
					tmplCopy := *v.MapStepTemplate
					if v.MapStepTemplate.ToolArgs != nil {
						tmplCopy.ToolArgs = deepCopyMap(v.MapStepTemplate.ToolArgs)
					}
					stepCopy.MapStepTemplate = &tmplCopy
				}
				if v.ReduceToolArgs != nil {
					stepCopy.ReduceToolArgs = deepCopyMap(v.ReduceToolArgs)
				}
				cp.StepStates[k] = &stepCopy
			}
		}
	}
	if w.Input != nil {
		cp.Input = deepCopyMap(w.Input)
	}
	if w.Output != nil {
		cp.Output = deepCopyMap(w.Output)
	}
	if w.Context != nil {
		cp.Context = deepCopyMap(w.Context)
	}

	return &cp
}

// deepCopyMap creates a deep copy of a map[string]any, recursing into nested
// maps and slices so that the clone shares no mutable state with the original.
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = deepCopyValue(v)
	}
	return cp
}

func deepCopyValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return deepCopyMap(val)
	case []any:
		s := make([]any, len(val))
		for i, elem := range val {
			s[i] = deepCopyValue(elem)
		}
		return s
	default:
		return v
	}
}

// WorkflowEvent represents an event in workflow execution
type WorkflowEvent struct {
	ID         string         `json:"id"`
	WorkflowID string         `json:"workflow_id"`
	StepID     string         `json:"step_id,omitempty"`
	EventType  string         `json:"event_type"` // started, completed, failed, approval_requested, approved, rejected, rolled_back
	Timestamp  time.Time      `json:"timestamp"`
	Details    map[string]any `json:"details,omitempty"`
}

// WorkflowSummary is a compact view of a workflow
type WorkflowSummary struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Status      WorkflowStatus `json:"status"`
	Progress    float64        `json:"progress"` // 0.0-1.0
	CurrentStep string         `json:"current_step,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Error       string         `json:"error,omitempty"`
}
