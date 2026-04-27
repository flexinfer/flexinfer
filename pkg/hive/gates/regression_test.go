package gates

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// regressionEnv wires a real SQLite store + a SkipWatch policy manager
// so the gate runs against the real DAO codepath. The active policy is
// mutable via setAutoRevert so we cover both the default (off) and
// opt-in (on) branches.
type regressionEnv struct {
	t      *testing.T
	store  *store.Store
	policy *hive.PolicyManager
	gate   *RegressionGate
	now    time.Time
}

func newRegressionEnv(t *testing.T, autoRevert bool) *regressionEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{
		Path: filepath.Join(dir, "h.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	policyPath := filepath.Join(dir, "policy.yaml")
	body := []byte("version: 1\nenabled: true\npipeline:\n  default_template: hive-default-pipeline\n")
	if autoRevert {
		body = append(body, []byte("  auto_revert_on_regression: true\n")...)
	}
	if err := os.WriteFile(policyPath, body, 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	pm, err := hive.NewPolicyManager(context.Background(), policyPath, hive.PolicyManagerOptions{SkipWatch: true})
	if err != nil {
		t.Fatalf("policy mgr: %v", err)
	}
	t.Cleanup(func() { _ = pm.Close() })

	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	g := &RegressionGate{
		Store:  st,
		Policy: pm,
		Window: 30 * time.Minute,
		Now:    func() time.Time { return now },
	}
	return &regressionEnv{t: t, store: st, policy: pm, gate: g, now: now}
}

// seedMergedRun inserts a backlog item + matching merged pipeline run
// whose EndedAt = now - sinceMerge.
func (e *regressionEnv) seedMergedRun(id string, sinceMerge time.Duration) {
	e.t.Helper()
	ctx := context.Background()
	item := &store.BacklogItem{
		ID: id, Title: "test " + id, State: store.BacklogMerged,
		Priority: store.P2, CreatedAt: e.now.Add(-1 * time.Hour), UpdatedAt: e.now,
	}
	if err := e.store.Backlog.Put(ctx, item); err != nil {
		e.t.Fatalf("seed backlog %s: %v", id, err)
	}
	endedAt := e.now.Add(-sinceMerge)
	run := &store.PipelineRun{
		ID: "PIPE-" + id, BacklogID: id, Template: "hive-default-pipeline",
		State: store.PipelineDone, Attempts: 1,
		StartedAt: e.now.Add(-2 * time.Hour), EndedAt: &endedAt,
	}
	if err := e.store.Pipeline.PutRun(ctx, run); err != nil {
		e.t.Fatalf("seed run %s: %v", id, err)
	}
}

// ----- Acceptance: alert burst within 30min of an auto-merge bumps the metric -----

func TestRegressionGate_CorrelatesRecentMerge(t *testing.T) {
	env := newRegressionEnv(t, false)
	env.seedMergedRun("HIVE-A", 5*time.Minute)  // inside window
	env.seedMergedRun("HIVE-B", 10*time.Minute) // inside window
	env.seedMergedRun("HIVE-C", 45*time.Minute) // OUTSIDE window — must skip

	res, err := env.gate.OnAlert(context.Background(), AlertEvent{
		Name: "ApiErrorRateHigh", Severity: "critical", Status: "firing",
	})
	if err != nil {
		t.Fatalf("OnAlert: %v", err)
	}
	if got := len(res.Correlated); got != 2 {
		t.Errorf("correlated=%d want 2; got=%v", got, res.Correlated)
	}
	if res.AutoRevert {
		t.Errorf("auto_revert true with policy default off")
	}
}

func TestRegressionGate_NoMergesNoOp(t *testing.T) {
	env := newRegressionEnv(t, false)
	res, err := env.gate.OnAlert(context.Background(), AlertEvent{
		Name: "DiskFull", Severity: "warning", Status: "firing",
	})
	if err != nil {
		t.Fatalf("OnAlert: %v", err)
	}
	if len(res.Correlated) != 0 {
		t.Errorf("correlated=%d want 0", len(res.Correlated))
	}
}

func TestRegressionGate_ResolvedAlertSkipped(t *testing.T) {
	env := newRegressionEnv(t, false)
	env.seedMergedRun("HIVE-R", 1*time.Minute)

	res, err := env.gate.OnAlert(context.Background(), AlertEvent{
		Name: "Whatever", Severity: "critical", Status: "resolved",
	})
	if err != nil {
		t.Fatalf("OnAlert: %v", err)
	}
	if len(res.Correlated) != 0 {
		t.Errorf("resolved alerts must not correlate; got %d", len(res.Correlated))
	}
}

func TestRegressionGate_AutoRevertOptIn(t *testing.T) {
	env := newRegressionEnv(t, true)
	env.seedMergedRun("HIVE-X", 5*time.Minute)
	env.seedMergedRun("HIVE-Y", 10*time.Minute)

	res, err := env.gate.OnAlert(context.Background(), AlertEvent{
		Name: "TestAlert", Severity: "critical", Status: "firing",
	})
	if err != nil {
		t.Fatalf("OnAlert: %v", err)
	}
	if !res.AutoRevert || !res.AutoRevertPending {
		t.Errorf("expected AutoRevert + AutoRevertPending true with policy on; got %+v", res)
	}
	if len(res.Correlated) != 2 {
		t.Errorf("correlated=%d want 2", len(res.Correlated))
	}
}

func TestRegressionGate_AppendsAuditEvent(t *testing.T) {
	env := newRegressionEnv(t, false)
	env.seedMergedRun("HIVE-AUD", 5*time.Minute)

	if _, err := env.gate.OnAlert(context.Background(), AlertEvent{
		Name: "Audit", Severity: "warning", Status: "firing",
	}); err != nil {
		t.Fatalf("OnAlert: %v", err)
	}
	events, err := env.store.Events.ListSince(context.Background(), env.now.Add(-1*time.Hour), 100)
	if err != nil {
		t.Fatalf("events list: %v", err)
	}
	var found bool
	for _, ev := range events {
		if ev.Kind == "regression.correlated" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected regression.correlated event in audit log")
	}
}

func TestRegressionGate_NilStoreErrors(t *testing.T) {
	g := &RegressionGate{}
	_, err := g.OnAlert(context.Background(), AlertEvent{Name: "x", Status: "firing"})
	if err == nil {
		t.Errorf("expected error on nil-store gate")
	}
}

// ----- Pure helpers -----

func TestNormalizeSeverity(t *testing.T) {
	cases := map[string]string{
		"CRITICAL": "critical",
		"warning":  "warning",
		"INFO":     "info",
		"":         "none",
		"none":     "none",
		"weird":    "unknown",
	}
	for in, want := range cases {
		if got := normalizeSeverity(in); got != want {
			t.Errorf("normalizeSeverity(%q) = %q want %q", in, got, want)
		}
	}
}

func TestDefaultStr(t *testing.T) {
	if got := defaultStr("", "fallback"); got != "fallback" {
		t.Errorf("defaultStr empty: got %q", got)
	}
	if got := defaultStr("real", "fallback"); got != "real" {
		t.Errorf("defaultStr non-empty: got %q", got)
	}
}
