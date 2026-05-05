# Research: Agent Telemetry Event Bus + Live Spectator Mode

**Date:** 2026-05-04
**Branch (loom-core):** `plan/event-bus-spectator`
**Status:** draft for review
**Companion docs:**
- Spec: `.loom/98-product-spec-agent-telemetry-spectator-2026-05-04.md`
- Plan: `.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md`

## Goal

Make the HUD (and the iOS companion) a live X-ray of every active agent session — see which tool each Claude/Codex/Gemini session is invoking *as it happens*, not via a 5–15s polling delta. Same primitive should unblock follow-on features the user picked in the same brainstorm: backchannel between live sessions, adversarial-pair workflows, and a skill effectiveness leaderboard.

## Headline finding

Most of the substrate is already shipped. We do **not** need to design a new transport, choose a new pubsub, or build a new HUD streaming layer. We need to:

1. Publish session-lifecycle and per-tool-call events that today either don't fire or only flow through the post-hoc `agent.spawn.telemetry.delta` batch event.
2. Add a privacy/redaction layer (currently zero) so spectator output is safe to render.
3. Wire the `PreToolUse` and `SessionStart` hooks across all three agent platforms (Claude Code, Codex, Gemini) — today only `PostToolUse` is wired and only for `agent_task_update`.
4. Add a `LiveSessionsCard` to the HUD that subscribes to the existing SSE endpoint with the new event types.

Everything else (HTTP SSE transport, fan-out, exponential-backoff reconnection, presence registry, iOS SSE client parity) reuses code that already passes tests.

## What already exists

### Daemon EventBus + SSE endpoint
- `internal/daemon/events.go:14-207` — in-process pub/sub; HTTP SSE endpoint mounted at `/events`; non-blocking publish with 256-slot per-subscriber buffer + dropped-event counter.
- 18 event types already defined: `server.health`, `process.start|stop|error`, `workflow.step`, `tool.call`, `config.reload`, `cache.evict`, `access.denied`, `decomp.hint`, `tools.list_changed`, `resources.list_changed`, `hub.connected|disconnected`. **`tool.call` is defined but not currently published per-invocation** — it only appears inside `agent.spawn.telemetry.delta` aggregates.

### HUD SSE Hub
- `internal/hud/sse_hub.go:15-138` — fan-out broker; mounted at `/api/events`; 64-slot per-client buffer; 15s heartbeat; non-blocking broadcast (drops on slow clients).

### Bridge event consumer (daemon → HUD)
- `internal/hud/bridge/events.go:29-323` — HTTP SSE client with exponential backoff (1s base, 30s max, 2× factor); `On(eventType, handler)` and `OnAny(handler)` registration; native fast-path parser + legacy fallback.

### Spawn telemetry (closest existing analog)
- `internal/hud/bridge/spawn_telemetry.go:1-150` — emits `agent.spawn.telemetry.delta` events; `SpawnTelemetryAccumulator` is thread-safe with `StartToolCall` → `CompleteToolCall` tracking. The per-call data exists internally but is only flushed in delta packets keyed off the spawn lifecycle, not per tool invocation.

### Agent presence registry
- `pkg/agentcontext/svc_presence.go:15-539` — full schema: `AgentPresence{ID, AgentID, SessionID, Status, Description, CurrentTask, ActiveFiles, WorkingDir, Branch, WorktreeID, AgentType, PRUrl, LastHeartbeat, HeartbeatTTL, ...}`. State machine: active → idle (1× TTL) → offline (2× TTL) → expired (3× TTL with auto-cleanup). Persisted in Qdrant. Has `onEvent(eventType, agentID, oldStatus, newStatus)` callback hook **wired for SSE broadcast but not actually publishing today**.

### Session lifecycle
- `pkg/agentcontext/schema.go:166-194` — `Session{ID, AgentID, Namespace, Project, StartedAt, EndedAt, Status, Description, WorkingDir, ParentSessionID, RootSessionID, PipelineRef, EntryCount, TotalTokens, LastSummaryAt}`.
- Lifecycle: `agent_session_start` → active → `agent_session_end` → ended → auto-summarize → summarized.
- **Gap:** no SSE events emitted on any state transition. Presence-cleanup callback exists but doesn't broadcast.

### Hook surface
- `cmd/loom/cmd_agent_task_sync.go:1-50+` — only `PostToolUse` is implemented, only for `agent_task_update`. No `SessionStart`, `PreToolUse`, or `SubagentStart/Stop` event publishing.
- Per `CLAUDE.md` lifecycle hooks table: Claude Code supports 22 events, Gemini supports 11, Codex has only `notify`. Cross-platform parity is uneven.

### iOS SSE client
- `apps/loom-companion-ios/Sources/LoomCompanionKit/Networking/SSEClient.swift` — `AsyncStream`-based, 1s–30s exponential backoff (matches daemon constants exactly), `SSEEventBroadcaster` (MainActor) fans out to multiple handlers via UUID registry. Already consumes `/api/mobile/v1/events/stream` (or equivalent).

### HUD card / monitor pattern
- `internal/hud/monitor/*.go` — `BaseMonitor[T]` with `StartLoop(interval, refreshFn)`; `StreamMonitor` polls `agent.ContextStream(since=watermark, limit=100)` at fixed interval, dedupes by ID, broadcasts deltas via `OnRefresh`.
- For *live* cards (not poll-based), the pattern is: subscribe via `bridge/events.go` `On(eventType, handler)` → store in Svelte store → render.

### Daemon socket bridge (request/response only)
- `internal/hud/bridge/daemon.go:45-100+` — Unix socket + `mcp.StdioTransport` (JSON-RPC 2.0); persistent connection with 30s call timeout; circuit breaker (3-failure threshold, 10–60s cooldown). **Synchronous only — events do not multiplex over this socket.** The bridge holds a *separate* HTTP SSE connection for the event stream.

## What's missing

| Gap | Impact | Where it bites |
|---|---|---|
| **G-1** Session lifecycle events not published | Spectator can't show "session X started, ended, went idle" | `pkg/agentcontext/svc_sessions.go`, `svc_presence.go` callbacks |
| **G-2** `PreToolUse` hook unwired across all 3 platforms | Spectator sees tool *completion* but not "Claude is about to call X" — UX is laggy | `.claude/settings.json`, `.gemini/settings.json`, `cmd/loom/cmd_agent_*.go` |
| **G-3** No per-tool-call live event (only batched in spawn telemetry) | Cannot do per-call timing visualization or "Claude is reading file Y right now" | `internal/hud/bridge/spawn_telemetry.go` accumulator flushes only at delta-batch boundaries |
| **G-4** No redaction layer | Tool args/results may contain secrets; broadcasting raw is unsafe | Codebase-wide; only `sanitizeContainerName`, `sanitizeBrowserKitMessage` exist (unrelated) |
| **G-5** Multi-platform hook parity uneven | Codex sessions invisible to spectator (only `notify` hook); Gemini partial | `services/loom-core/mcp/context/registry.yaml` sync surface |
| **G-6** Backpressure → silent drops | Under busy multi-agent load, spectator events get dropped without UI signal | `internal/daemon/events.go` 256-slot buffer |

## Transport decision

| Option | Pros | Cons | Verdict |
|---|---|---|---|
| Reuse daemon EventBus + HTTP SSE | All clients (HUD, iOS, future) already speak it; 18 event types defined; reconnection battle-tested | Per-subscriber buffer drops on slow consumers (G-6) | **✅ Use this.** Add backpressure metrics rather than re-architect. |
| Multiplex events over the daemon Unix socket | One connection | Breaks request/response semantics; no mobile analog (mobile speaks HTTPS) | ❌ |
| External pubsub (NATS/Redis) | Multi-host scale | Premature; daemon is single-host today; would force ops burden | ❌ for v1; revisit if we go multi-host |

## Schema-shape decisions to make in the spec

1. **Event type naming.** Should new events be `session.start`, `session.end`, `tool.call.start`, `tool.call.end`, `agent.status.change` (verb-suffix style matching existing `process.start|stop|error`) or `session.started`, etc.? — Existing convention uses **bare nouns + verbs**, e.g., `process.start` not `process.started`. Match that.
2. **Per-call vs aggregate.** Per-tool-call events are higher fidelity but 10–100× event volume. Recommend: emit per-call by default, allow subscribers to filter by agent_id or namespace.
3. **Redaction tiers.** Three tiers: `public` (event metadata only — name, timing, agent_id), `redacted` (args/results with secret patterns masked), `private` (args/results visible only to the originating session's owner via auth). Default: `redacted` for tool args/results.
4. **Backwards compat.** Keep existing `agent.spawn.telemetry.delta` for the post-hoc summary; new events are additive.

## Risks + open questions

- **Privacy is the dominant risk.** A spectator card that streams raw tool args means secrets in environment variables, prompts, or DB connection strings could leak across sessions. The redaction layer must land *before* the spectator card is enabled by default.
- **Multi-platform hook gap is structural.** Codex offers only `notify`; we cannot get per-tool-call events from a Codex session without a wrapping shim or until OpenAI ships richer hooks. Spec should call this out as "Claude Code first; Codex/Gemini follow as platform support permits."
- **Event volume.** A busy day with 4 active agents × ~30 tool calls/min = ~120 events/min just from `tool.call.start|end`. The 256-slot buffer drains fast; need to confirm or raise.
- **Event correlation across worktrees.** Today `agent_id` is per-process; sessions in different Claude Code worktrees share an agent_id namespace. Need a clean way to display them as separate "lanes" in the spectator UI.
- **Auth model.** Today the SSE endpoint at `/events` and `/api/events` is unauthenticated within localhost. iOS hits `/api/mobile/v1/events/stream` which is admin-token-gated (per recent `fix/hud-embed-admin-token` MR). Spec must specify which surfaces broadcast which redaction tier.

## Sources

- `internal/daemon/events.go:14-207` — EventBus + SSE endpoint
- `internal/hud/sse_hub.go:15-138` — HUD fan-out
- `internal/hud/bridge/events.go:29-323` — bridge consumer
- `internal/hud/bridge/spawn_telemetry.go:1-150` — current per-call tracking + delta event
- `pkg/agentcontext/svc_presence.go:15-539` — presence schema + lifecycle
- `pkg/agentcontext/schema.go:166-194` — session record
- `cmd/loom/cmd_agent_task_sync.go:1-50` — current PostToolUse wiring
- `apps/loom-companion-ios/Sources/LoomCompanionKit/Networking/SSEClient.swift` — iOS SSE consumer
- `internal/hud/bridge/daemon.go:45-100` — socket bridge (request/response only)
- `CLAUDE.md` lifecycle hooks table — multi-platform hook coverage
- `.loom/80-product-spec-headless-agent-telemetry-sdk-2026-04-06.md` — prior cache/usage telemetry work
- This session's MR !281 cleanup context (Mills v2 phases 1–8 just shipped)
