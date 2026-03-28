// workflow_executor.go — workflow DAG execution, step dispatch, rollback, and event emission.
package agentcontext

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

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
