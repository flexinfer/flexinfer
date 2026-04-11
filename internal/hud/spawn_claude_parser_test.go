package hud

import (
	"strings"
	"sync"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// ---------- mock sink ----------

type tokenCall struct {
	Input, Output, CacheCreate, CacheRead int
}

type toolStartCall struct {
	ID, Name, ServerName string
}

type toolEnsureCall struct {
	ID, Name, ServerName string
}

type toolCompleteCall struct {
	ID       string
	Duration int
	ExitCode *int
	ErrMsg   string
}

type fileChangeCall struct {
	Path, Kind               string
	LinesAdded, LinesRemoved int
}

type errorCall struct {
	ErrType, Message string
}

type resultCall struct {
	CostUSD    float64
	Turns      int
	StopReason string
}

type deltaSnapshotCall struct {
	SpawnID string
	AgentID string
}

type mockSink struct {
	mu             sync.Mutex
	tokens         []tokenCall
	toolStarts     []toolStartCall
	toolEnsures    []toolEnsureCall
	toolCompletes  []toolCompleteCall
	fileChanges    []fileChangeCall
	errors         []errorCall
	result         *resultCall
	lastMessage    string
	externalID     string
	turns          int
	estimatedCost  float64
	costEstimated  bool
	deltaSnapshots []deltaSnapshotCall
}

func (m *mockSink) AddTokens(input, output, cacheCreate, cacheRead int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens = append(m.tokens, tokenCall{input, output, cacheCreate, cacheRead})
}

func (m *mockSink) StartToolCall(id, name, serverName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolStarts = append(m.toolStarts, toolStartCall{id, name, serverName})
}

func (m *mockSink) EnsureToolCall(id, name, serverName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolEnsures = append(m.toolEnsures, toolEnsureCall{id, name, serverName})
}

func (m *mockSink) CompleteToolCall(id string, durationMs int, exitCode *int, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCompletes = append(m.toolCompletes, toolCompleteCall{id, durationMs, exitCode, errMsg})
}

func (m *mockSink) AddFileChange(path, kind string, linesAdded, linesRemoved int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fileChanges = append(m.fileChanges, fileChangeCall{path, kind, linesAdded, linesRemoved})
}

func (m *mockSink) AddError(errType, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors = append(m.errors, errorCall{errType, message})
}

func (m *mockSink) SetExternalSessionID(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.externalID = id
}

func (m *mockSink) SetResult(costUSD float64, turns int, stopReason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.result = &resultCall{costUSD, turns, stopReason}
}

func (m *mockSink) SetLastMessage(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastMessage = msg
}

func (m *mockSink) IncrementTurns() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turns++
}

func (m *mockSink) AddEstimatedCost(usd float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.estimatedCost += usd
	m.costEstimated = true
}

// TelemetryDeltaSnapshot records the call and returns a deterministic
// snapshot built from the mock's current in-memory counters. Tests use
// the recorded calls to assert parsers emit delta events at the right
// times, and assert on the returned struct to verify slim-payload shape.
func (m *mockSink) TelemetryDeltaSnapshot(spawnID, agentID string) bridge.SpawnTelemetryDelta {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deltaSnapshots = append(m.deltaSnapshots, deltaSnapshotCall{
		SpawnID: spawnID,
		AgentID: agentID,
	})
	var stopReason string
	var costUSD float64
	if m.result != nil {
		stopReason = m.result.StopReason
		costUSD = m.result.CostUSD
	}
	// Mirror AddEstimatedCost semantics so CostEstimated propagates into
	// the delta when only estimated cost was recorded (Codex path).
	if m.costEstimated {
		costUSD += m.estimatedCost
	}
	return bridge.SpawnTelemetryDelta{
		SpawnID:         spawnID,
		AgentID:         agentID,
		TurnCount:       m.turns,
		ToolCallCount:   len(m.toolStarts),
		FileChangeCount: len(m.fileChanges),
		ErrorCount:      len(m.errors),
		TotalCostUSD:    costUSD,
		CostEstimated:   m.costEstimated,
		LastMessage:     m.lastMessage,
		StopReason:      stopReason,
	}
}

// ---------- broadcast recorder ----------

type broadcastCall struct {
	EventType string
	AgentID   string
	Data      any
}

func recordingBroadcaster(calls *[]broadcastCall) SpawnEventBroadcaster {
	return func(eventType, agentID string, data any) {
		*calls = append(*calls, broadcastCall{eventType, agentID, data})
	}
}

// ---------- Claude parser tests ----------

func TestClaudeParser_AssistantTokenUsage(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	line := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_001","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":10,"cache_read_input_tokens":20},"content":[{"type":"text","text":"Hello world"}]}}`)
	p.HandleLine(line)

	if len(sink.tokens) != 1 {
		t.Fatalf("expected 1 token call, got %d", len(sink.tokens))
	}
	tc := sink.tokens[0]
	if tc.Input != 100 || tc.Output != 50 || tc.CacheCreate != 10 || tc.CacheRead != 20 {
		t.Errorf("unexpected token counts: %+v", tc)
	}
	if sink.lastMessage != "Hello world" {
		t.Errorf("expected last message 'Hello world', got %q", sink.lastMessage)
	}
	if sink.externalID != "sess-1" {
		t.Errorf("expected external session ID 'sess-1', got %q", sink.externalID)
	}
}

func TestClaudeParser_TokenDedup(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	// Two assistant messages with the same message.id should only count tokens once.
	line1 := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_001","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}`)
	line2 := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_001","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"tool_use","id":"toolu_2","name":"Read","input":{"file_path":"/tmp/foo"}}]}}`)

	p.HandleLine(line1)
	p.HandleLine(line2)

	if len(sink.tokens) != 1 {
		t.Fatalf("expected 1 token call (deduped), got %d", len(sink.tokens))
	}
	// Both tool starts should be recorded.
	if len(sink.toolStarts) != 2 {
		t.Fatalf("expected 2 tool starts, got %d", len(sink.toolStarts))
	}
}

func TestClaudeParser_ToolCallLifecycle(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	// Assistant sends tool_use.
	assistant := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_002","usage":{"input_tokens":50,"output_tokens":25,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"tool_use","id":"toolu_abc","name":"Bash","input":{"command":"echo hello"}}]}}`)
	p.HandleLine(assistant)

	if len(sink.toolStarts) != 1 {
		t.Fatalf("expected 1 tool start, got %d", len(sink.toolStarts))
	}
	if sink.toolStarts[0].ID != "toolu_abc" || sink.toolStarts[0].Name != "Bash" {
		t.Errorf("unexpected tool start: %+v", sink.toolStarts[0])
	}

	// User sends tool_result.
	user := []byte(`{"type":"user","content":[{"type":"tool_result","tool_use_id":"toolu_abc","content":"hello\n","is_error":false}]}`)
	p.HandleLine(user)

	if len(sink.toolCompletes) != 1 {
		t.Fatalf("expected 1 tool complete, got %d", len(sink.toolCompletes))
	}
	tc := sink.toolCompletes[0]
	if tc.ID != "toolu_abc" {
		t.Errorf("expected tool complete ID 'toolu_abc', got %q", tc.ID)
	}
	if tc.ErrMsg != "" {
		t.Errorf("expected no error, got %q", tc.ErrMsg)
	}
	if tc.Duration <= 0 {
		// Duration should be > 0 since we recorded the start time above.
		// In fast tests this could be 0ms, so we only check it's non-negative.
		if tc.Duration < 0 {
			t.Errorf("expected non-negative duration, got %d", tc.Duration)
		}
	}
}

func TestClaudeParser_ToolCallError(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	// Start a tool call.
	assistant := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_003","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"tool_use","id":"toolu_err","name":"Bash","input":{"command":"false"}}]}}`)
	p.HandleLine(assistant)

	// Complete with error.
	user := []byte(`{"type":"user","content":[{"type":"tool_result","tool_use_id":"toolu_err","content":"command failed with exit code 1","is_error":true}]}`)
	p.HandleLine(user)

	if len(sink.errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(sink.errors))
	}
	if sink.errors[0].ErrType != "tool_failure" {
		t.Errorf("expected error type 'tool_failure', got %q", sink.errors[0].ErrType)
	}
	if sink.errors[0].Message != "command failed with exit code 1" {
		t.Errorf("unexpected error message: %q", sink.errors[0].Message)
	}
}

func TestClaudeParser_MCPToolUseCapturesServerName(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewClaudeJSONLParser(sink, "test-agent", "", bc, nil)

	// The Claude SDK surfaces MCP tool invocations as a distinct
	// "mcp_tool_use" content block with an explicit server_name field. The
	// parser must forward that name into SpawnTelemetry.ToolCalls[].ServerName
	// so the HUD can group calls by MCP server.
	line := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_mcp","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"mcp_tool_use","id":"mcptool_abc","name":"tavily_search","server_name":"tavily","input":{"query":"loom"}}]}}`)
	p.HandleLine(line)

	if len(sink.toolStarts) != 1 {
		t.Fatalf("expected 1 tool start, got %d", len(sink.toolStarts))
	}
	ts := sink.toolStarts[0]
	if ts.ID != "mcptool_abc" {
		t.Errorf("expected tool id 'mcptool_abc', got %q", ts.ID)
	}
	if ts.Name != "tavily_search" {
		t.Errorf("expected tool name 'tavily_search', got %q", ts.Name)
	}
	if ts.ServerName != "tavily" {
		t.Errorf("expected server name 'tavily', got %q", ts.ServerName)
	}

	// MCP tool calls are not file mutations, so no file change should be
	// recorded.
	if len(sink.fileChanges) != 0 {
		t.Errorf("expected no file changes for mcp_tool_use, got %d", len(sink.fileChanges))
	}

	// The broadcast should include the server_name field so web/mobile clients
	// can render it without re-querying the canonical telemetry.
	var found bool
	for _, b := range broadcasts {
		if b.EventType != "agent.spawn.tool_start" {
			continue
		}
		data, ok := b.Data.(map[string]string)
		if !ok {
			t.Fatalf("expected tool_start broadcast payload to be map[string]string, got %T", b.Data)
		}
		if data["server_name"] != "tavily" {
			t.Errorf("expected broadcast server_name 'tavily', got %q", data["server_name"])
		}
		if data["id"] != "mcptool_abc" || data["name"] != "tavily_search" {
			t.Errorf("unexpected broadcast payload: %+v", data)
		}
		found = true
	}
	if !found {
		t.Error("expected agent.spawn.tool_start broadcast for mcp_tool_use block")
	}
}

func TestClaudeParser_MCPToolUseLifecycle(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	// mcp_tool_use followed by a tool_result should complete the tool call
	// through the same tool_use_id path as native tool_use blocks.
	assistant := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_mcp2","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"mcp_tool_use","id":"mcptool_xyz","name":"github_search","server_name":"github","input":{"q":"loom"}}]}}`)
	p.HandleLine(assistant)

	user := []byte(`{"type":"user","content":[{"type":"tool_result","tool_use_id":"mcptool_xyz","content":"ok","is_error":false}]}`)
	p.HandleLine(user)

	if len(sink.toolCompletes) != 1 {
		t.Fatalf("expected 1 tool complete, got %d", len(sink.toolCompletes))
	}
	if sink.toolCompletes[0].ID != "mcptool_xyz" {
		t.Errorf("expected completion for 'mcptool_xyz', got %q", sink.toolCompletes[0].ID)
	}
}

func TestClaudeParser_FileChangeInference(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	// Write tool call.
	writeCall := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_004","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"tool_use","id":"toolu_w1","name":"Write","input":{"file_path":"/workspace/foo.go","content":"package main"}}]}}`)
	p.HandleLine(writeCall)

	// Edit tool call.
	editCall := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_005","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"tool_use","id":"toolu_e1","name":"Edit","input":{"file_path":"/workspace/bar.go","old_string":"foo","new_string":"bar"}}]}}`)
	p.HandleLine(editCall)

	// NotebookEdit tool call.
	nbCall := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_006","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"tool_use","id":"toolu_n1","name":"NotebookEdit","input":{"file_path":"/workspace/notebook.ipynb","cell_number":0}}]}}`)
	p.HandleLine(nbCall)

	if len(sink.fileChanges) != 3 {
		t.Fatalf("expected 3 file changes, got %d", len(sink.fileChanges))
	}

	// Write: content = "package main" → 1 line added, 0 removed
	// Edit: old_string = "foo", new_string = "bar" → 1 line each
	// NotebookEdit: no content fields → 0, 0
	expected := []fileChangeCall{
		{"/workspace/foo.go", "modify", 1, 0},
		{"/workspace/bar.go", "modify", 1, 1},
		{"/workspace/notebook.ipynb", "modify", 0, 0},
	}
	for i, fc := range sink.fileChanges {
		if fc.Path != expected[i].Path || fc.Kind != expected[i].Kind ||
			fc.LinesAdded != expected[i].LinesAdded || fc.LinesRemoved != expected[i].LinesRemoved {
			t.Errorf("file change %d: expected %+v, got %+v", i, expected[i], fc)
		}
	}
}

func TestClaudeParser_Result(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	line := []byte(`{"type":"result","subtype":"success","session_id":"sess-1","duration_ms":45000,"num_turns":5,"total_cost_usd":0.42,"result":"Task completed successfully."}`)
	p.HandleLine(line)

	if sink.result == nil {
		t.Fatal("expected result to be set")
	}
	if sink.result.CostUSD != 0.42 {
		t.Errorf("expected cost 0.42, got %f", sink.result.CostUSD)
	}
	if sink.result.Turns != 5 {
		t.Errorf("expected 5 turns, got %d", sink.result.Turns)
	}
	if sink.result.StopReason != "end_turn" {
		t.Errorf("expected stop reason 'end_turn', got %q", sink.result.StopReason)
	}
	if sink.lastMessage != "Task completed successfully." {
		t.Errorf("expected last message 'Task completed successfully.', got %q", sink.lastMessage)
	}
	if len(sink.errors) != 0 {
		t.Errorf("expected no errors for success result, got %d", len(sink.errors))
	}
}

func TestClaudeParser_ResultError(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	line := []byte(`{"type":"result","subtype":"error_max_turns","session_id":"sess-1","duration_ms":120000,"num_turns":50,"total_cost_usd":5.00,"result":"Exceeded maximum turn limit."}`)
	p.HandleLine(line)

	if sink.result == nil {
		t.Fatal("expected result to be set")
	}
	if sink.result.StopReason != "max_turns" {
		t.Errorf("expected stop reason 'max_turns', got %q", sink.result.StopReason)
	}
	if len(sink.errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(sink.errors))
	}
	if sink.errors[0].ErrType != "error_max_turns" {
		t.Errorf("expected error type 'error_max_turns', got %q", sink.errors[0].ErrType)
	}
	if sink.errors[0].Message != "Exceeded maximum turn limit." {
		t.Errorf("unexpected error message: %q", sink.errors[0].Message)
	}
}

func TestClaudeParser_ResultPermissionDenials(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewClaudeJSONLParser(sink, "test-agent", "", bc, nil)

	// Successful result with two permission-denied tool calls. These should
	// surface as structured errors on SpawnTelemetry.Errors[] without
	// overriding the stop_reason (the run itself was a success).
	line := []byte(`{"type":"result","subtype":"success","session_id":"sess-1","duration_ms":10000,"num_turns":3,"total_cost_usd":0.08,"result":"done","permission_denials":[{"tool_name":"Bash","tool_use_id":"toolu_den1","tool_input":{"command":"rm -rf /"}},{"tool_name":"Write","tool_use_id":"toolu_den2","tool_input":{"file_path":"/etc/passwd"}}]}`)
	p.HandleLine(line)

	if sink.result == nil || sink.result.StopReason != "end_turn" {
		t.Fatalf("expected stop_reason 'end_turn', got %+v", sink.result)
	}

	if len(sink.errors) != 2 {
		t.Fatalf("expected 2 permission_denied errors, got %d: %+v", len(sink.errors), sink.errors)
	}
	for i, want := range []string{"Bash", "Write"} {
		if sink.errors[i].ErrType != "permission_denied" {
			t.Errorf("error %d: expected type 'permission_denied', got %q", i, sink.errors[i].ErrType)
		}
		if !strings.Contains(sink.errors[i].Message, want) {
			t.Errorf("error %d: expected message to contain %q, got %q", i, want, sink.errors[i].Message)
		}
	}
	// The tool_use_id should be carried in the message for correlation.
	if !strings.Contains(sink.errors[0].Message, "toolu_den1") {
		t.Errorf("expected message to include tool_use_id, got %q", sink.errors[0].Message)
	}

	// The result broadcast should carry a count so clients can badge without
	// re-querying the canonical telemetry.
	var found bool
	for _, b := range broadcasts {
		if b.EventType != "agent.spawn.result" {
			continue
		}
		data, ok := b.Data.(map[string]any)
		if !ok {
			t.Fatalf("expected result broadcast payload to be map[string]any, got %T", b.Data)
		}
		if data["permission_denials_len"] != 2 {
			t.Errorf("expected permission_denials_len 2, got %v", data["permission_denials_len"])
		}
		found = true
	}
	if !found {
		t.Error("expected agent.spawn.result broadcast")
	}
}

func TestClaudeParser_ResultNoPermissionDenials(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	// Absent permission_denials field must not synthesize errors.
	line := []byte(`{"type":"result","subtype":"success","session_id":"sess-1","duration_ms":5000,"num_turns":1,"total_cost_usd":0.01,"result":"ok"}`)
	p.HandleLine(line)

	for _, e := range sink.errors {
		if e.ErrType == "permission_denied" {
			t.Errorf("unexpected permission_denied error: %+v", e)
		}
	}
}

func TestClaudeParser_ResultErrorMaxBudget(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	line := []byte(`{"type":"result","subtype":"error_max_budget_usd","session_id":"sess-1","duration_ms":60000,"num_turns":10,"total_cost_usd":10.00,"result":"Budget exceeded."}`)
	p.HandleLine(line)

	if sink.result.StopReason != "max_budget" {
		t.Errorf("expected stop reason 'max_budget', got %q", sink.result.StopReason)
	}
	if len(sink.errors) != 1 || sink.errors[0].ErrType != "error_max_budget_usd" {
		t.Errorf("expected error_max_budget_usd error, got %+v", sink.errors)
	}
}

func TestClaudeParser_ResultErrorDuringExecution(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	line := []byte(`{"type":"result","subtype":"error_during_execution","session_id":"sess-1","duration_ms":5000,"num_turns":1,"total_cost_usd":0.01,"result":"Unexpected crash."}`)
	p.HandleLine(line)

	if sink.result.StopReason != "execution_error" {
		t.Errorf("expected stop reason 'execution_error', got %q", sink.result.StopReason)
	}
	if len(sink.errors) != 1 || sink.errors[0].ErrType != "error_during_execution" {
		t.Errorf("expected error_during_execution error, got %+v", sink.errors)
	}
}

func TestClaudeParser_RateLimit(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	line := []byte(`{"type":"system","subtype":"api_retry","attempt":2,"error_status":429}`)
	p.HandleLine(line)

	if len(sink.errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(sink.errors))
	}
	if sink.errors[0].ErrType != "rate_limit" {
		t.Errorf("expected error type 'rate_limit', got %q", sink.errors[0].ErrType)
	}
	if sink.errors[0].Message != "API retry attempt 2, status 429" {
		t.Errorf("unexpected error message: %q", sink.errors[0].Message)
	}
}

func TestClaudeParser_InvalidJSON(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	// Should not panic.
	p.HandleLine([]byte(`not json at all`))
	p.HandleLine([]byte(`{broken json`))
	p.HandleLine([]byte(``))
	p.HandleLine(nil)

	// Sink should remain empty.
	if len(sink.tokens) != 0 || len(sink.toolStarts) != 0 || len(sink.errors) != 0 {
		t.Error("expected sink to be empty after invalid input")
	}
}

func TestClaudeParser_UnknownType(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	line := []byte(`{"type":"future_event_type","data":"something"}`)
	p.HandleLine(line)

	// Should be skipped gracefully.
	if len(sink.tokens) != 0 || len(sink.toolStarts) != 0 || len(sink.errors) != 0 {
		t.Error("expected sink to be empty after unknown type")
	}
}

func TestClaudeParser_SessionIDSetOnce(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	line1 := []byte(`{"type":"assistant","session_id":"first-session","message":{"id":"msg_a","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[]}}`)
	line2 := []byte(`{"type":"assistant","session_id":"second-session","message":{"id":"msg_b","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[]}}`)

	p.HandleLine(line1)
	p.HandleLine(line2)

	if sink.externalID != "first-session" {
		t.Errorf("expected external ID 'first-session', got %q", sink.externalID)
	}
}

func TestClaudeParser_Broadcast(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewClaudeJSONLParser(sink, "test-agent", "", bc, nil)

	// Assistant with text and tool_use.
	line := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_bc","usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"Working on it"},{"type":"tool_use","id":"toolu_bc1","name":"Grep","input":{}}]}}`)
	p.HandleLine(line)

	if len(broadcasts) < 2 {
		t.Fatalf("expected at least 2 broadcast calls, got %d", len(broadcasts))
	}

	// Verify broadcast types.
	found := map[string]bool{}
	for _, bc := range broadcasts {
		found[bc.EventType] = true
		if bc.AgentID != "test-agent" {
			t.Errorf("expected agent ID 'test-agent', got %q", bc.AgentID)
		}
	}
	if !found["agent.spawn.message"] {
		t.Error("expected agent.spawn.message broadcast")
	}
	if !found["agent.spawn.tool_start"] {
		t.Error("expected agent.spawn.tool_start broadcast")
	}
}

func TestClaudeParser_ThinkingBroadcast(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewClaudeJSONLParser(sink, "test-agent", "", bc, nil)

	line := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_th","usage":{"input_tokens":5,"output_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"thinking","thinking":"Let me think about this..."}]}}`)
	p.HandleLine(line)

	found := false
	for _, bc := range broadcasts {
		if bc.EventType == "agent.spawn.thinking" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected agent.spawn.thinking broadcast")
	}
}

func TestClaudeParser_NoFileChangeForReadTool(t *testing.T) {
	sink := &mockSink{}
	p := NewClaudeJSONLParser(sink, "test-agent", "", nil, nil)

	// Read tool should not trigger file change.
	line := []byte(`{"type":"assistant","session_id":"sess-1","message":{"id":"msg_rd","usage":{"input_tokens":5,"output_tokens":3,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"tool_use","id":"toolu_rd","name":"Read","input":{"file_path":"/workspace/readme.md"}}]}}`)
	p.HandleLine(line)

	if len(sink.fileChanges) != 0 {
		t.Errorf("expected no file changes for Read tool, got %d", len(sink.fileChanges))
	}
}
