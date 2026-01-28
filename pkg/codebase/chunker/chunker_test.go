package chunker

import (
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

func TestSplitLargeChunks_SmallChunk(t *testing.T) {
	chunks := []schema.Chunk{
		{
			ID:         "test1",
			TokenCount: 100,
			Content:    "small content",
		},
	}

	cfg := Config{MaxTokens: 200}
	result := SplitLargeChunks(chunks, cfg)

	if len(result) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(result))
	}
	if result[0].ID != "test1" {
		t.Errorf("expected unchanged chunk ID")
	}
}

func TestSplitLargeChunks_LargeChunk(t *testing.T) {
	// Create a large chunk with ~100 lines
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "func example() { return something.Call() }"
	}
	content := strings.Join(lines, "\n")

	chunks := []schema.Chunk{
		{
			ID:         "large1",
			RepoID:     "test-repo",
			FilePath:   "test.go",
			Language:   "go",
			ChunkType:  "function",
			Name:       "LargeFunction",
			Signature:  "func LargeFunction()",
			Docstring:  "This is a large function",
			TokenCount: 5000, // Way over limit
			Content:    content,
			StartLine:  10,
			EndLine:    110,
		},
	}

	cfg := Config{
		MaxTokens:     500,
		OverlapTokens: 50,
		MinTokens:     20,
	}
	result := SplitLargeChunks(chunks, cfg)

	if len(result) <= 1 {
		t.Errorf("expected multiple windows, got %d", len(result))
	}

	// Check first window has docstring
	if result[0].Docstring != "This is a large function" {
		t.Error("first window should have docstring")
	}

	// Check subsequent windows don't have docstring
	for i := 1; i < len(result); i++ {
		if result[i].Docstring != "" {
			t.Errorf("window %d should not have docstring", i)
		}
	}

	// Check all windows have proper chunk type
	for i, w := range result {
		if w.ChunkType != "function_window" {
			t.Errorf("window %d: expected chunk_type 'function_window', got %q", i, w.ChunkType)
		}
	}

	// Check IDs are unique
	seen := make(map[string]bool)
	for _, w := range result {
		if seen[w.ID] {
			t.Errorf("duplicate ID: %s", w.ID)
		}
		seen[w.ID] = true
	}
}

func TestEstimateLineTokens(t *testing.T) {
	tests := []struct {
		line     string
		minTokens int
		maxTokens int
	}{
		{"", 1, 1},
		{"x", 1, 3},
		{"func foo() {", 3, 10},
		{"    return x + y * z", 4, 15},
		{"fmt.Println(\"hello world\")", 3, 15},
	}

	for _, tt := range tests {
		tokens := estimateLineTokens(tt.line)
		if tokens < tt.minTokens || tokens > tt.maxTokens {
			t.Errorf("estimateLineTokens(%q) = %d, want [%d, %d]",
				tt.line, tokens, tt.minTokens, tt.maxTokens)
		}
	}
}

func TestExtractCallsFromContent(t *testing.T) {
	content := `
func example() {
    foo()
    bar.Baz()
    if x {
        qux()
    }
    for i := range items {
        process(i)
    }
}
`
	calls := extractCallsFromContent(content)

	expected := map[string]bool{
		"foo":     true,
		"bar":     false, // not followed by (
		"Baz":     true,
		"qux":     true,
		"process": true,
	}

	callSet := make(map[string]bool)
	for _, c := range calls {
		callSet[c] = true
	}

	for name, shouldExist := range expected {
		if shouldExist && !callSet[name] {
			t.Errorf("expected call %q not found", name)
		}
	}

	// Keywords should not be included
	keywords := []string{"if", "for", "range", "func"}
	for _, kw := range keywords {
		if callSet[kw] {
			t.Errorf("keyword %q should not be in calls", kw)
		}
	}
}

func TestIsKeyword(t *testing.T) {
	keywords := []string{"if", "for", "while", "func", "def", "class", "return"}
	for _, kw := range keywords {
		if !isKeyword(kw) {
			t.Errorf("%q should be a keyword", kw)
		}
	}

	nonKeywords := []string{"foo", "bar", "myFunc", "getData", "processItem"}
	for _, nk := range nonKeywords {
		if isKeyword(nk) {
			t.Errorf("%q should not be a keyword", nk)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxTokens <= 0 {
		t.Error("MaxTokens should be positive")
	}
	if cfg.OverlapTokens <= 0 {
		t.Error("OverlapTokens should be positive")
	}
	if cfg.MinTokens <= 0 {
		t.Error("MinTokens should be positive")
	}
	if cfg.OverlapTokens >= cfg.MaxTokens {
		t.Error("OverlapTokens should be less than MaxTokens")
	}
}

func TestSplitLargeChunks_PreservesMetadata(t *testing.T) {
	chunks := []schema.Chunk{
		{
			ID:         "meta-test",
			RepoID:     "repo-123",
			FilePath:   "pkg/foo/bar.go",
			Language:   "go",
			ChunkType:  "method",
			GitCommit:  "abc123",
			GitBlame:   "author@example.com",
			Name:       "ProcessData",
			Signature:  "func (s *Service) ProcessData(ctx context.Context)",
			ParentName: "Service",
			ParentType: "struct",
			TokenCount: 5000,
			Content:    strings.Repeat("line of code\n", 500),
			StartLine:  100,
		},
	}

	cfg := Config{MaxTokens: 500, OverlapTokens: 50, MinTokens: 20}
	result := SplitLargeChunks(chunks, cfg)

	for _, w := range result {
		if w.RepoID != "repo-123" {
			t.Error("RepoID not preserved")
		}
		if w.FilePath != "pkg/foo/bar.go" {
			t.Error("FilePath not preserved")
		}
		if w.Language != "go" {
			t.Error("Language not preserved")
		}
		if w.GitCommit != "abc123" {
			t.Error("GitCommit not preserved")
		}
		if w.GitBlame != "author@example.com" {
			t.Error("GitBlame not preserved")
		}
		if w.Name != "ProcessData" {
			t.Error("Name not preserved")
		}
		if w.ParentName != "Service" {
			t.Error("ParentName not preserved")
		}
	}
}
