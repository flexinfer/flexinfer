package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func contextInspectWithFallback(cmd *cobra.Command, port, agentID, sessionID string, detail bool, limit int) (json.RawMessage, error) {
	request := bridge.ContextInspectRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		Detail:    detail,
		Limit:     limit,
	}
	path, err := request.Path()
	if err != nil {
		return nil, err
	}

	return withAgentFallback(
		"agent context-inspect",
		func() (json.RawMessage, error) {
			return hudGet(port, path)
		},
		func() (json.RawMessage, error) {
			return withAgentBridge(cmd, func(agentBridge *bridge.AgentBridge) (json.RawMessage, error) {
				result, err := agentBridge.ContextInspect(agentID, sessionID, detail, limit)
				if err != nil {
					return nil, err
				}
				return json.Marshal(result)
			})
		},
	)
}

func updateTaskWithFallback(cmd *cobra.Command, port string, p bridge.UpdateTaskParams) (json.RawMessage, error) {
	return withAgentFallback(
		"agent task-update",
		func() (json.RawMessage, error) {
			return hudPost(port, "/api/agent/task-update", p)
		},
		func() (json.RawMessage, error) {
			return withAgentBridge(cmd, func(agentBridge *bridge.AgentBridge) (json.RawMessage, error) {
				if err := agentBridge.UpdateTask(p); err != nil {
					return nil, err
				}
				return json.Marshal(map[string]string{"status": "updated"})
			})
		},
	)
}

// newAgentTaskUpdateCmd creates the `loom agent task-update` command.
func newAgentTaskUpdateCmd() *cobra.Command {
	var (
		taskID     string
		status     string
		resolution string
		quiet      bool
	)

	cmd := &cobra.Command{
		Use:   "task-update",
		Short: "Update task status",
		Long:  `Update a task's status (pending → in_progress → completed) and optionally add a resolution note.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			result, err := updateTaskWithFallback(cmd, port, bridge.UpdateTaskParams{
				ID:         taskID,
				Status:     status,
				Resolution: resolution,
			})
			if err != nil {
				if quiet {
					return nil
				}
				return err
			}

			if !quiet {
				fmt.Println(string(result))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&taskID, "task-id", "", "Task ID to update")
	cmd.Flags().StringVar(&status, "status", "", "New status (pending, in_progress, completed)")
	cmd.Flags().StringVar(&resolution, "resolution", "", "Resolution note (for completed tasks)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

	return cmd
}

// newAgentContextInspectCmd creates the `loom agent context-inspect` command.
func newAgentContextInspectCmd() *cobra.Command {
	var (
		agentID   string
		sessionID string
		detail    bool
		limit     int
		quiet     bool
	)

	cmd := &cobra.Command{
		Use:   "context-inspect",
		Short: "Inspect context budget for an agent/session",
		Long: `Return a context budget breakdown for an agent session, including
entry-type aggregates and optional top-entry detail.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(agentID) == "" && strings.TrimSpace(sessionID) == "" {
				return fmt.Errorf("agent-id or session-id is required")
			}

			port := resolvePort(cmd)
			result, err := contextInspectWithFallback(cmd, port, agentID, sessionID, detail, limit)
			if err != nil {
				if quiet {
					return nil
				}
				return err
			}
			if !quiet {
				fmt.Println(string(result))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent identifier (uses active session when session-id is omitted)")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to inspect")
	cmd.Flags().BoolVar(&detail, "detail", false, "Include top entries by estimated token weight")
	cmd.Flags().IntVar(&limit, "limit", 200, "Maximum context entries to analyze")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

	return cmd
}
