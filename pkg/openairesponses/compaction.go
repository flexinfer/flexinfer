package openairesponses

import (
	"encoding/json"
	"fmt"
)

// CompactionStrategy defines how context is compacted when approaching token limits.
type CompactionStrategy string

const (
	// CompactNone disables compaction (fail if over budget).
	CompactNone CompactionStrategy = "none"
	// CompactTruncate truncates older tool results to fit within budget.
	CompactTruncate CompactionStrategy = "truncate"
	// CompactSummarize replaces older tool results with a condensed summary.
	CompactSummarize CompactionStrategy = "summarize"
)

// CompactRequest attempts to reduce a TurnRequest's token count to fit within budget.
// Returns the compacted request and whether compaction was applied. If the strategy
// is "none" or compaction cannot bring the request within budget, returns an error.
func CompactRequest(req TurnRequest, budget int, strategy CompactionStrategy) (TurnRequest, bool, error) {
	if budget <= 0 {
		return req, false, nil // No budget — pass through.
	}

	est := EstimateTokens(req)
	if est.TotalTokens <= budget {
		return req, false, nil // Already fits.
	}

	switch strategy {
	case CompactNone, "":
		return req, false, fmt.Errorf(
			"token budget exceeded (%d > %d) and compaction is disabled",
			est.TotalTokens, budget,
		)
	case CompactTruncate:
		return truncateToolResults(req, budget)
	case CompactSummarize:
		return summarizeToolResults(req, budget)
	default:
		return req, false, fmt.Errorf("unknown compaction strategy: %q", strategy)
	}
}

// truncateToolResults reduces tool result sizes by trimming output content
// from oldest to newest until the request fits within budget.
func truncateToolResults(req TurnRequest, budget int) (TurnRequest, bool, error) {
	results, ok := req.Input.([]ToolResult)
	if !ok || len(results) == 0 {
		return req, false, fmt.Errorf("cannot truncate: input is not tool results")
	}

	// Work on a copy.
	compacted := make([]ToolResult, len(results))
	copy(compacted, results)

	const maxOutputLen = 200 // truncated output max chars

	// Truncate from oldest (index 0) toward newest.
	for i := 0; i < len(compacted); i++ {
		outputStr := stringifyOutput(compacted[i].Output)
		if len(outputStr) > maxOutputLen {
			compacted[i].Output = outputStr[:maxOutputLen] + "... [truncated]"
		}

		// Re-check budget.
		modified := req
		modified.Input = compacted
		if est := EstimateTokens(modified); est.TotalTokens <= budget {
			return modified, true, nil
		}
	}

	// Even after truncating all, check if we fit.
	modified := req
	modified.Input = compacted
	if est := EstimateTokens(modified); est.TotalTokens <= budget {
		return modified, true, nil
	}

	return req, false, fmt.Errorf(
		"truncation insufficient: estimated %d tokens still exceeds budget %d",
		EstimateTokens(modified).TotalTokens, budget,
	)
}

// summarizeToolResults replaces older tool results with a single condensed
// placeholder, keeping only the most recent result intact.
func summarizeToolResults(req TurnRequest, budget int) (TurnRequest, bool, error) {
	results, ok := req.Input.([]ToolResult)
	if !ok || len(results) == 0 {
		return req, false, fmt.Errorf("cannot summarize: input is not tool results")
	}

	if len(results) <= 1 {
		// Only one result — try truncation instead.
		return truncateToolResults(req, budget)
	}

	// Keep the last result, summarize all earlier ones.
	summarized := make([]ToolResult, 0, 2)

	// Create a summary of older results.
	summary := fmt.Sprintf("[%d prior tool results compacted]", len(results)-1)
	summarized = append(summarized, ToolResult{
		CallID: results[0].CallID,
		Output: summary,
	})
	// Keep the most recent result.
	summarized = append(summarized, results[len(results)-1])

	modified := req
	modified.Input = summarized

	if est := EstimateTokens(modified); est.TotalTokens <= budget {
		return modified, true, nil
	}

	// Still over budget — try truncating the remaining.
	return truncateToolResults(modified, budget)
}

// stringifyOutput converts a tool result output to a string for truncation.
func stringifyOutput(output any) string {
	if output == nil {
		return ""
	}
	switch v := output.(type) {
	case string:
		return v
	case json.RawMessage:
		return string(v)
	case []byte:
		return string(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	}
}
