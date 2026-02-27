package agentcontext

import (
	"context"
	"fmt"
	"sort"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

func (s *Service) HandleTaskAdd(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	tasksRaw := v.RequiredAny("tasks")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := s.getSession(ctx, sessionID)
	if err != nil || session == nil {
		return mcp.ErrorResult(fmt.Errorf("session %s not found", sessionID)), nil
	}

	tasksArr, ok := tasksRaw.([]any)
	if !ok || len(tasksArr) == 0 {
		return mcp.ErrorResult(fmt.Errorf("tasks array is required")), nil
	}

	var tasks []Task
	var embedTexts []string
	now := time.Now()

	for _, raw := range tasksArr {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		title := toString(m["title"])
		if title == "" {
			continue
		}

		priority := TaskPriority(toString(m["priority"]))
		if priority == "" {
			priority = TaskPriorityMedium
		}

		task := Task{
			ID:         GenerateID(session.AgentID, sessionID, title, now),
			SessionID:  sessionID,
			AgentID:    session.AgentID,
			Namespace:  session.Namespace,
			Title:      title,
			Context:    toString(m["context"]),
			Priority:   priority,
			Status:     TaskStatusPending,
			FilePath:   toString(m["file_path"]),
			LineNumber: toInt(m["line_number"]),
			Tags:       toStringSlice(m["tags"]),
			BlockedBy:  toStringSlice(m["blocked_by"]),
			CreatedAt:  now,
			UpdatedAt:  now,
			TokenCount: EstimateTokens(title + " " + toString(m["context"])),
		}

		tasks = append(tasks, task)
		embedTexts = append(embedTexts, task.Title+" "+task.Context)
	}

	if len(tasks) == 0 {
		return mcp.ErrorResult(fmt.Errorf("no valid tasks provided")), nil
	}

	// Generate embeddings
	vectors, err := s.embed.EmbedDocuments(ctx, embedTexts)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("embedding tasks: %w", err)), nil
	}
	if len(vectors) != len(tasks) {
		return mcp.ErrorResult(fmt.Errorf("embedding count mismatch")), nil
	}

	for _, v := range vectors {
		if len(v) > 0 {
			s.vectorSize = len(v)
			break
		}
	}
	if s.vectorSize <= 0 {
		return mcp.ErrorResult(fmt.Errorf("unknown vector size")), nil
	}

	if err := s.qdrant.Get(CollTasks).EnsureCollection(ctx, s.vectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	// Build points
	points := make([]Point, 0, len(tasks))
	for i, task := range tasks {
		vector := vectors[i]
		if len(vector) == 0 {
			vector = make([]float64, s.vectorSize)
		}
		points = append(points, Point{
			ID:      task.ID,
			Vector:  vector,
			Payload: taskToPayload(task),
		})
	}

	if err := s.upsertPointsBatched(ctx, s.qdrant.Get(CollTasks), points); err != nil {
		return mcp.ErrorResult(fmt.Errorf("upsert tasks: %w", err)), nil
	}

	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(tasks),
		"task_ids": ids,
	})
}

func (s *Service) HandleTaskUpdate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	taskID := v.Required("task_id")
	statusStr := v.String("status", "")
	resolution := v.String("resolution", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	status := TaskStatus(statusStr)

	// Get existing task
	p, err := s.qdrant.Get(CollTasks).GetPoint(ctx, taskID, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("task %s not found: %w", taskID, err)), nil
	}

	task, err := payloadToTask(p.Payload)
	if err != nil || task == nil {
		return mcp.ErrorResult(fmt.Errorf("invalid task payload")), nil
	}

	// Update fields
	if status != "" {
		task.Status = status
	}
	if resolution != "" {
		task.Resolution = resolution
	}
	task.UpdatedAt = time.Now()

	if status == TaskStatusCompleted {
		now := time.Now()
		task.CompletedAt = &now
	}

	// Update payload
	payload := taskToPayload(*task)
	if err := s.qdrant.Get(CollTasks).SetPayload(ctx, []string{taskID}, payload, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("update task: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":   true,
		"task": task,
	})
}

func (s *Service) HandleTaskList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.String("session_id", "")
	agentID := v.String("agent_id", "")
	statusesRaw := v.StringSlice("status")
	includeCompleted := v.Bool("include_completed", false)
	limit := v.Int("limit", 50)

	// Build filter
	var conds []any
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}

	// Status filter
	if len(statusesRaw) > 0 {
		conds = append(conds, FilterShould(Matches("status", statusesRaw)...))
	} else if !includeCompleted {
		// Exclude completed by default
		conds = append(conds, FilterShould(
			Match("status", string(TaskStatusPending)),
			Match("status", string(TaskStatusInProgress)),
			Match("status", string(TaskStatusBlocked)),
		))
	}

	var filter map[string]any
	if len(conds) > 0 {
		filter = FilterMust(conds...)
	}

	points, err := s.qdrant.Get(CollTasks).ScrollPoints(ctx, filter, limit, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list tasks: %w", err)), nil
	}

	tasks := make([]Task, 0, len(points))
	for _, p := range points {
		task, err := payloadToTask(p.Payload)
		if err != nil || task == nil {
			continue
		}
		tasks = append(tasks, *task)
	}

	// Sort by priority (critical > high > medium > low), then by created_at
	sort.Slice(tasks, func(i, j int) bool {
		pi, pj := priorityRank(tasks[i].Priority), priorityRank(tasks[j].Priority)
		if pi != pj {
			return pi > pj
		}
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"tasks": tasks,
		"count": len(tasks),
	})
}

// HandleTaskDelete deletes one or more tasks by ID.
func (s *Service) HandleTaskDelete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	taskIDsRaw := v.RequiredAny("task_ids")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ids, ok := taskIDsRaw.([]any)
	if !ok || len(ids) == 0 {
		return mcp.ErrorResult(fmt.Errorf("task_ids array is required")), nil
	}

	taskIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if s := toString(id); s != "" {
			taskIDs = append(taskIDs, s)
		}
	}

	if len(taskIDs) == 0 {
		return mcp.ErrorResult(fmt.Errorf("no valid task IDs provided")), nil
	}

	if err := s.qdrant.Get(CollTasks).Delete(ctx, taskIDs); err != nil {
		return mcp.ErrorResult(fmt.Errorf("delete tasks: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"deleted": len(taskIDs),
	})
}

func (s *Service) getActiveTasks(ctx context.Context, agentID, sessionID string, limit int) ([]Task, error) {
	var conds []any
	conds = append(conds, FilterShould(
		Match("status", string(TaskStatusPending)),
		Match("status", string(TaskStatusInProgress)),
		Match("status", string(TaskStatusBlocked)),
	))
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}

	points, err := s.qdrant.Get(CollTasks).ScrollPoints(ctx, FilterMust(conds...), limit*2, false)
	if err != nil {
		return nil, err
	}

	tasks := make([]Task, 0, len(points))
	for _, p := range points {
		task, err := payloadToTask(p.Payload)
		if err != nil || task == nil {
			continue
		}
		tasks = append(tasks, *task)
	}

	// Sort by priority
	sort.Slice(tasks, func(i, j int) bool {
		return priorityRank(tasks[i].Priority) > priorityRank(tasks[j].Priority)
	})

	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	return tasks, nil
}

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
