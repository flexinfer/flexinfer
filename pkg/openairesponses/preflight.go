package openairesponses

import (
	"encoding/json"
	"fmt"
)

// TokenEstimate holds the estimated token count for a turn request.
type TokenEstimate struct {
	InputTokens int
	ToolTokens  int
	TotalTokens int
}

// EstimateTokens provides a heuristic token count for a TurnRequest.
// Uses ~4 characters per token for English text plus overhead for
// tool schemas and structural JSON.
func EstimateTokens(req TurnRequest) TokenEstimate {
	var est TokenEstimate

	// Estimate input tokens.
	est.InputTokens = estimateInputTokens(req.Input)

	// Estimate tool schema tokens.
	for _, t := range req.Tools {
		est.ToolTokens += estimateToolTokens(t)
	}

	// Structural overhead: model name, context IDs, JSON framing.
	overhead := 50 + len(req.Model)/4

	est.TotalTokens = est.InputTokens + est.ToolTokens + overhead
	return est
}

// PreflightCheck validates that a request fits within the configured token budget.
// Returns nil if within budget, or an error describing the overage.
func PreflightCheck(req TurnRequest, budget int) error {
	if budget <= 0 {
		return nil // No budget configured — skip check.
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

func estimateInputTokens(input any) int {
	if input == nil {
		return 0
	}

	switch v := input.(type) {
	case string:
		return charToTokens(len(v))
	case []byte:
		return charToTokens(len(v))
	case json.RawMessage:
		return charToTokens(len(v))
	case []ToolResult:
		total := 0
		for _, r := range v {
			total += estimateToolResultTokens(r)
		}
		return total
	default:
		// Marshal to JSON for size estimation.
		data, err := json.Marshal(v)
		if err != nil {
			return 100 // conservative fallback
		}
		return charToTokens(len(data))
	}
}

func estimateToolTokens(t ToolDefinition) int {
	tokens := charToTokens(len(t.Name) + len(t.Description))
	if t.InputSchema != nil {
		data, err := json.Marshal(t.InputSchema)
		if err == nil {
			tokens += charToTokens(len(data))
		}
	}
	return tokens + 10 // per-tool structural overhead
}

func estimateToolResultTokens(r ToolResult) int {
	tokens := 20 // call_id + structural overhead
	if r.IsError {
		tokens += charToTokens(len(r.ErrorText))
	} else if r.Output != nil {
		switch v := r.Output.(type) {
		case string:
			tokens += charToTokens(len(v))
		case json.RawMessage:
			tokens += charToTokens(len(v))
		default:
			data, err := json.Marshal(v)
			if err == nil {
				tokens += charToTokens(len(data))
			} else {
				tokens += 100
			}
		}
	}
	return tokens
}

// charToTokens converts character count to estimated token count.
// Uses ~4 characters per token heuristic for English text.
func charToTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}
