# Implementation Plan: Agent Telemetry Event Bus + Live Spectator Mode

**Date:** 2026-05-04
**Branch (loom-core):** `plan/event-bus-spectator`
**Status:** draft for review
**Companion docs:**
- Research: `.loom/97-research-agent-telemetry-spectator-2026-05-04.md`
- Spec: `.loom/98-product-spec-agent-telemetry-spectator-2026-05-04.md`

## Sequencing principle

Each phase is **independently shippable** behind a feature flag. The minimum demo (Phase 1) shows session lifecycle events flowing in the existing HUD within hours of merge. The first user-visible spectator (Phase 3) requires Phases 0 + 1 + 2. Phases 4–6 harden and extend.

Estimated total: **5–7 working days** of focused work, parallelizable across worktrees as noted per phase.

```
Phase 0 (foundation) ──► Phase 1 (session events)
                    └──► Phase 2 (per-call events) ──► Phase 3 (HUD card)
                                                  └──► Phase 5 (iOS)
                    └──► Phase 4 (backpressure metrics)
                                                       └──► Phase 6 (hardening + CLI spectate)
```

## Phase 0 — Foundation: redaction primitive + spec ratification

**Goal:** Land the standalone `pkg/telemetry/redact/` package with full test coverage, and a one-line policy doc, so every later slice can call `redact.Redact()` and `redact.Summary()` without bikeshedding.

### Slice 0.1 — `pkg/telemetry/redact/` package

- Spec ref: `.loom/98-…2026-05-04.md` §"Redaction model" + §"Redaction primitive"
- Files: `pkg/telemetry/redact/redact.go`, `redact_test.go`, `patterns.go`, `patterns_test.go`, `policy.go`, `policy.yaml` (embedded), `policy_test.go`
- Implementation:
  - `Tier` enum (`public`/`redacted`/`private`)
  - `Redact(args, tier)` pure function; idempotent
  - `Summary(toolName, result, tier)` returning bounded string
  - 6 default patterns: AWS keys, GitLab/GitHub tokens, Bearer/Basic auth, JWT-shape, `password|secret|api_key=`, connection strings
  - Per-tool policy registry seeded with `Read`, `Write`, `Edit`, `Bash`, `agent_context_add` defaults
  - Fallback default: `DROP` for `public`, mask-everything for `redacted`
- Acceptance:
  - Unit tests: every pattern has positive + negative cases
  - Property test: `Redact(Redact(x)) == Redact(x)` (idempotent)
  - Property test: `len(Redact(x).keys) <= len(x.keys)` (never adds keys)
  - Coverage > 90% for the package
  - Benchmark: `BenchmarkRedact_LargeArgs` < 50µs for typical Bash args
- Quality gate: `go test -race -cover ./pkg/telemetry/redact/...`; `golangci-lint run pkg/telemetry/redact`
- Dependencies: none — fully standalone
- **Parallelizable:** yes, single worktree

### Slice 0.2 — Spec walk-through + signoff

- Files: this file + 97/98
- Action: short async review with the user; resolve any pushback on schema names, redaction defaults, or hook coverage promises
- Acceptance: user comment "approved" on the planning artifact PR
- **Blocking:** all subsequent phases

---

## Phase 1 — Session lifecycle events (smallest valuable demo)

**Goal:** `session.start`, `session.end`, `agent.status.change` flow through the existing EventBus end-to-end, visible in the existing HUD via the existing `bridge/events.go` consumer (no new UI yet — verify with browser DevTools or `curl /api/events`).

### Slice 1.1 — Event types + publish sites

- Spec ref: `.loom/98-…2026-05-04.md` §"Event schema additions"
- Files:
  - `internal/daemon/events.go`: add `EventSessionStart`, `EventSessionEnd`, `EventAgentStatusChange` constants + payload structs (matching existing `EventProcessStart`/`EventToolCall` convention — no `Type` infix)
  - `pkg/agentcontext/svc_sessions.go`: emit `session.start` in `Start`, `session.end` in `End`
  - `pkg/agentcontext/svc_presence.go:25-48,379-380` (`onEvent` callback declared + invoked): wire to publish `agent.status.change` from the call site at line 379-380
- Acceptance:
  - Starting a session via `agent_session_start` MCP tool produces a `session.start` event observable at `curl -N http://localhost:<port>/events`
  - Ending a session emits `session.end` with correct `EntryCount` and `DurationMs`
  - Presence transitions (active → idle → offline) emit `agent.status.change`
- Quality gate: `go test -race ./pkg/agentcontext/... ./internal/daemon/...`; manual smoke via `curl`
- **Parallelizable** with Slice 0.1

### Slice 1.2 — Event consumer integration smoke

- Files: `internal/hud/bridge/events.go` — register handlers for the new types (no-op handlers, just confirm flow)
- Acceptance: bridge logs the new event types when running `loom hud --debug`
- Quality gate: manual smoke; no new tests needed (consumer is generic)

---

## Phase 2 — Per-tool-call events + hook wiring

**Goal:** `tool.call.start` and `tool.call.end` emit reliably for every Claude Code tool invocation; Gemini parity; Codex coarse via `notify`.

### Slice 2.1 — `loom agent event-emit` CLI subcommand

- Spec ref: `.loom/98-…2026-05-04.md` §"Hook wiring per platform"
- Files: `cmd/loom/cmd_agent_event_emit.go`, `cmd/loom/cmd_agent_event_emit_test.go`
- Implementation:
  - Reads stdin JSON (hook payload — schema differs per platform)
  - `--hook <name>` flag (`session-start`, `session-end`, `pre-tool-use`, `post-tool-use`)
  - `--platform <name>` flag for platform-specific normalization
  - Calls `redact.Redact(args, TierPublic)` before publishing
  - Publishes via existing daemon socket bridge
- Acceptance:
  - Unit tests for each platform's hook payload normalization
  - End-to-end: piping a recorded Claude Code `PreToolUse` payload produces a well-formed `tool.call.start` event
- Quality gate: `go test -race ./cmd/loom/...`; smoke test with recorded fixtures from `testdata/hook-payloads/`
- Dependencies: Phase 0 (uses `redact.Redact`)

### Slice 2.2 — Hook config generator updates

- Spec ref: `CLAUDE.md` "Auto-Generated Configs" section + spec §"Hook wiring per platform"
- Files: `pkg/sync/claude.go`, `pkg/sync/gemini.go`, `pkg/sync/codex.go`, golden test fixtures under `testdata/sync/`
- Implementation:
  - Extend Claude Code generator to wire `SessionStart`, `SessionEnd`, `PreToolUse`, `PostToolUse` → `loom agent event-emit --hook <name> --platform claude-code`
  - Extend Gemini generator: `SessionStart`, `SessionEnd`, `BeforeTool`, `AfterTool`
  - Codex: parse `notify` payload → `tool.call.end` (no `start` available)
- Acceptance:
  - `loom sync claude --regen` produces a `.claude/settings.json` with the four hooks pointing at `loom agent event-emit`
  - `loom sync claude --check` passes (no drift)
  - Golden tests cover all three platforms
- Quality gate: `go test ./pkg/sync/...`; manual `loom sync claude --regen --diff`

### Slice 2.3 — Per-tool-call event types + spawn telemetry refactor

- Spec ref: `.loom/98-…2026-05-04.md` §"`tool.call.start`" / "`tool.call.end`"
- Files:
  - `internal/daemon/events.go`: add `EventToolCallStart`, `EventToolCallEnd` payload structs (keep existing `EventToolCall` as legacy alias)
  - `internal/hud/bridge/spawn_telemetry.go`: at `StartToolCall` publish `tool.call.start`; at `CompleteToolCall` publish `tool.call.end` (in addition to current delta-batch behavior)
- Acceptance:
  - A live Claude Code session emits `tool.call.start` ≤ 100ms after the tool invocation begins
  - `call_id` correlates the `start`/`end` pair
  - Existing `agent.spawn.telemetry.delta` continues to fire (no regression)
- Quality gate: `go test -race ./internal/hud/bridge/...`; smoke with `loom hud` + a Claude Code session
- Dependencies: Slice 0.1, 2.1, 2.2

---

## Phase 3 — HUD `LiveSessionsCard`

**Goal:** Visible spectator card on the HUD; default landing view for the user.

### Slice 3.1 — Svelte component shell + Svelte store

- Spec ref: `.loom/98-…2026-05-04.md` §"HUD LiveSessionsCard UX"
- Files:
  - `internal/hud/web/src/lib/components/LiveSessionsCard.svelte`
  - `internal/hud/web/src/lib/stores/liveSessions.ts` — store keyed by `agent_id`, ring buffer of last 4 calls per session
  - `internal/hud/web/src/routes/+page.svelte` — add card to the dashboard
- Implementation:
  - Subscribe to `tool.call.start`, `tool.call.end`, `session.start`, `session.end`, `agent.status.change` via existing SSE client
  - Render collapsed view per spec
  - Click row → expand modal showing last 50 calls
- Acceptance:
  - Card renders in HUD with mock data
  - With one Claude Code session running, ticker updates within 1s of each tool call
- Quality gate: `pnpm test` in `internal/hud/web/`; visual smoke in Chrome
- Dependencies: Phase 2 (events flowing)

### Slice 3.2 — Auth-gated `redacted`-tier expanded view

- Files: `internal/hud/api_events.go` (or equivalent) — add tier-aware routing on `/api/events`
- Implementation:
  - Default subscription = `public`
  - Authenticated subscription with `?tier=redacted` parameter (gated by HUD session cookie)
  - Server-side enforcement; never trust client tier param
- Acceptance:
  - Unauthenticated subscriber receives only `public` events
  - Authenticated subscriber receives `redacted` events with masked args
  - Expanded modal in `LiveSessionsCard` shows masked secrets for known patterns (test with planted `AKIA...` key)
- Quality gate: `go test ./internal/hud/...`; integration test with auth fixture

---

## Phase 4 — Backpressure observability (parallel with Phase 3)

**Goal:** Operator can see when the spectator stream is dropping events, before user complaints arrive.

### Slice 4.1 — Per-subscriber metrics + `bus.backpressure` event

- Spec ref: `.loom/98-…2026-05-04.md` §"Backpressure + observability of the bus itself"
- Files:
  - `internal/daemon/events.go`: per-subscriber drop counter + windowed rate (10/min trigger)
  - `internal/daemon/metrics.go` (new or extend existing): Prometheus gauges
- Acceptance:
  - Metrics exposed on existing `/metrics` endpoint
  - `bus.backpressure` event fires with subscriber identity + drop count when rate exceeded
- Quality gate: `go test ./internal/daemon/...`; load test (slice 4.2)

### Slice 4.2 — Load test + buffer tuning

- Files: `internal/daemon/events_loadtest.go` (gated by build tag `loadtest`)
- Implementation:
  - Synthetic publisher firing 200 events/sec for 60s across 5 event types
  - Verify spectator subscriber miss-rate < 1%
  - If miss-rate > 1%, raise per-spectator buffer from 256 → 1024 (configurable per registration)
- Acceptance: `go test -tags loadtest -run TestEventBusUnderLoad ./internal/daemon/...` passes

### Slice 4.3 — HUD warning banner

- Files: `internal/hud/web/src/lib/components/StreamHealthBanner.svelte`
- Acceptance: yellow banner appears when `bus.backpressure` event received; auto-dismisses after 5min of clean stream

---

## Phase 5 — iOS spectator (parallel with Phase 3)

### Slice 5.1 — `LiveSessionsView` SwiftUI screen

- Spec ref: `.loom/98-…2026-05-04.md` §"iOS spectator surface"
- Files:
  - `apps/loom-companion-ios/Sources/LoomCompanion/Views/LiveSessions/LiveSessionsView.swift`
  - `apps/loom-companion-ios/Sources/LoomCompanionKit/LiveSessions/LiveSessionsAPI.swift`
  - `apps/loom-companion-ios/Sources/LoomCompanionKit/LiveSessions/LiveSessionEvent.swift` (matches Go schema)
  - Update `Sources/LoomCompanion/ContentView.swift` to add tab/route
- Implementation:
  - Subscribe via existing `SSEClient` to new event types
  - Filter to `public` tier (server-side enforced; client also pins query param)
  - Collapsed list view + tap-to-expand modal
- Acceptance:
  - With HUD running on Mac, iOS sim shows the same active sessions within 5s
  - Tap a session → modal with last 20 calls
- Quality gate: `xcodebuild -scheme LoomCompanion -destination 'platform=iOS Simulator,name=iPhone 17 Pro' test`
- Dependencies: Phase 2 (events flowing); Phase 3 not strictly required

### Slice 5.2 — `xcodegen` regen

- Files: `apps/loom-companion-ios/project.yml`
- Action: `make mobile-ios-project-sync`
- Acceptance: `make mobile-app-build` green

---

## Phase 6 — Hardening + multi-platform CLI spectator

### Slice 6.1 — `loom agent spectate` terminal CLI

- Spec ref: `.loom/98-…2026-05-04.md` §"New CLI commands"
- Files: `cmd/loom/cmd_agent_spectate.go`, `internal/spectate/render.go`
- Implementation:
  - `tcell` or `bubbletea`-style live terminal pane (check existing dep choices in `go.mod` first)
  - `--filter "agent_id=X,tool_name=Bash"` query string syntax
  - Subscribes at `redacted` tier (CLI runs as user; trusted)
- Acceptance: live demo in a terminal next to a Claude Code session
- Quality gate: `go test ./cmd/loom/... ./internal/spectate/...`

### Slice 6.2 — `loom sync … --check` CI gate

- Files: `.gitlab-ci.yml` lint stage
- Implementation: add a job that runs `loom sync claude --check && loom sync gemini --check && loom sync codex --check` and fails CI on drift
- Acceptance: a deliberate edit to `.claude/settings.json` causes CI to fail with clear message

### Slice 6.3 — Default-on flip + telemetry retro

- Files: `internal/hud/web/src/routes/+page.svelte` — make `LiveSessionsCard` default-visible (was opt-in earlier)
- Acceptance: 1-week soak: no regressions in HUD load time; backpressure metric stays under 0.1% drop rate

---

## Quality gates (cumulative)

| After phase | Gate |
|---|---|
| 0 | `go test -race -cover ./pkg/telemetry/redact/...` ≥ 90% |
| 1 | `curl -N /events` shows `session.*` and `agent.status.change` events end-to-end |
| 2 | Recorded hook payloads → events round-trip; sync golden tests green |
| 3 | HUD card visually correct in Chrome with one live session; secrets masked in expanded view |
| 4 | Load test passes (>99% delivery at 200 events/sec); HUD banner fires under synthetic backpressure |
| 5 | iOS sim shows live sessions within 5s of Mac HUD |
| 6 | CI drift check active; default-on with no regression |

## Parallelization plan

- Phase 0 + Phase 1 + Phase 4 can land in parallel (no shared files).
- Phase 2 depends on Phase 0; Phase 3 depends on Phase 2; Phase 5 depends on Phase 2.
- Phase 3 and Phase 5 can ship in parallel from different worktrees.
- Phase 6 sequential after 3 + 5 land.

Recommended worktree split for an aggressive shipping pace:
- worktree 1: Phase 0 (redaction) + Phase 1 (session events) + Phase 2 slice 2.1 (CLI) → one engineer/agent
- worktree 2: Phase 4 (backpressure) → independent
- worktree 3: Phase 2 slice 2.2 + 2.3 → after Phase 0
- worktree 4: Phase 3 (HUD card) → after Phase 2
- worktree 5: Phase 5 (iOS) → after Phase 2

## Out of scope for this plan

(For follow-on /plan-loom-core cycles)
- **Backchannel between live sessions** (brainstorm item #2): subscribe to `tool.call.*` from another session and reply via a new `agent_message_send` MCP tool. Plan once event substrate is real.
- **Adversarial pair workflow** (brainstorm #3): wraps Mills debate-mode + spectator + diff-comparison. Plan after backchannel.
- **Skill effectiveness leaderboard** (brainstorm #4): subscribes to `tool.call.*` filtered to MCP `Skill` invocations; aggregate per skill. ~1 day once Phase 2 lands.
- **Cache ROI dashboard** (brainstorm #5): independent surface; depends on capturing `usage.cache_*` from Anthropic SDK responses (separate plumbing per `.loom/80-…`).
- **Time-to-green per branch type** (brainstorm #6): independent; pure GitLab API; doesn't need event bus.

## Success criteria for the whole plan

1. With two terminal sessions (Claude Code + Codex) running, the HUD `LiveSessionsCard` shows both sessions and their tool calls in real time.
2. iOS companion shows the same on the user's phone.
3. No raw secrets ever broadcast on `public` tier; manual audit of one busy day's event log shows zero secret leaks.
4. Backpressure metric stays < 0.1% drop rate under normal multi-agent use.
5. The same event substrate is wired into a working `loom agent spectate` CLI.
6. Foundation is in place for backchannel + adversarial-pair + skill-leaderboard plans to be ~1-week each instead of multi-week.

## Sources

- `.loom/97-research-agent-telemetry-spectator-2026-05-04.md`
- `.loom/98-product-spec-agent-telemetry-spectator-2026-05-04.md`
- `internal/daemon/events.go` — current EventBus
- `pkg/agentcontext/svc_sessions.go`, `svc_presence.go` — session/presence lifecycle
- `internal/hud/bridge/spawn_telemetry.go` — current per-call accumulator
- `apps/loom-companion-ios/Sources/LoomCompanionKit/Networking/SSEClient.swift` — iOS SSE client
- `CLAUDE.md` — lifecycle hooks table
