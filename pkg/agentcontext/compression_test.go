package agentcontext

import (
	"strings"
	"testing"
)

func TestSplitSentences(t *testing.T) {
	text := "This is sentence one. This is sentence two! And three? Short. This is another sentence."
	sentences := splitSentences(text)
	if len(sentences) == 0 {
		t.Fatal("splitSentences returned no sentences")
	}
	for _, s := range sentences {
		if len(s) <= 10 {
			t.Errorf("splitSentences returned short fragment: %q", s)
		}
	}
}

func TestExtractKeywords(t *testing.T) {
	fc := NewFallbackCompressor()
	text := "The Go programming language is efficient and concurrent. Go provides goroutines for concurrent programming. Go is compiled and statically typed."
	keywords := fc.ExtractKeywords(text, 5)
	if len(keywords) == 0 {
		t.Fatal("extractKeywords returned no keywords")
	}
	if len(keywords) > 5 {
		t.Errorf("extractKeywords returned %d keywords, want <= 5", len(keywords))
	}
}

func TestExtractKeywordsEmpty(t *testing.T) {
	fc := NewFallbackCompressor()
	keywords := fc.ExtractKeywords("", 5)
	if keywords != nil {
		t.Errorf("extractKeywords on empty string returned %v, want nil", keywords)
	}
}

func TestCompress(t *testing.T) {
	fc := NewFallbackCompressor()
	text := "This is a first sentence about programming. This is a second sentence about testing. " +
		"This is a third sentence about Go language. This is a fourth sentence about compression. " +
		"This is a fifth sentence about algorithms."
	result := fc.Compress(text, 0.5)
	if result.Ratio > 1.0 {
		t.Errorf("Compress ratio %f > 1.0", result.Ratio)
	}
	if result.Summary == "" {
		t.Error("Compress returned empty summary")
	}
}

func TestTruncateText(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		targetLen int
		check     func(string) bool
	}{
		{"short text unchanged", "hello", 10, func(s string) bool { return s == "hello" }},
		{"exact length unchanged", "hello", 5, func(s string) bool { return s == "hello" }},
		{"breaks at word boundary", "hello world foo", 12, func(s string) bool { return s == "hello world..." }},
		{"falls back to hard cut", "abcdefghijklmnop", 10, func(s string) bool { return s == "abcdefg..." }},
		{"empty string", "", 5, func(s string) bool { return s == "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateText(tc.text, tc.targetLen)
			if !tc.check(got) {
				t.Errorf("truncateText(%q, %d) = %q", tc.text, tc.targetLen, got)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"Hello, World! 123", 3},
		{"a-b_c", 3},
	}
	for _, tc := range tests {
		got := tokenize(tc.input)
		if len(got) != tc.expected {
			t.Errorf("tokenize(%q) returned %d words, want %d: %v", tc.input, len(got), tc.expected, got)
		}
	}
}

func TestTokenize_Lowercases(t *testing.T) {
	got := tokenize("Hello WORLD")
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Errorf("expected [hello world], got %v", got)
	}
}

func TestDefaultStopwords(t *testing.T) {
	sw := defaultStopwords()
	if len(sw) == 0 {
		t.Fatal("defaultStopwords returned empty map")
	}
	for _, word := range []string{"the", "and", "is", "a"} {
		if !sw[word] {
			t.Errorf("expected stopword %q to be present", word)
		}
	}
	if sw["kubernetes"] {
		t.Error("unexpected stopword: kubernetes")
	}
}

func TestNewFallbackCompressor(t *testing.T) {
	fc := NewFallbackCompressor()
	if fc == nil {
		t.Fatal("NewFallbackCompressor returned nil")
	}
	if len(fc.stopwords) == 0 {
		t.Error("expected non-empty stopwords")
	}
}

func TestCompress_InvalidRatio(t *testing.T) {
	fc := NewFallbackCompressor()
	text := "First sentence here. Second sentence here. Third sentence here."
	result := fc.Compress(text, -1)
	if result.Summary == "" {
		t.Error("Compress with invalid ratio returned empty summary")
	}
}

func TestCompress_EmptyText(t *testing.T) {
	fc := NewFallbackCompressor()
	result := fc.Compress("", 0.5)
	if result.Ratio != 1.0 {
		t.Errorf("expected ratio 1.0 for empty text, got %f", result.Ratio)
	}
}

func BenchmarkExtractKeywords(b *testing.B) {
	fc := NewFallbackCompressor()
	// Build a realistic document (~2000 words)
	words := []string{
		"infrastructure", "kubernetes", "deployment", "service", "monitoring",
		"configuration", "management", "performance", "optimization", "algorithm",
		"distributed", "architecture", "container", "orchestration", "scalability",
		"observability", "resilience", "microservice", "integration", "pipeline",
	}
	var builder strings.Builder
	for i := 0; i < 2000; i++ {
		if i > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(words[i%len(words)])
	}
	text := builder.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fc.extractKeywords(text, 10)
	}
}

func BenchmarkSplitSentences(b *testing.B) {
	text := strings.Repeat("This is a sample sentence for benchmarking. ", 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		splitSentences(text)
	}
}
