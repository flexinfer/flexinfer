package agentcontext

import (
	"encoding/json"
	"errors"
	"sort"
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
	// Unresolved returns the original string "${input.missing}" which is non-empty
	// and not "false" or "0", so it's truthy
	if !evaluateCondition("input.missing", nil, nil) {
		t.Error("expected true for unresolved string (non-empty)")
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
