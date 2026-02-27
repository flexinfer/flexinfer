package agentcontext

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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

		// Propagate skips from gate steps to dependents
		e.propagateSkips(wf)

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

// propagateSkips transitively marks pending steps as skipped if any of their
// dependencies were skipped (e.g., a gate that evaluated to false). This runs
// in the main executeWorkflow loop before findReadySteps to prevent downstream
// steps from executing past a failed gate.
func (e *WorkflowEngine) propagateSkips(wf *Workflow) {
	e.mu.Lock()
	defer e.mu.Unlock()

	changed := true
	for changed {
		changed = false
		for stepID, step := range wf.StepStates {
			if step.Status != StepStatusPending {
				continue
			}
			for _, depID := range step.DependsOn {
				depStep := wf.StepStates[depID]
				if depStep != nil && depStep.Status == StepStatusSkipped {
					step.Status = StepStatusSkipped
					step.Result = map[string]any{
						"skipped": true,
						"reason":  "upstream gate " + depID + " evaluated false",
					}
					wf.Context[stepID] = step.Result
					wf.CompletedSteps++
					changed = true
					break
				}
			}
		}
	}
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
	case StepTypeMapReduce:
		result, err = e.executeMapReduceStep(ctx, wf, step)
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
		// Check retry with exponential backoff
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

			// Calculate delay with exponential backoff
			delay := calculateBackoffDelay(step.RetryCount, step.RetryDelay)
			if delay > 0 {
				time.Sleep(delay)
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

	// Gate steps that evaluate to false are skipped, not completed.
	// propagateSkips will transitively skip downstream dependents.
	if step.StepType == StepTypeGate {
		if passed, ok := result["passed"].(bool); ok && !passed {
			step.Status = StepStatusSkipped
			step.Result = result
			wf.CompletedSteps++
			if result != nil {
				wf.Context[stepID] = result
			}
			e.mu.Unlock()

			e.emitEvent(WorkflowEvent{
				ID:         uuid.New().String()[:8],
				WorkflowID: wf.ID,
				StepID:     stepID,
				EventType:  "step_skipped",
				Timestamp:  completedAt,
				Details:    map[string]any{"condition": step.Condition, "passed": false},
			})
			return
		}
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

// executeGateStep evaluates a conditional gate. Returns {"passed": true/false}.
// When the condition evaluates to false, executeStep marks the step as skipped,
// and propagateSkips transitively skips all downstream dependents.
func (e *WorkflowEngine) executeGateStep(wf *Workflow, step *WorkflowStep) (map[string]any, error) {
	if step.Condition == "" {
		return map[string]any{"passed": true}, nil
	}

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

	// Wait for completion via done channel instead of polling
	select {
	case <-subWf.done:
		// Subflow completed (or failed)
	case <-ctx.Done():
		return nil, fmt.Errorf("subflow cancelled: %w", ctx.Err())
	}

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
	default:
		return nil, fmt.Errorf("subflow ended in unexpected status: %s", status)
	}
}

// executeMapReduceStep fans out a template step over items and optionally reduces.
func (e *WorkflowEngine) executeMapReduceStep(ctx context.Context, wf *Workflow, step *WorkflowStep) (map[string]any, error) {
	if step.MapStepTemplate == nil {
		return nil, fmt.Errorf("map_reduce step %s: map_step_template is required", step.ID)
	}
	if step.MapInputKey == "" {
		return nil, fmt.Errorf("map_reduce step %s: map_input_key is required", step.ID)
	}

	// Read items from workflow context
	e.mu.RLock()
	rawItems, ok := wf.Context[step.MapInputKey]
	e.mu.RUnlock()
	if !ok {
		return map[string]any{"map_results": []any{}, "count": 0}, nil
	}

	items, ok := rawItems.([]any)
	if !ok {
		return nil, fmt.Errorf("map_reduce step %s: %s must be []any, got %T", step.ID, step.MapInputKey, rawItems)
	}

	if len(items) == 0 {
		return map[string]any{"map_results": []any{}, "count": 0}, nil
	}

	// Determine concurrency bound
	concurrency := step.MaxConcurrency
	if concurrency <= 0 {
		concurrency = len(items)
	}
	sem := make(chan struct{}, concurrency)

	// Fan-out: execute template per item
	type indexedResult struct {
		idx    int
		result map[string]any
		err    error
	}
	resultCh := make(chan indexedResult, len(items))

	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		go func(idx int, itm any) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Clone template and inject ${item} variable
			tmpl := *step.MapStepTemplate
			tmpl.ToolArgs = injectItemVariable(tmpl.ToolArgs, itm, wf.Input, wf.Context)

			r, err := e.executeToolStep(ctx, wf, &tmpl)
			resultCh <- indexedResult{idx: idx, result: r, err: err}
		}(i, item)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results in order
	mapResults := make([]any, len(items))
	var firstErr error
	for ir := range resultCh {
		if ir.err != nil && firstErr == nil {
			firstErr = ir.err
		}
		if ir.result != nil {
			mapResults[ir.idx] = ir.result
		}
	}

	if firstErr != nil {
		return nil, fmt.Errorf("map_reduce step %s: map phase error: %w", step.ID, firstErr)
	}

	// Optional reduce phase
	if step.ReduceToolName != "" && e.toolExecutor != nil {
		reduceArgs := resolveVariables(step.ReduceToolArgs, wf.Input, wf.Context)
		if reduceArgs == nil {
			reduceArgs = make(map[string]any)
		}
		reduceArgs["map_results"] = mapResults

		reduceResult, err := e.toolExecutor(ctx, step.ReduceServerName, step.ReduceToolName, reduceArgs)
		if err != nil {
			return nil, fmt.Errorf("map_reduce step %s: reduce phase error: %w", step.ID, err)
		}
		return reduceResult, nil
	}

	return map[string]any{"map_results": mapResults, "count": len(mapResults)}, nil
}

// injectItemVariable clones args, resolves step/input variables, and replaces
// all "${item}" references (including in nested maps and slices) with the current item.
func injectItemVariable(args map[string]any, item any, input, context map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	resolved := resolveVariables(args, input, context)
	return injectItemInMap(resolved, item)
}

// injectItemInMap recursively replaces "${item}" in a map.
func injectItemInMap(m map[string]any, item any) map[string]any {
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = injectItemInValue(v, item)
	}
	return result
}

// injectItemInValue replaces "${item}" in a single value, recursing into maps and slices.
func injectItemInValue(v any, item any) any {
	switch val := v.(type) {
	case string:
		if val == "${item}" {
			return item
		}
		return val
	case map[string]any:
		return injectItemInMap(val, item)
	case []any:
		result := make([]any, len(val))
		for i, elem := range val {
			result[i] = injectItemInValue(elem, item)
		}
		return result
	default:
		return v
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

	// Signal subflow waiters
	if wf.done != nil {
		select {
		case <-wf.done:
			// Already closed
		default:
			close(wf.done)
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
				if _, err := e.executeToolStep(ctx, wf, rollbackStep); err != nil {
					e.logger.Warn("rollback step failed",
						"workflow_id", wf.ID,
						"step_id", wf.Definition.Steps[i].ID,
						"rollback_step_id", step.RollbackStepID,
						"error", err,
					)
					e.emitEvent(WorkflowEvent{
						ID:         uuid.New().String()[:8],
						WorkflowID: wf.ID,
						StepID:     step.RollbackStepID,
						EventType:  "rollback_step_failed",
						Timestamp:  time.Now().UTC(),
						Details:    map[string]any{"error": err.Error(), "original_step_id": wf.Definition.Steps[i].ID},
					})
				}
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
	// Build adjacency list and step map
	stepMap := make(map[string]bool)
	adjacency := make(map[string][]string)
	for _, s := range steps {
		stepMap[s.ID] = true
		adjacency[s.ID] = s.DependsOn
	}

	// Check all dependencies exist
	for _, step := range steps {
		for _, dep := range step.DependsOn {
			if !stepMap[dep] {
				return fmt.Errorf("step %s depends on non-existent step %s", step.ID, dep)
			}
		}
	}

	// Full cycle detection with DFS
	// States: 0 = unvisited, 1 = visiting, 2 = visited
	state := make(map[string]int)
	var path []string

	var dfs func(node string) error
	dfs = func(node string) error {
		if state[node] == 1 {
			// Found cycle - build cycle path
			cycleStart := -1
			for i, n := range path {
				if n == node {
					cycleStart = i
					break
				}
			}
			cyclePath := append(path[cycleStart:], node)
			return fmt.Errorf("cycle detected: %s", formatCycle(cyclePath))
		}
		if state[node] == 2 {
			return nil // Already visited
		}

		state[node] = 1 // Visiting
		path = append(path, node)

		for _, dep := range adjacency[node] {
			if err := dfs(dep); err != nil {
				return err
			}
		}

		path = path[:len(path)-1]
		state[node] = 2 // Visited
		return nil
	}

	// Run DFS from each unvisited node
	for stepID := range stepMap {
		if state[stepID] == 0 {
			if err := dfs(stepID); err != nil {
				return err
			}
		}
	}

	return nil
}

func formatCycle(cycle []string) string {
	result := ""
	for i, node := range cycle {
		if i > 0 {
			result += " -> "
		}
		result += node
	}
	return result
}

// calculateBackoffDelay returns the delay for a retry attempt using exponential backoff.
// baseDelay is in milliseconds. Returns time.Duration.
// Formula: min(baseDelay * 2^(attempt-1), maxDelay) with jitter
func calculateBackoffDelay(attempt int, baseDelayMs int) time.Duration {
	if baseDelayMs <= 0 {
		baseDelayMs = 1000 // Default 1 second
	}

	const maxDelayMs = 60000 // Max 1 minute

	// Calculate exponential delay: base * 2^(attempt-1)
	delayMs := baseDelayMs
	for i := 1; i < attempt; i++ {
		delayMs *= 2
		if delayMs > maxDelayMs {
			delayMs = maxDelayMs
			break
		}
	}

	// Add jitter (up to 25% of delay)
	jitterMs := delayMs / 4
	if jitterMs > 0 {
		// Simple pseudo-random jitter using time
		jitter := int(time.Now().UnixNano() % int64(jitterMs))
		delayMs += jitter
	}

	return time.Duration(delayMs) * time.Millisecond
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
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return true
	}

	// Handle boolean operators: split on AND/OR (left-to-right, no precedence)
	if parts := splitBoolOp(cond, " AND "); len(parts) > 1 {
		for _, p := range parts {
			if !evaluateCondition(p, input, context) {
				return false
			}
		}
		return true
	}
	if parts := splitBoolOp(cond, " OR "); len(parts) > 1 {
		for _, p := range parts {
			if evaluateCondition(p, input, context) {
				return true
			}
		}
		return false
	}

	// Handle EXISTS operator: "<ref> EXISTS"
	if strings.HasSuffix(cond, " EXISTS") {
		ref := strings.TrimSuffix(cond, " EXISTS")
		ref = strings.TrimSpace(ref)
		val := resolveRef(ref, input, context)
		return val != nil
	}

	// Handle comparison operators: >=, <=, !=, >, <, ==
	for _, op := range []string{">=", "<=", "!=", ">", "<", "=="} {
		if idx := strings.Index(cond, " "+op+" "); idx >= 0 {
			left := strings.TrimSpace(cond[:idx])
			right := strings.TrimSpace(cond[idx+len(op)+2:])
			lval := resolveRef(left, input, context)
			rval := parseCondValue(right)
			return compareValues(lval, rval, op)
		}
	}

	// Fallback: simple truthy check on resolved reference
	val := resolveRef(cond, input, context)
	return isTruthy(val)
}

// resolveRef resolves a dotted reference like "step_id.field" or "input.key"
// against the input and context maps. Returns nil if unresolved.
func resolveRef(ref string, input, context map[string]any) any {
	ref = strings.TrimSpace(ref)
	val := resolveString("${"+ref+"}", input, context)
	if s, ok := val.(string); ok && s == "${"+ref+"}" {
		return nil // unresolved
	}
	return val
}

// parseCondValue parses a literal value from a condition expression.
func parseCondValue(s string) any {
	s = strings.TrimSpace(s)
	if s == "true" {
		return true
	}
	if s == "false" {
		return false
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// compareValues compares two values using the given operator.
func compareValues(left, right any, op string) bool {
	lf, lok := condToFloat64(left)
	rf, rok := condToFloat64(right)

	if lok && rok {
		switch op {
		case ">":
			return lf > rf
		case ">=":
			return lf >= rf
		case "<":
			return lf < rf
		case "<=":
			return lf <= rf
		case "==":
			return lf == rf
		case "!=":
			return lf != rf
		}
	}

	ls := fmt.Sprintf("%v", left)
	rs := fmt.Sprintf("%v", right)
	switch op {
	case "==":
		return ls == rs
	case "!=":
		return ls != rs
	default:
		return false
	}
}

// condToFloat64 attempts to convert any to float64 for condition comparisons.
func condToFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// isTruthy checks if a value is truthy.
func isTruthy(val any) bool {
	if val == nil {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		return v != "" && v != "false" && v != "0"
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	default:
		return true
	}
}

// splitBoolOp splits a condition string on a boolean operator while respecting
// single-quoted string boundaries. This prevents splitting on operators that
// appear inside string literals (e.g., "name == 'CONNECT AND PLAY' AND ok").
func splitBoolOp(cond, op string) []string {
	var parts []string
	inQuote := false
	start := 0
	for i := 0; i < len(cond); i++ {
		if cond[i] == '\'' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && i+len(op) <= len(cond) && cond[i:i+len(op)] == op {
			if part := strings.TrimSpace(cond[start:i]); part != "" {
				parts = append(parts, part)
			}
			start = i + len(op)
			i += len(op) - 1
		}
	}
	if last := strings.TrimSpace(cond[start:]); last != "" {
		parts = append(parts, last)
	}
	return parts
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

// =========================================================================
// Persistence Layer (Phase 1.2)
// =========================================================================

// WorkflowPersistenceConfig holds Qdrant clients for workflow persistence
type WorkflowPersistenceConfig struct {
	WorkflowsQdrant    *QdrantClient
	WorkflowDefsQdrant *QdrantClient
}

// persistedWorkflowEngine wraps WorkflowEngine with Qdrant persistence
type persistedWorkflowEngine struct {
	*WorkflowEngine
	cfg *WorkflowPersistenceConfig
}

// SetPersistence configures Qdrant persistence for the workflow engine
func (e *WorkflowEngine) SetPersistence(cfg *WorkflowPersistenceConfig) *persistedWorkflowEngine {
	return &persistedWorkflowEngine{
		WorkflowEngine: e,
		cfg:            cfg,
	}
}

// dummyWorkflowVector is used for workflows (no semantic search needed)
var dummyWorkflowVector = []float64{0, 0, 0, 0}

// PersistWorkflow saves a workflow to Qdrant
func (pwe *persistedWorkflowEngine) PersistWorkflow(ctx context.Context, wf *Workflow) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowsQdrant == nil {
		return nil // No persistence configured
	}

	// Ensure collection exists (using minimal vector size)
	if err := pwe.cfg.WorkflowsQdrant.EnsureCollection(ctx, 4); err != nil {
		return fmt.Errorf("ensure workflows collection: %w", err)
	}

	point := Point{
		ID:      wf.ID,
		Vector:  dummyWorkflowVector,
		Payload: WorkflowToPayload(*wf),
	}

	if err := pwe.cfg.WorkflowsQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return fmt.Errorf("persist workflow: %w", err)
	}

	return nil
}

// PersistDefinition saves a workflow definition to Qdrant
func (pwe *persistedWorkflowEngine) PersistDefinition(ctx context.Context, def *WorkflowDefinition) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowDefsQdrant == nil {
		return nil // No persistence configured
	}

	// Ensure collection exists
	if err := pwe.cfg.WorkflowDefsQdrant.EnsureCollection(ctx, 4); err != nil {
		return fmt.Errorf("ensure workflow definitions collection: %w", err)
	}

	point := Point{
		ID:      def.ID,
		Vector:  dummyWorkflowVector,
		Payload: WorkflowDefinitionToPayload(*def),
	}

	if err := pwe.cfg.WorkflowDefsQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return fmt.Errorf("persist workflow definition: %w", err)
	}

	return nil
}

// DeletePersistedWorkflow removes a workflow from Qdrant
func (pwe *persistedWorkflowEngine) DeletePersistedWorkflow(ctx context.Context, id string) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowsQdrant == nil {
		return nil
	}
	return pwe.cfg.WorkflowsQdrant.Delete(ctx, []string{id})
}

// DeletePersistedDefinition removes a workflow definition from Qdrant
func (pwe *persistedWorkflowEngine) DeletePersistedDefinition(ctx context.Context, id string) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowDefsQdrant == nil {
		return nil
	}
	return pwe.cfg.WorkflowDefsQdrant.Delete(ctx, []string{id})
}

// LoadWorkflowsFromQdrant loads all workflows from Qdrant into memory
func (pwe *persistedWorkflowEngine) LoadWorkflowsFromQdrant(ctx context.Context) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowsQdrant == nil {
		return nil
	}

	exists, err := pwe.cfg.WorkflowsQdrant.CollectionExists(ctx)
	if err != nil {
		return fmt.Errorf("check workflows collection: %w", err)
	}
	if !exists {
		return nil
	}

	points, err := pwe.cfg.WorkflowsQdrant.ScrollPoints(ctx, nil, 10000, false)
	if err != nil {
		return fmt.Errorf("load workflows: %w", err)
	}

	pwe.mu.Lock()
	defer pwe.mu.Unlock()

	for _, p := range points {
		wf, err := PayloadToWorkflow(p.Payload)
		if err != nil || wf == nil {
			continue
		}
		pwe.workflows[wf.ID] = wf
		pwe.events[wf.ID] = []WorkflowEvent{} // Events not persisted
	}

	return nil
}

// LoadDefinitionsFromQdrant loads all workflow definitions from Qdrant into memory
func (pwe *persistedWorkflowEngine) LoadDefinitionsFromQdrant(ctx context.Context) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowDefsQdrant == nil {
		return nil
	}

	exists, err := pwe.cfg.WorkflowDefsQdrant.CollectionExists(ctx)
	if err != nil {
		return fmt.Errorf("check workflow definitions collection: %w", err)
	}
	if !exists {
		return nil
	}

	points, err := pwe.cfg.WorkflowDefsQdrant.ScrollPoints(ctx, nil, 10000, false)
	if err != nil {
		return fmt.Errorf("load workflow definitions: %w", err)
	}

	pwe.mu.Lock()
	defer pwe.mu.Unlock()

	for _, p := range points {
		def, err := PayloadToWorkflowDefinition(p.Payload)
		if err != nil || def == nil {
			continue
		}
		pwe.definitions[def.ID] = def
	}

	return nil
}

// RegisterDefinitionWithPersistence registers and persists a workflow definition
func (pwe *persistedWorkflowEngine) RegisterDefinitionWithPersistence(ctx context.Context, def *WorkflowDefinition) error {
	if err := pwe.RegisterDefinition(def); err != nil {
		return err
	}
	return pwe.PersistDefinition(ctx, def)
}

// StartWorkflowWithPersistence starts a workflow and persists it
func (pwe *persistedWorkflowEngine) StartWorkflowWithPersistence(ctx context.Context, definitionID, sessionID, agentID string, input map[string]any) (*Workflow, error) {
	wf, err := pwe.StartWorkflow(ctx, definitionID, sessionID, agentID, input)
	if err != nil {
		return nil, err
	}

	// Persist the initial workflow state
	if err := pwe.PersistWorkflow(ctx, wf); err != nil {
		// Non-fatal - workflow is running in memory
		fmt.Printf("warning: failed to persist workflow: %v\n", err)
	}

	return wf, nil
}

// CheckpointWorkflow saves the current workflow state to Qdrant
// This should be called after each step completion
func (pwe *persistedWorkflowEngine) CheckpointWorkflow(ctx context.Context, workflowID string) error {
	pwe.mu.RLock()
	wf, ok := pwe.workflows[workflowID]
	pwe.mu.RUnlock()

	if !ok {
		return fmt.Errorf("workflow not found: %s", workflowID)
	}

	return pwe.PersistWorkflow(ctx, wf)
}

// ResumeInProgressWorkflows resumes workflows that were in-progress when service restarted
func (pwe *persistedWorkflowEngine) ResumeInProgressWorkflows(ctx context.Context) error {
	pwe.mu.RLock()
	var toResume []*Workflow
	for _, wf := range pwe.workflows {
		if wf.Status == WorkflowStatusRunning || wf.Status == WorkflowStatusWaiting {
			toResume = append(toResume, wf)
		}
	}
	pwe.mu.RUnlock()

	for _, wf := range toResume {
		// Only resume running workflows, not waiting ones (they need user action)
		if wf.Status == WorkflowStatusRunning {
			go pwe.executeWorkflow(ctx, wf)
		}
	}

	return nil
}
