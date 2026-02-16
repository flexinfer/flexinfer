package skills

import (
	"testing"
)

func TestToTitleCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-skill", "My Skill"},
		{"deploy", "Deploy"},
		{"agent-context", "Agent Context"},
		{"coding-standards", "Coding Standards"},
		{"", ""},
		{"a", "A"},
		{"already", "Already"},
		{"multi-word-skill-name", "Multi Word Skill Name"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toTitleCase(tt.input)
			if got != tt.want {
				t.Errorf("toTitleCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStripFirstHeader_RemovesHeader(t *testing.T) {
	input := "# My Skill\n\nDo the thing.\nMore instructions."
	got := stripFirstHeader(input)

	if got != "Do the thing.\nMore instructions." {
		t.Errorf("unexpected output:\n%s", got)
	}
}

func TestStripFirstHeader_RemovesBlankLinesAfterHeader(t *testing.T) {
	input := "# My Skill\n\n\n\nFirst real line."
	got := stripFirstHeader(input)

	if got != "First real line." {
		t.Errorf("expected blank lines after header removed, got:\n%q", got)
	}
}

func TestStripFirstHeader_NoHeader(t *testing.T) {
	input := "No header here.\nJust text."
	got := stripFirstHeader(input)

	// Should return unchanged if no header found.
	if got != input {
		t.Errorf("expected unchanged input, got:\n%q", got)
	}
}

func TestStripFirstHeader_OnlyHeader(t *testing.T) {
	input := "# Just A Header"
	got := stripFirstHeader(input)

	// After stripping the header, nothing remains, so the original is returned.
	if got != input {
		t.Errorf("expected original for header-only input, got:\n%q", got)
	}
}

func TestStripFirstHeader_HeaderWithLeadingWhitespace(t *testing.T) {
	input := "  # My Skill\n\nContent."
	got := stripFirstHeader(input)

	if got != "Content." {
		t.Errorf("expected leading whitespace stripped header recognized, got:\n%q", got)
	}
}

func TestStripFirstHeader_PreservesSubsequentHeaders(t *testing.T) {
	input := "# Main Title\n\n## Section One\n\nText here.\n\n## Section Two\n\nMore text."
	got := stripFirstHeader(input)

	if got != "## Section One\n\nText here.\n\n## Section Two\n\nMore text." {
		t.Errorf("expected subsequent headers preserved, got:\n%s", got)
	}
}

func TestStripFirstHeader_EmptyString(t *testing.T) {
	got := stripFirstHeader("")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestEscapeYAMLString_NoSpecialChars(t *testing.T) {
	got := escapeYAMLString("simple string")
	if got != "simple string" {
		t.Errorf("expected unchanged, got %q", got)
	}
}

func TestEscapeYAMLString_Quotes(t *testing.T) {
	got := escapeYAMLString(`say "hello"`)
	want := `say \"hello\"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeYAMLString_Backslashes(t *testing.T) {
	got := escapeYAMLString(`path\to\file`)
	want := `path\\to\\file`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeYAMLString_Mixed(t *testing.T) {
	got := escapeYAMLString(`C:\Users\"test"`)
	want := `C:\\Users\\\"test\"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEscapeYAMLString_Empty(t *testing.T) {
	got := escapeYAMLString("")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestAllTargets_ContainsExpectedPlatforms(t *testing.T) {
	expected := map[string]bool{
		"codex":    false,
		"claude":   false,
		"kilocode": false,
		"gemini":   false,
	}

	for _, target := range AllTargets {
		if _, ok := expected[target]; ok {
			expected[target] = true
		}
	}

	for platform, found := range expected {
		if !found {
			t.Errorf("expected %q in AllTargets", platform)
		}
	}

	if len(AllTargets) != 4 {
		t.Errorf("expected 4 targets, got %d", len(AllTargets))
	}
}
