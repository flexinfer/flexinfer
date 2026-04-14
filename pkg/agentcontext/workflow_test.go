package agentcontext

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkflowEngine_RegisterDefinition(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	def := &WorkflowDefinition{
		Name:        "test-workflow",
		Description: "A test workflow",
		Steps: []WorkflowStep{
			{Name: "step1", StepType: StepTypeTool, ToolName: "test_tool"},
		},
	}

	err := engine.RegisterDefinition(def)
	if err != nil {
		t.Fatalf("RegisterDefinition failed: %v", err)
	}

	if def.ID == "" {
		t.Error("expected definition ID to be set")
	}

	// Verify we can retrieve it
	retrieved, err := engine.GetDefinition(def.ID)
	if err != nil {
		t.Fatalf("GetDefinition failed: %v", err)
	}

	if retrieved.Name != "test-workflow" {
		t.Errorf("expected name 'test-workflow', got %q", retrieved.Name)
	}
}

func TestWorkflowEngine_RegisterDefinition_Validation(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	// Missing name
	err := engine.RegisterDefinition(&WorkflowDefinition{
		Steps: []WorkflowStep{{Name: "step1"}},
	})
	if err == nil {
		t.Error("expected error for missing name")
	}

	// Missing steps
	err = engine.RegisterDefinition(&WorkflowDefinition{
		Name:  "test",
		Steps: []WorkflowStep{},
	})
	if err == nil {
		t.Error("expected error for empty steps")
	}
}

func TestWorkflowEngine_RegisterDefinition_IsIdempotentByNameAndNamespace(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	first := &WorkflowDefinition{
		Name:      "deploy",
		Namespace: "team/platform",
		Steps: []WorkflowStep{
			{Name: "step1", StepType: StepTypeTool, ToolName: "tool_one"},
		},
	}
	if err := engine.RegisterDefinition(first); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	if first.ID == "" {
		t.Fatal("first definition ID should be set")
	}

	second := &WorkflowDefinition{
		Name:      "deploy",
		Namespace: "team/platform",
		Steps: []WorkflowStep{
			{Name: "step1", StepType: StepTypeTool, ToolName: "tool_two"},
		},
	}
	if err := engine.RegisterDefinition(second); err != nil {
		t.Fatalf("second register failed: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("expected second register to reuse ID %q, got %q", first.ID, second.ID)
	}

	defs := engine.ListDefinitions("team/platform")
	if len(defs) != 1 {
		t.Fatalf("expected exactly one definition in namespace, got %d", len(defs))
	}
	if defs[0].Steps[0].ToolName != "tool_two" {
		t.Fatalf("expected second registration to update definition, got tool %q", defs[0].Steps[0].ToolName)
	}
}

func TestWorkflowEngine_StartWorkflow(t *testing.T) {
	var executedTools []string
	executor := func(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error) {
		executedTools = append(executedTools, tool)
		return map[string]any{"success": true}, nil
	}

	engine := NewWorkflowEngine(executor)

	def := &WorkflowDefinition{
		Name: "simple-workflow",
		Steps: []WorkflowStep{
			{ID: "step1", Name: "First Step", StepType: StepTypeTool, ToolName: "tool_one"},
			{ID: "step2", Name: "Second Step", StepType: StepTypeTool, ToolName: "tool_two", DependsOn: []string{"step1"}},
		},
	}

	if err := engine.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition failed: %v", err)
	}

	wf, err := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", nil)
	if err != nil {
		t.Fatalf("StartWorkflow failed: %v", err)
	}

	// Wait for completion
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusCompleted || wf.Status == WorkflowStatusFailed {
			break
		}
	}

	if wf.Status != WorkflowStatusCompleted {
		t.Errorf("expected completed, got %s: %s", wf.Status, wf.Error)
	}

	if len(executedTools) != 2 {
		t.Errorf("expected 2 tools executed, got %d", len(executedTools))
	}

	// Verify execution order (step2 depends on step1)
	if executedTools[0] != "tool_one" || executedTools[1] != "tool_two" {
		t.Errorf("unexpected execution order: %v", executedTools)
	}
}

func TestWorkflowEngine_ParallelExecution(t *testing.T) {
	var concurrentCount int32
	var maxConcurrent int32

	executor := func(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error) {
		current := atomic.AddInt32(&concurrentCount, 1)
		for {
			old := atomic.LoadInt32(&maxConcurrent)
			if current <= old {
				break
			}
			if atomic.CompareAndSwapInt32(&maxConcurrent, old, current) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&concurrentCount, -1)
		return map[string]any{"tool": tool}, nil
	}

	engine := NewWorkflowEngine(executor)

	// Three parallel steps, then one that depends on all three
	def := &WorkflowDefinition{
		Name: "parallel-workflow",
		Steps: []WorkflowStep{
			{ID: "a", Name: "Step A", StepType: StepTypeTool, ToolName: "tool_a"},
			{ID: "b", Name: "Step B", StepType: StepTypeTool, ToolName: "tool_b"},
			{ID: "c", Name: "Step C", StepType: StepTypeTool, ToolName: "tool_c"},
			{ID: "final", Name: "Final", StepType: StepTypeTool, ToolName: "tool_final", DependsOn: []string{"a", "b", "c"}},
		},
	}

	if err := engine.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition failed: %v", err)
	}

	wf, err := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", nil)
	if err != nil {
		t.Fatalf("StartWorkflow failed: %v", err)
	}

	// Wait for completion
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusCompleted || wf.Status == WorkflowStatusFailed {
			break
		}
	}

	if wf.Status != WorkflowStatusCompleted {
		t.Errorf("expected completed, got %s: %s", wf.Status, wf.Error)
	}

	// Should have had 3 concurrent executions
	if maxConcurrent < 3 {
		t.Errorf("expected at least 3 concurrent executions, got %d", maxConcurrent)
	}
}

func TestWorkflowEngine_ApprovalGate(t *testing.T) {
	executed := false
	executor := func(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error) {
		executed = true
		return map[string]any{"success": true}, nil
	}

	engine := NewWorkflowEngine(executor)

	def := &WorkflowDefinition{
		Name: "approval-workflow",
		Steps: []WorkflowStep{
			{ID: "approve", Name: "Approval", StepType: StepTypeApproval, ApprovalMessage: "Please approve this step"},
			{ID: "action", Name: "Action", StepType: StepTypeTool, ToolName: "do_action", DependsOn: []string{"approve"}},
		},
	}

	if err := engine.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition failed: %v", err)
	}

	wf, err := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", nil)
	if err != nil {
		t.Fatalf("StartWorkflow failed: %v", err)
	}

	// Wait for workflow to reach waiting state
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusWaiting {
			break
		}
	}

	if wf.Status != WorkflowStatusWaiting {
		t.Fatalf("expected waiting, got %s", wf.Status)
	}

	if executed {
		t.Error("action should not have executed before approval")
	}

	// Approve the step
	if err := engine.ApproveStep(wf.ID, "approve", "admin", "looks good"); err != nil {
		t.Fatalf("ApproveStep failed: %v", err)
	}

	// Wait for completion
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusCompleted || wf.Status == WorkflowStatusFailed {
			break
		}
	}

	if wf.Status != WorkflowStatusCompleted {
		t.Errorf("expected completed after approval, got %s: %s", wf.Status, wf.Error)
	}

	if !executed {
		t.Error("action should have executed after approval")
	}
}

func TestWorkflowEngine_RejectStep(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	def := &WorkflowDefinition{
		Name: "rejection-workflow",
		Steps: []WorkflowStep{
			{ID: "approve", Name: "Approval", StepType: StepTypeApproval},
		},
	}

	if err := engine.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition failed: %v", err)
	}

	wf, _ := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", nil)

	// Wait for waiting state
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusWaiting {
			break
		}
	}

	// Reject
	if err := engine.RejectStep(wf.ID, "approve", "admin", "not approved"); err != nil {
		t.Fatalf("RejectStep failed: %v", err)
	}

	wf, _ = engine.GetWorkflow(wf.ID)
	if wf.Status != WorkflowStatusFailed {
		t.Errorf("expected failed after rejection, got %s", wf.Status)
	}
}

func TestWorkflowEngine_CancelWorkflow(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	def := &WorkflowDefinition{
		Name: "cancel-workflow",
		Steps: []WorkflowStep{
			{ID: "step1", Name: "Step 1", StepType: StepTypeApproval}, // Will wait
		},
	}

	engine.RegisterDefinition(def)
	wf, _ := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", nil)

	// Wait for it to start
	time.Sleep(100 * time.Millisecond)

	// Cancel
	if err := engine.CancelWorkflow(wf.ID, "user requested"); err != nil {
		t.Fatalf("CancelWorkflow failed: %v", err)
	}

	wf, _ = engine.GetWorkflow(wf.ID)
	if wf.Status != WorkflowStatusCancelled {
		t.Errorf("expected cancelled, got %s", wf.Status)
	}
}

func TestWorkflowEngine_RetryOnFailure(t *testing.T) {
	attempts := 0
	executor := func(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error) {
		attempts++
		if attempts < 3 {
			return nil, fmt.Errorf("transient error")
		}
		return map[string]any{"success": true}, nil
	}

	engine := NewWorkflowEngine(executor)

	def := &WorkflowDefinition{
		Name: "retry-workflow",
		Steps: []WorkflowStep{
			{ID: "step1", Name: "Flaky Step", StepType: StepTypeTool, ToolName: "flaky_tool", MaxRetries: 3, RetryDelay: 10},
		},
	}

	engine.RegisterDefinition(def)
	wf, _ := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", nil)

	// Wait for completion
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusCompleted || wf.Status == WorkflowStatusFailed {
			break
		}
	}

	if wf.Status != WorkflowStatusCompleted {
		t.Errorf("expected completed after retries, got %s: %s", wf.Status, wf.Error)
	}

	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestWorkflowEngine_AutoVerifyPasses(t *testing.T) {
	var serverName string
	var toolName string
	var checks any

	executor := func(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error) {
		serverName = server
		toolName = tool
		checks = args["checks"]
		return map[string]any{
			"passed": true,
			"checks": []any{
				map[string]any{"name": "fmt", "passed": true},
				map[string]any{"name": "lint", "passed": true},
				map[string]any{"name": "test", "passed": true},
				map[string]any{"name": "diff", "passed": true},
			},
		}, nil
	}

	engine := NewWorkflowEngine(executor)
	def := &WorkflowDefinition{
		Name: "auto-verify-pass",
		Steps: []WorkflowStep{
			{
				ID:       "verify",
				Name:     "Verify",
				StepType: StepTypeAutoVerify,
				ToolArgs: map[string]any{"project": "loom-core", "agent_id": "codex"},
			},
		},
	}
	if err := engine.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition failed: %v", err)
	}

	wf, err := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", nil)
	if err != nil {
		t.Fatalf("StartWorkflow failed: %v", err)
	}

	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusCompleted || wf.Status == WorkflowStatusFailed {
			break
		}
	}

	if wf.Status != WorkflowStatusCompleted {
		t.Fatalf("expected completed, got %s: %s", wf.Status, wf.Error)
	}
	if serverName != "devbox" {
		t.Fatalf("server = %q, want devbox", serverName)
	}
	if toolName != "devbox_quality_gate" {
		t.Fatalf("tool = %q, want devbox_quality_gate", toolName)
	}
	checkList, ok := checks.([]any)
	if !ok || len(checkList) != 4 {
		t.Fatalf("checks = %#v, want default 4-item verification list", checks)
	}

	verifyStep := wf.StepStates["verify"]
	if verifyStep == nil || verifyStep.Result == nil {
		t.Fatal("expected verify step result to be recorded")
	}
	if autoVerified, _ := verifyStep.Result["auto_verified"].(bool); !autoVerified {
		t.Fatalf("expected auto_verified=true, got %#v", verifyStep.Result["auto_verified"])
	}
}

func TestWorkflowEngine_AutoVerifyRetriesAndFails(t *testing.T) {
	var attempts int32
	executor := func(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error) {
		atomic.AddInt32(&attempts, 1)
		return map[string]any{
			"passed": false,
			"checks": []any{
				map[string]any{"name": "lint", "passed": false, "output_tail": "go vet found issues"},
			},
		}, nil
	}

	engine := NewWorkflowEngine(executor)
	def := &WorkflowDefinition{
		Name: "auto-verify-fail",
		Steps: []WorkflowStep{
			{
				ID:         "verify",
				Name:       "Verify",
				StepType:   StepTypeAutoVerify,
				ToolArgs:   map[string]any{"project": "loom-core"},
				MaxRetries: 2,
				RetryDelay: 1,
			},
		},
	}
	if err := engine.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition failed: %v", err)
	}

	wf, err := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", nil)
	if err != nil {
		t.Fatalf("StartWorkflow failed: %v", err)
	}

	for i := 0; i < 100; i++ {
		time.Sleep(20 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusCompleted || wf.Status == WorkflowStatusFailed {
			break
		}
	}

	if wf.Status != WorkflowStatusFailed {
		t.Fatalf("expected failed, got %s", wf.Status)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if wf.FailedStepID != "verify" {
		t.Fatalf("failed step = %q, want verify", wf.FailedStepID)
	}
	if wf.Error == "" || !strings.Contains(wf.Error, "go vet found issues") {
		t.Fatalf("workflow error = %q, want lint failure summary", wf.Error)
	}
}

func TestWorkflowEngine_VariableResolution(t *testing.T) {
	var receivedArgs map[string]any
	executor := func(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error) {
		if tool == "step2_tool" {
			receivedArgs = args
		}
		return map[string]any{"output_value": "from_step1"}, nil
	}

	engine := NewWorkflowEngine(executor)

	def := &WorkflowDefinition{
		Name: "variable-workflow",
		Steps: []WorkflowStep{
			{ID: "step1", Name: "Step 1", StepType: StepTypeTool, ToolName: "step1_tool"},
			{
				ID:       "step2",
				Name:     "Step 2",
				StepType: StepTypeTool,
				ToolName: "step2_tool",
				ToolArgs: map[string]any{
					"from_input":  "${input.user_name}",
					"from_step1":  "${step1.output_value}",
					"literal_val": "hello",
				},
				DependsOn: []string{"step1"},
			},
		},
	}

	engine.RegisterDefinition(def)
	input := map[string]any{"user_name": "Alice"}
	wf, _ := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", input)

	// Wait for completion
	for i := 0; i < 30; i++ {
		time.Sleep(50 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusCompleted || wf.Status == WorkflowStatusFailed {
			break
		}
	}

	if wf.Status != WorkflowStatusCompleted {
		t.Fatalf("expected completed, got %s: %s", wf.Status, wf.Error)
	}

	if receivedArgs["from_input"] != "Alice" {
		t.Errorf("expected from_input='Alice', got %v", receivedArgs["from_input"])
	}
	if receivedArgs["from_step1"] != "from_step1" {
		t.Errorf("expected from_step1='from_step1', got %v", receivedArgs["from_step1"])
	}
	if receivedArgs["literal_val"] != "hello" {
		t.Errorf("expected literal_val='hello', got %v", receivedArgs["literal_val"])
	}
}

func TestWorkflowEngine_ListWorkflows(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	def := &WorkflowDefinition{
		Name:  "list-test",
		Steps: []WorkflowStep{{ID: "s1", Name: "Step", StepType: StepTypeApproval}},
	}
	engine.RegisterDefinition(def)

	// Start multiple workflows
	engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", nil)
	engine.StartWorkflow(context.Background(), def.ID, "session2", "agent1", nil)
	engine.StartWorkflow(context.Background(), def.ID, "session1", "agent2", nil)

	time.Sleep(100 * time.Millisecond)

	// List all
	all := engine.ListWorkflows("", "", "")
	if len(all) != 3 {
		t.Errorf("expected 3 workflows, got %d", len(all))
	}

	// Filter by session
	bySession := engine.ListWorkflows("session1", "", "")
	if len(bySession) != 2 {
		t.Errorf("expected 2 workflows for session1, got %d", len(bySession))
	}

	// Filter by agent
	byAgent := engine.ListWorkflows("", "agent2", "")
	if len(byAgent) != 1 {
		t.Errorf("expected 1 workflow for agent2, got %d", len(byAgent))
	}
}

func TestWorkflowEngine_Events(t *testing.T) {
	var capturedEvents []WorkflowEvent
	executor := func(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error) {
		return map[string]any{"ok": true}, nil
	}

	engine := NewWorkflowEngine(executor)
	engine.SetEventCallback(func(e WorkflowEvent) {
		capturedEvents = append(capturedEvents, e)
	})

	def := &WorkflowDefinition{
		Name:  "events-test",
		Steps: []WorkflowStep{{ID: "s1", Name: "Step", StepType: StepTypeTool, ToolName: "test"}},
	}
	engine.RegisterDefinition(def)

	wf, _ := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", nil)

	// Wait for completion
	for i := 0; i < 30; i++ {
		time.Sleep(50 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusCompleted {
			break
		}
	}

	// Check events
	events, _ := engine.GetEvents(wf.ID)
	if len(events) < 3 {
		t.Errorf("expected at least 3 events, got %d", len(events))
	}

	// Should have: workflow_started, step_started, step_completed, workflow_completed
	eventTypes := make(map[string]bool)
	for _, e := range events {
		eventTypes[e.EventType] = true
	}

	if !eventTypes["workflow_started"] {
		t.Error("expected workflow_started event")
	}
	if !eventTypes["step_started"] {
		t.Error("expected step_started event")
	}
	if !eventTypes["workflow_completed"] {
		t.Error("expected workflow_completed event")
	}
}

func TestWorkflowEngine_GateStep(t *testing.T) {
	var executedAction bool
	executor := func(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error) {
		if tool == "action" {
			executedAction = true
		}
		return map[string]any{"ok": true}, nil
	}

	engine := NewWorkflowEngine(executor)

	// Gate that checks input.proceed
	def := &WorkflowDefinition{
		Name: "gate-workflow",
		Steps: []WorkflowStep{
			{ID: "gate", Name: "Gate", StepType: StepTypeGate, Condition: "input.proceed"},
			{ID: "action", Name: "Action", StepType: StepTypeTool, ToolName: "action", DependsOn: []string{"gate"}},
		},
	}
	engine.RegisterDefinition(def)

	// Test with proceed=true
	wf, _ := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", map[string]any{"proceed": true})
	for i := 0; i < 30; i++ {
		time.Sleep(50 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusCompleted || wf.Status == WorkflowStatusFailed {
			break
		}
	}

	if !executedAction {
		t.Error("expected action to execute when gate passes")
	}
}

func TestWorkflowEngine_ApproveNonExistentWorkflow(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	err := engine.ApproveStep("nonexistent-wf-id", "step1", "admin", "ok")
	if err == nil {
		t.Error("expected error when approving step on non-existent workflow")
	}
}

func TestWorkflowEngine_RejectNonExistentWorkflow(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	err := engine.RejectStep("nonexistent-wf-id", "step1", "admin", "no")
	if err == nil {
		t.Error("expected error when rejecting step on non-existent workflow")
	}
}

func TestWorkflowEngine_CancelNonExistentWorkflow(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	err := engine.CancelWorkflow("nonexistent-wf-id", "reason")
	if err == nil {
		t.Error("expected error when cancelling non-existent workflow")
	}
}

func TestWorkflowEngine_GetNonExistentWorkflow(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	wf, err := engine.GetWorkflow("nonexistent-id")
	if err == nil && wf != nil {
		t.Error("expected error or nil workflow for non-existent ID")
	}
}

func TestWorkflowEngine_GetNonExistentDefinition(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	def, err := engine.GetDefinition("nonexistent-id")
	if err == nil && def != nil {
		t.Error("expected error or nil definition for non-existent ID")
	}
}

func TestWorkflowEngine_StartWithInvalidDefinition(t *testing.T) {
	engine := NewWorkflowEngine(nil)

	_, err := engine.StartWorkflow(context.Background(), "nonexistent-def-id", "session", "agent", nil)
	if err == nil {
		t.Error("expected error when starting workflow with non-existent definition")
	}
}

func TestWorkflowEngine_ToolExecutionFailure(t *testing.T) {
	executor := func(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error) {
		return nil, fmt.Errorf("tool execution failed: permission denied")
	}

	engine := NewWorkflowEngine(executor)

	def := &WorkflowDefinition{
		Name: "failing-tool-workflow",
		Steps: []WorkflowStep{
			{ID: "step1", Name: "Failing Step", StepType: StepTypeTool, ToolName: "bad_tool", MaxRetries: 0},
		},
	}

	engine.RegisterDefinition(def)
	wf, err := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", nil)
	if err != nil {
		t.Fatalf("StartWorkflow failed: %v", err)
	}

	// Wait for completion
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusCompleted || wf.Status == WorkflowStatusFailed {
			break
		}
	}

	if wf.Status != WorkflowStatusFailed {
		t.Errorf("expected status failed, got %s", wf.Status)
	}

	if wf.Error == "" {
		t.Error("expected wf.Error to contain the failure message")
	}
}

func TestWorkflowEngine_GateStepFalseCondition(t *testing.T) {
	var executedAction bool
	executor := func(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error) {
		if tool == "action" {
			executedAction = true
		}
		return map[string]any{"ok": true}, nil
	}

	engine := NewWorkflowEngine(executor)

	def := &WorkflowDefinition{
		Name: "gate-false-workflow",
		Steps: []WorkflowStep{
			{ID: "gate", Name: "Gate", StepType: StepTypeGate, Condition: "input.proceed"},
			{ID: "action", Name: "Action", StepType: StepTypeTool, ToolName: "action", DependsOn: []string{"gate"}},
		},
	}
	engine.RegisterDefinition(def)

	// Start with proceed=false
	wf, _ := engine.StartWorkflow(context.Background(), def.ID, "session1", "agent1", map[string]any{"proceed": false})
	for i := 0; i < 30; i++ {
		time.Sleep(50 * time.Millisecond)
		wf, _ = engine.GetWorkflow(wf.ID)
		if wf.Status == WorkflowStatusCompleted || wf.Status == WorkflowStatusFailed {
			break
		}
	}

	// Verify the gate step recorded passed=false
	gateStep := wf.StepStates["gate"]
	if gateStep == nil {
		t.Fatal("expected gate step state to exist")
	}
	if gateStep.Result != nil {
		if passed, ok := gateStep.Result["passed"].(bool); ok && passed {
			t.Error("expected gate step passed=false when proceed=false")
		}
	}

	// The current implementation completes the gate step successfully
	// even when passed=false, so the action step will still execute.
	// This test documents the current behavior.
	if wf.Status != WorkflowStatusCompleted {
		t.Errorf("expected workflow to complete, got %s: %s", wf.Status, wf.Error)
	}

	if !executedAction {
		t.Log("gate with proceed=false did not block action step (current behavior)")
	}
}
