//go:build cgo

package pyindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexer_Python_FunctionAndClass(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	src := `"""Test module."""

import os
from pathlib import Path

def hello(name: str) -> str:
    """Say hello to someone."""
    return name.upper()

class Greeter:
    """A greeter."""

    def greet(self, who: str) -> str:
        """Greet a person."""
        return hello(who)
`
	path := filepath.Join(tmp, "test.py")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ix := New()
	chunks, err := ix.IndexFile(context.Background(), tmp, path, "repo123")
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 3 {
		t.Fatalf("expected >= 3 chunks, got %d", len(chunks))
	}

	var gotModule, gotHello, gotGreeter, gotGreet bool
	for _, ch := range chunks {
		if ch.ChunkType == "module" {
			gotModule = true
			if ch.Docstring != "Test module." {
				t.Fatalf("unexpected module docstring: %q", ch.Docstring)
			}
			continue
		}
		switch ch.Name {
		case "hello":
			gotHello = true
			if ch.Docstring != "Say hello to someone." {
				t.Fatalf("unexpected hello docstring: %q", ch.Docstring)
			}
			if ch.ChunkType != "function" {
				t.Fatalf("expected hello chunk_type function, got %q", ch.ChunkType)
			}
		case "Greeter":
			gotGreeter = true
			if ch.Docstring != "A greeter." {
				t.Fatalf("unexpected Greeter docstring: %q", ch.Docstring)
			}
			if ch.ChunkType != "class" {
				t.Fatalf("expected Greeter chunk_type class, got %q", ch.ChunkType)
			}
		case "greet":
			gotGreet = true
			if ch.ChunkType != "method" {
				t.Fatalf("expected greet chunk_type method, got %q", ch.ChunkType)
			}
			if ch.ParentName != "Greeter" {
				t.Fatalf("expected greet parent_name Greeter, got %q", ch.ParentName)
			}
		}
	}

	if !gotModule || !gotHello || !gotGreeter || !gotGreet {
		t.Fatalf("missing chunks: module=%v hello=%v Greeter=%v greet=%v", gotModule, gotHello, gotGreeter, gotGreet)
	}
}
