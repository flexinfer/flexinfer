package hud

import (
	"testing"
)

// ---------- Gemini parser tests ----------

func TestGeminiParser_Init(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	line := []byte(`{"type":"init","session_id":"gemini-sess-abc","model":"gemini-2.5-pro"}`)
	p.HandleLine(line)

	if sink.externalID != "gemini-sess-abc" {
		t.Errorf("expected external session ID 'gemini-sess-abc', got %q", sink.externalID)
	}
}

func TestGeminiParser_MessageAssistantComplete(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", bc, nil)

	line := []byte(`{"type":"message","role":"assistant","content":"Here is the fix.","delta":false}`)
	p.HandleLine(line)

	if sink.turns != 1 {
		t.Errorf("expected 1 turn, got %d", sink.turns)
	}
	if sink.lastMessage != "Here is the fix." {
		t.Errorf("expected last message 'Here is the fix.', got %q", sink.lastMessage)
	}

	// Should broadcast agent.spawn.message.
	found := false
	for _, b := range broadcasts {
		if b.EventType == "agent.spawn.message" {
			found = true
			data, ok := b.Data.(map[string]string)
			if !ok {
				t.Fatalf("expected map[string]string, got %T", b.Data)
			}
			if data["text"] != "Here is the fix." {
				t.Errorf("expected broadcast text 'Here is the fix.', got %q", data["text"])
			}
			if data["spawn_id"] != "spawn-1" {
				t.Errorf("expected spawn_id 'spawn-1', got %q", data["spawn_id"])
			}
		}
	}
	if !found {
		t.Error("expected agent.spawn.message broadcast")
	}
}

func TestGeminiParser_MessageDeltaNotCounted(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	// Delta messages should not increment turns.
	line := []byte(`{"type":"message","role":"assistant","content":"partial","delta":true}`)
	p.HandleLine(line)

	if sink.turns != 0 {
		t.Errorf("expected 0 turns for delta message, got %d", sink.turns)
	}
	if sink.lastMessage != "" {
		t.Errorf("expected empty last message for delta, got %q", sink.lastMessage)
	}
}

func TestGeminiParser_MessageUserIgnored(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	line := []byte(`{"type":"message","role":"user","content":"hello","delta":false}`)
	p.HandleLine(line)

	if sink.turns != 0 {
		t.Errorf("expected 0 turns for user message, got %d", sink.turns)
	}
}

func TestGeminiParser_ToolUseAndResult(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", bc, nil)

	// Tool use.
	toolUse := []byte(`{"type":"tool_use","tool_name":"read_file","tool_id":"tool-abc"}`)
	p.HandleLine(toolUse)

	if len(sink.toolStarts) != 1 {
		t.Fatalf("expected 1 tool start, got %d", len(sink.toolStarts))
	}
	if sink.toolStarts[0].ID != "tool-abc" || sink.toolStarts[0].Name != "read_file" {
		t.Errorf("unexpected tool start: %+v", sink.toolStarts[0])
	}

	// Tool result.
	toolResult := []byte(`{"type":"tool_result","tool_id":"tool-abc","status":"success","output":"file contents"}`)
	p.HandleLine(toolResult)

	if len(sink.toolCompletes) != 1 {
		t.Fatalf("expected 1 tool complete, got %d", len(sink.toolCompletes))
	}
	tc := sink.toolCompletes[0]
	if tc.ID != "tool-abc" {
		t.Errorf("expected tool ID 'tool-abc', got %q", tc.ID)
	}
	if tc.Duration < 0 {
		t.Errorf("expected non-negative duration, got %d", tc.Duration)
	}
	if tc.ErrMsg != "" {
		t.Errorf("expected no error, got %q", tc.ErrMsg)
	}

	// Check broadcasts.
	foundStart, foundComplete := false, false
	for _, b := range broadcasts {
		switch b.EventType {
		case "agent.spawn.tool_start":
			foundStart = true
			data := b.Data.(map[string]string)
			if data["name"] != "read_file" {
				t.Errorf("expected tool name 'read_file', got %q", data["name"])
			}
		case "agent.spawn.tool_complete":
			foundComplete = true
			data := b.Data.(map[string]any)
			if data["id"] != "tool-abc" {
				t.Errorf("expected tool id 'tool-abc', got %v", data["id"])
			}
		}
	}
	if !foundStart {
		t.Error("expected agent.spawn.tool_start broadcast")
	}
	if !foundComplete {
		t.Error("expected agent.spawn.tool_complete broadcast")
	}
}

func TestGeminiParser_ToolResultError(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	// Start tool.
	p.HandleLine([]byte(`{"type":"tool_use","tool_name":"bash","tool_id":"tool-err"}`))

	// Complete with error.
	p.HandleLine([]byte(`{"type":"tool_result","tool_id":"tool-err","status":"error","output":"","error":{"type":"execution_error","message":"command failed"}}`))

	if len(sink.toolCompletes) != 1 {
		t.Fatalf("expected 1 tool complete, got %d", len(sink.toolCompletes))
	}
	if sink.toolCompletes[0].ErrMsg != "command failed" {
		t.Errorf("expected error 'command failed', got %q", sink.toolCompletes[0].ErrMsg)
	}
}

func TestGeminiParser_Error(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	line := []byte(`{"type":"error","severity":"fatal","message":"API key invalid"}`)
	p.HandleLine(line)

	if len(sink.errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(sink.errors))
	}
	if sink.errors[0].ErrType != "fatal" {
		t.Errorf("expected error type 'fatal', got %q", sink.errors[0].ErrType)
	}
	if sink.errors[0].Message != "API key invalid" {
		t.Errorf("expected message 'API key invalid', got %q", sink.errors[0].Message)
	}
}

func TestGeminiParser_ResultWithStats(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", bc, nil)

	line := []byte(`{"type":"result","status":"success","stats":{"total_tokens":5000,"input_tokens":3000,"output_tokens":2000,"cached":500,"input":2500,"duration_ms":45000,"tool_calls":12,"models":{"gemini-2.5-pro":{"total_tokens":5000,"input_tokens":3000,"output_tokens":2000,"cached":500,"input":2500}}}}`)
	p.HandleLine(line)

	if sink.result == nil {
		t.Fatal("expected result to be set")
	}
	if sink.result.StopReason != "success" {
		t.Errorf("expected stop reason 'success', got %q", sink.result.StopReason)
	}
	// Cost should be 0 (Gemini has no cost API).
	if sink.result.CostUSD != 0 {
		t.Errorf("expected cost 0, got %f", sink.result.CostUSD)
	}

	// Token usage: net input = 3000 - 500 = 2500.
	if len(sink.tokens) != 1 {
		t.Fatalf("expected 1 token call, got %d", len(sink.tokens))
	}
	tc := sink.tokens[0]
	if tc.Input != 2500 {
		t.Errorf("expected net input 2500, got %d", tc.Input)
	}
	if tc.Output != 2000 {
		t.Errorf("expected output 2000, got %d", tc.Output)
	}
	if tc.CacheRead != 500 {
		t.Errorf("expected cache read 500, got %d", tc.CacheRead)
	}

	// Broadcast should carry duration_ms.
	found := false
	for _, b := range broadcasts {
		if b.EventType == "agent.spawn.result" {
			found = true
			data := b.Data.(map[string]any)
			if data["duration_ms"] != 45000 {
				t.Errorf("expected duration_ms 45000, got %v", data["duration_ms"])
			}
			if data["spawn_id"] != "spawn-1" {
				t.Errorf("expected spawn_id 'spawn-1', got %v", data["spawn_id"])
			}
		}
	}
	if !found {
		t.Error("expected agent.spawn.result broadcast")
	}
}

func TestGeminiParser_ResultWithError(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	line := []byte(`{"type":"result","status":"error","error":{"type":"rate_limit","message":"Too many requests"}}`)
	p.HandleLine(line)

	if sink.result == nil {
		t.Fatal("expected result to be set")
	}
	if sink.result.StopReason != "error" {
		t.Errorf("expected stop reason 'error', got %q", sink.result.StopReason)
	}
	if len(sink.errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(sink.errors))
	}
	if sink.errors[0].ErrType != "rate_limit" {
		t.Errorf("expected error type 'rate_limit', got %q", sink.errors[0].ErrType)
	}
}

func TestGeminiParser_ResultNoStats(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	line := []byte(`{"type":"result","status":"success"}`)
	p.HandleLine(line)

	if sink.result == nil {
		t.Fatal("expected result to be set")
	}
	if sink.result.StopReason != "success" {
		t.Errorf("expected stop reason 'success', got %q", sink.result.StopReason)
	}
	// No stats = no token call.
	if len(sink.tokens) != 0 {
		t.Errorf("expected 0 token calls, got %d", len(sink.tokens))
	}
}

func TestGeminiParser_InvalidJSON(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	// Should not panic.
	p.HandleLine([]byte(`not json`))
	p.HandleLine([]byte(`{broken`))
	p.HandleLine([]byte(``))
	p.HandleLine(nil)

	if len(sink.tokens) != 0 || len(sink.toolStarts) != 0 || len(sink.errors) != 0 {
		t.Error("expected sink to be empty after invalid input")
	}
}

func TestGeminiParser_UnknownType(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	line := []byte(`{"type":"future_event","data":"whatever"}`)
	p.HandleLine(line)

	if len(sink.tokens) != 0 || len(sink.toolStarts) != 0 || len(sink.errors) != 0 {
		t.Error("expected sink to be empty after unknown type")
	}
}

func TestGeminiParser_NegativeNetInputClamped(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	// Edge case: cached > input_tokens should clamp net input to 0.
	line := []byte(`{"type":"result","status":"success","stats":{"total_tokens":1000,"input_tokens":100,"output_tokens":900,"cached":200,"input":0,"duration_ms":1000,"tool_calls":0}}`)
	p.HandleLine(line)

	if len(sink.tokens) != 1 {
		t.Fatalf("expected 1 token call, got %d", len(sink.tokens))
	}
	if sink.tokens[0].Input != 0 {
		t.Errorf("expected clamped input 0, got %d", sink.tokens[0].Input)
	}
}

func TestGeminiParser_TelemetryDeltaEmitted(t *testing.T) {
	sink := &mockSink{}
	var broadcasts []broadcastCall
	bc := recordingBroadcaster(&broadcasts)
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", bc, nil)

	// Each event type should emit a telemetry delta.
	p.HandleLine([]byte(`{"type":"init","session_id":"s1","model":"gemini-2.5-pro"}`))
	p.HandleLine([]byte(`{"type":"message","role":"assistant","content":"hello","delta":false}`))
	p.HandleLine([]byte(`{"type":"tool_use","tool_name":"bash","tool_id":"t1"}`))
	p.HandleLine([]byte(`{"type":"tool_result","tool_id":"t1","status":"success"}`))
	p.HandleLine([]byte(`{"type":"error","severity":"warn","message":"oops"}`))
	p.HandleLine([]byte(`{"type":"result","status":"success"}`))

	// Should have 6 delta snapshot calls (one per event).
	if len(sink.deltaSnapshots) != 6 {
		t.Errorf("expected 6 delta snapshots, got %d", len(sink.deltaSnapshots))
	}

	// Each should broadcast SpawnTelemetryDeltaEvent.
	deltaCount := 0
	for _, b := range broadcasts {
		if b.EventType == SpawnTelemetryDeltaEvent {
			deltaCount++
		}
	}
	if deltaCount != 6 {
		t.Errorf("expected 6 telemetry delta broadcasts, got %d", deltaCount)
	}
}

func TestGeminiParser_ToolResultWithoutStart(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	// tool_result without a matching tool_use should still complete gracefully.
	p.HandleLine([]byte(`{"type":"tool_result","tool_id":"orphan","status":"success"}`))

	if len(sink.toolCompletes) != 1 {
		t.Fatalf("expected 1 tool complete, got %d", len(sink.toolCompletes))
	}
	if sink.toolCompletes[0].Duration != 0 {
		t.Errorf("expected 0 duration for orphan tool result, got %d", sink.toolCompletes[0].Duration)
	}
}

func TestGeminiParser_MultipleMessages(t *testing.T) {
	sink := &mockSink{}
	p := NewGeminiJSONLParser(sink, "test-agent", "spawn-1", nil, nil)

	p.HandleLine([]byte(`{"type":"message","role":"assistant","content":"First","delta":false}`))
	p.HandleLine([]byte(`{"type":"message","role":"assistant","content":"Second","delta":false}`))
	p.HandleLine([]byte(`{"type":"message","role":"assistant","content":"Third","delta":false}`))

	if sink.turns != 3 {
		t.Errorf("expected 3 turns, got %d", sink.turns)
	}
	if sink.lastMessage != "Third" {
		t.Errorf("expected last message 'Third', got %q", sink.lastMessage)
	}
}
