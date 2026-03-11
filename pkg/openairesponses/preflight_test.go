package openairesponses

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimateTokens_EmptyRequest(t *testing.T) {
	est := EstimateTokens(TurnRequest{Model: "gpt-4o"})
	assert.True(t, est.TotalTokens > 0, "should have overhead even for empty request")
	assert.Equal(t, 0, est.InputTokens)
	assert.Equal(t, 0, est.ToolTokens)
}

func TestEstimateTokens_StringInput(t *testing.T) {
	est := EstimateTokens(TurnRequest{
		Model: "gpt-4o",
		Input: "Hello, world! This is a test message.",
	})
	assert.True(t, est.InputTokens > 5, "string input should produce tokens")
	assert.Equal(t, 0, est.ToolTokens)
}

func TestEstimateTokens_WithTools(t *testing.T) {
	est := EstimateTokens(TurnRequest{
		Model: "gpt-4o",
		Input: "test",
		Tools: []ToolDefinition{
			{Name: "read_file", Description: "Read a file from disk", InputSchema: map[string]any{"type": "object"}},
			{Name: "write_file", Description: "Write a file to disk", InputSchema: map[string]any{"type": "object"}},
		},
	})
	assert.True(t, est.ToolTokens > 10, "tools should contribute tokens")
}

func TestEstimateTokens_ToolResults(t *testing.T) {
	est := EstimateTokens(TurnRequest{
		Model: "gpt-4o",
		Input: []ToolResult{
			{CallID: "call_1", Output: "short result"},
			{CallID: "call_2", Output: strings.Repeat("a", 4000)},
		},
	})
	assert.True(t, est.InputTokens > 1000, "large tool results should produce many tokens")
}

func TestPreflightCheck_NoBudget(t *testing.T) {
	err := PreflightCheck(TurnRequest{Model: "gpt-4o"}, 0)
	assert.NoError(t, err, "no budget means no check")
}

func TestPreflightCheck_WithinBudget(t *testing.T) {
	err := PreflightCheck(TurnRequest{Model: "gpt-4o", Input: "hello"}, 100000)
	assert.NoError(t, err)
}

func TestPreflightCheck_ExceedsBudget(t *testing.T) {
	err := PreflightCheck(TurnRequest{
		Model: "gpt-4o",
		Input: strings.Repeat("word ", 10000),
	}, 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token preflight failed")
}
