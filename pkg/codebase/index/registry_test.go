package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectFiles_RespectsDefaultExcludes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	mustWrite := func(rel string) {
		t.Helper()
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mustWrite("app.py")
	mustWrite(".venv/ignored.py")
	mustWrite("node_modules/pkg/index.js")
	mustWrite("vendor/foo.go")
	mustWrite(".worktrees/wt/foo.go")
	mustWrite("src/main.go")

	r := NewRegistry(0)
	files, err := r.CollectFiles(root, []string{"python", "javascript", "go"}, nil)
	if err != nil {
		t.Fatalf("CollectFiles: %v", err)
	}

	got := map[string]bool{}
	for _, abs := range files {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		got[filepath.ToSlash(rel)] = true
	}

	if !got["app.py"] {
		t.Fatalf("expected app.py to be collected")
	}
	if !got["src/main.go"] {
		t.Fatalf("expected src/main.go to be collected")
	}

	if got[".venv/ignored.py"] {
		t.Fatalf("expected .venv/ignored.py to be excluded")
	}
	if got["node_modules/pkg/index.js"] {
		t.Fatalf("expected node_modules/** to be excluded")
	}
	if got["vendor/foo.go"] {
		t.Fatalf("expected vendor/** to be excluded")
	}
	if got[".worktrees/wt/foo.go"] {
		t.Fatalf("expected .worktrees/** to be excluded")
	}
}

func TestCollectFiles_RespectsCustomExclude(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	abs := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := NewRegistry(0)
	files, err := r.CollectFiles(root, []string{"go"}, []string{"src/**"})
	if err != nil {
		t.Fatalf("CollectFiles: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no files, got %d", len(files))
	}
}

func TestCollectFiles_RespectsNestedGitignoreAndNegation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	absNestedGitignore := filepath.Join(nested, ".gitignore")
	if err := os.WriteFile(absNestedGitignore, []byte("*.go\n!keep.go\n"), 0o644); err != nil {
		t.Fatalf("write nested .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write root.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "ignore.go"), []byte("package nested\n"), 0o644); err != nil {
		t.Fatalf("write nested ignore.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "keep.go"), []byte("package nested\n"), 0o644); err != nil {
		t.Fatalf("write nested keep.go: %v", err)
	}

	r := NewRegistry(0)
	files, err := r.CollectFiles(root, []string{"go"}, nil)
	if err != nil {
		t.Fatalf("CollectFiles: %v", err)
	}

	got := map[string]bool{}
	for _, abs := range files {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		got[filepath.ToSlash(rel)] = true
	}

	if !got["root.go"] {
		t.Fatalf("expected root.go")
	}
	if got["src/ignore.go"] {
		t.Fatalf("expected src/ignore.go to be ignored by nested .gitignore")
	}
	if !got["src/keep.go"] {
		t.Fatalf("expected src/keep.go to be re-included by nested ! rule")
	}
}

func TestCollectFiles_ExplicitNegationOverridesNestedGitignore(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(nested, ".gitignore"), []byte("*.go\n"), 0o644); err != nil {
		t.Fatalf("write nested .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "ignored.go"), []byte("package ignored\n"), 0o644); err != nil {
		t.Fatalf("write ignored.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "kept.go"), []byte("package kept\n"), 0o644); err != nil {
		t.Fatalf("write kept.go: %v", err)
	}

	r := NewRegistry(0)
	files, err := r.CollectFiles(root, []string{"go"}, []string{"!src/kept.go"})
	if err != nil {
		t.Fatalf("CollectFiles: %v", err)
	}

	got := map[string]bool{}
	for _, abs := range files {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		got[filepath.ToSlash(rel)] = true
	}

	if got["src/ignored.go"] {
		t.Fatalf("expected src/ignored.go to remain ignored by nested .gitignore")
	}
	if !got["src/kept.go"] {
		t.Fatalf("expected src/kept.go to be re-included by explicit ! rule")
	}
}
