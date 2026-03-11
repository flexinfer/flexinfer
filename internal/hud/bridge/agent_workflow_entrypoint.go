package bridge

import (
	"fmt"
	"strings"
)

type WorkStartParams struct {
	AgentID     string
	AgentType   string
	Namespace   string
	Description string

	WorktreeBranch     string
	WorktreeBaseBranch string
	WorktreePurpose    string
	WorktreeTTLHours   int

	TaskTitle      string
	TaskContext    string
	TaskPriority   string
	TaskTags       []string
	TaskFilePath   string
	TaskLineNumber int
	TaskBlockedBy  []string

	HeartbeatStatus string
	HeartbeatFiles  []string
}

type WorkStartResult struct {
	SessionID    string `json:"session_id"`
	AssignmentID string `json:"assignment_id"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
	TaskID       string `json:"task_id"`
}

// WorkStart enforces a single fail-fast lifecycle entrypoint:
// session start -> managed worktree -> task create -> in_progress -> heartbeat.
func (a *AgentBridge) WorkStart(p WorkStartParams) (*WorkStartResult, error) {
	agentID := strings.TrimSpace(p.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	taskTitle := strings.TrimSpace(p.TaskTitle)
	if taskTitle == "" {
		return nil, fmt.Errorf("task_title is required")
	}
	branchName := strings.TrimSpace(p.WorktreeBranch)
	if branchName == "" {
		return nil, fmt.Errorf("worktree_branch is required")
	}

	startResult, err := a.StartSession(SessionStartParams{
		Namespace:   strings.TrimSpace(p.Namespace),
		AgentID:     agentID,
		AgentType:   strings.TrimSpace(p.AgentType),
		Description: strings.TrimSpace(p.Description),
		AutoRecall:  false,
	})
	if err != nil {
		return nil, fmt.Errorf("work-start step=session-start: %w", err)
	}
	if startResult == nil || strings.TrimSpace(startResult.SessionID) == "" {
		return nil, fmt.Errorf("work-start step=session-start: empty session_id")
	}
	sessionID := strings.TrimSpace(startResult.SessionID)

	worktree, err := a.WorktreeAllocate(WorktreeAllocateParams{
		AgentID:    agentID,
		SessionID:  sessionID,
		BranchName: branchName,
		BaseBranch: strings.TrimSpace(p.WorktreeBaseBranch),
		Purpose:    strings.TrimSpace(p.WorktreePurpose),
		TTLHours:   p.WorktreeTTLHours,
	})
	if err != nil {
		return nil, fmt.Errorf("work-start step=worktree-allocate session_id=%s: %w", sessionID, err)
	}
	if worktree == nil || strings.TrimSpace(worktree.AssignmentID) == "" {
		return nil, fmt.Errorf("work-start step=worktree-allocate session_id=%s: empty assignment_id", sessionID)
	}

	taskResult, err := a.CreateTask(CreateTaskParams{
		SessionID:  sessionID,
		Title:      taskTitle,
		Context:    strings.TrimSpace(p.TaskContext),
		Priority:   strings.TrimSpace(p.TaskPriority),
		Tags:       p.TaskTags,
		FilePath:   strings.TrimSpace(p.TaskFilePath),
		LineNumber: p.TaskLineNumber,
		BlockedBy:  p.TaskBlockedBy,
	})
	if err != nil {
		return nil, fmt.Errorf("work-start step=task-add session_id=%s assignment_id=%s: %w", sessionID, worktree.AssignmentID, err)
	}
	if taskResult == nil || len(taskResult.TaskIDs) == 0 || strings.TrimSpace(taskResult.TaskIDs[0]) == "" {
		return nil, fmt.Errorf("work-start step=task-add session_id=%s assignment_id=%s: empty task_id", sessionID, worktree.AssignmentID)
	}
	taskID := strings.TrimSpace(taskResult.TaskIDs[0])

	if err := a.UpdateTask(UpdateTaskParams{ID: taskID, Status: "in_progress"}); err != nil {
		return nil, fmt.Errorf("work-start step=task-update session_id=%s assignment_id=%s task_id=%s: %w", sessionID, worktree.AssignmentID, taskID, err)
	}

	status := strings.TrimSpace(p.HeartbeatStatus)
	if status == "" {
		status = "active"
	}
	if _, err := a.PresenceHeartbeat(agentID, PresenceHeartbeatParams{
		Status:      status,
		ActiveFiles: p.HeartbeatFiles,
		CurrentTask: taskTitle,
		Branch:      strings.TrimSpace(worktree.Branch),
	}); err != nil {
		return nil, fmt.Errorf("work-start step=heartbeat session_id=%s assignment_id=%s task_id=%s: %w", sessionID, worktree.AssignmentID, taskID, err)
	}

	return &WorkStartResult{
		SessionID:    sessionID,
		AssignmentID: strings.TrimSpace(worktree.AssignmentID),
		WorktreePath: strings.TrimSpace(worktree.WorktreePath),
		Branch:       strings.TrimSpace(worktree.Branch),
		TaskID:       taskID,
	}, nil
}

type WorkHandoffParams struct {
	SourceSessionID string
	SourceAgentID   string
	TargetAgentID   string
	Instructions    string
	HandoffType     string
	EntryIDs        []string
	TokenBudget     int

	SharedEntryType    string
	SharedEntryTitle   string
	SharedEntryContent string

	CreateDispatchTask   bool
	DispatchTaskTitle    string
	DispatchTaskContext  string
	DispatchTaskPriority string
	DispatchTaskTags     []string
	DispatchFilePath     string
	DispatchLineNumber   int
	DispatchBlockedBy    []string
}

type WorkHandoffResult struct {
	SourceSessionID string `json:"source_session_id"`
	HandoffID       string `json:"handoff_id"`
	DispatchTaskID  string `json:"dispatch_task_id,omitempty"`
}

// WorkHandoff enforces shared context + handoff creation and optional target dispatch task.
func (a *AgentBridge) WorkHandoff(p WorkHandoffParams) (*WorkHandoffResult, error) {
	targetAgentID := strings.TrimSpace(p.TargetAgentID)
	if targetAgentID == "" {
		return nil, fmt.Errorf("target_agent_id is required")
	}
	instructions := strings.TrimSpace(p.Instructions)
	if instructions == "" {
		return nil, fmt.Errorf("instructions are required")
	}

	sourceSessionID := strings.TrimSpace(p.SourceSessionID)
	if sourceSessionID == "" {
		sourceAgentID := strings.TrimSpace(p.SourceAgentID)
		if sourceAgentID == "" {
			return nil, fmt.Errorf("source_session_id or source_agent_id is required")
		}
		active, err := a.GetActiveSession(sourceAgentID)
		if err != nil {
			return nil, fmt.Errorf("work-handoff step=resolve-source-session source_agent_id=%s: %w", sourceAgentID, err)
		}
		if active == nil || strings.TrimSpace(active.ID) == "" {
			return nil, fmt.Errorf("work-handoff step=resolve-source-session source_agent_id=%s: no active session", sourceAgentID)
		}
		sourceSessionID = strings.TrimSpace(active.ID)
	}

	sharedEntryType := strings.TrimSpace(p.SharedEntryType)
	if sharedEntryType == "" {
		sharedEntryType = "note"
	}
	sharedEntryTitle := strings.TrimSpace(p.SharedEntryTitle)
	if sharedEntryTitle == "" {
		sharedEntryTitle = "Shared handoff context"
	}
	sharedEntryContent := strings.TrimSpace(p.SharedEntryContent)
	if sharedEntryContent == "" {
		sharedEntryContent = instructions
	}

	if err := a.ContextAdd(sourceSessionID, []map[string]any{
		{
			"entry_type":  sharedEntryType,
			"title":       sharedEntryTitle,
			"content":     sharedEntryContent,
			"visibility":  "shared",
			"shared_with": []string{targetAgentID},
		},
	}); err != nil {
		return nil, fmt.Errorf("work-handoff step=context-share source_session_id=%s: %w", sourceSessionID, err)
	}

	handoffResult, err := a.HandoffCreate(HandoffCreateParams{
		SessionID:     sourceSessionID,
		TargetAgentID: targetAgentID,
		Instructions:  instructions,
		HandoffType:   strings.TrimSpace(p.HandoffType),
		EntryIDs:      p.EntryIDs,
		TokenBudget:   p.TokenBudget,
	})
	if err != nil {
		return nil, fmt.Errorf("work-handoff step=handoff-create source_session_id=%s: %w", sourceSessionID, err)
	}
	handoffID := ""
	if handoffResult != nil {
		handoffID = strings.TrimSpace(handoffResult.HandoffID)
	}

	result := &WorkHandoffResult{
		SourceSessionID: sourceSessionID,
		HandoffID:       handoffID,
	}

	if !p.CreateDispatchTask {
		return result, nil
	}

	targetSession, err := a.GetActiveSession(targetAgentID)
	if err != nil {
		return nil, fmt.Errorf("work-handoff step=resolve-target-session target_agent_id=%s handoff_id=%s: %w", targetAgentID, handoffID, err)
	}
	if targetSession == nil || strings.TrimSpace(targetSession.ID) == "" {
		return nil, fmt.Errorf("work-handoff step=resolve-target-session target_agent_id=%s handoff_id=%s: no active session", targetAgentID, handoffID)
	}

	dispatchTitle := strings.TrimSpace(p.DispatchTaskTitle)
	if dispatchTitle == "" {
		dispatchTitle = "Follow handoff " + handoffID
	}
	dispatchTask, err := a.CreateTask(CreateTaskParams{
		SessionID:  strings.TrimSpace(targetSession.ID),
		Title:      dispatchTitle,
		Context:    strings.TrimSpace(p.DispatchTaskContext),
		Priority:   strings.TrimSpace(p.DispatchTaskPriority),
		Tags:       mergeDispatchTags(p.DispatchTaskTags),
		FilePath:   strings.TrimSpace(p.DispatchFilePath),
		LineNumber: p.DispatchLineNumber,
		BlockedBy:  p.DispatchBlockedBy,
	})
	if err != nil {
		return nil, fmt.Errorf("work-handoff step=dispatch-task-add target_agent_id=%s handoff_id=%s: %w", targetAgentID, handoffID, err)
	}
	if dispatchTask == nil || len(dispatchTask.TaskIDs) == 0 {
		return nil, fmt.Errorf("work-handoff step=dispatch-task-add target_agent_id=%s handoff_id=%s: empty task_id", targetAgentID, handoffID)
	}

	result.DispatchTaskID = strings.TrimSpace(dispatchTask.TaskIDs[0])
	return result, nil
}
