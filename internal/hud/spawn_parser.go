package hud

// SpawnLineParser processes a single JSONL line from agent stdout.
// Implementations exist for Claude Code and Codex.
type SpawnLineParser interface {
	HandleLine(line []byte)
}

// SpawnEventSink receives structured telemetry events from JSONL parsers.
// The concrete implementation (bridge.SpawnTelemetryAccumulator) is created
// in a parallel slice; this interface decouples the parsers from it.
type SpawnEventSink interface {
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
	AddFileChange(path, kind string)
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
type SpawnEventBroadcaster func(eventType string, agentID string, data any)
