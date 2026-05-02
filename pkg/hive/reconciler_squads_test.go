package hive

import (
	"context"
	"errors"
	"testing"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// fakeSquadRouter satisfies SquadRouter for the reconciler-routing tests.
// last captures the most recent Pick call so tests can assert the
// reconciler passed the right item; out / err drive the response.
type fakeSquadRouter struct {
	last *store.BacklogItem
	out  SquadDecision
	err  error
}

func (f *fakeSquadRouter) Pick(_ context.Context, item *store.BacklogItem) (SquadDecision, error) {
	f.last = item
	if f.err != nil {
		return SquadDecision{}, f.err
	}
	return f.out, nil
}

// seedQueuedItemForRouting persists a fresh queued backlog item with a
// known file path. Tests then call rec.Tick and inspect the events log
// for the squad attribution row.
func seedQueuedItemForRouting(t *testing.T, env *recTestEnv, id, file string) *store.BacklogItem {
	t.Helper()
	item := &store.BacklogItem{
		ID:        id,
		Title:     "routed",
		State:     store.BacklogQueued,
		Priority:  store.P2,
		Slices:    []store.Slice{{Name: "x", Files: []string{file}}},
		Budget:    store.Budget{MaxCostUSD: 1.0},
		CreatedBy: "test",
	}
	if err := env.store.Backlog.Put(context.Background(), item); err != nil {
		t.Fatalf("seed item: %v", err)
	}
	return item
}

// findRoutedEvent returns the most recent reconciler.squad_routed event
// for a pipeline run, or nil if none.
func findRoutedEvent(t *testing.T, env *recTestEnv, runID string) *store.Event {
	t.Helper()
	rows, err := env.store.Events.ListBySubject(context.Background(),
		"pipeline_run", runID, 50)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	for _, ev := range rows {
		if ev != nil && ev.Kind == "reconciler.squad_routed" {
			return ev
		}
	}
	return nil
}

func TestReconciler_NoSquadRouter_EmitsNoAttribution(t *testing.T) {
	env := newRecEnv(t, nil)
	item := seedQueuedItemForRouting(t, env, "HIVE-NOROUTE-1",
		"internal/hud/frontend/foo.svelte")

	if _, err := env.rec.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	runs, _ := env.store.Pipeline.ListByBacklog(context.Background(), item.ID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run; got %d", len(runs))
	}
	if findRoutedEvent(t, env, runs[0].ID) != nil {
		t.Error("no SquadRouter set; reconciler must not emit a squad_routed event")
	}
}

func TestReconciler_SquadRouter_EmitsAttributionEvent(t *testing.T) {
	env := newRecEnv(t, nil)
	router := &fakeSquadRouter{
		out: SquadDecision{
			SquadName:  "hud-frontend",
			PathClass:  "internal/hud/frontend/**",
			Confidence: 0.74,
			SampleSize: 12,
			Reason:     "success_rate=0.74 over 12 outcomes",
		},
	}
	env.rec.SquadRouter = router
	item := seedQueuedItemForRouting(t, env, "HIVE-ROUTE-1",
		"internal/hud/frontend/SpawnPanel.svelte")

	if _, err := env.rec.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if router.last == nil || router.last.ID != item.ID {
		t.Errorf("router.Pick was not called with the queued item: %+v", router.last)
	}

	runs, _ := env.store.Pipeline.ListByBacklog(context.Background(), item.ID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run; got %d", len(runs))
	}
	ev := findRoutedEvent(t, env, runs[0].ID)
	if ev == nil {
		t.Fatal("expected a reconciler.squad_routed event")
	}
	if got, _ := ev.Payload["squad_name"].(string); got != "hud-frontend" {
		t.Errorf("squad_name in payload: got %q want hud-frontend", got)
	}
	if got, _ := ev.Payload["path_class"].(string); got != "internal/hud/frontend/**" {
		t.Errorf("path_class in payload: %q", got)
	}
	if got, _ := ev.Payload["confidence"].(float64); got != 0.74 {
		t.Errorf("confidence in payload: %v", got)
	}
	// SubjectKind / SubjectID let the recorder lookup hit the indexed
	// (subject_kind, subject_id) tuple — verify they round-trip.
	if ev.SubjectKind != "pipeline_run" || ev.SubjectID != runs[0].ID {
		t.Errorf("subject (%q,%q) want (pipeline_run,%s)",
			ev.SubjectKind, ev.SubjectID, runs[0].ID)
	}
}

func TestReconciler_SquadRouter_FallbackEmitsEventToo(t *testing.T) {
	env := newRecEnv(t, nil)
	env.rec.SquadRouter = &fakeSquadRouter{
		out: SquadDecision{
			SquadName: "_default",
			Reason:    "no squad paths matched item",
		},
	}
	item := seedQueuedItemForRouting(t, env, "HIVE-FB-1",
		"pkg/agentcontext/svc.go")

	if _, err := env.rec.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	runs, _ := env.store.Pipeline.ListByBacklog(context.Background(), item.ID)
	ev := findRoutedEvent(t, env, runs[0].ID)
	if ev == nil {
		t.Fatal("fallback routing should still emit an audit event")
	}
	if got, _ := ev.Payload["squad_name"].(string); got != "_default" {
		t.Errorf("fallback squad_name: %q", got)
	}
}

func TestReconciler_SquadRouter_ErrorDoesNotBlockStart(t *testing.T) {
	env := newRecEnv(t, nil)
	env.rec.SquadRouter = &fakeSquadRouter{err: errors.New("router exploded")}
	item := seedQueuedItemForRouting(t, env, "HIVE-ROUTERR-1",
		"internal/hud/frontend/foo.svelte")

	res, err := env.rec.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if res.Started != 1 {
		t.Errorf("router error should not block run start; res=%+v", res)
	}
	runs, _ := env.store.Pipeline.ListByBacklog(context.Background(), item.ID)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run; got %d", len(runs))
	}
	// No event when routing failed — the runner still starts but the
	// audit trail records the failure path via the slog.Warn the
	// reconciler emits (test asserts behavior, not the log line itself).
	if findRoutedEvent(t, env, runs[0].ID) != nil {
		t.Error("router error should suppress the squad_routed event")
	}
}
