package hive

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeReader is a deterministic stand-in for the canonical store. Every
// budget knob is exercised by varying the field values directly.
type fakeReader struct {
	councilCost  float64
	pipelineCost float64
	councilRuns  int
	pipelineRuns int
	pipelineActv int
}

func (f *fakeReader) CouncilCostSince(_ context.Context, _ time.Time) (float64, error) {
	return f.councilCost, nil
}
func (f *fakeReader) PipelineCostSince(_ context.Context, _ time.Time) (float64, error) {
	return f.pipelineCost, nil
}
func (f *fakeReader) CouncilRunsSince(_ context.Context, _ time.Time) (int, error) {
	return f.councilRuns, nil
}
func (f *fakeReader) PipelineRunsSince(_ context.Context, _ time.Time) (int, error) {
	return f.pipelineRuns, nil
}
func (f *fakeReader) PipelineActiveRuns(_ context.Context) (int, error) {
	return f.pipelineActv, nil
}

// budgetForTest returns a Budget wired to a fixed *Policy and a fake reader.
func budgetForTest(p *Policy, r *fakeReader) *Budget {
	return &Budget{
		PolicyFunc: func() *Policy { return p },
		Reader:     r,
		Now:        func() time.Time { return time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC) },
	}
}

func TestBudget_Allow_KillSwitch(t *testing.T) {
	p, _ := ParsePolicy([]byte(fixtureV1))
	off := false
	p.Enabled = &off
	b := budgetForTest(p, &fakeReader{})

	d, err := b.Allow(context.Background(), TierPipeline, 1.0)
	if err != nil {
		t.Fatalf("allow: %v", err)
	}
	if d.Allowed {
		t.Errorf("kill switch should reject")
	}
}

func TestBudget_Allow_PerRunCap(t *testing.T) {
	p, _ := ParsePolicy([]byte(fixtureV1))
	b := budgetForTest(p, &fakeReader{})

	// pipeline.max_usd_per_run = 5 in the fixture
	d, err := b.Allow(context.Background(), TierPipeline, 7.5)
	if err != nil {
		t.Fatalf("allow: %v", err)
	}
	if d.Allowed {
		t.Errorf("expected reject")
	}
	if !containsReason(d, "exceeds pipeline.max_usd_per_run") {
		t.Errorf("missing per-run reason: %v", d.Reasons)
	}
}

func TestBudget_Allow_DailyCap(t *testing.T) {
	p, _ := ParsePolicy([]byte(fixtureV1))
	b := budgetForTest(p, &fakeReader{pipelineCost: 73.0}) // 75 cap

	d, err := b.Allow(context.Background(), TierPipeline, 4.0)
	if err != nil {
		t.Fatalf("allow: %v", err)
	}
	if d.Allowed {
		t.Errorf("expected daily cap rejection: %+v", d)
	}
	if d.SpentUSD != 73.0 {
		t.Errorf("spent: %v", d.SpentUSD)
	}
	if d.RemainingUSD != 2.0 {
		t.Errorf("remaining: %v", d.RemainingUSD)
	}
	if !containsReason(d, "daily cap") {
		t.Errorf("missing daily reason: %v", d.Reasons)
	}
}

func TestBudget_Allow_RunCount(t *testing.T) {
	p, _ := ParsePolicy([]byte(fixtureV1))
	b := budgetForTest(p, &fakeReader{pipelineRuns: 20}) // == max_runs_per_day

	d, _ := b.Allow(context.Background(), TierPipeline, 0.5)
	if d.Allowed {
		t.Errorf("expected run-count rejection: %+v", d)
	}
	if !containsReason(d, "daily run count") {
		t.Errorf("missing run-count reason: %v", d.Reasons)
	}
}

func TestBudget_Allow_Concurrency(t *testing.T) {
	p, _ := ParsePolicy([]byte(fixtureV1))
	b := budgetForTest(p, &fakeReader{pipelineActv: 4}) // == max_concurrent_runs

	d, _ := b.Allow(context.Background(), TierPipeline, 0.5)
	if d.Allowed {
		t.Errorf("expected concurrency rejection: %+v", d)
	}
	if !containsReason(d, "max_concurrent_runs") {
		t.Errorf("missing concurrency reason: %v", d.Reasons)
	}
}

func TestBudget_Allow_HappyPath(t *testing.T) {
	p, _ := ParsePolicy([]byte(fixtureV1))
	b := budgetForTest(p, &fakeReader{
		pipelineCost: 10, pipelineRuns: 3, pipelineActv: 1,
	})

	d, err := b.Allow(context.Background(), TierPipeline, 2.0)
	if err != nil {
		t.Fatalf("allow: %v", err)
	}
	if !d.Allowed {
		t.Errorf("expected allow: %+v", d)
	}
	if d.RemainingUSD != 65.0 { // 75 - 10
		t.Errorf("remaining: %v", d.RemainingUSD)
	}
}

func TestBudget_Allow_CouncilTier(t *testing.T) {
	p, _ := ParsePolicy([]byte(fixtureV1))
	// council has no concurrency or run-count cap configured; the spend
	// caps still apply.
	b := budgetForTest(p, &fakeReader{councilCost: 49.0})

	d, _ := b.Allow(context.Background(), TierCouncil, 5.0)
	if d.Allowed {
		t.Errorf("expected daily reject: %+v", d)
	}
	d2, _ := b.Allow(context.Background(), TierCouncil, 0.5)
	if !d2.Allowed {
		t.Errorf("expected allow under cap: %+v", d2)
	}
}

func TestBudget_Remaining(t *testing.T) {
	p, _ := ParsePolicy([]byte(fixtureV1))
	b := budgetForTest(p, &fakeReader{pipelineCost: 30.0})

	r, err := b.Remaining(context.Background(), TierPipeline)
	if err != nil {
		t.Fatalf("remaining: %v", err)
	}
	if r != 45.0 { // 75 - 30
		t.Errorf("remaining: %v", r)
	}
}

func TestBudget_Allow_RejectsNegativeEstimate(t *testing.T) {
	p, _ := ParsePolicy([]byte(fixtureV1))
	b := budgetForTest(p, &fakeReader{})
	if _, err := b.Allow(context.Background(), TierPipeline, -1); err == nil {
		t.Errorf("expected error on negative estimate")
	}
}

func containsReason(d Decision, sub string) bool {
	for _, r := range d.Reasons {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}
