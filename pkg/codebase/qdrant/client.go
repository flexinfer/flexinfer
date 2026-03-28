package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/crb2nu/loom/pkg/httpclient"
)

var ErrCollectionNotFound = errors.New("qdrant collection not found")

const maxQdrantResponseBytes = 10 * 1024 * 1024 // 10MB

// VectorSizeMismatchError indicates that an existing collection's vector size is
// incompatible with a requested size.
type VectorSizeMismatchError struct {
	Collection string
	Existing   int
	Expected   int
}

func (e *VectorSizeMismatchError) Error() string {
	return fmt.Sprintf(
		"qdrant collection %q vector size=%d expected=%d (use CODEBASE_QDRANT_COLLECTION or delete/recreate collection)",
		e.Collection,
		e.Existing,
		e.Expected,
	)
}

func IsVectorSizeMismatch(err error) bool {
	var mismatchErr *VectorSizeMismatchError
	return errors.As(err, &mismatchErr)
}

type Client struct {
	http       *httpclient.Client
	baseURL    string
	apiKey     string
	collection string
	distance   string
}

func NewClient(httpc *httpclient.Client, baseURL, apiKey, collection, distance string) *Client {
	return &Client{
		http:       httpc,
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		collection: collection,
		distance:   distance,
	}
}

type Point struct {
	ID      string
	Vector  []float64
	Payload map[string]any
}

var nonNameCharRE = regexp.MustCompile(`[^A-Za-z0-9_]+`)

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
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
		// Qdrant may return 404 for both missing collections and point-level misses.
		// Treat as collection-missing only when the response body indicates so.
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

func (c *Client) setHeaders(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
}
