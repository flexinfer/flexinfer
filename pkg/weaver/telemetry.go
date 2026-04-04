package weaver

import (
	"context"

	"github.com/crb2nu/loom/pkg/openairesponses"
)

// SubagentTelemetry implements openairesponses.TelemetrySink for per-subagent
// token tracking that feeds into weaver Metrics.
type SubagentTelemetry struct {
	domain  string
	metrics *Metrics
}

// NewSubagentTelemetry creates a telemetry sink scoped to a domain.
func NewSubagentTelemetry(domain string, metrics *Metrics) *SubagentTelemetry {
	return &SubagentTelemetry{domain: domain, metrics: metrics}
}

func (t *SubagentTelemetry) RecordTurnStart(_ context.Context, _ openairesponses.TurnRequest, _ openairesponses.ExecutionIdentity) {
}

func (t *SubagentTelemetry) RecordTurnEnd(_ context.Context, resp openairesponses.TurnResponse, err error, _ openairesponses.ExecutionIdentity) {
	if t.metrics == nil {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	t.metrics.SubagentCallTotal.WithLabelValues(t.domain, status).Inc()

	if resp.PromptTokens > 0 {
		t.metrics.TokensTotal.WithLabelValues(t.domain, "prompt").Add(float64(resp.PromptTokens))
	}
	if resp.CompletionTokens > 0 {
		t.metrics.TokensTotal.WithLabelValues(t.domain, "completion").Add(float64(resp.CompletionTokens))
	}
}

func (t *SubagentTelemetry) RecordToolCall(_ context.Context, call openairesponses.ToolCall, _ openairesponses.ToolResult, err error, _ openairesponses.ExecutionIdentity) {
	if t.metrics == nil {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
		t.metrics.ErrorsTotal.WithLabelValues(t.domain).Inc()
	}
	_ = status
	_ = call
}
