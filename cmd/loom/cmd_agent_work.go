package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func newAgentWorkStartCmd() *cobra.Command {
	var (
		agentID         string
		agentType       string
		namespace       string
		description     string
		worktreeBranch  string
		worktreeBase    string
		worktreePurpose string
		worktreeTTL     int
		taskTitle       string
		taskContext     string
		taskPriority    string
		taskTags        []string
		taskFile        string
		taskLine        int
		taskBlockedBy   []string
		heartbeatStatus string
		heartbeatFiles  []string
		quiet           bool
	)

	cmd := &cobra.Command{
		Use:   "work-start",
		Short: "Start a managed work session (session, worktree, task, heartbeat)",
		Long: `Run a single enforced workflow:
1) session start/ensure
2) managed worktree allocation
3) task creation
4) task status -> in_progress
5) initial heartbeat`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(agentID) == "" {
				return fmt.Errorf("--agent-id is required")
			}
			if strings.TrimSpace(namespace) == "" {
				return fmt.Errorf("--namespace is required")
			}
			if strings.TrimSpace(worktreeBranch) == "" {
				return fmt.Errorf("--worktree-branch is required")
			}
			if strings.TrimSpace(taskTitle) == "" {
				return fmt.Errorf("--task-title is required")
			}

			result, err := withAgentBridge(cmd, func(agentBridge *bridge.AgentBridge) (json.RawMessage, error) {
				workResult, err := agentBridge.WorkStart(bridge.WorkStartParams{
					AgentID:            strings.TrimSpace(agentID),
					AgentType:          strings.TrimSpace(agentType),
					Namespace:          strings.TrimSpace(namespace),
					Description:        strings.TrimSpace(description),
					WorktreeBranch:     strings.TrimSpace(worktreeBranch),
					WorktreeBaseBranch: strings.TrimSpace(worktreeBase),
					WorktreePurpose:    strings.TrimSpace(worktreePurpose),
					WorktreeTTLHours:   worktreeTTL,
					TaskTitle:          strings.TrimSpace(taskTitle),
					TaskContext:        strings.TrimSpace(taskContext),
					TaskPriority:       strings.TrimSpace(taskPriority),
					TaskTags:           taskTags,
					TaskFilePath:       strings.TrimSpace(taskFile),
					TaskLineNumber:     taskLine,
					TaskBlockedBy:      taskBlockedBy,
					HeartbeatStatus:    strings.TrimSpace(heartbeatStatus),
					HeartbeatFiles:     heartbeatFiles,
				})
				if err != nil {
					return nil, err
				}
				return json.Marshal(workResult)
			})
			if err != nil {
				return err
			}

			if !quiet {
				fmt.Println(string(result))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent identifier (required)")
	cmd.Flags().StringVar(&agentType, "agent-type", "", "Agent type")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Session namespace (required)")
	cmd.Flags().StringVar(&description, "description", "", "Session description")
	cmd.Flags().StringVar(&worktreeBranch, "worktree-branch", "", "Managed worktree branch name (required)")
	cmd.Flags().StringVar(&worktreeBase, "worktree-base", "", "Worktree base branch/commit")
	cmd.Flags().StringVar(&worktreePurpose, "worktree-purpose", "", "Worktree allocation purpose")
	cmd.Flags().IntVar(&worktreeTTL, "worktree-ttl-hours", 72, "Worktree TTL in hours (0 = no limit)")
	cmd.Flags().StringVar(&taskTitle, "task-title", "", "Task title (required)")
	cmd.Flags().StringVar(&taskContext, "task-context", "", "Task context/description")
	cmd.Flags().StringVar(&taskPriority, "priority", "medium", "Task priority (low, medium, high, critical)")
	cmd.Flags().StringSliceVar(&taskTags, "tag", nil, "Task tag(s)")
	cmd.Flags().StringVar(&taskFile, "file", "", "Task related file path")
	cmd.Flags().IntVar(&taskLine, "line", 0, "Task related line number")
	cmd.Flags().StringSliceVar(&taskBlockedBy, "blocked-by", nil, "Task IDs this task is blocked by")
	cmd.Flags().StringVar(&heartbeatStatus, "heartbeat-status", "active", "Heartbeat status")
	cmd.Flags().StringSliceVar(&heartbeatFiles, "heartbeat-file", nil, "Initial heartbeat active file(s)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output")

	return cmd
}

func newAgentWorkHandoffCmd() *cobra.Command {
	var (
		sourceSessionID      string
		sourceAgentID        string
		targetAgentID        string
		instructions         string
		handoffType          string
		entryIDs             []string
		tokenBudget          int
		sharedEntryType      string
		sharedEntryTitle     string
		sharedEntryContent   string
		createDispatchTask   bool
		dispatchTaskTitle    string
		dispatchTaskContext  string
		dispatchTaskPriority string
		dispatchTaskTags     []string
		dispatchFile         string
		dispatchLine         int
		dispatchBlockedBy    []string
		quiet                bool
	)

	cmd := &cobra.Command{
		Use:   "work-handoff",
		Short: "Create shared context + handoff (+ optional dispatch task)",
		Long: `Run a single enforced handoff workflow:
1) shared context entry via agent_context_add (visibility=shared)
2) handoff package creation
3) optional dispatch task for target agent`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(targetAgentID) == "" {
				return fmt.Errorf("--target-agent-id is required")
			}
			if strings.TrimSpace(instructions) == "" {
				return fmt.Errorf("--instructions is required")
			}
			if strings.TrimSpace(sourceSessionID) == "" && strings.TrimSpace(sourceAgentID) == "" {
				return fmt.Errorf("--source-session-id or --source-agent-id is required")
			}

			result, err := withAgentBridge(cmd, func(agentBridge *bridge.AgentBridge) (json.RawMessage, error) {
				handoffResult, err := agentBridge.WorkHandoff(bridge.WorkHandoffParams{
					SourceSessionID:      strings.TrimSpace(sourceSessionID),
					SourceAgentID:        strings.TrimSpace(sourceAgentID),
					TargetAgentID:        strings.TrimSpace(targetAgentID),
					Instructions:         strings.TrimSpace(instructions),
					HandoffType:          strings.TrimSpace(handoffType),
					EntryIDs:             entryIDs,
					TokenBudget:          tokenBudget,
					SharedEntryType:      strings.TrimSpace(sharedEntryType),
					SharedEntryTitle:     strings.TrimSpace(sharedEntryTitle),
					SharedEntryContent:   strings.TrimSpace(sharedEntryContent),
					CreateDispatchTask:   createDispatchTask,
					DispatchTaskTitle:    strings.TrimSpace(dispatchTaskTitle),
					DispatchTaskContext:  strings.TrimSpace(dispatchTaskContext),
					DispatchTaskPriority: strings.TrimSpace(dispatchTaskPriority),
					DispatchTaskTags:     dispatchTaskTags,
					DispatchFilePath:     strings.TrimSpace(dispatchFile),
					DispatchLineNumber:   dispatchLine,
					DispatchBlockedBy:    dispatchBlockedBy,
				})
				if err != nil {
					return nil, err
				}
				return json.Marshal(handoffResult)
			})
			if err != nil {
				return err
			}

			if !quiet {
				fmt.Println(string(result))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sourceSessionID, "source-session-id", "", "Source session ID")
	cmd.Flags().StringVar(&sourceAgentID, "source-agent-id", "", "Source agent ID (used when source-session-id is omitted)")
	cmd.Flags().StringVar(&targetAgentID, "target-agent-id", "", "Target agent ID (required)")
	cmd.Flags().StringVar(&instructions, "instructions", "", "Handoff instructions for target agent (required)")
	cmd.Flags().StringVar(&handoffType, "handoff-type", "summary_only", "Handoff type (summary_only, selective, full)")
	cmd.Flags().StringSliceVar(&entryIDs, "entry-id", nil, "Entry IDs for selective handoff")
	cmd.Flags().IntVar(&tokenBudget, "token-budget", 0, "Handoff token budget")
	cmd.Flags().StringVar(&sharedEntryType, "shared-entry-type", "note", "Shared context entry type")
	cmd.Flags().StringVar(&sharedEntryTitle, "shared-entry-title", "Shared handoff context", "Shared context entry title")
	cmd.Flags().StringVar(&sharedEntryContent, "shared-entry-content", "", "Shared context entry content (defaults to instructions)")
	cmd.Flags().BoolVar(&createDispatchTask, "create-dispatch-task", false, "Create a dispatch task in target agent's active session")
	cmd.Flags().StringVar(&dispatchTaskTitle, "dispatch-task-title", "", "Dispatch task title")
	cmd.Flags().StringVar(&dispatchTaskContext, "dispatch-task-context", "", "Dispatch task context")
	cmd.Flags().StringVar(&dispatchTaskPriority, "dispatch-priority", "medium", "Dispatch task priority")
	cmd.Flags().StringSliceVar(&dispatchTaskTags, "dispatch-tag", nil, "Dispatch task tag(s)")
	cmd.Flags().StringVar(&dispatchFile, "dispatch-file", "", "Dispatch task related file path")
	cmd.Flags().IntVar(&dispatchLine, "dispatch-line", 0, "Dispatch task related line number")
	cmd.Flags().StringSliceVar(&dispatchBlockedBy, "dispatch-blocked-by", nil, "Dispatch task dependency IDs")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output")

	return cmd
}
