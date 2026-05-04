package adaptive

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	millseval "github.com/crb2nu/loom/pkg/mills/eval"
	"github.com/crb2nu/loom/pkg/mills/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "h.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// fixedNow is the canonical "now" used across rules tests — the Sunday the
// adaptive job fires for the 2026-04-19..2026-05-03 window.
func fixedNow() time.Time {
	return time.Date(2026, 5, 3, 5, 0, 0, 0, time.UTC)
}

// seedPipelineRun is a one-liner constructor for a terminal-state pipeline
// run inside the analysis window.
func seedPipelineRun(t *testing.T, st *store.Store, id, backlogID string, state store.PipelineState, startedAt time.Time) {
	t.Helper()
	// BacklogItem is a foreign key on pipeline_runs — seed an item if not
	// present so the insert succeeds.
	if _, err := st.Backlog.Get(context.Background(), backlogID); err != nil {
		_ = st.Backlog.Put(context.Background(), &store.BacklogItem{
			ID:        backlogID,
			Title:     "x",
			State:     store.BacklogQueued,
			Priority:  store.P3,
			CreatedAt: startedAt,
		})
	}
	end := startedAt.Add(30 * time.Minute)
	if err := st.Pipeline.PutRun(context.Background(), &store.PipelineRun{
		ID:        id,
		BacklogID: backlogID,
		Template:  "default",
		State:     state,
		StartedAt: startedAt,
		EndedAt:   &end,
	}); err != nil {
		t.Fatalf("put run %s: %v", id, err)
	}
}

func seedGate(t *testing.T, st *store.Store, runID, name string, outcome store.GateOutcomeKind, at time.Time) {
	t.Helper()
	if err := st.Pipeline.PutGate(context.Background(), &store.GateOutcome{
		PipelineRunID: runID,
		AfterStage:    "test",
		GateName:      name,
		Outcome:       outcome,
		EvaluatedAt:   at,
		JudgedBy:      "test",
	}); err != nil {
		t.Fatalf("put gate: %v", err)
	}
}

func seedEval(t *testing.T, st *store.Store, kind store.EvalSubjectKind, subjectID, rubric string, score float64, at time.Time) {
	t.Helper()
	if err := st.Eval.RecordScore(context.Background(), &store.EvalScore{
		SubjectKind: kind,
		SubjectID:   subjectID,
		Rubric:      rubric,
		Score:       score,
		JudgedBy:    "test",
		EvaluatedAt: at,
	}); err != nil {
		t.Fatalf("record eval: %v", err)
	}
}

func seedAudit(t *testing.T, st *store.Store, sev store.AuditSeverity, at time.Time) {
	t.Helper()
	if err := st.Audit.RecordFinding(context.Background(), &store.AuditFinding{
		SubjectKind:   store.AuditSubjectPipelineMerge,
		SubjectID:     "RUN-X",
		Severity:      sev,
		RubricID:      "audit_v1",
		SurvivalScore: 0.5,
		CreatedAt:     at,
	}); err != nil {
		t.Fatalf("record audit: %v", err)
	}
}

func seedCouncil(t *testing.T, st *store.Store, id string, trigger store.CouncilTrigger, startedAt time.Time) {
	t.Helper()
	if err := st.Council.Put(context.Background(), &store.CouncilRun{
		ID:        id,
		Trigger:   trigger,
		StartedAt: startedAt,
		Outcome:   store.CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("put council: %v", err)
	}
}

// TestProposalsBuilder_Relax seeds a healthy week (high merge rate, no
// criticals, low gate fail rate) and asserts exactly one relax proposal
// appears with the budget target and a +20%-style diff.
func TestProposalsBuilder_Relax(t *testing.T) {
	st := openTestStore(t)
	now := fixedNow()
	// 19 merged + 1 escalated = 95% merge rate (matches threshold).
	for i := 0; i < 19; i++ {
		seedPipelineRun(t, st, runID(i), backlogID(i), store.PipelineDone, now.Add(-7*24*time.Hour))
	}
	seedPipelineRun(t, st, runID(19), backlogID(19), store.PipelineEscalated, now.Add(-7*24*time.Hour))

	// 1 gate fail / 30 evaluations = 3.3% fail rate (under 5%).
	for i := 0; i < 29; i++ {
		seedGate(t, st, runID(i%19), "ci", store.GateOutcomePass, now.Add(-3*24*time.Hour))
	}
	seedGate(t, st, runID(0), "ci", store.GateOutcomeFail, now.Add(-3*24*time.Hour))

	b := &ProposalsBuilder{
		Store:               st,
		DocsRoot:            t.TempDir(),
		CurrentMaxUSDPerRun: 5.00,
		Now:                 func() time.Time { return now },
	}
	res, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	relaxes := filterByKind(res.Proposals, store.PolicyProposalRelax)
	if len(relaxes) != 1 {
		t.Fatalf("want 1 relax proposal, got %d (all kinds: %s)", len(relaxes), kindList(res.Proposals))
	}
	got := relaxes[0]
	if got.Target != "policy.budgets.pipeline.max_usd_per_run" {
		t.Errorf("target = %q; want max_usd_per_run", got.Target)
	}
	if !strings.Contains(got.Diff, "$5.00") || !strings.Contains(got.Diff, "$6.00") {
		t.Errorf("diff = %q; want $5.00 -> $6.00 (current * 1.2)", got.Diff)
	}
	if got.State != store.PolicyProposalPending {
		t.Errorf("state = %q; want pending", got.State)
	}
	// Persistence check: row must exist on disk.
	persisted, err := st.PolicyProposals.ListByDate(context.Background(), res.ProposalDate)
	if err != nil {
		t.Fatalf("ListByDate: %v", err)
	}
	if len(persisted) != len(res.Proposals) {
		t.Errorf("persisted %d, want %d", len(persisted), len(res.Proposals))
	}
}

// TestProposalsBuilder_Tighten seeds a degraded week (median pipeline
// outcome below 0.7) with no audit criticals and asserts the tighten rule
// fires with the budget shrink diff.
func TestProposalsBuilder_Tighten(t *testing.T) {
	st := openTestStore(t)
	now := fixedNow()
	// Seed pipeline runs to satisfy the persistence FKs.
	for i := 0; i < 10; i++ {
		seedPipelineRun(t, st, runID(i), backlogID(i), store.PipelineDone, now.Add(-7*24*time.Hour))
	}
	// Median = 0.5 (below 0.7 threshold). 10 rows, all 0.5.
	for i := 0; i < 10; i++ {
		seedEval(t, st, store.EvalSubjectPipelineRun, runID(i), millseval.PipelineOutcomeRubric, 0.5, now.Add(-3*24*time.Hour))
	}
	b := &ProposalsBuilder{
		Store:               st,
		DocsRoot:            t.TempDir(),
		CurrentMaxUSDPerRun: 5.00,
		Now:                 func() time.Time { return now },
	}
	res, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	tightens := filterByKind(res.Proposals, store.PolicyProposalTighten)
	if len(tightens) != 1 {
		t.Fatalf("want 1 tighten proposal, got %d (all kinds: %s)", len(tightens), kindList(res.Proposals))
	}
	got := tightens[0]
	if got.Target != "policy.budgets.pipeline.max_usd_per_run" {
		t.Errorf("target = %q; want max_usd_per_run", got.Target)
	}
	if !strings.Contains(got.Diff, "$5.00") || !strings.Contains(got.Diff, "$4.00") {
		t.Errorf("diff = %q; want $5.00 -> $4.00 (current * 0.8)", got.Diff)
	}
}

// TestProposalsBuilder_Tighten_AuditCritical seeds a single critical audit
// finding and asserts the audit-priority branch fires (advisory_only flip).
func TestProposalsBuilder_Tighten_AuditCritical(t *testing.T) {
	st := openTestStore(t)
	now := fixedNow()
	seedAudit(t, st, store.AuditSeverityCritical, now.Add(-2*24*time.Hour))

	b := &ProposalsBuilder{
		Store:    st,
		DocsRoot: t.TempDir(),
		Now:      func() time.Time { return now },
	}
	res, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	tightens := filterByKind(res.Proposals, store.PolicyProposalTighten)
	if len(tightens) != 1 {
		t.Fatalf("want 1 tighten proposal, got %d", len(tightens))
	}
	got := tightens[0]
	if got.Target != "policy.audit.advisory_only" {
		t.Errorf("target = %q; want advisory_only", got.Target)
	}
	if got.Diff != "true -> false" {
		t.Errorf("diff = %q; want \"true -> false\"", got.Diff)
	}
}

// TestProposalsBuilder_RotateEnsemble seeds council ROI scores where the
// `cron` slot's median is well below the rolling median; assert exactly one
// rotate proposal targeting that slot.
func TestProposalsBuilder_RotateEnsemble(t *testing.T) {
	st := openTestStore(t)
	now := fixedNow()

	// roadmap slot: high scores (median 0.9)
	for i := 0; i < 4; i++ {
		id := "COUNCIL-RM-" + strconv.Itoa(i)
		seedCouncil(t, st, id, store.CouncilTriggerRoadmap, now.Add(-5*24*time.Hour))
		seedEval(t, st, store.EvalSubjectCouncilRun, id, millseval.CouncilROIRubric, 0.9, now.Add(-5*24*time.Hour))
	}
	// cron slot: low scores (median 0.4) — bottom-half of distribution
	for i := 0; i < 4; i++ {
		id := "COUNCIL-CR-" + strconv.Itoa(i)
		seedCouncil(t, st, id, store.CouncilTriggerCron, now.Add(-5*24*time.Hour))
		seedEval(t, st, store.EvalSubjectCouncilRun, id, millseval.CouncilROIRubric, 0.4, now.Add(-5*24*time.Hour))
	}

	b := &ProposalsBuilder{
		Store:    st,
		DocsRoot: t.TempDir(),
		Now:      func() time.Time { return now },
	}
	res, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rotates := filterByKind(res.Proposals, store.PolicyProposalRotateEnsemble)
	if len(rotates) != 1 {
		t.Fatalf("want 1 rotate proposal, got %d (all kinds: %s)", len(rotates), kindList(res.Proposals))
	}
	got := rotates[0]
	if got.Target != "policy.council.ensembles.cron" {
		t.Errorf("target = %q; want policy.council.ensembles.cron", got.Target)
	}
	if !strings.Contains(got.Rationale, "0.400") {
		t.Errorf("rationale should cite slot median 0.400; got: %s", got.Rationale)
	}
}

// TestProposalsBuilder_NoProposals_WhenSteadyState seeds a moderate week —
// merge rate above 0.95 fails the relax condition because gate-fail rate
// can't be computed (no gates), median pipeline_outcome is irrelevant
// without a pipeline_run rubric, and no audit criticals or council eval
// rows exist.
//
// Concretely: all three rules abstain because their *minimum* sample sizes
// or threshold guards aren't met. Expected output: zero proposals.
func TestProposalsBuilder_NoProposals_WhenSteadyState(t *testing.T) {
	st := openTestStore(t)
	now := fixedNow()
	// No data at all — every input slice is empty.
	b := &ProposalsBuilder{
		Store:    st,
		DocsRoot: t.TempDir(),
		Now:      func() time.Time { return now },
	}
	res, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Proposals) != 0 {
		t.Errorf("want 0 proposals on empty inputs; got %d (kinds=%s)",
			len(res.Proposals), kindList(res.Proposals))
	}
	// Markdown should still get written (with the "no proposals" note).
	if res.MarkdownPath == "" {
		t.Errorf("MarkdownPath empty; want a path even when zero proposals")
	}
}

// TestProposalsBuilder_WritesMarkdown asserts the markdown digest lands at
// .loom/mills/policy_proposals/<date>.md under the configured DocsRoot, and
// includes the proposal kinds + target text.
func TestProposalsBuilder_WritesMarkdown(t *testing.T) {
	st := openTestStore(t)
	now := fixedNow()
	docsRoot := t.TempDir()

	// Seed enough to trigger one relax proposal so the markdown has a
	// section, not just the "no proposals" placeholder.
	for i := 0; i < 19; i++ {
		seedPipelineRun(t, st, runID(i), backlogID(i), store.PipelineDone, now.Add(-7*24*time.Hour))
	}
	seedPipelineRun(t, st, runID(19), backlogID(19), store.PipelineEscalated, now.Add(-7*24*time.Hour))
	for i := 0; i < 30; i++ {
		seedGate(t, st, runID(i%19), "ci", store.GateOutcomePass, now.Add(-3*24*time.Hour))
	}

	b := &ProposalsBuilder{
		Store:               st,
		DocsRoot:            docsRoot,
		CurrentMaxUSDPerRun: 5.00,
		Now:                 func() time.Time { return now },
	}
	res, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantPath := filepath.Join(docsRoot, MarkdownDir, res.ProposalDate+".md")
	if res.MarkdownPath != wantPath {
		t.Errorf("MarkdownPath = %q; want %q", res.MarkdownPath, wantPath)
	}
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "# Policy proposals — "+res.ProposalDate) {
		t.Errorf("markdown header missing; body=%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "policy.budgets.pipeline.max_usd_per_run") {
		t.Errorf("markdown does not cite target; body=%s", bodyStr)
	}
}

// TestProposalsScheduler_FiresSundayMorning verifies the (weekday, hour)
// gate + de-dup behaviour without hitting the store. Build is set to a
// disabled-stub builder (Builder: nil-store-safe) — Tick should still
// return (true, nil) on the configured slot.
//
// We use a minimal real builder with an in-memory store so the call into
// b.Run actually succeeds. Most of the test verifies the (weekday, hour)
// gate, not the builder logic.
func TestProposalsScheduler_FiresSundayMorning(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 5, 10, 5, 0, 0, 0, time.UTC) // Sunday 05:00 UTC
	if got := now.Weekday(); got != time.Sunday {
		t.Fatalf("fixture date is not Sunday: %s", got)
	}
	b := &ProposalsBuilder{
		Store:    st,
		DocsRoot: t.TempDir(),
		Now:      func() time.Time { return now },
	}
	sched := NewProposalsScheduler(b)
	sched.Now = func() time.Time { return now }

	fired, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick #1: %v", err)
	}
	if !fired {
		t.Fatalf("first Tick on Sunday 05:00 should fire")
	}

	// Same hour → de-dup.
	fired2, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick #2: %v", err)
	}
	if fired2 {
		t.Errorf("second Tick in same window should NOT fire (dedup)")
	}
}

// TestProposalsScheduler_SkipsWrongWeekday confirms the scheduler is inert
// on Saturday at the same hour.
func TestProposalsScheduler_SkipsWrongWeekday(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 5, 9, 5, 0, 0, 0, time.UTC) // Saturday 05:00 UTC
	b := &ProposalsBuilder{
		Store:    st,
		DocsRoot: t.TempDir(),
		Now:      func() time.Time { return now },
	}
	sched := NewProposalsScheduler(b)
	sched.Now = func() time.Time { return now }

	fired, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if fired {
		t.Errorf("Saturday Tick fired; want skip")
	}
}

// TestProposalsScheduler_SkipsWrongHour same shape but for the wrong hour
// on the right weekday.
func TestProposalsScheduler_SkipsWrongHour(t *testing.T) {
	st := openTestStore(t)
	now := time.Date(2026, 5, 10, 4, 59, 0, 0, time.UTC) // Sunday 04:59
	b := &ProposalsBuilder{
		Store:    st,
		DocsRoot: t.TempDir(),
		Now:      func() time.Time { return now },
	}
	sched := NewProposalsScheduler(b)
	sched.Now = func() time.Time { return now }

	fired, err := sched.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if fired {
		t.Errorf("Sunday 04:59 Tick fired; want skip")
	}
}

// TestProposalsScheduler_NilBuilderExitsOnContextCancel verifies the
// "builder disabled" path waits for ctx and returns nil.
func TestProposalsScheduler_NilBuilderExitsOnContextCancel(t *testing.T) {
	sched := NewProposalsScheduler(nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not exit after cancel")
	}
}

// ----- helpers -----

func filterByKind(props []*store.PolicyProposal, kind store.PolicyProposalKind) []*store.PolicyProposal {
	out := make([]*store.PolicyProposal, 0, len(props))
	for _, p := range props {
		if p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

func kindList(props []*store.PolicyProposal) string {
	xs := make([]string, 0, len(props))
	for _, p := range props {
		xs = append(xs, string(p.Kind))
	}
	return strings.Join(xs, ",")
}

func runID(i int) string     { return "RUN-" + strconv.Itoa(i) }
func backlogID(i int) string { return "BL-" + strconv.Itoa(i) }
