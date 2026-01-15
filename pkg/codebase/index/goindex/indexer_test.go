package goindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexer_IndexFile_FunctionMethodAndType(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	src := `package main

import "fmt"

type Greeter struct{}

func Add(a int, b int) int {
	return a + b
}

func (g *Greeter) Hello() {
	fmt.Println("hi")
}
`
	absPath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(absPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ix := New()
	chunks, err := ix.IndexFile(context.Background(), tmpDir, absPath, "repo123")
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks) < 3 {
		t.Fatalf("expected >= 3 chunks, got %d", len(chunks))
	}

	var (
		gotType   bool
		gotAdd    bool
		gotHello  bool
		helloCall bool
	)
	for _, ch := range chunks {
		if ch.FilePath != "main.go" {
			t.Fatalf("expected file_path main.go, got %q", ch.FilePath)
		}
		if ch.RepoID != "repo123" {
			t.Fatalf("expected repo_id repo123, got %q", ch.RepoID)
		}
		if ch.Language != "go" {
			t.Fatalf("expected language go, got %q", ch.Language)
		}
		if ch.ID == "" {
			t.Fatalf("expected non-empty id for chunk %q", ch.Name)
		}
		if ch.SchemaVer == "" {
			t.Fatalf("expected schema_version set for chunk %q", ch.Name)
		}

		switch ch.Name {
		case "Greeter":
			gotType = true
			if ch.ChunkType != "class" {
				t.Fatalf("expected Greeter chunk_type class, got %q", ch.ChunkType)
			}
		case "Add":
			gotAdd = true
			if ch.ChunkType != "function" {
				t.Fatalf("expected Add chunk_type function, got %q", ch.ChunkType)
			}
		case "Hello":
			gotHello = true
			if ch.ChunkType != "method" {
				t.Fatalf("expected Hello chunk_type method, got %q", ch.ChunkType)
			}
			if ch.ParentName != "Greeter" {
				t.Fatalf("expected Hello parent_name Greeter, got %q", ch.ParentName)
			}
			if ch.ParentType != "class" {
				t.Fatalf("expected Hello parent_type class, got %q", ch.ParentType)
			}
			for _, c := range ch.Calls {
				if c == "fmt.Println" {
					helloCall = true
				}
			}
		}
	}

	if !gotType {
		t.Fatal("expected Greeter type chunk")
	}
	if !gotAdd {
		t.Fatal("expected Add function chunk")
	}
	if !gotHello {
		t.Fatal("expected Hello method chunk")
	}
	if !helloCall {
		t.Fatal("expected Hello to include call fmt.Println")
	}

	// IDs should be stable for the same file contents.
	chunks2, err := ix.IndexFile(context.Background(), tmpDir, absPath, "repo123")
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks2) != len(chunks) {
		t.Fatalf("expected same chunk count on second pass; got %d vs %d", len(chunks2), len(chunks))
	}
	for i := range chunks {
		if chunks[i].Name == chunks2[i].Name && chunks[i].ID != chunks2[i].ID {
			t.Fatalf("expected stable ID for %q: %q != %q", chunks[i].Name, chunks[i].ID, chunks2[i].ID)
		}
	}
}
