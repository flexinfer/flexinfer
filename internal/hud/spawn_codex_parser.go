package hud

import (
	"encoding/json"
	"log/slog"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// CodexJSONLParser parses Codex's --json JSONL output.
// Each line is a JSON object with a "type" field: thread.started, turn.started,
// turn.completed, item.started, item.completed, turn.failed, error.
type CodexJSONLParser struct {
	sink      SpawnEventSink
	broadcast SpawnEventBroadcaster
	agentID   string
	logger    *slog.Logger
}

// NewCodexJSONLParser creates a parser that writes structured events to sink.
// broadcast may be nil if real-time SSE is not needed.
func NewCodexJSONLParser(sink SpawnEventSink, agentID string, broadcast SpawnEventBroadcaster, logger *slog.Logger) *CodexJSONLParser {
	if logger == nil {
		logger = slog.Default()
	}
	return &CodexJSONLParser{
		sink:      sink,
		broadcast: broadcast,
		agentID:   agentID,
		logger:    logger.With("component", "codex-parser", "agent_id", agentID),
	}
}

// HandleLine processes a single JSONL line from Codex stdout.
func (p *CodexJSONLParser) HandleLine(line []byte) {
	if len(line) == 0 {
		return
	}

	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		p.logger.Debug("skipping non-JSON line", "error", err)
		return
	}

	switch envelope.Type {
	case "thread.started":
		p.handleThreadStarted(line)
	case "turn.started":
		p.handleTurnStarted()
	case "turn.completed":
		p.handleTurnCompleted(line)
	case "item.started":
		p.handleItemStarted(line)
	case "item.completed":
		p.handleItemCompleted(line)
	case "turn.failed":
		p.handleTurnFailed()
	case "error":
		p.handleError(line)
	default:
		p.logger.Debug("skipping unknown event type", "type", envelope.Type)
	}
}

// ---------- thread.started ----------

func (p *CodexJSONLParser) handleThreadStarted(line []byte) {
	var ev struct {
		ThreadID string `json:"thread_id"`
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		p.logger.Debug("failed to parse thread.started", "error", err)
		return
	}
	if ev.ThreadID != "" {
		p.sink.SetExternalSessionID(ev.ThreadID)
	}
}

// ---------- turn.started ----------

func (p *CodexJSONLParser) handleTurnStarted() {
	p.sink.IncrementTurns()
}

// ---------- turn.completed ----------

func (p *CodexJSONLParser) handleTurnCompleted(line []byte) {
	var ev struct {
		Usage struct {
			InputTokens       int `json:"input_tokens"`
			CachedInputTokens int `json:"cached_input_tokens"`
			OutputTokens      int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		p.logger.Debug("failed to parse turn.completed", "error", err)
		return
	}

	// Codex's `usage.input_tokens` follows the OpenAI Responses API convention:
	// it is the TOTAL input tokens including the cached portion, and
	// `cached_input_tokens` is a subset already included in that total (see
	// https://platform.openai.com/docs/guides/prompt-caching).
	//
	// Loom's canonical SpawnTokenUsage treats InputTokens and CacheReadTokens
	// as additive (total = input + output + cacheCreate + cacheRead), so we
	// must subtract the cached portion before forwarding to the sink.
	// Otherwise the HUD double-counts cached tokens and over-reports billable
	// input usage for every Codex turn.
	freshInputTokens := ev.Usage.InputTokens - ev.Usage.CachedInputTokens
	if freshInputTokens < 0 {
		// Defensive: if Codex ever emits cached > input (shouldn't happen per
		// the OpenAI contract), prefer under-reporting fresh input to negative
		// counts. Log for observability.
		p.logger.Warn("codex usage: cached_input_tokens > input_tokens",
			"input", ev.Usage.InputTokens,
			"cached", ev.Usage.CachedInputTokens)
		freshInputTokens = 0
	}

	p.sink.AddTokens(
		freshInputTokens,
		ev.Usage.OutputTokens,
		0,
		ev.Usage.CachedInputTokens,
	)

	// Codex's SDK does not emit per-turn cost (unlike Claude's `result`
	// event with `total_cost_usd`), so SpawnTelemetry.TotalCostUSD has been
	// 0 for every Codex spawn. Estimate the cost in-process using a
	// hard-coded price snapshot in the bridge package.
	//
	// TODO(slice 15b): Codex's `turn.completed` event does not include a
	// model field, so we use bridge.DefaultCodexModel here. A future slice
	// should plumb the actual model from the spawn config or
	// `thread.started` event so multi-model accounts get accurate per-model
	// rates. The accumulator marks the cost as estimated so the UI can
	// label it appropriately.
	estimatedCost := bridge.EstimateCodexCost(
		bridge.DefaultCodexModel,
		freshInputTokens,
		ev.Usage.CachedInputTokens,
		ev.Usage.OutputTokens,
	)
	if estimatedCost > 0 {
		p.sink.AddEstimatedCost(estimatedCost)
	}
}

// ---------- item.started ----------

type codexItemStartedEvent struct {
	Item codexItem `json:"item"`
}

type codexItem struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Command  string `json:"command,omitempty"`
	Tool     string `json:"tool,omitempty"`
	Server   string `json:"server,omitempty"`
	Status   string `json:"status,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Text     string `json:"text,omitempty"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Changes  []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes,omitempty"`
}

func (p *CodexJSONLParser) handleItemStarted(line []byte) {
	var ev codexItemStartedEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		p.logger.Debug("failed to parse item.started", "error", err)
		return
	}

	item := ev.Item
	switch item.Type {
	case "command_execution":
		p.sink.StartToolCall(item.ID, "Bash", "")
	case "mcp_tool_call":
		name := item.Tool
		if name == "" {
			name = "unknown"
		}
		p.sink.StartToolCall(item.ID, name, item.Server)
	}
}

// ---------- item.completed ----------

func (p *CodexJSONLParser) handleItemCompleted(line []byte) {
	var ev struct {
		Item codexItem `json:"item"`
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		p.logger.Debug("failed to parse item.completed", "error", err)
		return
	}

	item := ev.Item
	switch item.Type {
	case "command_execution":
		p.handleCommandExecution(item)
	case "file_change":
		p.handleFileChange(item)
	case "agent_message":
		p.handleAgentMessage(item)
	case "mcp_tool_call":
		p.handleMCPToolCall(item)
	case "reasoning":
		if p.broadcast != nil {
			p.broadcast("agent.spawn.reasoning", p.agentID, map[string]string{
				"text": item.Text,
			})
		}
	case "error":
		msg := item.Message
		if msg == "" {
			msg = item.Error
		}
		p.sink.AddError("execution", msg)
	case "todo_list":
		if p.broadcast != nil {
			p.broadcast("agent.spawn.todo", p.agentID, map[string]string{
				"text": item.Text,
			})
		}
	default:
		p.logger.Debug("skipping unknown item type", "item_type", item.Type)
	}
}

func (p *CodexJSONLParser) handleCommandExecution(item codexItem) {
	errMsg := ""
	if item.ExitCode != nil && *item.ExitCode != 0 {
		errMsg = item.Stderr
		if errMsg == "" {
			errMsg = item.Error
		}
		p.sink.AddError("tool_failure", errMsg)
	}
	p.sink.CompleteToolCall(item.ID, 0, item.ExitCode, errMsg)

	if p.broadcast != nil {
		p.broadcast("agent.spawn.tool_complete", p.agentID, map[string]any{
			"id":        item.ID,
			"command":   item.Command,
			"exit_code": item.ExitCode,
		})
	}
}

func (p *CodexJSONLParser) handleFileChange(item codexItem) {
	for _, ch := range item.Changes {
		p.sink.AddFileChange(ch.Path, ch.Kind)
	}
	if p.broadcast != nil {
		p.broadcast("agent.spawn.file_change", p.agentID, map[string]any{
			"changes": item.Changes,
		})
	}
}

func (p *CodexJSONLParser) handleAgentMessage(item codexItem) {
	text := item.Text
	if text == "" {
		text = item.Message
	}
	if text != "" {
		p.sink.SetLastMessage(text)
	}
	if p.broadcast != nil {
		p.broadcast("agent.spawn.message", p.agentID, map[string]string{
			"text": text,
		})
	}
}

func (p *CodexJSONLParser) handleMCPToolCall(item codexItem) {
	errMsg := ""
	if item.Error != "" {
		errMsg = item.Error
		p.sink.AddError("tool_failure", errMsg)
	}
	p.sink.CompleteToolCall(item.ID, 0, nil, errMsg)

	if p.broadcast != nil {
		p.broadcast("agent.spawn.tool_complete", p.agentID, map[string]any{
			"id":    item.ID,
			"tool":  item.Tool,
			"error": errMsg,
		})
	}
}

// ---------- turn.failed ----------

func (p *CodexJSONLParser) handleTurnFailed() {
	p.sink.AddError("execution", "turn failed")
}

// ---------- error ----------

func (p *CodexJSONLParser) handleError(line []byte) {
	var ev struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		p.logger.Debug("failed to parse error event", "error", err)
		return
	}
	p.sink.AddError("fatal", ev.Message)
}
