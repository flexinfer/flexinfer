// qdrant_collection.go -- Qdrant collection management (exists, ensure, vector-size helpers).
package agentcontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// toPointID converts an arbitrary string ID to a valid qdrant point ID (UUID format).
// Qdrant only accepts unsigned integers or UUIDs as point IDs, but our internal IDs
// are 16-char hex strings (from GenerateID) or prefixed strings like "rc_...".
// This deterministic conversion ensures the same input always maps to the same UUID.
func toPointID(id string) string {
	h := sha256.Sum256([]byte(id))
	h[6] = (h[6] & 0x0f) | 0x50 // UUID version 5
	h[8] = (h[8] & 0x3f) | 0x80 // RFC 4122 variant
	s := hex.EncodeToString(h[:16])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// dummyRelationsVector is used for relations that don't need embeddings
var dummyRelationsVector = []float64{0, 0, 0, 0}

func (c *QdrantClient) CollectionExists(ctx context.Context) (bool, error) {
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
	_, _ = io.ReadAll(resp.Body) // drain body for connection reuse
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("qdrant HTTP %d", resp.StatusCode)
	}
	return true, nil
}

func (c *QdrantClient) GetCollectionVectorSize(ctx context.Context) (exists bool, size int, err error) {
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

func (c *QdrantClient) EnsureCollection(ctx context.Context, vectorSize int) error {
	exists, existingSize, err := c.GetCollectionVectorSize(ctx)
	if err != nil {
		return err
	}
	if exists {
		if existingSize > 0 && vectorSize > 0 && existingSize != vectorSize {
			return fmt.Errorf("qdrant collection %q vector size=%d expected=%d (change AGENT_CONTEXT_COLLECTION or delete/recreate collection)", c.collection, existingSize, vectorSize)
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
