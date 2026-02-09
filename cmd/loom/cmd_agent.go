// cmd_agent.go implements `loom agent` subcommands for agent lifecycle management.
//
// These commands call the HUD REST API (not the daemon socket directly) to
// manage sessions, presence, and tasks. They are designed to be called from
// Claude Code hooks, shell scripts, and other automation.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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

// resolvePort returns the HUD port from flag, env var, or default.
func resolvePort(cmd *cobra.Command) string {
	port, _ := cmd.Flags().GetString("port")
	if port != "" {
		return port
	}
	if p := os.Getenv("LOOM_HUD_PORT"); p != "" {
		return p
	}
	return defaultHUDPort
}

// newAgentCmd creates the `loom agent` command group and all subcommands.
func newAgentCmd() *cobra.Command {
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Agent lifecycle management (sessions, heartbeats, tasks)",
		Long: `Manage agent lifecycle through the HUD API.

These commands are designed to be called from Claude Code hooks, shell scripts,
and other automation to ensure consistent session tracking and presence management.

The HUD server must be running (default port 3333). Set LOOM_HUD_PORT or use
--port to override.`,
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

			// Preflight ping: fail fast if HUD is not running (1s timeout).
			if _, err := hudGetFast(port, "/api/ping", 1*time.Second); err != nil {
				if quiet {
					fmt.Fprintf(os.Stderr, "loom: session-start: HUD not reachable: %v\n", err)
					return nil
				}
				return fmt.Errorf("HUD not reachable at port %s: %w", port, err)
			}

			body := map[string]any{
				"namespace":   namespace,
				"agent_id":    agentID,
				"agent_type":  agentType,
				"description": description,
				"auto_recall": autoRecall,
			}

			result, err := hudPost(port, "/api/agent/session-start", body)
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

			body := map[string]any{
				"agent_id":  agentID,
				"summarize": summarize,
			}
			if sessionID != "" {
				body["session_id"] = sessionID
			}

			result, err := hudPost(port, "/api/agent/session-end", body)
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
		agentID string
		status  string
		quiet   bool
	)

	cmd := &cobra.Command{
		Use:   "heartbeat",
		Short: "Send agent presence heartbeat",
		Long: `Update the agent's presence heartbeat timestamp and optional status.

Designed for use in Claude Code PostToolUse hooks to keep presence alive
during active tool use.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			body := map[string]any{
				"agent_id": agentID,
			}
			if status != "" {
				body["status"] = status
			}

			// Heartbeat uses a short 3s timeout with retry (50ms, 100ms, 200ms backoff).
			_, err := hudPostWithRetry(port, "/api/agent/heartbeat", body,
				3*time.Second,
				[]time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond},
			)
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
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

	return cmd
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

			body := map[string]any{
				"task_id": taskID,
				"status":  status,
			}
			if resolution != "" {
				body["resolution"] = resolution
			}

			result, err := hudPost(port, "/api/agent/task-update", body)
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

			result, err := hudGet(port, "/api/agent/session?agent_id="+agentID)
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
with the agent-context workflow engine via the HUD API.

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

				result, err := hudPost(port, "/api/agent/workflow-define", body)
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
