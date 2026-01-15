package tsindex

import (
	"context"
	"os"
	"path/filepath"
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

	var gotHello, gotGreeter, gotGreet bool
	for _, ch := range chunks {
		switch ch.Name {
		case "hello":
			gotHello = true
			if ch.ChunkType != "function" {
				t.Fatalf("expected hello chunk_type function, got %q", ch.ChunkType)
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
		}
	}
	if !gotHello || !gotGreeter || !gotGreet {
		t.Fatalf("missing chunks: hello=%v Greeter=%v greet=%v", gotHello, gotGreeter, gotGreet)
	}
}
