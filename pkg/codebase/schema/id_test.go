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
