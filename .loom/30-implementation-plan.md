# Implementation Plan: HUD UI/UX Overhaul

## Scope

Refactor the HUD frontend into a cohesive dashboard with a formal design system, grouped navigation (13 panels -> 6 views), consistent interaction patterns, accessibility compliance, and performance optimization.

## Milestones

| Milestone | Description | Depends On | Status |
|-----------|-------------|------------|--------|
| **M1** | Design system foundation (tokens, primitives) | — | ✅ Complete |
| **M2** | Navigation restructure (grouped views) | M1 | ✅ Complete |
| **M3** | Interaction patterns (drawer, filters, bulk, confirm) | M1 | ~80% — bulk actions remaining |
| **M4** | Accessibility + performance hardening | M1, M2 | ~85% — VirtualList, lazy load remaining |

## Plan

### M1: Design System Foundation ✅ COMPLETE

#### 1.1 Formalize token system

**Files**: `theme.css`, new `tokens.ts`

- Define type scale: `--text-xs` (10px), `--text-sm` (12px), `--text-base` (14px), `--text-lg` (16px), `--text-xl` (20px), `--text-2xl` (24px).
- Define spacing scale: `--space-1` (4px), `--space-2` (8px), `--space-3` (12px), `--space-4` (16px), `--space-6` (24px), `--space-8` (32px), `--space-12` (48px).
- Define elevation scale: `--elevation-1` (card), `--elevation-2` (dropdown/drawer), `--elevation-3` (modal/overlay).
- Export constants in `tokens.ts` for use in JS (animation durations, breakpoints, polling intervals).
- Replace all hardcoded px values in `theme.css` and `layout.css` with token references.

Source: `theme.css` (current ad-hoc tokens), `layout.css` (hardcoded values).

#### 1.2 Create shared primitive components

**New files** in `src/lib/components/shared/`:

| Component | Purpose | Replaces |
|-----------|---------|----------|
| `PanelShell.svelte` | Standard panel wrapper (header + filter bar + content + empty state) | Ad-hoc `<section>` in each panel |
| `DataTable.svelte` | Sortable, selectable table with sticky header, virtual scroll, skeleton loading | Custom `<table>` in Fleet/Servers/Tasks |
| `FilterBar.svelte` | Search input + filter dropdowns + result count | Custom filter UIs per panel |
| `DetailDrawer.svelte` | Slide-in panel from right for drill-down | FleetPanel inline detail, ServersPanel footer |
| `EmptyState.svelte` | Icon + message + CTA button | Inconsistent empty states |
| `ConfirmAction.svelte` | Destructive action confirmation dialog | Missing (actions are fire-and-forget) |
| `MetricCard.svelte` | Stat display with label, value, trend sparkline, optional badge | Duplicated metric displays |

#### 1.3 Centralize utility functions

**New file**: `src/lib/utils/format.ts`

- `formatTime(date)` — HH:MM:SS
- `relativeTime(date)` — "2m ago", "1h ago"
- `formatNumber(n)` — locale-aware with abbreviation (1.2k, 3.4M)
- `truncateId(id, len)` — first N chars with ellipsis
- `truncatePath(path, maxLen)` — smart path truncation (keep filename)
- `statusColor(status)` — map status string to CSS variable
- `entryIcon(type)` — map entry type to icon character

Source: duplicated functions in FleetPanel (formatTime, relativeTime), PresencePanel (truncateId), TimelinePanel (formatTime).

#### 1.4 Verify: no visual regression

- Run `npm run build` and confirm bundle compiles.
- Open each existing panel in browser and verify appearance matches pre-refactor.
- No behavioral changes in this milestone.

---

### M2: Navigation Restructure ✅ COMPLETE

#### 2.1 Implement grouped view routing

**Files**: `App.svelte`, `router.svelte.ts`

- Change hash format: `#fleet` -> `#agents/fleet`, `#servers` -> `#infra/servers`, etc.
- Add redirect map for old hashes (backward compat).
- Router state: `{ view: string, subView: string }`.

View grouping:

```
agents/     -> fleet (default), presence, topology, lifecycle
infra/      -> servers (default)
tasks/      -> tasks (default), workflows
knowledge/  -> memory (default), graph, reasoning
activity/   -> timeline (default), stream
sandbox/    -> sandbox (default)
overview    -> overview (standalone, no sub-views)
```

#### 2.2 Implement view shell with sub-navigation

**New file**: `src/lib/components/ViewShell.svelte`

- Renders top-level view container with sub-tab bar.
- Sub-tabs styled as segmented control (compact, inline).
- Keyboard: `a`-`d` keys switch sub-views within current view.
- Preserves panel content as child components (no rewrite of panel internals yet).

#### 2.3 Update App.svelte navigation bar

- Reduce nav tabs from 13 to 7 (6 views + overview).
- Each tab shows view label + active sub-view indicator.
- Badge counts on view tabs (e.g., "Tasks (3)" for pending count).
- Update keyboard shortcut help overlay.

#### 2.4 Update overlay mode

- OverlayShell continues to work with stores directly.
- Add compact sub-view switching for overlay (swipe or arrow keys).

---

### M3: Interaction Patterns (~80% Complete)

#### 3.1 DetailDrawer integration ✅ COMPLETE

- ~~Wire `DetailDrawer` into views that have drill-down data:~~
  - ✅ Agents/Fleet: session detail (entries, tokens, tasks)
  - ✅ Infra/Servers: server detail (status, tools, latency sparkline, categories, errors)
  - ✅ Tasks: task detail (context, file, tags, blocked_by, resolution)
  - ✅ Knowledge/Memory: memory item detail (content, importance, tokens, promote/demote actions)
  - ✅ Knowledge/Graph: entity detail (properties, inbound/outbound relations)
- ✅ Drawer opens on row click, closes on Escape or outside click.
- ✅ Drawer width: 400px default.

#### 3.2 FilterBar integration ✅ MOSTLY COMPLETE

- ~~Replace per-panel custom filters with shared `FilterBar`:~~
  - ✅ Infra/Servers: search by name
  - ✅ Tasks: search by subject, filter by status/priority
  - ✅ Knowledge/Memory: search by content, filter by tier/importance/category
  - ✅ Knowledge/Graph: search by entity name, filter by type
  - Remaining: Agents/Fleet filter by status, Activity/Timeline filter by time range

#### 3.3 DataTable integration ✅ MOSTLY COMPLETE

- ~~Replace custom `<table>` implementations with `DataTable`:~~
  - ✅ Sortable columns (click header to sort, `aria-sort` included)
  - ✅ Skeleton loading rows during fetch
  - ✅ Expandable rows with detail content
  - ✅ Row click handlers for drill-down
  - ✅ Used in Fleet, Tasks, Servers, Memory panels
  - Remaining: row selection (checkbox column, shift+click range select) — infrastructure exists (`selectable` prop) but no panel uses it
  - Remaining: virtual scrolling for >50 rows (VirtualList component exists, not integrated)

#### 3.4 Bulk actions toolbar

- When rows are selected, show floating toolbar above table:
  - Tasks: "Complete", "Delete", "Assign to agent"
  - Memory: "Promote", "Demote", "Delete"
  - Claims: "Release selected"
- All bulk destructive actions use `ConfirmAction`.

#### 3.5 EmptyState integration ✅ COMPLETE

- ✅ Standardized across all panels: Fleet, Tasks, Servers, Memory, Graph
- ✅ Icon, heading, compact variant support

---

### M4: Accessibility + Performance (~85% Complete)

#### 4.1 Semantic HTML audit ✅ COMPLETE

- ✅ `<section>` with `aria-label` in PanelShell.
- ✅ `<nav>` with `aria-label` for main and sub-navigation.
- ✅ `<main>` with `id="main-content"`, skip-to-content link.
- ✅ `<aside role="dialog" aria-modal="true">` for DetailDrawer.
- ✅ `<table role="grid">` for DataTable with `aria-sort`.
- ✅ `<div role="search" aria-label="Filter">` for FilterBar.
- ✅ `aria-current="page"` on active nav tabs.
- ✅ `role="button"` + `tabindex=0` on interactive elements (MetricCard, DataTable rows).

#### 4.2 Focus management ✅ COMPLETE

- ✅ DetailDrawer: focus trapping with `previousFocus` restoration.
- ✅ DetailDrawer: first focusable element auto-focus on open.
- ✅ Escape closes drawer, restores focus.
- ✅ Keyboard shortcuts for view/sub-view navigation (1-6, a-d keys).

#### 4.3 Color-blind safe status indicators

- Every `StatusDot` renders with both color and text label (or icon shape).
- Add shape variants: circle (healthy), triangle (warning), square (error), diamond (unknown).
- Test with Sim Daltonism or similar tool.

#### 4.4 SSE/polling optimization ✅ MOSTLY COMPLETE

- ✅ SSE circuit breaker with exponential backoff (MAX_RECONNECT_ERRORS: 5).
- ✅ Fallback HTTP polling only when SSE disconnected.
- ✅ Incremental fetching with `lastTimestamp`.
- ✅ Deduplication by ID (seenIds Set, capped at 500).
- Remaining: `lastEventId` tracking for reconnection without data loss.

#### 4.5 Store diffing — NOT STARTED

- Before replacing store state with new SSE snapshot, deep-compare with current state.
- Only trigger reactivity if data actually changed.

#### 4.6 D3 simulation pause ✅ COMPLETE

- ✅ EntityGraph: `simulation.stop()` on cleanup.
- ✅ Drag handling with `simulation.alpha(0.3).restart()` on drag start, `alphaTarget(0)` on end.

#### 4.7 Lazy panel loading — NOT STARTED

- Use Svelte `{#await import(...)}` for heavy panels (Topology, Graph, Lifecycle).
- Show skeleton loader while module loads.
- Pre-fetch adjacent panels on idle.

---

## Test Plan

### Unit tests
- `format.ts` utility functions: all formatters, all edge cases.
- `DataTable`: sort, select, virtual scroll boundaries.
- `FilterBar`: search debounce, filter combination.

### Integration tests
- Navigation: hash routing, old hash redirect, keyboard shortcuts.
- DetailDrawer: open/close, focus management, Escape key.
- ConfirmAction: confirm/cancel flow, focus trap.

### Visual regression
- Screenshot each view at 1440x900 before and after each milestone.
- Compare with browserkit screenshots (when available).

### Accessibility audit
- Run axe-core on every view.
- Keyboard-only navigation test (no mouse).
- Screen reader test (VoiceOver on macOS).

### Performance
- Measure bundle size before/after each milestone.
- Profile with Chrome DevTools: no jank on panel switch, no memory leaks on long SSE sessions.
- Lighthouse score target: Performance >90, Accessibility >90.

## Rollout / Backout

- Each milestone ships independently.
- M1 is invisible (internal refactor only).
- M2 includes hash redirect for backward compat. Backout: revert routing changes.
- M3 can be shipped per-feature (drawer, filters, bulk actions independently).
- M4 improvements are additive. No backout needed.

## Acceptance Criteria

- [x] All panels use shared `PanelShell`, `DataTable`, `FilterBar`, `EmptyState` components.
- [x] Navigation has 7 items max. Keyboard shortcuts documented and working.
- [x] DetailDrawer available in all views with drill-down data.
- [x] `ConfirmDialog` used for destructive actions (Memory delete, Graph entity/relation delete).
- [ ] Bulk actions toolbar for multi-select operations (Tasks, Memory, Claims).
- [ ] axe-core reports 0 violations on every view.
- [x] SSE circuit breaker with polling fallback.
- [ ] VirtualList used for all lists with >50 items.
- [x] Bundle size under 200KB gzipped (current: ~106KB gzipped JS + ~16KB gzipped CSS = ~122KB total).

## Risks / Dependencies

- Svelte 5 runes API may have edge cases with lazy imports.
- D3 force layout pause/resume needs testing for visual glitches.
- Grouped navigation changes keyboard shortcut semantics (need user communication).
- Go embed requires `npm run build` before `make build` (dev workflow friction).

## Sources

- [S1] Research findings F1-F8 (`10-research.md`)
- [S2] Product spec R1-R5 (`20-product-spec.md`)
- [S3] `App.svelte:28-41` — panel list
- [S4] `theme.css` — token system
- [S5] `layout.css` — grid system
- [S6] `VirtualList.svelte` — existing unused virtual scroll widget
- [S7] `StatusDot.svelte` — current status indicator
- [S8] `events.svelte.ts` — SSE connection manager

---

## M5: Agent Operator Ergonomics (Context + Queue)

| Step | Description | Status |
|------|-------------|--------|
| **M5.1** | Add context inspector API (`/api/agent/context-inspect`) | ✅ Complete |
| **M5.2** | Add CLI command (`loom agent context-inspect`) | ✅ Complete |
| **M5.3** | Add lane-aware nudge queue policy (cap/drop/debounce/lane order) | ✅ Complete |
| **M5.4** | Add queue status API (`/api/agent/nudge-queue`) | ✅ Complete |
| **M5.4a** | Add runtime queue policy controls (`GET/POST /api/agent/nudge-queue-policy`, `loom agent nudge-queue-policy`) | ✅ Complete |
| **M5.5** | Add HUD UI surface for context/queue diagnostics | ✅ Complete (Presence Diagnostics tab + in-HUD runtime policy mutation controls) |
| **M5.6** | Add full prompt-section/tool-schema context accounting | ✅ Complete |

### M5 Delivered Files

- `internal/hud/nudge.go`
- `internal/hud/api_agent.go`
- `internal/hud/app.go`
- `internal/hud/bridge/agent.go`
- `cmd/loom/cmd_agent.go`
- `docs/USER_GUIDE.md`
- `internal/hud/frontend/src/lib/components/PresencePanel.svelte`

### M5 Test Coverage Added

- `internal/hud/nudge_test.go`
  - lane ordering
  - `drop_new`
  - `summarize`
  - debounce behavior
  - queue status shape
- `internal/hud/bridge/agent_test.go`
  - context inspector aggregation path

### M5 Next Actions

1. Tune section heuristics per provider/model (system prompt + response budget defaults).
