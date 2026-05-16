package codexwatch

import (
	"encoding/json"
	"fmt"
	"time"
)

// Canonical loom event types. Mirror internal/daemon/events.go so the
// envelopes produced here are indistinguishable from those emitted by the
// existing hook normalizer (cmd/loom/cmd_agent_event_emit.go).
const (
	EventSessionStart      = "session.start"
	EventSessionEnd        = "session.end"
	EventToolCallStart     = "tool.call.start"
	EventToolCallEnd       = "tool.call.end"
	EventAgentStatusChange = "agent.status.change"
)

// AgentType is the agent_type string used for Codex Desktop sessions. The
// existing loom proxy already uses "codex" for CLI sessions (verified live
// via `loom agent keepalive --agent-type codex`); reusing it keeps fleet
// view consolidated rather than splitting by surface.
const AgentType = "codex"

// agentIDPrefix prefixes the per-thread agent_id so the fleet view can
// distinguish Codex Desktop threads from other Codex surfaces at a glance.
const agentIDPrefix = "codex-desktop-"

// Event is a canonical loom event envelope payload (the inner object the
// publisher wraps as {type, data}). Type is the canonical event type;
// Payload is the inner data sent verbatim.
type Event struct {
	Type    string
	Payload map[string]any
}

// rawRecord is the on-disk JSONL envelope. payload is decoded lazily so
// the mapper can branch on top-level type before parsing the inner
// payload shape.
type rawRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// sessionMetaPayload is the first record of every session file.
type sessionMetaPayload struct {
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	CWD           string `json:"cwd"`
	Originator    string `json:"originator"`
	CLIVersion    string `json:"cli_version"`
	Source        string `json:"source"`
	ThreadSource  string `json:"thread_source"`
	ModelProvider string `json:"model_provider"`
}

// responseItemPayload covers the response_item top-level type. The inner
// Type field discriminates the shape (message / reasoning / function_call /
// function_call_output / tool_search_call / tool_search_output).
type responseItemPayload struct {
	Type      string          `json:"type"`
	Name      string          `json:"name,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
}

// eventMsgPayload covers the event_msg top-level type. Only the inner
// type and (when present) turn_id are needed for the canonical events
// the mapper emits today.
type eventMsgPayload struct {
	Type   string `json:"type"`
	TurnID string `json:"turn_id,omitempty"`
}

// SessionState tracks per-file invariants the mapper needs across
// records: the parsed session_id (from session_meta), the cwd, and the
// agent_id derived from the session_id. A SessionState is owned by a
// tailer and passed to MapRecord on each line.
type SessionState struct {
	SessionID  string
	AgentID    string
	CWD        string
	Originator string
	// Started is set once session.start has been emitted; subsequent
	// session_meta records (impossible in current Codex versions, but
	// cheap to guard against) won't re-emit.
	Started bool
}

// MapRecord parses one JSONL line and returns the zero or more canonical
// loom events to emit. SessionState is mutated in place when the record
// establishes session identity (session_meta).
//
// Unknown top-level types and unknown payload subtypes return (nil, nil)
// — the mapper is forward-compatible by design. A non-nil error is
// returned only for hard parse failures (malformed JSON, missing
// session_id when expected).
func MapRecord(line []byte, state *SessionState) ([]Event, error) {
	if len(line) == 0 {
		return nil, nil
	}
	var rec rawRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return nil, fmt.Errorf("decode envelope: %w", err)
	}
	switch rec.Type {
	case "session_meta":
		return mapSessionMeta(rec, state)
	case "response_item":
		return mapResponseItem(rec, state)
	case "event_msg":
		return mapEventMsg(rec, state)
	case "turn_context":
		// Carries useful per-turn metadata (model, sandbox, approval
		// policy) but not yet mapped to a canonical event. Logging is
		// the caller's responsibility.
		return nil, nil
	default:
		// Forward-compatible: unknown top-level types are skipped.
		return nil, nil
	}
}

// MakeSessionEnd produces a synthetic session.end event for a session
// the tailer can no longer observe (file archived, idle timeout, or
// watcher shutdown). reason is included verbatim for fleet diagnostics.
func MakeSessionEnd(state *SessionState, reason string, at time.Time) Event {
	if state == nil || state.SessionID == "" {
		return Event{}
	}
	return Event{
		Type: EventSessionEnd,
		Payload: map[string]any{
			"session_id": state.SessionID,
			"agent_id":   state.AgentID,
			"agent_type": AgentType,
			"ended_at":   at.UTC().Format(time.RFC3339Nano),
			"reason":     reason,
		},
	}
}

func mapSessionMeta(rec rawRecord, state *SessionState) ([]Event, error) {
	var meta sessionMetaPayload
	if err := json.Unmarshal(rec.Payload, &meta); err != nil {
		return nil, fmt.Errorf("decode session_meta payload: %w", err)
	}
	if meta.ID == "" {
		return nil, fmt.Errorf("session_meta missing id")
	}
	if state.Started {
		// Defensive: don't double-emit session.start if the same file
		// somehow yields a second session_meta record.
		return nil, nil
	}
	state.SessionID = meta.ID
	state.AgentID = agentIDPrefix + shortID(meta.ID)
	state.CWD = meta.CWD
	state.Originator = meta.Originator
	state.Started = true

	startedAt := meta.Timestamp
	if startedAt == "" {
		startedAt = rec.Timestamp
	}
	payload := map[string]any{
		"session_id": meta.ID,
		"agent_id":   state.AgentID,
		"agent_type": AgentType,
		"started_at": startedAt,
		"cwd":        meta.CWD,
		"source":     meta.Source,
		"originator": meta.Originator,
	}
	if meta.CLIVersion != "" {
		payload["cli_version"] = meta.CLIVersion
	}
	if meta.ModelProvider != "" {
		payload["model_provider"] = meta.ModelProvider
	}
	return []Event{{Type: EventSessionStart, Payload: payload}}, nil
}

func mapResponseItem(rec rawRecord, state *SessionState) ([]Event, error) {
	if state.SessionID == "" {
		// Records before session_meta are not addressable by the fleet
		// view; skip rather than emitting events with empty session_id.
		return nil, nil
	}
	var item responseItemPayload
	if err := json.Unmarshal(rec.Payload, &item); err != nil {
		return nil, fmt.Errorf("decode response_item payload: %w", err)
	}
	switch item.Type {
	case "function_call":
		if item.Name == "" || item.CallID == "" {
			return nil, nil
		}
		payload := map[string]any{
			"session_id": state.SessionID,
			"agent_id":   state.AgentID,
			"agent_type": AgentType,
			"tool_name":  item.Name,
			"call_id":    item.CallID,
			"started_at": rec.Timestamp,
		}
		// tool_input arguments are emitted only as a length hint, not
		// the full string. Argument bodies routinely include user file
		// paths, shell commands, and prompt text — the existing
		// redaction tier in pkg/telemetry/redact targets envelopes
		// going to public consumers; the watcher conservatively elides
		// content here and leaves enrichment to a future slice where
		// we can plumb redaction through cleanly.
		if len(item.Arguments) > 0 {
			payload["tool_input_bytes"] = len(item.Arguments)
		}
		return []Event{{Type: EventToolCallStart, Payload: payload}}, nil
	case "function_call_output":
		if item.CallID == "" {
			return nil, nil
		}
		payload := map[string]any{
			"session_id": state.SessionID,
			"agent_id":   state.AgentID,
			"agent_type": AgentType,
			"call_id":    item.CallID,
			"ended_at":   rec.Timestamp,
		}
		if len(item.Output) > 0 {
			payload["output_bytes"] = len(item.Output)
		}
		return []Event{{Type: EventToolCallEnd, Payload: payload}}, nil
	default:
		// reasoning, message, tool_search_*, etc. — skip in this slice.
		return nil, nil
	}
}

func mapEventMsg(rec rawRecord, state *SessionState) ([]Event, error) {
	if state.SessionID == "" {
		return nil, nil
	}
	var msg eventMsgPayload
	if err := json.Unmarshal(rec.Payload, &msg); err != nil {
		return nil, fmt.Errorf("decode event_msg payload: %w", err)
	}
	switch msg.Type {
	case "task_started":
		payload := map[string]any{
			"session_id": state.SessionID,
			"agent_id":   state.AgentID,
			"agent_type": AgentType,
			"status":     "working",
			"at":         rec.Timestamp,
		}
		if msg.TurnID != "" {
			payload["turn_id"] = msg.TurnID
		}
		return []Event{{Type: EventAgentStatusChange, Payload: payload}}, nil
	default:
		// agent_message, user_message, token_count: high-frequency or
		// low-signal as fleet events. Skip in this slice.
		return nil, nil
	}
}

// shortID returns the leading 8 hex chars of a session uuid, used as a
// human-readable suffix in agent_id. Codex session ids are RFC 9562
// uuid-style (e.g. 019e32a5-ca2d-72e2-bea2-ec568b73ecd3); we slice
// rather than parse so unexpected formats degrade gracefully.
func shortID(sessionID string) string {
	const want = 8
	out := make([]byte, 0, want)
	for i := 0; i < len(sessionID) && len(out) < want; i++ {
		c := sessionID[i]
		if c == '-' {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return sessionID
	}
	return string(out)
}
