package openairesponses

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompactRequest_NoBudget(t *testing.T) {
	req := TurnRequest{Model: "gpt-4o", Input: "hello"}
	out, compacted, err := CompactRequest(req, 0, CompactTruncate)
	require.NoError(t, err)
	assert.False(t, compacted)
	assert.Equal(t, req, out)
}

func TestCompactRequest_WithinBudget(t *testing.T) {
	req := TurnRequest{Model: "gpt-4o", Input: "hello"}
	out, compacted, err := CompactRequest(req, 100000, CompactTruncate)
	require.NoError(t, err)
	assert.False(t, compacted)
	assert.Equal(t, req, out)
}

func TestCompactRequest_NoneStrategy(t *testing.T) {
	req := TurnRequest{
		Model: "gpt-4o",
		Input: []ToolResult{
			{CallID: "c1", Output: strings.Repeat("x", 10000)},
		},
	}
	_, _, err := CompactRequest(req, 100, CompactNone)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compaction is disabled")
}

func TestCompactRequest_TruncateStrategy(t *testing.T) {
	largeOutput := strings.Repeat("data ", 2000) // ~10000 chars
	req := TurnRequest{
		Model: "gpt-4o",
		Input: []ToolResult{
			{CallID: "c1", Output: largeOutput},
			{CallID: "c2", Output: "small result"},
		},
	}

	// Budget that's enough for truncated but not full.
	budget := 500
	out, compacted, err := CompactRequest(req, budget, CompactTruncate)
	require.NoError(t, err)
	assert.True(t, compacted)

	results, ok := out.Input.([]ToolResult)
	require.True(t, ok)
	require.Len(t, results, 2)

	// First result should be truncated.
	firstOutput, ok := results[0].Output.(string)
	require.True(t, ok)
	assert.True(t, len(firstOutput) < len(largeOutput), "should be truncated")
	assert.Contains(t, firstOutput, "[truncated]")
}

func TestCompactRequest_SummarizeStrategy(t *testing.T) {
	req := TurnRequest{
		Model: "gpt-4o",
		Input: []ToolResult{
			{CallID: "c1", Output: strings.Repeat("old data ", 500)},
			{CallID: "c2", Output: strings.Repeat("more data ", 500)},
			{CallID: "c3", Output: "recent result"},
		},
	}

	budget := 500
	out, compacted, err := CompactRequest(req, budget, CompactSummarize)
	require.NoError(t, err)
	assert.True(t, compacted)

	results, ok := out.Input.([]ToolResult)
	require.True(t, ok)

	// Should have at most 2 results: summary + recent.
	assert.True(t, len(results) <= 2, "should have summarized older results")
}

func TestCompactRequest_NonToolInput(t *testing.T) {
	req := TurnRequest{
		Model: "gpt-4o",
		Input: strings.Repeat("x", 10000),
	}
	_, _, err := CompactRequest(req, 100, CompactTruncate)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not tool results")
}

func TestStringifyOutput(t *testing.T) {
	assert.Equal(t, "", stringifyOutput(nil))
	assert.Equal(t, "hello", stringifyOutput("hello"))
	assert.Equal(t, `{"a":1}`, stringifyOutput(map[string]int{"a": 1}))
}
