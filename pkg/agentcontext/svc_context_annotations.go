package agentcontext

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// --- Codebase Link ---

func (cs *ContextSvc) LinkCodebase(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	filePath := v.Required("file_path")
	repoID := v.String("repo_id", "")
	symbol := v.String("symbol", "")
	note := v.String("note", "")
	tags := v.StringSlice("tags")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := cs.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	content := fmt.Sprintf("File: %s", filePath)
	if symbol != "" {
		content += fmt.Sprintf("\nSymbol: %s", symbol)
	}
	if note != "" {
		content += fmt.Sprintf("\nNote: %s", note)
	}

	entry := ContextEntry{
		ID:            GenerateID(session.AgentID, sessionID, content, time.Now()),
		SchemaVersion: SchemaVersion,
		AgentID:       session.AgentID,
		SessionID:     sessionID,
		Namespace:     session.Namespace,
		EntryType:     EntryTypeCodeContext,
		Timestamp:     time.Now(),
		Title:         fmt.Sprintf("Code: %s", filePath),
		Content:       content,
		ContentHash:   ContentHashFunc(content),
		FilePath:      filePath,
		Tags:          tags,
		TokenCount:    EstimateTokens(content),
		Visibility:    cs.cfg.DefaultVisibility,
		Metadata: map[string]any{
			"repo_id": repoID,
			"symbol":  symbol,
		},
	}

	cs.metrics.EmbeddingRequests.Add(1)
	vector, err := cs.embed.EmbedQuery(ctx, entry.Title+" "+entry.Content)
	if err != nil {
		cs.metrics.EmbeddingErrors.Add(1)
		return mcp.ErrorResult(fmt.Errorf("embedding: %w", err)), nil
	}
	if len(vector) > 0 {
		*cs.vectorSize = len(vector)
	}
	if *cs.vectorSize <= 0 {
		return mcp.ErrorResult(fmt.Errorf("unknown vector size")), nil
	}
	if err := cs.qdrant.Get(CollContext).EnsureCollection(ctx, *cs.vectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	point := Point{
		ID:      entry.ID,
		Vector:  vector,
		Payload: EntryToPayload(entry, cs.cfg.EmbedModel),
	}

	if err := cs.qdrant.Get(CollContext).Upsert(ctx, []Point{point}, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"entry_id": entry.ID,
	})
}

// --- Annotations ---

func (cs *ContextSvc) AnnotationAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	filePath := v.Required("file_path")
	lineStart := v.RequiredInt("line_start")
	content := v.Required("content")
	annotationTypeStr := v.String("annotation_type", string(AnnotationTypeNote))
	lineEnd := v.Int("line_end", 0)
	symbol := v.String("symbol", "")
	repoID := v.String("repo_id", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := cs.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	annotationType := AnnotationType(annotationTypeStr)

	now := time.Now()
	annotation := CodeAnnotation{
		ID:             GenerateID(session.AgentID, sessionID, filePath+content, now),
		SessionID:      sessionID,
		AgentID:        session.AgentID,
		Namespace:      session.Namespace,
		FilePath:       filePath,
		LineStart:      lineStart,
		LineEnd:        lineEnd,
		Symbol:         symbol,
		RepoID:         repoID,
		AnnotationType: annotationType,
		Content:        content,
		CreatedAt:      now,
		UpdatedAt:      now,
		TokenCount:     EstimateTokens(content),
	}

	vector, err := cs.embed.EmbedQuery(ctx, annotation.Content)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("embedding: %w", err)), nil
	}
	if len(vector) > 0 {
		*cs.vectorSize = len(vector)
	}
	if *cs.vectorSize <= 0 {
		return mcp.ErrorResult(fmt.Errorf("unknown vector size")), nil
	}

	if err := cs.qdrant.Get(CollAnnotations).EnsureCollection(ctx, *cs.vectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	point := Point{
		ID:      annotation.ID,
		Vector:  vector,
		Payload: annotationToPayload(annotation),
	}

	if err := cs.qdrant.Get(CollAnnotations).Upsert(ctx, []Point{point}, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert annotation: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":            true,
		"annotation_id": annotation.ID,
	})
}

func (cs *ContextSvc) AnnotationsGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	filePath := v.String("file_path", "")
	agentID := v.String("agent_id", "")
	lineStart := v.Int("line_start", 0)
	lineEnd := v.Int("line_end", 0)
	annotationTypes := v.StringSlice("annotation_types")
	limit := v.Int("limit", 50)

	var conds []any
	if filePath != "" {
		conds = append(conds, Match("file_path", filePath))
	}
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if len(annotationTypes) > 0 {
		conds = append(conds, FilterShould(Matches("annotation_type", annotationTypes)...))
	}

	var filter map[string]any
	if len(conds) > 0 {
		filter = FilterMust(conds...)
	}

	points, err := cs.qdrant.Get(CollAnnotations).ScrollPoints(ctx, filter, limit, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get annotations: %w", err)), nil
	}

	annotations := make([]CodeAnnotation, 0, len(points))
	for _, p := range points {
		ann, err := payloadToAnnotation(p.Payload)
		if err != nil || ann == nil {
			continue
		}
		if lineStart > 0 && ann.LineStart < lineStart {
			continue
		}
		if lineEnd > 0 && ann.LineStart > lineEnd {
			continue
		}
		annotations = append(annotations, *ann)
	}

	sort.Slice(annotations, func(i, j int) bool {
		return annotations[i].LineStart < annotations[j].LineStart
	})

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"annotations": annotations,
		"count":       len(annotations),
	})
}

func (cs *ContextSvc) GetAnnotationsForFile(ctx context.Context, agentID, filePath string, limit int) ([]CodeAnnotation, error) {
	var conds []any
	conds = append(conds, Match("file_path", filePath))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}

	points, err := cs.qdrant.Get(CollAnnotations).ScrollPoints(ctx, FilterMust(conds...), limit, false)
	if err != nil {
		return nil, err
	}

	annotations := make([]CodeAnnotation, 0, len(points))
	for _, p := range points {
		ann, err := payloadToAnnotation(p.Payload)
		if err != nil || ann == nil {
			continue
		}
		annotations = append(annotations, *ann)
	}

	return annotations, nil
}

// --- Internal helpers ---

func (cs *ContextSvc) getRecentByType(ctx context.Context, agentID, sessionID string, entryType EntryType, limit int) ([]ContextEntry, error) {
	var conds []any
	conds = append(conds, Match("entry_type", string(entryType)))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}

	entries, err := cs.qdrant.Get(CollContext).Scroll(ctx, FilterMust(conds...), limit*2)
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (cs *ContextSvc) getEntriesForFile(ctx context.Context, agentID, filePath string, limit int) ([]ContextEntry, error) {
	var conds []any
	conds = append(conds, Match("file_path", filePath))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}

	return cs.qdrant.Get(CollContext).Scroll(ctx, FilterMust(conds...), limit)
}

func (cs *ContextSvc) upsertBatched(ctx context.Context, q *QdrantClient, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	batchSize := cs.cfg.UpsertBatchSize
	if batchSize <= 0 {
		batchSize = 64
	}
	for i := 0; i < len(points); i += batchSize {
		j := i + batchSize
		if j > len(points) {
			j = len(points)
		}
		if err := q.Upsert(ctx, points[i:j], true); err != nil {
			return err
		}
	}
	return nil
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
