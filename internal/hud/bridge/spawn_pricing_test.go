package bridge

import (
	"math"
	"testing"
)

const floatTolerance = 1e-9

func floatsApproxEqual(a, b float64) bool {
	return math.Abs(a-b) < floatTolerance
}

func TestEstimateCodexCost_KnownModel(t *testing.T) {
	// gpt-5-codex: input $1.25/M, cached $0.125/M, output $10.00/M.
	// 1000 fresh + 500 cached + 200 output =
	//   (1000 * 1.25 + 500 * 0.125 + 200 * 10.00) / 1_000_000
	//   = (1250 + 62.5 + 2000) / 1_000_000
	//   = 3312.5 / 1_000_000
	//   = 0.0033125
	got := EstimateCodexCost("gpt-5-codex", 1000, 500, 200)
	want := 0.0033125
	if !floatsApproxEqual(got, want) {
		t.Errorf("EstimateCodexCost(gpt-5-codex, 1000, 500, 200) = %v, want %v", got, want)
	}
}

func TestEstimateCodexCost_GPT5Mini(t *testing.T) {
	// gpt-5-mini: input $0.25/M, cached $0.025/M, output $2.00/M.
	// 4000 fresh + 1000 cached + 800 output =
	//   (4000 * 0.25 + 1000 * 0.025 + 800 * 2.00) / 1_000_000
	//   = (1000 + 25 + 1600) / 1_000_000
	//   = 2625 / 1_000_000
	//   = 0.002625
	got := EstimateCodexCost("gpt-5-mini", 4000, 1000, 800)
	want := 0.002625
	if !floatsApproxEqual(got, want) {
		t.Errorf("EstimateCodexCost(gpt-5-mini, 4000, 1000, 800) = %v, want %v", got, want)
	}
}

func TestEstimateCodexCost_UnknownModelReturnsZero(t *testing.T) {
	got := EstimateCodexCost("not-a-real-model", 1000, 500, 200)
	if got != 0 {
		t.Errorf("EstimateCodexCost(unknown, ...) = %v, want 0", got)
	}
}

func TestEstimateCodexCost_ZeroTokensReturnsZero(t *testing.T) {
	got := EstimateCodexCost("gpt-5-codex", 0, 0, 0)
	if got != 0 {
		t.Errorf("EstimateCodexCost(gpt-5-codex, 0, 0, 0) = %v, want 0", got)
	}
}

func TestEstimateCodexCost_OnlyOutputTokens(t *testing.T) {
	// gpt-5-codex output $10.00/M => 1000 output tokens = $0.01.
	got := EstimateCodexCost("gpt-5-codex", 0, 0, 1000)
	want := 0.01
	if !floatsApproxEqual(got, want) {
		t.Errorf("EstimateCodexCost(gpt-5-codex, 0, 0, 1000) = %v, want %v", got, want)
	}
}

func TestLookupCodexPrice_KnownModel(t *testing.T) {
	p, ok := LookupCodexPrice("gpt-5-codex")
	if !ok {
		t.Fatalf("LookupCodexPrice(gpt-5-codex) ok=false, want true")
	}
	if p.InputPer1M != 1.25 || p.CachedInputPer1M != 0.125 || p.OutputPer1M != 10.00 {
		t.Errorf("LookupCodexPrice(gpt-5-codex) = %+v, want input=1.25 cached=0.125 output=10.00", p)
	}
}

func TestLookupCodexPrice_UnknownFallsBackToDefault(t *testing.T) {
	p, ok := LookupCodexPrice("not-a-real-model")
	if !ok {
		t.Fatalf("LookupCodexPrice(unknown) ok=false, want true (should fall back to %s)", DefaultCodexModel)
	}
	defaultPrice, _ := LookupCodexPrice(DefaultCodexModel)
	if p != defaultPrice {
		t.Errorf("LookupCodexPrice(unknown) = %+v, want default %+v", p, defaultPrice)
	}
}

func TestLookupCodexPrice_DefaultModelExists(t *testing.T) {
	// Sanity check: the default model must be present in the price table.
	if _, ok := codexModelPrices[DefaultCodexModel]; !ok {
		t.Errorf("DefaultCodexModel %q is not in codexModelPrices table", DefaultCodexModel)
	}
}

func TestEstimateCodexCost_AllKnownModelsNonZeroForNonZeroInput(t *testing.T) {
	// Smoke test: every entry in the table should produce a positive cost
	// when non-zero token counts are passed.
	for model := range codexModelPrices {
		got := EstimateCodexCost(model, 100, 100, 100)
		if got <= 0 {
			t.Errorf("EstimateCodexCost(%q, 100, 100, 100) = %v, want > 0", model, got)
		}
	}
}
