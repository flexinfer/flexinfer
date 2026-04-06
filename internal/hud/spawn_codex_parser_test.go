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

	line := []byte(`{"type":"turn.completed","usage":{"input_tokens":500,"cached_input_tokens":200,"output_tokens":150}}`)
	p.HandleLine(line)

	if len(sink.tokens) != 1 {
		t.Fatalf("expected 1 token call, got %d", len(sink.tokens))
	}
	tc := sink.tokens[0]
	if tc.Input != 500 || tc.Output != 150 || tc.CacheCreate != 0 || tc.CacheRead != 200 {
		t.Errorf("unexpected token counts: input=%d output=%d cacheCreate=%d cacheRead=%d",
			tc.Input, tc.Output, tc.CacheCreate, tc.CacheRead)
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
