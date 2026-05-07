package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/pkg/eventpub"
	"github.com/crb2nu/loom/pkg/telemetry/redact"
)

// Canonical CLI hook names. These are the platform-agnostic names the
// generator wires into platform-native hook configs (Claude Code's
// PreToolUse, Gemini's BeforeTool, Codex's notify, etc.). Keeping them
// stable here means hook scripts only need updating if a hook category is
// added, not if a platform renames its trigger.
const (
	hookSessionStart = "session-start"
	hookSessionEnd   = "session-end"
	hookPreToolUse   = "pre-tool-use"
	hookPostToolUse  = "post-tool-use"
)

// Source platforms recognized by --platform. claude-code is the dogfood
// target; gemini-cli and codex were added in Phase 2.2b/c. generic assumes
// stdin is already a canonical {type, payload} envelope and bypasses
// platform-specific normalization.
const (
	platformClaudeCode = "claude-code"
	platformGemini     = "gemini-cli"
	platformCodex      = "codex"
	platformGeneric    = "generic"
)

// Canonical event type strings. Mirror internal/daemon.EventType constants
// so events emitted from hooks are indistinguishable from ones the daemon
// publishes itself.
const (
	eventSessionStart      = "session.start"
	eventSessionEnd        = "session.end"
	eventAgentStatusChange = "agent.status.change"
	eventToolCallStart     = "tool.call.start"
	eventToolCallEnd       = "tool.call.end"
)

// emittedEvent is the {type, payload} envelope produced by hook
// normalization. Sent verbatim to pkg/eventpub.HTTPPublisher.
type emittedEvent struct {
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

// hookToEvent normalizes a CLI hook payload to a canonical event for the
// requested platform. agentID overrides any payload-supplied agent_id when
// non-empty (Claude Code's hook contract does not carry it; the hook script
// passes it via --agent-id).
func hookToEvent(hook, platform, agentID string, raw map[string]any) (emittedEvent, error) {
	switch platform {
	case "", platformClaudeCode:
		return nativeHookToEvent(hook, agentID, raw)
	case platformGemini:
		// Gemini's hook stdin schema converged on Claude's keys (tool_name,
		// tool_input, session_id, tool_response). Reuse the native normalizer.
		return nativeHookToEvent(hook, agentID, raw)
	case platformCodex:
		return codexHookToEvent(hook, agentID, raw)
	case platformGeneric:
		return genericHookToEvent(hook, agentID, raw)
	default:
		return emittedEvent{}, fmt.Errorf("unsupported platform %q (supported: %s, %s, %s, %s)", platform, platformClaudeCode, platformGemini, platformCodex, platformGeneric)
	}
}

// nativeHookToEvent maps a Claude-style hook stdin schema (tool_name +
// tool_input + session_id, etc.) to the canonical event payload. Args go
// through redact.Redact at TierPublic so secrets in tool inputs never reach
// the event bus. Used for both claude-code (originator) and gemini-cli
// (whose hook schema converged on the same shape).
func nativeHookToEvent(hook, agentID string, raw map[string]any) (emittedEvent, error) {
	sessionID := stringField(raw, "session_id")
	if agentID == "" {
		agentID = stringField(raw, "agent_id")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	switch hook {
	case hookSessionStart:
		return emittedEvent{
			Type: eventSessionStart,
			Payload: map[string]any{
				"session_id": sessionID,
				"agent_id":   agentID,
				"started_at": now,
			},
		}, nil

	case hookSessionEnd:
		return emittedEvent{
			Type: eventSessionEnd,
			Payload: map[string]any{
				"session_id": sessionID,
				"agent_id":   agentID,
				"ended_at":   now,
			},
		}, nil

	case hookPreToolUse:
		toolName := stringField(raw, "tool_name")
		if toolName == "" {
			return emittedEvent{}, errors.New("pre-tool-use: tool_name is required")
		}
		toolInput, _ := raw["tool_input"].(map[string]any)
		callID := stringField(raw, "call_id")
		if callID == "" {
			callID = generateEventEmitCallID()
		}
		return emittedEvent{
			Type: eventToolCallStart,
			Payload: map[string]any{
				"call_id":       callID,
				"session_id":    sessionID,
				"agent_id":      agentID,
				"tool_name":     toolName,
				"args_redacted": redact.Redact(toolName, toolInput, redact.TierPublic),
				"args_tier":     string(redact.TierPublic),
				"started_at":    now,
			},
		}, nil

	case hookPostToolUse:
		toolName := stringField(raw, "tool_name")
		if toolName == "" {
			return emittedEvent{}, errors.New("post-tool-use: tool_name is required")
		}
		callID := stringField(raw, "call_id")
		payload := map[string]any{
			"call_id":     callID,
			"session_id":  sessionID,
			"agent_id":    agentID,
			"tool_name":   toolName,
			"exit_code":   intField(raw, "exit_code"),
			"duration_ms": intField(raw, "duration_ms"),
			"ended_at":    now,
		}
		if errMsg := stringField(raw, "error"); errMsg != "" {
			payload["error"] = errMsg
		}
		// Claude Code uses tool_response; older payloads use result.
		if v, ok := raw["tool_response"]; ok && v != nil {
			if s := redact.Summary(toolName, v, redact.TierPublic); s != "" {
				payload["result_summary"] = s
			}
		} else if v, ok := raw["result"]; ok && v != nil {
			if s := redact.Summary(toolName, v, redact.TierPublic); s != "" {
				payload["result_summary"] = s
			}
		}
		return emittedEvent{Type: eventToolCallEnd, Payload: payload}, nil

	default:
		return emittedEvent{}, fmt.Errorf("unknown hook %q (expected: %s, %s, %s, %s)",
			hook, hookSessionStart, hookSessionEnd, hookPreToolUse, hookPostToolUse)
	}
}

// codexHookToEvent maps the Codex `notify` payload (the only hook surface
// Codex exposes) to a canonical event. Codex notify fires once per agent
// turn-end with a flat payload — no `tool_name`/`tool_input` granularity,
// no separate start/end, no per-call IDs. Best we can do is emit a coarse
// `tool.call.end` with `tool_name="codex.turn"` per the spectator plan
// (`.loom/98-…2026-05-04.md` — "best-effort via notify hook only").
//
// hook is expected to be hookPostToolUse (the only canonical hook codex
// supports); other hooks are explicitly rejected so callers don't silently
// publish wrong types.
func codexHookToEvent(hook, agentID string, raw map[string]any) (emittedEvent, error) {
	if hook != hookPostToolUse {
		return emittedEvent{}, fmt.Errorf("codex platform only supports --hook=%s (got %q); codex notify has no start/session-lifecycle granularity", hookPostToolUse, hook)
	}
	sessionID := stringField(raw, "session_id")
	if agentID == "" {
		agentID = stringField(raw, "agent_id")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Codex notify carries minimal turn metadata. Pull what's commonly there
	// (status, model, usage) and surface it as a coarse end event. Anything
	// missing is omitted rather than zero-filled so consumers can distinguish
	// "absent" from "zero".
	payload := map[string]any{
		"call_id":    generateEventEmitCallID(),
		"session_id": sessionID,
		"agent_id":   agentID,
		"tool_name":  "codex.turn",
		"ended_at":   now,
	}
	if v := intField(raw, "duration_ms"); v != 0 {
		payload["duration_ms"] = v
	}
	if v := intField(raw, "exit_code"); v != 0 {
		payload["exit_code"] = v
	}
	if status := stringField(raw, "status"); status != "" {
		// Codex statuses ("completed", "failed", …) — emit verbatim; consumers
		// can branch without us guessing exit codes.
		payload["status"] = status
	}
	if errMsg := stringField(raw, "error"); errMsg != "" {
		payload["error"] = errMsg
	}
	return emittedEvent{Type: eventToolCallEnd, Payload: payload}, nil
}

// genericHookToEvent passes through a pre-normalized {type, payload}
// envelope. Used by automation that already knows the canonical schema (e.g.
// custom orchestrators) so we don't need a per-tool platform.
func genericHookToEvent(hook, agentID string, raw map[string]any) (emittedEvent, error) {
	t := stringField(raw, "type")
	if t == "" {
		return emittedEvent{}, errors.New("generic platform requires \"type\" field in stdin")
	}
	if !isAllowedEmittedType(t) {
		return emittedEvent{}, fmt.Errorf("event type %q not permitted via event-emit", t)
	}
	if hook != "" {
		if expected := hookForType(t); expected != "" && expected != hook {
			return emittedEvent{}, fmt.Errorf("--hook=%s does not match payload type %q (expected hook=%s)", hook, t, expected)
		}
	}
	payload, _ := raw["payload"].(map[string]any)
	if payload == nil {
		payload = map[string]any{}
	}
	if agentID != "" {
		if _, present := payload["agent_id"]; !present {
			payload["agent_id"] = agentID
		}
	}
	return emittedEvent{Type: t, Payload: payload}, nil
}

// isAllowedEmittedType is the CLI-side allowlist. Defense-in-depth: a typo'd
// hook script can't accidentally publish (say) a fake server.health event.
func isAllowedEmittedType(t string) bool {
	switch t {
	case eventSessionStart, eventSessionEnd, eventAgentStatusChange, eventToolCallStart, eventToolCallEnd:
		return true
	}
	return false
}

// hookForType maps an event type back to its canonical hook name. Used to
// validate generic-platform inputs.
func hookForType(t string) string {
	switch t {
	case eventSessionStart:
		return hookSessionStart
	case eventSessionEnd:
		return hookSessionEnd
	case eventToolCallStart:
		return hookPreToolUse
	case eventToolCallEnd:
		return hookPostToolUse
	}
	return ""
}

func generateEventEmitCallID() string {
	return fmt.Sprintf("call-%d", time.Now().UnixNano())
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func intField(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
	}
	return 0
}

// resolveDaemonHTTPURL prefers --daemon-url, then $LOOM_DAEMON_HTTP_URL, then
// the same loopback default loomd uses (defaultMetricsAddr in cmd/loomd).
func resolveDaemonHTTPURL(flagURL string) string {
	if u := strings.TrimSpace(flagURL); u != "" {
		return u
	}
	if u := strings.TrimSpace(os.Getenv("LOOM_DAEMON_HTTP_URL")); u != "" {
		return u
	}
	return "http://127.0.0.1:9876"
}

func resolveAdminToken() string {
	if t := strings.TrimSpace(os.Getenv("LOOM_ADMIN_TOKEN")); t != "" {
		return t
	}
	return strings.TrimSpace(os.Getenv("LOOM_HUD_ADMIN_TOKEN"))
}

// newAgentEventEmitCmd creates the `loom agent event-emit` command.
func newAgentEventEmitCmd() *cobra.Command {
	var (
		hook      string
		platform  string
		agentID   string
		daemonURL string
		quiet     bool
	)

	cmd := &cobra.Command{
		Use:   "event-emit",
		Short: "Emit a canonical telemetry event from a hook payload",
		Long: `Read a CLI hook payload from stdin and publish the corresponding canonical
event (session.start/end, tool.call.start/end, agent.status.change) to the
loom daemon's event bus.

Designed for agent CLI hooks (Claude Code SessionStart/SessionEnd/PreToolUse/
PostToolUse, Gemini, Codex). Tool args are redacted at TierPublic before
publishing so secrets never reach the bus.

Configure the target via --daemon-url or $LOOM_DAEMON_HTTP_URL; defaults to
http://127.0.0.1:9876 (the loomd metrics/events port).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fail := func(err error) error {
				if quiet {
					return nil
				}
				return err
			}

			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fail(fmt.Errorf("read stdin: %w", err))
			}
			if len(data) == 0 {
				return fail(errors.New("empty stdin"))
			}

			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err != nil {
				return fail(fmt.Errorf("parse hook input: %w", err))
			}

			ev, err := hookToEvent(strings.TrimSpace(hook), strings.TrimSpace(platform), strings.TrimSpace(agentID), raw)
			if err != nil {
				return fail(err)
			}
			if !isAllowedEmittedType(ev.Type) {
				return fail(fmt.Errorf("normalized event type %q not allowed", ev.Type))
			}

			pub := eventpub.NewHTTPPublisher(
				resolveDaemonHTTPURL(daemonURL),
				resolveAdminToken(),
				slog.New(slog.DiscardHandler),
			)
			pub.Publish(ev.Type, ev.Payload)

			if !quiet {
				out, _ := json.Marshal(map[string]any{
					"emitted":  true,
					"type":     ev.Type,
					"hook":     hook,
					"platform": platform,
				})
				fmt.Println(string(out))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&hook, "hook", "", "Hook name: session-start, session-end, pre-tool-use, post-tool-use")
	cmd.Flags().StringVar(&platform, "platform", platformClaudeCode, "Source platform: claude-code, gemini-cli, codex, generic")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent identifier (overrides payload agent_id)")
	cmd.Flags().StringVar(&daemonURL, "daemon-url", "", "Daemon HTTP URL (default: $LOOM_DAEMON_HTTP_URL or http://127.0.0.1:9876)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output and errors (recommended for hook context)")

	return cmd
}
