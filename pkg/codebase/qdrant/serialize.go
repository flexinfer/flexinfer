package qdrant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

func (c *Client) Upsert(ctx context.Context, points []Point, wait bool) error {
	if len(points) == 0 {
		return nil
	}
	path := fmt.Sprintf("/collections/%s/points?wait=%v", c.collection, wait)
	body := map[string]any{"points": pointsToJSON(points)}
	return c.doJSON(ctx, http.MethodPut, path, body, nil)
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
		"identifiers":    ch.Identifiers,
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
			"id":      toPointID(p.ID),
			"vector":  p.Vector,
			"payload": p.Payload,
		})
	}
	return out
}

// toPointID converts an arbitrary string to a stable UUIDv5-like value accepted
// by Qdrant point ID validation.
func toPointID(id string) string {
	h := sha256.Sum256([]byte(id))
	h[6] = (h[6] & 0x0f) | 0x50 // version 5
	h[8] = (h[8] & 0x3f) | 0x80 // RFC 4122 variant
	s := hex.EncodeToString(h[:16])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
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
