package runner

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills"
	"github.com/crb2nu/loom/pkg/mills/council"
	"github.com/crb2nu/loom/pkg/mills/eval"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// runnerEnv wires every council component with deterministic fakes so
// the test exercises the same flow the operator's live wiring does.
type runnerEnv struct {
	store    *store.Store
	policy   *mills.PolicyManager
	repoRoot string
	now      time.Time
	runner   *Runner
}

const validPolicyYAML = `
version: 1
budgets:
  council:  { max_usd_per_run: 5, max_usd_per_day: 50 }
  pipeline: { max_usd_per_run: 5, max_usd_per_day: 50 }
council:
  schedule_cron: "0 5 * * *"
  artifacts_branch: "council/{date}"
  artifacts_merge_strategy: "fast-merge-loom-only"
  ensemble:
    editor: { model: claude-opus, backend: claude-code }
    reviewers:
      - { name: security,  model: gpt-5-codex, backend: codex }
      - { name: tech-debt, model: qwen3.5-9b,  backend: flexinfer }
pipeline:
  default_template: mills-default-pipeline
  retry: { max_attempts: 3, cooldown_seconds: 60 }
human_handoff:
  on_escalation_create_handoff: true
  on_escalation_create_issue: true
`

// runnerSampleTitles supplies distinct multi-token titles so the slice
// 6.2 dedup logic doesn't collapse the fixture (single-char
// disambiguators all normalize to the same token set after stopwords +
// single-char drops).
var runnerSampleTitles = []string{
	"exercise reconciler pipeline starter",
	"exercise gate library expansion",
	"exercise eval loop attribution",
	"exercise integrator fan-out",
	"exercise escalation runbook",
	"exercise weaver subagent dispatch",
}

func sampleProposals(n int) []council.BacklogProposal {
	out := make([]council.BacklogProposal, n)
	for i := 0; i < n; i++ {
		title := "exercise sample council " + string(rune('A'+i))
		if i < len(runnerSampleTitles) {
			title = runnerSampleTitles[i]
		}
		out[i] = council.BacklogProposal{
			Title:    title,
			Labels:   []string{"debt"},
			Priority: store.P2,
			Slices: []store.Slice{{
				Name:  "core",
				Files: []string{"pkg/foo/bar.go"},
			}},
			Success: store.SuccessCriteria{Tests: []string{"go test ./pkg/foo/..."}},
			Budget:  store.Budget{MaxCostUSD: 1.0},
		}
	}
	return out
}

func newRunnerEnv(t *testing.T, proposals []council.BacklogProposal) *runnerEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "h.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	policyPath := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(policyPath, []byte(validPolicyYAML), 0o644); err != nil {
		t.Fatalf("policy write: %v", err)
	}
	pm, err := mills.NewPolicyManager(context.Background(), policyPath, mills.PolicyManagerOptions{SkipWatch: true})
	if err != nil {
		t.Fatalf("policy mgr: %v", err)
	}
	t.Cleanup(func() { _ = pm.Close() })

	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".loom"), 0o755); err != nil {
		t.Fatalf("mkdir loom: %v", err)
	}
	// Seed a roadmap intent so the eval Loop A judge's roadmap_alignment
	// criterion has something to match.
	if err := st.Roadmap.Upsert(context.Background(), &store.RoadmapIntent{
		Theme: "Tier 1", Priority: 1, Summary: "exercise the council",
		LastSeenInRoadmapSHA: "test",
	}); err != nil {
		t.Fatalf("seed roadmap: %v", err)
	}

	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)

	dispatcher := &council.Dispatcher{Reviewers: map[string]council.Reviewer{
		"security":  &council.FakeReviewer{Notes: "audit ok", CostUSD: 0.10},
		"tech-debt": &council.FakeReviewer{Notes: "lean", CostUSD: 0.05},
	}}
	editor := &council.FakeEditor{
		Backend: "claude-code", Model: "claude-opus", CostUSD: 0.42, Notes: "fake",
		BacklogCreated: len(proposals),
	}
	wrappedEditor := &editorWithProposals{base: editor, proposals: proposals}

	writer := &council.ArtifactWriter{RepoRoot: repo, Now: func() time.Time { return now }}
	mutator := &council.BacklogMutator{Store: st, Now: func() time.Time { return now }}
	judge := &eval.Judge{Criteria: eval.DefaultRubric(&eval.FakeLLMJudge{Score: 1.0})}

	r := &Runner{
		Store:     st,
		Policy:    pm,
		Budget:    mills.NewBudget(pm, mills.NewStoreBudgetReader(st)),
		Reviewers: dispatcher,
		Editor:    wrappedEditor,
		Writer:    writer,
		Mutator:   mutator,
		Judge:     judge,
		RepoRoot:  repo,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:       func() time.Time { return now },
	}
	return &runnerEnv{store: st, policy: pm, repoRoot: repo, now: now, runner: r}
}

// editorWithProposals decorates a base Editor so each Edit call adds the
// caller-supplied BacklogProposals to the output. Keeps proposal
// generation out of council.FakeEditor's contract.
type editorWithProposals struct {
	base      council.Editor
	proposals []council.BacklogProposal
}

func (e *editorWithProposals) Edit(ctx context.Context, brief *council.Brief, reviews []council.ReviewerOutput) (*council.EditorOutput, error) {
	out, err := e.base.Edit(ctx, brief, reviews)
	if err != nil {
		return nil, err
	}
	out.BacklogProposals = append(out.BacklogProposals, e.proposals...)
	return out, nil
}

// Revise delegates to the wrapped editor when it implements Reviser so
// the debate-mode runner test can drive the same fixture as the
// single-pass tests. Adds the same canned proposals onto the revised
// output (matching Edit's behaviour), so the mutator stage downstream
// sees a non-empty BacklogProposals on the final revise.
func (e *editorWithProposals) Revise(ctx context.Context, prior *council.EditorOutput, critiques []council.ReviewerOutput, focusAreas []string) (*council.EditorOutput, error) {
	rv, ok := e.base.(council.Reviser)
	if !ok {
		// Fall back to a propose-only impl: reuse Edit so tests
		// covering not-Reviser editors still resolve.
		return e.base.Edit(ctx, &council.Brief{Markdown: "(revise)"}, critiques)
	}
	out, err := rv.Revise(ctx, prior, critiques, focusAreas)
	if err != nil {
		return nil, err
	}
	out.BacklogProposals = append(out.BacklogProposals, e.proposals...)
	return out, nil
}

// ----- happy path -----

func TestRun_HappyPathPersistsEverything(t *testing.T) {
	env := newRunnerEnv(t, sampleProposals(2))
	res, err := env.runner.Run(context.Background(), RunInput{
		Trigger: store.CouncilTriggerManual,
		Reason:  "test",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Verdict.Partial {
		t.Errorf("happy path should pass judge, got verdict=%+v", res.Verdict)
	}
	if len(res.Mutation.CreatedItems) != 2 {
		t.Errorf("created items: got %d want 2", len(res.Mutation.CreatedItems))
	}
	if len(res.Write.ArtifactRefs) != 3 {
		t.Errorf("artifact refs: got %d want 3", len(res.Write.ArtifactRefs))
	}

	// Council run row persisted with the populated deltas.
	got, err := env.store.Council.Get(context.Background(), res.RunID)
	if err != nil {
		t.Fatalf("council get: %v", err)
	}
	if got.Outcome != store.CouncilOutcomeSuccess {
		t.Errorf("outcome: %v", got.Outcome)
	}
	if len(got.BacklogDeltas.Created) != 2 {
		t.Errorf("persisted backlog deltas: %+v", got.BacklogDeltas)
	}
	if got.Notes != "test" {
		t.Errorf("notes: %q", got.Notes)
	}

	// Eval row recorded.
	scores, _ := env.store.Eval.LatestPerSubject(context.Background(),
		store.EvalSubjectCouncilRun, res.RunID)
	if len(scores) != 1 {
		t.Errorf("expected 1 eval score, got %d", len(scores))
	}

	// Files materialised on disk.
	for _, ref := range res.Write.ArtifactRefs {
		if _, err := os.Stat(filepath.Join(env.repoRoot, ref.Path)); err != nil {
			t.Errorf("artifact missing: %s (%v)", ref.Path, err)
		}
	}
	for _, p := range res.Mutation.CreatedYAMLPath {
		if _, err := os.Stat(filepath.Join(env.repoRoot, p)); err != nil {
			t.Errorf("backlog yaml missing: %s (%v)", p, err)
		}
	}

	// Cost is reviewer cost + editor cost.
	want := 0.42 + 0.10 + 0.05
	if abs(res.CostUSDApprox-want) > 1e-6 {
		t.Errorf("CostUSDApprox: got %v want %v", res.CostUSDApprox, want)
	}
}

// ----- partial path -----

func TestRun_PartialSkipsBacklog(t *testing.T) {
	env := newRunnerEnv(t, sampleProposals(2))
	// Swap the judge for one that returns a hard 0 to force partial.
	env.runner.Judge = &eval.Judge{
		Criteria: []eval.Criterion{alwaysZeroCriterion{}},
	}
	res, err := env.runner.Run(context.Background(), RunInput{
		Trigger: store.CouncilTriggerCron,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Verdict.Partial {
		t.Errorf("expected partial verdict")
	}
	if !res.Mutation.Skipped {
		t.Errorf("mutator should have skipped, got %+v", res.Mutation)
	}
	if res.Mutation.TotalProposed != 2 {
		t.Errorf("TotalProposed should be populated for audit: %d", res.Mutation.TotalProposed)
	}

	q, _ := env.store.Backlog.ListByState(context.Background(), store.BacklogQueued)
	if len(q) != 0 {
		t.Errorf("backlog should be untouched on partial run: %d items", len(q))
	}

	// Persisted council run reflects partial.
	got, _ := env.store.Council.Get(context.Background(), res.RunID)
	if got.Outcome != store.CouncilOutcomePartial {
		t.Errorf("outcome should be partial, got %v", got.Outcome)
	}
}

// ----- dryrun path -----

func TestRun_DryrunWritesScratchDir(t *testing.T) {
	env := newRunnerEnv(t, sampleProposals(1))
	res, err := env.runner.Run(context.Background(), RunInput{
		Trigger: store.CouncilTriggerManual,
		Dryrun:  true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// Canonical store untouched: no council_runs, no backlog_items, no eval row.
	q, _ := env.store.Backlog.ListByState(context.Background(), store.BacklogQueued)
	if len(q) != 0 {
		t.Errorf("dryrun should not write backlog, got %d items", len(q))
	}
	if _, err := env.store.Council.Get(context.Background(), res.RunID); err == nil {
		t.Errorf("dryrun should not persist council_runs row")
	}

	// Scratch dir under .loom/dryrun/<runID>/ contains the artifacts.
	scratch := filepath.Join(env.repoRoot, ".loom", "dryrun", res.RunID, ".loom")
	entries, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatalf("scratch dir missing: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("scratch dir empty: %s", scratch)
	}
	mdCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			mdCount++
		}
	}
	if mdCount != 3 {
		t.Errorf("expected 3 markdown artifacts in scratch, got %d", mdCount)
	}
}

// ----- guards -----

func TestRun_PolicyDisabledRejects(t *testing.T) {
	env := newRunnerEnv(t, nil)
	off := false
	env.policy.Current().Enabled = &off
	_, err := env.runner.Run(context.Background(), RunInput{Trigger: store.CouncilTriggerManual})
	if err == nil {
		t.Errorf("expected error when policy disabled")
	}
}

func TestRun_MissingDepsErrors(t *testing.T) {
	if _, err := (&Runner{}).Run(context.Background(), RunInput{}); err == nil {
		t.Error("expected error with no config")
	}
}

// ----- Phase 5.2: debate persistence + cost rollup -----

// TestRun_DebatePersistsTranscriptAndRollsUpCost is the slice-5.2
// acceptance gate: when policy.Debate.Enabled enables the trigger AND
// a Moderator is wired, the runner (a) drives Council Debate Mode end
// to end, (b) stamps the per-round transcript onto the persisted
// council_debate_rounds table, and (c) rolls the *full* debate spend
// into council_runs.cost_frontier_usd so CouncilDAO.SumCostSince
// reflects the daily cap. Single-pass cost tracking is unchanged.
func TestRun_DebatePersistsTranscriptAndRollsUpCost(t *testing.T) {
	env := newRunnerEnv(t, sampleProposals(1))

	// Flip debate on for the manual trigger (V2-D5 default).
	pol := env.policy.Current()
	pol.Debate = mills.DebatePolicy{
		Enabled:            mills.DebateTriggers{Manual: true},
		MaxUSD:             8.0,
		MaxRounds:          3,
		EarlyExitThreshold: 0.8,
	}

	// Wire the moderator: converge after the first decision so the
	// debate emits Round 0 propose + Round 1 critique + Round 1
	// moderator_decision (converged=true) and exits without a
	// follow-up revise. That gives us 3 transcript rows under the
	// per-run MaxUSD cap.
	env.runner.Moderator = &council.FakeModerator{
		ConvergeAfterRound: 0,
		FocusAreas:         nil,
		CostUSD:            0.05,
	}

	res, err := env.runner.Run(context.Background(), RunInput{
		Trigger: store.CouncilTriggerManual,
		Reason:  "phase 5.2 persistence test",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// (1) Sidecar.Debate populated.
	if res.Editor == nil || res.Editor.Sidecar.Debate == nil {
		t.Fatalf("sidecar.debate not populated")
	}
	sb := res.Editor.Sidecar.Debate
	if !sb.Enabled {
		t.Errorf("sidecar.debate.enabled: got %v want true", sb.Enabled)
	}
	if sb.EarlyExitReason != "converged" {
		t.Errorf("early_exit_reason: got %q want %q", sb.EarlyExitReason, "converged")
	}
	if len(sb.Rounds) != 3 {
		t.Fatalf("transcript rows: got %d want 3 (R0 propose, R1 critique, R1 moderator)", len(sb.Rounds))
	}

	// (2) Persistence: every transcript row should land in
	// council_debate_rounds.
	persisted, err := env.store.Debate.ListByRun(context.Background(), res.RunID)
	if err != nil {
		t.Fatalf("list-by-run: %v", err)
	}
	if len(persisted) != 3 {
		t.Errorf("persisted rows: got %d want 3", len(persisted))
	}
	wantRoles := []store.DebateRole{
		store.DebateRoleEditorProposes,
		store.DebateRoleReviewerCritiques,
		store.DebateRoleModeratorDecision,
	}
	for i, want := range wantRoles {
		if i >= len(persisted) {
			break
		}
		if persisted[i].Role != want {
			t.Errorf("persisted[%d].Role: got %q want %q", i, persisted[i].Role, want)
		}
	}

	// (3) Cost rollup: SumCostSince via DebateDAO must equal the
	// transcript total, and the persisted council_runs row's
	// frontier cost must equal the same total.
	now := env.now
	debateSpent, err := env.store.Debate.SumCostSince(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("debate sum-since: %v", err)
	}
	if !approxEqual(debateSpent, sb.TotalCostUSD, 0.001) {
		t.Errorf("DebateDAO.SumCostSince: got %.4f want %.4f", debateSpent, sb.TotalCostUSD)
	}
	cRun, err := env.store.Council.Get(context.Background(), res.RunID)
	if err != nil {
		t.Fatalf("get council run: %v", err)
	}
	if !approxEqual(cRun.CostFrontierUSD, sb.TotalCostUSD, 0.001) {
		t.Errorf("council_runs.cost_frontier_usd: got %.4f want %.4f (full debate cost should roll up)",
			cRun.CostFrontierUSD, sb.TotalCostUSD)
	}
	if cRun.CostLocalUSD != 0 {
		t.Errorf("council_runs.cost_local_usd: got %.4f want 0 (debate Local refined in slice 5.3)", cRun.CostLocalUSD)
	}
}

// TestRun_DebateDryrunSkipsPersistence pins that the dryrun path does
// NOT write debate rounds even when debate ran successfully — the
// scratch-dir invariant of dryrun is "no canonical writes".
func TestRun_DebateDryrunSkipsPersistence(t *testing.T) {
	env := newRunnerEnv(t, nil)
	pol := env.policy.Current()
	pol.Debate = mills.DebatePolicy{
		Enabled:   mills.DebateTriggers{Manual: true},
		MaxUSD:    8.0,
		MaxRounds: 3,
	}
	env.runner.Moderator = &council.FakeModerator{ConvergeAfterRound: 0, CostUSD: 0.05}

	res, err := env.runner.Run(context.Background(), RunInput{
		Trigger: store.CouncilTriggerManual,
		Dryrun:  true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Editor == nil || res.Editor.Sidecar.Debate == nil {
		t.Fatalf("dryrun should still populate Sidecar.Debate (transcript visible to caller)")
	}
	persisted, err := env.store.Debate.ListByRun(context.Background(), res.RunID)
	if err != nil {
		t.Fatalf("list-by-run: %v", err)
	}
	if len(persisted) != 0 {
		t.Errorf("dryrun should not persist debate rounds; got %d rows", len(persisted))
	}
}

func approxEqual(a, b, eps float64) bool {
	if a == b {
		return true
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

// ----- helpers -----

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

type alwaysZeroCriterion struct{}

func (alwaysZeroCriterion) Name() string    { return "always_zero" }
func (alwaysZeroCriterion) Weight() float64 { return 1.0 }
func (alwaysZeroCriterion) Score(_ context.Context, _ eval.Input) (eval.CriterionResult, error) {
	return eval.CriterionResult{Name: "always_zero", Weight: 1.0, Score: 0}, nil
}
