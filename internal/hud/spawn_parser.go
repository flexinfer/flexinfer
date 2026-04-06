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
}

// SpawnEventBroadcaster is called for each significant parsed event to enable
// real-time SSE broadcast to HUD/mobile clients.
type SpawnEventBroadcaster func(eventType string, agentID string, data any)
