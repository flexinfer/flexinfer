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
	src := `//! Crate docs.

use std::fmt;

struct Greeter;

/// Says hello.
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

	var gotModule, gotGreeter, gotHello, gotGreet, gotSay bool
	for _, ch := range chunks {
		if ch.ChunkType == "module" {
			gotModule = true
			if ch.Docstring != "Crate docs." {
				t.Fatalf("unexpected module docstring: %q", ch.Docstring)
			}
			continue
		}
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
			if ch.Docstring != "Says hello." {
				t.Fatalf("unexpected hello docstring: %q", ch.Docstring)
			}
			if ch.Signature == "" {
				t.Fatal("expected hello signature set")
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
	if !gotModule || !gotGreeter || !gotHello || !gotGreet || !gotSay {
		t.Fatalf("missing chunks: module=%v Greeter=%v hello=%v greet=%v say=%v", gotModule, gotGreeter, gotHello, gotGreet, gotSay)
	}
}
