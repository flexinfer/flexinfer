package council

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/crb2nu/loom/pkg/hive/store"
)

func newMutatorEnv(t *testing.T) (*BacklogMutator, *store.Store, string) {
	t.Helper()
	st := newCouncilTestStore(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".loom"), 0o755); err != nil {
		t.Fatalf("mkdir loom: %v", err)
	}
	m := &BacklogMutator{
		Store: st,
		Now:   func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) },
	}
	// Seed every council run id the tests pass to Apply so the
	// backlog_items FK on council_run_id resolves. Runtime callers
	// already persist the council run before invoking the mutator.
	for _, id := range []string{
		"COUNCIL-X", "COUNCIL-Y", "COUNCIL-T", "COUNCIL-C", "COUNCIL-P",
		"COUNCIL-H", "COUNCIL-E", "COUNCIL-D", "COUNCIL-N", "COUNCIL-A",
	} {
		if err := st.Council.Put(context.Background(), &store.CouncilRun{
			ID: id, Trigger: store.CouncilTriggerManual,
			StartedAt: time.Now().UTC(), Outcome: store.CouncilOutcomeSuccess,
		}); err != nil {
			t.Fatalf("seed council %s: %v", id, err)
		}
	}
	return m, st, repo
}

// sampleProposalTitles supplies distinct multi-token titles for
// sampleProposals so the slice 6.2 dedup logic doesn't collapse the
// fixture (single-char disambiguators like "proposal A" / "proposal B"
// all normalize to {proposal} and would dedup against each other).
var sampleProposalTitles = []string{
	"alpha refactor pipeline starter",
	"bravo HUD panel for backlog",
	"charlie reconciler idle backoff",
	"delta gate library expansion",
	"echo eval loop attribution",
	"foxtrot integrator fan-out",
	"golf escalation path runbook",
	"hotel weaver subagent dispatch",
	"india council brief assembler",
	"juliet roadmap intent extractor",
	"kilo merge train automation",
	"lima dedup canonical store",
}

func sampleProposals(n int) []BacklogProposal {
	out := make([]BacklogProposal, n)
	for i := 0; i < n; i++ {
		title := "proposal sample " + string(rune('A'+i))
		if i < len(sampleProposalTitles) {
			title = sampleProposalTitles[i]
		}
		out[i] = BacklogProposal{
			Title:    title,
			Labels:   []string{"debt"},
			Priority: store.P2,
			Slices: []store.Slice{{
				Name:  "core",
				Files: []string{"pkg/foo/" + string(rune('a'+i)) + ".go"},
			}},
			Success: store.SuccessCriteria{Tests: []string{"go test ./pkg/foo/..."}},
			Budget:  store.Budget{MaxCostUSD: 1.5},
		}
	}
	return out
}

// ----- happy path -----

func TestApply_PersistsAndExports(t *testing.T) {
	m, st, repo := newMutatorEnv(t)

	out := &EditorOutput{BacklogProposals: sampleProposals(3)}
	res, err := m.Apply(context.Background(), "COUNCIL-X", out, MutationOptions{RepoRoot: repo})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Skipped {
		t.Fatalf("unexpected skip: %s", res.SkipReason)
	}
	if len(res.CreatedItems) != 3 {
		t.Errorf("created: got %d want 3", len(res.CreatedItems))
	}
	if len(res.CreatedYAMLPath) != 3 {
		t.Errorf("yaml exports: got %d want 3", len(res.CreatedYAMLPath))
	}

	// Canonical store reflects the writes.
	q, _ := st.Backlog.ListByState(context.Background(), store.BacklogQueued)
	if len(q) != 3 {
		t.Errorf("queue: got %d want 3", len(q))
	}

	// Auto-IDs follow the HIVE-YYYY-MM-DD-NNN convention.
	for i, item := range res.CreatedItems {
		want := "HIVE-2026-04-26-00" + string(rune('1'+i))
		if item.ID != want {
			t.Errorf("id[%d]: got %q want %q", i, item.ID, want)
		}
		if item.CouncilRunID == nil || *item.CouncilRunID != "COUNCIL-X" {
			t.Errorf("council ref: %v", item.CouncilRunID)
		}
		if item.CreatedBy != "council" {
			t.Errorf("created_by: %s", item.CreatedBy)
		}
	}

	// YAML exports are valid + carry the right fields.
	for _, rel := range res.CreatedYAMLPath {
		body, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatalf("read yaml: %v", err)
		}
		var bk backlogYAML
		if err := yaml.Unmarshal(body, &bk); err != nil {
			t.Fatalf("yaml decode %s: %v", rel, err)
		}
		if bk.State != "queued" {
			t.Errorf("yaml state: %s", bk.State)
		}
		if bk.CouncilRunID != "COUNCIL-X" {
			t.Errorf("yaml council ref: %s", bk.CouncilRunID)
		}
	}
}

func TestApply_SummaryAndCreatedIDs(t *testing.T) {
	m, _, _ := newMutatorEnv(t)
	out := &EditorOutput{BacklogProposals: sampleProposals(2)}
	res, _ := m.Apply(context.Background(), "COUNCIL-Y", out, MutationOptions{})

	ids := res.CreatedIDs()
	if len(ids) != 2 {
		t.Errorf("CreatedIDs: %v", ids)
	}
	if !strings.Contains(res.Summary(), "created=2") {
		t.Errorf("summary missing created count: %s", res.Summary())
	}
}

// ----- caps + truncation -----

func TestApply_DefaultCapTruncates(t *testing.T) {
	m, _, _ := newMutatorEnv(t)
	out := &EditorOutput{BacklogProposals: sampleProposals(15)}
	res, err := m.Apply(context.Background(), "COUNCIL-T", out, MutationOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(res.CreatedItems) != defaultMaxNewItems {
		t.Errorf("expected %d created (default cap), got %d",
			defaultMaxNewItems, len(res.CreatedItems))
	}
	if res.Truncated != 5 {
		t.Errorf("truncated: %d", res.Truncated)
	}
}

func TestApply_CustomCapTruncates(t *testing.T) {
	m, _, _ := newMutatorEnv(t)
	out := &EditorOutput{BacklogProposals: sampleProposals(8)}
	res, _ := m.Apply(context.Background(), "COUNCIL-C", out, MutationOptions{MaxNewItems: 3})
	if len(res.CreatedItems) != 3 {
		t.Errorf("custom cap not applied: %d", len(res.CreatedItems))
	}
	if res.Truncated != 5 {
		t.Errorf("truncated count: %d", res.Truncated)
	}
}

// ----- partial-skip path -----

func TestApply_SkipsWhenPartial(t *testing.T) {
	m, st, _ := newMutatorEnv(t)
	out := &EditorOutput{BacklogProposals: sampleProposals(3)}
	res, _ := m.Apply(context.Background(), "COUNCIL-P", out, MutationOptions{
		SkipBecausePartial: true,
	})
	if !res.Skipped {
		t.Errorf("expected skip")
	}
	if res.TotalProposed != 3 {
		t.Errorf("TotalProposed should be populated for audit: %d", res.TotalProposed)
	}
	if len(res.CreatedItems) != 0 {
		t.Errorf("nothing should have been persisted: %d", len(res.CreatedItems))
	}
	q, _ := st.Backlog.ListByState(context.Background(), store.BacklogQueued)
	if len(q) != 0 {
		t.Errorf("store should be empty after skip: %d", len(q))
	}
	if !strings.Contains(res.Summary(), "below eval threshold") {
		t.Errorf("summary missing skip reason: %s", res.Summary())
	}
}

// ----- IDHint determinism -----

func TestApply_HonorsIDHint(t *testing.T) {
	m, _, _ := newMutatorEnv(t)
	out := &EditorOutput{BacklogProposals: []BacklogProposal{{
		IDHint:   "HIVE-CUSTOM-001",
		Title:    "named",
		Priority: store.P1,
	}}}
	res, err := m.Apply(context.Background(), "COUNCIL-H", out, MutationOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.CreatedItems[0].ID != "HIVE-CUSTOM-001" {
		t.Errorf("id hint ignored: %s", res.CreatedItems[0].ID)
	}
}

// ----- guards + validation -----

func TestApply_RejectsEmptyTitle(t *testing.T) {
	m, _, _ := newMutatorEnv(t)
	out := &EditorOutput{BacklogProposals: []BacklogProposal{{Title: ""}}}
	if _, err := m.Apply(context.Background(), "COUNCIL-E", out, MutationOptions{}); err == nil {
		t.Error("expected error for empty title")
	}
}

func TestApply_NoConfigErrors(t *testing.T) {
	if _, err := (&BacklogMutator{}).Apply(context.Background(), "X",
		&EditorOutput{}, MutationOptions{}); err == nil {
		t.Error("expected error with nil store")
	}
}

func TestApply_NoOutputErrors(t *testing.T) {
	m, _, _ := newMutatorEnv(t)
	if _, err := m.Apply(context.Background(), "X", nil, MutationOptions{}); err == nil {
		t.Error("expected error with nil EditorOutput")
	}
}

func TestApply_NoRunIDErrors(t *testing.T) {
	m, _, _ := newMutatorEnv(t)
	if _, err := m.Apply(context.Background(), "", &EditorOutput{}, MutationOptions{}); err == nil {
		t.Error("expected error with empty runID")
	}
}

func TestApply_DefaultPriority(t *testing.T) {
	m, _, _ := newMutatorEnv(t)
	out := &EditorOutput{BacklogProposals: []BacklogProposal{{Title: "no prio set"}}}
	res, err := m.Apply(context.Background(), "COUNCIL-D", out, MutationOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.CreatedItems[0].Priority != store.P2 {
		t.Errorf("default priority should be P2, got %v", res.CreatedItems[0].Priority)
	}
}

// ----- YAML export -----

func TestApply_NoRepoRootSkipsYAML(t *testing.T) {
	m, _, _ := newMutatorEnv(t)
	out := &EditorOutput{BacklogProposals: sampleProposals(2)}
	res, err := m.Apply(context.Background(), "COUNCIL-N", out, MutationOptions{}) // no RepoRoot
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(res.CreatedItems) != 2 {
		t.Errorf("created: %d", len(res.CreatedItems))
	}
	if len(res.CreatedYAMLPath) != 0 {
		t.Errorf("YAML exports should be empty when RepoRoot unset: %v", res.CreatedYAMLPath)
	}
}

func TestApply_YAMLExportIsAtomic(t *testing.T) {
	m, _, repo := newMutatorEnv(t)
	out := &EditorOutput{BacklogProposals: sampleProposals(1)}
	if _, err := m.Apply(context.Background(), "COUNCIL-A", out, MutationOptions{RepoRoot: repo}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(repo, ".loom", "backlog"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") || strings.HasPrefix(e.Name(), ".council-") {
			t.Errorf("tempfile leaked into backlog dir: %s", e.Name())
		}
	}
}

// ----- slice 6.2 — canonical-store dedup -----

// TestApply_DedupSameBriefRunTwice is the acceptance test from the spec:
// running the council twice with the same brief produces 0 new items on
// the second run.
func TestApply_DedupSameBriefRunTwice(t *testing.T) {
	m, st, _ := newMutatorEnv(t)
	// Use realistic multi-token titles so within-batch dedup doesn't
	// confound the across-batch acceptance check.
	out := &EditorOutput{BacklogProposals: []BacklogProposal{
		{Title: "Add HUD panel for spawn budgets", Priority: store.P1, Budget: store.Budget{MaxCostUSD: 1}},
		{Title: "Reconciler idle backoff throttle", Priority: store.P2, Budget: store.Budget{MaxCostUSD: 1}},
		{Title: "Council backlog dedup with Jaccard", Priority: store.P2, Budget: store.Budget{MaxCostUSD: 1}},
	}}

	first, err := m.Apply(context.Background(), "COUNCIL-D", out, MutationOptions{})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if len(first.CreatedItems) != 3 {
		t.Fatalf("first run: created=%d want 3", len(first.CreatedItems))
	}
	if len(first.DuplicatesSkipped) != 0 {
		t.Fatalf("first run: dedup_skipped=%d want 0", len(first.DuplicatesSkipped))
	}

	// Second run with the same proposals — every one should dedup.
	second, err := m.Apply(context.Background(), "COUNCIL-E", out, MutationOptions{})
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(second.CreatedItems) != 0 {
		t.Errorf("second run: created=%d want 0", len(second.CreatedItems))
	}
	if len(second.DuplicatesSkipped) != 3 {
		t.Errorf("second run: dedup_skipped=%d want 3", len(second.DuplicatesSkipped))
	}
	for _, d := range second.DuplicatesSkipped {
		if d.JaccardScore < 0.99 {
			t.Errorf("identical title: jaccard=%v want ~1", d.JaccardScore)
		}
	}

	// Canonical store still holds exactly the first three items.
	all, _ := st.Backlog.List(context.Background())
	if len(all) != 3 {
		t.Errorf("backlog total after second run: got %d want 3", len(all))
	}
}

// TestApply_DedupWithinBatch ensures two near-identical proposals in the
// SAME Apply call dedup against each other (the second one should drop).
func TestApply_DedupWithinBatch(t *testing.T) {
	m, _, _ := newMutatorEnv(t)
	out := &EditorOutput{BacklogProposals: []BacklogProposal{
		{Title: "Add HUD panel for spawn budgets", Priority: store.P1, Budget: store.Budget{MaxCostUSD: 1}},
		{Title: "Add HUD panel for the spawn budgets", Priority: store.P1, Budget: store.Budget{MaxCostUSD: 1}}, // near-identical (stopwords stripped)
		{Title: "Reconciler idle backoff", Priority: store.P2, Budget: store.Budget{MaxCostUSD: 1}},
	}}
	res, err := m.Apply(context.Background(), "COUNCIL-N", out, MutationOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(res.CreatedItems) != 2 {
		t.Errorf("created=%d want 2", len(res.CreatedItems))
	}
	if len(res.DuplicatesSkipped) != 1 {
		t.Errorf("dedup_skipped=%d want 1", len(res.DuplicatesSkipped))
	}
	if got := res.DuplicatesSkipped[0].ProposalIndex; got != 1 {
		t.Errorf("skipped index: got %d want 1", got)
	}
}

// TestApply_DedupRespectsThreshold proves a low threshold catches loose
// matches and a threshold > 1 disables dedup entirely.
func TestApply_DedupRespectsThreshold(t *testing.T) {
	m, _, _ := newMutatorEnv(t)
	loose := []BacklogProposal{
		{Title: "Refactor pipeline starter", Priority: store.P2, Budget: store.Budget{MaxCostUSD: 1}},
		{Title: "Refactor pipeline runner", Priority: store.P2, Budget: store.Budget{MaxCostUSD: 1}},
	}
	// At a low threshold these two share enough tokens to dedup.
	res, err := m.Apply(context.Background(), "COUNCIL-T", &EditorOutput{BacklogProposals: loose},
		MutationOptions{DedupSimilarityThreshold: 0.3})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(res.CreatedItems) != 1 || len(res.DuplicatesSkipped) != 1 {
		t.Errorf("low threshold: created=%d skipped=%d want 1/1", len(res.CreatedItems), len(res.DuplicatesSkipped))
	}

	// Reset env: at a threshold > 1 dedup is effectively disabled and
	// both proposals land.
	m2, _, _ := newMutatorEnv(t)
	res2, err := m2.Apply(context.Background(), "COUNCIL-C", &EditorOutput{BacklogProposals: loose},
		MutationOptions{DedupSimilarityThreshold: 1.5})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(res2.CreatedItems) != 2 || len(res2.DuplicatesSkipped) != 0 {
		t.Errorf("disabled threshold: created=%d skipped=%d want 2/0", len(res2.CreatedItems), len(res2.DuplicatesSkipped))
	}
}

func TestNormalizeTitleTokens(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"Add HUD panel", []string{"add", "hud", "panel"}},
		{"add the new HUD panel", []string{"add", "new", "hud", "panel"}}, // "the" dropped
		{"a b c d", nil}, // tokens shorter than 2 dropped
		{"Reconciler-idle/backoff!", []string{"reconciler", "idle", "backoff"}},
		{"foo foo bar", []string{"foo", "bar"}}, // dedupe within title
	}
	for _, tc := range cases {
		got := normalizeTitleTokens(tc.in)
		if !equalStrings(got, tc.want) {
			t.Errorf("normalize(%q) = %v want %v", tc.in, got, tc.want)
		}
	}
}

func TestJaccard(t *testing.T) {
	cases := []struct {
		name  string
		a, b  []string
		want  float64
		delta float64
	}{
		{"identical", []string{"a", "b", "c"}, []string{"a", "b", "c"}, 1.0, 0.001},
		{"disjoint", []string{"a", "b"}, []string{"c", "d"}, 0.0, 0.001},
		{"half overlap", []string{"a", "b"}, []string{"a", "c"}, 1.0 / 3.0, 0.001}, // |∩|=1, |∪|=3
		{"empty A", nil, []string{"a"}, 0, 0.001},
		{"empty B", []string{"a"}, nil, 0, 0.001},
	}
	for _, tc := range cases {
		got := jaccard(tc.a, tc.b)
		if abs(got-tc.want) > tc.delta {
			t.Errorf("%s: jaccard(%v,%v)=%v want %v", tc.name, tc.a, tc.b, got, tc.want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
