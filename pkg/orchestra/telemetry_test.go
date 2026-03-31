package orchestra

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/crb2nu/loom/pkg/openairesponses"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestNewSubagentTelemetry(t *testing.T) {
	t.Parallel()

	tel := NewSubagentTelemetry("test-domain", nil)
	if tel == nil {
		t.Fatal("expected non-nil telemetry")
	}
	if tel.domain != "test-domain" {
		t.Errorf("expected domain 'test-domain', got %q", tel.domain)
	}
}

func TestSubagentTelemetry_RecordTurnStart_NoPanic(t *testing.T) {
	t.Parallel()

	tel := NewSubagentTelemetry("test", nil)

	// Should not panic even with nil metrics.
	tel.RecordTurnStart(
		context.Background(),
		openairesponses.TurnRequest{},
		openairesponses.ExecutionIdentity{},
	)
}

func TestSubagentTelemetry_RecordTurnEnd_NilMetrics(t *testing.T) {
	t.Parallel()

	tel := NewSubagentTelemetry("test", nil)

	// Should not panic with nil metrics.
	tel.RecordTurnEnd(
		context.Background(),
		openairesponses.TurnResponse{},
		nil,
		openairesponses.ExecutionIdentity{},
	)
}

func TestSubagentTelemetry_RecordTurnEnd_Success(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	tel := NewSubagentTelemetry("my-domain", m)

	tel.RecordTurnEnd(
		context.Background(),
		openairesponses.TurnResponse{},
		nil,
		openairesponses.ExecutionIdentity{},
	)

	// Verify the counter incremented for domain=my-domain, status=ok.
	val := counterValue(t, m.SubagentCallTotal, "my-domain", "ok")
	if val != 1 {
		t.Errorf("expected SubagentCallTotal=1, got %v", val)
	}
}

func TestSubagentTelemetry_RecordTurnEnd_Error(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	tel := NewSubagentTelemetry("my-domain", m)

	tel.RecordTurnEnd(
		context.Background(),
		openairesponses.TurnResponse{},
		errors.New("subagent failed"),
		openairesponses.ExecutionIdentity{},
	)

	val := counterValue(t, m.SubagentCallTotal, "my-domain", "error")
	if val != 1 {
		t.Errorf("expected SubagentCallTotal=1 for error, got %v", val)
	}
}

func TestSubagentTelemetry_RecordToolCall_NilMetrics(t *testing.T) {
	t.Parallel()

	tel := NewSubagentTelemetry("test", nil)

	// Should not panic with nil metrics.
	tel.RecordToolCall(
		context.Background(),
		openairesponses.ToolCall{},
		openairesponses.ToolResult{},
		nil,
		openairesponses.ExecutionIdentity{},
	)
}

func TestSubagentTelemetry_RecordToolCall_Success(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	tel := NewSubagentTelemetry("my-domain", m)

	tel.RecordToolCall(
		context.Background(),
		openairesponses.ToolCall{
			CallID:    "call-1",
			ToolName:  "git__git_status",
			Arguments: json.RawMessage(`{}`),
		},
		openairesponses.ToolResult{CallID: "call-1", Output: "ok"},
		nil,
		openairesponses.ExecutionIdentity{},
	)

	// On success, ErrorsTotal should not be incremented.
	val := counterValue(t, m.ErrorsTotal, "my-domain")
	if val != 0 {
		t.Errorf("expected ErrorsTotal=0 for success, got %v", val)
	}
}

func TestSubagentTelemetry_RecordToolCall_Error(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	tel := NewSubagentTelemetry("my-domain", m)

	tel.RecordToolCall(
		context.Background(),
		openairesponses.ToolCall{
			CallID:    "call-2",
			ToolName:  "failing_tool",
			Arguments: json.RawMessage(`{}`),
		},
		openairesponses.ToolResult{CallID: "call-2", IsError: true},
		errors.New("tool failed"),
		openairesponses.ExecutionIdentity{},
	)

	val := counterValue(t, m.ErrorsTotal, "my-domain")
	if val != 1 {
		t.Errorf("expected ErrorsTotal=1, got %v", val)
	}
}

func TestSubagentTelemetry_RecordToolCall_MultipleIncrements(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	tel := NewSubagentTelemetry("my-domain", m)

	// Record 3 error calls.
	for i := 0; i < 3; i++ {
		tel.RecordToolCall(
			context.Background(),
			openairesponses.ToolCall{},
			openairesponses.ToolResult{},
			errors.New("error"),
			openairesponses.ExecutionIdentity{},
		)
	}

	val := counterValue(t, m.ErrorsTotal, "my-domain")
	if val != 3 {
		t.Errorf("expected ErrorsTotal=3 after 3 errors, got %v", val)
	}
}

func TestSubagentTelemetry_RecordTurnEnd_TokenMetrics(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	tel := NewSubagentTelemetry("my-domain", m)

	tel.RecordTurnEnd(
		context.Background(),
		openairesponses.TurnResponse{
			PromptTokens:     150,
			CompletionTokens: 75,
		},
		nil,
		openairesponses.ExecutionIdentity{},
	)

	promptVal := counterValue(t, m.TokensTotal, "my-domain", "prompt")
	if promptVal != 150 {
		t.Errorf("expected TokensTotal prompt=150, got %v", promptVal)
	}

	completionVal := counterValue(t, m.TokensTotal, "my-domain", "completion")
	if completionVal != 75 {
		t.Errorf("expected TokensTotal completion=75, got %v", completionVal)
	}
}

func TestSubagentTelemetry_RecordTurnEnd_TokenMetricsAccumulate(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	tel := NewSubagentTelemetry("my-domain", m)

	// Simulate two turns with token counts.
	tel.RecordTurnEnd(
		context.Background(),
		openairesponses.TurnResponse{
			PromptTokens:     100,
			CompletionTokens: 50,
		},
		nil,
		openairesponses.ExecutionIdentity{},
	)
	tel.RecordTurnEnd(
		context.Background(),
		openairesponses.TurnResponse{
			PromptTokens:     200,
			CompletionTokens: 80,
		},
		nil,
		openairesponses.ExecutionIdentity{},
	)

	promptVal := counterValue(t, m.TokensTotal, "my-domain", "prompt")
	if promptVal != 300 {
		t.Errorf("expected TokensTotal prompt=300, got %v", promptVal)
	}

	completionVal := counterValue(t, m.TokensTotal, "my-domain", "completion")
	if completionVal != 130 {
		t.Errorf("expected TokensTotal completion=130, got %v", completionVal)
	}
}

func TestSubagentTelemetry_RecordTurnEnd_ZeroTokensNotRecorded(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	tel := NewSubagentTelemetry("my-domain", m)

	// Response with zero tokens should not increment the counter.
	tel.RecordTurnEnd(
		context.Background(),
		openairesponses.TurnResponse{
			PromptTokens:     0,
			CompletionTokens: 0,
		},
		nil,
		openairesponses.ExecutionIdentity{},
	)

	promptVal := counterValue(t, m.TokensTotal, "my-domain", "prompt")
	if promptVal != 0 {
		t.Errorf("expected TokensTotal prompt=0 for zero tokens, got %v", promptVal)
	}

	completionVal := counterValue(t, m.TokensTotal, "my-domain", "completion")
	if completionVal != 0 {
		t.Errorf("expected TokensTotal completion=0 for zero tokens, got %v", completionVal)
	}
}

// counterValue reads the current value of a prometheus.CounterVec for given labels.
func counterValue(t *testing.T, cv *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	counter, err := cv.GetMetricWithLabelValues(labels...)
	if err != nil {
		t.Fatalf("get metric: %v", err)
	}
	pb := &dto.Metric{}
	if err := counter.Write(pb); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return pb.GetCounter().GetValue()
}
