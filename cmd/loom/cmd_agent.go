// cmd_agent.go implements `loom agent` subcommands for agent lifecycle management.
//
// These commands prefer the HUD REST API and fall back to daemon socket calls
// when HUD is unavailable. They are designed to be called from Claude Code
// hooks, shell scripts, and other automation.
//
// Domain files:
//   - cmd_agent_transport.go  — HTTP transport helpers (hudRequest, resolvePort, withAgentBridge, etc.)
//   - cmd_agent_session.go    — Session lifecycle commands (session-start, session-end, session, session-list, session-prune)
//   - cmd_agent_presence.go   — Presence/heartbeat commands (heartbeat, keepalive, hook-status)
//   - cmd_agent_context.go    — Context inspect and task update commands
//   - cmd_agent_nudge.go      — Nudge queue status and policy commands
//   - cmd_agent_dispatch.go   — Workflow sync, dispatch, and quality-gate commands
package main

import "github.com/spf13/cobra"

// defaultHUDPort is the default port for the Agent HUD server.
const defaultHUDPort = "3333"

// hudConfig caches HUD settings loaded from config.yaml.
// Loaded once per process to avoid repeated file reads in tight loops.
var hudConfigOnce struct {
	url      string
	host     string // Host header override (for internal ingress access)
	cfID     string
	cfSecret string
	loaded   bool
}

// newAgentCmd creates the `loom agent` command group and all subcommands.
func newAgentCmd() *cobra.Command {
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Agent lifecycle management (sessions, heartbeats, tasks)",
		Long: `Manage agent lifecycle via HUD API with daemon fallback.

These commands are designed to be called from Claude Code hooks, shell scripts,
and other automation to ensure consistent session tracking and presence management.

Set LOOM_HUD_URL for a remote/HTTPS HUD (e.g., https://hud.flexinfer.ai).
Set LOOM_HUD_PORT or use --port for a local HUD on a non-default port.
If HUD is not reachable, commands fall back to daemon socket tool calls.`,
	}

	// Persistent flag for all subcommands.
	agentCmd.PersistentFlags().String("port", "", "HUD server port (default: $LOOM_HUD_PORT or 3333)")

	agentCmd.AddCommand(
		newAgentSessionStartCmd(),
		newAgentSessionEndCmd(),
		newAgentHeartbeatCmd(),
		newAgentKeepaliveCmd(),
		newAgentTaskUpdateCmd(),
		newAgentSessionCmd(),
		newAgentSessionListCmd(),
		newAgentSessionPruneCmd(),
		newAgentHookStatusCmd(),
		newAgentContextInspectCmd(),
		newAgentNudgeQueueStatusCmd(),
		newAgentNudgeQueuePolicyCmd(),
		newAgentWorkflowSyncCmd(),
		newAgentDispatchCmd(),
		newAgentQualityGateCmd(),
		newAgentWorkStartCmd(),
		newAgentWorkHandoffCmd(),
	)

	return agentCmd
}
