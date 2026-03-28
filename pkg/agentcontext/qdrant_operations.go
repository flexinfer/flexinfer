// qdrant_operations.go -- Qdrant CRUD operations, search, scroll, and filter builders.
package agentcontext

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

func (c *QdrantClient) Upsert(ctx context.Context, points []Point, wait bool) error {
	if len(points) == 0 {
		return nil
	}
	path := fmt.Sprintf("/collections/%s/points?wait=%v", c.collection, wait)
	body := map[string]any{"points": pointsToJSON(points)}
	return c.doJSON(ctx, http.MethodPut, path, body, nil)
}

func (c *QdrantClient) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	uuids := make([]string, len(ids))
	for i, id := range ids {
		uuids[i] = toPointID(id)
	}
	path := fmt.Sprintf("/collections/%s/points/delete", c.collection)
	body := map[string]any{"points": uuids}
	return c.doJSON(ctx, http.MethodPost, path, body, nil)
}

func (c *QdrantClient) DeleteByFilter(ctx context.Context, filter map[string]any) error {
	path := fmt.Sprintf("/collections/%s/points/delete", c.collection)
	body := map[string]any{"filter": filter}
	return c.doJSON(ctx, http.MethodPost, path, body, nil)
}

func (c *QdrantClient) SetPayload(ctx context.Context, ids []string, payload map[string]any, wait bool) error {
	if len(ids) == 0 {
		return nil
	}
	uuids := make([]string, len(ids))
	for i, id := range ids {
		uuids[i] = toPointID(id)
	}
	path := fmt.Sprintf("/collections/%s/points/payload?wait=%v", c.collection, wait)
	body := map[string]any{
		"payload": payload,
		"points":  uuids,
	}
	return c.doJSON(ctx, http.MethodPost, path, body, nil)
}

func (c *QdrantClient) GetPoint(ctx context.Context, id string, withVector bool) (RawPoint, error) {
	if strings.TrimSpace(id) == "" {
		return RawPoint{}, fmt.Errorf("id is required")
	}

	path := fmt.Sprintf("/collections/%s/points/%s?with_payload=true&with_vector=%v", c.collection, toPointID(id), withVector)
	var resp struct {
		Result struct {
			ID      string         `json:"id"`
			Vector  []float64      `json:"vector"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		if errors.Is(err, ErrCollectionNotFound) {
			return RawPoint{}, ErrCollectionNotFound
		}
		return RawPoint{}, err
	}
	return RawPoint{
		ID:      resp.Result.ID,
		Vector:  resp.Result.Vector,
		Payload: resp.Result.Payload,
	}, nil
}

// GetPoints fetches multiple points by ID in a single request.
func (c *QdrantClient) GetPoints(ctx context.Context, ids []string, withVector bool) ([]RawPoint, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	uuids := make([]string, len(ids))
	for i, id := range ids {
		uuids[i] = toPointID(id)
	}
	body := map[string]any{
		"ids":          uuids,
		"with_payload": true,
		"with_vector":  withVector,
	}
	path := fmt.Sprintf("/collections/%s/points", c.collection)
	var resp struct {
		Result []struct {
			ID      string         `json:"id"`
			Vector  []float64      `json:"vector"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
		if errors.Is(err, ErrCollectionNotFound) {
			return []RawPoint{}, nil
		}
		return nil, err
	}
	points := make([]RawPoint, 0, len(resp.Result))
	for _, p := range resp.Result {
		points = append(points, RawPoint{
			ID:      p.ID,
			Vector:  p.Vector,
			Payload: p.Payload,
		})
	}
	return points, nil
}

func pointsToJSON(points []Point) []map[string]any {
	out := make([]map[string]any, 0, len(points))
	for _, p := range points {
		out = append(out, map[string]any{
			"id":      toPointID(p.ID),
			"vector":  p.Vector,
			"payload": p.Payload,
		})
	}
	return out
}

func (c *QdrantClient) Search(
	ctx context.Context,
	vector []float64,
	filter map[string]any,
	limit int,
	withPayload bool,
) ([]SearchResult, error) {
	path := fmt.Sprintf("/collections/%s/points/search", c.collection)
	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": withPayload,
	}
	if filter != nil {
		body["filter"] = filter
	}

	var resp struct {
		Result []struct {
			ID      string         `json:"id"`
			Score   float64        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
		if errors.Is(err, ErrCollectionNotFound) {
			return []SearchResult{}, nil
		}
		return nil, err
	}

	results := make([]SearchResult, 0, len(resp.Result))
	for _, hit := range resp.Result {
		entry, err := PayloadToEntry(hit.Payload)
		if err != nil || entry == nil {
			continue
		}
		results = append(results, SearchResult{
			Score: hit.Score,
			Entry: *entry,
		})
	}
	return results, nil
}

func (c *QdrantClient) Scroll(ctx context.Context, filter map[string]any, limit int) ([]ContextEntry, error) {
	points, err := c.ScrollPoints(ctx, filter, limit, false)
	if err != nil {
		return nil, err
	}

	out := make([]ContextEntry, 0, len(points))
	for _, p := range points {
		entry, err := PayloadToEntry(p.Payload)
		if err == nil && entry != nil {
			out = append(out, *entry)
		}
	}
	return out, nil
}

func (c *QdrantClient) ScrollPoints(ctx context.Context, filter map[string]any, limit int, withVector bool) ([]RawPoint, error) {
	var out []RawPoint
	var offset any

	for {
		remaining := limit - len(out)
		if remaining <= 0 {
			break
		}
		batchLimit := remaining
		if batchLimit > 256 {
			batchLimit = 256
		}

		body := map[string]any{
			"limit":        batchLimit,
			"with_payload": true,
			"with_vector":  withVector,
		}
		if filter != nil {
			body["filter"] = filter
		}
		if offset != nil {
			body["offset"] = offset
		}

		path := fmt.Sprintf("/collections/%s/points/scroll", c.collection)
		var resp struct {
			Result struct {
				Points []struct {
					ID      string         `json:"id"`
					Vector  []float64      `json:"vector"`
					Payload map[string]any `json:"payload"`
				} `json:"points"`
				NextPageOffset any `json:"next_page_offset"`
			} `json:"result"`
		}

		if err := c.doJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
			if errors.Is(err, ErrCollectionNotFound) {
				return []RawPoint{}, nil
			}
			return nil, err
		}

		for _, p := range resp.Result.Points {
			out = append(out, RawPoint{
				ID:      p.ID,
				Vector:  p.Vector,
				Payload: p.Payload,
			})
		}

		if resp.Result.NextPageOffset == nil || len(resp.Result.Points) == 0 {
			break
		}
		offset = resp.Result.NextPageOffset
	}

	return out, nil
}

func (c *QdrantClient) Count(ctx context.Context, filter map[string]any) (int, error) {
	path := fmt.Sprintf("/collections/%s/points/count", c.collection)
	body := map[string]any{"exact": true}
	if filter != nil {
		body["filter"] = filter
	}

	var resp struct {
		Result struct {
			Count int `json:"count"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
		if errors.Is(err, ErrCollectionNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return resp.Result.Count, nil
}

// Filter helpers

func FilterMust(conds ...any) map[string]any {
	return map[string]any{"must": conds}
}

func FilterShould(conds ...any) map[string]any {
	return map[string]any{"should": conds}
}

func Match(key, value string) map[string]any {
	return map[string]any{
		"key":   key,
		"match": map[string]any{"value": value},
	}
}

func MatchAny(key string, values []string) map[string]any {
	return map[string]any{
		"key":   key,
		"match": map[string]any{"any": values},
	}
}

func Matches(key string, values []string) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, Match(key, v))
	}
	return out
}
