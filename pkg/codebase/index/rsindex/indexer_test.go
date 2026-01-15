package rsindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexer_Rust_StructFnImpl(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	src := `use std::fmt;

struct Greeter;

fn hello() {
    greet();
}

fn greet() {}

impl Greeter {
    fn say(&self) {
        hello();
    }
}
`
	path := filepath.Join(tmp, "test.rs")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ix := New()
	chunks, err := ix.IndexFile(context.Background(), tmp, path, "repo123")
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 4 {
		t.Fatalf("expected >= 4 chunks, got %d", len(chunks))
	}

	var gotGreeter, gotHello, gotGreet, gotSay bool
	for _, ch := range chunks {
		switch ch.Name {
		case "Greeter":
			gotGreeter = true
			if ch.ChunkType != "class" {
				t.Fatalf("expected Greeter chunk_type class, got %q", ch.ChunkType)
			}
		case "hello":
			gotHello = true
			if ch.ChunkType != "function" {
				t.Fatalf("expected hello chunk_type function, got %q", ch.ChunkType)
			}
		case "greet":
			gotGreet = true
		case "say":
			gotSay = true
			if ch.ChunkType != "method" {
				t.Fatalf("expected say chunk_type method, got %q", ch.ChunkType)
			}
			if ch.ParentName == "" {
				t.Fatal("expected say parent_name set")
			}
		}
	}
	if !gotGreeter || !gotHello || !gotGreet || !gotSay {
		t.Fatalf("missing chunks: Greeter=%v hello=%v greet=%v say=%v", gotGreeter, gotHello, gotGreet, gotSay)
	}
}
