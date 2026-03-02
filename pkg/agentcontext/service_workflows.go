package agentcontext

import (
	"context"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

type WorkflowSvc struct{ *Service }

// SetToolExecutor sets the callback for executing MCP tools from workflows
func (s *WorkflowSvc) SetToolExecutor(executor ToolExecutor) {
	s.workflowEngine.toolExecutor = executor
}

// GetWorkflowEngine returns the workflow engine for direct access
func (s *WorkflowSvc) GetWorkflowEngine() *WorkflowEngine {
	return s.workflowEngine
}

// HandleWorkflowDefine registers a new workflow definition
func (s *WorkflowSvc) HandleWorkflowDefine(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	id := v.String("id", "")
	name := v.Required("name")
	description := v.String("description", "")
	namespace := v.String("namespace", s.cfg.DefaultNamespace)
	createdBy := v.String("created_by", s.cfg.DefaultAgentID)
	stepsRaw := v.RequiredAny("steps")
	rollbackOnFailure := v.Bool("rollback_on_failure", false)
	timeoutSeconds := v.Int("timeout_seconds", 0)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Parse steps
	stepsArr, ok := stepsRaw.([]any)
	if !ok || len(stepsArr) == 0 {
		return mcp.ErrorResult(fmt.Errorf("steps array is required")), nil
	}

	steps := make([]WorkflowStep, len(stepsArr))
	for i, stepRaw := range stepsArr {
		stepMap, ok := stepRaw.(map[string]any)
		if !ok {
			return mcp.ErrorResult(fmt.Errorf("step %d must be an object", i)), nil
		}

		step := WorkflowStep{
			ID:               toString(stepMap["id"]),
			Name:             toString(stepMap["name"]),
			Description:      toString(stepMap["description"]),
			StepType:         StepType(toString(stepMap["step_type"])),
			ToolName:         toString(stepMap["tool_name"]),
			ServerName:       toString(stepMap["server_name"]),
			RequiresApproval: getBool(stepMap["requires_approval"], false),
			ApprovalMessage:  toString(stepMap["approval_message"]),
			Condition:        toString(stepMap["condition"]),
			MaxRetries:       toInt(stepMap["max_retries"]),
			RetryDelay:       toInt(stepMap["retry_delay_ms"]),
			Timeout:          toInt(stepMap["timeout_seconds"]),
			RollbackStepID:   toString(stepMap["rollback_step_id"]),
			SubflowID:        toString(stepMap["subflow_id"]),
		}

		if step.ID == "" {
			step.ID = fmt.Sprintf("step-%d", i+1)
		}
		if step.StepType == "" {
			step.StepType = StepTypeTool
		}

		// Parse tool args
		if toolArgs, ok := stepMap["tool_args"].(map[string]any); ok {
			step.ToolArgs = toolArgs
		}

		// Parse depends_on
		if deps, ok := stepMap["depends_on"].([]any); ok {
			step.DependsOn = make([]string, len(deps))
			for j, dep := range deps {
				step.DependsOn[j] = toString(dep)
			}
		}

		steps[i] = step
	}

	def := &WorkflowDefinition{
		ID:                id,
		Name:              name,
		Description:       description,
		Namespace:         namespace,
		CreatedBy:         createdBy,
		Steps:             steps,
		RollbackOnFailure: rollbackOnFailure,
		TimeoutSeconds:    timeoutSeconds,
	}

	// Parse input schema if provided
	if schema, ok := args["input_schema"].(map[string]any); ok {
		def.InputSchema = schema
	}

	if err := s.persistedWorkflowEngine.RegisterDefinitionWithPersistence(ctx, def); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":            true,
		"definition_id": def.ID,
		"name":          def.Name,
		"step_count":    len(def.Steps),
	})
}

// HandleWorkflowStart starts a new workflow instance
func (s *WorkflowSvc) HandleWorkflowStart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	definitionID := v.Required("definition_id")
	sessionID := v.Required("session_id")
	agentID := v.String("agent_id", s.cfg.DefaultAgentID)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Parse input
	var input map[string]any
	if inputRaw, ok := args["input"].(map[string]any); ok {
		input = inputRaw
	}

	wf, err := s.persistedWorkflowEngine.StartWorkflowWithPersistence(ctx, definitionID, sessionID, agentID, input)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	s.metrics.WorkflowsStarted.Add(1)

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"workflow_id": wf.ID,
		"name":        wf.Definition.Name,
		"status":      string(wf.Status),
		"total_steps": wf.TotalSteps,
	})
}

// HandleWorkflowStatus gets the status of a workflow
func (s *WorkflowSvc) HandleWorkflowStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	workflowID := v.Required("workflow_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	wf, err := s.workflowEngine.GetWorkflow(workflowID)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	progress := 0.0
	if wf.TotalSteps > 0 {
		progress = float64(wf.CompletedSteps) / float64(wf.TotalSteps)
	}

	// Build step summaries
	stepSummaries := make([]map[string]any, 0, len(wf.StepStates))
	for _, step := range wf.Definition.Steps {
		state := wf.StepStates[step.ID]
		summary := map[string]any{
			"id":     step.ID,
			"name":   step.Name,
			"type":   string(step.StepType),
			"status": string(state.Status),
		}
		if state.Error != "" {
			summary["error"] = state.Error
		}
		if state.ApprovalInfo != nil {
			summary["approval_status"] = string(state.ApprovalInfo.Status)
		}
		stepSummaries = append(stepSummaries, summary)
	}

	result := map[string]any{
		"workflow_id":     wf.ID,
		"name":            wf.Definition.Name,
		"status":          string(wf.Status),
		"current_step":    wf.CurrentStep,
		"progress":        progress,
		"completed_steps": wf.CompletedSteps,
		"total_steps":     wf.TotalSteps,
		"steps":           stepSummaries,
		"created_at":      wf.CreatedAt.Format(time.RFC3339Nano),
	}

	if wf.Error != "" {
		result["error"] = wf.Error
	}
	if wf.StartedAt != nil {
		result["started_at"] = wf.StartedAt.Format(time.RFC3339Nano)
	}
	if wf.CompletedAt != nil {
		result["completed_at"] = wf.CompletedAt.Format(time.RFC3339Nano)
	}

	// Include output if completed
	if wf.Status == WorkflowStatusCompleted && wf.Output != nil {
		result["output"] = wf.Output
	}

	return mcp.JSONResult(result)
}

// HandleWorkflowList lists workflows with filtering
func (s *WorkflowSvc) HandleWorkflowList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.String("session_id", "")
	agentID := v.String("agent_id", s.cfg.DefaultAgentID)
	statusStr := v.String("status", "")

	status := WorkflowStatus(statusStr)

	workflows := s.workflowEngine.ListWorkflows(sessionID, agentID, status)

	results := make([]map[string]any, len(workflows))
	for i, wf := range workflows {
		results[i] = map[string]any{
			"workflow_id":  wf.ID,
			"name":         wf.Name,
			"status":       string(wf.Status),
			"progress":     wf.Progress,
			"current_step": wf.CurrentStep,
			"created_at":   wf.CreatedAt.Format(time.RFC3339Nano),
		}
		if wf.Error != "" {
			results[i]["error"] = wf.Error
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"count":     len(results),
		"workflows": results,
	})
}

// HandleWorkflowApprove approves a pending step
func (s *WorkflowSvc) HandleWorkflowApprove(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	workflowID := v.Required("workflow_id")
	stepID := v.Required("step_id")
	approverID := v.String("approver_id", s.cfg.DefaultAgentID)
	comment := v.String("comment", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if err := s.workflowEngine.ApproveStep(workflowID, stepID, approverID, comment); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Checkpoint workflow state to Qdrant after approval mutation
	if err := s.persistedWorkflowEngine.CheckpointWorkflow(ctx, workflowID); err != nil {
		s.logger.Warn("failed to checkpoint workflow after approve", "workflow_id", workflowID, "error", err)
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"workflow_id": workflowID,
		"step_id":     stepID,
		"approved_by": approverID,
	})
}

// HandleWorkflowReject rejects a pending step
func (s *WorkflowSvc) HandleWorkflowReject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	workflowID := v.Required("workflow_id")
	stepID := v.Required("step_id")
	rejecterID := v.String("rejecter_id", s.cfg.DefaultAgentID)
	comment := v.String("comment", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if err := s.workflowEngine.RejectStep(workflowID, stepID, rejecterID, comment); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Checkpoint workflow state to Qdrant after rejection mutation
	if err := s.persistedWorkflowEngine.CheckpointWorkflow(ctx, workflowID); err != nil {
		s.logger.Warn("failed to checkpoint workflow after reject", "workflow_id", workflowID, "error", err)
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"workflow_id": workflowID,
		"step_id":     stepID,
		"rejected_by": rejecterID,
	})
}

// HandleWorkflowCancel cancels a running workflow
func (s *WorkflowSvc) HandleWorkflowCancel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	workflowID := v.Required("workflow_id")
	reason := v.String("reason", "cancelled by user")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if err := s.workflowEngine.CancelWorkflow(workflowID, reason); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Checkpoint workflow state to Qdrant after cancellation
	if err := s.persistedWorkflowEngine.CheckpointWorkflow(ctx, workflowID); err != nil {
		s.logger.Warn("failed to checkpoint workflow after cancel", "workflow_id", workflowID, "error", err)
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"workflow_id": workflowID,
		"reason":      reason,
	})
}

// HandleWorkflowEvents gets events for a workflow
func (s *WorkflowSvc) HandleWorkflowEvents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	workflowID := v.Required("workflow_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	events, err := s.workflowEngine.GetEvents(workflowID)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	results := make([]map[string]any, len(events))
	for i, e := range events {
		results[i] = map[string]any{
			"id":         e.ID,
			"event_type": e.EventType,
			"timestamp":  e.Timestamp.Format(time.RFC3339Nano),
		}
		if e.StepID != "" {
			results[i]["step_id"] = e.StepID
		}
		if e.Details != nil {
			results[i]["details"] = e.Details
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"workflow_id": workflowID,
		"count":       len(results),
		"events":      results,
	})
}

// HandleWorkflowDefinitionList lists workflow definitions
func (s *WorkflowSvc) HandleWorkflowDefinitionList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", "")

	definitions := s.workflowEngine.ListDefinitions(namespace)

	results := make([]map[string]any, len(definitions))
	for i, def := range definitions {
		results[i] = map[string]any{
			"id":          def.ID,
			"name":        def.Name,
			"description": def.Description,
			"namespace":   def.Namespace,
			"step_count":  len(def.Steps),
			"created_by":  def.CreatedBy,
			"created_at":  def.CreatedAt.Format(time.RFC3339Nano),
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"count":       len(results),
		"definitions": results,
	})
}
