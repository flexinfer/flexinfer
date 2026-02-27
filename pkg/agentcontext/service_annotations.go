package agentcontext

import (
	"context"
	"fmt"
	"sort"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

func (s *Service) HandleAnnotationAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
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

	session, err := s.getSession(ctx, sessionID)
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

	// Generate embedding
	vector, err := s.embed.EmbedQuery(ctx, annotation.Content)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("embedding: %w", err)), nil
	}
	if len(vector) > 0 {
		s.vectorSize = len(vector)
	}
	if s.vectorSize <= 0 {
		return mcp.ErrorResult(fmt.Errorf("unknown vector size")), nil
	}

	if err := s.qdrant.Get(CollAnnotations).EnsureCollection(ctx, s.vectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	point := Point{
		ID:      annotation.ID,
		Vector:  vector,
		Payload: annotationToPayload(annotation),
	}

	if err := s.qdrant.Get(CollAnnotations).Upsert(ctx, []Point{point}, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert annotation: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":            true,
		"annotation_id": annotation.ID,
	})
}

func (s *Service) HandleAnnotationsGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	filePath := v.String("file_path", "")
	agentID := v.String("agent_id", "")
	lineStart := v.Int("line_start", 0)
	lineEnd := v.Int("line_end", 0)
	annotationTypes := v.StringSlice("annotation_types")
	limit := v.Int("limit", 50)

	// Build filter
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

	points, err := s.qdrant.Get(CollAnnotations).ScrollPoints(ctx, filter, limit, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get annotations: %w", err)), nil
	}

	annotations := make([]CodeAnnotation, 0, len(points))
	for _, p := range points {
		ann, err := payloadToAnnotation(p.Payload)
		if err != nil || ann == nil {
			continue
		}
		// Filter by line range if specified
		if lineStart > 0 && ann.LineStart < lineStart {
			continue
		}
		if lineEnd > 0 && ann.LineStart > lineEnd {
			continue
		}
		annotations = append(annotations, *ann)
	}

	// Sort by line number
	sort.Slice(annotations, func(i, j int) bool {
		return annotations[i].LineStart < annotations[j].LineStart
	})

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"annotations": annotations,
		"count":       len(annotations),
	})
}

func (s *Service) getAnnotationsForFile(ctx context.Context, agentID, filePath string, limit int) ([]CodeAnnotation, error) {
	var conds []any
	conds = append(conds, Match("file_path", filePath))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}

	points, err := s.qdrant.Get(CollAnnotations).ScrollPoints(ctx, FilterMust(conds...), limit, false)
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

func annotationToPayload(a CodeAnnotation) map[string]any {
	return map[string]any{
		"id":              a.ID,
		"session_id":      a.SessionID,
		"agent_id":        a.AgentID,
		"namespace":       a.Namespace,
		"file_path":       a.FilePath,
		"line_start":      a.LineStart,
		"line_end":        a.LineEnd,
		"symbol":          a.Symbol,
		"repo_id":         a.RepoID,
		"annotation_type": string(a.AnnotationType),
		"content":         a.Content,
		"created_at":      a.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":      a.UpdatedAt.Format(time.RFC3339Nano),
		"token_count":     a.TokenCount,
	}
}

func payloadToAnnotation(payload map[string]any) (*CodeAnnotation, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	ann := &CodeAnnotation{
		ID:             toString(payload["id"]),
		SessionID:      toString(payload["session_id"]),
		AgentID:        toString(payload["agent_id"]),
		Namespace:      toString(payload["namespace"]),
		FilePath:       toString(payload["file_path"]),
		LineStart:      toInt(payload["line_start"]),
		LineEnd:        toInt(payload["line_end"]),
		Symbol:         toString(payload["symbol"]),
		RepoID:         toString(payload["repo_id"]),
		AnnotationType: AnnotationType(toString(payload["annotation_type"])),
		Content:        toString(payload["content"]),
		TokenCount:     toInt(payload["token_count"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			ann.CreatedAt = t
		}
	}
	if ts := toString(payload["updated_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			ann.UpdatedAt = t
		}
	}

	return ann, nil
}
