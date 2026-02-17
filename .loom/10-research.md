# Research Brief: HUD UI/UX Overhaul

## Problem

The HUD has grown from a simple status page to a 13-panel real-time dashboard in ~3 weeks. The rapid iteration produced a feature-rich but inconsistent UI with duplicated patterns, accessibility gaps, and no formal design system. Each panel invents its own layout, filtering, detail-view, and empty-state patterns.

## Questions

- Q1: What are the major UI/UX inconsistencies across panels?
- Q2: What accessibility issues exist?
- Q3: Where does performance degrade with scale?
- Q4: What information architecture improvements would reduce cognitive load?

## Constraints

- Frontend: Svelte 5 + Vite, no external component library (pure CSS variables).
- Backend: Go HTTP server with embedded dist/ (zero-dependency deploy).
- Data flow: SSE-first with 30s polling fallback.
- Target environment: macOS desktop (primary), overlay mode in Ghostty terminal.

## Method

- Full code audit of all 13 panel components, 14 widget components, 16 Svelte stores, and 2 CSS files.
- Reviewed recent git history (7 HUD-related commits since Jan 12, 2026).
- Analyzed theme.css and layout.css token systems.
- Mapped data flow from Go monitors through SSE to Svelte stores.

## Findings

### F1: Inconsistent layout patterns (source: `layout.css`, panel components)

Each panel uses different grid/layout approaches:
- FleetPanel: 2x2 grid (table + stats + activity + gauges)
- ServersPanel: table + 2-col infra cards below
- OverviewPanel: KPI strip + auto-fill tile grid
- TasksPanel: full-width table with toolbar
- MemoryPanel: tier gauges + search + item list
- TopologyPanel/LifecyclePanel: full-bleed canvas
- Others: single-column scrollable lists

No shared "panel template" component exists. Each panel rebuilds header, filter bar, content area, and empty states from scratch.

### F2: Accessibility gaps (source: all panel .svelte files)

| Issue | Severity | Location |
|-------|----------|----------|
| Clickable `<div>`/`<tr>` without `role="button"` | High | FleetPanel, ServersPanel |
| No `aria-sort` on sortable table headers | Medium | ServersPanel, TasksPanel |
| No `aria-current="page"` on active nav tab | Medium | App.svelte:210 |
| Color-only status indication (no text/icon fallback) | High | StatusDot, all panels |
| No `aria-label` on icon-only buttons | Medium | App.svelte:222 (Cmd+K) |
| Missing focus management on panel switch | Medium | App.svelte:237 |
| No skip-to-content link | Low | App.svelte |

### F3: Duplicated utility code (source: panel components)

Functions duplicated across panels instead of centralized:
- `formatTime()` / `relativeTime()` / `formatNumber()` — FleetPanel, TimelinePanel, LifecyclePanel
- `truncateId()` / `truncatePath()` — FleetPanel, PresencePanel, TasksPanel
- Status color mapping logic — repeated in 6+ panels
- Entry type icons/labels — FleetPanel, StreamPanel

### F4: Performance concerns (source: stores, panel components)

- **No virtual scrolling**: FleetPanel entries timeline, TasksPanel list, StreamPanel can grow unbounded. VirtualList widget exists (`VirtualList.svelte`) but is unused in main panels.
- **Redundant polling**: Even with SSE connected, stores continue 30s fallback polling. No coordination between SSE receipt and poll suppression.
- **Re-render on every SSE event**: Fleet/health snapshots trigger full store replacement, causing all derived values to recompute even when data hasn't changed.
- **D3 force layout runs continuously**: TopologyPanel and EntityGraph tick on every animation frame even when data is static.

### F5: Information architecture issues (source: App.svelte panel list)

13 panels is too many for a single nav bar at 1440px viewport:
- Tab labels truncate or crowd at smaller widths
- Related panels are scattered: Fleet(1), Presence(8), Topology(t), Lifecycle(l) all show "agent state"
- Memory(5), Reasoning(0), Graph(6) all show "agent knowledge"
- No visual grouping or hierarchy in navigation

### F6: Missing interaction patterns

- **No consistent drill-down**: FleetPanel has click-to-detail, ServersPanel has footer detail, others have none
- **No bulk actions**: can't select multiple tasks/entities for batch operations
- **No inline editing**: all mutations require navigating to a form or using command palette
- **No confirmation on destructive actions**: file claim release, memory demote happen immediately
- **No undo/redo**: actions are fire-and-forget

### F7: Empty state inconsistency

- Some panels show styled `.empty-state` with icon + message
- Others show raw "No data" text in table cells
- TasksPanel shows a "Create" button in empty state, others don't
- No guidance for what to do when empty (onboarding flow)

### F8: Typography and spacing irregularities (source: `theme.css`, `layout.css`)

- **No type scale**: font sizes jump irregularly (9px, 10px, 11px, 12px, 14px, 16px, 20px)
- **No spacing scale**: padding/gap values are ad-hoc (4, 5, 6, 8, 10, 12, 16, 20, 24px)
- **Inconsistent text transforms**: some headers uppercase, some sentence case, some title case
- **Mixed font usage**: `--font-mono` used for both data and UI labels inconsistently

## Options

### Option A: Incremental polish (panel-by-panel cleanup)

Fix inconsistencies in each panel individually. No structural changes.
- Pros: low risk, can ship incrementally.
- Cons: doesn't address information architecture, nav crowding, or missing shared components. Whack-a-mole.

### Option B: Design system first, then panel refactor

Build a formal component library (tokens, primitives, composites), then rewrite panels to use it.
- Pros: systematic, prevents future drift, enables theme switching.
- Cons: larger upfront investment, panels frozen during component work.

### Option C: IA restructure + design system in parallel (recommended)

Restructure panels into grouped views (reducing nav items from 13 to 5-6), while simultaneously building shared components. Implement in phases.
- Pros: addresses both structural and visual issues, delivers value in each phase.
- Cons: requires careful phasing to avoid breaking existing workflows.
- Risks: must preserve keyboard shortcuts and overlay mode compatibility.

## Recommendation

Option C: IA restructure + design system, phased.

Phase 1 establishes the token/component foundation. Phase 2 restructures navigation. Phase 3 adds missing interactions. Phase 4 polishes performance and accessibility.

## Sources

- [S1] `internal/hud/frontend/src/App.svelte` — panel list, routing, keyboard shortcuts
- [S2] `internal/hud/frontend/src/lib/styles/theme.css` — CSS variables, color palette, typography
- [S3] `internal/hud/frontend/src/lib/styles/layout.css` — grid system, utility classes, component styles
- [S4] `internal/hud/frontend/src/lib/components/FleetPanel.svelte` — main panel with session detail
- [S5] `internal/hud/frontend/src/lib/components/ServersPanel.svelte` — server health table
- [S6] `internal/hud/frontend/src/lib/components/OverviewPanel.svelte` — KPI dashboard
- [S7] `internal/hud/frontend/src/lib/stores/events.svelte.ts` — SSE connection, circuit breaker
- [S8] `internal/hud/frontend/src/lib/stores/fleet.svelte.ts` — fleet data, polling
- [S9] `internal/hud/frontend/src/lib/widgets/VirtualList.svelte` — unused virtual scroll
- [S10] `internal/hud/frontend/src/lib/widgets/StatusDot.svelte` — status indicator
- [S11] Git log: 7 HUD commits from 2026-01-12 to 2026-02-13
- [S12] `ROADMAP.md` — current priorities
