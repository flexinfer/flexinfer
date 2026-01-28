package embed

import (
	"context"
	"testing"
)

func TestDummyEmbedder(t *testing.T) {
	ctx := context.Background()

	t.Run("default dimension", func(t *testing.T) {
		e := NewDummyEmbedder(0)
		if e.Dimension() != 1 {
			t.Errorf("expected dimension 1, got %d", e.Dimension())
		}
	})

	t.Run("custom dimension", func(t *testing.T) {
		e := NewDummyEmbedder(384)
		if e.Dimension() != 384 {
			t.Errorf("expected dimension 384, got %d", e.Dimension())
		}
	})

	t.Run("embed query", func(t *testing.T) {
		e := NewDummyEmbedder(128)
		vec, err := e.EmbedQuery(ctx, "test query")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(vec) != 128 {
			t.Errorf("expected vector length 128, got %d", len(vec))
		}
		// First element should be non-zero
		if vec[0] != 1 {
			t.Errorf("expected first element to be 1, got %f", vec[0])
		}
	})

	t.Run("embed documents", func(t *testing.T) {
		e := NewDummyEmbedder(64)
		texts := []string{"doc 1", "doc 2", "doc 3"}
		vecs, err := e.EmbedDocuments(ctx, texts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(vecs) != 3 {
			t.Errorf("expected 3 vectors, got %d", len(vecs))
		}
		for i, vec := range vecs {
			if len(vec) != 64 {
				t.Errorf("vector %d: expected length 64, got %d", i, len(vec))
			}
		}
	})

	t.Run("name and model", func(t *testing.T) {
		e := NewDummyEmbedder(128)
		if e.Name() != "dummy" {
			t.Errorf("expected name 'dummy', got %q", e.Name())
		}
		if e.Model() != "none" {
			t.Errorf("expected model 'none', got %q", e.Model())
		}
	})

	t.Run("empty documents", func(t *testing.T) {
		e := NewDummyEmbedder(128)
		vecs, err := e.EmbedDocuments(ctx, []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(vecs) != 0 {
			t.Errorf("expected 0 vectors, got %d", len(vecs))
		}
	})
}

func TestEmbedderInterface(t *testing.T) {
	// Verify interface compliance at compile time
	var _ Embedder = (*DummyEmbedder)(nil)
	var _ Embedder = (*MorphClient)(nil)
}
