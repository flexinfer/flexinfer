package bridge

import (
	"fmt"
	"strings"
)

const (
	hudDispatchAgentID   = "hud-dispatcher"
	hudDispatchNamespace = "loom-core/hud-dispatch"
)

// Tasks returns tasks for a specific session.
func (a *AgentBridge) Tasks(sessionID string) ([]TaskInfo, error) {
	args := map[string]any{}
	if sessionID != "" {
		args["session_id"] = sessionID
	}
	var result struct {
		Tasks []TaskInfo `json:"tasks"`
	}
	if err := a.callAgentTool("agent_task_list", args, &result); err != nil {
		return nil, err
	}
	return result.Tasks, nil
}

// AllTasks returns all tasks across all sessions.
func (a *AgentBridge) AllTasks() ([]TaskInfo, error) {
	return a.Tasks("")
}

// CreateTaskParams holds all fields for task creation.
type CreateTaskParams struct {
	SessionID  string
	Title      string
	Priority   string
	Tags       []string
	Context    string   // Description of what needs to be done
	FilePath   string   // Related file
	LineNumber int      // Related line
	BlockedBy  []string // Task IDs this is blocked by
}

type CreateTaskResult struct {
	TaskIDs []string `json:"task_ids"`
	Count   int      `json:"count,omitempty"`
}

// CreateTask creates a new task in a session.
func (a *AgentBridge) CreateTask(p CreateTaskParams) (*CreateTaskResult, error) {
	if p.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	task := map[string]any{
		"title": p.Title,
	}
	if p.Priority != "" {
		task["priority"] = p.Priority
	}
	if len(p.Tags) > 0 {
		task["tags"] = p.Tags
	}
	if p.Context != "" {
		task["context"] = p.Context
	}
	if p.FilePath != "" {
		task["file_path"] = p.FilePath
	}
	if p.LineNumber > 0 {
		task["line_number"] = p.LineNumber
	}
	if len(p.BlockedBy) > 0 {
		task["blocked_by"] = p.BlockedBy
	}
	args := map[string]any{
		"session_id": p.SessionID,
		"tasks":      []map[string]any{task},
	}
	var result CreateTaskResult
	if err := a.callAgentTool("agent_task_add", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdateTaskParams holds all fields for task updates.
type UpdateTaskParams struct {
	ID         string `json:"task_id"`
	AgentID    string `json:"agent_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Status     string `json:"status"`
	Title      string `json:"title,omitempty"`
	Priority   string `json:"priority,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

// UpdateTask updates a task's status, priority, and/or resolution.
func (a *AgentBridge) UpdateTask(p UpdateTaskParams) error {
	args := map[string]any{"task_id": p.ID}
	if p.Status != "" {
		args["status"] = p.Status
	}
	if p.Priority != "" {
		args["priority"] = p.Priority
	}
	if p.Resolution != "" {
		args["resolution"] = p.Resolution
	}
	return a.callAgentTool("agent_task_update", args, nil)
}

// --- Dispatch + Claim methods ---

// DispatchTaskParams holds parameters for dispatching a task to an agent.
type DispatchTaskParams struct {
	TargetAgentID   string
	SourceSessionID string
	Title           string
	Context         string
	Priority        string
	Tags            []string
	FilePath        string
	LineNumber      int
	BlockedBy       []string
}

// DispatchTask creates a task and a handoff targeting a specific agent.
// This enables the HUD or CLI to push work to an active agent.
func (a *AgentBridge) DispatchTask(p DispatchTaskParams) (map[string]any, error) {
	// Find target agent's active session for task creation.
	session, err := a.GetActiveSession(p.TargetAgentID)
	if err != nil {
		return nil, fmt.Errorf("find target session: %w", err)
	}

	sessionID := ""
	createdTaskID := ""
	if session != nil {
		sessionID = session.ID
	}

	taskCreated := false

	// Create the task.
	if sessionID != "" {
		taskResult, err := a.CreateTask(CreateTaskParams{
			SessionID:  sessionID,
			Title:      p.Title,
			Context:    p.Context,
			Priority:   p.Priority,
			Tags:       mergeDispatchTags(p.Tags),
			FilePath:   p.FilePath,
			LineNumber: p.LineNumber,
			BlockedBy:  p.BlockedBy,
		})
		if err != nil {
			return nil, fmt.Errorf("create task: %w", err)
		}
		if taskResult != nil && len(taskResult.TaskIDs) > 0 {
			createdTaskID = strings.TrimSpace(taskResult.TaskIDs[0])
		}
		taskCreated = true
	}

	sourceSessionID, err := a.resolveDispatchSourceSessionID(strings.TrimSpace(p.SourceSessionID))
	if err != nil {
		return nil, fmt.Errorf("resolve dispatch source session: %w", err)
	}

	// Create a handoff targeting the agent.
	handoffSummary := fmt.Sprintf("[Dispatched] %s", p.Title)
	instructions := handoffSummary
	if ctx := strings.TrimSpace(p.Context); ctx != "" {
		instructions += "\n\n" + ctx
	}
	handoffResult, err := a.HandoffCreate(HandoffCreateParams{
		SessionID:     sourceSessionID,
		TargetAgentID: p.TargetAgentID,
		Instructions:  instructions,
		HandoffType:   "summary_only",
	})
	if err != nil {
		return nil, fmt.Errorf("create handoff: %w", err)
	}

	return map[string]any{
		"ok":                true,
		"target_agent_id":   p.TargetAgentID,
		"source_session_id": sourceSessionID,
		"session_id":        sessionID,
		"task_id":           createdTaskID,
		"handoff_id":        handoffResult.HandoffID,
		"title":             p.Title,
		"priority":          p.Priority,
		"task_created":      taskCreated,
		"handoff_created":   true,
	}, nil
}

func (a *AgentBridge) resolveDispatchSourceSessionID(sourceSessionID string) (string, error) {
	if sourceSessionID != "" {
		return sourceSessionID, nil
	}
	result, err := a.StartSession(SessionStartParams{
		Namespace:   hudDispatchNamespace,
		AgentID:     hudDispatchAgentID,
		AgentType:   hudDispatchAgentID,
		Description: "HUD dispatch handoff source",
		AutoRecall:  false,
	})
	if err != nil {
		return "", err
	}
	if result == nil || strings.TrimSpace(result.SessionID) == "" {
		return "", fmt.Errorf("dispatcher session start returned empty session_id")
	}
	return strings.TrimSpace(result.SessionID), nil
}

func mergeDispatchTags(tags []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tags)+1)

	for _, tag := range append([]string{"dispatched"}, tags...) {
		normalized := strings.TrimSpace(tag)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}
