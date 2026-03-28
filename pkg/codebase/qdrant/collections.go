package qdrant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// FilePreflight holds pre-indexed metadata for a single file, combining
// the module-level content hash and any cached embedding vectors.
type FilePreflight struct {
	ModuleContentHash string
	ModuleFound       bool
	EmbeddingCache    map[string][]float64
}

func (c *Client) CollectionExists(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/collections/"+c.collection, nil)
	if err != nil {
		return false, err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("qdrant HTTP %d", resp.StatusCode)
	}
	return true, nil
}

func (c *Client) GetCollectionVectorSize(ctx context.Context) (exists bool, size int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/collections/"+c.collection, nil)
	if err != nil {
		return false, 0, err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, 0, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, 0, nil
	}
	if resp.StatusCode >= 400 {
		return false, 0, fmt.Errorf("qdrant HTTP %d", resp.StatusCode)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return false, 0, fmt.Errorf("parse qdrant response: %w", err)
	}

	// Qdrant response shape (v1+):
	// result.config.params.vectors.size
	// Or: result.config.params.vectors.<name>.size (named vectors).
	getMap := func(v any) (map[string]any, bool) {
		m, ok := v.(map[string]any)
		return m, ok
	}

	result, ok := getMap(raw["result"])
	if !ok {
		return true, 0, fmt.Errorf("parse qdrant response: missing result")
	}
	cfg, ok := getMap(result["config"])
	if !ok {
		return true, 0, fmt.Errorf("parse qdrant response: missing result.config")
	}
	params, ok := getMap(cfg["params"])
	if !ok {
		return true, 0, fmt.Errorf("parse qdrant response: missing result.config.params")
	}
	vectors := params["vectors"]
	if vectors == nil {
		return true, 0, fmt.Errorf("parse qdrant response: missing result.config.params.vectors")
	}

	if sz, ok := parseVectorSize(vectors); ok {
		return true, sz, nil
	}
	if m, ok := getMap(vectors); ok {
		for _, v := range m {
			if sz, ok := parseVectorSize(v); ok {
				return true, sz, nil
			}
		}
	}

	return true, 0, fmt.Errorf("parse qdrant response: could not determine vector size")
}

func parseVectorSize(v any) (int, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return 0, false
	}
	switch n := m["size"].(type) {
	case float64:
		if n > 0 {
			return int(n), true
		}
	case int:
		if n > 0 {
			return n, true
		}
	}
	return 0, false
}

func (c *Client) EnsureCollection(ctx context.Context, vectorSize int) error {
	exists, existingSize, err := c.GetCollectionVectorSize(ctx)
	if err != nil {
		return err
	}
	if exists {
		if existingSize > 0 && vectorSize > 0 && existingSize != vectorSize {
			return &VectorSizeMismatchError{
				Collection: c.collection,
				Existing:   existingSize,
				Expected:   vectorSize,
			}
		}
		return nil
	}
	if vectorSize <= 0 {
		return fmt.Errorf("invalid vectorSize %d", vectorSize)
	}

	body := map[string]any{
		"vectors": map[string]any{
			"size":     vectorSize,
			"distance": c.distance,
		},
	}
	return c.doJSON(ctx, http.MethodPut, "/collections/"+c.collection, body, nil)
}

func (c *Client) RecreateCollection(ctx context.Context, vectorSize int) error {
	if vectorSize <= 0 {
		return fmt.Errorf("invalid vectorSize %d", vectorSize)
	}
	if err := c.doJSON(ctx, http.MethodDelete, "/collections/"+c.collection, nil, nil); err != nil && !errors.Is(err, ErrCollectionNotFound) {
		return err
	}
	return c.EnsureCollection(ctx, vectorSize)
}

func (c *Client) DeleteRepo(ctx context.Context, repoID string) error {
	return c.deleteByFilter(ctx, filterMust(
		match("repo_id", repoID),
	))
}

func (c *Client) DeleteFile(ctx context.Context, repoID, filePath string) error {
	return c.deleteByFilter(ctx, filterMust(
		match("repo_id", repoID),
		match("file_path", filePath),
	))
}

func (c *Client) deleteByFilter(ctx context.Context, filter map[string]any) error {
	path := fmt.Sprintf("/collections/%s/points/delete", c.collection)
	body := map[string]any{"filter": filter}
	return c.doJSON(ctx, http.MethodPost, path, body, nil)
}
