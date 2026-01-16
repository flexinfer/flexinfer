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
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/codebase/schema"
	"github.com/crb2nu/loom/pkg/httpclient"
)

var ErrCollectionNotFound = errors.New("qdrant collection not found")

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
			return fmt.Errorf("qdrant collection %q vector size=%d expected=%d (use CODEBASE_QDRANT_COLLECTION or delete/recreate collection)", c.collection, existingSize, vectorSize)
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

func (c *Client) Upsert(ctx context.Context, points []Point, wait bool) error {
	if len(points) == 0 {
		return nil
	}
	path := fmt.Sprintf("/collections/%s/points?wait=%v", c.collection, wait)
	body := map[string]any{"points": pointsToJSON(points)}
	return c.doJSON(ctx, http.MethodPut, path, body, nil)
}

func (c *Client) Search(
	ctx context.Context,
	repoID string,
	vector []float64,
	limit int,
	languages []string,
	chunkTypes []string,
	withPayload bool,
) ([]schema.SearchResult, error) {
	path := fmt.Sprintf("/collections/%s/points/search", c.collection)
	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": withPayload,
		"filter":       buildFilter(repoID, "", languages, chunkTypes),
	}

	var resp struct {
		Result []struct {
			Score   float64        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
		if errors.Is(err, ErrCollectionNotFound) {
			return []schema.SearchResult{}, nil
		}
		return nil, err
	}

	results := make([]schema.SearchResult, 0, len(resp.Result))
	for _, hit := range resp.Result {
		ch, err := payloadToChunk(hit.Payload)
		if err != nil || ch == nil {
			continue
		}
		results = append(results, schema.SearchResult{
			Score: hit.Score,
			Chunk: *ch,
		})
	}
	return results, nil
}

func (c *Client) GetFileContext(
	ctx context.Context,
	repoID string,
	filePath string,
	line int,
	relatedLimit int,
) (*schema.ContextInfo, error) {
	chunks, err := c.scrollAllForFile(ctx, repoID, filePath, 1024)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return &schema.ContextInfo{Chunk: nil}, nil
	}

	var (
		target *schema.Chunk
		other  []schema.Chunk
	)
	for _, ch := range chunks {
		if ch.StartLine <= line && line <= ch.EndLine {
			if target == nil || (ch.EndLine-ch.StartLine) < (target.EndLine-target.StartLine) {
				cp := ch
				target = &cp
			}
		} else {
			other = append(other, ch)
		}
	}
	if target == nil {
		return &schema.ContextInfo{Chunk: nil}, nil
	}

	sort.Slice(other, func(i, j int) bool {
		di := absInt(other[i].StartLine - line)
		dj := absInt(other[j].StartLine - line)
		if di == dj {
			return other[i].StartLine < other[j].StartLine
		}
		return di < dj
	})
	if relatedLimit > 0 && len(other) > relatedLimit {
		other = other[:relatedLimit]
	}

	return &schema.ContextInfo{
		Chunk:         target,
		RelatedChunks: other,
		Imports:       target.Imports,
	}, nil
}

func (c *Client) FindCallers(ctx context.Context, repoID, functionName string, limit int) ([]schema.CallerInfo, error) {
	if limit <= 0 {
		limit = 256
	}

	target := normalizeCallToken(functionName)
	if target == "" {
		return []schema.CallerInfo{}, nil
	}

	chunks, err := c.scroll(ctx, filterMust(
		match("repo_id", repoID),
		match("call_names", target),
	), limit)
	if err != nil {
		return nil, err
	}

	var callers []schema.CallerInfo
	for _, ch := range chunks {
		callExpr := findCallExpr(ch.Calls, functionName)
		if callExpr == "" {
			continue
		}
		callers = append(callers, schema.CallerInfo{
			FilePath:     ch.FilePath,
			FunctionName: ch.Name,
			LineNumber:   ch.StartLine,
			CallExpr:     callExpr,
		})
	}
	return callers, nil
}

func (c *Client) FindCallersInFile(
	ctx context.Context,
	repoID string,
	filePath string,
	functionName string,
	limit int,
) ([]schema.CallerInfo, error) {
	if limit <= 0 {
		limit = 256
	}

	target := normalizeCallToken(functionName)
	if target == "" {
		return []schema.CallerInfo{}, nil
	}

	chunks, err := c.scroll(ctx, filterMust(
		match("repo_id", repoID),
		match("file_path", filePath),
		match("call_names", target),
	), limit)
	if err != nil {
		return nil, err
	}

	var callers []schema.CallerInfo
	for _, ch := range chunks {
		callExpr := findCallExpr(ch.Calls, functionName)
		if callExpr == "" {
			continue
		}
		callers = append(callers, schema.CallerInfo{
			FilePath:     ch.FilePath,
			FunctionName: ch.Name,
			LineNumber:   ch.StartLine,
			CallExpr:     callExpr,
		})
	}
	return callers, nil
}

func (c *Client) FindChunkByName(
	ctx context.Context,
	repoID string,
	symbol string,
	filePath string,
	languages []string,
	limit int,
) (*schema.Chunk, error) {
	if limit <= 0 {
		limit = 512
	}

	conds := []any{
		match("repo_id", repoID),
		match("name", symbol),
	}
	if filePath != "" {
		conds = append(conds, match("file_path", filePath))
	}
	if len(languages) > 0 {
		conds = append(conds, filterShould(matches("language", languages)...))
	}
	filter := filterMust(conds...)

	chunks, err := c.scroll(ctx, filter, limit)
	if err != nil {
		return nil, err
	}

	var best *schema.Chunk
	bestScore := 0
	for _, ch := range chunks {
		// Prefer smallest containing chunk (often method vs larger type decl).
		score := ch.EndLine - ch.StartLine
		if ch.ChunkType == "module" {
			score += 1_000_000
		}
		if best == nil || score < bestScore {
			cp := ch
			best = &cp
			bestScore = score
		}
	}
	return best, nil
}

func (c *Client) FindChunksByName(
	ctx context.Context,
	repoID string,
	symbol string,
	filePath string,
	languages []string,
	limit int,
) ([]schema.Chunk, error) {
	if limit <= 0 {
		limit = 512
	}

	conds := []any{
		match("repo_id", repoID),
		match("name", symbol),
	}
	if filePath != "" {
		conds = append(conds, match("file_path", filePath))
	}
	if len(languages) > 0 {
		conds = append(conds, filterShould(matches("language", languages)...))
	}

	return c.scroll(ctx, filterMust(conds...), limit)
}

func (c *Client) GetModuleContentHash(ctx context.Context, repoID, filePath string) (string, bool, error) {
	chunks, err := c.scroll(ctx, filterMust(
		match("repo_id", repoID),
		match("file_path", filePath),
		match("chunk_type", "module"),
	), 8)
	if err != nil {
		if errors.Is(err, ErrCollectionNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if len(chunks) == 0 {
		return "", false, nil
	}
	return chunks[0].ContentHash, true, nil
}

func ChunkToPayload(ch schema.Chunk, includeContent bool, embedModel string) map[string]any {
	payload := map[string]any{
		"id":             ch.ID,
		"schema_version": ch.SchemaVer,
		"repo_id":        ch.RepoID,
		"file_path":      ch.FilePath,
		"language":       ch.Language,
		"chunk_type":     ch.ChunkType,
		"embed_model":    embedModel,
		"git_commit":     ch.GitCommit,
		"git_blame":      ch.GitBlame,
		"name":           ch.Name,
		"signature":      ch.Signature,
		"docstring":      ch.Docstring,
		"parent_name":    ch.ParentName,
		"parent_type":    ch.ParentType,
		"imports":        ch.Imports,
		"calls":          ch.Calls,
		"call_names":     normalizeCallNames(ch.Calls),
		"definitions":    ch.Defs,
		"start_line":     ch.StartLine,
		"end_line":       ch.EndLine,
		"start_column":   ch.StartColumn,
		"end_column":     ch.EndColumn,
		"token_count":    ch.TokenCount,
		"indexed_at":     ch.IndexedAt.Format(time.RFC3339Nano),
		"content_hash":   ch.ContentHash,
	}
	if includeContent {
		payload["content"] = ch.Content
	}
	return payload
}

func payloadToChunk(payload map[string]any) (*schema.Chunk, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	ch := &schema.Chunk{
		ID:          toString(payload["id"]),
		RepoID:      toString(payload["repo_id"]),
		FilePath:    toString(payload["file_path"]),
		Language:    toString(payload["language"]),
		ChunkType:   toString(payload["chunk_type"]),
		GitCommit:   toString(payload["git_commit"]),
		GitBlame:    toString(payload["git_blame"]),
		Name:        toString(payload["name"]),
		Signature:   toString(payload["signature"]),
		Docstring:   toString(payload["docstring"]),
		ParentName:  toString(payload["parent_name"]),
		ParentType:  toString(payload["parent_type"]),
		ContentHash: toString(payload["content_hash"]),
		SchemaVer:   toString(payload["schema_version"]),
		Content:     toString(payload["content"]),
		StartLine:   toInt(payload["start_line"]),
		EndLine:     toInt(payload["end_line"]),
		StartColumn: toInt(payload["start_column"]),
		EndColumn:   toInt(payload["end_column"]),
		TokenCount:  toInt(payload["token_count"]),
		Imports:     toStringSlice(payload["imports"]),
		Calls:       toStringSlice(payload["calls"]),
		Defs:        toStringSlice(payload["definitions"]),
	}
	return ch, nil
}

func (c *Client) GetFileEmbeddingCache(
	ctx context.Context,
	repoID string,
	filePath string,
	embedModel string,
	max int,
) (map[string][]float64, error) {
	if strings.TrimSpace(embedModel) == "" {
		return map[string][]float64{}, nil
	}
	if max <= 0 {
		max = 4096
	}

	filter := filterMust(
		match("repo_id", repoID),
		match("file_path", filePath),
		match("embed_model", embedModel),
	)

	points, err := c.scrollPointsWithVectors(ctx, filter, max)
	if err != nil {
		if errors.Is(err, ErrCollectionNotFound) {
			return map[string][]float64{}, nil
		}
		return nil, err
	}

	cache := make(map[string][]float64, len(points))
	for _, p := range points {
		hash := toString(p.Payload["content_hash"])
		if hash == "" || len(p.Vector) == 0 {
			continue
		}
		if _, ok := cache[hash]; ok {
			continue
		}
		cache[hash] = p.Vector
	}
	return cache, nil
}

type scrolledPoint struct {
	Payload map[string]any
	Vector  []float64
}

func (c *Client) scrollPointsWithVectors(ctx context.Context, filter map[string]any, max int) ([]scrolledPoint, error) {
	var out []scrolledPoint
	var offset any

	for {
		remaining := max - len(out)
		if remaining <= 0 {
			break
		}
		limit := remaining
		if limit > 256 {
			limit = 256
		}

		body := map[string]any{
			"filter":       filter,
			"limit":        limit,
			"with_payload": true,
			"with_vector":  true,
		}
		if offset != nil {
			body["offset"] = offset
		}

		path := fmt.Sprintf("/collections/%s/points/scroll", c.collection)
		var resp struct {
			Result struct {
				Points []struct {
					Payload map[string]any `json:"payload"`
					Vector  any            `json:"vector"`
				} `json:"points"`
				NextPageOffset any `json:"next_page_offset"`
			} `json:"result"`
		}

		if err := c.doJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
			return nil, err
		}

		for _, p := range resp.Result.Points {
			vec := vectorFromAny(p.Vector)
			if vec == nil {
				continue
			}
			out = append(out, scrolledPoint{Payload: p.Payload, Vector: vec})
		}

		if resp.Result.NextPageOffset == nil || len(resp.Result.Points) == 0 {
			break
		}
		offset = resp.Result.NextPageOffset
	}

	return out, nil
}

func vectorFromAny(v any) []float64 {
	switch raw := v.(type) {
	case []float64:
		if len(raw) == 0 {
			return nil
		}
		return raw
	case []any:
		out := make([]float64, 0, len(raw))
		for _, it := range raw {
			switch n := it.(type) {
			case float64:
				out = append(out, n)
			case int:
				out = append(out, float64(n))
			case int64:
				out = append(out, float64(n))
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case map[string]any:
		// Named vectors: prefer "default" if present; otherwise take the first vector-like value.
		if def, ok := raw["default"]; ok {
			if vec := vectorFromAny(def); vec != nil {
				return vec
			}
		}
		for _, vv := range raw {
			if vec := vectorFromAny(vv); vec != nil {
				return vec
			}
		}
		return nil
	default:
		return nil
	}
}

func (c *Client) scrollAllForFile(ctx context.Context, repoID, filePath string, max int) ([]schema.Chunk, error) {
	return c.scroll(ctx, buildFilter(repoID, filePath, nil, nil), max)
}

func (c *Client) ScrollChunks(ctx context.Context, filter map[string]any, max int) ([]schema.Chunk, error) {
	return c.scroll(ctx, filter, max)
}

func (c *Client) scroll(ctx context.Context, filter map[string]any, max int) ([]schema.Chunk, error) {
	var out []schema.Chunk
	var offset any

	for {
		remaining := max - len(out)
		if remaining <= 0 {
			break
		}
		limit := remaining
		if limit > 256 {
			limit = 256
		}

		body := map[string]any{
			"filter":       filter,
			"limit":        limit,
			"with_payload": true,
		}
		if offset != nil {
			body["offset"] = offset
		}

		path := fmt.Sprintf("/collections/%s/points/scroll", c.collection)
		var resp struct {
			Result struct {
				Points []struct {
					Payload map[string]any `json:"payload"`
				} `json:"points"`
				NextPageOffset any `json:"next_page_offset"`
			} `json:"result"`
		}

		if err := c.doJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
			if errors.Is(err, ErrCollectionNotFound) {
				return []schema.Chunk{}, nil
			}
			return nil, err
		}

		for _, p := range resp.Result.Points {
			ch, err := payloadToChunk(p.Payload)
			if err == nil && ch != nil {
				out = append(out, *ch)
			}
		}

		if resp.Result.NextPageOffset == nil || len(resp.Result.Points) == 0 {
			break
		}
		offset = resp.Result.NextPageOffset
	}

	return out, nil
}

func buildFilter(repoID, filePath string, languages []string, chunkTypes []string) map[string]any {
	conds := []any{match("repo_id", repoID)}
	if filePath != "" {
		conds = append(conds, match("file_path", filePath))
	}
	if len(languages) > 0 {
		conds = append(conds, filterShould(matches("language", languages)...))
	}
	if len(chunkTypes) > 0 {
		conds = append(conds, filterShould(matches("chunk_type", chunkTypes)...))
	}
	return filterMust(conds...)
}

func Filter(repoID, filePath string, languages []string, chunkTypes []string) map[string]any {
	return buildFilter(repoID, filePath, languages, chunkTypes)
}

func match(key, value string) map[string]any {
	return map[string]any{
		"key":   key,
		"match": map[string]any{"value": value},
	}
}

func matches(key string, values []string) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, match(key, v))
	}
	return out
}

func filterMust(conds ...any) map[string]any {
	return map[string]any{"must": conds}
}

func filterShould(conds ...any) map[string]any {
	return map[string]any{"should": conds}
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

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
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
		return ErrCollectionNotFound
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

func toString(v any) string {
	s, _ := v.(string)
	return s
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

func toStringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			return ss
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, it := range raw {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

var nonNameCharRE = regexp.MustCompile(`[^A-Za-z0-9_]+`)

func findCallExpr(calls []string, functionName string) string {
	target := normalizeCallToken(functionName)
	for _, call := range calls {
		if call == functionName {
			return call
		}
		if strings.HasSuffix(call, "."+functionName) || strings.HasSuffix(call, "::"+functionName) {
			return call
		}
		if target != "" && normalizeCallToken(call) == target {
			return call
		}
	}
	return ""
}

func normalizeCallToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Drop generic args (best-effort), e.g. "foo::<T>".
	if idx := strings.Index(s, "<"); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimRight(s, ":.")
	s = strings.TrimSpace(s)

	// Prefer last segment of qualified names.
	if idx := strings.LastIndex(s, "::"); idx >= 0 {
		s = s[idx+2:]
	} else if idx := strings.LastIndex(s, "."); idx >= 0 {
		s = s[idx+1:]
	}
	s = strings.TrimSpace(s)
	s = nonNameCharRE.ReplaceAllString(s, "")
	return s
}

func normalizeCallNames(calls []string) []string {
	if len(calls) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, c := range calls {
		tok := normalizeCallToken(c)
		if tok == "" || seen[tok] {
			continue
		}
		seen[tok] = true
	}
	out := make([]string, 0, len(seen))
	for tok := range seen {
		out = append(out, tok)
	}
	sort.Strings(out)
	return out
}

func NormalizeCallToken(s string) string {
	return normalizeCallToken(s)
}
