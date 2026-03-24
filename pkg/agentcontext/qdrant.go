package agentcontext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/httpclient"
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
		"_record_type":   "entry", // discriminator for shared collection (SIMP-12)
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
	// Source versioning (Phase 2.1)
	if e.SourceVersion != nil {
		sv := map[string]any{
			"indexed_at": e.SourceVersion.IndexedAt.Format(time.RFC3339Nano),
			"is_stale":   e.SourceVersion.IsStale,
		}
		if e.SourceVersion.CommitHash != "" {
			sv["commit_hash"] = e.SourceVersion.CommitHash
		}
		if !e.SourceVersion.FileMtime.IsZero() {
			sv["file_mtime"] = e.SourceVersion.FileMtime.Format(time.RFC3339Nano)
		}
		payload["source_version"] = sv
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

	// Parse source version (Phase 2.1)
	if sv, ok := payload["source_version"].(map[string]any); ok {
		entry.SourceVersion = &SourceVersion{
			CommitHash: toString(sv["commit_hash"]),
			IsStale:    toBool(sv["is_stale"]),
		}
		if ts := toString(sv["indexed_at"]); ts != "" {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				entry.SourceVersion.IndexedAt = t
			}
		}
		if ts := toString(sv["file_mtime"]); ts != "" {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				entry.SourceVersion.FileMtime = t
			}
		}
	}

	return entry, nil
}

func SessionToPayload(s Session) map[string]any {
	payload := map[string]any{
		"id":           s.ID,
		"agent_id":     s.AgentID,
		"namespace":    s.Namespace,
		"project":      canonicalProject(s.Project, s.Namespace, s.PipelineRef),
		"started_at":   s.StartedAt.Format(time.RFC3339Nano),
		"status":       s.Status,
		"description":  s.Description,
		"working_dir":  s.WorkingDir,
		"entry_count":  s.EntryCount,
		"total_tokens": s.TotalTokens,
	}
	if s.PipelineRef != nil {
		payload["pipeline_ref"] = pipelineRefToPayload(s.PipelineRef)
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
		Project:     toString(payload["project"]),
		Status:      toString(payload["status"]),
		Description: toString(payload["description"]),
		WorkingDir:  toString(payload["working_dir"]),
		EntryCount:  toInt(payload["entry_count"]),
		TotalTokens: toInt(payload["total_tokens"]),
		PipelineRef: pipelineRefFromValue(payload["pipeline_ref"]),
	}
	session.Project = canonicalProject(session.Project, session.Namespace, session.PipelineRef)

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
			"id":      toPointID(p.ID),
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

func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
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

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func toMapStringAny(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// Entity payload conversion

func EntityToPayload(e Entity, embedModel string) map[string]any {
	payload := map[string]any{
		"id":          e.ID,
		"type":        string(e.Type),
		"name":        e.Name,
		"description": e.Description,
		"namespace":   e.Namespace,
		"file_path":   e.FilePath,
		"line_start":  e.LineStart,
		"line_end":    e.LineEnd,
		"language":    e.Language,
		"signature":   e.Signature,
		"session_id":  e.SessionID,
		"agent_id":    e.AgentID,
		"created_at":  e.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":  e.UpdatedAt.Format(time.RFC3339Nano),
		"tags":        e.Tags,
		"embed_model": embedModel,
	}
	if e.Properties != nil {
		payload["properties"] = e.Properties
	}
	return payload
}

func PayloadToEntity(payload map[string]any) (*Entity, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	entity := &Entity{
		ID:          toString(payload["id"]),
		Type:        EntityType(toString(payload["type"])),
		Name:        toString(payload["name"]),
		Description: toString(payload["description"]),
		Namespace:   toString(payload["namespace"]),
		FilePath:    toString(payload["file_path"]),
		LineStart:   toInt(payload["line_start"]),
		LineEnd:     toInt(payload["line_end"]),
		Language:    toString(payload["language"]),
		Signature:   toString(payload["signature"]),
		SessionID:   toString(payload["session_id"]),
		AgentID:     toString(payload["agent_id"]),
		Tags:        toStringSlice(payload["tags"]),
		Properties:  toMapStringAny(payload["properties"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			entity.CreatedAt = t
		}
	}
	if ts := toString(payload["updated_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			entity.UpdatedAt = t
		}
	}

	return entity, nil
}

// Relation payload conversion

func RelationToPayload(r Relation) map[string]any {
	payload := map[string]any{
		"id":            r.ID,
		"type":          string(r.Type),
		"source_id":     r.SourceID,
		"target_id":     r.TargetID,
		"weight":        r.Weight,
		"bidirectional": r.Bidirectional,
		"evidence":      r.Evidence,
		"reasoning":     r.Reasoning,
		"session_id":    r.SessionID,
		"agent_id":      r.AgentID,
		"created_at":    r.CreatedAt.Format(time.RFC3339Nano),
	}
	if r.Properties != nil {
		payload["properties"] = r.Properties
	}
	return payload
}

func PayloadToRelation(payload map[string]any) (*Relation, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	rel := &Relation{
		ID:            toString(payload["id"]),
		Type:          RelationType(toString(payload["type"])),
		SourceID:      toString(payload["source_id"]),
		TargetID:      toString(payload["target_id"]),
		Weight:        toFloat64(payload["weight"]),
		Bidirectional: toBool(payload["bidirectional"]),
		Evidence:      toString(payload["evidence"]),
		Reasoning:     toString(payload["reasoning"]),
		SessionID:     toString(payload["session_id"]),
		AgentID:       toString(payload["agent_id"]),
		Properties:    toMapStringAny(payload["properties"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			rel.CreatedAt = t
		}
	}

	return rel, nil
}

func toBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

// MemoryItem payload conversion

func MemoryItemToPayload(m MemoryItem, embedModel string) map[string]any {
	payload := map[string]any{
		"id":                m.ID,
		"tier":              string(m.Tier),
		"status":            string(m.Status),
		"importance":        string(m.Importance),
		"importance_score":  m.ImportanceScore,
		"title":             m.Title,
		"content":           m.Content,
		"summary":           m.Summary,
		"source_entry_id":   m.SourceEntryID,
		"source_type":       string(m.SourceType),
		"category":          m.Category,
		"tags":              m.Tags,
		"namespace":         m.Namespace,
		"session_id":        m.SessionID,
		"agent_id":          m.AgentID,
		"created_at":        m.CreatedAt.Format(time.RFC3339Nano),
		"last_accessed_at":  m.LastAccessedAt.Format(time.RFC3339Nano),
		"access_count":      m.AccessCount,
		"original_tokens":   m.OriginalTokens,
		"compressed_tokens": m.CompressedTokens,
		"related_ids":       m.RelatedIDs,
		"parent_id":         m.ParentID,
		"child_ids":         m.ChildIDs,
		"embed_model":       embedModel,
	}
	if m.ExpiresAt != nil {
		payload["expires_at"] = m.ExpiresAt.Format(time.RFC3339Nano)
	}
	if m.CompressedAt != nil {
		payload["compressed_at"] = m.CompressedAt.Format(time.RFC3339Nano)
	}
	if m.ArchivedAt != nil {
		payload["archived_at"] = m.ArchivedAt.Format(time.RFC3339Nano)
	}
	if m.Metadata != nil {
		payload["metadata"] = m.Metadata
	}
	return payload
}

func PayloadToMemoryItem(payload map[string]any) (*MemoryItem, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	item := &MemoryItem{
		ID:               toString(payload["id"]),
		Tier:             MemoryTier(toString(payload["tier"])),
		Status:           MemoryItemStatus(toString(payload["status"])),
		Importance:       ImportanceLevel(toString(payload["importance"])),
		ImportanceScore:  toFloat64(payload["importance_score"]),
		Title:            toString(payload["title"]),
		Content:          toString(payload["content"]),
		Summary:          toString(payload["summary"]),
		SourceEntryID:    toString(payload["source_entry_id"]),
		SourceType:       EntryType(toString(payload["source_type"])),
		Category:         toString(payload["category"]),
		Tags:             toStringSlice(payload["tags"]),
		Namespace:        toString(payload["namespace"]),
		SessionID:        toString(payload["session_id"]),
		AgentID:          toString(payload["agent_id"]),
		AccessCount:      toInt(payload["access_count"]),
		OriginalTokens:   toInt(payload["original_tokens"]),
		CompressedTokens: toInt(payload["compressed_tokens"]),
		RelatedIDs:       toStringSlice(payload["related_ids"]),
		ParentID:         toString(payload["parent_id"]),
		ChildIDs:         toStringSlice(payload["child_ids"]),
		Metadata:         toMapStringAny(payload["metadata"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			item.CreatedAt = t
		}
	}
	if ts := toString(payload["last_accessed_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			item.LastAccessedAt = t
		}
	}
	if ts := toString(payload["expires_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			item.ExpiresAt = &t
		}
	}
	if ts := toString(payload["compressed_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			item.CompressedAt = &t
		}
	}
	if ts := toString(payload["archived_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			item.ArchivedAt = &t
		}
	}

	return item, nil
}

// Workflow payload conversion

func WorkflowToPayload(wf Workflow) map[string]any {
	payload := map[string]any{
		"id":              wf.ID,
		"definition_id":   wf.DefinitionID,
		"session_id":      wf.SessionID,
		"agent_id":        wf.AgentID,
		"namespace":       wf.Namespace,
		"status":          string(wf.Status),
		"current_step":    wf.CurrentStep,
		"error":           wf.Error,
		"failed_step_id":  wf.FailedStepID,
		"created_at":      wf.CreatedAt.Format(time.RFC3339Nano),
		"total_steps":     wf.TotalSteps,
		"completed_steps": wf.CompletedSteps,
		"failed_steps":    wf.FailedSteps,
	}
	if wf.StartedAt != nil {
		payload["started_at"] = wf.StartedAt.Format(time.RFC3339Nano)
	}
	if wf.CompletedAt != nil {
		payload["completed_at"] = wf.CompletedAt.Format(time.RFC3339Nano)
	}
	if wf.Input != nil {
		payload["input"] = wf.Input
	}
	if wf.Output != nil {
		payload["output"] = wf.Output
	}
	if wf.Context != nil {
		payload["context"] = wf.Context
	}
	// Store definition and step states as JSON
	if defBytes, err := json.Marshal(wf.Definition); err == nil {
		payload["definition_json"] = string(defBytes)
	}
	if statesBytes, err := json.Marshal(wf.StepStates); err == nil {
		payload["step_states_json"] = string(statesBytes)
	}
	return payload
}

func PayloadToWorkflow(payload map[string]any) (*Workflow, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	wf := &Workflow{
		ID:             toString(payload["id"]),
		DefinitionID:   toString(payload["definition_id"]),
		SessionID:      toString(payload["session_id"]),
		AgentID:        toString(payload["agent_id"]),
		Namespace:      toString(payload["namespace"]),
		Status:         WorkflowStatus(toString(payload["status"])),
		CurrentStep:    toString(payload["current_step"]),
		Error:          toString(payload["error"]),
		FailedStepID:   toString(payload["failed_step_id"]),
		TotalSteps:     toInt(payload["total_steps"]),
		CompletedSteps: toInt(payload["completed_steps"]),
		FailedSteps:    toInt(payload["failed_steps"]),
		Input:          toMapStringAny(payload["input"]),
		Output:         toMapStringAny(payload["output"]),
		Context:        toMapStringAny(payload["context"]),
		StepStates:     make(map[string]*WorkflowStep),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			wf.CreatedAt = t
		}
	}
	if ts := toString(payload["started_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			wf.StartedAt = &t
		}
	}
	if ts := toString(payload["completed_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			wf.CompletedAt = &t
		}
	}

	// Parse definition from JSON
	if defJSON := toString(payload["definition_json"]); defJSON != "" {
		if err := json.Unmarshal([]byte(defJSON), &wf.Definition); err != nil {
			return nil, fmt.Errorf("parse definition_json: %w", err)
		}
	}

	// Parse step states from JSON
	if statesJSON := toString(payload["step_states_json"]); statesJSON != "" {
		if err := json.Unmarshal([]byte(statesJSON), &wf.StepStates); err != nil {
			return nil, fmt.Errorf("parse step_states_json: %w", err)
		}
	}

	return wf, nil
}

// WorkflowDefinition payload conversion

func WorkflowDefinitionToPayload(def WorkflowDefinition) map[string]any {
	payload := map[string]any{
		"id":                  def.ID,
		"name":                def.Name,
		"description":         def.Description,
		"version":             def.Version,
		"namespace":           def.Namespace,
		"created_by":          def.CreatedBy,
		"timeout_seconds":     def.TimeoutSeconds,
		"rollback_on_failure": def.RollbackOnFailure,
		"created_at":          def.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":          def.UpdatedAt.Format(time.RFC3339Nano),
	}
	// Store steps and input schema as JSON
	if stepsBytes, err := json.Marshal(def.Steps); err == nil {
		payload["steps_json"] = string(stepsBytes)
	}
	if def.InputSchema != nil {
		if schemaBytes, err := json.Marshal(def.InputSchema); err == nil {
			payload["input_schema_json"] = string(schemaBytes)
		}
	}
	return payload
}

func PayloadToWorkflowDefinition(payload map[string]any) (*WorkflowDefinition, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	def := &WorkflowDefinition{
		ID:                toString(payload["id"]),
		Name:              toString(payload["name"]),
		Description:       toString(payload["description"]),
		Version:           toString(payload["version"]),
		Namespace:         toString(payload["namespace"]),
		CreatedBy:         toString(payload["created_by"]),
		TimeoutSeconds:    toInt(payload["timeout_seconds"]),
		RollbackOnFailure: toBool(payload["rollback_on_failure"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			def.CreatedAt = t
		}
	}
	if ts := toString(payload["updated_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			def.UpdatedAt = t
		}
	}

	// Parse steps from JSON
	if stepsJSON := toString(payload["steps_json"]); stepsJSON != "" {
		if err := json.Unmarshal([]byte(stepsJSON), &def.Steps); err != nil {
			return nil, fmt.Errorf("parse steps_json: %w", err)
		}
	}

	// Parse input schema from JSON
	if schemaJSON := toString(payload["input_schema_json"]); schemaJSON != "" {
		if err := json.Unmarshal([]byte(schemaJSON), &def.InputSchema); err != nil {
			return nil, fmt.Errorf("parse input_schema_json: %w", err)
		}
	}

	return def, nil
}

// ReasoningChain payload conversion

func ReasoningChainToPayload(rc ReasoningChain) map[string]any {
	payload := map[string]any{
		"id":         rc.ID,
		"query":      rc.Query,
		"conclusion": rc.Conclusion,
		"confidence": rc.Confidence,
		"session_id": rc.SessionID,
		"agent_id":   rc.AgentID,
		"created_at": rc.CreatedAt.Format(time.RFC3339Nano),
	}
	if stepsBytes, err := json.Marshal(rc.Steps); err == nil {
		payload["steps_json"] = string(stepsBytes)
	}
	return payload
}

func PayloadToReasoningChain(payload map[string]any) (*ReasoningChain, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	rc := &ReasoningChain{
		ID:         toString(payload["id"]),
		Query:      toString(payload["query"]),
		Conclusion: toString(payload["conclusion"]),
		Confidence: toFloat64(payload["confidence"]),
		SessionID:  toString(payload["session_id"]),
		AgentID:    toString(payload["agent_id"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			rc.CreatedAt = t
		}
	}

	if stepsJSON := toString(payload["steps_json"]); stepsJSON != "" {
		if err := json.Unmarshal([]byte(stepsJSON), &rc.Steps); err != nil {
			return nil, fmt.Errorf("parse steps_json: %w", err)
		}
	}

	return rc, nil
}
