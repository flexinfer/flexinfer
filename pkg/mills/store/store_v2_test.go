package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Mills v2 — Hierarchical Swarm DAO round-trip tests. These exercise the new
// tables introduced by 002_v2.sql and the v2 fields on pipeline_runs.

func TestMigrate_v2_TablesExist(t *testing.T) {
	st := newTestStore(t)
	want := []string{
		"squads", "squad_memory", "squad_outcomes",
		"audit_findings",
		"cross_repo_runs",
		"council_debate_rounds",
		"policy_proposals",
	}
	for _, table := range want {
		var name string
		err := st.DB().QueryRowContext(context.Background(),
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("v2 table %s missing: %v", table, err)
		}
	}
	// Also assert the two new pipeline_runs columns landed.
	var col string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT name FROM pragma_table_info('pipeline_runs') WHERE name='parent_run_id'`,
	).Scan(&col); err != nil {
		t.Errorf("pipeline_runs.parent_run_id missing: %v", err)
	}
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT name FROM pragma_table_info('pipeline_runs') WHERE name='depth'`,
	).Scan(&col); err != nil {
		t.Errorf("pipeline_runs.depth missing: %v", err)
	}
}

func TestMigrate_v2_Idempotent(t *testing.T) {
	st := newTestStore(t)
	// Re-running Migrate must be a no-op for both 001 and 002.
	if err := Migrate(context.Background(), st.DB()); err != nil {
		t.Fatalf("re-migrate v2: %v", err)
	}
	// schema_migrations should have exactly two rows: 1 and 2.
	rows, err := st.DB().QueryContext(context.Background(),
		`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		versions = append(versions, v)
	}
	// 003 (research_diff column) lands alongside the v2 migrations.
	if len(versions) != 3 || versions[0] != 1 || versions[1] != 2 || versions[2] != 3 {
		t.Errorf("schema_migrations versions: got %v want [1 2 3]", versions)
	}
}

func TestSquad_RoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	sq := &Squad{
		Name:  "hud-frontend",
		Paths: []string{"internal/hud/frontend/**", "mcp/skills/hud-*"},
		Tests: []string{"pnpm-typecheck", "pnpm-vitest", "pnpm-build"},
		Gates: map[string]any{
			"required": []string{"pr_self_review", "scope", "secret_scan", "commit_format"},
			"advisory": []string{"coverage"},
		},
		Ensemble: map[string]any{
			"editor": map[string]any{"backend": "spawn", "driver": "claude-opus", "max_cost_usd": 4.0},
			"judge":  map[string]any{"backend": "flexinfer", "model": "llama-4-70b-instruct"},
			"reviewers": []map[string]any{
				{"backend": "flexinfer", "model": "llama-4-70b-instruct", "lens": "ux"},
			},
		},
		BudgetShare:      0.30,
		RecursionEnabled: false,
		Enabled:          true,
		LastLoadedSHA:    "abc1234",
	}
	if err := st.Squads.PutSquad(ctx, sq); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := st.Squads.GetSquad(ctx, "hud-frontend")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != sq.Name || got.BudgetShare != 0.30 || !got.Enabled || got.RecursionEnabled {
		t.Errorf("scalars round-trip: %+v", got)
	}
	if len(got.Paths) != 2 || got.Paths[0] != "internal/hud/frontend/**" {
		t.Errorf("paths round-trip: %v", got.Paths)
	}
	if got.LastLoadedSHA != "abc1234" {
		t.Errorf("last_loaded_sha: %q", got.LastLoadedSHA)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps zero: %+v", got)
	}

	// Update reflects on UpdatedAt but preserves CreatedAt.
	createdBefore := got.CreatedAt
	sq.BudgetShare = 0.40
	time.Sleep(2 * time.Millisecond) // ensure timestamp delta
	if err := st.Squads.PutSquad(ctx, sq); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, err := st.Squads.GetSquad(ctx, "hud-frontend")
	if err != nil {
		t.Fatalf("re-get: %v", err)
	}
	if got2.BudgetShare != 0.40 {
		t.Errorf("budget_share update: got %v want 0.40", got2.BudgetShare)
	}
	if !got2.CreatedAt.Equal(createdBefore) {
		t.Errorf("CreatedAt should be preserved: before=%v after=%v", createdBefore, got2.CreatedAt)
	}

	// List + Delete.
	if err := st.Squads.PutSquad(ctx, &Squad{Name: "gitops", Enabled: true, BudgetShare: 0.20}); err != nil {
		t.Fatalf("put gitops: %v", err)
	}
	all, err := st.Squads.ListSquads(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("list size: got %d want 2", len(all))
	}
	// alpha order
	if all[0].Name != "gitops" || all[1].Name != "hud-frontend" {
		t.Errorf("list order: %v %v", all[0].Name, all[1].Name)
	}

	if err := st.Squads.DeleteSquad(ctx, "gitops"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := st.Squads.DeleteSquad(ctx, "gitops"); !errors.Is(err, ErrNotFound) {
		t.Errorf("re-delete: got %v want ErrNotFound", err)
	}
}

func TestSquadMemory_UpsertAndRecall(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.Squads.PutSquad(ctx, &Squad{Name: "hud-frontend"}); err != nil {
		t.Fatalf("seed squad: %v", err)
	}

	for i, m := range []*SquadMemory{
		{SquadName: "hud-frontend", Kind: SquadMemoryConvention, Title: "writeFileAtomic", Body: "always atomic for watched files", Importance: 0.9},
		{SquadName: "hud-frontend", Kind: SquadMemoryTechDebt, Title: "split SpawnPanel", Body: "DEBT-072", Importance: 0.7, Refs: []string{"internal/hud/frontend/src/lib/components/SpawnPanel.svelte:1"}},
		{SquadName: "hud-frontend", Kind: SquadMemoryConvention, Title: "lo-fi sparkline", Body: "use SparkLine for trends", Importance: 0.4},
		{SquadName: "hud-frontend", Kind: SquadMemoryFollowup, Title: "prune-dead-stores", Body: "stores never read", Importance: 0.2},
	} {
		if err := st.Squads.PutMemory(ctx, m); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}

	// Upsert by (squad, kind, title) — same title same kind updates Body.
	if err := st.Squads.PutMemory(ctx, &SquadMemory{
		SquadName: "hud-frontend", Kind: SquadMemoryConvention, Title: "writeFileAtomic",
		Body: "always atomic — pkg/skills/fileops.go", Importance: 0.95,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	all, err := st.Squads.MemoryRecall(ctx, "hud-frontend", "", 10)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("recall all: got %d want 4 (upsert should replace)", len(all))
	}
	// Sorted importance DESC.
	if all[0].Importance < all[1].Importance {
		t.Errorf("recall not sorted by importance: %v", all)
	}
	// Filter by kind.
	conv, err := st.Squads.MemoryRecall(ctx, "hud-frontend", SquadMemoryConvention, 10)
	if err != nil {
		t.Fatalf("recall convention: %v", err)
	}
	if len(conv) != 2 {
		t.Errorf("recall convention: got %d want 2", len(conv))
	}

	// Prune: importance < 0.3 older than now+1s.
	cutoff := time.Now().UTC().Add(time.Second)
	n, err := st.Squads.PruneMemory(ctx, "hud-frontend", 0.3, cutoff)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("prune count: got %d want 1 (only the 0.2 followup row)", n)
	}
}

func TestSquadOutcomes_SuccessRate(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.Squads.PutSquad(ctx, &Squad{Name: "hud-frontend"}); err != nil {
		t.Fatalf("seed squad: %v", err)
	}
	// Need a backlog item + pipeline run for the FK; v1 helpers handle that.
	council := "COUNCIL-2026-05-02"
	if err := st.Council.Put(ctx, &CouncilRun{
		ID: council, Trigger: CouncilTriggerCron,
		StartedAt: time.Now().UTC(), Outcome: CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed council: %v", err)
	}
	for i, kind := range []SquadOutcomeKind{
		SquadOutcomeMergedClean, SquadOutcomeMergedClean, SquadOutcomeMergedClean,
		SquadOutcomeFailed, SquadOutcomeMergedClean, SquadOutcomeMergedRegressed,
	} {
		runID := nextRunID(t, i)
		if err := seedPipelineRun(ctx, st, runID, "MILLS-2026-05-02-001", i+1); err != nil {
			t.Fatalf("seed pipeline run %d: %v", i, err)
		}
		out := &SquadOutcome{
			SquadName:       "hud-frontend",
			PathClass:       "internal/hud/frontend/**",
			PipelineRunID:   runID,
			Outcome:         kind,
			CostUSD:         0.42,
			DurationSeconds: 600,
		}
		if err := st.Squads.RecordOutcome(ctx, out); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	rate, n, err := st.Squads.SuccessRate(ctx, "hud-frontend", "internal/hud/frontend/**", 30)
	if err != nil {
		t.Fatalf("success rate: %v", err)
	}
	if n != 6 {
		t.Errorf("sample size: got %d want 6", n)
	}
	// 4 clean / 6 total = 0.6667.
	if rate < 0.66 || rate > 0.67 {
		t.Errorf("success rate: got %v want ~0.667", rate)
	}

	// Flip the last clean outcome to regressed via UpdateOutcome.
	if err := st.Squads.UpdateOutcome(ctx, "PIPE-0", SquadOutcomeMergedRegressed); err != nil {
		t.Fatalf("update: %v", err)
	}
	rate2, _, err := st.Squads.SuccessRate(ctx, "hud-frontend", "internal/hud/frontend/**", 30)
	if err != nil {
		t.Fatalf("re-rate: %v", err)
	}
	if rate2 >= rate {
		t.Errorf("rate should drop after regression: was %v now %v", rate, rate2)
	}
}

func TestAudit_RecordAndRecall(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	for i, sc := range []float64{0.92, 0.55, 0.71} {
		f := &AuditFinding{
			SubjectKind:   AuditSubjectCouncilArtifact,
			SubjectID:     "COUNCIL-2026-05-02",
			Severity:      AuditSeverityInfo,
			RubricID:      "audit_v1",
			SurvivalScore: sc,
			Findings:      []map[string]any{{"id": "F1", "msg": "missing edge case"}},
			AuditorPool:   []map[string]any{{"backend": "flexinfer", "model": "llama-4-70b-instruct"}},
			CostUSD:       0.04,
			CreatedAt:     now.Add(time.Duration(i) * time.Second),
		}
		if err := st.Audit.RecordFinding(ctx, f); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	latest, err := st.Audit.LatestForSubject(ctx, AuditSubjectCouncilArtifact, "COUNCIL-2026-05-02")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest.SurvivalScore != 0.71 {
		t.Errorf("latest score: got %v want 0.71", latest.SurvivalScore)
	}

	all, err := st.Audit.ListForSubject(ctx, AuditSubjectCouncilArtifact, "COUNCIL-2026-05-02")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("list len: got %d want 3", len(all))
	}

	rate, n, err := st.Audit.SurvivalRate(ctx, AuditSubjectCouncilArtifact, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("survival rate: %v", err)
	}
	if n != 3 {
		t.Errorf("survival sample: got %d want 3", n)
	}
	want := (0.92 + 0.55 + 0.71) / 3.0
	if rate < want-0.01 || rate > want+0.01 {
		t.Errorf("survival rate: got %v want %v", rate, want)
	}

	// Validation: out-of-range score rejected.
	if err := st.Audit.RecordFinding(ctx, &AuditFinding{
		SubjectKind: AuditSubjectCouncilArtifact, SubjectID: "x",
		Severity: AuditSeverityInfo, RubricID: "audit_v1", SurvivalScore: 1.5,
	}); err == nil {
		t.Error("expected validation error for SurvivalScore > 1")
	}
}

func TestCrossRepo_Lifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	council := "COUNCIL-2026-05-02"
	if err := st.Council.Put(ctx, &CouncilRun{
		ID: council, Trigger: CouncilTriggerCron,
		StartedAt: time.Now().UTC(), Outcome: CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed council: %v", err)
	}
	if err := st.Backlog.Put(ctx, &BacklogItem{
		ID: "MILLS-2026-05-02-001", Title: "Cross-repo test", State: BacklogQueued,
		Priority: P2, CreatedBy: "council", CouncilRunID: &council,
	}); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}

	mr1, mr2 := int64(101), int64(202)
	r := &CrossRepoRun{
		ID:            "XR-2026-05-02-001",
		BacklogItemID: "MILLS-2026-05-02-001",
		Repos: []CrossRepoRepoEntry{
			{ProjectID: 47, RepoName: "loom-core", Branch: "feat/x-loom-core", MRIID: &mr1, CIStatus: "success", GateStatus: "pass"},
			{ProjectID: 51, RepoName: "loom", Branch: "feat/x-loom-vscode", MRIID: &mr2, CIStatus: "running"},
		},
		State: CrossRepoOpen,
	}
	if err := st.CrossRepo.PutRun(ctx, r); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := st.CrossRepo.GetRun(ctx, r.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AtomicityStrategy != "all_or_revert" {
		t.Errorf("atomicity default: %q", got.AtomicityStrategy)
	}
	if len(got.Repos) != 2 || got.Repos[1].RepoName != "loom" {
		t.Errorf("repos round-trip: %+v", got.Repos)
	}
	if got.Repos[0].MRIID == nil || *got.Repos[0].MRIID != 101 {
		t.Errorf("mr_iid round-trip: %+v", got.Repos[0])
	}

	// Lifecycle transitions.
	for _, st2 := range []CrossRepoState{CrossRepoGatesGreen, CrossRepoMerging, CrossRepoMerged} {
		if err := st.CrossRepo.SetState(ctx, r.ID, st2); err != nil {
			t.Fatalf("set %s: %v", st2, err)
		}
	}
	final, _ := st.CrossRepo.GetRun(ctx, r.ID)
	if final.State != CrossRepoMerged {
		t.Errorf("final state: %v", final.State)
	}

	// ListByBacklog.
	byBacklog, err := st.CrossRepo.ListByBacklog(ctx, "MILLS-2026-05-02-001")
	if err != nil {
		t.Fatalf("list-backlog: %v", err)
	}
	if len(byBacklog) != 1 {
		t.Errorf("list-backlog len: %d", len(byBacklog))
	}
}

func TestDebate_Transcript(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	council := "COUNCIL-2026-05-02"
	if err := st.Council.Put(ctx, &CouncilRun{
		ID: council, Trigger: CouncilTriggerIncident,
		StartedAt: time.Now().UTC(), Outcome: CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed council: %v", err)
	}

	rounds := []*CouncilDebateRound{
		{CouncilRunID: council, RoundIndex: 0, Role: DebateRoleEditorProposes, CostUSD: 0.42, Summary: "draft v0"},
		{CouncilRunID: council, RoundIndex: 1, Role: DebateRoleReviewerCritiques, CostUSD: 0.38, Summary: "ux concerns"},
		{CouncilRunID: council, RoundIndex: 1, Role: DebateRoleReviewerCritiques, CostUSD: 0.41, Summary: "a11y concerns"},
		{CouncilRunID: council, RoundIndex: 1, Role: DebateRoleModeratorDecision, CostUSD: 0.06, Summary: "not converged"},
		{CouncilRunID: council, RoundIndex: 2, Role: DebateRoleEditorRevises, CostUSD: 0.51, Summary: "draft v1",
			ArtifactDeltas: []map[string]any{{"path": ".loom/95-...", "line_range": "120-145"}},
		},
	}
	for i, r := range rounds {
		if err := st.Debate.AppendRound(ctx, r); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	got, err := st.Debate.ListByRun(ctx, council)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("list len: got %d want 5", len(got))
	}
	// Round 1 has 3 entries; verify ordering by id within the same round.
	if got[1].RoundIndex != 1 || got[2].RoundIndex != 1 || got[3].RoundIndex != 1 {
		t.Errorf("round-index ordering: %+v", got)
	}
	if got[4].Role != DebateRoleEditorRevises {
		t.Errorf("last role: %v", got[4].Role)
	}
	if len(got[4].ArtifactDeltas) != 1 {
		t.Errorf("deltas round-trip: %+v", got[4].ArtifactDeltas)
	}

	total, err := st.Debate.TotalCost(ctx, council)
	if err != nil {
		t.Fatalf("total cost: %v", err)
	}
	want := 0.42 + 0.38 + 0.41 + 0.06 + 0.51
	if total < want-0.001 || total > want+0.001 {
		t.Errorf("total cost: got %v want %v", total, want)
	}
}

func TestPolicyProposal_Lifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	p := &PolicyProposal{
		Kind:      PolicyProposalRelax,
		Target:    "gates.coverage",
		Diff:      "-required: [coverage]\n+advisory: [coverage]",
		Rationale: "Pass rate 105/106 over last 30 days; cite kpi_snapshots id 4421",
	}
	if err := st.PolicyProposals.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ID == 0 || p.State != PolicyProposalPending {
		t.Errorf("initial state: %+v", p)
	}

	pending, err := st.PolicyProposals.ListByState(ctx, PolicyProposalPending)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("pending len: %d", len(pending))
	}

	// Apply (human).
	if err := st.PolicyProposals.Apply(ctx, p.ID, PolicyProposalAppliedHuman, time.Time{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, err := st.PolicyProposals.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != PolicyProposalAppliedHuman {
		t.Errorf("applied state: %v", got.State)
	}
	if got.AppliedAt == nil {
		t.Error("AppliedAt nil after Apply")
	}

	// Re-apply pending fails (not pending anymore).
	if err := st.PolicyProposals.Apply(ctx, p.ID, PolicyProposalAppliedHuman, time.Time{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("re-apply: got %v want ErrNotFound", err)
	}

	// Revert (used for v2.1 regression-driven auto-revert).
	if err := st.PolicyProposals.Revert(ctx, p.ID); err != nil {
		t.Fatalf("revert: %v", err)
	}
	got, _ = st.PolicyProposals.Get(ctx, p.ID)
	if got.State != PolicyProposalReverted {
		t.Errorf("reverted state: %v", got.State)
	}

	// Reject path on a fresh proposal.
	q := &PolicyProposal{Kind: PolicyProposalTighten, Target: "gates.spec_conformance", Diff: "+required", Rationale: "regression spike"}
	if err := st.PolicyProposals.Create(ctx, q); err != nil {
		t.Fatalf("create q: %v", err)
	}
	if err := st.PolicyProposals.Reject(ctx, q.ID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := st.PolicyProposals.Reject(ctx, q.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("re-reject: got %v want ErrNotFound", err)
	}
}

func TestPipelineRun_RecursionFields(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	council := "COUNCIL-2026-05-02"
	if err := st.Council.Put(ctx, &CouncilRun{
		ID: council, Trigger: CouncilTriggerCron,
		StartedAt: time.Now().UTC(), Outcome: CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed council: %v", err)
	}
	if err := st.Backlog.Put(ctx, &BacklogItem{
		ID: "MILLS-2026-05-02-001", Title: "recursion test", State: BacklogQueued,
		Priority: P2, CreatedBy: "council", CouncilRunID: &council,
	}); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}

	parent := &PipelineRun{
		ID:        "PIPE-PARENT",
		BacklogID: "MILLS-2026-05-02-001",
		Template:  "mills-default-pipeline",
		State:     PipelineImplementing,
		Attempts:  1,
		StartedAt: time.Now().UTC(),
		Depth:     0, // top-level
	}
	if err := st.Pipeline.PutRun(ctx, parent); err != nil {
		t.Fatalf("put parent: %v", err)
	}

	parentID := parent.ID
	// Sub-runs share backlog_id with their parent but use their own
	// attempts ordinal so the v1 UNIQUE(backlog_id, attempts) index
	// doesn't collide. The dispatcher in pkg/mills/pipeline/recursion
	// (slice 6.1) is responsible for picking the next free attempts
	// value when it allocates a sub-run.
	child := &PipelineRun{
		ID:          "PIPE-CHILD",
		BacklogID:   "MILLS-2026-05-02-001",
		Template:    "mills-default-pipeline",
		State:       PipelineImplementing,
		Attempts:    2,
		StartedAt:   time.Now().UTC(),
		ParentRunID: &parentID,
		Depth:       1,
	}
	if err := st.Pipeline.PutRun(ctx, child); err != nil {
		t.Fatalf("put child: %v", err)
	}

	gotChild, err := st.Pipeline.GetRun(ctx, "PIPE-CHILD")
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if gotChild.Depth != 1 {
		t.Errorf("depth round-trip: got %d want 1", gotChild.Depth)
	}
	if gotChild.ParentRunID == nil || *gotChild.ParentRunID != "PIPE-PARENT" {
		t.Errorf("parent_run_id round-trip: %+v", gotChild.ParentRunID)
	}

	subruns, err := st.Pipeline.ListSubruns(ctx, "PIPE-PARENT")
	if err != nil {
		t.Fatalf("list-subruns: %v", err)
	}
	if len(subruns) != 1 || subruns[0].ID != "PIPE-CHILD" {
		t.Errorf("list-subruns: got %+v want [PIPE-CHILD]", subruns)
	}

	// Negative depth rejected at validation.
	if err := st.Pipeline.PutRun(ctx, &PipelineRun{
		ID: "PIPE-BAD", BacklogID: "MILLS-2026-05-02-001",
		Template: "mills-default-pipeline", State: PipelineQueued,
		StartedAt: time.Now().UTC(), Depth: -1,
	}); err == nil {
		t.Error("expected validation error for Depth < 0")
	}
}

// ----- helpers -----

// nextRunID returns a deterministic pipeline run id "PIPE-<i>" for the
// outcome tests above, with descending created_at so the i-th call is
// recognised as "older" by SuccessRate's LIMIT/ORDER. We bias created_at by
// negative offsets — i=0 is newest. SuccessRate's ORDER BY created_at DESC
// then LIMIT N picks all rows; what matters is identity per row.
func nextRunID(_ *testing.T, i int) string {
	return "PIPE-" + itoa(i)
}

func itoa(i int) string {
	// Tiny inline strconv.Itoa to avoid an extra import in this test file.
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// seedPipelineRun inserts a minimal pipeline_runs row for FK satisfaction.
// Council + backlog rows are upserts so repeat calls are safe. Caller passes
// a unique `attempt` because pipeline_runs has UNIQUE(backlog_id, attempts).
func seedPipelineRun(ctx context.Context, st *Store, runID, backlogID string, attempt int) error {
	council := "COUNCIL-2026-05-02"
	if err := st.Council.Put(ctx, &CouncilRun{
		ID: council, Trigger: CouncilTriggerCron,
		StartedAt: time.Now().UTC(), Outcome: CouncilOutcomeSuccess,
	}); err != nil {
		return err
	}
	if err := st.Backlog.Put(ctx, &BacklogItem{
		ID: backlogID, Title: "auto-seeded", State: BacklogQueued,
		Priority: P2, CreatedBy: "test", CouncilRunID: &council,
	}); err != nil {
		return err
	}
	if attempt <= 0 {
		attempt = 1
	}
	return st.Pipeline.PutRun(ctx, &PipelineRun{
		ID:        runID,
		BacklogID: backlogID,
		Template:  "mills-default-pipeline",
		State:     PipelineDone,
		Attempts:  attempt,
		StartedAt: time.Now().UTC(),
	})
}
