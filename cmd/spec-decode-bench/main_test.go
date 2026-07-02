package main

import (
	"context"
	"math"
	"testing"

	"github.com/flexinfer/flexinfer/internal/proxy/spec_decode"
)

// smallCorpus keeps the test runtime short by using just two prompts.
func smallCorpus() []CorpusEntry {
	return []CorpusEntry{
		{Name: "short", Prompt: "hi"},
		{Name: "tiny", Prompt: "hello world"},
	}
}

func baseConfig() benchConfig {
	return benchConfig{
		draftN:             4,
		maxTokens:          16,
		maxRounds:          8,
		mode:               "compare",
		accept:             "greedy",
		seed:               20260525,
		mockAcceptance:     0.0,
		mockAcceptanceSet:  true,
		mockDecodeMsPerTok: 1,
		mockDraftMsPerTok:  0,
	}
}

func TestRun_AllAccepted_SpecDecodeIsFaster(t *testing.T) {
	cfg := baseConfig()
	cfg.mockAcceptance = 1.0
	report, err := run(context.Background(), cfg, smallCorpus(), spec_decode.Coordinate)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(report.PerPrompt) != 2 {
		t.Fatalf("expected 2 prompt rows, got %d", len(report.PerPrompt))
	}
	for _, r := range report.PerPrompt {
		if r.Baseline == nil || r.SpecDecode == nil {
			t.Fatalf("%s: missing baseline or spec-decode", r.Name)
		}
		if r.SpecDecode.TokensGenerated <= 0 {
			t.Fatalf("%s: spec-decode produced no tokens", r.Name)
		}
		if r.SpecDecode.AcceptanceRate <= 0.9 {
			t.Errorf("%s: expected ~1.0 acceptance with mock=1.0, got %.3f",
				r.Name, r.SpecDecode.AcceptanceRate)
		}
	}
	if report.Summary.SpeedupP50 < 1.0 {
		t.Errorf("expected speedup >= 1.0 with all-accept; got %.3f",
			report.Summary.SpeedupP50)
	}
	// Deterministic under the simulated clock: 16 baseline ticks vs 4
	// spec-decode rounds (4 accepted tokens each) = modeled speedup 4.0.
	if !report.Summary.VerdictPass {
		t.Errorf("expected verdict_pass=true with all-accept; got false (%s)",
			report.Summary.VerdictReason)
	}
}

func TestRun_AllRejected_SpecDecodeIsSlower(t *testing.T) {
	cfg := baseConfig()
	cfg.mockAcceptance = 0.0
	report, err := run(context.Background(), cfg, smallCorpus(), spec_decode.Coordinate)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, r := range report.PerPrompt {
		if r.Baseline == nil || r.SpecDecode == nil {
			t.Fatalf("%s: missing baseline or spec-decode", r.Name)
		}
		if r.SpecDecode.AcceptanceRate > 0.1 {
			t.Errorf("%s: expected ~0 acceptance with mock=0.0, got %.3f",
				r.Name, r.SpecDecode.AcceptanceRate)
		}
		// Regression guard: elapsed must come from the mock's simulated
		// clock, not the wall clock. Wall-clock stats made this test flaky
		// on loaded CI runners — sleep jitter across ~16ms of 1ms ticks
		// pushed the measured speedup past the 1.5 gate.
		const eps = 1e-9
		wantBaseline := 0.016 // 16 tokens x 1ms modeled decode
		if math.Abs(r.Baseline.ElapsedSeconds-wantBaseline) > eps {
			t.Errorf("%s: baseline elapsed = %v, want modeled %v",
				r.Name, r.Baseline.ElapsedSeconds, wantBaseline)
		}
		wantSpec := 0.008 // 8 rounds x 1ms verify, 0ms draft
		if math.Abs(r.SpecDecode.ElapsedSeconds-wantSpec) > eps {
			t.Errorf("%s: spec-decode elapsed = %v, want modeled %v",
				r.Name, r.SpecDecode.ElapsedSeconds, wantSpec)
		}
	}
	// All-reject emits 1 bonus token per verify tick — the same 1 tok/ms
	// rate as baseline — so the modeled speedup is exactly 1.0, below the
	// 1.5 gate.
	if report.Summary.VerdictPass {
		t.Errorf("expected verdict_pass=false with all-reject; got true (%s)",
			report.Summary.VerdictReason)
	}
}

func TestRun_ReportShapeIsValidJSON(t *testing.T) {
	cfg := baseConfig()
	cfg.mockAcceptance = 0.7
	report, err := run(context.Background(), cfg, smallCorpus(), spec_decode.Coordinate)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.SchemaVersion != "flexinfer.spec_decode_bench.v1" {
		t.Errorf("schema_version = %q, want flexinfer.spec_decode_bench.v1",
			report.SchemaVersion)
	}
	if report.CreatedAt == "" {
		t.Error("created_at is empty")
	}
}
