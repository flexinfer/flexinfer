package bridge

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/telemetry/redact"
)

// EventTypeToolCallStart is the canonical event type string for per-tool-call
// start events emitted by the SpawnTelemetryAccumulator. Matches
// internal/daemon.EventToolCallStart.
const EventTypeToolCallStart = "tool.call.start"

// EventTypeToolCallEnd is the canonical event type string for per-tool-call
// end events emitted by the SpawnTelemetryAccumulator. Matches
// internal/daemon.EventToolCallEnd.
const EventTypeToolCallEnd = "tool.call.end"

// TelemetryPublisher is anything that can broadcast a structured event with
// a string event type and arbitrary payload. The accumulator uses it to emit
// per-tool-call events (tool.call.start, tool.call.end) in real time. The
// interface is intentionally minimal so callers can adapt either an
// agentcontext.Publisher, a daemon.EventBus, or an SSE hub. nil is
// permitted — a nil publisher disables emission silently.
type TelemetryPublisher interface {
	Publish(eventType string, payload any)
}

// ToolCallStartEvent is the payload broadcast on tool.call.start. The args
// have already been filtered through pkg/telemetry/redact at the requested
// tier. Consumers should never see raw secrets.
type ToolCallStartEvent struct {
	CallID       string         `json:"call_id"`
	SessionID    string         `json:"session_id"`
	AgentID      string         `json:"agent_id"`
	ToolName     string         `json:"tool_name"`
	ServerName   string         `json:"server_name,omitempty"`
	ArgsRedacted map[string]any `json:"args_redacted"`
	ArgsTier     string         `json:"args_tier"`
	StartedAt    time.Time      `json:"started_at"`
}

// ToolCallEndEvent is the payload broadcast on tool.call.end. CallID
// correlates back to the matching ToolCallStartEvent. ResultSummary is a
// secret-safe one-line preview produced by pkg/telemetry/redact.Summary; raw
// result content never crosses this boundary.
type ToolCallEndEvent struct {
	CallID        string    `json:"call_id"`
	SessionID     string    `json:"session_id"`
	AgentID       string    `json:"agent_id"`
	ToolName      string    `json:"tool_name"`
	DurationMs    int64     `json:"duration_ms"`
	ExitCode      int       `json:"exit_code"`
	ResultSize    int       `json:"result_size_bytes"`
	ResultSummary string    `json:"result_summary,omitempty"`
	Error         string    `json:"error,omitempty"`
	EndedAt       time.Time `json:"ended_at"`
}

// SpawnTelemetry holds SDK-sourced structured telemetry for a headless agent spawn.
type SpawnTelemetry struct {
	ExternalSessionID string              `json:"external_session_id,omitempty"` // claude session_id or codex thread_id
	TurnCount         int                 `json:"turn_count"`
	TotalCostUSD      float64             `json:"total_cost_usd"`
	CostEstimated     bool                `json:"cost_estimated,omitempty"` // true when TotalCostUSD is a Loom-side estimate (e.g., Codex)
	TokenUsage        SpawnTokenUsage     `json:"token_usage"`
	ModelUsage        map[string]ModelUse `json:"model_usage,omitempty"`
	ToolCalls         []ToolCallEntry     `json:"tool_calls,omitempty"`
	FileChanges       []FileChangeEntry   `json:"file_changes,omitempty"`
	Errors            []AgentError        `json:"errors,omitempty"`
	StopReason        string              `json:"stop_reason,omitempty"`
	LastMessage       string              `json:"last_message,omitempty"`
	// Messages is the per-turn conversation transcript. Accumulated by parsers
	// from agent JSONL stdout: assistant text, thinking blocks (Claude),
	// reasoning items and todo lists (Codex). Capped at maxMessages to bound
	// memory; older entries are kept (FIFO drop is a future option).
	Messages []Message `json:"messages,omitempty"`
}

// Message is a single transcript entry from a spawned agent. Multiple Kind
// values exist because different agents emit different content types:
//   - "text"      — assistant prose (Claude/Codex/Gemini final output)
//   - "thinking"  — Claude extended-thinking block
//   - "reasoning" — Codex internal reasoning item
//   - "todo"      — Codex todo_list item
//   - "result"    — terminal result message (Claude `result` event)
type Message struct {
	Role string `json:"role"` // "assistant" by default; reserved for future "user"
	Kind string `json:"kind"` // see Message doc comment
	Text string `json:"text"`
	Time string `json:"time"` // RFC3339 UTC
}

// SpawnTokenUsage aggregates token counts across all turns.
type SpawnTokenUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
}

// ModelUse tracks per-model cost and token usage.
type ModelUse struct {
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
}

// ToolCallEntry records a single tool invocation.
type ToolCallEntry struct {
	Name       string `json:"name"`
	ServerName string `json:"server_name,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	Error      string `json:"error,omitempty"`
	Timestamp  string `json:"timestamp"`
}

// FileChangeEntry records a file modification by the agent.
type FileChangeEntry struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"` // create, modify, delete
	LinesAdded   int    `json:"lines_added,omitempty"`
	LinesRemoved int    `json:"lines_removed,omitempty"`
}

// AgentError records an error encountered during agent execution.
type AgentError struct {
	Type    string `json:"type"` // max_turns, max_budget, rate_limit, execution, tool_failure
	Message string `json:"message"`
	Time    string `json:"time"`
}

const (
	maxToolCalls   = 500
	maxFileChanges = 200
	maxMessages    = 500
)

// SpawnTelemetryAccumulator is a thread-safe accumulator for building SpawnTelemetry
// from streaming JSONL events. Parsers call its methods as events arrive;
// Snapshot() returns the current state for SSE broadcast or API response.
//
// When Publisher is non-nil, the accumulator additionally emits per-tool-call
// events (tool.call.start, tool.call.end) in real time so spectator UIs can
// render tool activity without waiting for the spawn-lifecycle batch flush.
// SessionID and AgentID are stamped on those events as identity context.
type SpawnTelemetryAccumulator struct {
	mu        sync.Mutex
	data      SpawnTelemetry
	toolStart map[string]time.Time // tool_use_id -> start time
	// toolMeta records the metadata captured at start time so the matching
	// end event can echo tool name + session identity without the caller
	// having to thread it back through.
	toolMeta map[string]toolCallMeta

	// publisher is the optional sink for per-tool-call events. nil disables
	// emission. Set via constructor or SetPublisher.
	publisher TelemetryPublisher
	// sessionID and agentID are stamped on emitted ToolCallStart/End events.
	// Empty strings are tolerated; callers that care about identity should
	// supply non-empty values via NewSpawnTelemetryAccumulatorWithPublisher.
	sessionID string
	agentID   string
}

// toolCallMeta is the per-call state needed to assemble a ToolCallEndEvent
// without requiring the caller to re-supply tool name or identity.
type toolCallMeta struct {
	toolName  string
	startedAt time.Time
}

// NewSpawnTelemetryAccumulator creates a new accumulator ready for use. The
// returned accumulator has a nil Publisher; per-tool-call events will not be
// emitted. Use NewSpawnTelemetryAccumulatorWithPublisher (or SetPublisher) to
// wire in a real event sink.
func NewSpawnTelemetryAccumulator() *SpawnTelemetryAccumulator {
	return &SpawnTelemetryAccumulator{
		toolStart: make(map[string]time.Time),
		toolMeta:  make(map[string]toolCallMeta),
		data: SpawnTelemetry{
			ModelUsage: make(map[string]ModelUse),
		},
	}
}

// NewSpawnTelemetryAccumulatorWithPublisher creates an accumulator wired to
// publish per-tool-call events. publisher may be nil (equivalent to the bare
// constructor). sessionID and agentID are stamped on every emitted event.
func NewSpawnTelemetryAccumulatorWithPublisher(publisher TelemetryPublisher, sessionID, agentID string) *SpawnTelemetryAccumulator {
	a := NewSpawnTelemetryAccumulator()
	a.publisher = publisher
	a.sessionID = sessionID
	a.agentID = agentID
	return a
}

// SetPublisher replaces the per-tool-call event publisher. Pass nil to
// disable emission. sessionID and agentID stamp future emissions; they are
// only updated when the corresponding argument is non-empty so callers can
// pass "" to leave them unchanged.
func (a *SpawnTelemetryAccumulator) SetPublisher(publisher TelemetryPublisher, sessionID, agentID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.publisher = publisher
	if sessionID != "" {
		a.sessionID = sessionID
	}
	if agentID != "" {
		a.agentID = agentID
	}
}

// AddTokens accumulates token counts for the spawn session.
func (a *SpawnTelemetryAccumulator) AddTokens(input, output, cacheCreate, cacheRead int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.data.TokenUsage.InputTokens += input
	a.data.TokenUsage.OutputTokens += output
	a.data.TokenUsage.CacheCreationTokens += cacheCreate
	a.data.TokenUsage.CacheReadTokens += cacheRead
}

// StartToolCall records the start of a tool call for duration tracking.
func (a *SpawnTelemetryAccumulator) StartToolCall(id, name, serverName string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.toolStart[id] = time.Now()

	if len(a.data.ToolCalls) >= maxToolCalls {
		return
	}
	a.data.ToolCalls = append(a.data.ToolCalls, ToolCallEntry{
		Name:       name,
		ServerName: serverName,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	})
}

// EnsureToolCall makes sure a tool call entry exists for the given id with
// the supplied metadata. If a matching open entry exists (started but not
// completed), populate any missing ServerName/Name. If no entry exists yet
// (because item.started was never emitted), create one so a subsequent
// CompleteToolCall has something to update.
//
// This exists to make MCP server_name capture symmetric across agents: the
// Codex SDK only emits item.started for some mcp_tool_call items, so the
// parser must defensively backfill the entry on completion. Idempotent: safe
// to call multiple times for the same id.
func (a *SpawnTelemetryAccumulator) EnsureToolCall(id, name, serverName string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Walk backwards looking for an open (incomplete) entry that matches by
	// name. If found, fill in serverName when missing.
	for i := len(a.data.ToolCalls) - 1; i >= 0; i-- {
		tc := &a.data.ToolCalls[i]
		if tc.DurationMs != 0 || tc.Error != "" || tc.ExitCode != nil {
			continue // already completed
		}
		if name != "" && tc.Name == name {
			if serverName != "" && tc.ServerName == "" {
				tc.ServerName = serverName
			}
			return
		}
	}

	// No matching open entry — create one. Mirrors StartToolCall semantics.
	if len(a.data.ToolCalls) >= maxToolCalls {
		return
	}
	a.data.ToolCalls = append(a.data.ToolCalls, ToolCallEntry{
		Name:       name,
		ServerName: serverName,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	})
	// Record a synthetic start so the upcoming CompleteToolCall can compute a
	// (near-zero) duration without leaking the toolStart map.
	a.toolStart[id] = time.Now()
}

// CompleteToolCall updates a previously started tool call with its result.
// If the tool call was started, the duration is computed from the start time.
func (a *SpawnTelemetryAccumulator) CompleteToolCall(id string, durationMs int, exitCode *int, errMsg string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Compute duration from tracked start time if available
	if start, ok := a.toolStart[id]; ok {
		durationMs = int(time.Since(start).Milliseconds())
		delete(a.toolStart, id)
	}
	delete(a.toolMeta, id)

	// Find and update the matching tool call entry (walk backwards for recent matches)
	for i := len(a.data.ToolCalls) - 1; i >= 0; i-- {
		tc := &a.data.ToolCalls[i]
		if tc.DurationMs == 0 && tc.Error == "" && tc.ExitCode == nil {
			tc.DurationMs = durationMs
			tc.ExitCode = exitCode
			tc.Error = errMsg
			return
		}
	}
}

// StartToolCallWithArgs records the start of a tool call AND emits a
// tool.call.start event to the configured Publisher (if any). It generates
// and returns a fresh CallID; callers pass that ID to
// CompleteToolCallWithResult to close the loop.
//
// args are filtered through pkg/telemetry/redact at TierPublic before
// emission so secrets never leave the process. Pass nil args for tools that
// take none.
//
// Internally, this is built on top of StartToolCall so the existing
// SpawnTelemetry.ToolCalls accumulation remains correct; both the legacy
// batch event (agent.spawn.telemetry.delta) and the new per-call event fire.
func (a *SpawnTelemetryAccumulator) StartToolCallWithArgs(name string, args map[string]any) string {
	return a.StartToolCallWithArgsServer(name, "", args)
}

// StartToolCallWithArgsServer is the MCP-aware variant of
// StartToolCallWithArgs. serverName populates the tool.call.start event's
// ServerName field for MCP-routed tools; pass "" for builtin tools.
func (a *SpawnTelemetryAccumulator) StartToolCallWithArgsServer(name, serverName string, args map[string]any) string {
	callID := generateCallID()
	now := time.Now()

	// Take the lock once for state mutation + payload assembly so the
	// accumulator's view of identity/publisher is consistent with the entry
	// we just appended.
	a.mu.Lock()
	a.toolStart[callID] = now
	a.toolMeta[callID] = toolCallMeta{toolName: name, startedAt: now}
	if len(a.data.ToolCalls) < maxToolCalls {
		a.data.ToolCalls = append(a.data.ToolCalls, ToolCallEntry{
			Name:       name,
			ServerName: serverName,
			Timestamp:  now.UTC().Format(time.RFC3339),
		})
	}
	publisher := a.publisher
	sessionID := a.sessionID
	agentID := a.agentID
	a.mu.Unlock()

	if publisher == nil {
		return callID
	}

	publisher.Publish(EventTypeToolCallStart, ToolCallStartEvent{
		CallID:       callID,
		SessionID:    sessionID,
		AgentID:      agentID,
		ToolName:     name,
		ServerName:   serverName,
		ArgsRedacted: redact.Redact(name, args, redact.TierPublic),
		ArgsTier:     string(redact.TierPublic),
		StartedAt:    now,
	})
	return callID
}

// CompleteToolCallWithResult records the end of a tool call AND emits a
// tool.call.end event with redacted result metadata. callID must be the
// value returned by StartToolCallWithArgs/StartToolCallWithArgsServer.
//
// result is the raw tool return value; only its byte size and a redacted
// summary (via pkg/telemetry/redact.Summary at TierPublic) are emitted —
// the raw payload never crosses this boundary. exitCode is 0 for tools that
// don't expose one; errMsg is empty on success.
func (a *SpawnTelemetryAccumulator) CompleteToolCallWithResult(callID string, result any, exitCode int, errMsg string) {
	now := time.Now()

	a.mu.Lock()
	meta, hadMeta := a.toolMeta[callID]
	delete(a.toolMeta, callID)
	start, hadStart := a.toolStart[callID]
	if hadStart {
		delete(a.toolStart, callID)
	}
	durationMs := int64(0)
	if hadStart {
		durationMs = time.Since(start).Milliseconds()
	}

	// Update the matching open ToolCallEntry so legacy snapshots stay
	// consistent with the new per-call event stream.
	for i := len(a.data.ToolCalls) - 1; i >= 0; i-- {
		tc := &a.data.ToolCalls[i]
		if tc.DurationMs == 0 && tc.Error == "" && tc.ExitCode == nil {
			tc.DurationMs = int(durationMs)
			ec := exitCode
			tc.ExitCode = &ec
			tc.Error = errMsg
			break
		}
	}

	publisher := a.publisher
	sessionID := a.sessionID
	agentID := a.agentID
	toolName := ""
	if hadMeta {
		toolName = meta.toolName
	}
	a.mu.Unlock()

	if publisher == nil {
		return
	}

	publisher.Publish(EventTypeToolCallEnd, ToolCallEndEvent{
		CallID:        callID,
		SessionID:     sessionID,
		AgentID:       agentID,
		ToolName:      toolName,
		DurationMs:    durationMs,
		ExitCode:      exitCode,
		ResultSize:    resultByteSize(result),
		ResultSummary: redact.Summary(toolName, result, redact.TierPublic),
		Error:         errMsg,
		EndedAt:       now,
	})
}

// generateCallID returns a 16-char hex string suitable for correlating
// tool.call.start with tool.call.end. Falls back to a timestamp-derived ID
// if crypto/rand fails (effectively never on supported platforms).
func generateCallID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Extremely unlikely; degrade to time-based uniqueness rather than
		// failing the spawn pipeline.
		ts := time.Now().UnixNano()
		for i := 0; i < 8; i++ {
			buf[i] = byte(ts >> (i * 8))
		}
	}
	return hex.EncodeToString(buf[:])
}

// resultByteSize returns the JSON-encoded byte size of result. For string
// results it skips the round-trip and returns the raw length (faster + a
// more meaningful "payload size" for typical tool outputs). Returns 0 when
// result is nil or fails to marshal.
func resultByteSize(result any) int {
	if result == nil {
		return 0
	}
	if s, ok := result.(string); ok {
		return len(s)
	}
	b, err := json.Marshal(result)
	if err != nil {
		return 0
	}
	return len(b)
}

// AddFileChange records a file modification, capped at maxFileChanges.
func (a *SpawnTelemetryAccumulator) AddFileChange(path, kind string, linesAdded, linesRemoved int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.data.FileChanges) >= maxFileChanges {
		return
	}
	a.data.FileChanges = append(a.data.FileChanges, FileChangeEntry{
		Path:         path,
		Kind:         kind,
		LinesAdded:   linesAdded,
		LinesRemoved: linesRemoved,
	})
}

// AddError records an agent error.
func (a *SpawnTelemetryAccumulator) AddError(errType, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.data.Errors = append(a.data.Errors, AgentError{
		Type:    errType,
		Message: message,
		Time:    time.Now().UTC().Format(time.RFC3339),
	})
}

// SetExternalSessionID sets the external session identifier.
func (a *SpawnTelemetryAccumulator) SetExternalSessionID(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.data.ExternalSessionID = id
}

// SetResult sets final cost, turn count, and stop reason.
func (a *SpawnTelemetryAccumulator) SetResult(costUSD float64, turns int, stopReason string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.data.TotalCostUSD = costUSD
	a.data.TurnCount = turns
	a.data.StopReason = stopReason
}

// AddEstimatedCost adds an estimated USD amount to the running total cost
// and marks CostEstimated=true so consumers can label it as a Loom-side
// estimate rather than an SDK-reported figure. Used by the Codex parser
// because the OpenAI Codex SDK does not emit per-turn cost.
func (a *SpawnTelemetryAccumulator) AddEstimatedCost(usd float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.data.TotalCostUSD += usd
	a.data.CostEstimated = true
}

// SetModelUsage sets per-model cost and token breakdown (Claude-specific).
func (a *SpawnTelemetryAccumulator) SetModelUsage(modelUsage map[string]ModelUse) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if modelUsage != nil {
		a.data.ModelUsage = make(map[string]ModelUse, len(modelUsage))
		for k, v := range modelUsage {
			a.data.ModelUsage[k] = v
		}
	}
}

// SetLastMessage sets the final assistant message text.
func (a *SpawnTelemetryAccumulator) SetLastMessage(msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.data.LastMessage = msg
}

// AddMessage appends a transcript entry. role is typically "assistant"; kind
// is one of the documented Message.Kind values (text, thinking, reasoning,
// todo, result). Empty text entries are ignored. Capped at maxMessages —
// further entries are dropped silently to bound per-spawn memory; the cap is
// large enough for normal sessions (~500 turns).
func (a *SpawnTelemetryAccumulator) AddMessage(role, kind, text string) {
	if text == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.data.Messages) >= maxMessages {
		return
	}
	if role == "" {
		role = "assistant"
	}
	a.data.Messages = append(a.data.Messages, Message{
		Role: role,
		Kind: kind,
		Text: text,
		Time: time.Now().UTC().Format(time.RFC3339),
	})
}

// IncrementTurns adds one to the turn counter.
func (a *SpawnTelemetryAccumulator) IncrementTurns() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.data.TurnCount++
}

// Snapshot returns a deep copy of the current telemetry state.
func (a *SpawnTelemetryAccumulator) Snapshot() SpawnTelemetry {
	a.mu.Lock()
	defer a.mu.Unlock()

	snap := a.data

	// Deep copy slices
	if a.data.ToolCalls != nil {
		snap.ToolCalls = make([]ToolCallEntry, len(a.data.ToolCalls))
		for i, tc := range a.data.ToolCalls {
			snap.ToolCalls[i] = tc
			if tc.ExitCode != nil {
				code := *tc.ExitCode
				snap.ToolCalls[i].ExitCode = &code
			}
		}
	}
	if a.data.FileChanges != nil {
		snap.FileChanges = make([]FileChangeEntry, len(a.data.FileChanges))
		copy(snap.FileChanges, a.data.FileChanges)
	}
	if a.data.Errors != nil {
		snap.Errors = make([]AgentError, len(a.data.Errors))
		copy(snap.Errors, a.data.Errors)
	}
	if a.data.Messages != nil {
		snap.Messages = make([]Message, len(a.data.Messages))
		copy(snap.Messages, a.data.Messages)
	}

	// Deep copy map
	if a.data.ModelUsage != nil {
		snap.ModelUsage = make(map[string]ModelUse, len(a.data.ModelUsage))
		for k, v := range a.data.ModelUsage {
			snap.ModelUsage[k] = v
		}
	}

	return snap
}
