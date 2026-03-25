package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// taskSyncPayload is the request body sent to POST /api/agent/task-sync.
type taskSyncPayload struct {
	AgentID   string         `json:"agent_id"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

// newAgentTaskSyncCmd creates the `loom agent task-sync` command.
// It reads Claude Code PostToolUse hook JSON from stdin and forwards the
// native task tool invocation to the HUD for bridging into the agent-context
// task system.
func newAgentTaskSyncCmd() *cobra.Command {
	var (
		agentID string
		quiet   bool
	)

	cmd := &cobra.Command{
		Use:   "task-sync",
		Short: "Sync native Claude Code task tools to agent-context tasks",
		Long: `Read PostToolUse hook JSON from stdin and forward native task tool
invocations (TaskCreate, TaskUpdate, TodoWrite) to the HUD API for bridging
into the agent-context task system.

This command is designed to be called from a Claude Code PostToolUse hook.
The hook pipes the tool invocation JSON to stdin.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			// Read stdin JSON from the PostToolUse hook.
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				if quiet {
					return nil
				}
				return fmt.Errorf("read stdin: %w", err)
			}
			if len(data) == 0 {
				if quiet {
					return nil
				}
				return fmt.Errorf("empty stdin")
			}

			// Parse the hook input to extract tool_name and tool_input.
			var hookInput struct {
				ToolName  string         `json:"tool_name"`
				ToolInput map[string]any `json:"tool_input"`
			}
			if err := json.Unmarshal(data, &hookInput); err != nil {
				if quiet {
					return nil
				}
				return fmt.Errorf("parse hook input: %w", err)
			}
			if hookInput.ToolName == "" {
				if quiet {
					return nil
				}
				return fmt.Errorf("tool_name is empty in hook input")
			}

			payload := taskSyncPayload{
				AgentID:   agentID,
				ToolName:  hookInput.ToolName,
				ToolInput: hookInput.ToolInput,
			}

			result, err := hudPost(port, "/api/agent/task-sync", payload)
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

	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent identifier")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output and errors (for hooks)")

	return cmd
}
