package bridge

// CodexModelPrice describes per-1M token costs in USD for an OpenAI model.
// Source: https://platform.openai.com/docs/pricing (snapshot 2026-04-07).
type CodexModelPrice struct {
	InputPer1M       float64 // fresh input
	CachedInputPer1M float64 // cached input (Anthropic-style "cache read")
	OutputPer1M      float64 // output
}

// codexModelPrices is a hard-coded snapshot of public OpenAI pricing.
// Update on a release cadence; the cost is reported with `cost_estimated:true`
// so consumers know it's a Loom-side estimate, not an SDK number.
//
// Snapshot date: 2026-04-07
// Source: https://platform.openai.com/docs/pricing
var codexModelPrices = map[string]CodexModelPrice{
	// gpt-5 family (defaults Codex CLI uses)
	"gpt-5":       {InputPer1M: 1.25, CachedInputPer1M: 0.125, OutputPer1M: 10.00},
	"gpt-5-mini":  {InputPer1M: 0.25, CachedInputPer1M: 0.025, OutputPer1M: 2.00},
	"gpt-5-nano":  {InputPer1M: 0.05, CachedInputPer1M: 0.005, OutputPer1M: 0.40},
	"gpt-5-codex": {InputPer1M: 1.25, CachedInputPer1M: 0.125, OutputPer1M: 10.00},
	// gpt-4.1 family
	"gpt-4.1":      {InputPer1M: 2.00, CachedInputPer1M: 0.50, OutputPer1M: 8.00},
	"gpt-4.1-mini": {InputPer1M: 0.40, CachedInputPer1M: 0.10, OutputPer1M: 1.60},
	"gpt-4.1-nano": {InputPer1M: 0.10, CachedInputPer1M: 0.025, OutputPer1M: 0.40},
	// o-series
	"o3":      {InputPer1M: 2.00, CachedInputPer1M: 0.50, OutputPer1M: 8.00},
	"o3-mini": {InputPer1M: 1.10, CachedInputPer1M: 0.275, OutputPer1M: 4.40},
	"o4-mini": {InputPer1M: 1.10, CachedInputPer1M: 0.275, OutputPer1M: 4.40},
}

// DefaultCodexModel is used when the parser does not know which model the
// Codex turn ran against. Codex CLI defaults to gpt-5-codex.
const DefaultCodexModel = "gpt-5-codex"

// EstimateCodexCost returns an estimated USD cost for a single turn given
// fresh-input, cached-input, and output token counts. Returns 0 for unknown
// models so callers can decide whether to fall back to a default.
func EstimateCodexCost(model string, freshInput, cachedInput, output int) float64 {
	price, ok := codexModelPrices[model]
	if !ok {
		return 0
	}
	return (float64(freshInput)*price.InputPer1M +
		float64(cachedInput)*price.CachedInputPer1M +
		float64(output)*price.OutputPer1M) / 1_000_000.0
}

// LookupCodexPrice returns the price entry for the given model, falling back
// to the default Codex model if unknown. ok=false means even the default is
// missing (programming error).
func LookupCodexPrice(model string) (CodexModelPrice, bool) {
	if p, ok := codexModelPrices[model]; ok {
		return p, true
	}
	if p, ok := codexModelPrices[DefaultCodexModel]; ok {
		return p, true
	}
	return CodexModelPrice{}, false
}
