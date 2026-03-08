package agentcontext

import (
	"testing"
)

func TestNewTrustedSourceRegistry(t *testing.T) {
	sources := []TrustedSource{
		{Pattern: "*.md", Priority: 0.9, Description: "Markdown"},
	}
	reg := NewTrustedSourceRegistry(sources)
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
	if len(reg.sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(reg.sources))
	}
}

func TestGetPriority_EmptyPath(t *testing.T) {
	reg := NewTrustedSourceRegistry(defaultTrustedSources())
	if p := reg.GetPriority(""); p != 0.5 {
		t.Errorf("expected 0.5 for empty path, got %f", p)
	}
}

func TestGetPriority_MatchingPatterns(t *testing.T) {
	reg := NewTrustedSourceRegistry([]TrustedSource{
		{Pattern: "*.md", Priority: 0.9, Description: "Markdown"},
		{Pattern: "README*", Priority: 0.95, Description: "README"},
		{Pattern: "*.go", Priority: 0.7, Description: "Go files"},
	})

	tests := []struct {
		path     string
		expected float64
	}{
		{"README.md", 0.95},
		{"docs/guide.md", 0.9},
		{"main.go", 0.7},
		{"image.png", 0.5}, // no match
	}

	for _, tc := range tests {
		got := reg.GetPriority(tc.path)
		if got != tc.expected {
			t.Errorf("GetPriority(%q) = %f, want %f", tc.path, got, tc.expected)
		}
	}
}

func TestGetPriority_DirectoryPatterns(t *testing.T) {
	reg := NewTrustedSourceRegistry([]TrustedSource{
		{Pattern: "src/**/test", Priority: 0.8, Description: "Test dirs"},
	})

	got := reg.GetPriority("src/pkg/test")
	if got < 0.5 {
		t.Errorf("expected boost for directory pattern match, got %f", got)
	}
}

func TestIsTrusted(t *testing.T) {
	reg := NewTrustedSourceRegistry([]TrustedSource{
		{Pattern: "*.md", Priority: 0.9, Description: "Markdown"},
		{Pattern: "*.txt", Priority: 0.3, Description: "Text files"},
	})

	if !reg.IsTrusted("README.md", 0.8) {
		t.Error("expected README.md to be trusted at threshold 0.8")
	}
	if reg.IsTrusted("notes.txt", 0.8) {
		t.Error("expected notes.txt to NOT be trusted at threshold 0.8")
	}
	if !reg.IsTrusted("notes.txt", 0.3) {
		t.Error("expected notes.txt to be trusted at threshold 0.3")
	}
	if reg.IsTrusted("image.png", 0.6) {
		t.Error("expected image.png to NOT be trusted")
	}
}

func TestGetMatchingPatterns(t *testing.T) {
	reg := NewTrustedSourceRegistry([]TrustedSource{
		{Pattern: "*.md", Priority: 0.9, Description: "Markdown"},
		{Pattern: "README*", Priority: 0.95, Description: "README"},
		{Pattern: "*.go", Priority: 0.7, Description: "Go files"},
	})

	matches := reg.GetMatchingPatterns("README.md")
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches for README.md, got %d", len(matches))
	}

	matches = reg.GetMatchingPatterns("")
	if matches != nil {
		t.Error("expected nil for empty path")
	}

	matches = reg.GetMatchingPatterns("image.png")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for image.png, got %d", len(matches))
	}
}

func TestAddSource(t *testing.T) {
	reg := NewTrustedSourceRegistry(nil)
	reg.AddSource(TrustedSource{Pattern: "*.rs", Priority: 0.8, Description: "Rust"})
	if len(reg.sources) != 1 {
		t.Fatalf("expected 1 source after add, got %d", len(reg.sources))
	}
	if p := reg.GetPriority("main.rs"); p != 0.8 {
		t.Errorf("expected 0.8 for main.rs, got %f", p)
	}
}

func TestRemoveSource(t *testing.T) {
	reg := NewTrustedSourceRegistry([]TrustedSource{
		{Pattern: "*.md", Priority: 0.9},
		{Pattern: "*.go", Priority: 0.7},
	})

	if !reg.RemoveSource("*.md") {
		t.Error("expected RemoveSource to return true for existing pattern")
	}
	if len(reg.sources) != 1 {
		t.Fatalf("expected 1 source after remove, got %d", len(reg.sources))
	}
	if reg.RemoveSource("*.nonexistent") {
		t.Error("expected RemoveSource to return false for non-existing pattern")
	}
}

func TestListSources(t *testing.T) {
	orig := []TrustedSource{
		{Pattern: "*.md", Priority: 0.9},
		{Pattern: "*.go", Priority: 0.7},
	}
	reg := NewTrustedSourceRegistry(orig)
	listed := reg.ListSources()

	if len(listed) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(listed))
	}

	// Verify it's a copy (mutating returned slice shouldn't affect registry)
	listed[0].Priority = 0.0
	if reg.sources[0].Priority == 0.0 {
		t.Error("ListSources should return a copy, not the original slice")
	}
}

func TestApplyTrustBoost(t *testing.T) {
	reg := NewTrustedSourceRegistry([]TrustedSource{
		{Pattern: "*.md", Priority: 0.9, Description: "Markdown"},
	})

	results := []SearchResult{
		{Score: 1.0, Entry: ContextEntry{FilePath: "README.md"}},
		{Score: 1.0, Entry: ContextEntry{FilePath: "main.go"}},
	}

	boosted := reg.ApplyTrustBoost(results)

	// README.md should get a boost (priority 0.9, boost = 1 + (0.9-0.5)*0.5 = 1.2)
	if boosted[0].Score <= 1.0 {
		t.Errorf("expected boosted score > 1.0 for README.md, got %f", boosted[0].Score)
	}
	// main.go should stay neutral (priority 0.5, boost = 1.0)
	if boosted[1].Score != 1.0 {
		t.Errorf("expected neutral score 1.0 for main.go, got %f", boosted[1].Score)
	}
}

func TestApplyTrustBoostToContextEntries(t *testing.T) {
	reg := NewTrustedSourceRegistry([]TrustedSource{
		{Pattern: "*.md", Priority: 0.9},
		{Pattern: "*.go", Priority: 0.7},
	})

	entries := []ContextEntry{
		{FilePath: "main.go"},
		{FilePath: "README.md"},
		{FilePath: "image.png"},
	}

	sorted := reg.ApplyTrustBoostToContextEntries(entries)
	if len(sorted) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(sorted))
	}
	// README.md (0.9) should be first, main.go (0.7) second, image.png (0.5) last
	if sorted[0].FilePath != "README.md" {
		t.Errorf("expected README.md first, got %s", sorted[0].FilePath)
	}
	if sorted[1].FilePath != "main.go" {
		t.Errorf("expected main.go second, got %s", sorted[1].FilePath)
	}
	if sorted[2].FilePath != "image.png" {
		t.Errorf("expected image.png last, got %s", sorted[2].FilePath)
	}
}

func TestDefaultTrustedSources(t *testing.T) {
	sources := defaultTrustedSources()
	if len(sources) == 0 {
		t.Fatal("expected non-empty default trusted sources")
	}
	// Verify some expected defaults exist
	found := false
	for _, s := range sources {
		if s.Pattern == "*.md" {
			found = true
			if s.Priority != 0.9 {
				t.Errorf("expected *.md priority 0.9, got %f", s.Priority)
			}
			break
		}
	}
	if !found {
		t.Error("expected *.md in default trusted sources")
	}
}
