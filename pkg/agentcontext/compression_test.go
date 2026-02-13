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
