package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// newTestStore opens a temp-file SQLite store. Using a real file (not
// ":memory:") keeps connection pooling honest: each pool conn gets its own
// view but they share the same persistent state via the OS file.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(context.Background(), Options{Path: filepath.Join(dir, "mills.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestOpen_AppliesMigrations(t *testing.T) {
	st := newTestStore(t)
	// Re-running Migrate must be idempotent — goose tracks applied versions.
	if err := Migrate(context.Background(), st.DB()); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	// Sanity: every table named in the schema exists.
	want := []string{
		"roadmap_intents", "council_runs", "backlog_items",
		"pipeline_runs", "stage_results", "gate_outcomes",
		"kpi_snapshots", "eval_scores", "events",
	}
	for _, table := range want {
		var name string
		err := st.DB().QueryRowContext(context.Background(),
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestBacklog_RoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Seed the council parent so the backlog FK resolves.
	council := "COUNCIL-2026-04-25"
	if err := st.Council.Put(ctx, &CouncilRun{
		ID: council, Trigger: CouncilTriggerCron,
		StartedAt: time.Now().UTC(), Outcome: CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed council: %v", err)
	}

	iid := int64(312)
	item := &BacklogItem{
		ID:             "MILLS-2026-04-25-001",
		GitLabIssueIID: &iid,
		Title:          "Refactor SpawnPanel to use shared DataTable",
		Labels:         []string{"debt", "hud", "auto"},
		State:          BacklogQueued,
		Priority:       P2,
		SpecDoc:        ".loom/91-implementation-plan.md",
		SpecAnchor:     "Slice 4",
		Success: SuccessCriteria{
			Tests: []string{"pnpm --dir internal/hud/frontend test -- SpawnPanel"},
		},
		Budget: Budget{MaxCostUSD: 2.50, MaxTurns: 60, MaxPipelineMinutes: 45},
		Policy: ItemPolicy{AutoMerge: true},
		Slices: []Slice{{
			Name:  "refactor-table",
			Files: []string{"internal/hud/frontend/src/lib/components/SpawnPanel.svelte"},
			Tests: []string{"internal/hud/frontend/src/lib/components/SpawnPanel.test.ts"},
		}},
		Dependencies: []string{"MILLS-2026-04-25-000"},
		CouncilRunID: &council,
		CreatedBy:    "council",
	}
	if err := st.Backlog.Put(ctx, item); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := st.Backlog.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != item.Title {
		t.Errorf("title: got %q want %q", got.Title, item.Title)
	}
	if got.GitLabIssueIID == nil || *got.GitLabIssueIID != iid {
		t.Errorf("gitlab iid: got %v want %d", got.GitLabIssueIID, iid)
	}
	if len(got.Labels) != 3 || got.Labels[0] != "debt" {
		t.Errorf("labels: got %v", got.Labels)
	}
	if got.Budget.MaxCostUSD != 2.50 {
		t.Errorf("budget cost: got %v", got.Budget.MaxCostUSD)
	}
	if !got.Policy.AutoMerge {
		t.Errorf("policy auto_merge lost")
	}
	if len(got.Slices) != 1 || got.Slices[0].Name != "refactor-table" {
		t.Errorf("slices: %+v", got.Slices)
	}
	if got.CouncilRunID == nil || *got.CouncilRunID != council {
		t.Errorf("council ref: got %v", got.CouncilRunID)
	}

	// Update flow: change state and re-Put; CreatedAt must be preserved.
	originalCreated := got.CreatedAt
	got.State = BacklogRunning
	got.CreatedAt = time.Time{} // simulate caller forgetting; Put must re-fill
	if err := st.Backlog.Put(ctx, got); err != nil {
		t.Fatalf("re-put: %v", err)
	}
	got2, err := st.Backlog.Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got2.State != BacklogRunning {
		t.Errorf("state not updated: %v", got2.State)
	}
	if !got2.CreatedAt.Equal(originalCreated) && got2.CreatedAt.IsZero() {
		t.Errorf("created_at clobbered to zero")
	}

	// ListByState
	queueOnly := &BacklogItem{
		ID: "MILLS-2026-04-25-002", Title: "second", State: BacklogQueued,
		Priority: P3, CreatedBy: "council",
	}
	if err := st.Backlog.Put(ctx, queueOnly); err != nil {
		t.Fatalf("put 2: %v", err)
	}
	queued, err := st.Backlog.ListByState(ctx, BacklogQueued)
	if err != nil {
		t.Fatalf("list queued: %v", err)
	}
	if len(queued) != 1 || queued[0].ID != queueOnly.ID {
		t.Errorf("queued list: %+v", queued)
	}

	// Delete
	if err := st.Backlog.Delete(ctx, queueOnly.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.Backlog.Get(ctx, queueOnly.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if err := st.Backlog.Delete(ctx, queueOnly.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete-already-gone: expected ErrNotFound, got %v", err)
	}
}

func TestCouncil_RoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	end := time.Now().UTC().Add(10 * time.Minute)
	run := &CouncilRun{
		ID:              "COUNCIL-2026-04-25",
		Trigger:         CouncilTriggerCron,
		StartedAt:       time.Now().UTC(),
		EndedAt:         &end,
		Outcome:         CouncilOutcomeSuccess,
		CostFrontierUSD: 8.42,
		CostLocalUSD:    0.10,
		Artifacts: []ArtifactRef{
			{Kind: "research", Path: ".loom/89.md"},
			{Kind: "backlog_create", ID: "MILLS-2026-04-25-001"},
		},
		BacklogDeltas: BacklogDeltas{Created: []string{"MILLS-2026-04-25-001"}},
		Sidecar:       map[string]any{"models": []string{"claude", "codex"}},
		BranchName:    "council/2026-04-25",
	}
	if err := st.Council.Put(ctx, run); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := st.Council.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Outcome != CouncilOutcomeSuccess {
		t.Errorf("outcome: %v", got.Outcome)
	}
	if got.EndedAt == nil {
		t.Errorf("ended_at lost")
	}
	if len(got.Artifacts) != 2 || got.Artifacts[0].Kind != "research" {
		t.Errorf("artifacts: %+v", got.Artifacts)
	}
	if len(got.BacklogDeltas.Created) != 1 {
		t.Errorf("deltas.created: %+v", got.BacklogDeltas)
	}

	list, err := st.Council.List(ctx, 10)
	if err != nil || len(list) != 1 {
		t.Errorf("list: %v len=%d", err, len(list))
	}
}

func TestPipeline_RoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Foreign key requires backlog parent.
	parent := &BacklogItem{
		ID: "MILLS-X", Title: "x", State: BacklogQueued, Priority: P2, CreatedBy: "test",
	}
	if err := st.Backlog.Put(ctx, parent); err != nil {
		t.Fatalf("put backlog: %v", err)
	}

	mr := int64(99)
	run := &PipelineRun{
		ID:              "PIPE-1",
		BacklogID:       parent.ID,
		Template:        "mills-default-pipeline",
		State:           PipelineRunning(),
		CurrentStage:    "implement",
		Attempts:        1,
		WorktreePath:    "/work/PIPE-1",
		MRIID:           &mr,
		StartedAt:       time.Now().UTC(),
		CostUSD:         0.42,
		ParentSessionID: "sess-abc",
	}
	if err := st.Pipeline.PutRun(ctx, run); err != nil {
		t.Fatalf("put run: %v", err)
	}
	gotRun, err := st.Pipeline.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if gotRun.MRIID == nil || *gotRun.MRIID != mr {
		t.Errorf("mr_iid: %v", gotRun.MRIID)
	}
	if gotRun.CurrentStage != "implement" {
		t.Errorf("current_stage: %v", gotRun.CurrentStage)
	}

	// Stage results: idempotent retry.
	outcome := StageOutcomeSuccess
	end := time.Now().UTC().Add(time.Second)
	stage := &StageResult{
		PipelineRunID: run.ID,
		Stage:         "implement",
		Attempt:       1,
		StartedAt:     time.Now().UTC(),
		EndedAt:       &end,
		Outcome:       &outcome,
		SpawnID:       "spawn-1",
		CostUSD:       0.10,
		Artifacts:     map[string]any{"files_changed": 3},
		LogTail:       "wrote 3 files",
	}
	if err := st.Pipeline.PutStage(ctx, stage); err != nil {
		t.Fatalf("put stage: %v", err)
	}
	// Re-Put with same key should upsert, not duplicate.
	stage.LogTail = "wrote 4 files (retry)"
	if err := st.Pipeline.PutStage(ctx, stage); err != nil {
		t.Fatalf("re-put stage: %v", err)
	}
	stages, err := st.Pipeline.ListStages(ctx, run.ID)
	if err != nil {
		t.Fatalf("list stages: %v", err)
	}
	if len(stages) != 1 {
		t.Errorf("expected 1 stage (idempotent retry), got %d", len(stages))
	}
	if stages[0].LogTail != "wrote 4 files (retry)" {
		t.Errorf("log tail not updated: %q", stages[0].LogTail)
	}

	// Gate outcomes.
	gate := &GateOutcome{
		PipelineRunID: run.ID,
		AfterStage:    "implement",
		GateName:      "diff_size",
		Outcome:       GateOutcomePass,
		Reasons:       []string{},
		JudgedBy:      "go",
	}
	if err := st.Pipeline.PutGate(ctx, gate); err != nil {
		t.Fatalf("put gate: %v", err)
	}
	gates, err := st.Pipeline.ListGates(ctx, run.ID)
	if err != nil || len(gates) != 1 {
		t.Errorf("list gates: err=%v len=%d", err, len(gates))
	}

	// Foreign-key cascade: deleting backlog item should clean up.
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM backlog_items WHERE id=?`, parent.ID); err != nil {
		t.Fatalf("delete backlog: %v", err)
	}
	if _, err := st.Pipeline.GetRun(ctx, run.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected cascade delete; got %v", err)
	}
}

// PipelineRunning returns a state value; small helper to keep call site readable.
func PipelineRunning() PipelineState { return PipelineImplementing }

func TestKPI_RoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	snap := &KPISnapshot{
		WindowSeconds: 86400,
		Metrics:       map[string]any{"cost_per_merged": 1.23, "p50_latency_s": 420},
	}
	if err := st.KPI.RecordSnapshot(ctx, snap); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := st.KPI.Latest(ctx, 86400)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if got.Metrics["cost_per_merged"] != 1.23 {
		t.Errorf("metrics decode: %+v", got.Metrics)
	}
	rng, err := st.KPI.Range(ctx, 86400, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil || len(rng) != 1 {
		t.Errorf("range: err=%v len=%d", err, len(rng))
	}
}

func TestEval_RoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	first := &EvalScore{
		SubjectKind: EvalSubjectCouncilRun,
		SubjectID:   "COUNCIL-1",
		Rubric:      "artifact",
		Score:       0.85,
		Breakdown:   map[string]any{"validity": 1.0, "completeness": 0.7},
		JudgedBy:    "flexinfer:qwen3.5-9b",
	}
	if err := st.Eval.RecordScore(ctx, first); err != nil {
		t.Fatalf("record first: %v", err)
	}
	// Newer score for the same rubric should be returned by LatestPerSubject.
	second := &EvalScore{
		SubjectKind: EvalSubjectCouncilRun,
		SubjectID:   "COUNCIL-1",
		Rubric:      "artifact",
		Score:       0.92,
		Breakdown:   map[string]any{"validity": 1.0, "completeness": 0.84},
		JudgedBy:    "flexinfer:qwen3.5-9b",
		EvaluatedAt: time.Now().UTC().Add(time.Minute),
	}
	if err := st.Eval.RecordScore(ctx, second); err != nil {
		t.Fatalf("record second: %v", err)
	}
	latest, err := st.Eval.LatestPerSubject(ctx, EvalSubjectCouncilRun, "COUNCIL-1")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(latest) != 1 || latest[0].Score != 0.92 {
		t.Errorf("latest score: %+v", latest)
	}
}

func TestEvents_RoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		e := &Event{
			Actor:       "reconciler",
			Kind:        "tick",
			SubjectKind: "pipeline_run",
			SubjectID:   fmt.Sprintf("PIPE-%d", i),
			Payload:     map[string]any{"i": i},
		}
		if err := st.Events.Append(ctx, e); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	pipe1, err := st.Events.ListBySubject(ctx, "pipeline_run", "PIPE-1", 10)
	if err != nil || len(pipe1) != 1 {
		t.Errorf("by subject: err=%v len=%d", err, len(pipe1))
	}
	all, err := st.Events.ListSince(ctx, time.Now().Add(-time.Hour), 10)
	if err != nil || len(all) != 3 {
		t.Errorf("since: err=%v len=%d", err, len(all))
	}
}

func TestRoadmap_UpsertAndStale(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	a := &RoadmapIntent{
		Theme:                "agent-orchestration",
		Priority:             1,
		Summary:              "Loom Mills — council + pipeline",
		LastSeenInRoadmapSHA: "sha-1",
	}
	if err := st.Roadmap.Upsert(ctx, a); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	// Same theme + summary → upsert (no duplicate row).
	a.Priority = 2
	a.LastSeenInRoadmapSHA = "sha-2"
	if err := st.Roadmap.Upsert(ctx, a); err != nil {
		t.Fatalf("upsert a-2: %v", err)
	}
	list, err := st.Roadmap.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Priority != 2 {
		t.Errorf("dedup failed: %+v", list)
	}

	// A stale intent that wasn't seen in sha-2 should be cleanable.
	b := &RoadmapIntent{
		Theme: "deprecated", Priority: 9, Summary: "old idea",
		LastSeenInRoadmapSHA: "sha-1",
	}
	if err := st.Roadmap.Upsert(ctx, b); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	n, err := st.Roadmap.DeleteStale(ctx, "sha-2")
	if err != nil {
		t.Fatalf("delete-stale: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 stale row deleted, got %d", n)
	}
}

// TestConcurrency_NoLockedErrors writes to multiple tables from N goroutines.
// With WAL + busy_timeout we should not see "database is locked" errors.
func TestConcurrency_NoLockedErrors(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	const goroutines = 16
	const opsPerG = 25

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*opsPerG)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerG; i++ {
				e := &Event{
					Actor:   "test",
					Kind:    "load",
					Payload: map[string]any{"g": g, "i": i},
				}
				if err := st.Events.Append(ctx, e); err != nil {
					errs <- fmt.Errorf("g%d/i%d: %w", g, i, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent write failed: %v", err)
	}

	// Verify count.
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	want := goroutines * opsPerG
	if n != want {
		t.Errorf("rowcount: got %d want %d", n, want)
	}
}

func TestOpen_RejectsEmptyPath(t *testing.T) {
	_, err := Open(context.Background(), Options{Path: ""})
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// Compile-time guard: scanner must accept *sql.Row and *sql.Rows.
var (
	_ scanner = (*sql.Row)(nil)
	_ scanner = (*sql.Rows)(nil)
)
