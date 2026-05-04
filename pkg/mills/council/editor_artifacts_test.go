package council

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// ----- FakeEditor -----

func TestFakeEditor_ProducesThreeDocs(t *testing.T) {
	ed := &FakeEditor{Backend: "claude-code", Model: "claude-opus-4-7", CostUSD: 0.42, Notes: "fake run"}
	out, err := ed.Edit(context.Background(), newFakeBrief(), []ReviewerOutput{
		{Lens: ReviewerLens{Name: "security", Model: "codex", Backend: "codex"}, Markdown: "looked at auth"},
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if len(out.Documents) != 3 {
		t.Errorf("expected 3 docs, got %d", len(out.Documents))
	}
	kinds := make(map[ArtifactKind]bool)
	for _, d := range out.Documents {
		kinds[d.Kind] = true
		if !strings.Contains(d.Body, "looked at auth") {
			t.Errorf("doc %s missing reviewer note", d.Kind)
		}
	}
	for _, want := range []ArtifactKind{KindResearch, KindProductSpec, KindImplementation} {
		if !kinds[want] {
			t.Errorf("missing kind %s", want)
		}
	}
	if out.Sidecar.Notes != "fake run" {
		t.Errorf("sidecar notes: %q", out.Sidecar.Notes)
	}
	if out.Sidecar.CostUSD.Frontier != 0.42 {
		t.Errorf("sidecar cost: %v", out.Sidecar.CostUSD)
	}
}

func TestFakeEditor_RejectsNilBrief(t *testing.T) {
	if _, err := (&FakeEditor{}).Edit(context.Background(), nil, nil); err == nil {
		t.Error("expected error on nil brief")
	}
}

func TestFakeEditor_RespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&FakeEditor{}).Edit(ctx, newFakeBrief(), nil); err == nil {
		t.Error("expected error from cancelled context")
	}
}

// ----- ArtifactKind -----

func TestArtifactKind_FilenameFragment(t *testing.T) {
	cases := map[ArtifactKind]string{
		KindResearch:           "research",
		KindProductSpec:        "product-spec",
		KindImplementation:     "implementation-plan",
		ArtifactKind("custom"): "custom",
	}
	for k, want := range cases {
		if got := k.FilenameFragment(); got != want {
			t.Errorf("%s: got %q want %q", k, got, want)
		}
	}
}

// ----- ArtifactWriter -----

func newWriterEnv(t *testing.T) (*ArtifactWriter, string) {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".loom"), 0o755); err != nil {
		t.Fatalf("mkdir loom: %v", err)
	}
	w := &ArtifactWriter{
		RepoRoot: repo,
		Now:      func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) },
	}
	return w, repo
}

func TestArtifactWriter_WritesDocsAndSidecar(t *testing.T) {
	w, repo := newWriterEnv(t)
	ed := &FakeEditor{Backend: "claude-code", Model: "claude-opus-4-7", CostUSD: 1.5, Notes: "n"}
	out, err := ed.Edit(context.Background(), newFakeBrief(), nil)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}

	res, err := w.Write(context.Background(), "COUNCIL-T", out)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(res.ArtifactRefs) != 3 {
		t.Errorf("expected 3 refs, got %d", len(res.ArtifactRefs))
	}
	if res.SidecarPath == "" {
		t.Errorf("sidecar path empty")
	}
	// All four expected files must exist on disk.
	for _, ref := range res.ArtifactRefs {
		if _, err := os.Stat(filepath.Join(repo, ref.Path)); err != nil {
			t.Errorf("missing file %s: %v", ref.Path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, res.SidecarPath)); err != nil {
		t.Errorf("sidecar missing: %v", err)
	}
	if res.Run == nil {
		t.Fatal("Write returned nil CouncilRun")
	}
	if res.Run.ID != "COUNCIL-T" {
		t.Errorf("run id: %q", res.Run.ID)
	}
	if res.Run.CostFrontierUSD != 1.5 {
		t.Errorf("frontier cost: %v", res.Run.CostFrontierUSD)
	}
	if res.Run.Outcome != store.CouncilOutcomeSuccess {
		t.Errorf("outcome: %v", res.Run.Outcome)
	}
}

func TestArtifactWriter_SidecarIsValidJSON(t *testing.T) {
	w, repo := newWriterEnv(t)
	out, _ := (&FakeEditor{Model: "m"}).Edit(context.Background(), newFakeBrief(), nil)
	res, err := w.Write(context.Background(), "COUNCIL-J", out)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, res.SidecarPath))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var sc Sidecar
	if err := json.Unmarshal(body, &sc); err != nil {
		t.Fatalf("sidecar not valid JSON: %v\n%s", err, body)
	}
	if sc.CouncilRunID != "COUNCIL-J" {
		t.Errorf("sidecar council_run_id: %q", sc.CouncilRunID)
	}
	if len(sc.Artifacts) != 3 {
		t.Errorf("sidecar artifacts: %d", len(sc.Artifacts))
	}
	if sc.EndedAt == nil {
		t.Errorf("sidecar ended_at unset")
	}
}

func TestArtifactWriter_NextFreeIndexAvoidsCollisions(t *testing.T) {
	w, repo := newWriterEnv(t)
	// Pre-seed an index that's already taken.
	for _, name := range []string{"95-research-prior.md", "97-other.md"} {
		if err := os.WriteFile(filepath.Join(repo, ".loom", name), []byte("seed"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	out, _ := (&FakeEditor{Model: "m"}).Edit(context.Background(), newFakeBrief(), nil)
	res, err := w.Write(context.Background(), "COUNCIL-NX", out)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	// First doc must be at index 98 (highest seen=97 → +1).
	if !strings.HasPrefix(filepath.Base(res.ArtifactRefs[0].Path), "98-") {
		t.Errorf("first doc not at next-free index 98: %s", res.ArtifactRefs[0].Path)
	}
	if res.NextFreeIndex != 98 {
		t.Errorf("NextFreeIndex: got %d want 98", res.NextFreeIndex)
	}
}

func TestArtifactWriter_FreshRepoStartsAt90(t *testing.T) {
	w, _ := newWriterEnv(t)
	out, _ := (&FakeEditor{Model: "m"}).Edit(context.Background(), newFakeBrief(), nil)
	res, err := w.Write(context.Background(), "COUNCIL-FRESH", out)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if res.NextFreeIndex != 90 {
		t.Errorf("NextFreeIndex: got %d want 90", res.NextFreeIndex)
	}
}

func TestArtifactWriter_RejectsMissingLoomDir(t *testing.T) {
	w := &ArtifactWriter{RepoRoot: t.TempDir()} // no .loom subdir
	out, _ := (&FakeEditor{Model: "m"}).Edit(context.Background(), newFakeBrief(), nil)
	if _, err := w.Write(context.Background(), "X", out); err == nil {
		t.Error("expected error when .loom dir missing")
	}
}

func TestArtifactWriter_RejectsEmptyRunID(t *testing.T) {
	w, _ := newWriterEnv(t)
	out, _ := (&FakeEditor{Model: "m"}).Edit(context.Background(), newFakeBrief(), nil)
	if _, err := w.Write(context.Background(), "", out); err == nil {
		t.Error("expected error on empty runID")
	}
}

func TestArtifactWriter_AtomicWriteSurvivesConcurrentReader(t *testing.T) {
	// We can't fully test the watcher race here, but we can sanity-check
	// that the writer's tempfile pattern means no .tmp or empty-body
	// file is left behind on disk.
	w, repo := newWriterEnv(t)
	out, _ := (&FakeEditor{Model: "m"}).Edit(context.Background(), newFakeBrief(), nil)
	if _, err := w.Write(context.Background(), "COUNCIL-A", out); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(repo, ".loom"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") || strings.HasPrefix(e.Name(), ".council-") {
			t.Errorf("tempfile leaked into .loom: %s", e.Name())
		}
	}
}

// ----- Sidecar -----

func TestSidecar_MarshalIsStable(t *testing.T) {
	end := time.Date(2026, 4, 26, 12, 1, 0, 0, time.UTC)
	sc := Sidecar{
		CouncilRunID:  "COUNCIL-X",
		Models:        []string{"a", "b"},
		StartedAt:     time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		EndedAt:       &end,
		CostUSD:       SidecarCost{Frontier: 1.5, Local: 0.1},
		Artifacts:     []store.ArtifactRef{{Kind: "research", Path: ".loom/90-research-X.md"}},
		BacklogDeltas: SidecarBacklog{Created: 1},
		Notes:         "stable",
	}
	a, err := sc.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b, _ := sc.Marshal()
	if string(a) != string(b) {
		t.Errorf("Marshal not deterministic")
	}
	// Indented JSON ends with no trailing newline; spot-check for key
	// presence rather than full equality so a future field add doesn't
	// reverse-break the test.
	for _, want := range []string{
		`"council_run_id": "COUNCIL-X"`,
		`"frontier": 1.5`,
		`"local": 0.1`,
		`".loom/90-research-X.md"`,
	} {
		if !strings.Contains(string(a), want) {
			t.Errorf("sidecar missing %q\n%s", want, a)
		}
	}
}
