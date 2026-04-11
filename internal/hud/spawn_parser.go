package hud

import "github.com/crb2nu/loom/internal/hud/bridge"

// SpawnLineParser processes a single JSONL line from agent stdout.
// Implementations exist for Claude Code and Codex.
type SpawnLineParser interface {
	HandleLine(line []byte)
}

// SpawnEventSink receives structured telemetry events from JSONL parsers.
// The concrete implementation (bridge.SpawnTelemetryAccumulator) is created
// in a parallel slice; this interface decouples the parsers from it.
type SpawnEventSink interface {
	// TelemetryDeltaSnapshot returns a slim snapshot of the current
	// accumulator state for SSE broadcast via the
	// agent.spawn.telemetry.delta event. Parsers call it after any
	// state-changing operation so clients can render live cost / token /
	// tool counts without polling the full /telemetry endpoint.
	//
	// The returned value is a value copy and is safe to share across
	// goroutines. Implementations must be safe to call concurrently with
	// other sink methods.
	TelemetryDeltaSnapshot(spawnID, agentID string) bridge.SpawnTelemetryDelta
	// AddTokens accumulates token counts for the spawn session.
	AddTokens(input, output, cacheCreate, cacheRead int)
	// StartToolCall records the beginning of a tool invocation.
	StartToolCall(id, name, serverName string)
	// EnsureToolCall defensively guarantees that a tool call entry exists for
	// the given id with the supplied name/serverName. Used by the Codex parser
	// to backfill MCP server attribution when only item.completed is emitted
	// (i.e. the SDK skipped item.started for synchronous tool calls). Safe to
	// call alongside StartToolCall — implementations must be idempotent.
	EnsureToolCall(id, name, serverName string)
	// CompleteToolCall records the completion of a tool invocation.
	// exitCode is nil for non-command tools; errMsg is empty on success.
	CompleteToolCall(id string, durationMs int, exitCode *int, errMsg string)
	// AddFileChange records a file modification discovered via tool calls.
	// linesAdded/linesRemoved are best-effort estimates; pass 0 when unknown.
	AddFileChange(path, kind string, linesAdded, linesRemoved int)
	// AddError records an error encountered during the spawn.
	AddError(errType, message string)
	// SetExternalSessionID stores the agent platform's own session/thread ID.
	SetExternalSessionID(id string)
	// SetResult records the final outcome of the spawn.
	SetResult(costUSD float64, turns int, stopReason string)
	// SetLastMessage stores the most recent agent text output.
	SetLastMessage(msg string)
	// IncrementTurns increments the turn counter by one.
	IncrementTurns()
	// AddEstimatedCost adds a Loom-side estimated USD cost (used by Codex,
	// whose SDK does not emit per-turn cost). Marks the telemetry as
	// containing an estimated cost so the UI can label it.
	AddEstimatedCost(usd float64)
}

// SpawnEventBroadcaster is called for each significant parsed event to enable
// real-time SSE broadcast to HUD/mobile clients.
//
// Known eventType values:
//   - agent.spawn.tool_start       — tool invocation started (data: map with id, name, server_name)
//   - agent.spawn.tool_complete    — tool invocation finished (data: map with id, duration_ms, exit_code...)
//   - agent.spawn.message          — assistant text message (data: map with text)
//   - agent.spawn.thinking         — Claude thinking block (data: map with thinking)
//   - agent.spawn.file_change      — file mutation (data: map with changes)
//   - agent.spawn.reasoning        — Codex reasoning item (data: map with text)
//   - agent.spawn.todo             — Codex todo_list item (data: map with text)
//   - agent.spawn.rate_limit       — Claude API retry (data: map with attempt, status)
//   - agent.spawn.result           — Claude terminal result (data: map with stop_reason, cost_usd, turns...)
//   - agent.spawn.telemetry.delta  — slim telemetry snapshot for live UI updates
//     (data: bridge.SpawnTelemetryDelta); emitted by parsers after every
//     state-changing accumulator update so clients can render live cost /
//     token / tool counts without polling /api/agent/spawn/{id}/telemetry.
//     TODO: add a ~100-200ms debounce if SSE bandwidth ever becomes a concern.
type SpawnEventBroadcaster func(eventType string, agentID string, data any)

// SpawnTelemetryDeltaEvent is the canonical SSE event type name used by
// parsers when broadcasting a bridge.SpawnTelemetryDelta payload.
const SpawnTelemetryDeltaEvent = "agent.spawn.telemetry.delta"

// countLines returns the number of lines in s (counting newlines + 1 for
// non-empty strings). Returns 0 for empty strings.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := 1
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}
