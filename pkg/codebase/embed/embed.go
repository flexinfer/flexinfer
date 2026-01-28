// Package embed provides embedding interfaces and implementations for codebase indexing.
package embed

import (
	"context"
)

// Embedder is the interface for embedding providers.
type Embedder interface {
	// EmbedQuery embeds a single query string.
	EmbedQuery(ctx context.Context, query string) ([]float64, error)

	// EmbedDocuments embeds multiple documents in a batch.
	EmbedDocuments(ctx context.Context, texts []string) ([][]float64, error)

	// Name returns the embedder name (for logging/debugging).
	Name() string

	// Model returns the model identifier being used.
	Model() string
}

// DummyEmbedder returns zero vectors with a specified dimension.
// Useful for indexing without embeddings (structure-only mode).
type DummyEmbedder struct {
	dimension int
}

// NewDummyEmbedder creates a dummy embedder that returns zero vectors.
func NewDummyEmbedder(dimension int) *DummyEmbedder {
	if dimension <= 0 {
		dimension = 1
	}
	return &DummyEmbedder{dimension: dimension}
}

func (d *DummyEmbedder) EmbedQuery(ctx context.Context, query string) ([]float64, error) {
	return d.makeVector(), nil
}

func (d *DummyEmbedder) EmbedDocuments(ctx context.Context, texts []string) ([][]float64, error) {
	result := make([][]float64, len(texts))
	for i := range texts {
		result[i] = d.makeVector()
	}
	return result, nil
}

func (d *DummyEmbedder) Name() string {
	return "dummy"
}

func (d *DummyEmbedder) Model() string {
	return "none"
}

func (d *DummyEmbedder) makeVector() []float64 {
	v := make([]float64, d.dimension)
	if len(v) > 0 {
		v[0] = 1 // Non-zero for Qdrant compatibility
	}
	return v
}

// Dimension returns the vector dimension.
func (d *DummyEmbedder) Dimension() int {
	return d.dimension
}
