// qdrant.go -- Qdrant HTTP client core: types, constructor, and JSON transport.
package agentcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/crb2nu/loom/pkg/httpclient"
)

const maxQdrantResponseBytes = 10 * 1024 * 1024 // 10MB

var ErrCollectionNotFound = errors.New("qdrant collection not found")

type QdrantClient struct {
	http       *httpclient.Client
	baseURL    string
	apiKey     string
	collection string
	distance   string
}

type Point struct {
	ID      string
	Vector  []float64
	Payload map[string]any
}

type RawPoint struct {
	ID      string
	Vector  []float64
	Payload map[string]any
}

func NewQdrantClient(httpc *httpclient.Client, baseURL, apiKey, collection, distance string) *QdrantClient {
	return &QdrantClient{
		http:       httpc,
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		collection: collection,
		distance:   distance,
	}
}

func (c *QdrantClient) SetCollection(collection string) {
	c.collection = collection
}

func (c *QdrantClient) setHeaders(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
}

func (c *QdrantClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	url := c.baseURL + "/" + strings.TrimPrefix(path, "/")

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewBuffer(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxQdrantResponseBytes))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound && strings.Contains(path, "/collections/"+c.collection) {
		// Qdrant uses 404 for both missing collections and missing points.
		// Best-effort: only treat as "collection not found" if the response mentions a collection.
		lower := bytes.ToLower(respBody)
		if bytes.Contains(lower, []byte("collection")) {
			return ErrCollectionNotFound
		}
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("qdrant HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("parse qdrant response: %w", err)
	}
	return nil
}
