package qdrant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

type scrolledPoint struct {
	Payload map[string]any
	Vector  []float64
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

	points, err := c.scrollPoints(ctx, filter, max, true)
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

func (c *Client) GetFilePreflight(
	ctx context.Context,
	repoID string,
	filePath string,
	embedModel string,
	max int,
) (FilePreflight, error) {
	if max <= 0 {
		max = 4096
	}

	points, err := c.scrollPoints(ctx, filterMust(
		match("repo_id", repoID),
		match("file_path", filePath),
	), max, strings.TrimSpace(embedModel) != "")
	if err != nil {
		if errors.Is(err, ErrCollectionNotFound) {
			return FilePreflight{EmbeddingCache: map[string][]float64{}}, nil
		}
		return FilePreflight{}, err
	}

	out := FilePreflight{EmbeddingCache: make(map[string][]float64)}
	for _, p := range points {
		hash := toString(p.Payload["content_hash"])
		if !out.ModuleFound && toString(p.Payload["chunk_type"]) == "module" && hash != "" {
			out.ModuleContentHash = hash
			out.ModuleFound = true
		}
		if strings.TrimSpace(embedModel) == "" || toString(p.Payload["embed_model"]) != embedModel {
			continue
		}
		if hash == "" || len(p.Vector) == 0 {
			continue
		}
		if _, ok := out.EmbeddingCache[hash]; ok {
			continue
		}
		out.EmbeddingCache[hash] = p.Vector
	}
	return out, nil
}

func (c *Client) scrollPoints(ctx context.Context, filter map[string]any, max int, withVector bool) ([]scrolledPoint, error) {
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
			"with_vector":  withVector,
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
