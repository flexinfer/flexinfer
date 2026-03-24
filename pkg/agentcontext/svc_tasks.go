package agentcontext

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/embed"
	"github.com/crb2nu/loom/pkg/validate"
)

// TaskSvc manages task CRUD, embedding, and lifecycle.
// Tasks are stored exclusively in Qdrant (no in-memory map).
type TaskSvc struct {
	qdrant *QdrantClient // CollTasks
	embedr embed.Embedder
	cfg    Config
	logger *slog.Logger

	// Shared mutable state — pointer to Service.vectorSize so the first
	// embedding call from any domain propagates the discovered size.
	vectorSize *int

	// Reconciler (optional, set after construction).
	reconciler *TaskReconciler

	// Cross-domain callbacks (wired by Service).
	getSession    func(ctx context.Context, sessionID string) (*Session, error)
	upsertBatched func(ctx context.Context, q *QdrantClient, points []Point) error
}

// NewTaskSvc creates a new TaskSvc.
func NewTaskSvc(qdrant *QdrantClient, embedr embed.Embedder, cfg Config, logger *slog.Logger, vectorSize *int) *TaskSvc {
	return &TaskSvc{
		qdrant:     qdrant,
		embedr:     embedr,
		cfg:        cfg,
		logger:     logger,
		vectorSize: vectorSize,
	}
}

// Add creates tasks with embeddings and stores them in Qdrant.
func (ts *TaskSvc) Add(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	tasksRaw := v.RequiredAny("tasks")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session, err := ts.getSession(ctx, sessionID)
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
			Project:    session.Project,
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
		task.PipelineRef = pipelineRefFromValue(m["pipeline_ref"])
		task.WorkflowID = toString(m["workflow_id"])
		task.Project = canonicalProject(toString(m["project"]), task.Namespace, task.PipelineRef)

		tasks = append(tasks, task)
		embedTexts = append(embedTexts, task.Title+" "+task.Context)
	}

	if len(tasks) == 0 {
		return mcp.ErrorResult(fmt.Errorf("no valid tasks provided")), nil
	}

	// Generate embeddings
	vectors, err := ts.embedr.EmbedDocuments(ctx, embedTexts)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("embedding tasks: %w", err)), nil
	}
	if len(vectors) != len(tasks) {
		return mcp.ErrorResult(fmt.Errorf("embedding count mismatch")), nil
	}

	for _, vec := range vectors {
		if len(vec) > 0 {
			*ts.vectorSize = len(vec)
			break
		}
	}
	if *ts.vectorSize <= 0 {
		return mcp.ErrorResult(fmt.Errorf("unknown vector size")), nil
	}

	if err := ts.qdrant.EnsureCollection(ctx, *ts.vectorSize); err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure collection: %w", err)), nil
	}

	// Build points
	points := make([]Point, 0, len(tasks))
	for i, task := range tasks {
		vector := vectors[i]
		if len(vector) == 0 {
			vector = make([]float64, *ts.vectorSize)
		}
		points = append(points, Point{
			ID:      task.ID,
			Vector:  vector,
			Payload: taskToPayload(task),
		})
	}

	if err := ts.upsertBatched(ctx, ts.qdrant, points); err != nil {
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

// Update modifies a task's status or resolution.
func (ts *TaskSvc) Update(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	taskID := v.Required("task_id")
	statusStr := v.String("status", "")
	resolution := v.String("resolution", "")
	project := v.String("project", "")
	pipelineRef := pipelineRefFromValue(args["pipeline_ref"])
	workflowID := v.String("workflow_id", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	status := TaskStatus(statusStr)

	// Get existing task
	p, err := ts.qdrant.GetPoint(ctx, taskID, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("task %s not found: %w", taskID, err)), nil
	}

	task, err := payloadToTask(p.Payload)
	if err != nil || task == nil {
		return mcp.ErrorResult(fmt.Errorf("invalid task payload")), nil
	}

	if status != "" {
		task.Status = status
	}
	if resolution != "" {
		task.Resolution = resolution
	}
	if pipelineRef != nil {
		task.PipelineRef = pipelineRef
	}
	if workflowID != "" {
		task.WorkflowID = workflowID
	}
	task.Project = canonicalProject(project, task.Namespace, task.PipelineRef)
	task.UpdatedAt = time.Now()

	if status == TaskStatusCompleted {
		now := time.Now()
		task.CompletedAt = &now
	}

	payload := taskToPayload(*task)
	if err := ts.qdrant.SetPayload(ctx, []string{taskID}, payload, true); err != nil {
		return mcp.ErrorResult(fmt.Errorf("update task: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":   true,
		"task": task,
	})
}

// List returns tasks matching optional filters, sorted by priority.
func (ts *TaskSvc) List(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.String("session_id", "")
	agentID := v.String("agent_id", "")
	statusesRaw := v.StringSlice("status")
	includeCompleted := v.Bool("include_completed", false)
	limit := v.Int("limit", 50)

	var conds []any
	if sessionID != "" {
		conds = append(conds, Match("session_id", sessionID))
	}
	if agentID != "" {
		conds = append(conds, Match("agent_id", agentID))
	}

	if len(statusesRaw) > 0 {
		conds = append(conds, FilterShould(Matches("status", statusesRaw)...))
	} else if !includeCompleted {
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

	points, err := ts.qdrant.ScrollPoints(ctx, filter, limit, false)
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

// Delete removes tasks by ID.
func (ts *TaskSvc) Delete(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
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

	if err := ts.qdrant.Delete(ctx, taskIDs); err != nil {
		return mcp.ErrorResult(fmt.Errorf("delete tasks: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"deleted": len(taskIDs),
	})
}

// GetActive retrieves pending/in_progress/blocked tasks, sorted by priority.
func (ts *TaskSvc) GetActive(ctx context.Context, agentID, sessionID string, limit int) ([]Task, error) {
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

	points, err := ts.qdrant.ScrollPoints(ctx, FilterMust(conds...), limit*2, false)
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

	sort.Slice(tasks, func(i, j int) bool {
		return priorityRank(tasks[i].Priority) > priorityRank(tasks[j].Priority)
	})

	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	return tasks, nil
}

// MarkSessionTasksStale marks pending/in_progress tasks for a session as blocked.
func (ts *TaskSvc) MarkSessionTasksStale(ctx context.Context, sessionID string) int {
	filter := FilterMust(
		Match("session_id", sessionID),
		FilterShould(
			Match("status", string(TaskStatusPending)),
			Match("status", string(TaskStatusInProgress)),
		),
	)

	points, err := ts.qdrant.ScrollPoints(ctx, filter, 500, false)
	if err != nil || len(points) == 0 {
		return 0
	}

	count := 0
	now := time.Now().Format(time.RFC3339Nano)
	for _, p := range points {
		id := toString(p.Payload["id"])
		if id == "" {
			continue
		}
		payload := map[string]any{
			"status":     string(TaskStatusBlocked),
			"resolution": "session ended — task incomplete",
			"updated_at": now,
		}
		if err := ts.qdrant.SetPayload(ctx, []string{id}, payload, false); err != nil {
			ts.logger.Warn("failed to mark task stale on session end", "task_id", id, "error", err)
			continue
		}
		count++
	}
	return count
}

// StartReconciler starts the task reconciler if configured.
func (ts *TaskSvc) StartReconciler(ctx context.Context) {
	if ts.reconciler != nil {
		ts.reconciler.Start(ctx)
	}
}

// StopReconciler stops the reconciler if running.
func (ts *TaskSvc) StopReconciler() {
	if ts.reconciler != nil {
		ts.reconciler.Stop()
	}
}

// --- Payload converters ---

func taskToPayload(t Task) map[string]any {
	payload := map[string]any{
		"id":          t.ID,
		"session_id":  t.SessionID,
		"agent_id":    t.AgentID,
		"namespace":   t.Namespace,
		"project":     canonicalProject(t.Project, t.Namespace, t.PipelineRef),
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
	if t.PipelineRef != nil {
		payload["pipeline_ref"] = pipelineRefToPayload(t.PipelineRef)
	}
	if t.WorkflowID != "" {
		payload["workflow_id"] = t.WorkflowID
	}
	if t.CompletedAt != nil {
		payload["completed_at"] = t.CompletedAt.Format(time.RFC3339Nano)
	}
	return payload
}

func payloadToTask(payload map[string]any) (*Task, error) {
	if payload == nil {
		return nil, fmt.Errorf("nil payload")
	}

	task := &Task{
		ID:          toString(payload["id"]),
		SessionID:   toString(payload["session_id"]),
		AgentID:     toString(payload["agent_id"]),
		Namespace:   toString(payload["namespace"]),
		Project:     toString(payload["project"]),
		Title:       toString(payload["title"]),
		Context:     toString(payload["context"]),
		Priority:    TaskPriority(toString(payload["priority"])),
		Status:      TaskStatus(toString(payload["status"])),
		Resolution:  toString(payload["resolution"]),
		FilePath:    toString(payload["file_path"]),
		LineNumber:  toInt(payload["line_number"]),
		Symbol:      toString(payload["symbol"]),
		Tags:        toStringSlice(payload["tags"]),
		BlockedBy:   toStringSlice(payload["blocked_by"]),
		ParentID:    toString(payload["parent_id"]),
		PipelineRef: pipelineRefFromValue(payload["pipeline_ref"]),
		WorkflowID:  toString(payload["workflow_id"]),
		TokenCount:  toInt(payload["token_count"]),
	}
	task.Project = canonicalProject(task.Project, task.Namespace, task.PipelineRef)

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
