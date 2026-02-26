package mcplog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestNewLoggerJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(slog.LevelInfo, "json", &buf)
	logger.Info("hello", "key", "value")

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected log output")
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("expected json log output: %v", err)
	}
	if got["msg"] != "hello" {
		t.Fatalf("expected msg=hello, got %v", got["msg"])
	}
	if got["key"] != "value" {
		t.Fatalf("expected key=value, got %v", got["key"])
	}
}

func TestNewLoggerTextFormatFallback(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(slog.LevelInfo, "unknown", &buf)
	logger.Info("hello", "key", "value")

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected log output")
	}
	if strings.HasPrefix(line, "{") {
		t.Fatalf("expected text log output, got json: %s", line)
	}
	if !strings.Contains(line, "msg=hello") {
		t.Fatalf("expected msg=hello in text output: %s", line)
	}
}

func TestLoggerIncludesTraceContextFields(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(slog.LevelInfo, "json", &buf)

	traceID, err := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("TraceIDFromHex: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("0123456789abcdef")
	if err != nil {
		t.Fatalf("SpanIDFromHex: %v", err)
	}

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     false,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	logger.InfoContext(ctx, "with trace")

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected log output")
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("expected json log output: %v", err)
	}

	if got["trace_id"] != traceID.String() {
		t.Fatalf("expected trace_id=%s, got %v", traceID.String(), got["trace_id"])
	}
	if got["span_id"] != spanID.String() {
		t.Fatalf("expected span_id=%s, got %v", spanID.String(), got["span_id"])
	}
}
