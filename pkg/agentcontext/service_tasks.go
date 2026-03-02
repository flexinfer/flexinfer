package agentcontext

import (
	"context"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// Task handlers — thin delegation to TaskSvc.

func (s *Service) HandleTaskAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.tasks.Add(ctx, args)
}

func (s *Service) HandleTaskUpdate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.tasks.Update(ctx, args)
}

func (s *Service) HandleTaskList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.tasks.List(ctx, args)
}

func (s *Service) HandleTaskDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.tasks.Delete(ctx, args)
}

// getActiveTasks delegates to TaskSvc.GetActive.
func (s *Service) getActiveTasks(ctx context.Context, agentID, sessionID string, limit int) ([]Task, error) {
	return s.tasks.GetActive(ctx, agentID, sessionID, limit)
}

// markSessionTasksStale delegates to TaskSvc.MarkSessionTasksStale.
func (s *Service) markSessionTasksStale(ctx context.Context, sessionID string) int {
	return s.tasks.MarkSessionTasksStale(ctx, sessionID)
}

// Payload converters (used by TaskSvc and tests).

func taskToPayload(t Task) map[string]any {
	return map[string]any{
		"id":          t.ID,
		"session_id":  t.SessionID,
		"agent_id":    t.AgentID,
		"namespace":   t.Namespace,
		"title":       t.Title,
		"context":     t.Context,
		"priority":    string(t.Priority),
		"status":      string(t.Status),
		"resolution":  t.Resolution,
		"file_path":   t.FilePath,
		"line_number": t.LineNumber,
		"symbol":      t.Symbol,
		"tags":        t.Tags,
		"blocked_by":  t.BlockedBy,
		"parent_id":   t.ParentID,
		"created_at":  t.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":  t.UpdatedAt.Format(time.RFC3339Nano),
		"token_count": t.TokenCount,
	}
}

func payloadToTask(payload map[string]any) (*Task, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	task := &Task{
		ID:         toString(payload["id"]),
		SessionID:  toString(payload["session_id"]),
		AgentID:    toString(payload["agent_id"]),
		Namespace:  toString(payload["namespace"]),
		Title:      toString(payload["title"]),
		Context:    toString(payload["context"]),
		Priority:   TaskPriority(toString(payload["priority"])),
		Status:     TaskStatus(toString(payload["status"])),
		Resolution: toString(payload["resolution"]),
		FilePath:   toString(payload["file_path"]),
		LineNumber: toInt(payload["line_number"]),
		Symbol:     toString(payload["symbol"]),
		Tags:       toStringSlice(payload["tags"]),
		BlockedBy:  toStringSlice(payload["blocked_by"]),
		ParentID:   toString(payload["parent_id"]),
		TokenCount: toInt(payload["token_count"]),
	}

	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			task.CreatedAt = t
		}
	}
	if ts := toString(payload["updated_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			task.UpdatedAt = t
		}
	}
	if ts := toString(payload["completed_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			task.CompletedAt = &t
		}
	}

	return task, nil
}

func priorityRank(p TaskPriority) int {
	switch p {
	case TaskPriorityCritical:
		return 4
	case TaskPriorityHigh:
		return 3
	case TaskPriorityMedium:
		return 2
	case TaskPriorityLow:
		return 1
	}
	return 0
}
