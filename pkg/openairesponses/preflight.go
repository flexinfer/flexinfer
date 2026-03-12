package openairesponses

import "fmt"

// TokenEstimate holds the estimated token count for a turn request.
type TokenEstimate struct {
	InputTokens int
	ToolTokens  int
	TotalTokens int
}

// EstimateTokens estimates request token cost using fi-accel-backed tokenization
// when available, with a small cache to avoid retokenizing repeated request
// fragments such as tool schemas and prior tool outputs.
func EstimateTokens(req TurnRequest) TokenEstimate {
	model := normalizeTokenizerModel(req.Model)
	var est TokenEstimate

	est.InputTokens = estimateInputTokens(model, req.Input)
	for _, t := range req.Tools {
		est.ToolTokens += estimateToolTokens(model, t)
	}

	// Structural overhead for request framing and metadata that is not included in
	// the fragment-level counters.
	overhead := 50 + estimateTextTokens(model, req.Model)
	est.TotalTokens = est.InputTokens + est.ToolTokens + overhead
	return est
}

// PreflightCheck validates that a request fits within the configured token budget.
// Returns nil if within budget, or an error describing the overage.
func PreflightCheck(req TurnRequest, budget int) error {
	if budget <= 0 {
		return nil
	}

	est := EstimateTokens(req)
	if est.TotalTokens <= budget {
		return nil
	}

	return fmt.Errorf(
		"token preflight failed: estimated %d tokens exceeds budget %d (input=%d, tools=%d)",
		est.TotalTokens, budget, est.InputTokens, est.ToolTokens,
	)
}
