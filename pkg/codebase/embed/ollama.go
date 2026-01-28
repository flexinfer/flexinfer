package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/crb2nu/loom/pkg/httpclient"
)

// OllamaClient implements Embedder using the Ollama API.
type OllamaClient struct {
	http    *httpclient.Client
	baseURL string
	model   string
}

// Ensure OllamaClient implements Embedder.
var _ Embedder = (*OllamaClient)(nil)

// NewOllamaClient creates an Ollama embedder client.
// baseURL is typically "http://localhost:11434" for local Ollama.
// model is the embedding model name (e.g., "nomic-embed-text", "mxbai-embed-large").
func NewOllamaClient(httpc *httpclient.Client, baseURL, model string) *OllamaClient {
	if model == "" {
		model = "nomic-embed-text"
	}
	return &OllamaClient{
		http:    httpc,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
	}
}

// Name returns the embedder name.
func (c *OllamaClient) Name() string {
	return "ollama"
}

// Model returns the model identifier.
func (c *OllamaClient) Model() string {
	return c.model
}

// EmbedQuery embeds a single query string.
func (c *OllamaClient) EmbedQuery(ctx context.Context, query string) ([]float64, error) {
	vecs, err := c.EmbedDocuments(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("unexpected embeddings length: %d", len(vecs))
	}
	return vecs[0], nil
}

// EmbedDocuments embeds multiple documents.
// Note: Ollama's /api/embed endpoint processes one at a time, so we batch sequentially.
func (c *OllamaClient) EmbedDocuments(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return [][]float64{}, nil
	}

	results := make([][]float64, 0, len(texts))
	for _, text := range texts {
		vec, err := c.embedSingle(ctx, text)
		if err != nil {
			return nil, err
		}
		results = append(results, vec)
	}
	return results, nil
}

func (c *OllamaClient) embedSingle(ctx context.Context, text string) ([]float64, error) {
	payload := map[string]any{
		"model":  c.model,
		"prompt": text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embeddings", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("ollama API HTTP %d: %s", resp.StatusCode, truncateBody(respBody, 500))
	}

	var decoded struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(decoded.Embedding) == 0 {
		return nil, fmt.Errorf("ollama returned empty embedding")
	}

	return decoded.Embedding, nil
}

func truncateBody(body []byte, maxLen int) string {
	s := strings.TrimSpace(string(body))
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
