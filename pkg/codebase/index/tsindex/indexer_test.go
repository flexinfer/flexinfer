//go:build cgo

package tsindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexer_TypeScript_Basics(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	src := `import { foo } from "lib";

export function hello(name: string): string {
  return foo(name);
}

class Greeter {
  greet(): string {
    return hello("world");
  }
}
`
	path := filepath.Join(tmp, "test.ts")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	ix := NewTypeScript()
	chunks, err := ix.IndexFile(context.Background(), tmp, path, "repo123")
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}

	var gotModule, gotHello, gotGreeter, gotGreet bool
	for _, ch := range chunks {
		if ch.ChunkType == "module" {
			gotModule = true
			continue
		}
		switch ch.Name {
		case "hello":
			gotHello = true
			if ch.ChunkType != "function" {
				t.Fatalf("expected hello chunk_type function, got %q", ch.ChunkType)
			}
			if ch.Signature == "" || !strings.Contains(ch.Signature, "hello") {
				t.Fatalf("expected hello signature to contain name, got %q", ch.Signature)
			}
		case "Greeter":
			gotGreeter = true
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
			if ch.Signature == "" || !strings.Contains(ch.Signature, "greet") {
				t.Fatalf("expected greet signature to contain name, got %q", ch.Signature)
			}
		}
	}
	if !gotModule || !gotHello || !gotGreeter || !gotGreet {
		t.Fatalf("missing chunks: module=%v hello=%v Greeter=%v greet=%v", gotModule, gotHello, gotGreeter, gotGreet)
	}
}
