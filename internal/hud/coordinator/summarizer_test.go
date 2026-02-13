package coordinator

import (
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestFormatEntries(t *testing.T) {
	entries := []bridge.ContextEntryInfo{
		{
			Entry: bridge.ContextEntry{
				ID:        "e1",
				EntryType: "decision",
				Title:     "Chose REST over gRPC",
				Content:   "REST is simpler for this use case",
			},
		},
		{
			Entry: bridge.ContextEntry{
				ID:        "e2",
				EntryType: "finding",
				Title:     "Found memory leak",
			},
		},
	}

	result := formatEntries(entries)

	if result == "" {
		t.Fatal("expected non-empty formatted entries")
	}
	if !contains(result, "Chose REST over gRPC") {
		t.Error("expected formatted output to contain title")
	}
	if !contains(result, "decision") {
		t.Error("expected formatted output to contain entry type")
	}
	if !contains(result, "Entry 1") {
		t.Error("expected formatted output to contain entry number")
	}
}

func TestParseSummaryResponse_ValidJSON(t *testing.T) {
	raw := `{
		"summary": "The agent implemented authentication",
		"key_findings": ["JWT is supported"],
		"key_decisions": ["Use JWT for auth"],
		"files_touched": ["auth.go"],
		"unresolved": [],
		"tags": ["auth"]
	}`

	result, err := parseSummaryResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary != "The agent implemented authentication" {
		t.Fatalf("unexpected summary: %s", result.Summary)
	}
	if len(result.KeyDecisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(result.KeyDecisions))
	}
}

func TestParseSummaryResponse_CodeFence(t *testing.T) {
	raw := "```json\n{\"summary\": \"test\", \"key_findings\": []}\n```"

	result, err := parseSummaryResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary != "test" {
		t.Fatalf("unexpected summary: %s", result.Summary)
	}
}

func TestParseSummaryResponse_EmptySummary(t *testing.T) {
	raw := `{"summary": "", "key_findings": []}`

	_, err := parseSummaryResponse(raw)
	if err == nil {
		t.Fatal("expected error for empty summary")
	}
}

func TestParseSummaryResponse_InvalidJSON(t *testing.T) {
	raw := `not json at all`

	_, err := parseSummaryResponse(raw)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestHasSummaryEntry(t *testing.T) {
	tests := []struct {
		name    string
		entries []bridge.ContextEntryInfo
		want    bool
	}{
		{
			name:    "empty",
			entries: nil,
			want:    false,
		},
		{
			name: "has summary type",
			entries: []bridge.ContextEntryInfo{
				{Entry: bridge.ContextEntry{EntryType: "summary"}},
			},
			want: true,
		},
		{
			name: "has summary title",
			entries: []bridge.ContextEntryInfo{
				{Entry: bridge.ContextEntry{Title: "Session Summary: abc123"}},
			},
			want: true,
		},
		{
			name: "no summary",
			entries: []bridge.ContextEntryInfo{
				{Entry: bridge.ContextEntry{EntryType: "decision", Title: "test"}},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasSummaryEntry(tt.entries)
			if got != tt.want {
				t.Errorf("hasSummaryEntry() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStripCodeFence(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"key": "val"}`, `{"key": "val"}`},
		{"```json\n{\"key\": \"val\"}\n```", `{"key": "val"}`},
		{"```\n{\"key\": \"val\"}\n```", `{"key": "val"}`},
		{"  ```json\n{\"key\": \"val\"}\n```  ", `{"key": "val"}`},
	}

	for _, tt := range tests {
		got := stripCodeFence(tt.input)
		if got != tt.want {
			t.Errorf("stripCodeFence(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("expected 'short', got %q", got)
	}
	if got := truncate("hello world this is long", 10); got != "hello worl..." {
		t.Errorf("expected truncated, got %q", got)
	}
}

func TestSummaryContent(t *testing.T) {
	result := &SessionSummaryResult{
		Summary:      "Session implemented coordinator retries.",
		KeyFindings:  []string{"Qdrant search was unstable"},
		KeyDecisions: []string{"Persist summary as context entry"},
		Unresolved:   []string{"Tune model timeout"},
		FilesTouched: []string{"internal/hud/coordinator/summarizer.go"},
	}

	content := summaryContent(result)

	if !contains(content, "Session implemented coordinator retries.") {
		t.Fatal("expected base summary text in rendered content")
	}
	if !contains(content, "Key findings:") {
		t.Fatal("expected key findings section")
	}
	if !contains(content, "Persist summary as context entry") {
		t.Fatal("expected key decisions content")
	}
	if !contains(content, "Unresolved:") {
		t.Fatal("expected unresolved section")
	}
	if !contains(content, "Files touched: internal/hud/coordinator/summarizer.go") {
		t.Fatal("expected files touched line")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
