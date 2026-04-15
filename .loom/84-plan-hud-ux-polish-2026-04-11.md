# Plan: HUD UX Polish — Agent Spawning, Session Management, Cross-Cutting Coherence

**Date:** 2026-04-11
**Predecessors:**
- `.loom/31-hud-labs-prime-time-plan.md` (Labs prime-time — mostly shipped)
- `.loom/83-plan-headless-agent-ux-parity-2026-04-07.md` (Phase 4 UX parity — mostly shipped)

**Goal:** Polish the HUD experience across spawn, session, and fleet surfaces so they feel like one coherent operator console rather than three loosely coupled panels.

## Status Update (2026-04-14)

This draft predates the `feat/hud-traces` and `feat/hud-agent-unification` slices that have since landed on `main`.

Already shipped from this plan's gap list:
- P0 gap #1 is substantially resolved: desktop Fleet/Presence/Overview/footer now share one merged live-agent model, and live-agent rows deep-link into `Session` detail and agent-filtered `Traces`.
- P0 gap #2 is now resolved: Fleet exposes root/parent/child session hierarchy in the table and detail drawer instead of flattening spawned subagents into unrelated rows.
- P0 gap #3 and P2 gap #12 are resolved: Overview now participates in the same live-agent/store model instead of showing obviously stale or zeroed counters.
- P1 gap #7 is now resolved: Fleet polling ownership is caller-aware, so panel mount order no longer decides the active interval or shuts down sibling polling.
- The operator drilldown story improved beyond the original draft: the new `Traces` panel gives a direct path from an agent row to recent tool-call timing and status data.

Still relevant from this plan:
- P1 gap #5 Fleet drawer-specific error handling
- P1 gap #6 navigable attention rails
- P2 spawn convenience/persistence follow-ons that remain unshipped

Treat the remaining sections below as historical planning context plus a backlog for the unresolved items.

---

## 1. Current State Summary

### What shipped (Phase 3–4, last 2 weeks)

| Feature | Status |
|---------|--------|
| SpawnPanel: create form, filter/search, budget bars, agent type picker | Done |
| SpawnDetailPanel: status card, telemetry tabs (Activity/Tools/Files/Errors/Usage) | Done |
| Multi-turn follow-up message + interrupt | Done |
| Confirm dialogs, error recovery, token validation | Done |
| Spawn delete endpoint + UI | Done |
| Gemini CLI stream-json telemetry parser | Done |
| Line diff stats in file change telemetry | Done |
| Per-project sandbox status endpoint + UI | Done |
| Codex cost estimation (price table) | Done |
| Telemetry delta SSE (live cost/turn updates) | Done |
| Live spawn activity feed (SSE events) | Done |

### What needs polish

Cross-referenced from codebase audit of `SpawnPanel.svelte`, `SpawnDetailPanel.svelte`, `FleetPanel.svelte`, `PresencePanel.svelte`, `LifecyclePanel.svelte`, `OverviewPanel.svelte`, `fleet.svelte.ts`, `spawn.svelte.ts`.

---

## 2. Gap Analysis (Prioritized)

### P0 — Cross-Cutting Coherence

| # | Gap | Impact | Source |
|---|-----|--------|--------|
| 1 | **Spawn ↔ Session silo**: `spawn.svelte.ts` and `fleet.svelte.ts` share no cross-reference. A spawned agent creates a fleet session, but the two are only linked by `agent_id` — no store helper, no joined UI. | Operator sees "running spawn" in Labs but cannot navigate to its session context in Fleet (decisions, annotations, memory). Reverse is also true: Fleet shows a session but no indication it was spawned headlessly. | `spawn.svelte.ts` has no import of `fleet.svelte.ts`; `FleetPanel.svelte` has no awareness of spawns |
| 2 | **Session hierarchy invisible**: `parent_session_id` and `root_session_id` are carried on the `Session` interface but never surfaced in FleetPanel or any detail view. Subagent nesting is invisible. | Multi-agent workflows (parallel-slice-ship, worktree isolation) spawn child sessions that appear as flat rows indistinguishable from root sessions. | `fleet.svelte.ts:Session` interface, `FleetPanel.svelte` DataTable columns |
| 3 | **Overview data staleness**: KPI data from `/api/kpis` (15s timer) and live store-derived counts (5s fleet poll) can visibly disagree. No staleness indicator on any card. | "Today: 3 sessions" in the ring gauge vs. 5 sessions visible in the table — no way to know which is stale. | `OverviewPanel.svelte` dual data sources |

### P1 — UX Consistency

| # | Gap | Impact | Source |
|---|-----|--------|--------|
| 4 | **PresencePanel has no loading state**: All tab counts show 0 on mount with no skeleton or spinner. FleetPanel uses `skeletonRows`, Spawn uses "Loading spawn detail..." — Presence has nothing. | Feels broken on first load; inconsistent with sibling panels. | `PresencePanel.svelte` tab bar |
| 5 | **FleetPanel detail drawer swallows errors**: `fetchSessionEntries` stores errors into `this.error` (shared with main fleet fetch). A drawer failure briefly shows then clears on next poll. | Error feedback for context entries is unreliable. | `fleet.svelte.ts:fetchSessionEntries`, `FleetPanel.svelte` DetailDrawer |
| 6 | **Attention rail is not navigable**: LifecyclePanel and OverviewPanel show "attention lanes" but clicking does nothing — no navigation to the relevant agent, session, or spawn. | Alerts are informational but not actionable. | `LifecyclePanel.svelte` attention cards, `OverviewPanel.svelte` attention column |
| 7 | **Polling interval inconsistency**: FleetPanel starts fleet at 5s, LifecyclePanel at 30s. Both call `fleetStore.startPolling()` but the last caller wins on interval. | Data freshness depends on which panel was mounted most recently. | `FleetPanel.svelte:$effect`, `LifecyclePanel.svelte:$effect` |

### P2 — Polish & Completeness

| # | Gap | Impact | Source |
|---|-----|--------|--------|
| 8 | **Spawn activity has no persistence**: On page reload all activity events are gone. Capped at 500 per spawn with no "older events truncated" indicator. | Long-running spawns lose history; operators returning to the HUD see empty activity. | `spawn.svelte.ts:activityBySpawnId` |
| 9 | **Spawn row telemetry inline stats**: Spawn list rows show budget bars but no inline token/cost/file-change summary for spawns without budgets set. | For budget-less spawns, the row shows only status + duration + task text — no quick telemetry glance. | `SpawnPanel.svelte:427-448` |
| 10 | **No spawn re-run**: No way to re-launch a completed/failed spawn with the same parameters. | Operators manually re-enter the same task + project + config. | `SpawnPanel.svelte` form, `SpawnDetailPanel.svelte` |
| 11 | **Max cost / max turns not in spawn form**: The spawn form has no budget fields — `max_cost_usd` and `max_turns` only come from config defaults. | Operators cannot set per-spawn budgets from the UI. | `SpawnPanel.svelte:80-87` — only `timeout_minutes` is in the form |
| 12 | **Overview ring gauges ignore store startup**: If Overview is mounted first, all ring values are 0 because stores start in their respective panels. | Dashboard appears empty until you visit Fleet or Presence. | `OverviewPanel.svelte` store dependency |

---

## 3. Implementation Slices

### Slice A: Spawn ↔ Session Bridge (P0, #1)

**Goal:** Let operators navigate between a spawn and its corresponding fleet session, and vice versa.

**Backend:**
- `GET /api/agent/spawn/{id}` response already includes `agent_id`.
- `GET /api/sessions` response already includes `agent_id`.
- No new endpoints needed — this is a frontend join.

**Frontend changes:**

1. **`spawn.svelte.ts`**: Add a `sessionForSpawn(spawnId: string): Session | undefined` helper that looks up the spawn's `agent_id` in `fleetStore.sessions`.

2. **`fleet.svelte.ts`**: Add a `spawnForSession(sessionId: string): SpawnState | undefined` helper that finds a spawn whose `agent_id` matches the session's `agent_id` by importing `spawnStore`.

3. **`SpawnDetailPanel.svelte`**: Below the status card, add a "Session" link chip that navigates to `#agents/fleet` with the session ID in the detail drawer. Show session namespace, entry count, and duration.

4. **`FleetPanel.svelte`**: In the DataTable, add a "Spawned" column icon. In the DetailDrawer, if a session has a matching spawn, show a "View Spawn" link that navigates to `#sandbox/spawn/{spawnId}`.

**Files:** `spawn.svelte.ts`, `fleet.svelte.ts`, `SpawnDetailPanel.svelte`, `FleetPanel.svelte`

### Slice B: Session Hierarchy (P0, #2)

**Goal:** Surface parent-child session relationships in the Fleet table and detail drawer.

1. **`fleet.svelte.ts`**: Add `sessionTree` getter that groups sessions by `root_session_id`. Add `childSessions(sessionId)` and `parentSession(sessionId)` helpers.

2. **`FleetPanel.svelte`**:
   - Add a "depth" indicator to session rows (indent or badge showing `root > parent > child`).
   - In the sort/filter bar, add a "Group by root session" toggle.
   - In the DetailDrawer, show the session tree as a breadcrumb: `Root Session > Parent > Current`.

**Files:** `fleet.svelte.ts`, `FleetPanel.svelte`

### Slice C: Overview Data Coherence (P0, #3 + P2, #12)

**Goal:** Overview panel starts its own store polling and shows consistent, fresh data.

1. **`OverviewPanel.svelte`**: Start `fleetStore.startPolling(10000)` and `healthStore` from the Overview `$effect` rather than relying on other panels.

2. Replace the dual KPI/store pattern: use store-derived values for the ring gauges instead of `/api/kpis` (or clearly label the KPI values with a "last updated" timestamp).

3. Add a subtle "Last refreshed: Xs ago" footer to the signal strip.

**Files:** `OverviewPanel.svelte`

### Slice D: Presence Loading States (P1, #4)

**Goal:** Consistent loading experience across all panels.

1. **`PresencePanel.svelte`**: Add a loading state to the tab bar. When `presenceStore` has not yet completed its first fetch, show skeleton placeholders in the count badges.

2. **`PresenceAgentsTab.svelte`**: Show skeleton card/table during initial load.

3. Match the pattern from `FleetPanel.svelte`'s `skeletonRows` approach.

**Files:** `PresencePanel.svelte`, `presence/PresenceAgentsTab.svelte`

### Slice E: Fleet Detail Drawer Error Handling (P1, #5)

**Goal:** Context entry fetch errors show inline in the drawer, not in the main fleet error surface.

1. **`fleet.svelte.ts`**: Add a separate `drawerError` state that `fetchSessionEntries` writes to instead of the shared `error` field.

2. **`FleetPanel.svelte`**: Render `drawerError` inline in the DetailDrawer with a retry button.

**Files:** `fleet.svelte.ts`, `FleetPanel.svelte`

### Slice F: Navigable Attention Rails (P1, #6)

**Goal:** Attention lane cards navigate to the relevant agent/session when clicked.

1. **`OverviewPanel.svelte`**: Each attention card gets an `onclick` that calls `router.navigate('agents', 'fleet')` and opens the agent's detail drawer.

2. **`LifecyclePanel.svelte`**: Same pattern — clicking an attention lane scrolls the swim lane to that agent and highlights it.

**Files:** `OverviewPanel.svelte`, `LifecyclePanel.svelte`, `router.svelte.ts` (may need a `navigateWithDetail` helper)

### Slice G: Polling Interval Normalization (P1, #7)

**Goal:** Consistent data freshness regardless of which panels are mounted.

1. **`fleet.svelte.ts`**: Track the minimum requested interval across all callers. `startPolling(interval)` sets the timer to `min(current, new)`. `stopPolling()` removes a caller and resets to the max remaining interval (or stops if no callers).

2. Alternative simpler approach: standardize all panels to 10s for fleet, document the convention.

**Files:** `fleet.svelte.ts`, `FleetPanel.svelte`, `LifecyclePanel.svelte`

### Slice H: Spawn Form Budget Fields (P2, #11)

**Goal:** Let operators set per-spawn cost and turn budgets from the create form.

1. **`SpawnPanel.svelte`**: Add optional "Max Cost (USD)" and "Max Turns" number inputs below the timeout field. Pre-fill from config defaults when available. Wire into the `SpawnRequest`.

**Files:** `SpawnPanel.svelte`

### Slice I: Spawn Row Inline Telemetry (P2, #9)

**Goal:** Every spawn row shows a quick telemetry glance even without budget bars.

1. **`SpawnPanel.svelte`**: Below the task text, show inline chips: `$0.0234` cost, `12 turns`, `3 files`, `2 tools` — using `rowTelemetry()` data.

**Files:** `SpawnPanel.svelte`

### Slice J: Spawn Re-run (P2, #10)

**Goal:** One-click re-launch of a completed/failed spawn with the same parameters.

1. **`SpawnDetailPanel.svelte`**: Add a "Re-run" button in the actions row for terminal spawns. Clicking it populates the spawn form with the original request and navigates back to the SpawnPanel.

2. **`spawn.svelte.ts`**: Add a `prefill` state that the SpawnPanel reads on mount to pre-populate the form.

**Files:** `SpawnDetailPanel.svelte`, `SpawnPanel.svelte`, `spawn.svelte.ts`

### Slice K: Spawn Activity Persistence (P2, #8)

**Goal:** Activity events survive page reload for recent spawns.

1. **Backend**: `GET /api/agent/spawn/{id}/telemetry/activity` endpoint that returns persisted activity events from the spawn telemetry (already persisted in `spawn_persist.go` via `completeSpawn`).

2. **Frontend**: `SpawnDetailPanel.svelte` / `ActivityTab.svelte` loads persisted activity on mount, then appends live SSE events.

3. Show "Showing last 500 events" indicator when the buffer is full.

**Files:** `spawn/handlers.go`, `ActivityTab.svelte`, `spawn.svelte.ts`

---

## 4. Sequencing

**Wave 1 — Highest impact, independent slices:**
- **Slice A** (spawn ↔ session bridge) — the single biggest coherence win
- **Slice C** (overview data coherence) — fixes the "empty dashboard" first-impression
- **Slice H** (budget fields in form) — small, high-value

**Wave 2 — Consistency sweep:**
- **Slice D** (presence loading states)
- **Slice E** (drawer error handling)
- **Slice G** (polling normalization)

**Wave 3 — Navigation + inline richness:**
- **Slice B** (session hierarchy)
- **Slice F** (navigable attention rails)
- **Slice I** (spawn row inline telemetry)

**Wave 4 — Operator convenience:**
- **Slice J** (spawn re-run)
- **Slice K** (activity persistence)

---

## 5. Quality Gates

Every slice ships with:

- `cd internal/hud/frontend && pnpm typecheck && pnpm build`
- `go build ./... && go test ./internal/hud/... -count=1` (for slices with Go changes)
- Manual smoke: navigate between spawn detail → fleet session → back; verify data consistency
- Visual regression: compare spawn list, detail, fleet, presence, overview before/after

---

## 6. Out of Scope

- iOS companion parity for these changes (separate cycle)
- Gemini SDK driver (no public Node SDK)
- Multi-agent orchestration (one spawn → N processes)
- Performance optimization of `namespaceGroups` getter (premature; profile first)
- ShuttlePanel / AlertsPanel / ContextHealthPanel router wiring (separate concern)
