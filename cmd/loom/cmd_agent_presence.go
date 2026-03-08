package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// heartbeatResponse holds the parsed heartbeat response.
type heartbeatResponse struct {
	OK         bool              `json:"ok"`
	Directives map[string]any    `json:"directives,omitempty"`
	Nudges     []json.RawMessage `json:"nudges,omitempty"`
}

func heartbeatWithFallback(cmd *cobra.Command, port string, agentID, status string) (*heartbeatResponse, error) {
	body := map[string]any{
		"agent_id": agentID,
	}
	if status != "" {
		body["status"] = status
	}

	data, err := withAgentFallback(
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
	if err != nil {
		return nil, err
	}

	var resp heartbeatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return &heartbeatResponse{OK: true}, nil
	}
	return &resp, nil
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

			resp, err := heartbeatWithFallback(cmd, port, agentID, status)
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
				sessionParams := bridge.SessionStartParams{
					Namespace:   startNamespace,
					AgentID:     agentID,
					AgentType:   startAgentType,
					Description: startDescription,
					AutoRecall:  false,
				}

				_, ensureErr := startSessionWithFallback(cmd, port, sessionParams)
				if ensureErr == nil {
					if daemonEnsureErr := ensureDaemonSession(cmd, sessionParams); daemonEnsureErr != nil {
						err = fmt.Errorf("%v (daemon ensure-session failed: %w)", err, daemonEnsureErr)
					} else {
						err = nil
					}
				}
				if ensureErr == nil && err == nil {
					resp, err = heartbeatWithFallback(cmd, port, agentID, status)
				} else {
					if ensureErr != nil {
						err = fmt.Errorf("%v (ensure-session failed: %w)", err, ensureErr)
					}
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

			// Print nudges to stderr (visible even in quiet mode via 2> redirect).
			if resp != nil && len(resp.Nudges) > 0 {
				for _, n := range resp.Nudges {
					fmt.Fprintf(os.Stderr, "loom: nudge: %s\n", string(n))
				}
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

// newAgentKeepaliveCmd creates the `loom agent keepalive` command.
// It runs a background ticker loop that sends periodic heartbeats to keep
// agent presence alive even when no tool use is occurring.
func newAgentKeepaliveCmd() *cobra.Command {
	var (
		agentID   string
		agentType string
		interval  time.Duration
		quiet     bool
	)

	cmd := &cobra.Command{
		Use:   "keepalive",
		Short: "Background heartbeat daemon for agent presence",
		Long: `Run a ticker loop that sends periodic heartbeats to keep agent presence
alive. Designed to be spawned as a background process by session-start hooks
and killed by session-end hooks via the PID file.

Uses PID file deduplication: if a keepalive for the same agent-id is already
running, exits silently. On SIGINT/SIGTERM, sends a final deregister and exits.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if agentID == "" {
				return fmt.Errorf("--agent-id is required")
			}

			pidFile := keepalivePIDPath(agentID)

			// Dedup: if PID file exists and process is alive, exit silently.
			if existing, err := os.ReadFile(pidFile); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(existing))); err == nil {
					if proc, err := os.FindProcess(pid); err == nil {
						// Signal 0 checks if process is alive without sending a real signal.
						if proc.Signal(syscall.Signal(0)) == nil {
							if !quiet {
								fmt.Fprintf(os.Stderr, "keepalive already running (pid %d)\n", pid)
							}
							return nil
						}
					}
				}
			}

			// Write PID file.
			if err := os.MkdirAll(filepath.Dir(pidFile), 0755); err != nil {
				return fmt.Errorf("create pid dir: %w", err)
			}
			if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
				return fmt.Errorf("write pid file: %w", err)
			}
			defer os.Remove(pidFile)

			port := resolvePort(cmd)

			// Set up signal handling for clean shutdown.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			if !quiet {
				fmt.Fprintf(os.Stderr, "keepalive started for %s (interval=%s, pid=%d)\n", agentID, interval, os.Getpid())
			}

			for {
				select {
				case <-ticker.C:
					_, err := heartbeatWithFallback(cmd, port, agentID, "active")
					if err != nil && !quiet {
						fmt.Fprintf(os.Stderr, "keepalive: heartbeat: %v\n", err)
					}
				case <-sigCh:
					if !quiet {
						fmt.Fprintf(os.Stderr, "keepalive shutting down for %s\n", agentID)
					}
					// Best-effort deregister.
					deregBody := map[string]string{"agent_id": agentID}
					_, _ = hudPostFast(port, "/api/agent/deregister", deregBody, 3*time.Second)
					return nil
				}
			}
		},
	}

	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent identifier (required)")
	cmd.Flags().StringVar(&agentType, "agent-type", "", "Agent type for bootstrap")
	cmd.Flags().DurationVar(&interval, "interval", 20*time.Second, "Heartbeat interval")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output")

	return cmd
}

// keepalivePIDPath returns the PID file path for a keepalive daemon.
func keepalivePIDPath(agentID string) string {
	tmpDir := os.Getenv("TMPDIR")
	if tmpDir == "" {
		tmpDir = "/tmp"
	}
	return filepath.Join(tmpDir, "loom-keepalive-"+agentID+".pid")
}

func parseHeartbeatTimestamp(raw string) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return ts, true
	}
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return ts, true
	}
	return time.Time{}, false
}

func hookStateFromSignals(now time.Time, lastHeartbeat time.Time, hasHeartbeat bool, heartbeatsInWindow int) string {
	if hasHeartbeat {
		age := now.Sub(lastHeartbeat)
		switch {
		case age <= 30*time.Second:
			return "healthy"
		case age <= 5*time.Minute:
			return "stale"
		default:
			return "missing"
		}
	}
	if heartbeatsInWindow > 0 {
		return "stale"
	}
	return "missing"
}

func hookStatusWithHUD(cmd *cobra.Command, port, agentID string, window time.Duration, limit int) (json.RawMessage, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, fmt.Errorf("agent-id is required")
	}
	if window <= 0 {
		return nil, fmt.Errorf("window must be greater than zero")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be greater than zero")
	}

	now := time.Now().UTC()
	agentID = strings.TrimSpace(agentID)

	sessionRaw, err := activeSessionWithFallback(cmd, port, agentID)
	if err != nil {
		return nil, err
	}
	var sessionEnvelope struct {
		Session *bridge.SessionInfo `json:"session"`
	}
	_ = json.Unmarshal(sessionRaw, &sessionEnvelope)

	presenceRaw, err := hudGet(port, "/api/presence")
	if err != nil {
		return nil, err
	}
	var presenceEnvelope struct {
		Agents []bridge.PresenceInfo `json:"agents"`
	}
	if err := json.Unmarshal(presenceRaw, &presenceEnvelope); err != nil {
		return nil, fmt.Errorf("parse presence response: %w", err)
	}

	var presence *bridge.PresenceInfo
	for i := range presenceEnvelope.Agents {
		if presenceEnvelope.Agents[i].AgentID == agentID {
			presence = &presenceEnvelope.Agents[i]
			break
		}
	}

	query := url.Values{}
	query.Set("agent_id", agentID)
	query.Set("event_type", "agent.heartbeat")
	query.Set("since", now.Add(-window).Format(time.RFC3339))
	query.Set("limit", fmt.Sprintf("%d", limit))
	timelineRaw, err := hudGet(port, "/api/timeline?"+query.Encode())
	if err != nil {
		return nil, err
	}
	var timelineEnvelope struct {
		Entries []struct {
			Timestamp time.Time `json:"timestamp"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(timelineRaw, &timelineEnvelope); err != nil {
		return nil, fmt.Errorf("parse timeline response: %w", err)
	}

	heartbeatsInWindow := len(timelineEnvelope.Entries)
	var latestEventAt string
	if heartbeatsInWindow > 0 {
		latestEventAt = timelineEnvelope.Entries[0].Timestamp.UTC().Format(time.RFC3339)
	}

	var (
		lastHeartbeat    time.Time
		hasLastHeartbeat bool
		lastHeartbeatRaw string
		heartbeatAgeSec  int64
		presenceStatus   string
	)
	if presence != nil {
		lastHeartbeatRaw = strings.TrimSpace(presence.LastHeartbeat)
		presenceStatus = strings.TrimSpace(presence.Status)
		if ts, ok := parseHeartbeatTimestamp(lastHeartbeatRaw); ok {
			hasLastHeartbeat = true
			lastHeartbeat = ts.UTC()
			heartbeatAgeSec = int64(now.Sub(lastHeartbeat).Seconds())
		}
	}

	state := hookStateFromSignals(now, lastHeartbeat, hasLastHeartbeat, heartbeatsInWindow)

	result := map[string]any{
		"ok":                   true,
		"agent_id":             agentID,
		"hook_state":           state,
		"hooks_working":        state != "missing",
		"window_seconds":       int(window.Seconds()),
		"heartbeats_in_window": heartbeatsInWindow,
		"presence_registered":  presence != nil,
		"has_active_session":   sessionEnvelope.Session != nil,
		"checked_at":           now.Format(time.RFC3339),
	}
	if presenceStatus != "" {
		result["presence_status"] = presenceStatus
	}
	if lastHeartbeatRaw != "" {
		result["last_heartbeat"] = lastHeartbeatRaw
	}
	if hasLastHeartbeat {
		result["heartbeat_age_seconds"] = heartbeatAgeSec
	}
	if latestEventAt != "" {
		result["latest_heartbeat_event_at"] = latestEventAt
	}
	if sessionEnvelope.Session != nil {
		result["session_id"] = sessionEnvelope.Session.ID
		result["session_namespace"] = sessionEnvelope.Session.Namespace
		result["session_status"] = sessionEnvelope.Session.Status
	}

	return json.Marshal(result)
}

// newAgentHookStatusCmd creates the `loom agent hook-status` command.
func newAgentHookStatusCmd() *cobra.Command {
	var (
		agentID string
		window  time.Duration
		limit   int
		quiet   bool
	)

	cmd := &cobra.Command{
		Use:   "hook-status",
		Short: "Check if heartbeat hooks are firing for an agent",
		Long: `Summarize hook/control-loop health by combining active session state,
presence heartbeat recency, and recent agent.heartbeat timeline events.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)
			result, err := hookStatusWithHUD(cmd, port, agentID, window, limit)
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
	cmd.Flags().DurationVar(&window, "window", 5*time.Minute, "Observation window for heartbeat events")
	cmd.Flags().IntVar(&limit, "limit", 200, "Maximum timeline entries to inspect")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (for hooks)")

	return cmd
}
