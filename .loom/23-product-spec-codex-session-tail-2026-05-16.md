# Product Spec — Codex Desktop session tail (Slice 1a)

- **Date**: 2026-05-16
- **Author**: claude-code (funny-noyce worktree)
- **Brainstorm**: [.loom/brainstorm-cross-agent-gui-integration-2026-05-16.md](brainstorm-cross-agent-gui-integration-2026-05-16.md)
- **Status**: spec
- **Owning agent**: codex Desktop sessions become visible in loom HUD as first-class agent sessions

## Goal

When a user runs Codex Desktop, the loom HUD shows their active Codex threads identically to how it shows Claude Code sessions today: present in the fleet view, with task/tool activity, file touches, and session start/end visible to other agents via `agent_context`. Read-only. No writes back into Codex.

## Non-goals (this slice)

- Approval/handoff UI inside Codex (that's Slice 2)
- Custom rendering inside Codex Desktop chat (Codex Desktop doesn't render custom widgets; Slice 1b targets Claude + ChatGPT mobile)
- Spawning headless Codex sessions
- Acting on Codex events (we observe; we don't intervene)
- Bridging the Codex App Server JSON-RPC stream directly (the GUI app's app-server is stdio-locked to Electron at `--listen stdio://` — see "Why not app-server" below)

## What ground truth looks like today

- **Process inspection** confirmed: Codex.app spawns `/Applications/Codex.app/Contents/Resources/codex app-server --listen stdio://` (PID 43517 in observation). The stdio is owned by Electron; no external socket.
- **Persistent state**: every thread writes JSONL to `~/.codex/sessions/YYYY/MM/DD/rollout-<ISO>-<uuid>.jsonl`. One file per thread, append-only during the thread's life.
- **Record envelope** (verified from a live file):
  ```json
  {"timestamp": "2026-03-03T13:43:13.411Z",
   "type": "session_meta",
   "payload": {"id": "019cb3ef-…", "cwd": "/abs/path",
               "originator": "Codex Desktop", "source": "vscode|cli|appServer|…",
               "cli_version": "0.107.0-alpha.5", "model_provider": "openai", …}}
  {"timestamp": "...", "type": "response_item",
   "payload": {"type": "message"|"reasoning"|"function_call"|"function_call_output"|…,
               "role": "user|assistant|developer|tool",
               "content": [...], "id": "msg_…"}}
  ```
- **Loom side already in place** (no new pipeline needed):
  - `cmd/loom/cmd_agent_event_emit.go` defines canonical `{type, payload}` event envelopes
  - `internal/daemon/events.go`: `EventSessionStart`, `EventSessionEnd`, `EventToolCallStart`, `EventToolCallEnd`, `EventAgentStatusChange` constants
  - `pkg/eventpub.HTTPPublisher` posts envelopes to HUD (with admin-token bearer support)
  - `agent_type=codex` is already a known string in `cmd/mcp-agent-context/tools_presence.go` (loom proxy already emits `loom agent keepalive --agent-type codex` for CLI sessions — verified live: PID 53915)
  - `loom proxy --agent-hint codex` is the existing MCP-tool surface; this slice complements it by capturing the *internal* events the proxy never sees (turn boundaries, command execution, native file edits, plan updates)

## Architecture

```
~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl
        │
        ▼
internal/hud/codexwatch (new)
  ├─ Discoverer  : scan + fsnotify on the sessions root
  ├─ Tailer      : per-file goroutine, polls offset → emits parsed records
  ├─ Mapper      : codex record → canonical loom event envelope
  └─ Publisher   : pkg/eventpub.HTTPPublisher (existing)
        │
        ▼ HTTP POST /events
loom HUD daemon (existing pipeline)
        │
        ▼
agent_context (presence, sessions, file claims, fleet view)
```

## Public surface

### New long-running command
```
loom codex-watch [--sessions-dir ~/.codex/sessions] [--hud-url …] [--from now|all]
```
- `--from now` (default): only tail records appended after start; existing files skipped to head
- `--from all`: replay every file from disk (for backfill / debugging only)
- Idempotent: dedupes by `(session_id, record_index)` so re-running on the same files does not double-emit

### Daemon integration
- Add a `codex_watch` block to daemon config (`enabled: bool`, `sessions_dir: path`)
- Daemon launches the watcher as an internal goroutine when enabled, sharing the existing publisher
- Default `enabled: false` until validated; user enables explicitly in `~/.loom/config.yaml`

## Event mapping (Codex → canonical)

| Codex record | Trigger | Canonical event | Payload |
|---|---|---|---|
| `session_meta` (first record of a file) | new file detected with `originator: "Codex Desktop"` | `session.start` | `session_id`=payload.id, `agent_id`="codex-desktop-"+id_short, `agent_type`="codex", `cwd`, `model_provider`, `started_at`, `source` |
| `response_item` type=`function_call` | each tool invocation | `tool.call.start` | `session_id`, `agent_id`, `tool_name`=payload.name, `call_id`=payload.call_id, `tool_input` (redacted via `pkg/telemetry/redact` TierPublic), `started_at` |
| `response_item` type=`function_call_output` matching prior call_id | tool returns | `tool.call.end` | `session_id`, `agent_id`, `tool_name`, `call_id`, `status`=success/failure, `ended_at` |
| `response_item` type=`message` role=`assistant` | first assistant message after gap | `agent.status.change` | `session_id`, `agent_id`, `status`="active" |
| file mtime stable >30 min AND last record is terminal | timer | `session.end` | `session_id`, `agent_id`, `ended_at`, `reason`="idle" |
| file gzipped / moved to `archived_sessions/` | fsnotify | `session.end` | `session_id`, `agent_id`, `ended_at`, `reason`="archived" |

Unknown record types are logged at debug and skipped — never error the watcher.

## Redaction & security

- All `tool_input` payloads pass through `pkg/telemetry/redact.Redact` at `TierPublic` before publish (same path the existing hook normalizer uses — see `cmd_agent_event_emit.go:nativeHookToEvent`).
- The watcher reads JSONL **only**; never opens TTYs, sockets, or environment files in `~/.codex/`.
- Watcher process runs unprivileged as the user; no `sudo`, no root-owned paths.
- Per Mitiga Labs (May 2026) MCP-proxy token disclosure: this slice does NOT touch any OAuth token storage in `~/.codex/`. Explicit denylist in the discoverer: skip `~/.codex/{auth,credentials,tokens,backups}`.

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| Codex JSONL schema drifts (different `originator` strings, new record types) | Mapper is lookup-based with a fallthrough that logs at debug + emits an `agent.status.change`=unknown only. Schema check unit tests against committed real fixture. |
| Replaying old files on first run floods HUD | `--from now` default; positional offset persisted to `~/.loom/codex-watch/offsets.json` and respected on restart |
| Hundreds of historic session files cause fsnotify watch exhaustion | Only watch the current YYYY/MM/DD directory + the parent (for date rollovers); ignore older date dirs unless `--from all` |
| Per-file goroutine leak if sessions never end | 4-hour absolute max lifetime per goroutine; force a synthetic `session.end` on timeout |
| Disk full / partial JSON write | Use `bufio.Scanner` with `ScanLines`; on parse failure rewind to prior `\n` and retry next poll |
| Watcher crash silently loses Codex visibility | Existing daemon supervisor restarts; emit a single `daemon.codexwatch.up`/`down` event so HUD can show health |

## Why not the Codex App Server directly

The Codex App Server is the documented, stable JSON-RPC surface ([OpenAI Feb 2026 blog](https://openai.com/index/unlocking-the-codex-harness/)) and would be the "correct" integration in principle. But:

1. The **GUI app spawns it with `--listen stdio://`** (verified via `ps` — PID 43517 on observation). The stdio is owned by the Electron parent process — no external socket exposed.
2. Spawning our **own** app-server instance (`--listen unix://`) would not give us the GUI's live thread events; it's a separate process with its own event bus, even though both processes share the on-disk session store.
3. `codex app-server proxy --sock` requires a server already listening on a Unix socket — which the GUI does not provide.

The session-file tail is the only externally-observable hook into Codex Desktop activity today. If/when Codex Desktop exposes its app-server on a Unix socket (likely follow-up given v0.125 trajectory), we swap the source layer without touching the mapper or publisher.

## Test plan

- **Unit**: `mapper_test.go` — canonical events against a checked-in fixture trimmed from a real session file. Cover session_meta, function_call/output pairing, message role classification, unknown record fallthrough.
- **Unit**: `tailer_test.go` — append-only, file rotation, gzip-on-archive, partial-line recovery, idle-timeout synthetic end.
- **Integration**: `codexwatch_test.go` — temp dir with synthetic JSONL files + an in-process HUD HTTP receiver; assert envelopes appear with correct types and ordering.
- **Smoke**: run `loom codex-watch --from now` locally during this work session; touch the running Codex Desktop, then verify HUD shows the Codex session and our session as concurrent agents.

## Acceptance criteria

1. With `loom codex-watch` running and a fresh Codex Desktop session opened, the HUD fleet view shows the Codex agent within 5 seconds of session start.
2. Tool calls fired inside Codex appear as `tool.call.start`/`tool.call.end` events in the daemon event log with correct call_id pairing.
3. Closing the Codex Desktop window (and the session file going stale for >30 min, OR being archived) triggers a `session.end` event.
4. Running `loom codex-watch` against an empty `~/.codex/sessions/` does not error and emits no events.
5. All unit tests pass; `make test ./internal/hud/codexwatch/...` is green.
6. No new lint warnings on `make lint`.

## Out of scope follow-ups (Slice 1b / 2)

- Skybridge widget that renders the unified HUD inside Claude Code Desktop chat + Code tab + ChatGPT mobile
- Handoff approval cards via MCP elicitations
- Approval-mediation: hooking Codex's `item/commandExecution/requestApproval` (requires the app-server bridge — blocked on Codex exposing a unix socket from the GUI)
- Bi-directional: writing back to Codex (e.g. injecting a `thread/inject_items` reminder when another agent claims a file)
