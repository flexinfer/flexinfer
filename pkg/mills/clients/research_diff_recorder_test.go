package clients

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// fakeDiffStore captures SetResearchDiff calls and returns canned errors.
type fakeDiffStore struct {
	calls   []fakeDiffCall
	nextErr error
}

type fakeDiffCall struct {
	runID    string
	diffJSON string
}

func (f *fakeDiffStore) SetResearchDiff(_ context.Context, runID, diffJSON string) error {
	f.calls = append(f.calls, fakeDiffCall{runID: runID, diffJSON: diffJSON})
	return f.nextErr
}

func TestPipelineDAOResearchDiffRecorder_Record_HappyPath(t *testing.T) {
	st := &fakeDiffStore{}
	r := NewPipelineDAOResearchDiffRecorder(st, slog.Default())

	diff := map[string]any{
		"backlog_id":       "BL-1",
		"run_id":           "RUN-1",
		"legacy_chars":     100,
		"shadow_chars":     105,
		"length_delta_pct": 5.0,
	}
	r.Record(context.Background(), "RUN-1", "BL-1", diff)

	if len(st.calls) != 1 {
		t.Fatalf("SetResearchDiff calls = %d, want 1", len(st.calls))
	}
	if st.calls[0].runID != "RUN-1" {
		t.Errorf("runID = %q, want RUN-1", st.calls[0].runID)
	}
	// Marshal stable enough to assert key presence.
	var got map[string]any
	if err := json.Unmarshal([]byte(st.calls[0].diffJSON), &got); err != nil {
		t.Fatalf("decode persisted JSON: %v", err)
	}
	if got["backlog_id"] != "BL-1" {
		t.Errorf("persisted backlog_id = %v, want BL-1", got["backlog_id"])
	}
	if got["legacy_chars"].(float64) != 100 {
		t.Errorf("persisted legacy_chars = %v, want 100", got["legacy_chars"])
	}
}

func TestPipelineDAOResearchDiffRecorder_Record_EmptyRunIDSkipsWrite(t *testing.T) {
	st := &fakeDiffStore{}
	r := NewPipelineDAOResearchDiffRecorder(st, slog.Default())

	r.Record(context.Background(), "", "BL-1", map[string]any{"x": 1})

	if len(st.calls) != 0 {
		t.Errorf("expected zero writes for empty runID; got %d", len(st.calls))
	}
}

func TestPipelineDAOResearchDiffRecorder_Record_NotFoundIsSwallowed(t *testing.T) {
	st := &fakeDiffStore{nextErr: store.ErrNotFound}
	r := NewPipelineDAOResearchDiffRecorder(st, slog.Default())

	// Must not panic; must not affect caller (signature is void).
	r.Record(context.Background(), "RUN-missing", "BL-1", map[string]any{"x": 1})

	if len(st.calls) != 1 {
		t.Errorf("expected 1 attempted write; got %d", len(st.calls))
	}
}

func TestPipelineDAOResearchDiffRecorder_Record_GenericErrorIsSwallowed(t *testing.T) {
	st := &fakeDiffStore{nextErr: errors.New("disk full")}
	r := NewPipelineDAOResearchDiffRecorder(st, slog.Default())

	r.Record(context.Background(), "RUN-err", "BL-1", map[string]any{"x": 1})

	if len(st.calls) != 1 {
		t.Errorf("expected 1 attempted write; got %d", len(st.calls))
	}
}

func TestPipelineDAOResearchDiffRecorder_Record_NilStoreIsNoOp(t *testing.T) {
	// A nil store recorder should silently no-op so callers don't have
	// to gate every Record call on r != nil && r.Store != nil.
	r := NewPipelineDAOResearchDiffRecorder(nil, slog.Default())
	r.Record(context.Background(), "RUN-1", "BL-1", map[string]any{"x": 1})
	// no panic = pass
}

func TestPipelineDAOResearchDiffRecorder_Record_MarshalFailureIsSwallowed(t *testing.T) {
	st := &fakeDiffStore{}
	r := NewPipelineDAOResearchDiffRecorder(st, slog.Default())

	// chan values can't be JSON-encoded; recorder must not crash.
	r.Record(context.Background(), "RUN-1", "BL-1", map[string]any{
		"bad_chan": make(chan int),
	})
	if len(st.calls) != 0 {
		t.Errorf("expected zero writes when marshal fails; got %d", len(st.calls))
	}
}

// Compile-time assertion that *store.PipelineDAO satisfies our store
// interface. If a future schema change changes SetResearchDiff's
// signature, this will fail to build, surfacing the breakage at the
// recorder seam rather than at runtime in production.
var _ ResearchDiffStore = (*store.PipelineDAO)(nil)
