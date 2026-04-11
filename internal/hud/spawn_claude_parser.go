package hud

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// ClaudeJSONLParser parses Claude Code's --output-format stream-json JSONL.
// Each line is a JSON object with a "type" field: assistant, user, result, system.
type ClaudeJSONLParser struct {
	sink       SpawnEventSink
	broadcast  SpawnEventBroadcaster
	agentID    string
	spawnID    string
	logger     *slog.Logger
	sessionSet bool
	seenMsgIDs map[string]bool      // dedup token counts by message.id
	toolStarts map[string]time.Time // tool_use_id -> start timestamp
}

// NewClaudeJSONLParser creates a parser that writes structured events to sink.
// broadcast may be nil if real-time SSE is not needed. spawnID is used to
// stamp agent.spawn.telemetry.delta events with the owning spawn; it may be
// empty in unit tests that do not exercise delta broadcasts.
func NewClaudeJSONLParser(sink SpawnEventSink, agentID, spawnID string, broadcast SpawnEventBroadcaster, logger *slog.Logger) *ClaudeJSONLParser {
	if logger == nil {
		logger = slog.Default()
	}
	return &ClaudeJSONLParser{
		sink:       sink,
		broadcast:  broadcast,
		agentID:    agentID,
		spawnID:    spawnID,
		logger:     logger.With("component", "claude-parser", "agent_id", agentID),
		seenMsgIDs: make(map[string]bool),
		toolStarts: make(map[string]time.Time),
	}
}

// emitTelemetryDelta snapshots the current accumulator state and broadcasts
// an agent.spawn.telemetry.delta SSE event so web HUD and iOS clients can
// render live cost / token / tool counts without polling the full
// /api/agent/spawn/{id}/telemetry endpoint. No-op when the broadcaster is
// nil (unit tests or buffered-exec fallback).
func (p *ClaudeJSONLParser) emitTelemetryDelta() {
	if p.broadcast == nil {
		return
	}
	delta := p.sink.TelemetryDeltaSnapshot(p.spawnID, p.agentID)
	p.broadcast(SpawnTelemetryDeltaEvent, p.agentID, delta)
}

// HandleLine processes a single JSONL line from Claude stdout.
func (p *ClaudeJSONLParser) HandleLine(line []byte) {
	if len(line) == 0 {
		return
	}

	// Fast-path: parse only the type field first.
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		p.logger.Debug("skipping non-JSON line", "error", err)
		return
	}

	switch envelope.Type {
	case "assistant":
		p.handleAssistant(line)
	case "user":
		p.handleUser(line)
	case "result":
		p.handleResult(line)
	case "system":
		p.handleSystem(line)
	default:
		// Unknown types are silently skipped.
		p.logger.Debug("skipping unknown event type", "type", envelope.Type)
	}
}

// ---------- assistant events ----------

type claudeAssistantEvent struct {
	SessionID string `json:"session_id"`
	Message   struct {
		ID    string `json:"id"`
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
		Content []claudeContentBlock `json:"content"`
	} `json:"message"`
}

type claudeContentBlock struct {
	Type     string          `json:"type"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	// ServerName is populated only for "mcp_tool_use" blocks. The Claude SDK
	// surfaces MCP tool invocations as a distinct content block type with
	// the upstream server name attached, so we capture it here to preserve
	// provenance in SpawnTelemetry.ToolCalls[].ServerName.
	ServerName string `json:"server_name,omitempty"`
}

func (p *ClaudeJSONLParser) handleAssistant(line []byte) {
	var ev claudeAssistantEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		p.logger.Debug("failed to parse assistant event", "error", err)
		return
	}

	// Track whether this line produced any accumulator state change so we
	// only broadcast a telemetry delta when something actually changed.
	// Pure "thinking" blocks, for example, do not mutate the accumulator.
	changed := false

	// Set external session ID from the first event seen.
	if !p.sessionSet && ev.SessionID != "" {
		p.sink.SetExternalSessionID(ev.SessionID)
		p.sessionSet = true
	}

	// Deduplicate token counts by message ID. Parallel tool calls share
	// the same message, so we only count tokens once per message.
	msgID := ev.Message.ID
	if msgID != "" && !p.seenMsgIDs[msgID] {
		p.seenMsgIDs[msgID] = true
		u := ev.Message.Usage
		p.sink.AddTokens(u.InputTokens, u.OutputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens)
		changed = true
	}

	// Process content blocks.
	for _, block := range ev.Message.Content {
		switch block.Type {
		case "tool_use":
			p.toolStarts[block.ID] = time.Now()
			p.sink.StartToolCall(block.ID, block.Name, "")
			p.inferFileChange(block.Name, block.Input)
			changed = true
			if p.broadcast != nil {
				p.broadcast("agent.spawn.tool_start", p.agentID, map[string]string{
					"id":   block.ID,
					"name": block.Name,
				})
			}
		case "mcp_tool_use":
			// MCP tool calls carry the upstream server name explicitly. Forward
			// it through to the canonical telemetry so the HUD can group tool
			// calls by MCP server.
			p.toolStarts[block.ID] = time.Now()
			p.sink.StartToolCall(block.ID, block.Name, block.ServerName)
			changed = true
			if p.broadcast != nil {
				p.broadcast("agent.spawn.tool_start", p.agentID, map[string]string{
					"id":          block.ID,
					"name":        block.Name,
					"server_name": block.ServerName,
				})
			}
		case "text":
			if block.Text != "" {
				p.sink.SetLastMessage(block.Text)
				changed = true
				if p.broadcast != nil {
					p.broadcast("agent.spawn.message", p.agentID, map[string]string{
						"text": block.Text,
					})
				}
			}
		case "thinking":
			if p.broadcast != nil {
				p.broadcast("agent.spawn.thinking", p.agentID, map[string]string{
					"thinking": block.Thinking,
				})
			}
		}
	}

	if changed {
		p.emitTelemetryDelta()
	}
}

// inferFileChange detects file mutations from Write, Edit, and NotebookEdit tool calls.
func (p *ClaudeJSONLParser) inferFileChange(toolName string, rawInput json.RawMessage) {
	if len(rawInput) == 0 {
		return
	}

	switch toolName {
	case "Write", "Edit", "NotebookEdit":
		// ok
	default:
		return
	}

	var input struct {
		FilePath  string `json:"file_path"`
		Content   string `json:"content"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(rawInput, &input); err != nil || input.FilePath == "" {
		return
	}

	var linesAdded, linesRemoved int
	switch toolName {
	case "Write":
		linesAdded = countLines(input.Content)
	case "Edit":
		linesRemoved = countLines(input.OldString)
		linesAdded = countLines(input.NewString)
	}

	p.sink.AddFileChange(input.FilePath, "modify", linesAdded, linesRemoved)
}

// ---------- user events ----------

type claudeUserEvent struct {
	Content []claudeToolResult `json:"content"`
}

type claudeToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error"`
}

func (p *ClaudeJSONLParser) handleUser(line []byte) {
	var ev claudeUserEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		p.logger.Debug("failed to parse user event", "error", err)
		return
	}

	changed := false
	for _, tr := range ev.Content {
		if tr.Type != "tool_result" {
			continue
		}

		var durationMs int
		if start, ok := p.toolStarts[tr.ToolUseID]; ok {
			durationMs = int(time.Since(start).Milliseconds())
			delete(p.toolStarts, tr.ToolUseID)
		}

		errMsg := ""
		if tr.IsError {
			errMsg = tr.Content
			p.sink.AddError("tool_failure", tr.Content)
		}
		p.sink.CompleteToolCall(tr.ToolUseID, durationMs, nil, errMsg)
		changed = true

		if p.broadcast != nil {
			p.broadcast("agent.spawn.tool_complete", p.agentID, map[string]any{
				"id":          tr.ToolUseID,
				"duration_ms": durationMs,
				"is_error":    tr.IsError,
			})
		}
	}

	if changed {
		p.emitTelemetryDelta()
	}
}

// ---------- result events ----------

type claudeResultEvent struct {
	Subtype    string  `json:"subtype"`
	SessionID  string  `json:"session_id"`
	DurationMs int     `json:"duration_ms"`
	NumTurns   int     `json:"num_turns"`
	TotalCost  float64 `json:"total_cost_usd"`
	Result     string  `json:"result"`
	// PermissionDenials mirrors SDKResult.permission_denials from the Claude
	// Agent SDK. Each entry represents a tool invocation the agent requested
	// but was blocked from executing by the permission layer. We surface
	// these to the canonical SpawnTelemetry.Errors[] so operators can see
	// policy-denied tool calls without scraping stderr.
	PermissionDenials []claudePermissionDenial `json:"permission_denials,omitempty"`
}

// claudePermissionDenial mirrors SDKPermissionDenial from sdk.d.ts. We only
// pull the fields we need to build a human-readable error message; the full
// tool_input payload is intentionally dropped to keep telemetry compact.
type claudePermissionDenial struct {
	ToolName  string `json:"tool_name"`
	ToolUseID string `json:"tool_use_id"`
}

func (p *ClaudeJSONLParser) handleResult(line []byte) {
	var ev claudeResultEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		p.logger.Debug("failed to parse result event", "error", err)
		return
	}

	stopReason := mapClaudeSubtype(ev.Subtype)
	p.sink.SetResult(ev.TotalCost, ev.NumTurns, stopReason)

	if isClaudeErrorSubtype(ev.Subtype) {
		p.sink.AddError(ev.Subtype, ev.Result)
	} else if ev.Result != "" {
		p.sink.SetLastMessage(ev.Result)
	}

	// Surface permission-denied tool calls as structured errors on the
	// canonical telemetry so the HUD can badge them without scraping logs.
	// The Claude SDK only emits permission_denials on the terminal `result`
	// event, so this is the single source of truth for the whole session.
	for _, pd := range ev.PermissionDenials {
		tool := pd.ToolName
		if tool == "" {
			tool = "unknown"
		}
		msg := fmt.Sprintf("tool %q denied by permission policy", tool)
		if pd.ToolUseID != "" {
			msg = fmt.Sprintf("%s (tool_use_id=%s)", msg, pd.ToolUseID)
		}
		p.sink.AddError("permission_denied", msg)
	}

	if p.broadcast != nil {
		p.broadcast("agent.spawn.result", p.agentID, map[string]any{
			"stop_reason":            stopReason,
			"cost_usd":               ev.TotalCost,
			"turns":                  ev.NumTurns,
			"duration_ms":            ev.DurationMs,
			"permission_denials_len": len(ev.PermissionDenials),
		})
	}

	// Terminal result always mutates the accumulator (SetResult + often
	// SetLastMessage/AddError), so unconditionally emit one last delta so
	// clients land on the final cost / turn / stop reason before the
	// orchestrator emits agent.spawn.completed / .failed.
	p.emitTelemetryDelta()
}

// mapClaudeSubtype maps Claude result subtypes to normalized stop reasons.
func mapClaudeSubtype(subtype string) string {
	switch subtype {
	case "success":
		return "end_turn"
	case "error_max_turns":
		return "max_turns"
	case "error_max_budget_usd":
		return "max_budget"
	case "error_during_execution":
		return "execution_error"
	default:
		return subtype
	}
}

// isClaudeErrorSubtype returns true if the subtype indicates an error outcome.
func isClaudeErrorSubtype(subtype string) bool {
	switch subtype {
	case "error_max_turns", "error_max_budget_usd", "error_during_execution":
		return true
	default:
		return false
	}
}

// ---------- system events ----------

type claudeSystemEvent struct {
	Subtype     string `json:"subtype"`
	Attempt     int    `json:"attempt"`
	ErrorStatus int    `json:"error_status"`
}

func (p *ClaudeJSONLParser) handleSystem(line []byte) {
	var ev claudeSystemEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		p.logger.Debug("failed to parse system event", "error", err)
		return
	}

	if ev.Subtype == "api_retry" {
		msg := fmt.Sprintf("API retry attempt %d, status %d", ev.Attempt, ev.ErrorStatus)
		p.sink.AddError("rate_limit", msg)
		if p.broadcast != nil {
			p.broadcast("agent.spawn.rate_limit", p.agentID, map[string]any{
				"attempt": ev.Attempt,
				"status":  ev.ErrorStatus,
			})
		}
		p.emitTelemetryDelta()
	}
}
