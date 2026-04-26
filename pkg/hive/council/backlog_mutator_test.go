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

func sampleProposals(n int) []BacklogProposal {
	out := make([]BacklogProposal, n)
	for i := 0; i < n; i++ {
		out[i] = BacklogProposal{
			Title:    "proposal " + string(rune('A'+i)),
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
