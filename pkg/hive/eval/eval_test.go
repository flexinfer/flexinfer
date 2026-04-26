package eval

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive/council"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// ----- helpers -----

func newEvalTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "e.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func goodSidecar() *council.Sidecar {
	end := time.Date(2026, 4, 26, 12, 1, 0, 0, time.UTC)
	return &council.Sidecar{
		CouncilRunID: "COUNCIL-G",
		Models:       []string{"claude", "qwen"},
		StartedAt:    time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		EndedAt:      &end,
		Artifacts: []store.ArtifactRef{
			{Kind: "research", Path: ".loom/90-research-G.md"},
		},
		BacklogDeltas: council.SidecarBacklog{Created: 1},
		CostUSD:       council.SidecarCost{Frontier: 1.0},
	}
}

// goodEditorOutput returns an EditorOutput whose implementation_plan
// satisfies plan_completeness + success_machine_check + alignment.
func goodEditorOutput() *council.EditorOutput {
	plan := `# plan

## Slice 1

files:
- pkg/foo/bar.go

tests:
- go test ./pkg/foo/...
- pytest tests/

budget:
- max_cost_usd: 2

This slice references "Onboarding polish" from the roadmap.
`
	return &council.EditorOutput{
		Documents: []council.ArtifactDoc{
			{Kind: council.KindResearch, Title: "research", Body: "ok"},
			{Kind: council.KindProductSpec, Title: "spec", Body: "ok"},
			{Kind: council.KindImplementation, Title: "plan", Body: plan},
		},
	}
}

func seedRoadmap(t *testing.T, st *store.Store, summaries ...string) {
	t.Helper()
	for _, s := range summaries {
		if err := st.Roadmap.Upsert(context.Background(), &store.RoadmapIntent{
			Theme: "Tier 1", Priority: 1, Summary: s,
			LastSeenInRoadmapSHA: "test",
		}); err != nil {
			t.Fatalf("seed roadmap: %v", err)
		}
	}
}

func newPassingInput(t *testing.T) Input {
	t.Helper()
	st := newEvalTestStore(t)
	seedRoadmap(t, st, "Onboarding polish")
	return Input{
		Sidecar:      goodSidecar(),
		EditorOutput: goodEditorOutput(),
		Store:        st,
		Now:          func() time.Time { return time.Date(2026, 4, 26, 12, 5, 0, 0, time.UTC) },
	}
}

// ----- SidecarValidity -----

func TestSidecarValidity_HappyPath(t *testing.T) {
	c := &SidecarValidity{}
	r, err := c.Score(context.Background(), Input{Sidecar: goodSidecar()})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if r.Score != 1.0 {
		t.Errorf("expected 1.0, got %v reasons=%v", r.Score, r.Reasons)
	}
}

func TestSidecarValidity_MissingFieldsDeductSlots(t *testing.T) {
	c := &SidecarValidity{}
	sc := &council.Sidecar{} // every field zero
	r, _ := c.Score(context.Background(), Input{Sidecar: sc})
	// Float drift from 1.0 - 5*(1.0/5.0) lands at ~5e-17, not exactly 0.
	// Anything below half a slot is "everything missing" for our purposes.
	if r.Score >= 0.1 {
		t.Errorf("expected ~0 with all fields missing, got %v", r.Score)
	}
	if len(r.Reasons) != 5 {
		t.Errorf("expected 5 reasons, got %d (%v)", len(r.Reasons), r.Reasons)
	}
}

func TestSidecarValidity_PartialDeduction(t *testing.T) {
	c := &SidecarValidity{}
	sc := goodSidecar()
	sc.Models = nil // drop one slot
	r, _ := c.Score(context.Background(), Input{Sidecar: sc})
	if r.Score >= 1.0 || r.Score <= 0 {
		t.Errorf("expected partial score 0<x<1, got %v", r.Score)
	}
}

// ----- SliceIndependence -----

func TestSliceIndependence_NoEditorOutputIsNeutral(t *testing.T) {
	c := &SliceIndependence{}
	r, _ := c.Score(context.Background(), Input{Sidecar: goodSidecar()})
	if r.Score != 1.0 {
		t.Errorf("expected neutral pass, got %v", r.Score)
	}
}

func TestSliceIndependence_EmptyPlanFails(t *testing.T) {
	c := &SliceIndependence{}
	out := goodEditorOutput()
	for i := range out.Documents {
		if out.Documents[i].Kind == council.KindImplementation {
			out.Documents[i].Body = ""
		}
	}
	r, _ := c.Score(context.Background(), Input{Sidecar: goodSidecar(), EditorOutput: out})
	if r.Score != 0 {
		t.Errorf("expected 0 for empty plan, got %v", r.Score)
	}
}

// ----- SuccessMachineCheck -----

func TestSuccessMachineCheck_RunnableTestsPass(t *testing.T) {
	c := &SuccessMachineCheck{}
	r, _ := c.Score(context.Background(), Input{
		Sidecar: goodSidecar(), EditorOutput: goodEditorOutput(),
	})
	if r.Score != 1.0 {
		t.Errorf("expected 1.0, got %v reasons=%v", r.Score, r.Reasons)
	}
}

func TestSuccessMachineCheck_ProseDeducts(t *testing.T) {
	c := &SuccessMachineCheck{}
	plan := `# plan

tests:
- go test ./...
- ensure logs are emitted on auth failure
- pnpm test
`
	out := &council.EditorOutput{Documents: []council.ArtifactDoc{
		{Kind: council.KindImplementation, Body: plan},
	}}
	r, _ := c.Score(context.Background(), Input{Sidecar: goodSidecar(), EditorOutput: out})
	// 2 of 3 runnable → 0.666
	if r.Score == 1.0 || r.Score == 0 {
		t.Errorf("expected fractional score, got %v", r.Score)
	}
	if len(r.Reasons) == 0 {
		t.Errorf("expected reasons listing the prose entry")
	}
}

// ----- PlanCompleteness -----

func TestPlanCompleteness_AllSectionsPresent(t *testing.T) {
	c := &PlanCompleteness{}
	r, _ := c.Score(context.Background(), Input{
		Sidecar: goodSidecar(), EditorOutput: goodEditorOutput(),
	})
	if r.Score != 1.0 {
		t.Errorf("expected 1.0, got %v reasons=%v", r.Score, r.Reasons)
	}
}

func TestPlanCompleteness_MissingPlanFails(t *testing.T) {
	c := &PlanCompleteness{}
	out := &council.EditorOutput{Documents: []council.ArtifactDoc{
		{Kind: council.KindResearch, Body: "x"},
	}}
	r, _ := c.Score(context.Background(), Input{Sidecar: goodSidecar(), EditorOutput: out})
	if r.Score != 0 {
		t.Errorf("expected 0, got %v", r.Score)
	}
}

func TestPlanCompleteness_DeductsForMissingSection(t *testing.T) {
	c := &PlanCompleteness{}
	// Plan has files: + tests: but no budget / max_ keyword anywhere.
	out := &council.EditorOutput{Documents: []council.ArtifactDoc{
		{Kind: council.KindImplementation, Body: "files: x\ntests: y\n(scope only)"},
	}}
	r, _ := c.Score(context.Background(), Input{Sidecar: goodSidecar(), EditorOutput: out})
	// 2 of 3 sections present → ~0.666
	if r.Score >= 1.0 || r.Score <= 0 {
		t.Errorf("expected partial score, got %v", r.Score)
	}
}

// ----- RoadmapAlignment -----

func TestRoadmapAlignment_HappyPath(t *testing.T) {
	in := newPassingInput(t)
	c := &RoadmapAlignment{}
	r, err := c.Score(context.Background(), in)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if r.Score != 1.0 {
		t.Errorf("expected 1.0, got %v reasons=%v", r.Score, r.Reasons)
	}
}

func TestRoadmapAlignment_NoIntentsButCreated(t *testing.T) {
	st := newEvalTestStore(t) // empty roadmap
	c := &RoadmapAlignment{}
	r, err := c.Score(context.Background(), Input{
		Sidecar: goodSidecar(), Store: st, EditorOutput: goodEditorOutput(),
	})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if r.Score != 0 {
		t.Errorf("expected 0 when intents absent + items created, got %v", r.Score)
	}
}

func TestRoadmapAlignment_NoCreatedItemsIsNeutral(t *testing.T) {
	st := newEvalTestStore(t)
	c := &RoadmapAlignment{}
	sc := goodSidecar()
	sc.BacklogDeltas.Created = 0
	r, _ := c.Score(context.Background(), Input{Sidecar: sc, Store: st})
	if r.Score != 1.0 {
		t.Errorf("expected neutral pass, got %v", r.Score)
	}
}

// ----- ContradictionFree -----

func TestContradictionFree_NoLLMSkipsCriterion(t *testing.T) {
	c := &ContradictionFree{}
	r, _ := c.Score(context.Background(), Input{Sidecar: goodSidecar()})
	if r.Score != 1.0 {
		t.Errorf("expected neutral pass without LLM, got %v", r.Score)
	}
	if len(r.Reasons) == 0 {
		t.Errorf("expected reason explaining the skip")
	}
}

func TestContradictionFree_LLMScoreApplied(t *testing.T) {
	llm := &FakeLLMJudge{Score: 0.4, Findings: []string{"slice X contradicts merged Y"}}
	c := &ContradictionFree{LLM: llm}
	r, _ := c.Score(context.Background(), Input{Sidecar: goodSidecar()})
	if r.Score != 0.4 {
		t.Errorf("expected 0.4, got %v", r.Score)
	}
	if len(r.Reasons) != 1 {
		t.Errorf("expected 1 finding, got %d", len(r.Reasons))
	}
}

func TestContradictionFree_LLMErrorPropagates(t *testing.T) {
	llm := &FakeLLMJudge{Err: errors.New("flexinfer timeout")}
	c := &ContradictionFree{LLM: llm}
	_, err := c.Score(context.Background(), Input{Sidecar: goodSidecar()})
	if err == nil {
		t.Error("expected LLM error to propagate")
	}
}

// ----- Judge -----

func TestJudge_HappyPathPasses(t *testing.T) {
	in := newPassingInput(t)
	j := &Judge{Criteria: DefaultRubric(&FakeLLMJudge{Score: 1.0})}
	v, err := j.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if v.Partial {
		t.Errorf("expected pass verdict, got Partial=true score=%v reasons=%v", v.Score, v)
	}
	if v.Score < 0.95 {
		t.Errorf("expected high score, got %v", v.Score)
	}
}

func TestJudge_PartialBelowThreshold(t *testing.T) {
	in := newPassingInput(t)
	in.Sidecar = &council.Sidecar{} // tank validity → score 0
	in.EditorOutput = nil           // tank slice/plan/success
	j := &Judge{
		Criteria:  DefaultRubric(&FakeLLMJudge{Score: 0.0}),
		Threshold: 0.7,
	}
	v, err := j.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !v.Partial {
		t.Errorf("expected Partial=true, got score=%v", v.Score)
	}
}

func TestJudge_ScoreClampedToUnitInterval(t *testing.T) {
	in := newPassingInput(t)
	in.Sidecar = goodSidecar()
	j := &Judge{Criteria: []Criterion{misbehavingCriterion{}}}
	v, _ := j.Run(context.Background(), in)
	if v.Score < 0 || v.Score > 1 {
		t.Errorf("score must be clamped to [0,1], got %v", v.Score)
	}
}

func TestJudge_PersistRecordsRow(t *testing.T) {
	in := newPassingInput(t)
	j := &Judge{Criteria: DefaultRubric(&FakeLLMJudge{Score: 1.0})}
	v, err := j.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := v.PersistTo(context.Background(), in.Store.Eval, "COUNCIL-PERSIST"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	rows, _ := in.Store.Eval.LatestPerSubject(context.Background(),
		store.EvalSubjectCouncilRun, "COUNCIL-PERSIST")
	if len(rows) != 1 {
		t.Fatalf("expected 1 score row, got %d", len(rows))
	}
	if rows[0].Score < 0.95 {
		t.Errorf("persisted score too low: %v", rows[0].Score)
	}
}

func TestJudge_RejectsEmptyCriteria(t *testing.T) {
	if _, err := (&Judge{}).Run(context.Background(), Input{Sidecar: goodSidecar()}); err == nil {
		t.Error("expected error with no criteria")
	}
}

func TestJudge_RejectsNilSidecar(t *testing.T) {
	j := &Judge{Criteria: DefaultRubric(nil)}
	if _, err := j.Run(context.Background(), Input{}); err == nil {
		t.Error("expected error when sidecar nil")
	}
}

func TestVerdictNotes_NamesWeakCriteria(t *testing.T) {
	v := &Verdict{Results: []CriterionResult{
		{Name: "a", Score: 1.0},
		{Name: "b", Score: 0.4},
		{Name: "c", Score: 0.0},
	}}
	notes := formatVerdictNotes(v)
	if !strings.Contains(notes, "b=0.40") || !strings.Contains(notes, "c=0.00") {
		t.Errorf("notes should name weak criteria: %q", notes)
	}
	if strings.Contains(notes, "a=") {
		t.Errorf("perfect criteria shouldn't appear: %q", notes)
	}
}

// ----- helpers for test-only criteria -----

type misbehavingCriterion struct{}

func (misbehavingCriterion) Name() string    { return "misbehaving" }
func (misbehavingCriterion) Weight() float64 { return 1.0 }
func (misbehavingCriterion) Score(_ context.Context, _ Input) (CriterionResult, error) {
	// Out-of-bounds score; the Judge must clamp.
	return CriterionResult{Name: "misbehaving", Weight: 1.0, Score: 99.0}, nil
}
