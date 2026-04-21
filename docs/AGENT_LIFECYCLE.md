# Agent Lifecycle

This document describes the canonical model loom-core uses to represent
vendor CLI processes (Claude Code, Codex, Gemini CLI) as agents + sessions,
how the lifecycle is wired for each vendor, and how gaps in vendor hook
surfaces are compensated for.

## The Model

Three concepts, owned by three different subsystems:

| Concept        | Owned by                                   | Lifetime                                                         | Source of truth                        |
| -------------- | ------------------------------------------ | ---------------------------------------------------------------- | -------------------------------------- |
| **Presence**   | `pkg/agentcontext` / `svc_presence.go`     | Ephemeral; TTL expires after missed heartbeats (3× TTL default)  | In-memory registry + Qdrant projection |
| **Session**    | `pkg/agentcontext` / `svc_sessions_*.go`   | Durable; explicit `active → ended → summarized` state transitions | Qdrant `CollSessions`                  |
| **Agent view** | `internal/hud/fleetview` / `Join`          | Derived per HUD refresh from live presence + session records     | Computed, never stored                 |

Presence is the liveness beacon. Session is the durable context. An "agent"
in the HUD is the derived join of the two, and `HasSession` is always
computed at read time, never persisted — see the design notes on
[MR !211](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/211).

### Orphan state

A presence row with `HasPresence=true && HasSession=false` past a 120s grace
window is flagged `IsOrphan=true` by `fleetview.Join`. The fleet monitor
auto-deregisters orphans past 10 minutes. See
[MR !212](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/212).

## Per-Vendor Lifecycle

### Claude Code

Full lifecycle via [native hook events](https://code.claude.com/docs/en/hooks):

| Hook event      | Generated command (summary)                                   | Purpose                                    |
| --------------- | ------------------------------------------------------------- | ------------------------------------------ |
| `SessionStart`  | `loom agent session-start --auto-recall` + keepalive daemon   | Register presence + start session          |
| `PostToolUse`   | `loom agent heartbeat --ensure-session`                       | Keep presence alive, bootstrap session    |
| `Stop`          | `loom agent session-end --summarize --summary-async`          | End session, deregister presence          |
| `SubagentStart` | Cache parent-session-id for subagent grouping                 | Thread hierarchy                           |

Wired in [`pkg/generator/configs_hooks.go`](../pkg/generator/configs_hooks.go)
via the platform profile `claude` — emits to `~/.claude/settings.json`.

### Gemini CLI

Full lifecycle via [Gemini hook events](https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md).
Event names differ from Claude's but the loom commands are identical:

| Hook event     | Generated command (summary)                          | Purpose                                    |
| -------------- | ---------------------------------------------------- | ------------------------------------------ |
| `SessionStart` | `loom agent session-start --auto-recall` + keepalive | Register presence + start session          |
| `AfterTool`    | `loom agent heartbeat --ensure-session`              | Keep presence alive, bootstrap session    |
| `SessionEnd`   | `loom agent session-end --summarize --summary-async` | End session, deregister presence          |

Gemini has no `SubagentStart` equivalent — closest are `BeforeAgent` /
`AfterAgent` (per-turn, not per-subagent), so subagent hierarchy is
claude-only for now. Emits to `~/.gemini/settings.json`.

### Codex CLI

**No native lifecycle hook surface** beyond `notify`, which fires on turn
completion only — not on process start, not on process exit, not on tool
use. See [Codex config reference](https://developers.openai.com/codex/config-reference).

Loom wires Codex via a single `notify = [...]` entry in `~/.codex/config.toml`
that invokes a background `loom agent keepalive-wrap` process with
`--ensure-session`, rate-limited to one launch per 15s. The keepalive
daemon runs detached (via `nohup`) with a 12h max-lifetime and sends
periodic heartbeats; when it exits (timeout, SIGTERM), it deregisters the
presence.

| Event            | Generated behavior                                                                                              | Gap vs Claude/Gemini             |
| ---------------- | --------------------------------------------------------------------------------------------------------------- | -------------------------------- |
| Process start    | Covered by `notify` on first turn (first meaningful signal we can hook) — slight delay vs `SessionStart` | ~1 turn late                     |
| Tool use         | Not hooked — `notify` only fires at turn boundary                                                               | No per-tool heartbeat            |
| Process exit     | **Not hooked** — `notify` does not fire on exit                                                                 | **Session-end never called**     |

The "session-end never called" gap is why the fleet monitor's orphan
reaper exists: Codex agents that terminate without a clean shutdown are
detected via `HasPresence && !HasSession` past 120s, and their presence
rows are force-deregistered after 10 minutes. See
[`internal/hud/monitor/fleet.go`](../internal/hud/monitor/fleet.go) —
`reapOrphans`.

#### Future: `codex_hooks` feature flag

OpenAI's Codex has an experimental `codex_hooks` feature (gated behind
`features.codex_hooks` in the config) that adds `session_start`, `stop`,
`user_prompt_submit`, `pre_tool_use`, `post_tool_use`, and
`permission_request` events. Events exist in
[codex-rs/hooks/src/events/](https://github.com/openai/codex/tree/main/codex-rs/hooks/src/events)
but are not yet exposed via the public enum. When this goes GA, Codex
should join the "native hooks" tier and the `notify` shim can be deprecated.

## Cross-Vendor Contract

The `TestVendorLifecycleContract` test in
[`pkg/generator/configs_hooks_test.go`](../pkg/generator/configs_hooks_test.go)
pins the contract for each vendor so a future generator refactor cannot
silently drop a hook. The test is the executable version of this document.

## Observability

- **Presence count by status** — exposed on `FleetSnapshot.{Active,Idle,Offline,Orphan}Agents`.
- **Orphan attention** — `internal/hud/coordination.Build` attaches
  `"orphan without session"` to the agent's `AttentionReasons`, so the
  HUD attention list surfaces orphans automatically.
- **Reap audit trail** — `fleet.go:reapOrphans` logs
  `"fleet: reaped orphan presence"` with `agent_id` and
  `reap_after_seconds` on every successful deregister.
