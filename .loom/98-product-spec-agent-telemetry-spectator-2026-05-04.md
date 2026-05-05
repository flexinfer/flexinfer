# Product Spec: Agent Telemetry Event Bus + Live Spectator Mode

**Date:** 2026-05-04
**Branch (loom-core):** `plan/event-bus-spectator`
**Status:** draft for review
**Companion docs:**
- Research: `.loom/97-research-agent-telemetry-spectator-2026-05-04.md`
- Plan: `.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md`

## Summary

Extend the existing daemon EventBus with a small set of new event types that capture per-tool-call activity and session lifecycle transitions across all active agent sessions, gate the broadcast through a redaction layer with three privacy tiers, and surface the stream as a `LiveSessionsCard` in the HUD and an equivalent on iOS. Designed so the same event substrate unblocks a future per-session backchannel, an adversarial-pair workflow, and a skill effectiveness leaderboard without re-architecture.

## Goals

1. **Live observability** — sub-second visibility into "what is each agent doing right now," not 5–15s polled deltas.
2. **Privacy by default** — no raw tool args/results broadcast on shared surfaces unless explicitly opted in.
3. **Multi-platform** — Claude Code, Codex, Gemini sessions all visible to the same spectator surface, even where hook coverage is uneven.
4. **Foundation for follow-ons** — the same event substrate must support backchannel, adversarial-pair, and skill-leaderboard subscribers without schema breakage.

## Non-goals

- Replacing the existing polled monitor pattern (`StreamMonitor`, `FleetMonitor`, etc.) — both coexist.
- Persisting every event durably. Events are ephemeral; durable history lives in `pkg/agentcontext` (sessions, entries, tasks) and Mills `events` audit table.
- Multi-host fanout (NATS/Redis). Single-host daemon stays the broker for v1.
- Cross-user/cross-machine spectating. Localhost + the user's own iOS device only.

## Event schema additions

All new events extend `internal/daemon/events.go` `Event{ID, Type, Timestamp, Data}`. Naming follows the existing `process.start|stop|error` convention (bare noun + verb, no `-ed` suffix).

### `session.start`

```go
type SessionStartEvent struct {
    SessionID    string            `json:"session_id"`
    AgentID      string            `json:"agent_id"`
    AgentType    string            `json:"agent_type"` // "claude-code" | "codex" | "gemini" | "kilocode"
    Namespace    string            `json:"namespace"`
    WorkingDir   string            `json:"working_dir"`
    Branch       string            `json:"branch,omitempty"`
    WorktreeID   string            `json:"worktree_id,omitempty"`
    Description  string            `json:"description,omitempty"`
    StartedAt    time.Time         `json:"started_at"`
}
```

### `session.end`

```go
type SessionEndEvent struct {
    SessionID    string    `json:"session_id"`
    AgentID      string    `json:"agent_id"`
    EndedAt      time.Time `json:"ended_at"`
    Reason       string    `json:"reason"` // "user_exit" | "expired" | "summarized" | "error"
    EntryCount   int       `json:"entry_count"`
    TotalTokens  int64     `json:"total_tokens"`
    DurationMs   int64     `json:"duration_ms"`
}
```

### `agent.status.change`

```go
type AgentStatusChangeEvent struct {
    AgentID      string    `json:"agent_id"`
    SessionID    string    `json:"session_id,omitempty"`
    OldStatus    string    `json:"old_status"` // active | idle | offline | expired
    NewStatus    string    `json:"new_status"`
    ChangedAt    time.Time `json:"changed_at"`
    CurrentTask  string    `json:"current_task,omitempty"`
}
```

This is the event the existing `presence.onEvent` callback already wants to publish; we wire it.

### `tool.call.start`

```go
type ToolCallStartEvent struct {
    CallID       string         `json:"call_id"`        // unique per invocation
    SessionID    string         `json:"session_id"`
    AgentID      string         `json:"agent_id"`
    ToolName     string         `json:"tool_name"`      // "Bash" | "Read" | "agent_session_start" | etc.
    ServerName   string         `json:"server_name,omitempty"` // mcp server prefix when applicable
    ArgsRedacted map[string]any `json:"args_redacted"`  // post-redaction; see Redaction tiers below
    ArgsTier     string         `json:"args_tier"`      // "public" | "redacted" | "private"
    StartedAt    time.Time      `json:"started_at"`
}
```

### `tool.call.end`

```go
type ToolCallEndEvent struct {
    CallID         string    `json:"call_id"`             // matches start
    SessionID      string    `json:"session_id"`
    AgentID        string    `json:"agent_id"`
    ToolName       string    `json:"tool_name"`
    DurationMs     int64     `json:"duration_ms"`
    ExitCode       int       `json:"exit_code"`           // 0 = success
    ResultSize     int       `json:"result_size_bytes"`
    ResultSummary  string    `json:"result_summary,omitempty"` // 1-line redacted preview
    Error          string    `json:"error,omitempty"`
    EndedAt        time.Time `json:"ended_at"`
}
```

The existing `tool.call` event type is repurposed as the **per-call summary** for legacy subscribers; new code subscribes to the start/end pair.

### Backwards compat
- Keep existing `agent.spawn.telemetry.delta` unchanged; it is the post-hoc per-spawn batch summary and stays useful for spawned subagent tracking.
- Existing `process.*` and `workflow.step` events are untouched.

## Redaction model

Three tiers, applied at the publish site (not the subscriber):

| Tier | Args | Result | Default for |
|---|---|---|---|
| `public` | dropped | dropped (size + summary only) | spectator broadcast on shared HUD; mobile companion |
| `redacted` | secret patterns masked (e.g., `***`) + path-only for file refs | first 200 chars, secret-masked | future authenticated single-user surfaces |
| `private` | full | full | persistence in `pkg/agentcontext` (already happens today; no change) |

### Redaction primitive

New package: `pkg/telemetry/redact/`.

```go
package redact

type Tier string

const (
    TierPublic   Tier = "public"
    TierRedacted Tier = "redacted"
    TierPrivate  Tier = "private"
)

// Redact returns args with secret patterns masked and large values truncated
// according to the requested tier. Idempotent. Pure function.
func Redact(args map[string]any, tier Tier) map[string]any

// Summary returns a one-line, secret-safe preview of a tool result for the
// requested tier. Returns "" for TierPublic on tool kinds that may leak.
func Summary(toolName string, result any, tier Tier) string
```

Built-in patterns (regex catalog in `pkg/telemetry/redact/patterns.go`):
- AWS keys (`AKIA[0-9A-Z]{16}`)
- GitLab/GitHub tokens (`gl[a-z]+-[0-9a-zA-Z_-]{20,}`, `ghp_[0-9a-zA-Z]{36}`)
- Bearer/Basic auth headers
- JWT-shaped strings (3 base64 segments)
- `password=`, `secret=`, `api_key=` form values
- Connection strings (`postgres://...:...@`, etc.)
- Anything matching the user's `~/.config/loom/redact-patterns.yaml` (user-extensible)

Per-tool overrides in `pkg/telemetry/redact/policy.go`:
```yaml
# pkg/telemetry/redact/policy.yaml (embedded default)
tools:
  Read:
    public:  { args: { file_path: PATH_ONLY }, result: SIZE_ONLY }
    redacted: { args: { file_path: PATH_ONLY }, result: FIRST_200_CHARS_MASKED }
  Bash:
    public:  { args: { command: FIRST_60_CHARS_MASKED }, result: SIZE_ONLY }
  Write:
    public:  { args: { file_path: PATH_ONLY, content: SIZE_ONLY }, result: SIZE_ONLY }
  agent_context_add:
    public:  { args: DROP, result: SIZE_ONLY }      # context entries are sensitive
```

If a tool is not in the policy, default to `DROP` for `public`, mask-everything for `redacted`.

## Hook wiring per platform

| Platform | Hook events captured | Mechanism | Coverage |
|---|---|---|---|
| **Claude Code** | `SessionStart`, `SessionEnd`, `PreToolUse`, `PostToolUse` | `.claude/settings.json` → `loom agent event-emit` subprocess | full |
| **Gemini** | `SessionStart`, `SessionEnd`, `BeforeTool`, `AfterTool` | `.gemini/settings.json` → same CLI | full |
| **Codex** | best-effort via `notify` hook only | `.codex/config.toml` → CLI parses notification payload, emits coarse `tool.call.end` | partial — no `start` event |
| **Kilocode** | none (no hook surface) | n/a | invisible to spectator until Kilo exposes hooks |

A new `loom agent event-emit` CLI subcommand (in `cmd/loom/cmd_agent_event_emit.go`) reads hook stdin JSON, normalizes, applies redaction at `TierPublic`, and publishes to the daemon EventBus via the existing socket. Hook payload schema differs per platform; CLI normalizes to the schemas above.

Per-platform hook configs are auto-generated by `loom sync <platform> --regen` — extend the existing generator, not hand-edit.

## HUD `LiveSessionsCard` UX

Layout sketch (Svelte component in `internal/hud/web/src/lib/components/LiveSessionsCard.svelte`):

```
┌─ Live Sessions (4 active) ────────────── [autoplay] [pause] ─┐
│                                                                │
│  claude-code  ●  feat/event-bus-spectator        2m12s active │
│  └─ Read   pkg/telemetry/redact/policy.go              42ms ✓ │
│  └─ Edit   pkg/telemetry/redact/redact.go             198ms ✓ │
│  └─ Bash   go test ./pkg/telemetry/redact/...      1.2s ✓     │
│  └─ ◌ Write …                                       running   │
│                                                                │
│  codex      ●  refactor/auth-cleanup            14m08s active │
│  └─ Read   internal/auth/middleware.go (×3)            ─      │
│                                                                │
│  gemini     ◐  docs/agent-context-update         32m idle     │
│  claude #2  ◑  spawned by claude-code (subagent)              │
│                                                                │
│  ──────────────────────────────────────────────────────────── │
│  Last 5 minutes: 142 tool calls · 3 errors · ~8.4k tokens     │
└────────────────────────────────────────────────────────────────┘
```

Key UX rules:
- **One row per active session**, sorted by recent activity.
- **Last 4 tool calls per session** shown inline; older fade out.
- **Per-call timing chip** (✓ success, ⚠ slow >1s, ✗ failed).
- **Click a row** → expand to a wide pane showing the full last 50 calls with `redacted`-tier args (auth-gated).
- **Status dot:** ● active, ◐ idle, ◑ subagent, ○ offline (greyed).
- **Pause** stops auto-scroll; **autoplay** is default.
- **No raw secrets ever rendered**, even in expanded view — that view is `redacted` tier.
- Footer rolls a 5-min activity summary (event count, error count, token usage).

Subscription mechanism: reuses `bridge/events.go` `On("tool.call.start", handler)` etc. Card state is a Svelte store keyed by `agent_id` with a bounded ring buffer per session.

## iOS spectator surface

Smaller-screen variant: condensed list of active sessions (one row each, no per-call inline ticker; expand row → modal with last 20 calls). Wired through the existing `SSEEventBroadcaster` in `apps/loom-companion-ios/Sources/LoomCompanionKit/Networking/SSEClient.swift` — register a new handler for the new event types, render in a SwiftUI `LiveSessionsView` parallel to the existing `MillsScreen.swift`.

Mobile fetches `TierPublic` only. Expanding a session row does **not** unlock `redacted` tier on mobile in v1 (cert/auth complexity); explicitly noted as a Phase 2 follow-up.

## Public API surface

### New MCP tools (in `pkg/agentcontext`)

```
agent_event_emit(event_type, payload)         # called by CLI hooks
agent_event_subscribe(filters: {agent_id?, namespace?, tool_name?, tier?})
                                              # streaming; returns event stream
```

Both registered in `mcp/context/registry.yaml` under the `agent-context` server.

### New HTTP endpoints

- `GET /api/events?type=tool.call.*&agent_id=X` — already-supported HUD SSE with new event type filters (no new endpoint, just new event types flowing through).
- `GET /api/mobile/v1/events/stream?tier=public` — explicit tier param; iOS pins to `public`.

### New CLI commands

- `loom agent event-emit --hook <hook-name>` — reads hook stdin, normalizes, publishes (called by `.claude/settings.json` etc. — not for direct user use).
- `loom agent spectate --filter "agent_id=claude-code"` — terminal spectator (`textual`-style live pane) for headless use.

## Privacy + auth contract

| Surface | Tier broadcast | Auth |
|---|---|---|
| Daemon `/events` (Unix socket / localhost) | `private` (in-process trust) | localhost-only |
| HUD `/api/events` (browser) | `public` for spectator card; `redacted` available via expanded auth-gated view | session cookie |
| iOS `/api/mobile/v1/events/stream` | `public` only in v1 | `HUD_ADMIN_TOKEN` (already wired per recent fix) |
| `loom agent spectate` (CLI) | `redacted` (running as user) | filesystem socket perm |

Default for any new subscriber type is `public`. Operators must explicitly opt-in to `redacted` per-surface.

## Backpressure + observability of the bus itself

- `internal/daemon/events.go` already counts `DroppedCount` per subscriber.
- Add a `bus.backpressure` event type that fires when any subscriber drops >10 events in a 60s window — surfaces in the HUD as a yellow banner, "spectator stream may be incomplete."
- Raise per-subscriber buffer from 256 → 1024 for spectator subscribers (configurable per-event-type registration).
- Prometheus metrics: `loom_event_bus_published_total{type}`, `loom_event_bus_dropped_total{type,subscriber}`, `loom_event_bus_subscribers{type}`.

## MVP scope vs phase 2

| Slice | MVP (v1) | Phase 2 |
|---|---|---|
| Session lifecycle events | ✅ start/end/status.change | parent/root session relationship change events |
| Per-tool-call events | ✅ start/end with redaction | streaming partial output for long tool calls |
| Redaction primitive | ✅ pkg + 6 default patterns + per-tool policy | user-extensible patterns from `~/.config/loom/redact-patterns.yaml` |
| Hook wiring | ✅ Claude Code + Gemini full; Codex coarse | Codex `start` event when OpenAI ships richer hooks |
| HUD `LiveSessionsCard` | ✅ collapsed view + click-to-expand | filtering, search, multi-pane layout |
| iOS spectator | ✅ collapsed list + modal expand | watch complication; push notification on `error` events |
| `loom agent spectate` CLI | ✅ basic terminal pane | recording → replay |
| Backpressure handling | ✅ metrics + warning banner | per-subscriber rate limiting |

## Acceptance criteria for v1

- Across two terminal sessions (one Claude Code, one Codex), the HUD `LiveSessionsCard` shows both, with the Claude session's per-call inline ticker and the Codex session's coarse status updates.
- Stopping the Claude Code session emits a `session.end` within 3s.
- Triggering a tool call with a known secret in args (e.g., `Bash` with an `AKIA…` key) results in the secret being masked in `args_redacted` for `tool.call.start` event captured at `/api/events`.
- iOS companion shows the same two sessions in `LiveSessionsView` within 5s of a tool call event.
- Backpressure: under a synthetic 200 events/sec test, no spectator subscriber misses more than 1% of events; if it does, the `bus.backpressure` warning fires.
- `loom agent spectate` CLI displays live tool calls with `redacted` tier args in a terminal pane.

## Risks called out for review

1. **Cross-platform parity is uneven and cannot fully fix in this spec.** Codex sessions will be coarse-grained until OpenAI ships richer hooks. Spec should not promise feature parity.
2. **Redaction is best-effort.** A `Bash` command with a novel secret format will leak unless the user adds a pattern. We default-drop `Bash` args to first-60-chars-masked to mitigate, but this is imperfect.
3. **Performance under load is unproven.** The 256-slot buffer is plausible but untested at the 200 events/sec scale. The MVP includes a load test.
4. **Subagent attribution.** Spawned subagents (Claude Code's `Agent` tool calls) currently share the parent's `agent_id`. The spec uses the existing `parent_session_id` field but the HUD UX needs to render the hierarchy clearly — easy to get wrong.
5. **Hook installation drift.** If `loom sync` is not run after a registry change, hook payloads may differ from what `loom agent event-emit` expects. Spec mandates a `loom sync claude --check` gate in CI to catch drift.

## Sources

- `.loom/97-research-agent-telemetry-spectator-2026-05-04.md` — substrate inventory + gaps
- `internal/daemon/events.go` — existing EventBus contract
- `pkg/agentcontext/svc_presence.go:25-48,379-380` — `onEvent` callback declared + wired but no broadcast on transitions today
- `internal/hud/bridge/spawn_telemetry.go` — existing per-call accumulator (template for new schema)
- `apps/loom-companion-ios/Sources/LoomCompanionKit/Networking/SSEClient.swift` — iOS subscriber pattern
- `CLAUDE.md` lifecycle hooks table — multi-platform hook coverage
- `mcp/context/registry.yaml` — sync surface for hook config generation
