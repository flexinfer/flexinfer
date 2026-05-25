package main

import (
	"context"
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
}

func TestRun_AllRejected_SpecDecodeIsSlower(t *testing.T) {
	cfg := baseConfig()
	cfg.mockAcceptance = 0.0
	report, err := run(context.Background(), cfg, smallCorpus(), spec_decode.Coordinate)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, r := range report.PerPrompt {
		if r.SpecDecode == nil {
			t.Fatalf("%s: missing spec-decode", r.Name)
		}
		if r.SpecDecode.AcceptanceRate > 0.1 {
			t.Errorf("%s: expected ~0 acceptance with mock=0.0, got %.3f",
				r.Name, r.SpecDecode.AcceptanceRate)
		}
	}
	if report.Summary.VerdictPass {
		t.Errorf("expected verdict_pass=false with all-reject; got true")
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
