# Phase 3 Plan: Headless Agent Full-Stack Drive + Canonical Telemetry Mapping

**Date:** 2026-04-07
**Predecessors:**
- `.loom/79-research-headless-agent-telemetry-sdk-2026-04-06.md`
- `.loom/80-product-spec-headless-agent-telemetry-sdk-2026-04-06.md`
- `.loom/81-implementation-plan-headless-agent-telemetry-sdk-2026-04-06.md`

**Goal (from user):** Ensure the platform can fully drive headless Codex and Claude agents via the web HUD and mobile app, with all session telemetry properly mapped from each CLI utility's internal data/concept models to Loom's canonical model.

---

## 1. Phase 2 status (what shipped this cycle)

| Slice | Commit | Description |
|---|---|---|
| 7a | `7195528a` | Scaffold `tools/spawn-driver/` with parser-compatible stubs |
| 7b | `536e9925` | Embed bundle via `go:embed`, wire `Request.UseSDKDriver` flag in `internal/hud/spawn.go:370` |
| 7c | `dd6140ca` | Replace stubs with real `@anthropic-ai/claude-agent-sdk` and `@openai/codex-sdk` calls; ship Codex compat layer (`aggregated_output→stderr`, `error.message→error`, `items[]→text`) |

Round 4 of Phase 1 also shipped earlier this cycle: budget enforcement (`runBudgetWatcher` at `internal/hud/spawn.go:879`), telemetry persistence to agent-context (`persistTelemetrySummary` at `internal/hud/spawn.go:773`), paginated telemetry sub-endpoints (`internal/hud/domain/spawn/handler_telemetry.go`).

**Net result:** the Loom platform can now spawn headless Claude/Codex agents and persist their telemetry, but the web HUD and iOS companion only render *part* of what the backend records, and there is no follow-up message capability — every spawn is single-shot.

---

## 2. Surface-area inventory (sourced)

### REST endpoints already shipped

**Web HUD** (`internal/hud/domain/spawn/spawn.go:23`):
- `POST /api/agent/spawn` — create spawn
- `GET  /api/agent/spawns` — list
- `GET  /api/agent/spawn/config` — agent types, projects, defaults
- `GET  /api/agent/spawn/{id}` — detail
- `GET  /api/agent/spawn/{id}/telemetry` — full snapshot (admin)
- `GET  /api/agent/spawn/{id}/telemetry/{tools|files|errors}` — paginated subviews (admin)
- `POST /api/agent/spawn/{id}/stop` — stop

**Mobile API** (`internal/hud/domain/mobile/mobile.go:53`):
- `POST /api/mobile/v1/agent/spawn`
- `GET  /api/mobile/v1/agent/spawns`
- `GET  /api/mobile/v1/agent/spawn/config`
- `GET  /api/mobile/v1/agent/spawn/{id}`
- `GET  /api/mobile/v1/agent/spawn/{id}/stream` — **SSE** (only place spawn events stream live today)
- `GET  /api/mobile/v1/agent/spawn/{id}/telemetry[/tools|files|errors]`
- `POST /api/mobile/v1/agent/spawn/{id}/stop`

### Spawn UI surfaces

| Surface | File | What it currently shows |
|---|---|---|
| Web HUD | `internal/hud/frontend/src/lib/components/SpawnPanel.svelte:1-365` | Create form (project, branch, timeout, task), list with status badge + duration, stop button. **No telemetry view, no detail page, no live event stream, no follow-up message, no cost/turn budget UI.** |
| iOS companion | `apps/loom-companion-ios/Sources/LoomCompanion/Views/Spawn/SpawnAgentView.swift:1-202` | Create form, list with elapsed time + status badge, stop. SSE wiring exists in `SpawnViewModel.swift:1-131` for lifecycle events. **No telemetry detail view, no follow-up.** |

### Canonical telemetry types

`internal/hud/bridge/spawn_telemetry.go:8-58`:

```go
type SpawnTelemetry struct {
    ExternalSessionID string                  // claude session_id or codex thread_id
    TurnCount         int
    TotalCostUSD      float64
    TokenUsage        SpawnTokenUsage         // input, output, cache_creation, cache_read
    ModelUsage        map[string]ModelUse     // per-model breakdown (Claude only today)
    ToolCalls         []ToolCallEntry         // capped 500
    FileChanges       []FileChangeEntry       // capped 200
    Errors            []AgentError            // type, message, time
    StopReason        string                  // end_turn|max_turns|max_budget|execution_error
    LastMessage       string                  // final assistant text
}
```

### JSONL parsers

| Parser | File | Event types handled |
|---|---|---|
| Claude | `internal/hud/spawn_claude_parser.go:1-311` | `assistant`, `user`, `result`, `system/api_retry` |
| Codex | `internal/hud/spawn_codex_parser.go:1-278` | `thread.started`, `turn.started`, `turn.completed`, `item.started`, `item.completed`, `turn.failed`, `error` |

---

## 3. Canonical telemetry mapping reference

This is the explicit field-level translation table the user asked for. **Rows marked ✅ are wired today; rows marked ⚠️ are partial; rows marked ❌ are missing.**

### Claude Code → `SpawnTelemetry`

Source: `@anthropic-ai/claude-agent-sdk` `SDKMessage` types in `tools/spawn-driver/node_modules/@anthropic-ai/claude-agent-sdk/sdk.d.ts`. The driver in `tools/spawn-driver/src/claude-driver.ts:64` forwards `assistant|user|result|system` types untouched, so the parser at `internal/hud/spawn_claude_parser.go` consumes the same shape whether the source is the CLI or the SDK driver.

| SDK field | Canonical field | Status | Notes |
|---|---|---|---|
| `system.session_id` (subtype `init`) | `ExternalSessionID` | ✅ | `spawn_claude_parser.go:294` |
| `assistant.message.usage.input_tokens` | `TokenUsage.InputTokens` | ✅ | `spawn_claude_parser.go:54` |
| `assistant.message.usage.output_tokens` | `TokenUsage.OutputTokens` | ✅ | |
| `assistant.message.usage.cache_creation_input_tokens` | `TokenUsage.CacheCreationTokens` | ✅ | |
| `assistant.message.usage.cache_read_input_tokens` | `TokenUsage.CacheReadTokens` | ✅ | |
| `assistant.message.content[].type=tool_use` | `ToolCalls[].Name` (start) | ✅ | `spawn_claude_parser.go:119` |
| `user.message.content[].type=tool_result` | `ToolCalls[].DurationMs/Error` (complete) | ✅ | `spawn_claude_parser.go:187` |
| `assistant.message.content[].text` (Write/Edit/NotebookEdit input) | `FileChanges[]` | ✅ | inferred at `spawn_claude_parser.go:147` |
| `assistant.message.content[].type=text` | `LastMessage` | ✅ | |
| `assistant.message.content[].type=thinking` | broadcast only (not stored) | ✅ | `agent.spawn.thinking` SSE |
| `result.total_cost_usd` | `TotalCostUSD` | ✅ | |
| `result.num_turns` | `TurnCount` | ✅ | |
| `result.subtype` (`success`/`error_max_turns`/`error_max_budget_usd`) | `StopReason` | ✅ | mapped via `mapClaudeSubtype` |
| `result.usage.modelUsage{}` | `ModelUsage` | ✅ | per-model cost breakdown |
| `system.subtype=api_retry` | `Errors[]{type:rate_limit}` | ✅ | `spawn_claude_parser.go:293` |
| **`assistant.message.content[].type=server_tool_use`** | `ToolCalls[].ServerName` | ❌ | MCP tool calls are recorded as plain tool calls; the upstream MCP server name is dropped. Need to populate `ToolCallEntry.ServerName`. |
| **`stream_event.partial_message`** | live progress events | ❌ | Driver currently sets `includePartialMessages: false`; we lose token-by-token streaming. Phase 3 should opt in for the live preview path. |
| **`result.permission_denials[]`** | `Errors[]{type:permission_denied}` | ❌ | not parsed; agents that hit permission gates today look "successful" |

### Codex CLI → `SpawnTelemetry`

Source: `@openai/codex-sdk` `ThreadEvent`/`ThreadItem` in `tools/spawn-driver/node_modules/@openai/codex-sdk/dist/index.d.ts`. The driver in `tools/spawn-driver/src/codex-driver.ts:91` runs every event through `transformItem()` which aliases SDK fields to the legacy parser names so `internal/hud/spawn_codex_parser.go` keeps working unchanged.

| SDK field | Canonical field | Status | Notes |
|---|---|---|---|
| `thread.started.thread_id` | `ExternalSessionID` | ✅ | `spawn_codex_parser.go:79` |
| `turn.started` | `TurnCount += 1` | ✅ | `spawn_codex_parser.go:85` |
| `turn.completed.usage.input_tokens` | `TokenUsage.InputTokens` | ✅ | also adds `cached_input_tokens` |
| `turn.completed.usage.output_tokens` | `TokenUsage.OutputTokens` | ✅ | |
| `turn.completed.usage.cached_input_tokens` | `TokenUsage.CacheReadTokens` | ⚠️ | currently summed into `InputTokens`, should map to `CacheReadTokens` for parity with Claude |
| `item.started` (command_execution / mcp_tool_call) | `ToolCalls[]` start | ✅ | |
| `item.completed.command_execution` (legacy: `stderr`; SDK: `aggregated_output`) | `ToolCalls[].ExitCode/Error` | ✅ | compat layer at `tools/spawn-driver/src/codex-driver.ts:96` |
| `item.completed.mcp_tool_call` (legacy: `error` string; SDK: `error.message`) | `ToolCalls[].Error` | ✅ | compat layer at `tools/spawn-driver/src/codex-driver.ts:105` |
| `item.completed.file_change` | `FileChanges[]` | ✅ | |
| `item.completed.agent_message` | `LastMessage` | ✅ | |
| `item.completed.todo_list` (legacy: `text`; SDK: `items[]`) | broadcast only | ✅ | compat layer joins items into `text` |
| `item.completed.reasoning` | broadcast only | ✅ | `agent.spawn.thinking` |
| `turn.failed` | `Errors[]{type:execution}` | ✅ | |
| `error` | `Errors[]{type:fatal}` | ✅ | |
| **`Thread.id` (post-runStreamed)** | `ExternalSessionID` | ⚠️ | duplicates `thread_id` capture but is the canonical post-turn handle; if we ever need to *resume* a Codex thread we need to capture this explicitly |
| **per-turn cost** | `TotalCostUSD` | ❌ | Codex SDK does not emit a `total_cost_usd` field; we'd need to multiply tokens × pricing in Loom. **Decision needed.** |
| **`item_type=mcp_tool_call.server`** | `ToolCalls[].ServerName` | ❌ | MCP server name is dropped today |
| **`turn.completed.usage.cache_creation_input_tokens`** | `TokenUsage.CacheCreationTokens` | ❌ | Codex SDK doesn't expose this — record as 0 |

### Mapping gaps to fix in Phase 3

1. **`ServerName` for MCP tool calls** — both parsers drop the upstream MCP server. Add fields to `tool_use`/`mcp_tool_call` parsing.
2. **`CacheReadTokens` for Codex** — split out `cached_input_tokens` instead of merging into `InputTokens`.
3. **Codex cost** — either ship a token×price calculator (`internal/hud/bridge/spawn_pricing.go`) or document `TotalCostUSD = 0` for Codex spawns and surface it as "n/a" in UI.
4. **Permission denials (Claude)** — add `Errors[]{type:permission_denied}` extraction from `result.permission_denials[]`.
5. **Live streaming preview** — opt into `includePartialMessages: true` (Claude) and forward `stream_event` partial messages over SSE for a typing indicator.

---

## 4. Phase 3 backlog — what's left to "fully drive" headless agents

Grouped by surface, sequenced for shipability.

### Track A — Multi-turn control plane (the missing capability)

**Why:** Without follow-up messages and interrupts, every spawn is one-shot. The user can't "say more" to a running agent. Both SDKs already support multi-turn (Claude via `Query.streamInput()` and `Query.interrupt()` in `sdk.d.ts:1608`; Codex by re-invoking `thread.runStreamed(input)` per turn — `index.d.ts:203`).

| Slice | Description | Files | Tests |
|---|---|---|---|
| **8a** | Driver multi-turn loop. Add `--control-file <path>` flag; driver polls file (200ms) for JSONL control commands `{"type":"message","text":"..."}` / `{"type":"interrupt"}` / `{"type":"close"}`. Claude path uses async-iterable input + `query.interrupt()`. Codex path queues messages and re-invokes `thread.runStreamed()` per turn, sharing the JSONL output stream. | `tools/spawn-driver/src/control.ts` (new), `tools/spawn-driver/src/{claude,codex}-driver.ts`, `tools/spawn-driver/src/cli.ts` | New `tools/spawn-driver/test/control.test.ts`; smoke tests with pre-seeded control file |
| **8b** | Spawn orchestrator wiring. Add `Request.MultiTurn bool`; create empty control file `/workspace/.loom/spawn-control-<id>.jsonl` via `injectControlMessage()` helper before invoking driver; pass `--control-file` to `buildSDKDriverCommand`. | `internal/spawn/types.go`, `internal/hud/spawn.go`, `internal/hud/spawn_sdk_driver.go` | `internal/hud/spawn_sdk_driver_test.go` |
| **8c** | REST endpoints. `POST /api/agent/spawn/{id}/message` and `/interrupt` (admin token). Mobile equivalents `POST /api/mobile/v1/agent/spawn/{id}/{message,interrupt}` (`ScopeAgentSpawn`). Each handler validates spawn is multi-turn, then writes a control message via `o.injectControlMessage()`. | `internal/hud/domain/spawn/handlers.go`, `internal/hud/domain/mobile/handler_spawn.go` | Handler-level table tests with fake orchestrator |

**Acceptance:** A Claude spawn started with `MultiTurn:true` accepts a follow-up `POST .../message` and the new prompt shows up as a new turn in the telemetry stream. Same for Codex. `interrupt` cancels the in-flight turn.

### Track B — Telemetry mapping fixes (make canonical model truly canonical)

Pure-Go, no UI changes. Each row from §3 above becomes one shippable change.

| Slice | Description | Files |
|---|---|---|
| **9a** | Capture MCP `server_name` for tool calls in both parsers; add `ServerName` to `tool_use`/`mcp_tool_call` extraction. | `internal/hud/spawn_claude_parser.go`, `internal/hud/spawn_codex_parser.go`, `internal/hud/bridge/spawn_telemetry.go` (already has the field) |
| **9b** | Split Codex `cached_input_tokens` into `CacheReadTokens` instead of `InputTokens`. | `internal/hud/spawn_codex_parser.go:107` |
| **9c** | Claude permission denials → `Errors[]{type:permission_denied}` from `result.permission_denials[]`. | `internal/hud/spawn_claude_parser.go:233` |
| **9d** | Codex cost estimator. Add `internal/hud/bridge/spawn_pricing.go` with a hard-coded price table (`gpt-5`, `gpt-5-codex`, etc.) and `EstimateCost(modelID, usage) float64`. Wire into Codex parser so `TotalCostUSD` is non-zero. **Decision:** ship as best-effort with a `cost_estimated:true` flag on telemetry. |
| **9e** | Live streaming preview. Opt into `includePartialMessages:true` in `claude-driver.ts`, forward `stream_event` partials as a new SSE event `agent.spawn.partial_message`. |

### Track C — Web HUD spawn detail + telemetry view (UI gap)

The web HUD currently shows status badges and duration but **none** of the rich telemetry the backend already records. This is the highest-value UI win because it surfaces the work Phase 1 already did.

| Slice | Description | Files |
|---|---|---|
| **10a** | Spawn detail page route + skeleton. Add `/spawns/{id}` route in the Svelte app, navigate from `SpawnPanel` row click. Server-render basic state, then fetch `/api/agent/spawn/{id}` and `/api/agent/spawn/{id}/telemetry`. | `internal/hud/frontend/src/routes/spawns/[id]/+page.svelte` (new), `SpawnPanel.svelte` |
| **10b** | Telemetry tabs: **Tool calls**, **File changes**, **Errors**, **Tokens & cost**. Each tab fetches the matching paginated endpoint (`/telemetry/tools` etc.) and renders a table with sort/filter. | `internal/hud/frontend/src/lib/components/SpawnTelemetry/{ToolsTab,FilesTab,ErrorsTab,UsageTab}.svelte` |
| **10c** | Live event stream via SSE. Reuse the existing mobile SSE route (`/api/mobile/v1/agent/spawn/{id}/stream`) by adding an admin-token-gated mirror at `/api/agent/spawn/{id}/stream` so the web HUD can subscribe. Render a live "agent activity" log on the detail page. | `internal/hud/domain/spawn/handlers.go`, frontend store |
| **10d** | Multi-turn input. After Track A ships, add a "Send follow-up" textarea + Interrupt button on the detail page (gated by `MultiTurn` flag). | `internal/hud/frontend/src/lib/components/SpawnTelemetry/MultiTurnInput.svelte` |
| **10e** | Cost & budget UI. Surface `TotalCostUSD`, `MaxCostUSD` budget, `TurnCount`, `MaxTurns` budget on spawn cards and detail page. Show a progress bar that turns red as budget is consumed. | `SpawnPanel.svelte`, detail page |

### Track D — iOS companion telemetry parity

The iOS app already has the SSE plumbing (`SpawnViewModel.swift:1`) but only consumes lifecycle events. We need to fetch and render telemetry.

| Slice | Description | Files |
|---|---|---|
| **11a** | `SpawnTelemetry` model + decoder. | `apps/loom-companion-ios/Sources/LoomCompanionKit/Models/SpawnTelemetryModels.swift` (new) |
| **11b** | API client methods: `getSpawnTelemetry`, `getSpawnTelemetryTools/Files/Errors`. | `LoomAPIClient.swift` |
| **11c** | Spawn detail view. New `SpawnDetailView.swift` reachable by tap on a `SpawnRow`, with the same four tabs as the web HUD (Tools, Files, Errors, Tokens). | `apps/loom-companion-ios/Sources/LoomCompanion/Views/Spawn/SpawnDetailView.swift` (new) |
| **11d** | Live SSE stream view — render `agent.spawn.tool_start/tool_complete/message/thinking` events as a scrollable activity feed. | `SpawnDetailView.swift` |
| **11e** | Multi-turn follow-up sheet — present a modal text field + send button when the spawn is multi-turn. After Track A. | `SpawnDetailView.swift` |
| **11f** | Widget: surface live cost / turn count / status of the most recently active spawn on the home-screen widget. | `apps/loom-companion-ios/Widget/` |

### Track E — Robustness / observability cross-cuts

Items from §1 of the original implementation plan that didn't make it into Phase 1.

| Slice | Description |
|---|---|
| **12a** | Spawn history archival — currently `internal/spawn/state.go` keeps every spawn forever in memory + the FileStore. Add a TTL prune for terminal spawns older than N days; expose archived spawns via a `?include_archived=true` query param. |
| **12b** | Concurrent spawn quota — `Config.MaxConcurrentSpawns` enforced in `SpawnOrchestrator.Spawn()` to prevent runaway pod creation. |
| **12c** | Pre-spawn cost estimate — call `EstimateCost(model, expected_tokens)` from §9d before spawning, return as part of `POST /api/agent/spawn` response so the UI can show "estimated $0.42–$1.05 for this task". |
| **12d** | Prometheus metrics dashboard — there's a `loom_spawn_*` metric set referenced in the original plan; verify it's wired and add a Grafana dashboard JSON under `platform/gitops/monitoring/dashboards/`. |
| **12e** | Live telemetry streaming via SSE — most telemetry fields broadcast as `agent.spawn.*` events but the **diff** between snapshots isn't broadcast; the web HUD has to re-fetch. Add `agent.spawn.telemetry.delta` events on every change. |

---

## 5. Sequencing & shippable order

Ordered by dependency + value. Each row is one shippable commit.

| Order | Slice | Track | Reason |
|---|---|---|---|
| 1 | 9a | B | Tiny, pure-Go, fixes a real telemetry bug (`ServerName`) — good warm-up |
| 2 | 9b | B | Tiny, fixes Codex token accounting parity |
| 3 | 9c | B | Tiny, surfaces silent permission failures |
| 4 | 8a | A | Driver multi-turn loop — independent of Go side, can be tested standalone via smoke tests |
| 5 | 8b | A | Wire `MultiTurn` flag into orchestrator + control file injection |
| 6 | 8c | A | REST endpoints + mobile equivalents |
| 7 | 9d | B | Codex cost estimator (separable; ships once we have the parser changes from 9b in) |
| 8 | 10a | C | Web HUD detail page skeleton — reads existing `/telemetry` endpoint |
| 9 | 10b | C | Telemetry tabs |
| 10 | 10c | C | SSE wiring + live activity log |
| 11 | 10e | C | Cost & budget UI |
| 12 | 10d | C | Multi-turn input UI (depends on 8c) |
| 13 | 11a–11c | D | iOS detail view + telemetry tabs |
| 14 | 11d | D | iOS live activity feed |
| 15 | 11e–11f | D | iOS multi-turn + widget |
| 16 | 12a–12e | E | Robustness cross-cuts; can interleave once core flows are green |
| 17 | 9e | B | Live streaming preview opt-in (depends on web HUD live log being stable) |

**Recommendation:** Ship slices 1–6 (Tracks A + B core) as a single parallel-slice-ship batch in this cycle, then move to UI in the next cycle. Track A unblocks the multi-turn UX everywhere downstream and is the single biggest capability gap in the platform.

---

## 6. Open decisions

1. **Codex cost source.** Hard-coded price table in Loom (slice 9d) vs. require Codex SDK to surface cost (upstream feature request). Hard-code is simpler and unblocks UI; we accept some drift when OpenAI changes prices.

2. **Multi-turn transport.** Polled control file vs. HTTP control port. The polled-file approach reuses the existing `injectAgentConfig` base64 cat heredoc pattern and avoids needing curl/wget in the pod image. Latency is ~200ms which is acceptable for human-typed follow-ups. **Recommendation:** ship file-based for 8a; revisit if anyone needs sub-100ms.

3. **Live streaming preview.** Opting into `includePartialMessages:true` ~3–4×s the JSONL volume. **Recommendation:** keep it off by default, expose via `Request.LivePreview bool`, default false.

4. **iOS widget refresh cadence.** Apple gives widgets a budget of ~40 refreshes/day. Live spawn metrics may need a push notification fallback rather than poll-based refresh. Park this for slice 11f.

5. **Permission scope split for `/message` and `/interrupt`.** Currently `/spawn/{id}/stop` requires `ScopeAgentSpawn` on mobile. Should follow-up messages need a separate scope (e.g. `ScopeAgentMessage`)? **Recommendation:** reuse `ScopeAgentSpawn` for now; split later if granularity matters.

---

## 7. Quality gates per slice

Every slice ships with:
- Go: `go build ./... && go test ./internal/hud/... ./internal/spawn/... ./internal/hud/bridge/... ./internal/hud/domain/spawn/... ./internal/hud/domain/mobile/...`
- TypeScript driver: `cd tools/spawn-driver && npm run typecheck && npm run build && npm run smoke:claude && npm run smoke:codex`
- Frontend (Track C): `cd internal/hud/frontend && pnpm typecheck && pnpm build && pnpm test`
- iOS (Track D): `xcodebuild -scheme LoomCompanion -sdk iphonesimulator -destination 'platform=iOS Simulator,name=iPhone 17 Pro,OS=26.2' build`
- Bundle sync after any TS driver change: `make sync-spawn-driver`

---

## 8. Out of scope for Phase 3

- Gemini SDK driver (no public Node SDK exists at parity with Claude/Codex). Stays as legacy CLI path.
- Multi-agent orchestration (one HUD spawn → multiple agent processes). Single-agent only.
- Per-spawn persistent vector DB context. Use existing agent-context recall.
- Voice / audio interaction with the agent (deferred to Phase 4).
