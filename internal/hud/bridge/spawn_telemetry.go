package bridge

import (
	"sync"
	"time"
)

// SpawnTelemetry holds SDK-sourced structured telemetry for a headless agent spawn.
type SpawnTelemetry struct {
	ExternalSessionID string              `json:"external_session_id,omitempty"` // claude session_id or codex thread_id
	TurnCount         int                 `json:"turn_count"`
	TotalCostUSD      float64             `json:"total_cost_usd"`
	TokenUsage        SpawnTokenUsage     `json:"token_usage"`
	ModelUsage        map[string]ModelUse `json:"model_usage,omitempty"`
	ToolCalls         []ToolCallEntry     `json:"tool_calls,omitempty"`
	FileChanges       []FileChangeEntry   `json:"file_changes,omitempty"`
	Errors            []AgentError        `json:"errors,omitempty"`
	StopReason        string              `json:"stop_reason,omitempty"`
	LastMessage       string              `json:"last_message,omitempty"`
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
	Path string `json:"path"`
	Kind string `json:"kind"` // create, modify, delete
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
)

// SpawnTelemetryAccumulator is a thread-safe accumulator for building SpawnTelemetry
// from streaming JSONL events. Parsers call its methods as events arrive;
// Snapshot() returns the current state for SSE broadcast or API response.
type SpawnTelemetryAccumulator struct {
	mu        sync.Mutex
	data      SpawnTelemetry
	toolStart map[string]time.Time // tool_use_id -> start time
}

// NewSpawnTelemetryAccumulator creates a new accumulator ready for use.
func NewSpawnTelemetryAccumulator() *SpawnTelemetryAccumulator {
	return &SpawnTelemetryAccumulator{
		toolStart: make(map[string]time.Time),
		data: SpawnTelemetry{
			ModelUsage: make(map[string]ModelUse),
		},
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

// AddFileChange records a file modification, capped at maxFileChanges.
func (a *SpawnTelemetryAccumulator) AddFileChange(path, kind string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.data.FileChanges) >= maxFileChanges {
		return
	}
	a.data.FileChanges = append(a.data.FileChanges, FileChangeEntry{
		Path: path,
		Kind: kind,
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

	// Deep copy map
	if a.data.ModelUsage != nil {
		snap.ModelUsage = make(map[string]ModelUse, len(a.data.ModelUsage))
		for k, v := range a.data.ModelUsage {
			snap.ModelUsage[k] = v
		}
	}

	return snap
}
