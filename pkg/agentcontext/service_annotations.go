package agentcontext

import (
	"fmt"
	"time"
)

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
