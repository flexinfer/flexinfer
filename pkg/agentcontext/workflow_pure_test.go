package agentcontext

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// validateDAG
// ---------------------------------------------------------------------------

func TestValidateDAG_ValidLinear(t *testing.T) {
	t.Parallel()
	steps := []WorkflowStep{
		{ID: "a"},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c", DependsOn: []string{"b"}},
	}
	if err := validateDAG(steps); err != nil {
		t.Errorf("expected valid DAG, got error: %v", err)
	}
}

func TestValidateDAG_ValidDiamond(t *testing.T) {
	t.Parallel()
	// a -> b, a -> c, b -> d, c -> d
	steps := []WorkflowStep{
		{ID: "a"},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c", DependsOn: []string{"a"}},
		{ID: "d", DependsOn: []string{"b", "c"}},
	}
	if err := validateDAG(steps); err != nil {
		t.Errorf("expected valid diamond DAG, got error: %v", err)
	}
}

func TestValidateDAG_NoDeps(t *testing.T) {
	t.Parallel()
	steps := []WorkflowStep{
		{ID: "a"},
		{ID: "b"},
		{ID: "c"},
	}
	if err := validateDAG(steps); err != nil {
		t.Errorf("expected valid DAG with no deps, got error: %v", err)
	}
}

func TestValidateDAG_CyclicDirect(t *testing.T) {
	t.Parallel()
	steps := []WorkflowStep{
		{ID: "a", DependsOn: []string{"b"}},
		{ID: "b", DependsOn: []string{"a"}},
	}
	err := validateDAG(steps)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestValidateDAG_CyclicTransitive(t *testing.T) {
	t.Parallel()
	steps := []WorkflowStep{
		{ID: "a", DependsOn: []string{"c"}},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c", DependsOn: []string{"b"}},
	}
	err := validateDAG(steps)
	if err == nil {
		t.Fatal("expected cycle error for transitive cycle")
	}
}

func TestValidateDAG_MissingDependency(t *testing.T) {
	t.Parallel()
	steps := []WorkflowStep{
		{ID: "a", DependsOn: []string{"nonexistent"}},
	}
	err := validateDAG(steps)
	if err == nil {
		t.Fatal("expected error for missing dependency")
	}
}

func TestValidateDAG_SelfCycle(t *testing.T) {
	t.Parallel()
	steps := []WorkflowStep{
		{ID: "a", DependsOn: []string{"a"}},
	}
	err := validateDAG(steps)
	if err == nil {
		t.Fatal("expected error for self-cycle")
	}
}

// ---------------------------------------------------------------------------
// formatCycle
// ---------------------------------------------------------------------------

func TestFormatCycle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cycle []string
		want  string
	}{
		{"two nodes", []string{"a", "b", "a"}, "a -> b -> a"},
		{"three nodes", []string{"x", "y", "z", "x"}, "x -> y -> z -> x"},
		{"single", []string{"a"}, "a"},
		{"empty", []string{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatCycle(tc.cycle)
			if got != tc.want {
				t.Errorf("formatCycle(%v) = %q, want %q", tc.cycle, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// calculateBackoffDelay
// ---------------------------------------------------------------------------

func TestCalculateBackoffDelay_BaseCase(t *testing.T) {
	t.Parallel()
	// Attempt 1 with base 1000ms: delay should be ~1000ms (plus up to 25% jitter)
	d := calculateBackoffDelay(1, 1000)
	if d < 1000*time.Millisecond || d > 1250*time.Millisecond {
		t.Errorf("attempt 1: expected ~1000ms, got %v", d)
	}
}

func TestCalculateBackoffDelay_Exponential(t *testing.T) {
	t.Parallel()
	// Attempt 2 with base 1000ms: delay should be ~2000ms (plus up to 25% jitter)
	d := calculateBackoffDelay(2, 1000)
	if d < 2000*time.Millisecond || d > 2500*time.Millisecond {
		t.Errorf("attempt 2: expected ~2000ms, got %v", d)
	}

	// Attempt 3: ~4000ms
	d = calculateBackoffDelay(3, 1000)
	if d < 4000*time.Millisecond || d > 5000*time.Millisecond {
		t.Errorf("attempt 3: expected ~4000ms, got %v", d)
	}
}

func TestCalculateBackoffDelay_DefaultBase(t *testing.T) {
	t.Parallel()
	// Zero base should default to 1000ms
	d := calculateBackoffDelay(1, 0)
	if d < 1000*time.Millisecond || d > 1250*time.Millisecond {
		t.Errorf("zero base attempt 1: expected ~1000ms, got %v", d)
	}
}

func TestCalculateBackoffDelay_MaxCap(t *testing.T) {
	t.Parallel()
	// High attempt should be capped at 60s + jitter
	d := calculateBackoffDelay(20, 1000)
	maxWithJitter := 60000*time.Millisecond + 15000*time.Millisecond // 60s + 25% jitter
	if d > maxWithJitter {
		t.Errorf("high attempt: expected <= %v, got %v", maxWithJitter, d)
	}
}

// ---------------------------------------------------------------------------
// resolveVariables / resolveString
// ---------------------------------------------------------------------------

func TestResolveVariables_NilArgs(t *testing.T) {
	t.Parallel()
	got := resolveVariables(nil, nil, nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestResolveVariables_InputRef(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"name": "${input.user_name}",
	}
	input := map[string]any{"user_name": "Alice"}
	got := resolveVariables(args, input, nil)
	if got["name"] != "Alice" {
		t.Errorf("expected name=Alice, got %v", got["name"])
	}
}

func TestResolveVariables_ContextRef(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"val": "${step1.output}",
	}
	ctx := map[string]any{
		"step1": map[string]any{"output": "result123"},
	}
	got := resolveVariables(args, nil, ctx)
	if got["val"] != "result123" {
		t.Errorf("expected val=result123, got %v", got["val"])
	}
}

func TestResolveVariables_LiteralPreserved(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"literal":    "hello",
		"number":     42,
		"unresolved": "${input.missing}",
	}
	got := resolveVariables(args, nil, nil)
	if got["literal"] != "hello" {
		t.Errorf("literal: got %v", got["literal"])
	}
	if got["number"] != 42 {
		t.Errorf("number: got %v", got["number"])
	}
	// Unresolved should return original string
	if got["unresolved"] != "${input.missing}" {
		t.Errorf("unresolved: got %v", got["unresolved"])
	}
}

func TestResolveVariables_NestedMap(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"nested": map[string]any{
			"ref": "${input.env}",
		},
	}
	input := map[string]any{"env": "production"}
	got := resolveVariables(args, input, nil)
	nested, ok := got["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map, got %T", got["nested"])
	}
	if nested["ref"] != "production" {
		t.Errorf("nested ref: got %v", nested["ref"])
	}
}

func TestResolveString_NotVariable(t *testing.T) {
	t.Parallel()
	// Should return as-is for non-variable strings
	tests := []string{"hello", "", "abc", "${", "x}", "${}"}
	for _, s := range tests {
		got := resolveString(s, nil, nil)
		if got != s {
			t.Errorf("resolveString(%q) = %v, want %q", s, got, s)
		}
	}
}

func TestResolveString_InputVariable(t *testing.T) {
	t.Parallel()
	input := map[string]any{"key": "value123"}
	got := resolveString("${input.key}", input, nil)
	if got != "value123" {
		t.Errorf("expected value123, got %v", got)
	}
}

func TestResolveString_ContextVariable(t *testing.T) {
	t.Parallel()
	ctx := map[string]any{
		"step_a": map[string]any{"result": "done"},
	}
	got := resolveString("${step_a.result}", nil, ctx)
	if got != "done" {
		t.Errorf("expected done, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// evaluateCondition
// ---------------------------------------------------------------------------

func TestEvaluateCondition_BoolTrue(t *testing.T) {
	t.Parallel()
	input := map[string]any{"enabled": true}
	if !evaluateCondition("input.enabled", input, nil) {
		t.Error("expected true for truthy bool")
	}
}

func TestEvaluateCondition_BoolFalse(t *testing.T) {
	t.Parallel()
	input := map[string]any{"enabled": false}
	if evaluateCondition("input.enabled", input, nil) {
		t.Error("expected false for falsy bool")
	}
}

func TestEvaluateCondition_StringTruthy(t *testing.T) {
	t.Parallel()
	input := map[string]any{"status": "completed"}
	if !evaluateCondition("input.status", input, nil) {
		t.Error("expected true for non-empty string")
	}
}

func TestEvaluateCondition_StringFalsy(t *testing.T) {
	t.Parallel()
	input := map[string]any{"status": "false"}
	if evaluateCondition("input.status", input, nil) {
		t.Error("expected false for string 'false'")
	}
}

func TestEvaluateCondition_MissingKey(t *testing.T) {
	t.Parallel()
	// Unresolved references return nil, which is falsy.
	// This is the correct behavior for gate conditions.
	if evaluateCondition("input.missing", nil, nil) {
		t.Error("expected false for unresolved reference")
	}
}

func TestEvaluateCondition_ContextRef(t *testing.T) {
	t.Parallel()
	ctx := map[string]any{
		"build": map[string]any{"success": true},
	}
	if !evaluateCondition("build.success", nil, ctx) {
		t.Error("expected true for context bool true")
	}
}

// ---------------------------------------------------------------------------
// indexOf
// ---------------------------------------------------------------------------

func TestIndexOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		s    string
		c    byte
		want int
	}{
		{"hello.world", '.', 5},
		{"no-dot-here", '.', -1},
		{".leading", '.', 0},
		{"trailing.", '.', 8},
		{"", '.', -1},
	}
	for _, tc := range tests {
		t.Run(tc.s, func(t *testing.T) {
			t.Parallel()
			if got := indexOf(tc.s, tc.c); got != tc.want {
				t.Errorf("indexOf(%q, %q) = %d, want %d", tc.s, tc.c, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// mapKeys
// ---------------------------------------------------------------------------

func TestMapKeys_Nil(t *testing.T) {
	t.Parallel()
	if got := mapKeys(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestMapKeys_Populated(t *testing.T) {
	t.Parallel()
	m := map[string]any{"a": 1, "b": 2, "c": 3}
	keys := mapKeys(m)
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}
	sort.Strings(keys)
	expected := []string{"a", "b", "c"}
	for i, k := range keys {
		if k != expected[i] {
			t.Errorf("key[%d] = %q, want %q", i, k, expected[i])
		}
	}
}

func TestMapKeys_Empty(t *testing.T) {
	t.Parallel()
	m := map[string]any{}
	keys := mapKeys(m)
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}

// ---------------------------------------------------------------------------
// errStr
// ---------------------------------------------------------------------------

func TestErrStr_Nil(t *testing.T) {
	t.Parallel()
	if got := errStr(nil); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestErrStr_NonNil(t *testing.T) {
	t.Parallel()
	err := errors.New("something went wrong")
	if got := errStr(err); got != "something went wrong" {
		t.Errorf("expected error message, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// ParseWorkflowDefinition
// ---------------------------------------------------------------------------

func TestParseWorkflowDefinition_Valid(t *testing.T) {
	t.Parallel()
	data := `{
		"id": "def-1",
		"name": "test-workflow",
		"description": "A test",
		"version": "1.0",
		"steps": [
			{"id": "s1", "name": "Step 1", "step_type": "tool", "tool_name": "build"},
			{"id": "s2", "name": "Step 2", "step_type": "tool", "depends_on": ["s1"]}
		],
		"timeout_seconds": 600
	}`
	def, err := ParseWorkflowDefinition([]byte(data))
	if err != nil {
		t.Fatalf("ParseWorkflowDefinition: %v", err)
	}
	if def.Name != "test-workflow" {
		t.Errorf("Name: got %q", def.Name)
	}
	if len(def.Steps) != 2 {
		t.Errorf("Steps: got %d, want 2", len(def.Steps))
	}
	if def.Steps[1].DependsOn[0] != "s1" {
		t.Errorf("Steps[1].DependsOn[0]: got %q", def.Steps[1].DependsOn[0])
	}
	if def.TimeoutSeconds != 600 {
		t.Errorf("TimeoutSeconds: got %d, want 600", def.TimeoutSeconds)
	}
}

func TestParseWorkflowDefinition_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := ParseWorkflowDefinition([]byte("{invalid"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseWorkflowDefinition_MissingFields(t *testing.T) {
	t.Parallel()
	// JSON parses fine but results in empty/zero fields
	data := `{}`
	def, err := ParseWorkflowDefinition([]byte(data))
	if err != nil {
		t.Fatalf("ParseWorkflowDefinition: %v", err)
	}
	if def.Name != "" {
		t.Errorf("expected empty name, got %q", def.Name)
	}
	if len(def.Steps) != 0 {
		t.Errorf("expected 0 steps, got %d", len(def.Steps))
	}
}

func TestParseWorkflowDefinition_FullStruct(t *testing.T) {
	t.Parallel()
	def := WorkflowDefinition{
		ID:                "d1",
		Name:              "full",
		Version:           "2.0",
		RollbackOnFailure: true,
		Steps: []WorkflowStep{
			{
				ID:         "s1",
				Name:       "Build",
				StepType:   StepTypeTool,
				ToolName:   "build",
				MaxRetries: 3,
				RetryDelay: 1000,
				Timeout:    60,
				ServerName: "mcp-server",
				ToolArgs:   map[string]any{"env": "prod"},
			},
		},
	}
	data, _ := json.Marshal(def)
	parsed, err := ParseWorkflowDefinition(data)
	if err != nil {
		t.Fatalf("ParseWorkflowDefinition: %v", err)
	}
	if parsed.RollbackOnFailure != true {
		t.Error("expected RollbackOnFailure=true")
	}
	if parsed.Steps[0].MaxRetries != 3 {
		t.Errorf("MaxRetries: got %d", parsed.Steps[0].MaxRetries)
	}
}

// ---------------------------------------------------------------------------
// WorkflowEngine.RegisterDefinition (in-memory, no external deps)
// ---------------------------------------------------------------------------

func TestRegisterDefinition_Valid(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	def := &WorkflowDefinition{
		Name: "test",
		Steps: []WorkflowStep{
			{ID: "s1", Name: "Step 1", StepType: StepTypeTool},
		},
	}
	if err := engine.RegisterDefinition(def); err != nil {
		t.Fatalf("RegisterDefinition: %v", err)
	}
	if def.ID == "" {
		t.Error("expected ID to be assigned")
	}
	if def.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be set")
	}
	if def.UpdatedAt.IsZero() {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestRegisterDefinition_MissingName(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	err := engine.RegisterDefinition(&WorkflowDefinition{
		Steps: []WorkflowStep{{ID: "s1"}},
	})
	if err == nil {
		t.Error("expected error for missing name")
	}
}

func TestRegisterDefinition_NoSteps(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	err := engine.RegisterDefinition(&WorkflowDefinition{
		Name: "empty",
	})
	if err == nil {
		t.Error("expected error for no steps")
	}
}

func TestRegisterDefinition_CyclicDAG(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	err := engine.RegisterDefinition(&WorkflowDefinition{
		Name: "cyclic",
		Steps: []WorkflowStep{
			{ID: "a", DependsOn: []string{"b"}},
			{ID: "b", DependsOn: []string{"a"}},
		},
	})
	if err == nil {
		t.Error("expected error for cyclic DAG")
	}
}

func TestRegisterDefinition_IdempotentByNameAndNamespace(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)

	first := &WorkflowDefinition{
		Name:      "deploy",
		Namespace: "team/ops",
		Steps:     []WorkflowStep{{ID: "s1", Name: "Step 1", StepType: StepTypeTool}},
	}
	if err := engine.RegisterDefinition(first); err != nil {
		t.Fatal(err)
	}
	firstID := first.ID

	second := &WorkflowDefinition{
		Name:      "deploy",
		Namespace: "team/ops",
		Steps:     []WorkflowStep{{ID: "s1", Name: "Updated Step", StepType: StepTypeTool}},
	}
	if err := engine.RegisterDefinition(second); err != nil {
		t.Fatal(err)
	}

	if second.ID != firstID {
		t.Errorf("expected same ID %q, got %q", firstID, second.ID)
	}

	defs := engine.ListDefinitions("team/ops")
	if len(defs) != 1 {
		t.Errorf("expected 1 definition, got %d", len(defs))
	}
}

func TestRegisterDefinition_DifferentNamespacesDifferentIDs(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)

	def1 := &WorkflowDefinition{
		Name:      "deploy",
		Namespace: "ns1",
		Steps:     []WorkflowStep{{ID: "s1", Name: "Step", StepType: StepTypeTool}},
	}
	def2 := &WorkflowDefinition{
		Name:      "deploy",
		Namespace: "ns2",
		Steps:     []WorkflowStep{{ID: "s1", Name: "Step", StepType: StepTypeTool}},
	}
	engine.RegisterDefinition(def1)
	engine.RegisterDefinition(def2)

	if def1.ID == def2.ID {
		t.Error("expected different IDs for different namespaces")
	}
}

// ---------------------------------------------------------------------------
// WorkflowEngine.ListDefinitions
// ---------------------------------------------------------------------------

func TestListDefinitions_FilterByNamespace(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)

	engine.RegisterDefinition(&WorkflowDefinition{
		Name: "a", Namespace: "ns1",
		Steps: []WorkflowStep{{ID: "s1", StepType: StepTypeTool}},
	})
	engine.RegisterDefinition(&WorkflowDefinition{
		Name: "b", Namespace: "ns1",
		Steps: []WorkflowStep{{ID: "s1", StepType: StepTypeTool}},
	})
	engine.RegisterDefinition(&WorkflowDefinition{
		Name: "c", Namespace: "ns2",
		Steps: []WorkflowStep{{ID: "s1", StepType: StepTypeTool}},
	})

	ns1 := engine.ListDefinitions("ns1")
	if len(ns1) != 2 {
		t.Errorf("expected 2 definitions in ns1, got %d", len(ns1))
	}

	ns2 := engine.ListDefinitions("ns2")
	if len(ns2) != 1 {
		t.Errorf("expected 1 definition in ns2, got %d", len(ns2))
	}

	all := engine.ListDefinitions("")
	if len(all) != 3 {
		t.Errorf("expected 3 definitions total, got %d", len(all))
	}
}

// ---------------------------------------------------------------------------
// findReadySteps / allStepsComplete / anyStepWaiting
// ---------------------------------------------------------------------------

func TestFindReadySteps_AllPendingNoDeps(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	wf := &Workflow{
		StepStates: map[string]*WorkflowStep{
			"a": {ID: "a", Status: StepStatusPending},
			"b": {ID: "b", Status: StepStatusPending},
		},
	}
	ready := engine.findReadySteps(wf)
	if len(ready) != 2 {
		t.Errorf("expected 2 ready steps, got %d", len(ready))
	}
}

func TestFindReadySteps_WithDeps(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	wf := &Workflow{
		StepStates: map[string]*WorkflowStep{
			"a": {ID: "a", Status: StepStatusCompleted},
			"b": {ID: "b", Status: StepStatusPending, DependsOn: []string{"a"}},
			"c": {ID: "c", Status: StepStatusPending, DependsOn: []string{"b"}},
		},
	}
	ready := engine.findReadySteps(wf)
	if len(ready) != 1 || ready[0] != "b" {
		t.Errorf("expected [b], got %v", ready)
	}
}

func TestFindReadySteps_BlockedByRunning(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	wf := &Workflow{
		StepStates: map[string]*WorkflowStep{
			"a": {ID: "a", Status: StepStatusRunning},
			"b": {ID: "b", Status: StepStatusPending, DependsOn: []string{"a"}},
		},
	}
	ready := engine.findReadySteps(wf)
	if len(ready) != 0 {
		t.Errorf("expected 0 ready steps (a is running), got %d", len(ready))
	}
}

func TestAllStepsComplete_True(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	wf := &Workflow{
		StepStates: map[string]*WorkflowStep{
			"a": {ID: "a", Status: StepStatusCompleted},
			"b": {ID: "b", Status: StepStatusSkipped},
		},
	}
	if !engine.allStepsComplete(wf) {
		t.Error("expected allStepsComplete=true")
	}
}

func TestAllStepsComplete_False(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	wf := &Workflow{
		StepStates: map[string]*WorkflowStep{
			"a": {ID: "a", Status: StepStatusCompleted},
			"b": {ID: "b", Status: StepStatusPending},
		},
	}
	if engine.allStepsComplete(wf) {
		t.Error("expected allStepsComplete=false")
	}
}

func TestAnyStepWaiting_True(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	wf := &Workflow{
		StepStates: map[string]*WorkflowStep{
			"a": {ID: "a", Status: StepStatusCompleted},
			"b": {ID: "b", Status: StepStatusWaiting},
		},
	}
	if !engine.anyStepWaiting(wf) {
		t.Error("expected anyStepWaiting=true")
	}
}

func TestAnyStepWaiting_False(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	wf := &Workflow{
		StepStates: map[string]*WorkflowStep{
			"a": {ID: "a", Status: StepStatusCompleted},
			"b": {ID: "b", Status: StepStatusPending},
		},
	}
	if engine.anyStepWaiting(wf) {
		t.Error("expected anyStepWaiting=false")
	}
}

func TestAllStepsComplete_EmptyWorkflow(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)
	wf := &Workflow{
		StepStates: map[string]*WorkflowStep{},
	}
	if !engine.allStepsComplete(wf) {
		t.Error("expected allStepsComplete=true for empty workflow")
	}
}

// ---------------------------------------------------------------------------
// Workflow.clone
// ---------------------------------------------------------------------------

func TestWorkflowClone(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	startedAt := now
	completedAt := now.Add(time.Minute)

	wf := &Workflow{
		ID:          "wf-1",
		Status:      WorkflowStatusCompleted,
		StartedAt:   &startedAt,
		CompletedAt: &completedAt,
		Input:       map[string]any{"k": "v"},
		Output:      map[string]any{"r": "ok"},
		Context:     map[string]any{"s1": map[string]any{"done": true}},
		StepStates: map[string]*WorkflowStep{
			"s1": {
				ID:       "s1",
				Status:   StepStatusCompleted,
				ToolArgs: map[string]any{"arg": "val"},
				Result:   map[string]any{"out": "data"},
			},
		},
	}

	cp := wf.clone()

	// Verify separate memory
	if cp == wf {
		t.Error("clone should return a different pointer")
	}
	if cp.ID != wf.ID {
		t.Errorf("ID mismatch")
	}

	// Modify original, verify clone unaffected
	wf.Input["k"] = "modified"
	if cp.Input["k"] == "modified" {
		t.Error("clone Input should be independent")
	}

	wf.StepStates["s1"].Status = StepStatusFailed
	if cp.StepStates["s1"].Status == StepStatusFailed {
		t.Error("clone StepStates should be independent")
	}
}

func TestWorkflowClone_Nil(t *testing.T) {
	t.Parallel()
	var wf *Workflow
	if wf.clone() != nil {
		t.Error("clone of nil should return nil")
	}
}

func TestWorkflowClone_MapReduceFields(t *testing.T) {
	t.Parallel()
	wf := &Workflow{
		ID:      "wf-mr-clone",
		Status:  WorkflowStatusRunning,
		Input:   map[string]any{},
		Context: map[string]any{},
		StepStates: map[string]*WorkflowStep{
			"mr": {
				ID:          "mr",
				StepType:    StepTypeMapReduce,
				MapInputKey: "items",
				MapStepTemplate: &WorkflowStep{
					ID:       "tmpl",
					ToolArgs: map[string]any{"q": "original"},
				},
				ReduceToolArgs: map[string]any{"mode": "sum"},
			},
		},
	}

	cp := wf.clone()

	// Modify original MapStepTemplate, verify clone is independent
	wf.StepStates["mr"].MapStepTemplate.ToolArgs["q"] = "modified"
	if cp.StepStates["mr"].MapStepTemplate.ToolArgs["q"] == "modified" {
		t.Error("clone MapStepTemplate.ToolArgs should be independent")
	}

	// Modify original ReduceToolArgs
	wf.StepStates["mr"].ReduceToolArgs["mode"] = "avg"
	if cp.StepStates["mr"].ReduceToolArgs["mode"] == "avg" {
		t.Error("clone ReduceToolArgs should be independent")
	}
}

// ---------------------------------------------------------------------------
// Subflow channel-based completion
// ---------------------------------------------------------------------------

func TestSubflowDoneChannel_ClosedOnComplete(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)

	wf := &Workflow{
		ID:         "wf-chan-1",
		Status:     WorkflowStatusRunning,
		StepStates: map[string]*WorkflowStep{},
		Context:    make(map[string]any),
		done:       make(chan struct{}),
	}
	engine.mu.Lock()
	engine.workflows[wf.ID] = wf
	engine.events[wf.ID] = []WorkflowEvent{}
	engine.mu.Unlock()

	engine.completeWorkflow(wf, nil)

	select {
	case <-wf.done:
		// success
	default:
		t.Error("done channel should be closed after completeWorkflow")
	}
}

func TestSubflowDoneChannel_ClosedOnFailure(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)

	wf := &Workflow{
		ID:         "wf-chan-2",
		Status:     WorkflowStatusRunning,
		StepStates: map[string]*WorkflowStep{},
		Context:    make(map[string]any),
		done:       make(chan struct{}),
	}
	engine.mu.Lock()
	engine.workflows[wf.ID] = wf
	engine.events[wf.ID] = []WorkflowEvent{}
	engine.mu.Unlock()

	engine.completeWorkflow(wf, errors.New("test failure"))

	select {
	case <-wf.done:
		// success
	default:
		t.Error("done channel should be closed on failure")
	}

	if wf.Status != WorkflowStatusFailed {
		t.Errorf("expected failed, got %s", wf.Status)
	}
}

func TestSubflowDoneChannel_DoubleCloseIsSafe(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)

	wf := &Workflow{
		ID:         "wf-chan-3",
		Status:     WorkflowStatusRunning,
		StepStates: map[string]*WorkflowStep{},
		Context:    make(map[string]any),
		done:       make(chan struct{}),
	}
	engine.mu.Lock()
	engine.workflows[wf.ID] = wf
	engine.events[wf.ID] = []WorkflowEvent{}
	engine.mu.Unlock()

	// Should not panic even if called twice
	engine.completeWorkflow(wf, nil)
	engine.completeWorkflow(wf, nil)
}

// ---------------------------------------------------------------------------
// MapReduce step
// ---------------------------------------------------------------------------

func TestMapReduceStep_BasicFanOut(t *testing.T) {
	t.Parallel()

	callCount := 0
	var mu sync.Mutex
	executor := func(_ context.Context, _, _ string, args map[string]any) (map[string]any, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		return map[string]any{"found": args["query"]}, nil
	}
	engine := NewWorkflowEngine(executor)

	wf := &Workflow{
		ID:         "wf-mr-1",
		Status:     WorkflowStatusRunning,
		StepStates: map[string]*WorkflowStep{},
		Input:      map[string]any{},
		Context: map[string]any{
			"partitions": []any{"auth", "database", "api"},
		},
		done: make(chan struct{}),
	}

	step := &WorkflowStep{
		ID:          "map_step",
		StepType:    StepTypeMapReduce,
		MapInputKey: "partitions",
		MapStepTemplate: &WorkflowStep{
			ID:         "tmpl",
			StepType:   StepTypeTool,
			ToolName:   "codebase_search",
			ServerName: "codebase-memory",
			ToolArgs:   map[string]any{"query": "${item}"},
		},
	}

	result, err := engine.executeMapReduceStep(context.Background(), wf, step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	cc := callCount
	mu.Unlock()
	if cc != 3 {
		t.Errorf("expected 3 tool calls, got %d", cc)
	}

	mapResults, ok := result["map_results"].([]any)
	if !ok {
		t.Fatal("expected map_results in result")
	}
	if len(mapResults) != 3 {
		t.Errorf("expected 3 results, got %d", len(mapResults))
	}
}

func TestMapReduceStep_BoundedConcurrency(t *testing.T) {
	t.Parallel()

	maxConcurrent := 0
	currentConcurrent := 0
	var mu sync.Mutex

	executor := func(_ context.Context, _, _ string, _ map[string]any) (map[string]any, error) {
		mu.Lock()
		currentConcurrent++
		if currentConcurrent > maxConcurrent {
			maxConcurrent = currentConcurrent
		}
		mu.Unlock()

		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		currentConcurrent--
		mu.Unlock()

		return map[string]any{"ok": true}, nil
	}
	engine := NewWorkflowEngine(executor)

	wf := &Workflow{
		ID:         "wf-mr-conc",
		Status:     WorkflowStatusRunning,
		StepStates: map[string]*WorkflowStep{},
		Input:      map[string]any{},
		Context: map[string]any{
			"items": []any{"a", "b", "c", "d", "e", "f", "g", "h"},
		},
		done: make(chan struct{}),
	}

	step := &WorkflowStep{
		ID:             "map_bounded",
		StepType:       StepTypeMapReduce,
		MapInputKey:    "items",
		MaxConcurrency: 2,
		MapStepTemplate: &WorkflowStep{
			ID:       "tmpl",
			StepType: StepTypeTool,
			ToolName: "test_tool",
			ToolArgs: map[string]any{"val": "${item}"},
		},
	}

	_, err := engine.executeMapReduceStep(context.Background(), wf, step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	mc := maxConcurrent
	mu.Unlock()

	if mc > 2 {
		t.Errorf("max concurrency should be <= 2, got %d", mc)
	}
}

func TestMapReduceStep_EmptyInput(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)

	wf := &Workflow{
		ID:         "wf-mr-empty",
		Status:     WorkflowStatusRunning,
		StepStates: map[string]*WorkflowStep{},
		Input:      map[string]any{},
		Context:    map[string]any{"items": []any{}},
		done:       make(chan struct{}),
	}

	step := &WorkflowStep{
		ID:          "map_empty",
		StepType:    StepTypeMapReduce,
		MapInputKey: "items",
		MapStepTemplate: &WorkflowStep{
			ID:       "tmpl",
			StepType: StepTypeTool,
			ToolName: "test_tool",
			ToolArgs: map[string]any{},
		},
	}

	result, err := engine.executeMapReduceStep(context.Background(), wf, step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	count, ok := result["count"]
	if !ok || count != 0 {
		t.Errorf("expected count=0, got %v", count)
	}
}

func TestMapReduceStep_MissingInput(t *testing.T) {
	t.Parallel()
	engine := NewWorkflowEngine(nil)

	wf := &Workflow{
		ID:         "wf-mr-miss",
		Status:     WorkflowStatusRunning,
		StepStates: map[string]*WorkflowStep{},
		Input:      map[string]any{},
		Context:    map[string]any{},
		done:       make(chan struct{}),
	}

	step := &WorkflowStep{
		ID:          "map_miss",
		StepType:    StepTypeMapReduce,
		MapInputKey: "nonexistent",
		MapStepTemplate: &WorkflowStep{
			ID:       "tmpl",
			StepType: StepTypeTool,
			ToolName: "test_tool",
			ToolArgs: map[string]any{},
		},
	}

	result, err := engine.executeMapReduceStep(context.Background(), wf, step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	count, ok := result["count"]
	if !ok || count != 0 {
		t.Errorf("expected count=0 for missing input, got %v", count)
	}
}

func TestMapReduceStep_WithReduce(t *testing.T) {
	t.Parallel()

	var reduceCalled bool
	executor := func(_ context.Context, _, tool string, args map[string]any) (map[string]any, error) {
		if tool == "reduce_tool" {
			reduceCalled = true
			results := args["map_results"].([]any)
			return map[string]any{"summary": "reduced", "input_count": len(results)}, nil
		}
		return map[string]any{"data": args["query"]}, nil
	}
	engine := NewWorkflowEngine(executor)

	wf := &Workflow{
		ID:         "wf-mr-reduce",
		Status:     WorkflowStatusRunning,
		StepStates: map[string]*WorkflowStep{},
		Input:      map[string]any{},
		Context: map[string]any{
			"parts": []any{"x", "y"},
		},
		done: make(chan struct{}),
	}

	step := &WorkflowStep{
		ID:          "map_reduce",
		StepType:    StepTypeMapReduce,
		MapInputKey: "parts",
		MapStepTemplate: &WorkflowStep{
			ID:       "tmpl",
			StepType: StepTypeTool,
			ToolName: "search",
			ToolArgs: map[string]any{"query": "${item}"},
		},
		ReduceToolName:   "reduce_tool",
		ReduceServerName: "test-server",
	}

	result, err := engine.executeMapReduceStep(context.Background(), wf, step)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reduceCalled {
		t.Error("reduce tool should have been called")
	}
	if result["summary"] != "reduced" {
		t.Errorf("expected reduced summary, got %v", result)
	}
}

func TestMapReduceStep_ErrorPropagation(t *testing.T) {
	t.Parallel()

	executor := func(_ context.Context, _, _ string, args map[string]any) (map[string]any, error) {
		if args["query"] == "bad" {
			return nil, errors.New("item failed")
		}
		return map[string]any{"ok": true}, nil
	}
	engine := NewWorkflowEngine(executor)

	wf := &Workflow{
		ID:         "wf-mr-err",
		Status:     WorkflowStatusRunning,
		StepStates: map[string]*WorkflowStep{},
		Input:      map[string]any{},
		Context: map[string]any{
			"items": []any{"good", "bad", "good2"},
		},
		done: make(chan struct{}),
	}

	step := &WorkflowStep{
		ID:          "map_err",
		StepType:    StepTypeMapReduce,
		MapInputKey: "items",
		MapStepTemplate: &WorkflowStep{
			ID:       "tmpl",
			StepType: StepTypeTool,
			ToolName: "test_tool",
			ToolArgs: map[string]any{"query": "${item}"},
		},
	}

	_, err := engine.executeMapReduceStep(context.Background(), wf, step)
	if err == nil {
		t.Fatal("expected error from failed map item")
	}
	if !strings.Contains(err.Error(), "item failed") {
		t.Errorf("error should contain original message, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Enhanced condition evaluator
// ---------------------------------------------------------------------------

func TestConditionEval_ComparisonGT(t *testing.T) {
	t.Parallel()
	ctx := map[string]any{
		"step1": map[string]any{"count": 5},
	}
	if !evaluateCondition("step1.count > 0", nil, ctx) {
		t.Error("5 > 0 should be true")
	}
	if evaluateCondition("step1.count > 10", nil, ctx) {
		t.Error("5 > 10 should be false")
	}
}

func TestConditionEval_ComparisonGTE(t *testing.T) {
	t.Parallel()
	ctx := map[string]any{
		"result": map[string]any{"confidence": 0.8},
	}
	if !evaluateCondition("result.confidence >= 0.8", nil, ctx) {
		t.Error("0.8 >= 0.8 should be true")
	}
	if evaluateCondition("result.confidence >= 0.9", nil, ctx) {
		t.Error("0.8 >= 0.9 should be false")
	}
}

func TestConditionEval_ComparisonEQ(t *testing.T) {
	t.Parallel()
	ctx := map[string]any{
		"gate": map[string]any{"passed": true},
	}
	if !evaluateCondition("gate.passed == true", nil, ctx) {
		t.Error("true == true should be true")
	}
}

func TestConditionEval_ComparisonNEQ(t *testing.T) {
	t.Parallel()
	ctx := map[string]any{
		"step": map[string]any{"status": "failed"},
	}
	if !evaluateCondition("step.status != 'success'", nil, ctx) {
		t.Error("failed != success should be true")
	}
}

func TestConditionEval_BooleanAND(t *testing.T) {
	t.Parallel()
	ctx := map[string]any{
		"a": map[string]any{"passed": true},
		"b": map[string]any{"passed": true},
	}
	if !evaluateCondition("a.passed AND b.passed", nil, ctx) {
		t.Error("true AND true should be true")
	}

	ctx["b"] = map[string]any{"passed": false}
	if evaluateCondition("a.passed AND b.passed", nil, ctx) {
		t.Error("true AND false should be false")
	}
}

func TestConditionEval_BooleanOR(t *testing.T) {
	t.Parallel()
	ctx := map[string]any{
		"a": map[string]any{"passed": false},
		"b": map[string]any{"passed": true},
	}
	if !evaluateCondition("a.passed OR b.passed", nil, ctx) {
		t.Error("false OR true should be true")
	}
}

func TestConditionEval_EXISTS(t *testing.T) {
	t.Parallel()
	ctx := map[string]any{
		"step1": map[string]any{"result": "found"},
	}
	if !evaluateCondition("step1.result EXISTS", nil, ctx) {
		t.Error("existing key should pass EXISTS")
	}
	if evaluateCondition("step2.result EXISTS", nil, ctx) {
		t.Error("missing key should fail EXISTS")
	}
}

func TestConditionEval_Empty(t *testing.T) {
	t.Parallel()
	if !evaluateCondition("", nil, nil) {
		t.Error("empty condition should be true")
	}
}

func TestConditionEval_SimpleTruthy(t *testing.T) {
	t.Parallel()
	ctx := map[string]any{
		"step": map[string]any{"ok": true},
	}
	if !evaluateCondition("step.ok", nil, ctx) {
		t.Error("truthy value should pass")
	}
}

func TestIsTruthy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		val  any
		want bool
	}{
		{nil, false},
		{true, true},
		{false, false},
		{"hello", true},
		{"", false},
		{"false", false},
		{"0", false},
		{0, false},
		{1, true},
		{int64(0), false},
		{int64(42), true},
		{0.0, false},
		{3.14, true},
		{map[string]any{}, true},
	}
	for _, tc := range cases {
		got := isTruthy(tc.val)
		if got != tc.want {
			t.Errorf("isTruthy(%v) = %v, want %v", tc.val, got, tc.want)
		}
	}
}

func TestParseCondValue(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  any
	}{
		{"true", true},
		{"false", false},
		{"42", int64(42)},
		{"3.14", 3.14},
		{"'hello'", "hello"},
		{"\"world\"", "world"},
		{"unknown", "unknown"},
	}
	for _, tc := range cases {
		got := parseCondValue(tc.input)
		if got != tc.want {
			t.Errorf("parseCondValue(%q) = %v (%T), want %v (%T)", tc.input, got, got, tc.want, tc.want)
		}
	}
}

func TestCompareValues_Numeric(t *testing.T) {
	t.Parallel()
	if !compareValues(5, int64(3), ">") {
		t.Error("5 > 3 should be true")
	}
	if !compareValues(3.0, 3.0, "==") {
		t.Error("3.0 == 3.0 should be true")
	}
	if !compareValues(1, 2, "<") {
		t.Error("1 < 2 should be true")
	}
	if !compareValues(5, int64(5), "<=") {
		t.Error("5 <= 5 should be true")
	}
	if !compareValues(5, int64(5), ">=") {
		t.Error("5 >= 5 should be true")
	}
	if !compareValues(5, 3, "!=") {
		t.Error("5 != 3 should be true")
	}
}

func TestCompareValues_String(t *testing.T) {
	t.Parallel()
	if !compareValues("abc", "abc", "==") {
		t.Error("abc == abc should be true")
	}
	if !compareValues("abc", "xyz", "!=") {
		t.Error("abc != xyz should be true")
	}
}

// --- Gate skip propagation tests ---

func TestGateStep_FalseConditionSkipsDownstream(t *testing.T) {
	executor := func(_ context.Context, _, _ string, _ map[string]any) (map[string]any, error) {
		return map[string]any{"ok": true}, nil
	}
	eng := NewWorkflowEngine(executor)

	def := &WorkflowDefinition{
		ID:   "gate-skip-def",
		Name: "Gate skip test",
		Steps: []WorkflowStep{
			{ID: "init", Name: "Init", StepType: StepTypeTool, ServerName: "s", ToolName: "t"},
			{ID: "gate", Name: "Gate", StepType: StepTypeGate, Condition: "init.missing_key > 0", DependsOn: []string{"init"}},
			{ID: "after", Name: "After Gate", StepType: StepTypeTool, ServerName: "s", ToolName: "t", DependsOn: []string{"gate"}},
		},
	}
	eng.RegisterDefinition(def)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wf, err := eng.StartWorkflow(ctx, "gate-skip-def", "sess", "agent", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for completion
	for {
		eng.mu.RLock()
		status := wf.Status
		eng.mu.RUnlock()
		if status == WorkflowStatusCompleted || status == WorkflowStatusFailed {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for workflow")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	eng.mu.RLock()
	defer eng.mu.RUnlock()

	// Gate should be skipped
	if wf.StepStates["gate"].Status != StepStatusSkipped {
		t.Errorf("gate status = %s, want skipped", wf.StepStates["gate"].Status)
	}
	// Downstream step should also be skipped (propagated)
	if wf.StepStates["after"].Status != StepStatusSkipped {
		t.Errorf("after status = %s, want skipped", wf.StepStates["after"].Status)
	}
	// Workflow should complete (all steps are either completed or skipped)
	if wf.Status != WorkflowStatusCompleted {
		t.Errorf("workflow status = %s, want completed", wf.Status)
	}
}

func TestPropagateSkips_Transitive(t *testing.T) {
	eng := NewWorkflowEngine(nil)

	wf := &Workflow{
		ID:     "skip-prop",
		Status: WorkflowStatusRunning,
		StepStates: map[string]*WorkflowStep{
			"a": {ID: "a", Status: StepStatusSkipped},
			"b": {ID: "b", Status: StepStatusPending, DependsOn: []string{"a"}},
			"c": {ID: "c", Status: StepStatusPending, DependsOn: []string{"b"}},
		},
		Context: map[string]any{},
	}

	eng.propagateSkips(wf)

	if wf.StepStates["b"].Status != StepStatusSkipped {
		t.Errorf("b status = %s, want skipped", wf.StepStates["b"].Status)
	}
	if wf.StepStates["c"].Status != StepStatusSkipped {
		t.Errorf("c status = %s, want skipped", wf.StepStates["c"].Status)
	}
	if wf.CompletedSteps != 2 {
		t.Errorf("completed steps = %d, want 2", wf.CompletedSteps)
	}
}

func TestPropagateSkips_NoEffectWhenNoneSkipped(t *testing.T) {
	eng := NewWorkflowEngine(nil)

	wf := &Workflow{
		ID:     "no-skip",
		Status: WorkflowStatusRunning,
		StepStates: map[string]*WorkflowStep{
			"a": {ID: "a", Status: StepStatusCompleted},
			"b": {ID: "b", Status: StepStatusPending, DependsOn: []string{"a"}},
		},
		Context: map[string]any{},
	}

	eng.propagateSkips(wf)

	if wf.StepStates["b"].Status != StepStatusPending {
		t.Errorf("b status = %s, want pending", wf.StepStates["b"].Status)
	}
}

// --- injectItemVariable nested replacement tests ---

func TestInjectItemVariable_NestedMap(t *testing.T) {
	args := map[string]any{
		"top": "${item}",
		"nested": map[string]any{
			"query": "${item}",
			"fixed": "unchanged",
		},
	}
	result := injectItemVariable(args, "search-term", nil, nil)

	if result["top"] != "search-term" {
		t.Errorf("top = %v, want search-term", result["top"])
	}
	nested := result["nested"].(map[string]any)
	if nested["query"] != "search-term" {
		t.Errorf("nested.query = %v, want search-term", nested["query"])
	}
	if nested["fixed"] != "unchanged" {
		t.Errorf("nested.fixed = %v, want unchanged", nested["fixed"])
	}
}

func TestInjectItemVariable_Slice(t *testing.T) {
	args := map[string]any{
		"items": []any{"${item}", "static", "${item}"},
	}
	result := injectItemVariable(args, 42, nil, nil)

	items := result["items"].([]any)
	if items[0] != 42 {
		t.Errorf("items[0] = %v, want 42", items[0])
	}
	if items[1] != "static" {
		t.Errorf("items[1] = %v, want static", items[1])
	}
	if items[2] != 42 {
		t.Errorf("items[2] = %v, want 42", items[2])
	}
}

func TestInjectItemVariable_DeeplyNested(t *testing.T) {
	args := map[string]any{
		"level1": map[string]any{
			"level2": map[string]any{
				"level3": "${item}",
			},
		},
	}
	result := injectItemVariable(args, "deep-value", nil, nil)

	l1 := result["level1"].(map[string]any)
	l2 := l1["level2"].(map[string]any)
	if l2["level3"] != "deep-value" {
		t.Errorf("level3 = %v, want deep-value", l2["level3"])
	}
}

// --- MapReduce with non-[]any input type ---

func TestMapReduceStep_NonSliceInput(t *testing.T) {
	executor := func(_ context.Context, _, _ string, _ map[string]any) (map[string]any, error) {
		return map[string]any{}, nil
	}
	eng := NewWorkflowEngine(executor)

	step := &WorkflowStep{
		ID:          "mr",
		StepType:    StepTypeMapReduce,
		MapInputKey: "items",
		MapStepTemplate: &WorkflowStep{
			ServerName: "s",
			ToolName:   "t",
			ToolArgs:   map[string]any{"q": "${item}"},
		},
	}

	wf := &Workflow{
		ID:      "test",
		Context: map[string]any{"items": "not-a-slice"},
		Input:   map[string]any{},
	}

	_, err := eng.executeMapReduceStep(context.Background(), wf, step)
	if err == nil {
		t.Fatal("expected error for non-slice input")
	}
	if !strings.Contains(err.Error(), "got string") {
		t.Errorf("error = %v, want mention of 'got string'", err)
	}
}

// ---------------------------------------------------------------------------
// deepCopyMap
// ---------------------------------------------------------------------------

func TestDeepCopyMap_NestedMaps(t *testing.T) {
	t.Parallel()
	orig := map[string]any{
		"top": "value",
		"nested": map[string]any{
			"key": "inner",
		},
	}
	cp := deepCopyMap(orig)

	// Mutate the nested map in the copy.
	cp["nested"].(map[string]any)["key"] = "mutated"

	// Original must be unaffected.
	if orig["nested"].(map[string]any)["key"] != "inner" {
		t.Fatal("deepCopyMap shared nested map reference")
	}
}

func TestDeepCopyMap_Slices(t *testing.T) {
	t.Parallel()
	orig := map[string]any{
		"items": []any{"a", map[string]any{"b": 1}},
	}
	cp := deepCopyMap(orig)

	// Mutate nested map inside the slice copy.
	cp["items"].([]any)[1].(map[string]any)["b"] = 99

	if orig["items"].([]any)[1].(map[string]any)["b"] != 1 {
		t.Fatal("deepCopyMap shared slice element reference")
	}
}

func TestDeepCopyMap_Nil(t *testing.T) {
	t.Parallel()
	if deepCopyMap(nil) != nil {
		t.Fatal("deepCopyMap(nil) should return nil")
	}
}

// ---------------------------------------------------------------------------
// splitBoolOp (quote-aware)
// ---------------------------------------------------------------------------

func TestSplitBoolOp_QuotedAND(t *testing.T) {
	t.Parallel()
	cond := "step.name == 'CONNECT AND PLAY' AND gate.passed"
	parts := splitBoolOp(cond, " AND ")
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %v", len(parts), parts)
	}
	if parts[0] != "step.name == 'CONNECT AND PLAY'" {
		t.Errorf("parts[0] = %q", parts[0])
	}
	if parts[1] != "gate.passed" {
		t.Errorf("parts[1] = %q", parts[1])
	}
}

func TestSplitBoolOp_QuotedOR(t *testing.T) {
	t.Parallel()
	cond := "x == 'A OR B' OR y == 'C'"
	parts := splitBoolOp(cond, " OR ")
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d: %v", len(parts), parts)
	}
	if parts[0] != "x == 'A OR B'" {
		t.Errorf("parts[0] = %q", parts[0])
	}
	if parts[1] != "y == 'C'" {
		t.Errorf("parts[1] = %q", parts[1])
	}
}

func TestSplitBoolOp_NoQuotes(t *testing.T) {
	t.Parallel()
	// Verify existing behavior is preserved.
	cond := "a == true AND b == false AND c != 0"
	parts := splitBoolOp(cond, " AND ")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %v", len(parts), parts)
	}
	if parts[0] != "a == true" || parts[1] != "b == false" || parts[2] != "c != 0" {
		t.Errorf("unexpected parts: %v", parts)
	}
}
