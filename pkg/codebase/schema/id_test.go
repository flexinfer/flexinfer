package schema

import "testing"

func TestChunkID_Stable(t *testing.T) {
	t.Parallel()

	h := ContentHash("hello")
	id1 := ChunkID("r", "f.go", 1, 2, h)
	id2 := ChunkID("r", "f.go", 1, 2, h)
	if id1 != id2 {
		t.Fatalf("expected stable id: %q != %q", id1, id2)
	}
}

func TestContentHashBytes_MatchesString(t *testing.T) {
	t.Parallel()

	content := []byte("package main\n\nfunc main() {}\n")
	if got, want := ContentHashBytes(content), ContentHash(string(content)); got != want {
		t.Fatalf("ContentHashBytes=%q want %q", got, want)
	}
}
