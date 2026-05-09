package daemon

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/crb2nu/loom/pkg/weaver"
)

// EventWeaverQueryComplete is emitted on the daemon EventBus once per
// completed weaver router Query (success, error, or no-match).
// Subscribers (HUD, Spectator, future agent-context bridge) consume
// this to stitch weaver activity into session timelines.
const EventWeaverQueryComplete EventType = "weaver.query.complete"

// envRecordToContext gates whether the daemon emits the
// weaver.query.complete event. Defaults to true; set
// WEAVER_RECORD_TO_CONTEXT=0 to silence.
const envRecordToContext = "WEAVER_RECORD_TO_CONTEXT"

// weaverContextRecorder is the QueryRecorder the daemon installs on
// the weaver Router. It logs every query and publishes a structured
// event on the daemon EventBus. The event payload follows the
// agent-context EntryTypeWeaverQuery schema so a downstream consumer
// (e.g., the existing /api/events publisher hooked to mcp-agent-
// context) can write it as a `weaver_query` entry to the originating
// session without further translation.
type weaverContextRecorder struct {
	logger   *slog.Logger
	bus      *EventBus
	enabled  bool
	maxChars int
}

// newWeaverContextRecorder returns a recorder that publishes to the
// supplied EventBus. The bus argument may be nil; in that case the
// recorder still logs.
func newWeaverContextRecorder(logger *slog.Logger, bus *EventBus) *weaverContextRecorder {
	enabled := true
	if v := os.Getenv(envRecordToContext); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			enabled = parsed
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &weaverContextRecorder{
		logger:   logger.With("component", "weaver-recorder"),
		bus:      bus,
		enabled:  enabled,
		maxChars: 500, // truncation cap for the answer body
	}
}

// RecordQuery implements weaver.QueryRecorder.
func (r *weaverContextRecorder) RecordQuery(_ context.Context, rec weaver.QueryRecord) {
	if !r.enabled {
		return
	}

	r.logger.Debug("weaver query complete",
		"query_id", rec.QueryID,
		"status", rec.Status,
		"latency_ms", rec.LatencyMs,
		"tokens", rec.TotalTokens,
		"parent_session_id", rec.ParentSessionID,
		"domains", rec.Domains,
	)

	if r.bus == nil {
		return
	}

	answer := rec.Answer
	if len(answer) > r.maxChars {
		answer = answer[:r.maxChars] + "…"
	}

	payload := map[string]any{
		"entry_type":        "weaver_query",
		"query_id":          rec.QueryID,
		"parent_session_id": rec.ParentSessionID,
		"query":             rec.Query,
		"status":            rec.Status,
		"answer_preview":    answer,
		"domains":           rec.Domains,
		"latency_ms":        rec.LatencyMs,
		"total_tokens":      rec.TotalTokens,
		"started_at":        rec.StartedAt.UTC().Format(time.RFC3339),
	}
	r.bus.Publish(EventWeaverQueryComplete, payload)
}
