package codexwatch

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMapRecord_RealFixture replays a trimmed real Codex Desktop session
// file and asserts the resulting canonical events line up with the slice
// spec (.loom/23-product-spec-codex-session-tail-2026-05-16.md):
// session_meta → session.start; task_started → agent.status.change;
// function_call/function_call_output → tool.call.start/end pairs.
func TestMapRecord_RealFixture(t *testing.T) {
	path := filepath.Join("testdata", "rollout-sample.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	state := &SessionState{}
	var (
		gotStart      int
		gotStatus     int
		gotToolStart  int
		gotToolEnd    int
		startCallIDs  = map[string]bool{}
		endCallIDs    = map[string]bool{}
		seenSessionID string
		seenAgentID   string
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		events, err := MapRecord(line, state)
		if err != nil {
			t.Fatalf("MapRecord: %v\nline=%s", err, string(line))
		}
		for _, ev := range events {
			switch ev.Type {
			case EventSessionStart:
				gotStart++
				if sid, _ := ev.Payload["session_id"].(string); sid != "" {
					seenSessionID = sid
				}
				if aid, _ := ev.Payload["agent_id"].(string); aid != "" {
					seenAgentID = aid
				}
				if at, _ := ev.Payload["agent_type"].(string); at != AgentType {
					t.Errorf("session.start agent_type=%q want %q", at, AgentType)
				}
			case EventAgentStatusChange:
				gotStatus++
			case EventToolCallStart:
				gotToolStart++
				if cid, _ := ev.Payload["call_id"].(string); cid != "" {
					startCallIDs[cid] = true
				}
				if tn, _ := ev.Payload["tool_name"].(string); tn == "" {
					t.Errorf("tool.call.start missing tool_name")
				}
			case EventToolCallEnd:
				gotToolEnd++
				if cid, _ := ev.Payload["call_id"].(string); cid != "" {
					endCallIDs[cid] = true
				}
			default:
				t.Errorf("unexpected event type %q", ev.Type)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}

	if gotStart != 1 {
		t.Errorf("session.start count = %d, want 1", gotStart)
	}
	if gotStatus < 1 {
		t.Errorf("agent.status.change count = %d, want >=1", gotStatus)
	}
	if gotToolStart < 1 {
		t.Errorf("tool.call.start count = %d, want >=1", gotToolStart)
	}
	if gotToolEnd < 1 {
		t.Errorf("tool.call.end count = %d, want >=1", gotToolEnd)
	}
	if gotToolStart != gotToolEnd {
		t.Errorf("tool start/end mismatch: %d starts, %d ends", gotToolStart, gotToolEnd)
	}

	// Every start should pair with an end by call_id (single fixture).
	for cid := range startCallIDs {
		if !endCallIDs[cid] {
			t.Errorf("call_id %q started but never ended", cid)
		}
	}

	if state.SessionID == "" {
		t.Fatal("state.SessionID empty after replay")
	}
	if seenSessionID != state.SessionID {
		t.Errorf("session.start payload session_id=%q != state %q", seenSessionID, state.SessionID)
	}
	if !strings.HasPrefix(seenAgentID, agentIDPrefix) {
		t.Errorf("agent_id %q lacks prefix %q", seenAgentID, agentIDPrefix)
	}
	if state.Originator != "Codex Desktop" {
		t.Errorf("originator = %q, want Codex Desktop", state.Originator)
	}
}

// TestMapRecord_SkipsBeforeSessionMeta confirms that records appearing
// before session_meta (impossible in real Codex output, but cheap to
// guard) do not emit events with empty session_id.
func TestMapRecord_SkipsBeforeSessionMeta(t *testing.T) {
	state := &SessionState{}
	line := []byte(`{"timestamp":"2026-05-16T21:17:00.582Z","type":"response_item","payload":{"type":"function_call","name":"exec","call_id":"c1","arguments":"{}"}}`)
	events, err := MapRecord(line, state)
	if err != nil {
		t.Fatalf("MapRecord: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events before session_meta, got %d", len(events))
	}
}

// TestMapRecord_UnknownTypesAreSilent confirms forward-compatibility:
// future top-level types and payload subtypes return (nil, nil) so the
// watcher keeps running across Codex CLI upgrades.
func TestMapRecord_UnknownTypesAreSilent(t *testing.T) {
	state := &SessionState{SessionID: "s1", AgentID: "codex-desktop-s1"}
	cases := []string{
		`{"type":"future_record","payload":{"x":1}}`,
		`{"type":"response_item","payload":{"type":"unknown_subtype"}}`,
		`{"type":"event_msg","payload":{"type":"another_unknown"}}`,
		`{"type":"turn_context","payload":{"model":"gpt-5"}}`,
	}
	for _, c := range cases {
		events, err := MapRecord([]byte(c), state)
		if err != nil {
			t.Errorf("MapRecord(%s): err = %v", c, err)
		}
		if len(events) != 0 {
			t.Errorf("MapRecord(%s): got %d events, want 0", c, len(events))
		}
	}
}

// TestMakeSessionEnd produces a well-formed envelope and is a no-op when
// no session_id has been observed yet.
func TestMakeSessionEnd(t *testing.T) {
	ev := MakeSessionEnd(&SessionState{}, "idle", testTime())
	if ev.Type != "" || ev.Payload != nil {
		t.Errorf("expected zero event for empty state, got %+v", ev)
	}
	state := &SessionState{SessionID: "abc", AgentID: "codex-desktop-abc"}
	ev = MakeSessionEnd(state, "archived", testTime())
	if ev.Type != EventSessionEnd {
		t.Errorf("type = %q, want %q", ev.Type, EventSessionEnd)
	}
	if got := ev.Payload["reason"]; got != "archived" {
		t.Errorf("reason = %v, want archived", got)
	}
	if got := ev.Payload["session_id"]; got != "abc" {
		t.Errorf("session_id = %v, want abc", got)
	}
}

// TestShortID returns a stable short suffix without dashes; degrades to
// full input when the id has no hex chars before exhausting want.
func TestShortID(t *testing.T) {
	cases := map[string]string{
		"019e32a5-ca2d-72e2-bea2-ec568b73ecd3": "019e32a5",
		"abc":                                  "abc",
		"":                                     "",
		"--ab--cd--ef--gh--ij--":               "abcdefgh",
	}
	for in, want := range cases {
		if got := shortID(in); got != want {
			t.Errorf("shortID(%q) = %q, want %q", in, got, want)
		}
	}
}
