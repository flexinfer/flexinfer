package agentcontext

import (
	"context"
	"log/slog"
	"time"
)

// =========================================================================
// Compaction audit event (F2)
//
// WriteCompactionEvent records a structured "compaction_event" describing
// what the scheduler did in a single run. The event is emitted to the
// service logger so that it shows up in the standard log pipeline. Full
// persistence via svc.ctxSvc.Add requires a session scope that the
// scheduler does not currently own; a follow-up will wire that through.
// =========================================================================

// WriteCompactionEvent emits a structured audit log line describing a
// completed compaction run. svc may be nil, in which case the default
// slog logger is used.
func WriteCompactionEvent(
	_ context.Context,
	svc *Service,
	beforeTokens, afterTokens int,
	strategy, model string,
	duration time.Duration,
) {
	logger := slog.Default()
	if svc != nil && svc.logger != nil {
		logger = svc.logger
	}

	saved := beforeTokens - afterTokens
	logger.Info("compaction_event",
		"entry_type", "compaction_event",
		"strategy", strategy,
		"model", model,
		"tokens_before", beforeTokens,
		"tokens_after", afterTokens,
		"tokens_saved", saved,
		"duration_ms", duration.Milliseconds(),
	)
	// TODO(follow-up): persist to svc.ctxSvc.Add once compaction scheduler
	// has a session-scoped audit channel; see F2 spec §5.F2.
}
