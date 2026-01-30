package agentcontext

import (
	"context"
	"encoding/json"
	"fmt"
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
	if def.ID == "" {
		def.ID = uuid.New().String()[:8]
	}
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

	def.CreatedAt = time.Now().UTC()
	def.UpdatedAt = def.CreatedAt

	e.mu.Lock()
	defer e.mu.Unlock()
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

// GetWorkflow retrieves a workflow by ID
func (e *WorkflowEngine) GetWorkflow(id string) (*Workflow, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	wf, ok := e.workflows[id]
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", id)
	}
	return wf, nil
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

// executeWorkflow runs the workflow DAG
func (e *WorkflowEngine) executeWorkflow(ctx context.Context, wf *Workflow) {
	for {
		// Check if cancelled
		e.mu.RLock()
		if wf.Status == WorkflowStatusCancelled || wf.Status == WorkflowStatusFailed {
			e.mu.RUnlock()
			return
		}
		e.mu.RUnlock()

		// Find next executable steps
		readySteps := e.findReadySteps(wf)
		if len(readySteps) == 0 {
			// Check if all done
			if e.allStepsComplete(wf) {
				e.completeWorkflow(wf, nil)
				return
			}
			// Might be waiting for approval
			if e.anyStepWaiting(wf) {
				e.mu.Lock()
				wf.Status = WorkflowStatusWaiting
				e.mu.Unlock()
				return
			}
			// No progress possible - deadlock or error
			e.completeWorkflow(wf, fmt.Errorf("workflow deadlock: no executable steps"))
			return
		}

		// Execute ready steps (in parallel if multiple)
		var wg sync.WaitGroup
		for _, stepID := range readySteps {
			wg.Add(1)
			go func(sid string) {
				defer wg.Done()
				e.executeStep(ctx, wf, sid)
			}(stepID)
		}
		wg.Wait()

		// Check for failures
		e.mu.RLock()
		failed := wf.Status == WorkflowStatusFailed
		e.mu.RUnlock()
		if failed {
			// Handle rollback if configured
			if wf.Definition.RollbackOnFailure {
				e.rollbackWorkflow(ctx, wf)
			}
			return
		}
	}
}

// findReadySteps finds steps ready to execute
func (e *WorkflowEngine) findReadySteps(wf *Workflow) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var ready []string
	for stepID, step := range wf.StepStates {
		if step.Status != StepStatusPending {
			continue
		}

		// Check dependencies
		depsComplete := true
		for _, depID := range step.DependsOn {
			depStep, ok := wf.StepStates[depID]
			if !ok || depStep.Status != StepStatusCompleted {
				depsComplete = false
				break
			}
		}

		if depsComplete {
			ready = append(ready, stepID)
		}
	}
	return ready
}

// allStepsComplete checks if all steps are done
func (e *WorkflowEngine) allStepsComplete(wf *Workflow) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, step := range wf.StepStates {
		if step.Status != StepStatusCompleted && step.Status != StepStatusSkipped {
			return false
		}
	}
	return true
}

// anyStepWaiting checks if any step is waiting for approval
func (e *WorkflowEngine) anyStepWaiting(wf *Workflow) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, step := range wf.StepStates {
		if step.Status == StepStatusWaiting {
			return true
		}
	}
	return false
}

// executeStep executes a single step
func (e *WorkflowEngine) executeStep(ctx context.Context, wf *Workflow, stepID string) {
	e.mu.Lock()
	step, ok := wf.StepStates[stepID]
	if !ok {
		e.mu.Unlock()
		return
	}

	now := time.Now().UTC()
	step.Status = StepStatusRunning
	step.StartedAt = &now
	wf.CurrentStep = stepID
	e.mu.Unlock()

	e.emitEvent(WorkflowEvent{
		ID:         uuid.New().String()[:8],
		WorkflowID: wf.ID,
		StepID:     stepID,
		EventType:  "step_started",
		Timestamp:  now,
		Details:    map[string]any{"step_name": step.Name, "step_type": step.StepType},
	})

	// Check if requires approval (and hasn't been approved yet)
	if step.RequiresApproval && (step.ApprovalInfo == nil || step.ApprovalInfo.Status == ApprovalStatusPending) {
		e.requestApproval(wf, stepID)
		return
	}

	// Execute based on step type
	var result map[string]any
	var err error

	switch step.StepType {
	case StepTypeTool:
		result, err = e.executeToolStep(ctx, wf, step)
	case StepTypeGate:
		result, err = e.executeGateStep(wf, step)
	case StepTypeParallel:
		result, err = e.executeParallelStep(ctx, wf, step)
	case StepTypeSubflow:
		result, err = e.executeSubflowStep(ctx, wf, step)
	case StepTypeApproval:
		// Pure approval step - check if already approved
		if step.ApprovalInfo != nil && step.ApprovalInfo.Status == ApprovalStatusApproved {
			// Already approved, mark as completed
			result = map[string]any{"approved": true, "approved_by": step.ApprovalInfo.DecidedBy}
		} else {
			// Not yet approved, request approval
			e.requestApproval(wf, stepID)
			return
		}
	default:
		err = fmt.Errorf("unknown step type: %s", step.StepType)
	}

	// Handle result
	e.mu.Lock()
	completedAt := time.Now().UTC()
	step.CompletedAt = &completedAt

	if err != nil {
		// Check retry
		if step.RetryCount < step.MaxRetries {
			step.RetryCount++
			step.Status = StepStatusPending
			step.Error = ""
			e.mu.Unlock()

			e.emitEvent(WorkflowEvent{
				ID:         uuid.New().String()[:8],
				WorkflowID: wf.ID,
				StepID:     stepID,
				EventType:  "step_retry",
				Timestamp:  completedAt,
				Details:    map[string]any{"attempt": step.RetryCount, "error": err.Error()},
			})

			if step.RetryDelay > 0 {
				time.Sleep(time.Duration(step.RetryDelay) * time.Millisecond)
			}
			return
		}

		step.Status = StepStatusFailed
		step.Error = err.Error()
		wf.Status = WorkflowStatusFailed
		wf.FailedStepID = stepID
		wf.Error = fmt.Sprintf("step %s failed: %s", step.Name, err.Error())
		wf.FailedSteps++
		e.mu.Unlock()

		e.emitEvent(WorkflowEvent{
			ID:         uuid.New().String()[:8],
			WorkflowID: wf.ID,
			StepID:     stepID,
			EventType:  "step_failed",
			Timestamp:  completedAt,
			Details:    map[string]any{"error": err.Error()},
		})
		return
	}

	step.Status = StepStatusCompleted
	step.Result = result
	wf.CompletedSteps++

	// Store result in workflow context
	if result != nil {
		wf.Context[stepID] = result
	}
	e.mu.Unlock()

	e.emitEvent(WorkflowEvent{
		ID:         uuid.New().String()[:8],
		WorkflowID: wf.ID,
		StepID:     stepID,
		EventType:  "step_completed",
		Timestamp:  completedAt,
		Details:    map[string]any{"result_keys": mapKeys(result)},
	})
}

// executeToolStep executes an MCP tool
func (e *WorkflowEngine) executeToolStep(ctx context.Context, wf *Workflow, step *WorkflowStep) (map[string]any, error) {
	if e.toolExecutor == nil {
		return nil, fmt.Errorf("no tool executor configured")
	}

	// Resolve variable references in args
	args := resolveVariables(step.ToolArgs, wf.Input, wf.Context)

	// Apply timeout if set
	if step.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(step.Timeout)*time.Second)
		defer cancel()
	}

	return e.toolExecutor(ctx, step.ServerName, step.ToolName, args)
}

// executeGateStep evaluates a conditional gate
func (e *WorkflowEngine) executeGateStep(wf *Workflow, step *WorkflowStep) (map[string]any, error) {
	// Simple condition evaluation
	// In a full implementation, this would use a proper expression evaluator
	if step.Condition == "" {
		return map[string]any{"passed": true}, nil
	}

	// For now, just check if a context variable is truthy
	result := evaluateCondition(step.Condition, wf.Input, wf.Context)
	return map[string]any{"passed": result}, nil
}

// executeParallelStep executes multiple steps in parallel
func (e *WorkflowEngine) executeParallelStep(ctx context.Context, wf *Workflow, step *WorkflowStep) (map[string]any, error) {
	if len(step.ParallelSteps) == 0 {
		return map[string]any{}, nil
	}

	var wg sync.WaitGroup
	results := make(map[string]any)
	var mu sync.Mutex
	var firstErr error

	for i := range step.ParallelSteps {
		wg.Add(1)
		go func(ps *WorkflowStep) {
			defer wg.Done()

			var result map[string]any
			var err error

			switch ps.StepType {
			case StepTypeTool:
				result, err = e.executeToolStep(ctx, wf, ps)
			default:
				err = fmt.Errorf("unsupported parallel step type: %s", ps.StepType)
			}

			mu.Lock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			if result != nil {
				results[ps.ID] = result
			}
			mu.Unlock()
		}(&step.ParallelSteps[i])
	}

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

// executeSubflowStep executes a nested workflow
func (e *WorkflowEngine) executeSubflowStep(ctx context.Context, wf *Workflow, step *WorkflowStep) (map[string]any, error) {
	subWf, err := e.StartWorkflow(ctx, step.SubflowID, wf.SessionID, wf.AgentID, wf.Context)
	if err != nil {
		return nil, fmt.Errorf("failed to start subflow: %w", err)
	}

	// Wait for completion (poll)
	for {
		time.Sleep(100 * time.Millisecond)

		e.mu.RLock()
		status := subWf.Status
		output := subWf.Output
		subErr := subWf.Error
		e.mu.RUnlock()

		switch status {
		case WorkflowStatusCompleted:
			return output, nil
		case WorkflowStatusFailed, WorkflowStatusCancelled:
			return nil, fmt.Errorf("subflow failed: %s", subErr)
		}
	}
}

// requestApproval marks a step as waiting for approval
func (e *WorkflowEngine) requestApproval(wf *Workflow, stepID string) {
	e.mu.Lock()
	step := wf.StepStates[stepID]
	now := time.Now().UTC()
	step.Status = StepStatusWaiting
	step.ApprovalInfo = &ApprovalInfo{
		Status:      ApprovalStatusPending,
		RequestedAt: now,
	}
	wf.Status = WorkflowStatusWaiting
	e.mu.Unlock()

	e.emitEvent(WorkflowEvent{
		ID:         uuid.New().String()[:8],
		WorkflowID: wf.ID,
		StepID:     stepID,
		EventType:  "approval_requested",
		Timestamp:  now,
		Details:    map[string]any{"message": step.ApprovalMessage},
	})
}

// completeWorkflow marks the workflow as complete
func (e *WorkflowEngine) completeWorkflow(wf *Workflow, err error) {
	e.mu.Lock()
	now := time.Now().UTC()
	wf.CompletedAt = &now

	if err != nil {
		wf.Status = WorkflowStatusFailed
		wf.Error = err.Error()
	} else {
		wf.Status = WorkflowStatusCompleted
		// Collect outputs from all completed steps
		wf.Output = make(map[string]any)
		for stepID, step := range wf.StepStates {
			if step.Result != nil {
				wf.Output[stepID] = step.Result
			}
		}
	}
	e.mu.Unlock()

	eventType := "workflow_completed"
	if err != nil {
		eventType = "workflow_failed"
	}

	e.emitEvent(WorkflowEvent{
		ID:         uuid.New().String()[:8],
		WorkflowID: wf.ID,
		EventType:  eventType,
		Timestamp:  now,
		Details:    map[string]any{"error": errStr(err), "completed_steps": wf.CompletedSteps},
	})
}

// rollbackWorkflow executes rollback steps
func (e *WorkflowEngine) rollbackWorkflow(ctx context.Context, wf *Workflow) {
	e.mu.Lock()
	wf.Status = WorkflowStatusRolledBack
	e.mu.Unlock()

	e.emitEvent(WorkflowEvent{
		ID:         uuid.New().String()[:8],
		WorkflowID: wf.ID,
		EventType:  "rollback_started",
		Timestamp:  time.Now().UTC(),
	})

	// Execute rollback steps for completed steps in reverse order
	for i := len(wf.Definition.Steps) - 1; i >= 0; i-- {
		step := wf.StepStates[wf.Definition.Steps[i].ID]
		if step.Status == StepStatusCompleted && step.RollbackStepID != "" {
			rollbackStep, ok := wf.StepStates[step.RollbackStepID]
			if ok && rollbackStep.StepType == StepTypeTool {
				_, _ = e.executeToolStep(ctx, wf, rollbackStep)
			}
		}
	}

	e.emitEvent(WorkflowEvent{
		ID:         uuid.New().String()[:8],
		WorkflowID: wf.ID,
		EventType:  "rollback_completed",
		Timestamp:  time.Now().UTC(),
	})
}

// emitEvent emits a workflow event
func (e *WorkflowEngine) emitEvent(event WorkflowEvent) {
	e.mu.Lock()
	e.events[event.WorkflowID] = append(e.events[event.WorkflowID], event)
	cb := e.onEvent
	e.mu.Unlock()

	if cb != nil {
		cb(event)
	}
}

// Helper functions

func validateDAG(steps []WorkflowStep) error {
	// Build adjacency list
	stepMap := make(map[string]bool)
	for _, s := range steps {
		stepMap[s.ID] = true
	}

	// Check all dependencies exist and no cycles (simplified)
	for _, step := range steps {
		for _, dep := range step.DependsOn {
			if !stepMap[dep] {
				return fmt.Errorf("step %s depends on non-existent step %s", step.ID, dep)
			}
		}
	}

	// TODO: Full cycle detection with DFS
	return nil
}

func resolveVariables(args map[string]any, input, context map[string]any) map[string]any {
	if args == nil {
		return nil
	}

	result := make(map[string]any)
	for k, v := range args {
		switch val := v.(type) {
		case string:
			// Check for variable reference like "${input.key}" or "${step_id.result}"
			result[k] = resolveString(val, input, context)
		case map[string]any:
			result[k] = resolveVariables(val, input, context)
		default:
			result[k] = v
		}
	}
	return result
}

func resolveString(s string, input, context map[string]any) any {
	// Simple variable resolution: ${input.key} or ${step_id.key}
	if len(s) < 4 || s[0:2] != "${" || s[len(s)-1] != '}' {
		return s
	}

	ref := s[2 : len(s)-1]
	if len(ref) > 6 && ref[:6] == "input." {
		key := ref[6:]
		if val, ok := input[key]; ok {
			return val
		}
	} else if dotIdx := indexOf(ref, '.'); dotIdx > 0 {
		stepID := ref[:dotIdx]
		key := ref[dotIdx+1:]
		if stepResult, ok := context[stepID].(map[string]any); ok {
			if val, ok := stepResult[key]; ok {
				return val
			}
		}
	}

	return s // Return original if not resolved
}

func indexOf(s string, c byte) int {
	for i := range s {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func evaluateCondition(cond string, input, context map[string]any) bool {
	// Simple truthy check
	val := resolveString("${"+cond+"}", input, context)
	switch v := val.(type) {
	case bool:
		return v
	case string:
		return v != "" && v != "false" && v != "0"
	case int, int64, float64:
		return v != 0
	default:
		return val != nil
	}
}

func mapKeys(m map[string]any) []string {
	if m == nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ParseWorkflowDefinition parses a workflow definition from JSON
func ParseWorkflowDefinition(data []byte) (*WorkflowDefinition, error) {
	var def WorkflowDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("failed to parse workflow: %w", err)
	}
	return &def, nil
}
