package hud

import (
	"testing"
)

// Note: mockSink and related helpers are defined in spawn_claude_parser_test.go.

func TestCodexParser_ThreadStarted(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	line := []byte(`{"type":"thread.started","thread_id":"thread_abc123"}`)
	p.HandleLine(line)

	if sink.externalID != "thread_abc123" {
		t.Errorf("expected external ID 'thread_abc123', got %q", sink.externalID)
	}
}

func TestCodexParser_TurnStarted(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	p.HandleLine([]byte(`{"type":"turn.started"}`))
	p.HandleLine([]byte(`{"type":"turn.started"}`))

	if sink.turns != 2 {
		t.Errorf("expected 2 turns, got %d", sink.turns)
	}
}

func TestCodexParser_TurnCompleted(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	// OpenAI Responses API convention: input_tokens is the TOTAL (500),
	// cached_input_tokens is a subset already included in that total (200).
	// The canonical SpawnTokenUsage treats InputTokens and CacheReadTokens as
	// additive, so fresh input = 500 - 200 = 300 must be reported to the sink
	// to avoid double-counting cache hits.
	line := []byte(`{"type":"turn.completed","usage":{"input_tokens":500,"cached_input_tokens":200,"output_tokens":150}}`)
	p.HandleLine(line)

	if len(sink.tokens) != 1 {
		t.Fatalf("expected 1 token call, got %d", len(sink.tokens))
	}
	tc := sink.tokens[0]
	if tc.Input != 300 || tc.Output != 150 || tc.CacheCreate != 0 || tc.CacheRead != 200 {
		t.Errorf("unexpected token counts: input=%d output=%d cacheCreate=%d cacheRead=%d "+
			"(expected input=300 output=150 cacheCreate=0 cacheRead=200)",
			tc.Input, tc.Output, tc.CacheCreate, tc.CacheRead)
	}

	// Sum must match Codex's billable total (input_tokens + output_tokens).
	// This is the contract the HUD and mobile app rely on for cost math.
	if got := tc.Input + tc.CacheRead + tc.Output; got != 650 {
		t.Errorf("billable total mismatch: input+cacheRead+output = %d, want 650", got)
	}
}

func TestCodexParser_TurnCompletedNoCacheHit(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	// Cold turn: no cache hits, entire input is fresh. With cached_input_tokens
	// == 0 the split should be a no-op (fresh == total).
	line := []byte(`{"type":"turn.completed","usage":{"input_tokens":400,"cached_input_tokens":0,"output_tokens":75}}`)
	p.HandleLine(line)

	tc := sink.tokens[0]
	if tc.Input != 400 || tc.CacheRead != 0 || tc.Output != 75 {
		t.Errorf("unexpected cold-turn token counts: %+v", tc)
	}
}

func TestCodexParser_TurnCompletedCachedExceedsInputClamp(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	// Defensive edge case: cached > input should never happen per the
	// OpenAI contract, but if it does the parser must clamp fresh input to
	// zero rather than forwarding a negative count to the accumulator.
	line := []byte(`{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":150,"output_tokens":25}}`)
	p.HandleLine(line)

	if len(sink.tokens) != 1 {
		t.Fatalf("expected 1 token call, got %d", len(sink.tokens))
	}
	tc := sink.tokens[0]
	if tc.Input != 0 {
		t.Errorf("expected fresh input clamped to 0, got %d", tc.Input)
	}
	if tc.CacheRead != 150 {
		t.Errorf("expected CacheRead 150, got %d", tc.CacheRead)
	}
	if tc.Output != 25 {
		t.Errorf("expected Output 25, got %d", tc.Output)
	}
}

func TestCodexParser_CommandExecution(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	// item.started with command_execution.
	started := []byte(`{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"echo hello","status":"in_progress"}}`)
	p.HandleLine(started)

	if len(sink.toolStarts) != 1 {
		t.Fatalf("expected 1 tool start, got %d", len(sink.toolStarts))
	}
	if sink.toolStarts[0].ID != "item_1" || sink.toolStarts[0].Name != "Bash" {
		t.Errorf("unexpected tool start: %+v", sink.toolStarts[0])
	}

	// item.completed with exit_code 0.
	exitZero := 0
	completed := []byte(`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"echo hello","exit_code":0,"status":"completed"}}`)
	p.HandleLine(completed)

	if len(sink.toolCompletes) != 1 {
		t.Fatalf("expected 1 tool complete, got %d", len(sink.toolCompletes))
	}
	tc := sink.toolCompletes[0]
	if tc.ID != "item_1" {
		t.Errorf("expected ID 'item_1', got %q", tc.ID)
	}
	if tc.ExitCode == nil || *tc.ExitCode != exitZero {
		t.Errorf("expected exit code 0, got %v", tc.ExitCode)
	}
	if tc.ErrMsg != "" {
		t.Errorf("expected no error, got %q", tc.ErrMsg)
	}
}

func TestCodexParser_CommandExecutionFailure(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	// Command with non-zero exit code and stderr.
	completed := []byte(`{"type":"item.completed","item":{"id":"item_2","type":"command_execution","command":"false","exit_code":1,"status":"completed","stderr":"command not found"}}`)
	p.HandleLine(completed)

	if len(sink.toolCompletes) != 1 {
		t.Fatalf("expected 1 tool complete, got %d", len(sink.toolCompletes))
	}
	tc := sink.toolCompletes[0]
	if tc.ExitCode == nil || *tc.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %v", tc.ExitCode)
	}
	if tc.ErrMsg != "command not found" {
		t.Errorf("expected error 'command not found', got %q", tc.ErrMsg)
	}

	if len(sink.errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(sink.errors))
	}
	if sink.errors[0].ErrType != "tool_failure" {
		t.Errorf("expected error type 'tool_failure', got %q", sink.errors[0].ErrType)
	}
}

func TestCodexParser_FileChange(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	line := []byte(`{"type":"item.completed","item":{"id":"item_3","type":"file_change","changes":[{"path":"src/main.go","kind":"modify"},{"path":"src/new.go","kind":"create"}]}}`)
	p.HandleLine(line)

	if len(sink.fileChanges) != 2 {
		t.Fatalf("expected 2 file changes, got %d", len(sink.fileChanges))
	}
	if sink.fileChanges[0].Path != "src/main.go" || sink.fileChanges[0].Kind != "modify" {
		t.Errorf("unexpected file change 0: %+v", sink.fileChanges[0])
	}
	if sink.fileChanges[1].Path != "src/new.go" || sink.fileChanges[1].Kind != "create" {
		t.Errorf("unexpected file change 1: %+v", sink.fileChanges[1])
	}
}

func TestCodexParser_MCPToolCall(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	// item.started for MCP tool call.
	started := []byte(`{"type":"item.started","item":{"id":"item_mcp","type":"mcp_tool_call","tool":"read_file","server":"filesystem","status":"in_progress"}}`)
	p.HandleLine(started)

	if len(sink.toolStarts) != 1 {
		t.Fatalf("expected 1 tool start, got %d", len(sink.toolStarts))
	}
	if sink.toolStarts[0].Name != "read_file" || sink.toolStarts[0].ServerName != "filesystem" {
		t.Errorf("unexpected tool start: %+v", sink.toolStarts[0])
	}

	// Successful completion.
	completed := []byte(`{"type":"item.completed","item":{"id":"item_mcp","type":"mcp_tool_call","tool":"read_file","server":"filesystem","status":"completed"}}`)
	p.HandleLine(completed)

	if len(sink.toolCompletes) != 1 {
		t.Fatalf("expected 1 tool complete, got %d", len(sink.toolCompletes))
	}
	if sink.toolCompletes[0].ErrMsg != "" {
		t.Errorf("expected no error, got %q", sink.toolCompletes[0].ErrMsg)
	}
}

// TestCodexParser_MCPToolCall_OnlyCompleted_PreservesServerName covers the
// path where the Codex SDK skips item.started for a synchronous mcp_tool_call
// and only emits item.completed. Slice 9a (claude side) made server_name a
// first-class field on ToolCallEntry; this test guards the symmetric behavior
// for Codex by verifying that handleMCPToolCall defensively calls
// EnsureToolCall so the server attribution survives even when no prior
// StartToolCall occurred.
func TestCodexParser_MCPToolCall_OnlyCompleted_PreservesServerName(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	// Only item.completed -- no prior item.started.
	line := []byte(`{"type":"item.completed","item":{"id":"item_mcp_only_done","type":"mcp_tool_call","tool":"read_file","server":"filesystem","status":"completed"}}`)
	p.HandleLine(line)

	if len(sink.toolStarts) != 0 {
		t.Errorf("expected 0 tool starts (no item.started fired), got %d", len(sink.toolStarts))
	}
	if len(sink.toolEnsures) != 1 {
		t.Fatalf("expected 1 EnsureToolCall, got %d", len(sink.toolEnsures))
	}
	if sink.toolEnsures[0].ServerName != "filesystem" {
		t.Errorf("expected server_name=filesystem, got %q", sink.toolEnsures[0].ServerName)
	}
	if sink.toolEnsures[0].Name != "read_file" {
		t.Errorf("expected name=read_file, got %q", sink.toolEnsures[0].Name)
	}
	if sink.toolEnsures[0].ID != "item_mcp_only_done" {
		t.Errorf("expected id=item_mcp_only_done, got %q", sink.toolEnsures[0].ID)
	}
	if len(sink.toolCompletes) != 1 {
		t.Fatalf("expected 1 tool complete, got %d", len(sink.toolCompletes))
	}
	if sink.toolCompletes[0].ID != "item_mcp_only_done" {
		t.Errorf("expected complete id=item_mcp_only_done, got %q", sink.toolCompletes[0].ID)
	}
}

// TestCodexParser_MCPToolCall_StartedThenCompleted_EnsureIsIdempotent
// guards the existing started+completed flow. EnsureToolCall must remain
// idempotent so that when item.started already created the entry with the
// server name, the completion path's defensive Ensure call does not
// duplicate it or stomp on the existing data.
func TestCodexParser_MCPToolCall_StartedThenCompleted_EnsureIsIdempotent(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	started := []byte(`{"type":"item.started","item":{"id":"item_mcp_idem","type":"mcp_tool_call","tool":"read_file","server":"filesystem","status":"in_progress"}}`)
	p.HandleLine(started)

	completed := []byte(`{"type":"item.completed","item":{"id":"item_mcp_idem","type":"mcp_tool_call","tool":"read_file","server":"filesystem","status":"completed"}}`)
	p.HandleLine(completed)

	if len(sink.toolStarts) != 1 {
		t.Fatalf("expected 1 tool start, got %d", len(sink.toolStarts))
	}
	if sink.toolStarts[0].ServerName != "filesystem" {
		t.Errorf("expected start server_name=filesystem, got %q", sink.toolStarts[0].ServerName)
	}
	// EnsureToolCall must still fire on completion (defensive), even though
	// it is a no-op for the accumulator in this case.
	if len(sink.toolEnsures) != 1 {
		t.Fatalf("expected 1 EnsureToolCall, got %d", len(sink.toolEnsures))
	}
	if sink.toolEnsures[0].ServerName != "filesystem" {
		t.Errorf("expected ensure server_name=filesystem, got %q", sink.toolEnsures[0].ServerName)
	}
	if len(sink.toolCompletes) != 1 {
		t.Fatalf("expected 1 tool complete, got %d", len(sink.toolCompletes))
	}
}

func TestCodexParser_MCPToolCallError(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	completed := []byte(`{"type":"item.completed","item":{"id":"item_mcp_err","type":"mcp_tool_call","tool":"write_file","server":"filesystem","error":"permission denied"}}`)
	p.HandleLine(completed)

	if len(sink.toolCompletes) != 1 {
		t.Fatalf("expected 1 tool complete, got %d", len(sink.toolCompletes))
	}
	if sink.toolCompletes[0].ErrMsg != "permission denied" {
		t.Errorf("expected error 'permission denied', got %q", sink.toolCompletes[0].ErrMsg)
	}
	if len(sink.errors) != 1 || sink.errors[0].ErrType != "tool_failure" {
		t.Errorf("expected tool_failure error, got %+v", sink.errors)
	}
}

func TestCodexParser_AgentMessage(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewCodexJSONLParser(sink, "test-codex", bc, nil)

	line := []byte(`{"type":"item.completed","item":{"id":"item_msg","type":"agent_message","text":"I've completed the task."}}`)
	p.HandleLine(line)

	if sink.lastMessage != "I've completed the task." {
		t.Errorf("expected last message, got %q", sink.lastMessage)
	}

	found := false
	for _, b := range broadcasts {
		if b.EventType == "agent.spawn.message" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected agent.spawn.message broadcast")
	}
}

func TestCodexParser_AgentMessageFallbackToField(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	// Some codex versions use "message" instead of "text".
	line := []byte(`{"type":"item.completed","item":{"id":"item_msg2","type":"agent_message","message":"Fallback message field."}}`)
	p.HandleLine(line)

	if sink.lastMessage != "Fallback message field." {
		t.Errorf("expected last message 'Fallback message field.', got %q", sink.lastMessage)
	}
}

func TestCodexParser_TurnFailed(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	p.HandleLine([]byte(`{"type":"turn.failed"}`))

	if len(sink.errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(sink.errors))
	}
	if sink.errors[0].ErrType != "execution" || sink.errors[0].Message != "turn failed" {
		t.Errorf("unexpected error: %+v", sink.errors[0])
	}
}

func TestCodexParser_Error(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	line := []byte(`{"type":"error","message":"API key invalid"}`)
	p.HandleLine(line)

	if len(sink.errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(sink.errors))
	}
	if sink.errors[0].ErrType != "fatal" || sink.errors[0].Message != "API key invalid" {
		t.Errorf("unexpected error: %+v", sink.errors[0])
	}
}

func TestCodexParser_InvalidJSON(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	// Should not panic.
	p.HandleLine([]byte(`not json`))
	p.HandleLine([]byte(`{broken`))
	p.HandleLine([]byte(``))
	p.HandleLine(nil)

	if len(sink.tokens) != 0 || len(sink.toolStarts) != 0 || len(sink.errors) != 0 {
		t.Error("expected sink to be empty after invalid input")
	}
}

func TestCodexParser_UnknownItemType(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	line := []byte(`{"type":"item.completed","item":{"id":"item_unknown","type":"future_type","data":"something"}}`)
	p.HandleLine(line)

	// Should be skipped gracefully.
	if len(sink.toolCompletes) != 0 || len(sink.errors) != 0 {
		t.Error("expected no tool completes or errors for unknown item type")
	}
}

func TestCodexParser_UnknownEventType(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	line := []byte(`{"type":"future.event","data":"something"}`)
	p.HandleLine(line)

	if len(sink.tokens) != 0 || len(sink.errors) != 0 {
		t.Error("expected sink to be empty after unknown event type")
	}
}

func TestCodexParser_ReasoningBroadcast(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewCodexJSONLParser(sink, "test-codex", bc, nil)

	line := []byte(`{"type":"item.completed","item":{"id":"item_r","type":"reasoning","text":"Thinking deeply..."}}`)
	p.HandleLine(line)

	found := false
	for _, b := range broadcasts {
		if b.EventType == "agent.spawn.reasoning" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected agent.spawn.reasoning broadcast")
	}

	// Reasoning should not be stored in sink.
	if sink.lastMessage != "" {
		t.Errorf("reasoning should not set lastMessage, got %q", sink.lastMessage)
	}
}

func TestCodexParser_TodoBroadcast(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewCodexJSONLParser(sink, "test-codex", bc, nil)

	line := []byte(`{"type":"item.completed","item":{"id":"item_todo","type":"todo_list","text":"1. Fix bug\n2. Add tests"}}`)
	p.HandleLine(line)

	found := false
	for _, b := range broadcasts {
		if b.EventType == "agent.spawn.todo" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected agent.spawn.todo broadcast")
	}
}

func TestCodexParser_ErrorItemType(t *testing.T) {
	sink := &mockSink{}
	p := NewCodexJSONLParser(sink, "test-codex", nil, nil)

	line := []byte(`{"type":"item.completed","item":{"id":"item_err","type":"error","message":"something went wrong"}}`)
	p.HandleLine(line)

	if len(sink.errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(sink.errors))
	}
	if sink.errors[0].ErrType != "execution" || sink.errors[0].Message != "something went wrong" {
		t.Errorf("unexpected error: %+v", sink.errors[0])
	}
}

func TestCodexParser_CommandExecutionBroadcast(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewCodexJSONLParser(sink, "test-codex", bc, nil)

	completed := []byte(`{"type":"item.completed","item":{"id":"item_bc","type":"command_execution","command":"make test","exit_code":0,"status":"completed"}}`)
	p.HandleLine(completed)

	found := false
	for _, b := range broadcasts {
		if b.EventType == "agent.spawn.tool_complete" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected agent.spawn.tool_complete broadcast")
	}
}
