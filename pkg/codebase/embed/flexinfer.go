package embed

import (
	"context"

	"github.com/crb2nu/loom/pkg/httpclient"
)

// FlexInferClient implements Embedder using a FlexInfer TEI backend.
// It wraps MorphClient since FlexInfer exposes an OpenAI-compatible
// /v1/embeddings endpoint.
type FlexInferClient struct {
	inner *MorphClient
}

// Ensure FlexInferClient implements Embedder.
var _ Embedder = (*FlexInferClient)(nil)

// NewFlexInferClient creates a FlexInfer embedder client.
// baseURL is typically "http://flexinfer-proxy:8080/v1" or similar.
// apiKey is optional (TEI backends often don't require auth).
// model defaults to "BAAI/bge-large-en-v1.5" if empty.
func NewFlexInferClient(httpc *httpclient.Client, baseURL, apiKey, model string) *FlexInferClient {
	if model == "" {
		model = "BAAI/bge-large-en-v1.5"
	}
	// TEI backends typically don't require auth. Use a placeholder key
	// so the underlying MorphClient doesn't reject the request.
	if apiKey == "" {
		apiKey = "not-required"
	}
	return &FlexInferClient{
		inner: NewMorphClient(httpc, baseURL, apiKey, model),
	}
}

// Name returns "flexinfer" to distinguish from the morph provider.
func (c *FlexInferClient) Name() string {
	return "flexinfer"
}

// Model returns the model identifier.
func (c *FlexInferClient) Model() string {
	return c.inner.Model()
}

// EmbedQuery embeds a single query string via the FlexInfer TEI backend.
func (c *FlexInferClient) EmbedQuery(ctx context.Context, query string) ([]float64, error) {
	return c.inner.EmbedQuery(ctx, query)
}

// EmbedDocuments embeds multiple documents via the FlexInfer TEI backend.
func (c *FlexInferClient) EmbedDocuments(ctx context.Context, texts []string) ([][]float64, error) {
	return c.inner.EmbedDocuments(ctx, texts)
}
