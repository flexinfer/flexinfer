package squads

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// SquadRoutedEventKind is the Kind written to the events log when the
// reconciler routes a queued backlog item to a squad. The OutcomeRecorder
// keys squad attribution off the most recent row with this Kind for a
// given pipeline_run_id.
const SquadRoutedEventKind = "reconciler.squad_routed"

// SquadRoutedEventPayload is the canonical payload shape for a squad
// attribution event. Persisted as JSON in the events table; read back
// by OutcomeRecorder.OnMerged.
type SquadRoutedEventPayload struct {
	RunID      string  `json:"run_id"`
	BacklogID  string  `json:"backlog_id"`
	SquadName  string  `json:"squad_name"`
	PathClass  string  `json:"path_class"`
	Confidence float64 `json:"confidence"`
	SampleSize int     `json:"sample_size"`
	Reason     string  `json:"reason,omitempty"`
}

// OutcomeRecorder writes squad_outcomes rows when a pipeline run merges,
// using the squad attribution emitted by the reconciler at routing time.
// It satisfies the same `OnMerged(ctx, run, item) error` shape as
// eval.OutcomeAttributor so the operator can chain both via a small
// composite hook (see WiredOnMerged).
//
// The recorder is best-effort: a missing or unparseable attribution
// event is logged and the merge proceeds without a squad_outcomes row.
// The eval Loop B attribution still fires.
type OutcomeRecorder struct {
	Store  *store.Store
	Logger *slog.Logger
}

// NewOutcomeRecorder constructs a recorder backed by the canonical store.
func NewOutcomeRecorder(st *store.Store) *OutcomeRecorder {
	return &OutcomeRecorder{Store: st}
}

// OnMerged records a squad_outcomes row attributing the merge to the
// squad chosen at routing time. It looks up the attribution event by
// run.ID; if none is found (item routed to FallbackName, or routing
// happened before squads were enabled) the call is a no-op.
//
// Errors writing the row are returned so the caller's chained hook can
// log; the returned error never blocks the merge.
func (r *OutcomeRecorder) OnMerged(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem) error {
	if r == nil || r.Store == nil || run == nil {
		return errors.New("squads: outcome recorder not configured")
	}
	payload, ok, err := r.lookupAttribution(ctx, run.ID)
	if err != nil {
		r.warn("attribution lookup failed", "error", err, "run", run.ID)
		return err
	}
	if !ok {
		return nil // no squad routing event for this run; not an error
	}
	if payload.SquadName == "" || payload.SquadName == FallbackName {
		return nil // routed to default; nothing to attribute
	}
	out := &store.SquadOutcome{
		SquadName:       payload.SquadName,
		PathClass:       payload.PathClass,
		PipelineRunID:   run.ID,
		Outcome:         store.SquadOutcomeMergedClean,
		CostUSD:         run.CostUSD,
		DurationSeconds: durationSeconds(run),
	}
	if err := r.Store.Squads.RecordOutcome(ctx, out); err != nil {
		// A unique-constraint violation here means the merge already had
		// a squad outcome recorded (idempotency). Don't surface as an
		// error; the regression-flip path keys on PipelineRunID anyway.
		if isAlreadyRecorded(err) {
			return nil
		}
		r.warn("record outcome failed", "error", err, "run", run.ID, "squad", payload.SquadName)
		return fmt.Errorf("squads: record outcome: %w", err)
	}
	return nil
}

// MarkRegressed flips an existing merged_clean outcome to merged_regressed
// for the given pipeline run. Called by the regression gate (slice 6.3)
// when an alert burst within 24h of merge correlates with this run. No-op
// when no squad outcome exists.
func (r *OutcomeRecorder) MarkRegressed(ctx context.Context, runID string) error {
	if r == nil || r.Store == nil || strings.TrimSpace(runID) == "" {
		return errors.New("squads: outcome recorder / runID required")
	}
	if err := r.Store.Squads.UpdateOutcome(ctx, runID, store.SquadOutcomeMergedRegressed); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("squads: mark regressed: %w", err)
	}
	return nil
}

// SquadRoutedSubjectKind is the events.subject_kind we use when
// recording squad attribution. Pairing it with the pipeline_run id
// puts the row in the existing idx_events_subject index so the
// recorder's lookup is one indexed query.
const SquadRoutedSubjectKind = "pipeline_run"

// lookupAttribution finds the most recent SquadRoutedEventKind row for
// a pipeline run. Reads via the indexed (subject_kind, subject_id) lookup
// the events DAO already exposes; filters to our event Kind in-memory
// (a bounded scan because we cap the query to 50 rows per run).
func (r *OutcomeRecorder) lookupAttribution(ctx context.Context, runID string) (SquadRoutedEventPayload, bool, error) {
	rows, err := r.Store.Events.ListBySubject(ctx, SquadRoutedSubjectKind, runID, 50)
	if err != nil {
		return SquadRoutedEventPayload{}, false, err
	}
	// Newest-first scan; ListBySubject returns DESC by occurred_at.
	for _, ev := range rows {
		if ev == nil || ev.Kind != SquadRoutedEventKind || ev.Payload == nil {
			continue
		}
		return decodePayload(ev.Payload), true, nil
	}
	return SquadRoutedEventPayload{}, false, nil
}

// decodePayload extracts the typed fields from the stored map[string]any
// payload. Defensive: missing or wrong-typed fields default to their zero
// value rather than failing the lookup.
func decodePayload(p map[string]any) SquadRoutedEventPayload {
	out := SquadRoutedEventPayload{}
	if v, ok := p["run_id"].(string); ok {
		out.RunID = v
	}
	if v, ok := p["backlog_id"].(string); ok {
		out.BacklogID = v
	}
	if v, ok := p["squad_name"].(string); ok {
		out.SquadName = v
	}
	if v, ok := p["path_class"].(string); ok {
		out.PathClass = v
	}
	if v, ok := p["confidence"].(float64); ok {
		out.Confidence = v
	}
	if v, ok := p["sample_size"].(float64); ok {
		// JSON unmarshals all numbers to float64.
		out.SampleSize = int(v)
	} else if v, ok := p["sample_size"].(int); ok {
		out.SampleSize = v
	}
	if v, ok := p["reason"].(string); ok {
		out.Reason = v
	}
	return out
}

// durationSeconds returns the wall-clock seconds between StartedAt and
// EndedAt, or 0 if EndedAt is unset.
func durationSeconds(run *store.PipelineRun) int64 {
	if run == nil || run.EndedAt == nil {
		return 0
	}
	return int64(run.EndedAt.Sub(run.StartedAt).Seconds())
}

// isAlreadyRecorded reports whether the DAO error is a unique-constraint
// violation on squad_outcomes(pipeline_run_id). The recorder treats it
// as a benign no-op so OnMerged stays idempotent under double-fire.
func isAlreadyRecorded(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") &&
		strings.Contains(msg, "squad_outcomes")
}

func (r *OutcomeRecorder) warn(msg string, kv ...any) {
	if r == nil || r.Logger == nil {
		return
	}
	r.Logger.Warn("squads: "+msg, kv...)
}
