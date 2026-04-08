# Phase 4 Plan: Headless Agent UX Parity — Spawn Detail, Telemetry View, Multi-turn Follow-ups

**Date:** 2026-04-07
**Predecessors:**
- `.loom/82-plan-headless-agent-fullstack-2026-04-07.md` (parent plan)
- `.loom/79-research-headless-agent-telemetry-sdk-2026-04-06.md`
- `.loom/80-product-spec-headless-agent-telemetry-sdk-2026-04-06.md`
- `.loom/81-implementation-plan-headless-agent-telemetry-sdk-2026-04-06.md`

**Goal (carried from user):** Fully drive headless Codex and Claude agents via the web HUD and mobile app, with all session telemetry properly mapped from each CLI's internal model to Loom's canonical model.

---

## 1. What landed this cycle (Phase 3)

Verified via `git log --oneline`:

| Commit | Slice | Description |
|---|---|---|
| `4d5596cf` | 9a | Claude `mcp_tool_use` server_name capture |
| `630d51ed` | 9b | Codex `cached_input_tokens` → `CacheReadTokens` |
| `042fb50c` | 9c | Claude `result.permission_denials[]` → `Errors[{type:permission_denied}]` |
| `686138c3` | 8a | Spawn driver control-file loop (Claude async-iterable input, Codex re-invoke) |
| `4092a6b7` | 8b | Orchestrator `MultiTurn` wiring + `injectControlMessage()` helper |
| `a8108bf1` | 8c | REST endpoints: admin `/spawn/{id}/{message,interrupt}` + mobile equivalents |
| `7be2a339` | — | CI: exclude `internal/spawn` from fi-accel-gated race test regex |
| `fdea8067` | — | `fix(skills): atomic SKILL.md writes` — closes false codex frontmatter warnings (openai/codex#11495) |

**Net result:** the Loom platform can now (a) spawn headless Claude/Codex agents via SDK drivers, (b) mirror their internal data models into the canonical `SpawnTelemetry` with MCP server attribution, Codex cache parity, and permission-denial surfacing, and (c) send follow-up messages / interrupts via REST. **All the backend capability exists. The UI has none of it.**

---

## 2. The remaining gap (what "fully drive" still needs)

Cross-checked against the actual source tree as of `09a53b89`:

### Web HUD (`internal/hud/frontend/src/`)

- `lib/components/SpawnPanel.svelte:19` — the `agentType` picker still **disables Codex and Gemini** (`<option value="codex" disabled>`). Shipped Codex support is unreachable from the web UI.
- `SpawnPanel.svelte:122` — spawn rows render status/duration/stop button only. No click-through, no telemetry view, no cost, no turn count, no tool-call log, no budget bar.
- `routes/spawns/...` does **not exist** — the frontend is plain Svelte+Vite (no SvelteKit), routing goes through the hash router in `lib/stores/router.svelte.ts`. The router already supports three-segment hashes (`#view/subView/detail`) via `router.detail`, so the spawn detail surface is a new panel rendered when `router.subView === 'spawn' && router.detail`.
- No SSE subscription for spawn events from the web UI. The `/api/mobile/v1/agent/spawn/{id}/stream` SSE route exists but has no admin-token mirror and no web client.

### iOS companion (`apps/loom-companion-ios/`)

- `Sources/LoomCompanionKit/Networking/Endpoint.swift:34-38` declares `spawnAgent/spawnList/spawnConfig/spawnDetail/spawnStop` — **zero telemetry endpoints**.
- `Sources/LoomCompanionKit/Models/SpawnModels.swift` has no `SpawnTelemetry` / `ToolCallEntry` / `FileChangeEntry` / `AgentError` types.
- `Views/Spawn/SpawnAgentView.swift` only shows status + elapsed. `ViewModels/SpawnViewModel.swift:1` has SSE wiring for lifecycle events but no `agent.spawn.telemetry.*` handlers.
- No `SpawnDetailView.swift` at all.
- No follow-up message UI.

### Telemetry loose ends

- **Codex cost** — `SpawnTelemetry.TotalCostUSD` is 0 for all Codex spawns because the SDK doesn't emit cost. The plan-82 decision is to ship an in-Loom `EstimateCost(model, usage)` with a hard-coded price table and a `cost_estimated:true` flag.
- **Live partial-message streaming** — `claude-driver.ts` still sets `includePartialMessages:false`. No typing-indicator path.
- **Telemetry delta SSE** — full telemetry snapshot is rebroadcast on every change, but there's no `agent.spawn.telemetry.delta` event, so web/iOS clients have to re-fetch.

---

## 3. Slice batch for Phase 4

Sequenced for shipability. Each row is one commit.

### Batch 4A — Web HUD parity (**highest user-visible value**)

| Slice | Files | Acceptance |
|---|---|---|
| **13a** Enable Codex in web spawn picker; add UX for multi-turn toggle on create form. | `SpawnPanel.svelte:19` (remove `disabled`), add `multiTurn` checkbox, post `multi_turn:true` in `SpawnRequest`. Add `MultiTurn` to `lib/stores/spawn.svelte.ts`'s `SpawnRequest` type. | Operator can create a Codex spawn and a multi-turn Claude spawn from the web UI. |
| **13b** Spawn detail panel skeleton. New `SpawnDetailPanel.svelte`; render when `router.detail` is set within `sandbox/spawn`; click a spawn row → `router.navigateDetail(spawn.spawn_id)`. Panel fetches `/api/agent/spawn/{id}` + `/api/agent/spawn/{id}/telemetry` (already exists) on mount. | `SpawnPanel.svelte`, new `SpawnDetailPanel.svelte`, `App.svelte:22` import, router guard. | Clicking a spawn opens a detail view with ExternalSessionID, turn count, cost, last message, stop button. |
| **13c** Telemetry tabs (Tools / Files / Errors / Usage). Four sub-components under `lib/components/SpawnTelemetry/`. Each fetches the existing paginated `/telemetry/{tools|files|errors}` endpoint. Usage tab renders `TokenUsage` + `ModelUsage` as a stacked bar. | `lib/components/SpawnTelemetry/{ToolsTab,FilesTab,ErrorsTab,UsageTab}.svelte` (new), `SpawnDetailPanel.svelte` tab strip. | All telemetry the backend records is visible in the HUD. |
| **13d** Cost & budget UI on list and detail. Show `total_cost_usd / max_cost_usd` progress bar on spawn rows; red past 80%. Surface `num_turns / max_turns` alongside. | `SpawnPanel.svelte`, `SpawnDetailPanel.svelte`, `widgets/BudgetBar.svelte` (new, reusable). | Budgets visible at a glance; users can see when an agent is about to hit its cap. |
| **13e** SSE event subscription on detail page. Add admin-token-gated `/api/agent/spawn/{id}/stream` that mirrors the mobile SSE; subscribe via `EventSource` in detail panel; render a scrolling activity feed of `agent.spawn.tool_start/tool_complete/message/thinking` events. | `internal/hud/domain/spawn/handlers.go` (new handler + route), `lib/stores/spawnStream.svelte.ts` (new), detail panel. | Live tool call / message / thinking events stream into the detail view. |
| **13f** Multi-turn input on detail page. Textarea + Send, Interrupt button. Gated on `spawn.multi_turn === true`. POSTs to `/api/agent/spawn/{id}/message` and `/api/agent/spawn/{id}/interrupt` (shipped in 8c). | `SpawnDetailPanel.svelte`, `lib/stores/spawn.svelte.ts` (add `sendMessage`, `interrupt`). | Operator can follow up mid-run from the web HUD. |

### Batch 4B — iOS companion parity

| Slice | Files | Acceptance |
|---|---|---|
| **14a** `SpawnTelemetry` decodables. Mirror `bridge/spawn_telemetry.go` as Swift `Codable` in `LoomCompanionKit/Models/SpawnTelemetryModels.swift`. | new file | Decoding unit test against a captured JSON snapshot. |
| **14b** API client methods. Add `spawnTelemetry`, `spawnTelemetryTools/Files/Errors`, `spawnSendMessage`, `spawnInterrupt` cases to `Endpoint.swift` and client methods in `APIClient.swift`. | `Networking/Endpoint.swift:34`, `Networking/APIClient.swift` | Client can fetch telemetry + post follow-ups. |
| **14c** `SpawnDetailView.swift` — tab-based detail (Tools, Files, Errors, Usage, Activity). Reached by tapping a row in `SpawnAgentView`. Uses `TabView(selection:)` + four list sections. | new `Views/Spawn/SpawnDetailView.swift`, `SpawnAgentView` row button. | Telemetry visible on device. |
| **14d** Live SSE stream in detail. Extend `SpawnViewModel` to surface `agent.spawn.tool_start/tool_complete/message/thinking` events; render a scrolling list. | `ViewModels/SpawnViewModel.swift` | Live activity log on iOS. |
| **14e** Multi-turn follow-up sheet + interrupt button. `.sheet(isPresented:)` with text field + send; toolbar Interrupt. Gated on `spawn.multi_turn`. | `SpawnDetailView.swift` | Mobile parity with web. |
| **14f** Budget chips + cost on list + widget. Update `SpawnRow`, `WidgetData` models, and `LoomCompanionWidget` timeline provider to include `total_cost_usd` and `turn_count`. | `SpawnModels.swift`, `WidgetData.swift`, widget source | Home-screen widget shows live cost for the latest active spawn. |

### Batch 4C — Telemetry polish

| Slice | Files | Acceptance |
|---|---|---|
| **15a** Codex `EstimateCost(model, usage)` price table. New `internal/hud/bridge/spawn_pricing.go` with a const map of model → (input, cached_input, output) per-1M USD; wire into `spawn_codex_parser.go` `handleTurnCompleted` to set `TotalCostUSD` non-zero. Add `CostEstimated bool` to `SpawnTelemetry`. Unit tests with a price table golden. | `internal/hud/bridge/spawn_pricing.go` (new), `internal/hud/spawn_codex_parser.go`, `internal/hud/bridge/spawn_telemetry.go` (new field) | Codex spawns report a non-zero estimated cost with the `cost_estimated` flag set. |
| **15b** `agent.spawn.telemetry.delta` SSE event. When telemetry changes (new tool call, file change, cost update), broadcast a slim diff `{type:"telemetry.delta", fields:{…}}` over the existing SSE channel. Web + iOS clients can apply the diff and avoid re-fetching. | `internal/hud/spawn.go` broadcast site, `internal/hud/domain/mobile/stream_spawn.go` event type. | Detail panels update without re-fetch. |
| **15c** Live partial-message preview (opt-in). Add `Request.LivePreview bool`; when true, driver flips `includePartialMessages:true` and forwards partials as `agent.spawn.partial_message` SSE events. Detail panel renders a "typing…" indicator. | `tools/spawn-driver/src/claude-driver.ts`, `internal/spawn/types.go`, detail panels. | Optional live-preview toggle in create form. |

### Batch 4D — Robustness / ops

| Slice | Files | Acceptance |
|---|---|---|
| **16a** Spawn TTL pruning. `internal/spawn/state.go` prunes terminal spawns older than `Config.RetentionHours` (default 168) on every persistence write. `?include_archived=true` query param on list endpoints. | `internal/spawn/state.go`, handlers | State files bounded. |
| **16b** Concurrent spawn quota. `Config.MaxConcurrentSpawns` enforced in `SpawnOrchestrator.Spawn()`; returns `429 Too Many Spawns` with the active count. | `internal/spawn/orchestrator.go`, handlers | Runaway pod creation blocked. |
| **16c** Pre-spawn cost estimate. `POST /api/agent/spawn` response includes `estimated_cost_usd_range` using `EstimateCost` × (min_tokens, max_tokens) from agent type config. UI shows "est. $0.42–$1.05". | handlers, `spawn_pricing.go`, UI. | Operators see a cost preview before committing. |
| **16d** Prometheus dashboard JSON. `platform/gitops/monitoring/dashboards/loom-spawn.json` covering `loom_spawn_total`, `loom_spawn_duration_seconds`, cost histograms, per-agent-type tool-call counts. | dashboard file | Grafana dashboard deployed via Flux. |

---

## 4. Sequencing recommendation

The highest-leverage batch is **4A (Web HUD parity)** — it surfaces all the telemetry that Phase 3 already records. After 4A ships the platform is "usable". Then 4B brings iOS to parity. 4C/4D are polish that can interleave.

Suggested parallel-slice-ship waves:

**Wave 1 (can run in parallel — independent files):**
- 13a (SpawnPanel picker enable) — tiny
- 13b (SpawnDetailPanel skeleton) — medium
- 14a (iOS SpawnTelemetry decodables) — tiny
- 15a (Codex EstimateCost) — small

**Wave 2 (depends on 13b):**
- 13c (telemetry tabs)
- 13d (budget bars)
- 14b (iOS API client methods)

**Wave 3 (depends on 13c/14b):**
- 13e (web SSE subscription)
- 14c (iOS detail view)
- 15b (telemetry delta SSE)

**Wave 4 (multi-turn UI, depends on waves 1-3):**
- 13f (web follow-up input)
- 14d (iOS live stream)
- 14e (iOS follow-up sheet)

**Wave 5 (polish):**
- 14f (iOS widget + cost)
- 15c (live partial messages)
- 16a-d (robustness)

Wave 1 is the recommended immediate next batch for a `parallel-slice-ship` run.

---

## 5. Canonical mapping table (updated)

Rows that moved from ❌/⚠️ to ✅ this cycle are **bold**.

### Claude Code → `SpawnTelemetry`

| SDK field | Canonical field | Status |
|---|---|---|
| `system.session_id` | `ExternalSessionID` | ✅ |
| `assistant.message.usage.*` | `TokenUsage.*` | ✅ |
| `assistant.content[].tool_use` | `ToolCalls[]` | ✅ |
| **`assistant.content[].mcp_tool_use.server_name`** | **`ToolCalls[].ServerName`** | ✅ (9a / `4d5596cf`) |
| `user.content[].tool_result` | `ToolCalls[]` completion | ✅ |
| `assistant.content[].text` (Write/Edit) | `FileChanges[]` | ✅ |
| `assistant.content[].text` | `LastMessage` | ✅ |
| `assistant.content[].thinking` | SSE only | ✅ |
| `result.total_cost_usd` | `TotalCostUSD` | ✅ |
| `result.num_turns` | `TurnCount` | ✅ |
| `result.subtype` | `StopReason` | ✅ |
| `result.usage.modelUsage{}` | `ModelUsage` | ✅ |
| `system.subtype=api_retry` | `Errors[]{rate_limit}` | ✅ |
| **`result.permission_denials[]`** | **`Errors[]{permission_denied}`** | ✅ (9c / `042fb50c`) |
| `stream_event.partial_message` | live partials | ❌ (slice 15c) |

### Codex CLI → `SpawnTelemetry`

| SDK field | Canonical field | Status |
|---|---|---|
| `thread.started.thread_id` | `ExternalSessionID` | ✅ |
| `turn.started` | `TurnCount` | ✅ |
| `turn.completed.usage.input_tokens` | `TokenUsage.InputTokens` | ✅ |
| `turn.completed.usage.output_tokens` | `TokenUsage.OutputTokens` | ✅ |
| **`turn.completed.usage.cached_input_tokens`** | **`TokenUsage.CacheReadTokens`** | ✅ (9b / `630d51ed`) |
| `item.completed.command_execution` | `ToolCalls[]` | ✅ (compat layer) |
| `item.completed.mcp_tool_call` | `ToolCalls[]` | ✅ (compat layer) |
| `item.completed.file_change` | `FileChanges[]` | ✅ |
| `item.completed.agent_message` | `LastMessage` | ✅ |
| `item.completed.todo_list.items[]` | SSE only | ✅ (compat layer) |
| `item.completed.reasoning` | SSE only | ✅ |
| `turn.failed` | `Errors[]{execution}` | ✅ |
| `error` | `Errors[]{fatal}` | ✅ |
| **per-turn cost** | **`TotalCostUSD`** | ❌ (slice 15a — EstimateCost) |
| **`item.mcp_tool_call.server`** | **`ToolCalls[].ServerName`** | ⚠️ — slice 9a landed for Claude only; needs symmetric Codex handling. New slice **9a' Codex server_name** added below. |
| `turn.completed.usage.cache_creation_input_tokens` | `CacheCreationTokens` | ❌ SDK doesn't emit; record 0 |

**New micro-slice:** **9a' — Codex MCP server_name capture.** While 9a ('4d5596cf') handled Claude `mcp_tool_use.server_name`, the Codex side still drops the server. Check `internal/hud/spawn_codex_parser.go` `handleItemStarted` + the `transformItem()` compat layer in `tools/spawn-driver/src/codex-driver.ts` — propagate `mcp_tool_call.server` onto `ToolCallEntry.ServerName`. Tiny. Should ship alongside wave 1 of 4A.

---

## 6. Open decisions

1. **Cost currency unit.** Use float USD everywhere (current). Revisit once we add multi-currency.
2. **Budget-exceed SSE event.** When a spawn hits a budget cap, broadcast a discrete `agent.spawn.budget_exceeded` event so the UI can flash red before the process exits. **Recommendation:** ship as part of 15b (delta event is the right carrier).
3. **Detail panel refresh cadence.** SSE deltas (15b) replace polling, but what if SSE disconnects? **Recommendation:** detail panel falls back to 5s polling when SSE is unhealthy, same pattern as `spawn.svelte.ts::startPolling`.
4. **iOS widget push vs. poll.** Widgets get ~40 refreshes/day. For cost tracking this is plenty if we refresh only on spawn state change. **Recommendation:** push silent APNs on spawn state transitions rather than periodic poll. Defer to 14f.
5. **Admin-token SSE endpoint.** Slice 13e adds `/api/agent/spawn/{id}/stream` — should it reuse the mobile SSE handler (`internal/hud/domain/mobile/stream_spawn.go`) or live in `domain/spawn/handlers.go`? **Recommendation:** factor out the SSE implementation into `internal/hud/bridge/spawn_sse.go` so both domains share it.

---

## 7. Quality gates per slice

Every slice ships with:

- **Go:** `go build ./... && go test ./internal/hud/... ./internal/spawn/... ./internal/hud/bridge/... ./internal/hud/domain/spawn/... ./internal/hud/domain/mobile/...`
- **TypeScript driver** (for 15a/15c): `cd tools/spawn-driver && npm run typecheck && npm run build && npm run smoke:claude && npm run smoke:codex`
- **Web frontend** (4A): `cd internal/hud/frontend && pnpm typecheck && pnpm build && pnpm test`
- **iOS** (4B): `xcodebuild -project apps/loom-companion-ios/LoomCompanion.xcodeproj -scheme LoomCompanion -sdk iphonesimulator -destination 'platform=iOS Simulator,name=iPhone 17 Pro,OS=26.2' build`
- **Bundle sync** after any TS driver change: `make sync-spawn-driver`
- **iOS project sync** after any new Swift file: `make mobile-ios-project-sync`

---

## 8. Out of scope for Phase 4

- Gemini SDK driver (no public Node SDK).
- Multi-agent orchestration (one HUD spawn → N processes).
- Voice / audio interaction (Phase 5).
- Fine-grained per-tool cost attribution (requires SDK-level changes upstream).
- Persistent vector DB per spawn (use agent-context).
