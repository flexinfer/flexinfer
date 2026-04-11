# Implementation Plan: HUD Labs — Prime Time

**Date**: 2026-04-10
**Goal**: Make the HUD Labs surface (Spawn + Sandbox) operationally trustworthy and fully functional.

## Current State Assessment

The recent commits (`2880f257`, `cc20a5f6`, `ed093de4`) landed the foundational wiring:
- Auth model (`labsAuth` store, `LabsAccessBar`, `adminFetch`, `X-Admin-Token`) fully operational.
- Spawn CRUD (create, list, detail, stop, message, interrupt) wired end-to-end with auth.
- Spawn telemetry (tools/files/errors/usage tabs) paginated and auth-protected.
- Sandbox summary/capabilities/policy reads working.
- Sandbox start/stop/exec wired through `adminFetch`.
- SSE events for spawn lifecycle + telemetry delta + sandbox snapshots subscribed.
- Spawn config handler enriched (active count, hints, follow-up/interrupt support).

**Uncommitted WIP** (in working tree, ready to commit):
- `spawn/handlers.go` + `spawn_test.go`: Config handler enrichment with `active_spawn_count`, `reason`, `hint`.
- `SpawnPanel.svelte` + `spawn.svelte.ts`: Frontend consuming new config notes.
- `k8s_sync.go` + `k8s_sync_test.go`: Exclude list refactor (glob support).
- `devbox-sync.sh` + `mount-devbox-nfs.sh`: NFS coordinate update.

## Remaining Gaps (Prioritized)

### P0 — Contract Bugs (must fix)

| # | Issue | Impact | Files |
|---|-------|--------|-------|
| 1 | `completedSpawns` getter includes `building` spawns | Completed count inflated during builds | `spawn.svelte.ts:118-120` |
| 2 | No token validation feedback | User enters bad token → opaque 401 errors, no indication | `labsAuth.svelte.ts`, `LabsAccessBar.svelte` |
| 3 | SpawnDetailPanel doesn't refresh on all SSE events | Detail view goes stale while list updates | `SpawnDetailPanel.svelte` |

### P1 — Operational Trust (important for "prime time")

| # | Issue | Impact | Files |
|---|-------|--------|-------|
| 4 | No confirmation dialogs for destructive actions | Accidental stop of running spawn/sandbox | `SpawnPanel.svelte`, `SpawnDetailPanel.svelte`, `SandboxPanel.svelte` |
| 5 | No error recovery UX | Failed requests show static text, no retry | All Labs panels |
| 6 | No spawn output / conversation view | Operators can't see what the agent is doing | `SpawnDetailPanel.svelte` (new tab or inline) |
| 7 | No spawn list filtering/sorting | Hard to find spawns in a busy list | `SpawnPanel.svelte`, `spawn.svelte.ts` |
| 8 | No exec cancel | No way to abort a queued sandbox exec | `SandboxPanel.svelte`, `sandbox/handlers.go` |

### P2 — Feature Completion (nice-to-have for v1)

| # | Issue | Impact | Notes |
|---|-------|--------|-------|
| 9 | No Gemini spawn telemetry | Gemini spawns show empty telemetry | Needs parser in `spawn_gemini_parser.go` |
| 10 | No file diff stats in telemetry | Files tab only shows path + kind | Backend needs to emit `lines_added`/`lines_removed` |
| 11 | No spawn cleanup/delete | Terminal spawns accumulate | Needs REST endpoint + UI |
| 12 | No sandbox per-project status | Single global snapshot | Needs backend endpoint |

## Implementation Slices

### Slice 1: Fix Contract Bugs (P0)

**1a. Fix `completedSpawns` getter**

```typescript
// spawn.svelte.ts:118-120
get completedSpawns(): SpawnState[] {
  return this.spawns.filter(s =>
    s.status !== 'creating' && s.status !== 'building' && s.status !== 'running'
  );
}
```

**1b. Add token validation**

Add a `validateToken()` method to `labsAuth.svelte.ts` that makes a lightweight authenticated GET (e.g., `GET /api/agent/spawn/config` doesn't need auth, but `GET /api/agent/spawn/{any}/telemetry` does — or better, add a dedicated `GET /api/labs/auth-check` endpoint that returns 200/401).

Approach: Add `GET /api/labs/auth-check` in the sandbox domain (or a shared labs domain) that runs `RequireAdminToken` and returns `{"valid": true}`. The frontend calls this on token save and shows a green/red indicator on the `LabsAccessBar`.

Backend:
- Add route in a new `internal/hud/domain/labs/` domain or directly in sandbox domain.
- Handler: `RequireAdminToken(w, r)` → if passed, write `{"valid": true}`.

Frontend:
- `labsAuth.svelte.ts`: Add `async validate(): Promise<boolean>` that calls `/api/labs/auth-check`.
- `LabsAccessBar.svelte`: Show validation state (checkmark / X icon) after token entry.

**1c. Make SpawnDetailPanel subscribe to live events**

The detail panel should subscribe to spawn SSE events and refresh when:
- `agent.spawn.building/running/completed/failed/stopped` fires for the viewed spawn ID
- `agent.spawn.message` / `agent.spawn.tool_start` / `agent.spawn.tool_complete` fire (for activity indicator)

In `SpawnDetailPanel.svelte`:
- Subscribe to the event store for spawn lifecycle events matching `router.detail`.
- Call `loadDetail()` on lifecycle transitions.
- Patch telemetry inline on `agent.spawn.telemetry.delta` (already done, but verify it covers all fields).

### Slice 2: Operational Trust (P1)

**2a. Confirmation dialogs**

Add a reusable `ConfirmDialog.svelte` shared component:
```svelte
<ConfirmDialog
  open={showConfirm}
  title="Stop Spawn?"
  message="This will terminate the running agent. This action cannot be undone."
  confirmLabel="Stop"
  confirmVariant="danger"
  onConfirm={doStop}
  onCancel={() => showConfirm = false}
/>
```

Wire into: spawn stop, sandbox stop, spawn interrupt.

**2b. Error recovery (retry buttons)**

Replace static error text with an error card that includes:
- Error message
- Timestamp
- "Retry" button that re-invokes the failed action
- "Dismiss" button

Pattern: wrap `adminFetch` calls in try/catch and set `{ message, retryFn }` on error state.

**2c. Spawn output / activity view**

Add an "Activity" tab to SpawnDetailPanel that renders spawn events in chronological order:
- `agent.spawn.message` → assistant text blocks
- `agent.spawn.tool_start` / `agent.spawn.tool_complete` → tool call cards
- `agent.spawn.thinking` → collapsible thinking blocks
- `agent.spawn.file_change` → file mutation entries
- `agent.spawn.result` → terminal result card

Data source: Accumulate events in `spawnStore.eventsBySpawnId` map (keyed by spawn ID, array of event objects). The SSE event handler already receives these events — just need to store them.

Store changes (`spawn.svelte.ts`):
- Add `eventsBySpawnId: Map<string, SpawnEvent[]>`
- Subscribe to spawn activity event types in the event store
- Push events into the map, capped at 500 per spawn

Component:
- New `ActivityTab.svelte` in `SpawnTelemetry/`
- Add tab to SpawnDetailPanel tab strip

**2d. Spawn list filtering/sorting**

Add filter/sort controls to SpawnPanel:
- Filter by status: All / Active / Completed / Failed
- Filter by agent type: All / Claude / Codex / Gemini
- Sort by: Start time (default), Duration, Cost
- Text search on project name / task description

Store changes: Add `filter` and `sort` state to spawn store, with computed `filteredSpawns` getter.

### Slice 3: Commit Pending Work + Quality Gate

Before implementing new slices, commit the existing WIP:
- Stage spawn handler enrichment + tests
- Stage frontend changes
- Stage devbox sync refactors
- Rebuild frontend dist
- Run quality gate

## Execution Order

1. **Slice 3** first — commit existing WIP to establish a clean baseline.
2. **Slice 1** — fix contract bugs (small, focused, high-impact).
3. **Slice 2a + 2b** — confirmation dialogs + error recovery (UX trust).
4. **Slice 2c** — spawn activity view (the biggest feature gap).
5. **Slice 2d** — spawn list filtering (polish).

## Validation

- `go test ./internal/hud/domain/spawn/... ./internal/hud/domain/sandbox/... -count=1`
- `pnpm build` in `internal/hud/frontend/`
- Manual smoke: spawn launch → building → running → message → stop → detail → telemetry tabs
- Manual smoke: sandbox start → exec → poll → stop → activity feed
- Token validation: enter bad token → see error indicator, enter good token → see success

## Sources

- `internal/spawn/types.go:58` — `StatusPending = "creating"`
- `internal/hud/frontend/src/lib/stores/spawn.svelte.ts:114-120` — active/completed getters
- `internal/hud/frontend/src/lib/stores/labsAuth.svelte.ts` — auth store
- `internal/hud/frontend/src/lib/components/shared/LabsAccessBar.svelte` — token input
- `internal/hud/frontend/src/lib/components/SpawnDetailPanel.svelte:27` — detail panel
- `internal/hud/frontend/src/lib/stores/events.svelte.ts:91` — SSE subscriptions
- `internal/hud/domain/spawn/handlers.go:11` — spawn route registration
- `internal/hud/domain/sandbox/handlers.go:29` — sandbox route registration
