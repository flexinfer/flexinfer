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
	logger     *slog.Logger
	sessionSet bool
	seenMsgIDs map[string]bool      // dedup token counts by message.id
	toolStarts map[string]time.Time // tool_use_id -> start timestamp
}

// NewClaudeJSONLParser creates a parser that writes structured events to sink.
// broadcast may be nil if real-time SSE is not needed.
func NewClaudeJSONLParser(sink SpawnEventSink, agentID string, broadcast SpawnEventBroadcaster, logger *slog.Logger) *ClaudeJSONLParser {
	if logger == nil {
		logger = slog.Default()
	}
	return &ClaudeJSONLParser{
		sink:       sink,
		broadcast:  broadcast,
		agentID:    agentID,
		logger:     logger.With("component", "claude-parser", "agent_id", agentID),
		seenMsgIDs: make(map[string]bool),
		toolStarts: make(map[string]time.Time),
	}
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
}

func (p *ClaudeJSONLParser) handleAssistant(line []byte) {
	var ev claudeAssistantEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		p.logger.Debug("failed to parse assistant event", "error", err)
		return
	}

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
	}

	// Process content blocks.
	for _, block := range ev.Message.Content {
		switch block.Type {
		case "tool_use":
			p.toolStarts[block.ID] = time.Now()
			p.sink.StartToolCall(block.ID, block.Name, "")
			p.inferFileChange(block.Name, block.Input)
			if p.broadcast != nil {
				p.broadcast("agent.spawn.tool_start", p.agentID, map[string]string{
					"id":   block.ID,
					"name": block.Name,
				})
			}
		case "text":
			if block.Text != "" {
				p.sink.SetLastMessage(block.Text)
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
}

// inferFileChange detects file mutations from Write, Edit, and NotebookEdit tool calls.
func (p *ClaudeJSONLParser) inferFileChange(toolName string, rawInput json.RawMessage) {
	if len(rawInput) == 0 {
		return
	}

	var kind string
	switch toolName {
	case "Write":
		kind = "modify" // could be create, but we can't distinguish without FS access
	case "Edit":
		kind = "modify"
	case "NotebookEdit":
		kind = "modify"
	default:
		return
	}

	var input struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(rawInput, &input); err != nil || input.FilePath == "" {
		return
	}
	p.sink.AddFileChange(input.FilePath, kind)
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

		if p.broadcast != nil {
			p.broadcast("agent.spawn.tool_complete", p.agentID, map[string]any{
				"id":          tr.ToolUseID,
				"duration_ms": durationMs,
				"is_error":    tr.IsError,
			})
		}
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

	if p.broadcast != nil {
		p.broadcast("agent.spawn.result", p.agentID, map[string]any{
			"stop_reason": stopReason,
			"cost_usd":    ev.TotalCost,
			"turns":       ev.NumTurns,
			"duration_ms": ev.DurationMs,
		})
	}
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
	}
}
