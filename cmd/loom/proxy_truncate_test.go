package main

import (
	"strings"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestTruncateCallToolResult_NoChangeWhenUnderLimit(t *testing.T) {
	r := &mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: "hello"}},
	}
	if truncateCallToolResult(r, 1024) {
		t.Fatalf("expected no truncation")
	}
	if got := r.Content[0].Text; got != "hello" {
		t.Fatalf("unexpected text: %q", got)
	}
}

func TestTruncateCallToolResult_TruncatesAndAddsSuffix(t *testing.T) {
	maxBytes := 2000
	huge := strings.Repeat("x", maxBytes*2)
	r := &mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: huge}},
	}

	if !truncateCallToolResult(r, maxBytes) {
		t.Fatalf("expected truncation")
	}
	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(r.Content))
	}
	if len(r.Content[0].Text) > maxBytes {
		t.Fatalf("expected truncated text <= %d bytes, got %d", maxBytes, len(r.Content[0].Text))
	}
	if !strings.Contains(r.Content[0].Text, "[loom] output truncated") {
		t.Fatalf("expected truncation suffix, got: %q", r.Content[0].Text)
	}
}

func TestTruncateCallToolResult_DropsExtraContentAndHonorsBudget(t *testing.T) {
	maxBytes := 1024
	r := &mcp.CallToolResult{
		Content: []mcp.Content{
			{Type: "text", Text: strings.Repeat("a", maxBytes-10)},
			{Type: "text", Text: strings.Repeat("b", maxBytes)},
		},
	}

	if !truncateCallToolResult(r, maxBytes) {
		t.Fatalf("expected truncation")
	}

	total := 0
	for _, c := range r.Content {
		total += len(c.Text) + len(c.Data)
	}
	if total > maxBytes {
		t.Fatalf("expected total <= %d bytes, got %d", maxBytes, total)
	}
	if len(r.Content) != 2 {
		t.Fatalf("expected second content item to remain (truncated), got %d items", len(r.Content))
	}
}
