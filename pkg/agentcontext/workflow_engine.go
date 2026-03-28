// workflow_engine.go — public workflow API, engine lifecycle, and CRUD operations.
package agentcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// WorkflowEngine manages workflow execution
type WorkflowEngine struct {
	mu sync.RWMutex

	// Storage
	definitions map[string]*WorkflowDefinition // definition ID -> definition
	workflows   map[string]*Workflow           // workflow ID -> workflow
	events      map[string][]WorkflowEvent     // workflow ID -> events

	// Tool executor callback
	toolExecutor ToolExecutor

	// Event callbacks
	onEvent func(event WorkflowEvent)

	// Logger
	logger *slog.Logger
}

// ToolExecutor is a function that executes an MCP tool
type ToolExecutor func(ctx context.Context, serverName, toolName string, args map[string]any) (map[string]any, error)

// NewWorkflowEngine creates a new workflow engine
func NewWorkflowEngine(toolExecutor ToolExecutor) *WorkflowEngine {
	return &WorkflowEngine{
		definitions:  make(map[string]*WorkflowDefinition),
		workflows:    make(map[string]*Workflow),
		events:       make(map[string][]WorkflowEvent),
		toolExecutor: toolExecutor,
		logger:       slog.Default(),
	}
}

// SetEventCallback sets a callback for workflow events
func (e *WorkflowEngine) SetEventCallback(cb func(WorkflowEvent)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onEvent = cb
}

// RegisterDefinition registers a workflow definition
func (e *WorkflowEngine) RegisterDefinition(def *WorkflowDefinition) error {
	if def.Name == "" {
		return fmt.Errorf("workflow definition name is required")
	}
	if len(def.Steps) == 0 {
		return fmt.Errorf("workflow must have at least one step")
	}

	// Validate DAG (no cycles)
	if err := validateDAG(def.Steps); err != nil {
		return fmt.Errorf("invalid workflow DAG: %w", err)
	}

	// Initialize step IDs if missing
	for i := range def.Steps {
		if def.Steps[i].ID == "" {
			def.Steps[i].ID = fmt.Sprintf("step-%d", i+1)
		}
		def.Steps[i].Status = StepStatusPending
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Idempotent registration by name+namespace when ID is not provided.
	// This keeps repeated sync/bootstrap runs from creating duplicate definitions.
	if def.ID == "" {
		for id, existing := range e.definitions {
			if existing.Name == def.Name && existing.Namespace == def.Namespace {
				def.ID = id
				break
			}
		}
		if def.ID == "" {
			def.ID = uuid.New().String()[:8]
		}
	}

	now := time.Now().UTC()
	if existing, ok := e.definitions[def.ID]; ok {
		def.CreatedAt = existing.CreatedAt
	} else {
		def.CreatedAt = now
	}
	def.UpdatedAt = now

	e.definitions[def.ID] = def

	return nil
}

// GetDefinition retrieves a workflow definition
func (e *WorkflowEngine) GetDefinition(id string) (*WorkflowDefinition, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	def, ok := e.definitions[id]
	if !ok {
		return nil, fmt.Errorf("workflow definition not found: %s", id)
	}
	return def, nil
}

// ListDefinitions lists workflow definitions
func (e *WorkflowEngine) ListDefinitions(namespace string) []*WorkflowDefinition {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result []*WorkflowDefinition
	for _, def := range e.definitions {
		if namespace == "" || def.Namespace == namespace {
			result = append(result, def)
		}
	}
	return result
}

// StartWorkflow starts a new workflow instance
func (e *WorkflowEngine) StartWorkflow(ctx context.Context, definitionID, sessionID, agentID string, input map[string]any) (*Workflow, error) {
	e.mu.Lock()
	def, ok := e.definitions[definitionID]
	if !ok {
		e.mu.Unlock()
		return nil, fmt.Errorf("workflow definition not found: %s", definitionID)
	}

	// Create workflow instance
	now := time.Now().UTC()
	wf := &Workflow{
		ID:           uuid.New().String()[:12],
		DefinitionID: definitionID,
		SessionID:    sessionID,
		AgentID:      agentID,
		Namespace:    def.Namespace,
		Definition:   *def, // Snapshot
		Status:       WorkflowStatusRunning,
		StepStates:   make(map[string]*WorkflowStep),
		Input:        input,
		Context:      make(map[string]any),
		CreatedAt:    now,
		StartedAt:    &now,
		TotalSteps:   len(def.Steps),
		done:         make(chan struct{}),
	}

	// Initialize step states
	for _, step := range def.Steps {
		stepCopy := step
		stepCopy.Status = StepStatusPending
		wf.StepStates[step.ID] = &stepCopy
	}

	e.workflows[wf.ID] = wf
	e.events[wf.ID] = []WorkflowEvent{}
	e.mu.Unlock()

	// Emit started event
	e.emitEvent(WorkflowEvent{
		ID:         uuid.New().String()[:8],
		WorkflowID: wf.ID,
		EventType:  "workflow_started",
		Timestamp:  now,
		Details:    map[string]any{"definition": def.Name, "input": input},
	})

	// Start execution in background
	go e.executeWorkflow(context.Background(), wf)

	return wf, nil
}

// GetWorkflow retrieves a workflow by ID.
// Returns a snapshot copy to avoid data races with concurrent modifications.
func (e *WorkflowEngine) GetWorkflow(id string) (*Workflow, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	wf, ok := e.workflows[id]
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", id)
	}
	return wf.clone(), nil
}

// ListWorkflows lists workflows with optional filtering
func (e *WorkflowEngine) ListWorkflows(sessionID, agentID string, status WorkflowStatus) []*WorkflowSummary {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result []*WorkflowSummary
	for _, wf := range e.workflows {
		if sessionID != "" && wf.SessionID != sessionID {
			continue
		}
		if agentID != "" && wf.AgentID != agentID {
			continue
		}
		if status != "" && wf.Status != status {
			continue
		}

		progress := 0.0
		if wf.TotalSteps > 0 {
			progress = float64(wf.CompletedSteps) / float64(wf.TotalSteps)
		}

		result = append(result, &WorkflowSummary{
			ID:          wf.ID,
			Name:        wf.Definition.Name,
			Status:      wf.Status,
			Progress:    progress,
			CurrentStep: wf.CurrentStep,
			CreatedAt:   wf.CreatedAt,
			Error:       wf.Error,
		})
	}
	return result
}

// ApproveStep approves a step waiting for approval
func (e *WorkflowEngine) ApproveStep(workflowID, stepID, approverID, comment string) error {
	e.mu.Lock()
	wf, ok := e.workflows[workflowID]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("workflow not found: %s", workflowID)
	}

	step, ok := wf.StepStates[stepID]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("step not found: %s", stepID)
	}

	if step.Status != StepStatusWaiting {
		e.mu.Unlock()
		return fmt.Errorf("step is not waiting for approval")
	}

	now := time.Now().UTC()
	step.ApprovalInfo.Status = ApprovalStatusApproved
	step.ApprovalInfo.DecidedAt = &now
	step.ApprovalInfo.DecidedBy = approverID
	step.ApprovalInfo.Comment = comment
	step.Status = StepStatusPending // Ready to execute

	// If workflow was waiting, resume
	if wf.Status == WorkflowStatusWaiting {
		wf.Status = WorkflowStatusRunning
	}
	e.mu.Unlock()

	e.emitEvent(WorkflowEvent{
		ID:         uuid.New().String()[:8],
		WorkflowID: workflowID,
		StepID:     stepID,
		EventType:  "step_approved",
		Timestamp:  now,
		Details:    map[string]any{"approved_by": approverID, "comment": comment},
	})

	// Resume execution
	go e.executeWorkflow(context.Background(), wf)

	return nil
}

// RejectStep rejects a step waiting for approval
func (e *WorkflowEngine) RejectStep(workflowID, stepID, rejecterID, comment string) error {
	e.mu.Lock()
	wf, ok := e.workflows[workflowID]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("workflow not found: %s", workflowID)
	}

	step, ok := wf.StepStates[stepID]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("step not found: %s", stepID)
	}

	if step.Status != StepStatusWaiting {
		e.mu.Unlock()
		return fmt.Errorf("step is not waiting for approval")
	}

	now := time.Now().UTC()
	step.ApprovalInfo.Status = ApprovalStatusRejected
	step.ApprovalInfo.DecidedAt = &now
	step.ApprovalInfo.DecidedBy = rejecterID
	step.ApprovalInfo.Comment = comment
	step.Status = StepStatusFailed
	step.Error = "approval rejected: " + comment

	wf.Status = WorkflowStatusFailed
	wf.FailedStepID = stepID
	wf.Error = step.Error
	wf.CompletedAt = &now
	e.mu.Unlock()

	e.emitEvent(WorkflowEvent{
		ID:         uuid.New().String()[:8],
		WorkflowID: workflowID,
		StepID:     stepID,
		EventType:  "step_rejected",
		Timestamp:  now,
		Details:    map[string]any{"rejected_by": rejecterID, "comment": comment},
	})

	return nil
}

// CancelWorkflow cancels a running workflow
func (e *WorkflowEngine) CancelWorkflow(workflowID, reason string) error {
	e.mu.Lock()
	wf, ok := e.workflows[workflowID]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("workflow not found: %s", workflowID)
	}

	if wf.Status == WorkflowStatusCompleted || wf.Status == WorkflowStatusCancelled {
		e.mu.Unlock()
		return fmt.Errorf("workflow already %s", wf.Status)
	}

	now := time.Now().UTC()
	wf.Status = WorkflowStatusCancelled
	wf.Error = reason
	wf.CompletedAt = &now

	// Signal subflow waiters
	if wf.done != nil {
		select {
		case <-wf.done:
		default:
			close(wf.done)
		}
	}
	e.mu.Unlock()

	e.emitEvent(WorkflowEvent{
		ID:         uuid.New().String()[:8],
		WorkflowID: workflowID,
		EventType:  "workflow_cancelled",
		Timestamp:  now,
		Details:    map[string]any{"reason": reason},
	})

	return nil
}

// GetEvents retrieves events for a workflow
func (e *WorkflowEngine) GetEvents(workflowID string) ([]WorkflowEvent, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	events, ok := e.events[workflowID]
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", workflowID)
	}
	return events, nil
}

// ParseWorkflowDefinition parses a workflow definition from JSON
func ParseWorkflowDefinition(data []byte) (*WorkflowDefinition, error) {
	var def WorkflowDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("failed to parse workflow: %w", err)
	}
	return &def, nil
}
