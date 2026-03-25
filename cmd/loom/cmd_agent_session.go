package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func startSessionWithFallback(cmd *cobra.Command, port string, p bridge.SessionStartParams) (json.RawMessage, error) {
	return withAgentFallback(
		"agent session-start",
		func() (json.RawMessage, error) {
			// Skip slow HUD POST when HUD is clearly not reachable.
			if _, err := hudGetFast(port, "/api/ping", sessionStartHUDPingTimeout); err != nil {
				return nil, err
			}
			return hudPostFast(port, "/api/agent/session-start", p, sessionStartHUDPostTimeout)
		},
		func() (json.RawMessage, error) {
			return withAgentBridge(cmd, func(agentBridge *bridge.AgentBridge) (json.RawMessage, error) {
				result, err := agentBridge.StartSession(p)
				if err != nil {
					return nil, err
				}
				return json.Marshal(result)
			})
		},
	)
}

func endSessionWithFallback(cmd *cobra.Command, port string, p bridge.SessionEndParams) (json.RawMessage, error) {
	return withAgentFallback(
		"agent session-end",
		func() (json.RawMessage, error) {
			return hudPost(port, "/api/agent/session-end", p)
		},
		func() (json.RawMessage, error) {
			return withAgentBridge(cmd, func(agentBridge *bridge.AgentBridge) (json.RawMessage, error) {
				_, err := agentBridge.EndSession(p)
				if err != nil {
					return nil, err
				}
				return json.Marshal(map[string]bool{"ok": true})
			})
		},
	)
}

func ensureDaemonSession(cmd *cobra.Command, p bridge.SessionStartParams) error {
	_, err := withAgentBridge(cmd, func(agentBridge *bridge.AgentBridge) (json.RawMessage, error) {
		result, err := agentBridge.StartSession(p)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	})
	return err
}

func activeSessionWithFallback(cmd *cobra.Command, port, agentID string) (json.RawMessage, error) {
	return withAgentFallback(
		"agent session",
		func() (json.RawMessage, error) {
			return hudGet(port, "/api/agent/session?agent_id="+agentID)
		},
		func() (json.RawMessage, error) {
			return withAgentBridge(cmd, func(agentBridge *bridge.AgentBridge) (json.RawMessage, error) {
				session, err := agentBridge.GetActiveSession(agentID)
				if err != nil {
					return nil, err
				}
				return json.Marshal(map[string]any{"session": session})
			})
		},
	)
}

// newAgentSessionStartCmd creates the `loom agent session-start` command.
func newAgentSessionStartCmd() *cobra.Command {
	var (
		namespace             string
		agentID               string
		agentType             string
		description           string
		autoRecall            bool
		autoRecallStrategy    string
		autoRecallQuery       string
		autoRecallTokenBudget int
		parentSessionID       string
		quiet                 bool
	)

	cmd := &cobra.Command{
		Use:   "session-start",
		Short: "Start an agent session (idempotent)",
		Long: `Start a new agent session, register presence, and optionally recall context.

This command is idempotent: if the agent already has an active session in the
same namespace, the existing session ID is returned without creating a duplicate.

Designed for use in Claude Code SessionStart hooks.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			result, err := startSessionWithFallback(cmd, port, bridge.SessionStartParams{
				Namespace:             namespace,
				AgentID:               agentID,
				AgentType:             agentType,
				Description:           description,
				AutoRecall:            autoRecall,
				AutoRecallStrategy:    autoRecallStrategy,
				AutoRecallQuery:       autoRecallQuery,
				AutoRecallTokenBudget: autoRecallTokenBudget,
				ParentSessionID:       parentSessionID,
			})
			if err != nil {
				if quiet {
					return nil // Silent failure for hooks.
				}
				return err
			}

			if !quiet {
				fmt.Println(string(result))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", "", "Session namespace (e.g., project/feature-branch)")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent identifier (e.g., claude-code)")
	cmd.Flags().StringVar(&agentType, "agent-type", "", "Agent type (e.g., claude-code)")
	cmd.Flags().StringVar(&description, "description", "", "Session description")
	cmd.Flags().BoolVar(&autoRecall, "auto-recall", false, "Auto-recall context on start")
	cmd.Flags().StringVar(&autoRecallStrategy, "auto-recall-strategy", "balanced", "Auto-recall depth profile: fast, balanced, deep")
	cmd.Flags().StringVar(&autoRecallQuery, "auto-recall-query", "", "Override auto-recall query (defaults to description, then namespace)")
	cmd.Flags().IntVar(&autoRecallTokenBudget, "auto-recall-token-budget", 0, "Override auto-recall token budget (256-32000)")
	cmd.Flags().StringVar(&parentSessionID, "parent-session-id", "", "Parent session ID (for subagent session grouping)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

	return cmd
}

// newAgentSessionEndCmd creates the `loom agent session-end` command.
func newAgentSessionEndCmd() *cobra.Command {
	var (
		sessionID    string
		agentID      string
		summarize    = true
		summaryAsync bool
		quiet        bool
	)

	cmd := &cobra.Command{
		Use:   "session-end",
		Short: "End an agent session",
		Long: `End the active session, optionally compress context, and deregister presence.

If --session-id is not provided, finds the active session by --agent-id.

Designed for use in Claude Code Stop hooks.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			result, err := endSessionWithFallback(cmd, port, bridge.SessionEndParams{
				SessionID:    sessionID,
				AgentID:      agentID,
				Summarize:    &summarize,
				SummaryAsync: summaryAsync,
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

	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to end (optional; finds by agent-id)")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent identifier")
	cmd.Flags().BoolVar(&summarize, "summarize", true, "Summarize and compress context on end")
	cmd.Flags().BoolVar(&summaryAsync, "summary-async", false, "Queue summarization in background and return immediately")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

	return cmd
}

// newAgentSessionCmd creates the `loom agent session` command.
func newAgentSessionCmd() *cobra.Command {
	var (
		agentID string
		quiet   bool
	)

	cmd := &cobra.Command{
		Use:   "session",
		Short: "Get the active session for an agent",
		Long:  `Query the HUD for the currently active session. Useful for scripts and hooks that need the session ID.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			result, err := activeSessionWithFallback(cmd, port, agentID)
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
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

	return cmd
}

// newAgentSessionListCmd creates the `loom agent session-list` command.
func newAgentSessionListCmd() *cobra.Command {
	var (
		namespace string
		agentID   string
		status    string
		limit     int
	)

	cmd := &cobra.Command{
		Use:   "session-list",
		Short: "List agent sessions",
		Long: `List sessions, optionally filtered by agent, namespace, or status.

Example:
  loom agent session-list --status summarized --limit 50
  loom agent session-list --agent-id claude-code --status active`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			params := map[string]any{
				"limit": limit,
			}
			if agentID != "" {
				params["agent_id"] = agentID
			}
			if namespace != "" {
				params["namespace"] = namespace
			}
			if status != "" {
				params["status"] = status
			}

			result, err := withAgentFallback(
				"agent session-list",
				func() (json.RawMessage, error) {
					return hudPost(port, "/api/agent/session-list", params)
				},
				func() (json.RawMessage, error) {
					return withAgentBridge(cmd, func(b *bridge.AgentBridge) (json.RawMessage, error) {
						return b.ListSessions(params)
					})
				},
			)
			if err != nil {
				return err
			}
			fmt.Println(string(result))
			return nil
		},
	}

	cmd.Flags().StringVar(&agentID, "agent-id", "", "Filter by agent ID")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Filter by namespace")
	cmd.Flags().StringVar(&status, "status", "", "Filter by status (active, ended, summarized)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum sessions to return")

	return cmd
}

// newAgentSessionPruneCmd creates the `loom agent session-prune` command.
func newAgentSessionPruneCmd() *cobra.Command {
	var (
		maxAge string
		status string
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "session-prune",
		Short: "Prune stale sessions",
		Long: `Delete stale sessions matching status and age criteria.

Example:
  loom agent session-prune --max-age 72h --dry-run
  loom agent session-prune --max-age 72h --status summarized,ended`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			// Parse max-age duration to hours
			dur, err := time.ParseDuration(maxAge)
			if err != nil {
				return fmt.Errorf("invalid --max-age: %w", err)
			}
			maxAgeHours := int(dur.Hours())
			if maxAgeHours <= 0 {
				maxAgeHours = 1
			}

			params := map[string]any{
				"max_age_hours": maxAgeHours,
				"status":        status,
				"dry_run":       dryRun,
			}

			result, err := withAgentFallback(
				"agent session-prune",
				func() (json.RawMessage, error) {
					return hudPost(port, "/api/agent/session-prune", params)
				},
				func() (json.RawMessage, error) {
					return withAgentBridge(cmd, func(b *bridge.AgentBridge) (json.RawMessage, error) {
						return b.PruneSessions(params)
					})
				},
			)
			if err != nil {
				return err
			}
			fmt.Println(string(result))
			return nil
		},
	}

	cmd.Flags().StringVar(&maxAge, "max-age", "72h", "Maximum session age (e.g., 72h, 168h)")
	cmd.Flags().StringVar(&status, "status", "ended,summarized", "Comma-separated status filter")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview what would be pruned without deleting")

	return cmd
}

// inferGitNamespace derives a namespace from the current git repository and branch.
// Returns "repo-name/branch" or empty string if git context is unavailable.
func inferGitNamespace() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Get repo root directory name.
	toplevel, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	repoName := filepath.Base(strings.TrimSpace(string(toplevel)))

	// Get current branch.
	branch, err := exec.CommandContext(ctx, "git", "branch", "--show-current").Output()
	if err != nil {
		return repoName
	}
	branchName := strings.TrimSpace(string(branch))
	if branchName == "" {
		return repoName
	}

	return repoName + "/" + branchName
}
