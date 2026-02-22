package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGitignorePattern_CommentsAndWhitespace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		line         string
		wantPattern  string
		wantNegate   bool
		wantHasMatch bool
	}{
		{"", "", false, false},
		{"#", "", false, false},
		{"   # comment", "", false, false},
		{"  src/*.go  ", "src/*.go", false, true},
		{"  ! src/*.go", "src/*.go", true, true},
		{"\\#literal.txt", "#literal.txt", false, true},
	}

	for _, tt := range cases {
		pattern, negate, ok := parseGitignorePattern(tt.line)
		if pattern != tt.wantPattern || negate != tt.wantNegate || ok != tt.wantHasMatch {
			t.Fatalf("parse %q = %q %v %v, want %q %v %v", tt.line, pattern, negate, ok, tt.wantPattern, tt.wantNegate, tt.wantHasMatch)
		}
	}
}

func TestIgnoreMatcher_RootGitignoreNegation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.go\n!kept.go\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored.go"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write ignored.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "kept.go"), []byte("kept"), 0o644); err != nil {
		t.Fatalf("write kept.go: %v", err)
	}

	matcher := NewIgnoreMatcher(root, nil)

	if !matcher.IsIgnored("ignored.go", false) {
		t.Fatal("expected ignored.go to be ignored")
	}
	if matcher.IsIgnored("kept.go", false) {
		t.Fatal("expected kept.go to be unignored by negation")
	}
}

func TestIgnoreMatcher_NestedGitignoreNegation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	nestedIgnore := filepath.Join(nested, ".gitignore")
	if err := os.WriteFile(nestedIgnore, []byte("*.tmp\n!keep.tmp\n"), 0o644); err != nil {
		t.Fatalf("write nested .gitignore: %v", err)
	}

	if err := os.WriteFile(filepath.Join(nested, "ignored.tmp"), []byte("1"), 0o644); err != nil {
		t.Fatalf("write ignored.tmp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "keep.tmp"), []byte("2"), 0o644); err != nil {
		t.Fatalf("write keep.tmp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "keep.go"), []byte("3"), 0o644); err != nil {
		t.Fatalf("write keep.go: %v", err)
	}

	matcher := NewIgnoreMatcher(root, nil)

	if !matcher.IsIgnored("nested/ignored.tmp", false) {
		t.Fatal("expected nested/ignored.tmp to be ignored")
	}
	if matcher.IsIgnored("nested/keep.tmp", false) {
		t.Fatal("expected nested/keep.tmp to be explicitly unignored")
	}
	if matcher.IsIgnored("nested/keep.go", false) {
		t.Fatal("expected nested/keep.go not affected by nested .gitignore")
	}
}

func TestIgnoreMatcher_DefaultIgnoreCanBeOverridden(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	buildDir := filepath.Join(root, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "main.go"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	matcher := NewIgnoreMatcher(root, []string{"!build/**"})
	if matcher.IsIgnored("build/main.go", false) {
		t.Fatal("expected build/main.go to be unignored by explicit exclude negation")
	}
}
