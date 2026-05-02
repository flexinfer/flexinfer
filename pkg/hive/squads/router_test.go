package squads

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// newRouterFixture returns a router wired against a temp store with the
// given manifest YAMLs already loaded. Callers seed squad_outcomes
// afterwards via st.Squads.RecordOutcome.
func newRouterFixture(t *testing.T, manifests map[string]string) (*Router, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range manifests {
		writeManifest(t, dir, name+".yaml", body)
	}
	st := newTestStore(t)
	loader, err := NewLoader(context.Background(), dir, st, LoaderOptions{SkipWatch: true})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	return NewRouter(loader, st), st
}

// seedOutcomes inserts N successful outcomes (and optionally M failed)
// for a squad against a path class. Pipeline runs share a single backlog
// item (auto-seeded inside the helper) but use distinct attempts ordinals
// so the v1 UNIQUE(backlog_id, attempts) index doesn't collide.
func seedOutcomes(t *testing.T, st *store.Store, squad, pathClass string, success, failed int) {
	t.Helper()
	ctx := context.Background()
	council := "COUNCIL-2026-05-02"
	if err := st.Council.Put(ctx, &store.CouncilRun{
		ID: council, Trigger: store.CouncilTriggerCron,
		StartedAt: time.Now().UTC(), Outcome: store.CouncilOutcomeSuccess,
	}); err != nil {
		t.Fatalf("seed council: %v", err)
	}
	backlogID := "HIVE-ROUTER-FIXTURE"
	if err := st.Backlog.Put(ctx, &store.BacklogItem{
		ID: backlogID, Title: "router fixture", State: store.BacklogQueued,
		Priority: store.P2, CreatedBy: "test", CouncilRunID: &council,
	}); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}
	if err := st.Squads.PutSquad(ctx, &store.Squad{Name: squad, Enabled: true}); err != nil {
		t.Fatalf("seed squad row: %v", err)
	}
	attempt := 0
	for i := 0; i < success; i++ {
		attempt++
		runID := pipelineRunID(squad, pathClass, attempt)
		if err := st.Pipeline.PutRun(ctx, &store.PipelineRun{
			ID: runID, BacklogID: backlogID, Template: "hive-default-pipeline",
			State: store.PipelineDone, Attempts: attempt, StartedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("put run %d: %v", attempt, err)
		}
		if err := st.Squads.RecordOutcome(ctx, &store.SquadOutcome{
			SquadName: squad, PathClass: pathClass, PipelineRunID: runID,
			Outcome: store.SquadOutcomeMergedClean, CostUSD: 0.42,
		}); err != nil {
			t.Fatalf("record success %d: %v", i, err)
		}
	}
	for i := 0; i < failed; i++ {
		attempt++
		runID := pipelineRunID(squad, pathClass, attempt)
		if err := st.Pipeline.PutRun(ctx, &store.PipelineRun{
			ID: runID, BacklogID: backlogID, Template: "hive-default-pipeline",
			State: store.PipelineEscalated, Attempts: attempt, StartedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("put run %d: %v", attempt, err)
		}
		if err := st.Squads.RecordOutcome(ctx, &store.SquadOutcome{
			SquadName: squad, PathClass: pathClass, PipelineRunID: runID,
			Outcome: store.SquadOutcomeFailed, CostUSD: 0.42,
		}); err != nil {
			t.Fatalf("record fail %d: %v", i, err)
		}
	}
}

func pipelineRunID(squad, pathClass string, attempt int) string {
	return "RUN-" + filepath.Base(squad) + "-" + filepath.Base(pathClass) + "-" + intStr(attempt)
}

func itemWithFiles(files ...string) *store.BacklogItem {
	return &store.BacklogItem{
		ID:    "HIVE-2026-05-02-001",
		Title: "router test",
		State: store.BacklogQueued,
		Slices: []store.Slice{{
			Name:  "main",
			Files: files,
		}},
	}
}

func TestRouter_PicksMatchingSquadWithBaselineConfidence(t *testing.T) {
	r, _ := newRouterFixture(t, map[string]string{
		"hud-frontend": validHUDFrontend,
		"gitops":       validGitops,
	})
	dec, err := r.Pick(context.Background(),
		itemWithFiles("internal/hud/frontend/src/lib/components/SpawnPanel.svelte"))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if dec.SquadName != "hud-frontend" {
		t.Errorf("squad: got %q want hud-frontend", dec.SquadName)
	}
	if dec.PathClass != "internal/hud/frontend/**" {
		t.Errorf("path_class: got %q", dec.PathClass)
	}
	if dec.SampleSize != 0 {
		t.Errorf("sample size on cold start: got %d want 0", dec.SampleSize)
	}
	if dec.Confidence != DefaultBaselineConfidence {
		t.Errorf("baseline confidence: got %v want %v", dec.Confidence, DefaultBaselineConfidence)
	}
}

func TestRouter_FallsBackWhenNoPathMatch(t *testing.T) {
	r, _ := newRouterFixture(t, map[string]string{
		"hud-frontend": validHUDFrontend,
	})
	dec, err := r.Pick(context.Background(),
		itemWithFiles("pkg/agentcontext/svc_workflow_definitions.go"))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if dec.SquadName != FallbackName {
		t.Errorf("expected fallback, got %q", dec.SquadName)
	}
	if dec.Reason == "" {
		t.Error("fallback reason empty")
	}
}

func TestRouter_FallsBackWhenLowConfidence(t *testing.T) {
	r, st := newRouterFixture(t, map[string]string{
		"hud-frontend": validHUDFrontend,
	})
	// 4 success / 6 total = 0.667 — above the 0.6 default. Tighten min to 0.7.
	seedOutcomes(t, st, "hud-frontend", "internal/hud/frontend/**", 4, 2)
	r.MinConfidence = 0.7

	dec, err := r.Pick(context.Background(),
		itemWithFiles("internal/hud/frontend/src/lib/components/SpawnPanel.svelte"))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if dec.SquadName != FallbackName {
		t.Errorf("low-conf should fall back, got %q (conf=%.3f)", dec.SquadName, dec.Confidence)
	}
}

func TestRouter_PicksByMeasuredConfidence(t *testing.T) {
	r, st := newRouterFixture(t, map[string]string{
		"hud-frontend": validHUDFrontend,
	})
	seedOutcomes(t, st, "hud-frontend", "internal/hud/frontend/**", 9, 1)

	dec, err := r.Pick(context.Background(),
		itemWithFiles("internal/hud/frontend/src/lib/components/SpawnPanel.svelte"))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if dec.SquadName != "hud-frontend" {
		t.Errorf("squad: got %q want hud-frontend", dec.SquadName)
	}
	if dec.SampleSize != 10 {
		t.Errorf("sample size: got %d want 10", dec.SampleSize)
	}
	if dec.Confidence < 0.89 || dec.Confidence > 0.91 {
		t.Errorf("confidence: got %v want ~0.90", dec.Confidence)
	}
}

func TestRouter_DisabledSquadIsIgnored(t *testing.T) {
	r, _ := newRouterFixture(t, map[string]string{
		"legacy": validDisabled,
	})
	dec, err := r.Pick(context.Background(), itemWithFiles("legacy/foo.go"))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if dec.SquadName != FallbackName {
		t.Errorf("disabled squad must not route: got %q", dec.SquadName)
	}
}

func TestRouter_TieBreakByPathSpecificity(t *testing.T) {
	// Two squads both match. Equal confidence (cold start). Longer path
	// wins by specificity.
	const broad = `
apiVersion: hive.loom.dev/v1
kind: Squad
metadata: { name: broad }
spec:
  paths: ["internal/**"]
  budget_share: 0.10
`
	const narrow = `
apiVersion: hive.loom.dev/v1
kind: Squad
metadata: { name: narrow }
spec:
  paths: ["internal/hud/frontend/**"]
  budget_share: 0.10
`
	r, _ := newRouterFixture(t, map[string]string{
		"broad":  broad,
		"narrow": narrow,
	})
	dec, err := r.Pick(context.Background(),
		itemWithFiles("internal/hud/frontend/foo.svelte"))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if dec.SquadName != "narrow" {
		t.Errorf("specificity tie-break: got %q want narrow", dec.SquadName)
	}
	if dec.PathClass != "internal/hud/frontend/**" {
		t.Errorf("path class: %q", dec.PathClass)
	}
}

// fakeGate implements squads.PolicyGate for unit tests. It returns the
// stored bool and lets tests flip the flag mid-test to mirror the
// production hot-reload path (PolicyManager.Current() returning a new
// *Policy with squads.enabled flipped).
type fakeGate struct{ enabled bool }

func (f *fakeGate) SquadsEnabled() bool { return f.enabled }

// TestRouter_PolicyGateBlocksHighConfidenceMatch proves the v2 gate
// short-circuits Pick before any path-class scoring. Even with a
// baseline-confidence match that *would* route to hud-frontend, the
// gate forces the fallback decision when policy.squads.enabled=false.
//
// This is the default-off rollout safety check from
// .loom/93-product-spec-hive-v2-…2026-05-02.md §"Policy file additions"
// (policy.squads.enabled defaults to false, flipped per Phase 8).
func TestRouter_PolicyGateBlocksHighConfidenceMatch(t *testing.T) {
	r, st := newRouterFixture(t, map[string]string{
		"hud-frontend": validHUDFrontend,
	})
	// Seed measured 95% confidence so the routing decision would
	// definitely fire if the gate weren't honored.
	seedOutcomes(t, st, "hud-frontend", "internal/hud/frontend/**", 19, 1)
	r.Policy = &fakeGate{enabled: false}

	dec, err := r.Pick(context.Background(),
		itemWithFiles("internal/hud/frontend/src/lib/components/SpawnPanel.svelte"))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if dec.SquadName != FallbackName {
		t.Errorf("policy gate ignored: got squad %q (conf=%.3f); want fallback",
			dec.SquadName, dec.Confidence)
	}
	if dec.Reason == "" {
		t.Error("gated fallback must surface a reason for HUD/log audit")
	}
}

// TestRouter_PolicyGateOnAllowsRouting is the symmetric case: with the
// gate on, routing behaves exactly as v1 did.
func TestRouter_PolicyGateOnAllowsRouting(t *testing.T) {
	r, _ := newRouterFixture(t, map[string]string{
		"hud-frontend": validHUDFrontend,
	})
	r.Policy = &fakeGate{enabled: true}

	dec, err := r.Pick(context.Background(),
		itemWithFiles("internal/hud/frontend/src/lib/components/SpawnPanel.svelte"))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if dec.SquadName != "hud-frontend" {
		t.Errorf("gate-on must permit routing: got %q want hud-frontend", dec.SquadName)
	}
}

func TestRouter_FallsBackWhenNoSquadsLoaded(t *testing.T) {
	r, _ := newRouterFixture(t, map[string]string{})
	dec, err := r.Pick(context.Background(), itemWithFiles("anywhere.go"))
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if dec.SquadName != FallbackName {
		t.Errorf("expected fallback, got %q", dec.SquadName)
	}
}

func TestRouter_UsesSpecDocWhenSlicesEmpty(t *testing.T) {
	r, _ := newRouterFixture(t, map[string]string{
		"hud-frontend": validHUDFrontend,
	})
	item := &store.BacklogItem{
		ID:      "HIVE-NOSLICES",
		Title:   "no slices",
		State:   store.BacklogQueued,
		SpecDoc: "internal/hud/frontend/foo.md",
	}
	dec, err := r.Pick(context.Background(), item)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if dec.SquadName != "hud-frontend" {
		t.Errorf("expected spec-doc fallback to match: %+v", dec)
	}
}

// intStr is a tiny strconv.Itoa replacement to keep this test file's
// import set minimal. The router_test file already pulls "strconv" from
// nothing; we keep it that way.
func intStr(i int) string {
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
