package budget

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// estimatorEnv wires a real SQLite store + a closure-backed policy func
// so each test can vary the policy without an fs-backed manager.
type estimatorEnv struct {
	t      *testing.T
	store  *store.Store
	policy *hive.Policy
	est    *Estimator
}

func newEstimatorEnv(t *testing.T) *estimatorEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{
		Path: filepath.Join(dir, "h.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	env := &estimatorEnv{
		t:      t,
		store:  st,
		policy: hive.Default(),
	}
	env.est = &Estimator{
		Store:      st,
		PolicyFunc: func() *hive.Policy { return env.policy },
	}
	return env
}

// seedBacklog inserts a backlog item with the given id + labels.
func (e *estimatorEnv) seedBacklog(id string, labels []string) {
	e.t.Helper()
	item := &store.BacklogItem{
		ID:        id,
		Title:     "test " + id,
		Labels:    labels,
		State:     store.BacklogQueued,
		Priority:  store.P3,
		CreatedBy: "test",
	}
	if err := e.store.Backlog.Put(context.Background(), item); err != nil {
		e.t.Fatalf("seed backlog %s: %v", id, err)
	}
}

// seedOutcomes inserts n squad_outcomes rows matching the given path_class
// with cost spread linearly between minCost and maxCost so the median is
// deterministic. Each outcome is FK-tied to a freshly seeded pipeline_run
// + backlog_item to satisfy the schema's REFERENCES constraints.
func (e *estimatorEnv) seedOutcomes(squad, pathClass string, n int, minCost, maxCost float64) {
	e.t.Helper()
	if n <= 0 {
		return
	}
	ctx := context.Background()
	// Seed a backlog item the pipeline_runs rows can reference.
	itemID := fmt.Sprintf("HIVE-FIXTURE-%s", squad)
	if err := e.store.Backlog.Put(ctx, &store.BacklogItem{
		ID: itemID, Title: "fixture", State: store.BacklogQueued,
		Priority: store.P3, CreatedBy: "test",
	}); err != nil {
		e.t.Fatalf("seed fixture backlog: %v", err)
	}

	step := 0.0
	if n > 1 {
		step = (maxCost - minCost) / float64(n-1)
	}
	for i := 0; i < n; i++ {
		cost := minCost + step*float64(i)
		runID := fmt.Sprintf("PIPE-%s-%d-%d", squad, i, time.Now().UnixNano())
		if err := e.store.Pipeline.PutRun(ctx, &store.PipelineRun{
			ID: runID, BacklogID: itemID, Template: "hive-default-pipeline",
			State: store.PipelineDone, Attempts: i + 1, StartedAt: time.Now().UTC(),
		}); err != nil {
			e.t.Fatalf("seed pipeline run %d: %v", i, err)
		}
		row := &store.SquadOutcome{
			SquadName:       squad,
			PathClass:       pathClass,
			PipelineRunID:   runID,
			Outcome:         store.SquadOutcomeMergedClean,
			CostUSD:         cost,
			DurationSeconds: 60,
			CreatedAt:       time.Now().UTC(),
		}
		if err := e.store.Squads.RecordOutcome(ctx, row); err != nil {
			e.t.Fatalf("seed outcome %d: %v", i, err)
		}
	}
}

// seedSquad records a placeholder squad row so the FK on squad_outcomes
// (if enforced) does not reject our seeded outcome rows. Most tests can
// skip this because the v2 schema does not enforce a hard FK between
// squads and squad_outcomes — see store/migrations.
func (e *estimatorEnv) seedSquad(name string) {
	e.t.Helper()
	sq := &store.Squad{
		Name:        name,
		Paths:       []string{"**/*"},
		Tests:       []string{"go test ./..."},
		BudgetShare: 0.25,
		Enabled:     true,
	}
	if err := e.store.Squads.PutSquad(context.Background(), sq); err != nil {
		e.t.Fatalf("seed squad: %v", err)
	}
}

func TestEstimator_LowConfidence_NoHistory(t *testing.T) {
	env := newEstimatorEnv(t)
	env.seedBacklog("HIVE-NOHIST", nil)

	got, err := env.est.Preview(context.Background(), "HIVE-NOHIST")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if got.Confidence != "low" {
		t.Errorf("confidence: got %q want low", got.Confidence)
	}
	if got.SampleSize != 0 {
		t.Errorf("sample size: got %d want 0", got.SampleSize)
	}
	if got.PathClass != defaultPathClass {
		t.Errorf("path class: got %q want %q", got.PathClass, defaultPathClass)
	}
	if got.MedianHistoricalUSD <= 0 {
		t.Errorf("expected non-zero fallback median, got %v", got.MedianHistoricalUSD)
	}
	// Default policy has Pipeline.MaxUSDPerRun = 5 so fallback is 2.5.
	wantMedian := env.policy.Budgets.Pipeline.MaxUSDPerRun / 2
	if got.MedianHistoricalUSD != wantMedian {
		t.Errorf("fallback median: got %v want %v", got.MedianHistoricalUSD, wantMedian)
	}
	if got.SidecarSliceCount != 0 {
		t.Errorf("sidecar count: got %d want 0", got.SidecarSliceCount)
	}
}

func TestEstimator_HighConfidence_KnownPathClass(t *testing.T) {
	env := newEstimatorEnv(t)
	// Disable the per-run cap so the raw estimate is observable.
	env.policy.Budgets.Pipeline.MaxUSDPerRun = 0
	env.seedSquad("test-squad")
	env.seedOutcomes("test-squad", "go-svc/internal/**", 11, 1.0, 3.0)
	env.seedBacklog("HIVE-HIST", []string{"path_class:go-svc/internal/**"})

	got, err := env.est.Preview(context.Background(), "HIVE-HIST")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if got.Confidence != "high" {
		t.Errorf("confidence: got %q want high (sample=%d)", got.Confidence, got.SampleSize)
	}
	if got.SampleSize != 11 {
		t.Errorf("sample size: got %d want 11", got.SampleSize)
	}
	if got.PathClass != "go-svc/internal/**" {
		t.Errorf("path class: got %q", got.PathClass)
	}
	// 11 evenly spaced points between 1.0 and 3.0 → median is the
	// 6th element (index 5) = 2.0.
	if got.MedianHistoricalUSD < 1.99 || got.MedianHistoricalUSD > 2.01 {
		t.Errorf("median: got %v want ~2.0", got.MedianHistoricalUSD)
	}
}

func TestEstimator_RespectsEnsembleCap(t *testing.T) {
	env := newEstimatorEnv(t)
	// Tight cap; historical median far above it.
	env.policy.Budgets.Pipeline.MaxUSDPerRun = 1.0
	env.policy.Budgets.Pipeline.MaxUSDPerDay = 100
	env.seedSquad("test-squad")
	env.seedOutcomes("test-squad", "expensive/**", 5, 50, 60)
	env.seedBacklog("HIVE-CAP", []string{"path_class:expensive/**"})

	got, err := env.est.Preview(context.Background(), "HIVE-CAP")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !got.CappedByPolicy {
		t.Errorf("expected CappedByPolicy=true when median %v >> cap %v",
			got.MedianHistoricalUSD, got.EnsembleCapUSD)
	}
	if got.EstimateUSD != got.EnsembleCapUSD {
		t.Errorf("estimate: got %v want cap %v", got.EstimateUSD, got.EnsembleCapUSD)
	}
	if got.EnsembleCapUSD != 1.0 {
		t.Errorf("cap: got %v want 1.0", got.EnsembleCapUSD)
	}
}

func TestEstimator_RecursionOverhead_AddedWhenEnabled(t *testing.T) {
	env := newEstimatorEnv(t)
	env.policy.Budgets.Pipeline.MaxUSDPerRun = 0 // observe raw sum
	env.seedSquad("test-squad")
	env.seedOutcomes("test-squad", "rec/**", 5, 2.0, 2.0) // median = 2.0
	env.seedBacklog("HIVE-REC", []string{"path_class:rec/**"})

	// Recursion off → overhead zero.
	env.policy.Recursion.Enabled = false
	off, err := env.est.Preview(context.Background(), "HIVE-REC")
	if err != nil {
		t.Fatalf("preview off: %v", err)
	}
	if off.RecursionOverheadUSD != 0 {
		t.Errorf("recursion off: got overhead %v want 0", off.RecursionOverheadUSD)
	}

	// Recursion on with depth=2, share=0.5 → overhead = 2 * 0.5 * 2.0 = 2.0
	env.policy.Recursion.Enabled = true
	env.policy.Recursion.MaxDepth = 2
	env.policy.Recursion.SubrunMaxBudgetShare = 0.5
	on, err := env.est.Preview(context.Background(), "HIVE-REC")
	if err != nil {
		t.Fatalf("preview on: %v", err)
	}
	wantOverhead := 2.0 * 0.5 * 2.0
	if on.RecursionOverheadUSD < wantOverhead-0.01 || on.RecursionOverheadUSD > wantOverhead+0.01 {
		t.Errorf("recursion on: overhead got %v want %v", on.RecursionOverheadUSD, wantOverhead)
	}
	if on.EstimateUSD <= off.EstimateUSD {
		t.Errorf("estimate should grow when recursion enabled: off=%v on=%v",
			off.EstimateUSD, on.EstimateUSD)
	}
}

func TestEstimator_SidecarSliceCount(t *testing.T) {
	env := newEstimatorEnv(t)
	env.policy.Budgets.Pipeline.MaxUSDPerRun = 0
	env.policy.Recursion.Enabled = false
	env.seedSquad("test-squad")
	env.seedOutcomes("test-squad", "sc/**", 5, 4.0, 4.0) // median = 4.0
	env.seedBacklog("HIVE-SC", []string{
		"path_class:sc/**",
		"sidecar:docs",
		"sidecar:tests",
	})

	got, err := env.est.Preview(context.Background(), "HIVE-SC")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if got.SidecarSliceCount != 2 {
		t.Errorf("sidecar count: got %d want 2", got.SidecarSliceCount)
	}
	// 2 sidecars * 0.25 * 4.0 = 2.0
	wantOverhead := 2.0
	if got.SidecarOverheadUSD < wantOverhead-0.01 || got.SidecarOverheadUSD > wantOverhead+0.01 {
		t.Errorf("sidecar overhead: got %v want %v", got.SidecarOverheadUSD, wantOverhead)
	}
	// Estimate = median + sidecar = 4 + 2 = 6
	if got.EstimateUSD < 5.99 || got.EstimateUSD > 6.01 {
		t.Errorf("estimate: got %v want ~6.0", got.EstimateUSD)
	}
}

func TestEstimator_UnknownBacklog_ReturnsErrNotFound(t *testing.T) {
	env := newEstimatorEnv(t)
	_, err := env.est.Preview(context.Background(), "HIVE-MISSING")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error: got %v want ErrNotFound", err)
	}
}

func TestEstimator_NotConfigured_ReturnsError(t *testing.T) {
	var e *Estimator
	if _, err := e.Preview(context.Background(), "X"); err == nil {
		t.Errorf("nil receiver should error")
	}
	e2 := &Estimator{Store: nil, PolicyFunc: func() *hive.Policy { return nil }}
	if _, err := e2.Preview(context.Background(), "X"); err == nil {
		t.Errorf("nil store should error")
	}
}

func TestEstimator_MedianComputation(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		{"odd len", []float64{1, 2, 3, 4, 5}, 3},
		{"even len", []float64{1, 2, 3, 4}, 2.5},
		{"single", []float64{7}, 7},
		{"empty", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := median(tc.in); got != tc.want {
				t.Errorf("median(%v) = %v want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestEstimator_ClassifyConfidence(t *testing.T) {
	cases := []struct {
		sample int
		want   string
	}{
		{0, "low"}, {2, "low"},
		{3, "medium"}, {10, "medium"},
		{11, "high"}, {100, "high"},
	}
	for _, tc := range cases {
		if got := classifyConfidence(tc.sample); got != tc.want {
			t.Errorf("classifyConfidence(%d) = %q want %q", tc.sample, got, tc.want)
		}
	}
}
