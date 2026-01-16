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
	"time"

	"github.com/crb2nu/loom/pkg/httpclient"
)

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
	_, _ = io.ReadAll(resp.Body)
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
	body, _ := io.ReadAll(resp.Body)
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
	path := fmt.Sprintf("/collections/%s/points/delete", c.collection)
	body := map[string]any{"points": ids}
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
	path := fmt.Sprintf("/collections/%s/points/payload?wait=%v", c.collection, wait)
	body := map[string]any{
		"payload": payload,
		"points":  ids,
	}
	return c.doJSON(ctx, http.MethodPost, path, body, nil)
}

func (c *QdrantClient) GetPoint(ctx context.Context, id string, withVector bool) (RawPoint, error) {
	if strings.TrimSpace(id) == "" {
		return RawPoint{}, fmt.Errorf("id is required")
	}

	path := fmt.Sprintf("/collections/%s/points/%s?with_payload=true&with_vector=%v", c.collection, id, withVector)
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

func (c *QdrantClient) setHeaders(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
}

func (c *QdrantClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	url := c.baseURL + "/" + strings.TrimPrefix(path, "/")

	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
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

	respBody, err := io.ReadAll(resp.Body)
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

// Payload conversion

func EntryToPayload(e ContextEntry, embedModel string) map[string]any {
	payload := map[string]any{
		"id":             e.ID,
		"schema_version": e.SchemaVersion,
		"agent_id":       e.AgentID,
		"session_id":     e.SessionID,
		"namespace":      e.Namespace,
		"entry_type":     string(e.EntryType),
		"timestamp":      e.Timestamp.Format(time.RFC3339Nano),
		"title":          e.Title,
		"content":        e.Content,
		"content_hash":   e.ContentHash,
		"file_path":      e.FilePath,
		"line_start":     e.LineStart,
		"line_end":       e.LineEnd,
		"parent_id":      e.ParentID,
		"related_ids":    e.RelatedIDs,
		"tags":           e.Tags,
		"token_count":    e.TokenCount,
		"visibility":     string(e.Visibility),
		"shared_with":    e.SharedWith,
		"embed_model":    embedModel,
	}
	if e.Metadata != nil {
		payload["metadata"] = e.Metadata
	}
	return payload
}

func PayloadToEntry(payload map[string]any) (*ContextEntry, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	entry := &ContextEntry{
		ID:            toString(payload["id"]),
		SchemaVersion: toString(payload["schema_version"]),
		AgentID:       toString(payload["agent_id"]),
		SessionID:     toString(payload["session_id"]),
		Namespace:     toString(payload["namespace"]),
		EntryType:     EntryType(toString(payload["entry_type"])),
		Title:         toString(payload["title"]),
		Content:       toString(payload["content"]),
		ContentHash:   toString(payload["content_hash"]),
		FilePath:      toString(payload["file_path"]),
		LineStart:     toInt(payload["line_start"]),
		LineEnd:       toInt(payload["line_end"]),
		ParentID:      toString(payload["parent_id"]),
		RelatedIDs:    toStringSlice(payload["related_ids"]),
		Tags:          toStringSlice(payload["tags"]),
		TokenCount:    toInt(payload["token_count"]),
		Visibility:    Visibility(toString(payload["visibility"])),
		SharedWith:    toStringSlice(payload["shared_with"]),
	}

	if ts := toString(payload["timestamp"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			entry.Timestamp = t
		}
	}

	if m, ok := payload["metadata"].(map[string]any); ok {
		entry.Metadata = m
	}

	return entry, nil
}

func SessionToPayload(s Session) map[string]any {
	payload := map[string]any{
		"id":           s.ID,
		"agent_id":     s.AgentID,
		"namespace":    s.Namespace,
		"started_at":   s.StartedAt.Format(time.RFC3339Nano),
		"status":       s.Status,
		"description":  s.Description,
		"working_dir":  s.WorkingDir,
		"entry_count":  s.EntryCount,
		"total_tokens": s.TotalTokens,
	}
	if s.EndedAt != nil {
		payload["ended_at"] = s.EndedAt.Format(time.RFC3339Nano)
	}
	if s.LastSummaryAt != nil {
		payload["last_summary_at"] = s.LastSummaryAt.Format(time.RFC3339Nano)
	}
	return payload
}

func PayloadToSession(payload map[string]any) (*Session, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	session := &Session{
		ID:          toString(payload["id"]),
		AgentID:     toString(payload["agent_id"]),
		Namespace:   toString(payload["namespace"]),
		Status:      toString(payload["status"]),
		Description: toString(payload["description"]),
		WorkingDir:  toString(payload["working_dir"]),
		EntryCount:  toInt(payload["entry_count"]),
		TotalTokens: toInt(payload["total_tokens"]),
	}

	if ts := toString(payload["started_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			session.StartedAt = t
		}
	}
	if ts := toString(payload["ended_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			session.EndedAt = &t
		}
	}
	if ts := toString(payload["last_summary_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			session.LastSummaryAt = &t
		}
	}

	return session, nil
}

func pointsToJSON(points []Point) []map[string]any {
	out := make([]map[string]any, 0, len(points))
	for _, p := range points {
		out = append(out, map[string]any{
			"id":      p.ID,
			"vector":  p.Vector,
			"payload": p.Payload,
		})
	}
	return out
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func toStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
