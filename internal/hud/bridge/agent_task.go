package bridge

import (
	"fmt"
	"strings"
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

// CreateTask creates a new task in a session.
func (a *AgentBridge) CreateTask(p CreateTaskParams) error {
	if p.SessionID == "" {
		return fmt.Errorf("session_id is required")
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
	return a.callAgentTool("agent_task_add", args, nil)
}

// UpdateTaskParams holds all fields for task updates.
type UpdateTaskParams struct {
	ID         string `json:"task_id"`
	Status     string `json:"status"`
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
	TargetAgentID string
	Title         string
	Context       string
	Priority      string
	Tags          []string
	FilePath      string
	LineNumber    int
	BlockedBy     []string
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
	if session != nil {
		sessionID = session.ID
	}

	taskCreated := false

	// Create the task.
	if sessionID != "" {
		if err := a.CreateTask(CreateTaskParams{
			SessionID:  sessionID,
			Title:      p.Title,
			Context:    p.Context,
			Priority:   p.Priority,
			Tags:       mergeDispatchTags(p.Tags),
			FilePath:   p.FilePath,
			LineNumber: p.LineNumber,
			BlockedBy:  p.BlockedBy,
		}); err != nil {
			return nil, fmt.Errorf("create task: %w", err)
		}
		taskCreated = true
	}

	// Create a handoff targeting the agent.
	handoffSummary := fmt.Sprintf("[Dispatched] %s", p.Title)
	if err := a.HandoffCreate(p.TargetAgentID, handoffSummary, p.Context); err != nil {
		return nil, fmt.Errorf("create handoff: %w", err)
	}

	return map[string]any{
		"ok":              true,
		"target_agent_id": p.TargetAgentID,
		"session_id":      sessionID,
		"title":           p.Title,
		"priority":        p.Priority,
		"task_created":    taskCreated,
		"handoff_created": true,
	}, nil
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
