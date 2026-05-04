package council

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/mills/store"
)

const fixtureRoadmap = `# Project Roadmap

## Current Status

This section is informational; tasks here aren't intents.

- [x] Some completed status item

## Recently Shipped

- [x] **Old work** — done.

## Tier 1: Strengthen Existing Moats (Current)

These build on shipped work.

- [x] **Finish HUD M3/M4** ([Issue](https://example/1)) ✅ Complete
- [ ] **Onboarding and docs consistency** ([Issue](https://example/2))
  - Sub-bullet describing constraint
- [ ] **Coverage to 60%** ([Issue](https://example/3))

## Tier 2: Capture Market Gaps (Next)

- [ ] **Remote MCP transport** ([Issue](https://example/4))
- [/] **In-progress thing**
- [-] **Skipped thing**

## Tier 3: Strategic Differentiation (Future)

- [ ] **Cross-repo mills**

## Ongoing Engineering Goals

- [ ] **Reduce p95 spawn latency below 5s**
`

func TestExtract_ParsesTieredOpenIntents(t *testing.T) {
	r, err := Extract(strings.NewReader(fixtureRoadmap), "sha-test")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if r.SHA != "sha-test" {
		t.Errorf("sha: %q", r.SHA)
	}

	// 3 open Tier-1 items (one closed), 3 Tier-2 items (one open `[ ]`,
	// one `[/]`, one `[-]` — all not-done by design), 1 Tier-3, 1 Ongoing.
	wantSummaries := map[string]int{
		"Onboarding and docs consistency":   1, // tier 1
		"Coverage to 60%":                   1,
		"Remote MCP transport":              2, // tier 2
		"In-progress thing":                 2,
		"Skipped thing":                     2,
		"Cross-repo mills":                  3, // tier 3
		"Reduce p95 spawn latency below 5s": 9, // ongoing
	}
	if len(r.Intents) != len(wantSummaries) {
		t.Errorf("intents: got %d want %d (intents=%+v)",
			len(r.Intents), len(wantSummaries), summaries(r.Intents))
	}
	for _, i := range r.Intents {
		want, ok := wantSummaries[i.Summary]
		if !ok {
			t.Errorf("unexpected intent: %q", i.Summary)
			continue
		}
		if i.Priority != want {
			t.Errorf("priority for %q: got %d want %d", i.Summary, i.Priority, want)
		}
		if i.LastSeenInRoadmapSHA != "sha-test" {
			t.Errorf("sha for %q: %q", i.Summary, i.LastSeenInRoadmapSHA)
		}
	}
}

func TestExtract_SkipsClosedTasks(t *testing.T) {
	r, err := Extract(strings.NewReader(fixtureRoadmap), "")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, i := range r.Intents {
		if i.Summary == "Finish HUD M3/M4" || i.Summary == "Old work" {
			t.Errorf("closed item leaked into intents: %q", i.Summary)
		}
	}
}

func TestExtract_SkipsTasksAboveFirstSection(t *testing.T) {
	r, err := Extract(strings.NewReader(`some preamble
- [ ] **stray task before any section**
## Tier 1: Real
- [ ] **kept**
`), "")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(r.Intents) != 1 || r.Intents[0].Summary != "kept" {
		t.Errorf("expected only 'kept', got %+v", summaries(r.Intents))
	}
}

func TestExtract_RealROADMAP(t *testing.T) {
	// If the canonical ROADMAP.md exists in the working tree, parsing it
	// should succeed and produce at least one intent. This is a smoke
	// test, not a contract — the file's content evolves freely.
	path := findRepoFile(t, "ROADMAP.md")
	r, err := ExtractFromFile(path, "smoke")
	if err != nil {
		t.Fatalf("extract real ROADMAP: %v", err)
	}
	if len(r.Intents) == 0 {
		t.Errorf("real ROADMAP.md produced 0 intents; the parser regressed")
	}
}

// ----- SyncToStore -----

func TestSync_UpsertIsIdempotent(t *testing.T) {
	st := newCouncilTestStore(t)
	defer func() { _ = st.Close() }()

	r, err := Extract(strings.NewReader(fixtureRoadmap), "sha-1")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	res, err := r.SyncToStore(context.Background(), st.Roadmap)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Upserted != len(r.Intents) {
		t.Errorf("upserted: got %d want %d", res.Upserted, len(r.Intents))
	}
	if res.Retired != 0 {
		t.Errorf("first run shouldn't retire anything, got %d", res.Retired)
	}

	// Second run with the same sha + same content: no row count change.
	res2, err := r.SyncToStore(context.Background(), st.Roadmap)
	if err != nil {
		t.Fatalf("sync2: %v", err)
	}
	if res2.Retired != 0 {
		t.Errorf("idempotent run retired %d rows", res2.Retired)
	}
	all, _ := st.Roadmap.List(context.Background())
	if len(all) != len(r.Intents) {
		t.Errorf("row count drifted across runs: got %d want %d", len(all), len(r.Intents))
	}
}

func TestSync_RetiresStaleIntents(t *testing.T) {
	st := newCouncilTestStore(t)
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	r1, _ := Extract(strings.NewReader(fixtureRoadmap), "sha-1")
	if _, err := r1.SyncToStore(ctx, st.Roadmap); err != nil {
		t.Fatalf("sync1: %v", err)
	}

	// Subset of the original — drops "Cross-repo mills" and replaces sha.
	trimmed := strings.Replace(fixtureRoadmap,
		`- [ ] **Cross-repo mills**`, "", 1)
	r2, _ := Extract(strings.NewReader(trimmed), "sha-2")
	res, err := r2.SyncToStore(ctx, st.Roadmap)
	if err != nil {
		t.Fatalf("sync2: %v", err)
	}
	if res.Retired != 1 {
		t.Errorf("expected 1 retired (Cross-repo mills), got %d", res.Retired)
	}
}

// ----- helpers -----

func summaries(ii []*store.RoadmapIntent) []string {
	out := make([]string, 0, len(ii))
	for _, i := range ii {
		out = append(out, i.Summary)
	}
	return out
}

// newCouncilTestStore opens a fresh SQLite store under t.TempDir.
// Lives here (not in a shared helpers file) so the council test files
// can stay self-contained.
func newCouncilTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(context.Background(), store.Options{
		Path: filepath.Join(dir, "council.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// findRepoFile mirrors pkg/mentatlab/autonomous_validator_test.go's helper
// so the council tests can locate ROADMAP.md regardless of where `go test`
// is invoked from.
func findRepoFile(t *testing.T, rel string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := wd
	for i := 0; i < 10; i++ {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find %s walking up from %s", rel, wd)
	return ""
}
