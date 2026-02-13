// cmd_agent.go implements `loom agent` subcommands for agent lifecycle management.
//
// These commands prefer the HUD REST API and fall back to daemon socket calls
// when HUD is unavailable. They are designed to be called from Claude Code
// hooks, shell scripts, and other automation.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/crb2nu/loom/internal/hud"
	"github.com/crb2nu/loom/internal/hud/bridge"
)

// defaultHUDPort is the default port for the Agent HUD server.
const defaultHUDPort = "3333"

// hudBaseURL builds the base URL for the HUD API.
func hudBaseURL(port string) string {
	return "http://127.0.0.1:" + port
}

// hudPost sends a POST request with a JSON body to the HUD API.
// Returns the response body or an error. On non-2xx status, returns an error.
func hudPost(port, path string, body any) (json.RawMessage, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hudBaseURL(port)+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HUD returned %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}

// hudGet sends a GET request to the HUD API.
func hudGet(port, path string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hudBaseURL(port)+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HUD returned %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}

// hudPostFast sends a POST with a short timeout (for latency-sensitive ops like heartbeats).
func hudPostFast(port, path string, body any, timeout time.Duration) (json.RawMessage, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hudBaseURL(port)+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HUD returned %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}

// hudGetFast sends a GET with a short timeout (for preflight health checks).
func hudGetFast(port, path string, timeout time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hudBaseURL(port)+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HUD returned %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}

// hudPostWithRetry sends a POST with retry and exponential backoff.
// Retries up to maxAttempts times with the given backoff schedule.
func hudPostWithRetry(port, path string, body any, timeout time.Duration, backoffs []time.Duration) (json.RawMessage, error) {
	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		result, err := hudPostFast(port, path, body, timeout)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt < len(backoffs) {
			time.Sleep(backoffs[attempt])
		}
	}
	return nil, lastErr
}

// resolvePort returns the HUD port from flag, env var, port file, or default.
// Priority: --port flag > LOOM_HUD_PORT env > port file > 3333.
func resolvePort(cmd *cobra.Command) string {
	port, _ := cmd.Flags().GetString("port")
	if port != "" {
		return port
	}
	if p := os.Getenv("LOOM_HUD_PORT"); p != "" {
		return p
	}
	// Read port file written by the running HUD.
	if data, err := os.ReadFile(hud.PortFilePath()); err == nil {
		if p := strings.TrimSpace(string(data)); p != "" {
			return p
		}
	}
	return defaultHUDPort
}

// resolveSocketPath returns the daemon socket path from inherited --socket,
// LOOM_SOCKET, or the default ~/.config/loom/loom.sock.
func resolveSocketPath(cmd *cobra.Command) string {
	if cmd != nil {
		if socketPath, err := cmd.Flags().GetString("socket"); err == nil && strings.TrimSpace(socketPath) != "" {
			return socketPath
		}
		if socketPath, err := cmd.InheritedFlags().GetString("socket"); err == nil && strings.TrimSpace(socketPath) != "" {
			return socketPath
		}
	}
	if socketPath := strings.TrimSpace(os.Getenv("LOOM_SOCKET")); socketPath != "" {
		return socketPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "loom", "loom.sock")
}

func withAgentBridge(cmd *cobra.Command, fn func(*bridge.AgentBridge) (json.RawMessage, error)) (json.RawMessage, error) {
	socketPath := resolveSocketPath(cmd)
	client := bridge.NewDaemonClient(socketPath, nil)
	if err := client.Connect(); err != nil {
		return nil, err
	}
	defer client.Close()
	return fn(bridge.NewAgentBridge(client))
}

func withAgentFallback(op string, hudCall, daemonCall func() (json.RawMessage, error)) (json.RawMessage, error) {
	result, err := hudCall()
	if err == nil {
		return result, nil
	}

	fallbackResult, fallbackErr := daemonCall()
	if fallbackErr == nil {
		return fallbackResult, nil
	}

	return nil, fmt.Errorf("%s failed via HUD (%v) and daemon fallback (%w)", op, err, fallbackErr)
}

func startSessionWithFallback(cmd *cobra.Command, port string, p bridge.SessionStartParams) (json.RawMessage, error) {
	body := map[string]any{
		"namespace":   p.Namespace,
		"agent_id":    p.AgentID,
		"agent_type":  p.AgentType,
		"description": p.Description,
		"auto_recall": p.AutoRecall,
	}

	return withAgentFallback(
		"agent session-start",
		func() (json.RawMessage, error) {
			// Skip slow HUD POST when HUD is clearly not reachable.
			if _, err := hudGetFast(port, "/api/ping", 1*time.Second); err != nil {
				return nil, err
			}
			return hudPost(port, "/api/agent/session-start", body)
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
	body := map[string]any{
		"agent_id":  p.AgentID,
		"summarize": p.Summarize,
	}
	if p.SessionID != "" {
		body["session_id"] = p.SessionID
	}

	return withAgentFallback(
		"agent session-end",
		func() (json.RawMessage, error) {
			return hudPost(port, "/api/agent/session-end", body)
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

func heartbeatWithFallback(cmd *cobra.Command, port string, agentID, status string) error {
	body := map[string]any{
		"agent_id": agentID,
	}
	if status != "" {
		body["status"] = status
	}

	_, err := withAgentFallback(
		"agent heartbeat",
		func() (json.RawMessage, error) {
			return hudPostWithRetry(port, "/api/agent/heartbeat", body,
				3*time.Second,
				[]time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond},
			)
		},
		func() (json.RawMessage, error) {
			return withAgentBridge(cmd, func(agentBridge *bridge.AgentBridge) (json.RawMessage, error) {
				if _, err := agentBridge.PresenceHeartbeat(agentID, bridge.PresenceHeartbeatParams{Status: status}); err != nil {
					return nil, err
				}
				return json.Marshal(map[string]bool{"ok": true})
			})
		},
	)
	return err
}

func updateTaskWithFallback(cmd *cobra.Command, port string, p bridge.UpdateTaskParams) (json.RawMessage, error) {
	body := map[string]any{
		"task_id": p.ID,
		"status":  p.Status,
	}
	if p.Resolution != "" {
		body["resolution"] = p.Resolution
	}

	return withAgentFallback(
		"agent task-update",
		func() (json.RawMessage, error) {
			return hudPost(port, "/api/agent/task-update", body)
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

func workflowDefineWithFallback(cmd *cobra.Command, port string, body map[string]any) (json.RawMessage, error) {
	return withAgentFallback(
		"agent workflow-define",
		func() (json.RawMessage, error) {
			return hudPost(port, "/api/agent/workflow-define", body)
		},
		func() (json.RawMessage, error) {
			return withAgentBridge(cmd, func(agentBridge *bridge.AgentBridge) (json.RawMessage, error) {
				result, err := agentBridge.WorkflowDefine(body)
				if err != nil {
					return nil, err
				}
				return json.Marshal(result)
			})
		},
	)
}

// newAgentCmd creates the `loom agent` command group and all subcommands.
func newAgentCmd() *cobra.Command {
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Agent lifecycle management (sessions, heartbeats, tasks)",
		Long: `Manage agent lifecycle via HUD API with daemon fallback.

These commands are designed to be called from Claude Code hooks, shell scripts,
and other automation to ensure consistent session tracking and presence management.

Set LOOM_HUD_PORT or use --port to target a non-default HUD instance. If HUD is
not reachable, commands fall back to daemon socket tool calls.`,
	}

	// Persistent flag for all subcommands.
	agentCmd.PersistentFlags().String("port", "", "HUD server port (default: $LOOM_HUD_PORT or 3333)")

	agentCmd.AddCommand(
		newAgentSessionStartCmd(),
		newAgentSessionEndCmd(),
		newAgentHeartbeatCmd(),
		newAgentTaskUpdateCmd(),
		newAgentSessionCmd(),
		newAgentWorkflowSyncCmd(),
		newAgentDispatchCmd(),
	)

	return agentCmd
}

// newAgentSessionStartCmd creates the `loom agent session-start` command.
func newAgentSessionStartCmd() *cobra.Command {
	var (
		namespace   string
		agentID     string
		agentType   string
		description string
		autoRecall  bool
		quiet       bool
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
				Namespace:   namespace,
				AgentID:     agentID,
				AgentType:   agentType,
				Description: description,
				AutoRecall:  autoRecall,
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
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

	return cmd
}

// newAgentSessionEndCmd creates the `loom agent session-end` command.
func newAgentSessionEndCmd() *cobra.Command {
	var (
		sessionID string
		agentID   string
		summarize bool
		quiet     bool
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
				SessionID: sessionID,
				AgentID:   agentID,
				Summarize: summarize,
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
	cmd.Flags().BoolVar(&summarize, "summarize", false, "Summarize and compress context on end")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

	return cmd
}

// newAgentHeartbeatCmd creates the `loom agent heartbeat` command.
func newAgentHeartbeatCmd() *cobra.Command {
	var (
		agentID        string
		status         string
		ensureSession  bool
		inferNamespace bool
		namespace      string
		agentType      string
		description    string
		prURL          string
		quiet          bool
	)

	cmd := &cobra.Command{
		Use:   "heartbeat",
		Short: "Send agent presence heartbeat",
		Long: `Update the agent's presence heartbeat timestamp and optional status.

Designed for use in Claude Code PostToolUse hooks to keep presence alive
during active tool use. Use --ensure-session for clients that only have
heartbeat hooks (for example Codex notify).

Use --infer-namespace to automatically derive the namespace from the current
git repo (repo-name/branch). This is useful for Codex and other agents that
don't have native session-start hooks.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			// Infer namespace from git context if requested.
			if inferNamespace && namespace == "" {
				namespace = inferGitNamespace()
			}

			err := heartbeatWithFallback(cmd, port, agentID, status)
			if err != nil && ensureSession {
				startNamespace := namespace
				startAgentType := agentType
				startDescription := description
				if startAgentType == "" {
					startAgentType = agentID
				}
				if startDescription == "" {
					startDescription = "Heartbeat bootstrap session"
				}

				_, ensureErr := startSessionWithFallback(cmd, port, bridge.SessionStartParams{
					Namespace:   startNamespace,
					AgentID:     agentID,
					AgentType:   startAgentType,
					Description: startDescription,
					AutoRecall:  false,
				})
				if ensureErr == nil {
					err = heartbeatWithFallback(cmd, port, agentID, status)
				} else {
					err = fmt.Errorf("%v (ensure-session failed: %w)", err, ensureErr)
				}
			}
			if err != nil {
				if quiet {
					// Log to stderr even in quiet mode (visible via 2> redirect).
					fmt.Fprintf(os.Stderr, "loom: heartbeat: %v\n", err)
					return nil
				}
				return err
			}

			if !quiet {
				fmt.Println(`{"ok":true}`)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent identifier")
	cmd.Flags().StringVar(&status, "status", "", "Agent status (active, idle)")
	cmd.Flags().BoolVar(&ensureSession, "ensure-session", false, "Auto-start session if heartbeat fails due to missing presence/session")
	cmd.Flags().BoolVar(&inferNamespace, "infer-namespace", false, "Derive namespace from git repo/branch context")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Namespace used with --ensure-session")
	cmd.Flags().StringVar(&agentType, "agent-type", "", "Agent type used with --ensure-session")
	cmd.Flags().StringVar(&description, "description", "", "Session description used with --ensure-session")
	cmd.Flags().StringVar(&prURL, "pr-url", "", "URL of the active pull request")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

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

// workflowYAML mirrors the YAML structure of workflow definition files.
type workflowYAML struct {
	ID                string           `yaml:"id"`
	Name              string           `yaml:"name"`
	Description       string           `yaml:"description"`
	Steps             []map[string]any `yaml:"steps"`
	InputSchema       map[string]any   `yaml:"input_schema"`
	RollbackOnFailure bool             `yaml:"rollback_on_failure"`
	TimeoutSeconds    int              `yaml:"timeout_seconds"`
}

// newAgentWorkflowSyncCmd creates the `loom agent workflow-sync` command.
func newAgentWorkflowSyncCmd() *cobra.Command {
	var (
		dir       string
		namespace string
		createdBy string
		quiet     bool
	)

	cmd := &cobra.Command{
		Use:   "workflow-sync",
		Short: "Register workflow definitions from YAML files",
		Long: `Read workflow definition YAML files from a directory and register them
with the agent-context workflow engine via HUD API with daemon fallback.

This is idempotent: re-registering a definition updates it in-memory.
Definitions are stored in-memory and must be re-synced after daemon restart.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			// Find YAML files.
			pattern := filepath.Join(dir, "*.yaml")
			files, err := filepath.Glob(pattern)
			if err != nil {
				return fmt.Errorf("glob %s: %w", pattern, err)
			}
			ymlPattern := filepath.Join(dir, "*.yml")
			ymlFiles, _ := filepath.Glob(ymlPattern)
			files = append(files, ymlFiles...)

			if len(files) == 0 {
				if !quiet {
					fmt.Printf("No workflow files found in %s\n", dir)
				}
				return nil
			}

			var registered, failed int
			for _, f := range files {
				body, err := loadWorkflowFile(f, namespace, createdBy)
				if err != nil {
					if !quiet {
						fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", filepath.Base(f), err)
					}
					failed++
					continue
				}

				result, err := workflowDefineWithFallback(cmd, port, body)
				if err != nil {
					if !quiet {
						fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", filepath.Base(f), err)
					}
					failed++
					continue
				}

				registered++
				if !quiet {
					var res struct {
						DefinitionID string `json:"definition_id"`
						Name         string `json:"name"`
						StepCount    int    `json:"step_count"`
					}
					_ = json.Unmarshal(result, &res)
					fmt.Printf("  ✓ %s (%s, %d steps)\n", res.Name, res.DefinitionID, res.StepCount)
				}
			}

			if !quiet {
				fmt.Printf("\nRegistered %d workflow(s)", registered)
				if failed > 0 {
					fmt.Printf(", %d failed", failed)
				}
				fmt.Println()
			}

			if failed > 0 {
				return fmt.Errorf("%d workflow(s) failed to register", failed)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".agents/workflows", "Directory containing workflow YAML files")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Override namespace for all definitions")
	cmd.Flags().StringVar(&createdBy, "created-by", "loom-cli", "Creator agent ID")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output")

	return cmd
}

// newAgentDispatchCmd creates the `loom agent dispatch` command.
func newAgentDispatchCmd() *cobra.Command {
	var (
		targetAgent string
		title       string
		ctx         string
		priority    string
		quiet       bool
	)

	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Dispatch a task to a specific agent",
		Long: `Create a task and handoff targeting a specific agent. The target agent
will see the dispatched task in its next heartbeat response.

This enables the HUD or CLI to push work to active agents.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			body := map[string]any{
				"target_agent_id": targetAgent,
				"title":           title,
				"context":         ctx,
				"priority":        priority,
			}

			result, err := hudPost(port, "/api/agent/dispatch", body)
			if err != nil {
				if quiet {
					fmt.Fprintf(os.Stderr, "loom: dispatch: %v\n", err)
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

	cmd.Flags().StringVar(&targetAgent, "to", "", "Target agent identifier (required)")
	cmd.Flags().StringVar(&title, "title", "", "Task title (required)")
	cmd.Flags().StringVar(&ctx, "context", "", "Additional context for the task")
	cmd.Flags().StringVar(&priority, "priority", "medium", "Priority (low, medium, high, critical)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("title")

	return cmd
}

// loadWorkflowFile reads a YAML workflow file and converts it to a map
// suitable for POSTing to the workflow-define API.
func loadWorkflowFile(path, namespace, createdBy string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var wf workflowYAML
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	if wf.Name == "" {
		return nil, fmt.Errorf("workflow name is required")
	}
	if len(wf.Steps) == 0 {
		return nil, fmt.Errorf("workflow must have at least one step")
	}

	body := map[string]any{
		"name":                wf.Name,
		"description":         wf.Description,
		"steps":               wf.Steps,
		"rollback_on_failure": wf.RollbackOnFailure,
		"created_by":          createdBy,
	}
	if wf.ID != "" {
		body["id"] = wf.ID
	}
	if namespace != "" {
		body["namespace"] = namespace
	}
	if wf.InputSchema != nil {
		body["input_schema"] = wf.InputSchema
	}
	if wf.TimeoutSeconds > 0 {
		body["timeout_seconds"] = wf.TimeoutSeconds
	}

	return body, nil
}
