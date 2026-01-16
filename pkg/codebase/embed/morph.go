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

type MorphClient struct {
	http    *httpclient.Client
	baseURL string
	apiKey  string
	model   string
}

func NewMorphClient(httpc *httpclient.Client, baseURL, apiKey, model string) *MorphClient {
	return &MorphClient{
		http:    httpc,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
	}
}

func (c *MorphClient) EmbedQuery(ctx context.Context, query string) ([]float64, error) {
	vecs, err := c.EmbedDocuments(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("unexpected embeddings length: %d", len(vecs))
	}
	return vecs[0], nil
}

func (c *MorphClient) EmbedDocuments(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return [][]float64{}, nil
	}
	if strings.TrimSpace(c.apiKey) == "" {
		return nil, fmt.Errorf("CODEBASE_EMBED_API_KEY (or MORPH_API_KEY / OPENAI_API_KEY) is not set")
	}

	payload := map[string]any{
		"model": c.model,
		"input": texts,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("morph API HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var decoded map[string]any
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	rawData, _ := decoded["data"].([]any)
	embeddings := make([][]float64, 0, len(rawData))
	for _, item := range rawData {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		rawEmb, _ := m["embedding"].([]any)
		vec := make([]float64, 0, len(rawEmb))
		for _, v := range rawEmb {
			switch n := v.(type) {
			case float64:
				vec = append(vec, n)
			case float32:
				vec = append(vec, float64(n))
			}
		}
		embeddings = append(embeddings, vec)
	}

	if len(embeddings) != len(texts) {
		return nil, fmt.Errorf("embedding count mismatch: got %d, want %d", len(embeddings), len(texts))
	}
	return embeddings, nil
}
