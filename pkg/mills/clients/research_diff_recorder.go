package clients

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// ResearchDiffStore is the narrow surface PipelineDAOResearchDiffRecorder
// depends on. The production implementation is *store.PipelineDAO; tests
// inject a fake to exercise the recorder without standing up a SQLite
// store + FK chain.
type ResearchDiffStore interface {
	SetResearchDiff(ctx context.Context, runID string, diffJSON string) error
}

// PipelineDAOResearchDiffRecorder is the production ResearchDiffRecorder
// that persists shadow-mode research diffs to the
// pipeline_runs.research_diff column via PipelineDAO.SetResearchDiff.
//
// It is intentionally tolerant of missing context:
//   - empty runID skips the write (the diff is logged instead so
//     operators can still spot the run during the soak window)
//   - store.ErrNotFound is logged but not returned — recorder is
//     observability, not control flow, and the WeaverClient discards
//     errors from Record by design (see flexinfer.go:recordDiff).
//
// Wiring lives in cmd/loom-mills-operator/main.go behind the
// MILLS_RESEARCH_VIA_WEAVER=shadow|on env knob.
type PipelineDAOResearchDiffRecorder struct {
	Store  ResearchDiffStore
	Logger *slog.Logger
}

// NewPipelineDAOResearchDiffRecorder builds a recorder. dao must be
// non-nil; the operator should not register a recorder when the DAO is
// unavailable so the WeaverClient drops the diff entirely instead.
func NewPipelineDAOResearchDiffRecorder(dao ResearchDiffStore, logger *slog.Logger) *PipelineDAOResearchDiffRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &PipelineDAOResearchDiffRecorder{
		Store:  dao,
		Logger: logger.With("component", "mills-research-diff-recorder"),
	}
}

// Record satisfies clients.ResearchDiffRecorder.
//
// The diff map is JSON-encoded with stable keys (the WeaverClient
// composes them in flexinfer.go:recordDiff). Marshal failure is
// logged + dropped — this method has no failure mode of its own.
func (r *PipelineDAOResearchDiffRecorder) Record(ctx context.Context, runID, backlogID string, diff map[string]any) {
	if r == nil || r.Store == nil {
		return
	}
	if runID == "" {
		// No run id — happens when the call originates outside a
		// pipeline run (tests, manual probe). Log so soak observers
		// can audit the surface area; nothing to persist.
		r.Logger.Info("research diff dropped: empty run id",
			"backlog_id", backlogID,
			"diff", diff,
		)
		return
	}
	buf, err := json.Marshal(diff)
	if err != nil {
		r.Logger.Warn("research diff marshal failed",
			"run_id", runID, "backlog_id", backlogID, "error", err)
		return
	}
	if err := r.Store.SetResearchDiff(ctx, runID, string(buf)); err != nil {
		// ErrNotFound is the common "the run row hasn't landed yet"
		// case (shadow path completes before the dispatcher commits
		// the row). Log at info, not warn, so it doesn't pollute the
		// alert pipeline.
		level := slog.LevelWarn
		if errors.Is(err, store.ErrNotFound) {
			level = slog.LevelInfo
		}
		r.Logger.Log(ctx, level, "research diff persist failed",
			"run_id", runID, "backlog_id", backlogID, "error", err)
		return
	}
	r.Logger.Debug("research diff persisted",
		"run_id", runID, "backlog_id", backlogID, "bytes", len(buf))
}
