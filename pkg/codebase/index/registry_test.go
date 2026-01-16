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
