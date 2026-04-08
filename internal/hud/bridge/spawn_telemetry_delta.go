package bridge

import "time"

// SpawnTelemetryDelta is a slim snapshot of the current SpawnTelemetry
// accumulator state, broadcast over SSE after each parser-driven update.
// Clients (web HUD, iOS) use it to render live cost / token / tool counts
// without polling the full /api/agent/spawn/{id}/telemetry endpoint.
//
// It intentionally omits the large, unbounded fields on SpawnTelemetry
// (ToolCalls[], FileChanges[], Errors[], ModelUsage{}) and reports only
// their counts. Clients that need the full lists should fetch the
// /telemetry endpoint on demand (e.g. when opening the spawn detail
// drawer). This keeps every delta event small and bounded so bursty
// parser updates do not blow up SSE bandwidth.
type SpawnTelemetryDelta struct {
	// SpawnID identifies the spawn this delta belongs to. Clients key
	// their local telemetry state by this value.
	SpawnID string `json:"spawn_id"`
	// AgentID echoes the owning agent ID so event routing and the
	// payload body stay self-describing.
	AgentID string `json:"agent_id"`

	// TurnCount is the number of completed turns so far.
	TurnCount int `json:"turn_count"`
	// ToolCallCount is the number of tool call entries currently
	// tracked on the accumulator (capped at maxToolCalls).
	ToolCallCount int `json:"tool_call_count"`
	// FileChangeCount is the number of file changes currently tracked
	// (capped at maxFileChanges).
	FileChangeCount int `json:"file_change_count"`
	// ErrorCount is the number of errors currently tracked.
	ErrorCount int `json:"error_count"`

	// TotalCostUSD is the running cost total. Matches
	// SpawnTelemetry.TotalCostUSD.
	TotalCostUSD float64 `json:"total_cost_usd"`
	// CostEstimated is true when the running cost is a Loom-side
	// estimate (currently Codex only).
	CostEstimated bool `json:"cost_estimated,omitempty"`

	// TokenUsage mirrors the running SpawnTelemetry.TokenUsage.
	TokenUsage SpawnTokenUsage `json:"token_usage"`

	// LastMessage echoes the most recent agent text output. Clients
	// can use this for a one-line status ticker in the spawn row.
	LastMessage string `json:"last_message,omitempty"`
	// StopReason is populated once the spawn reaches a terminal state;
	// empty for in-progress deltas.
	StopReason string `json:"stop_reason,omitempty"`

	// UpdatedAt is the server-side timestamp at which this delta was
	// materialised. Clients can use it to discard out-of-order events.
	UpdatedAt time.Time `json:"updated_at"`
}

// TelemetryDeltaSnapshot returns a slim snapshot of the current
// accumulator state suitable for SSE broadcast via the
// agent.spawn.telemetry.delta event. The returned value is a value copy
// and safe to share across goroutines.
//
// spawnID and agentID are supplied by the caller because the accumulator
// itself does not track them; the orchestrator owns that identity and
// the parser threads it through.
//
// This method implements hud.SpawnEventSink.TelemetryDeltaSnapshot so the
// parsers can trigger SSE broadcasts without reaching through the bridge
// import boundary.
func (a *SpawnTelemetryAccumulator) TelemetryDeltaSnapshot(spawnID, agentID string) SpawnTelemetryDelta {
	a.mu.Lock()
	defer a.mu.Unlock()

	return SpawnTelemetryDelta{
		SpawnID:         spawnID,
		AgentID:         agentID,
		TurnCount:       a.data.TurnCount,
		ToolCallCount:   len(a.data.ToolCalls),
		FileChangeCount: len(a.data.FileChanges),
		ErrorCount:      len(a.data.Errors),
		TotalCostUSD:    a.data.TotalCostUSD,
		CostEstimated:   a.data.CostEstimated,
		TokenUsage:      a.data.TokenUsage,
		LastMessage:     a.data.LastMessage,
		StopReason:      a.data.StopReason,
		UpdatedAt:       time.Now(),
	}
}
