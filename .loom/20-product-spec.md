# Product Spec: HUD UI/UX Overhaul

## Summary

Consolidate the HUD's 13 panels into a cohesive, accessible, and performant dashboard with a formal design system, grouped navigation, consistent interaction patterns, and scalable data display.

## Goals

- Establish a reusable design system (tokens, primitives, composites) that prevents drift.
- Reduce cognitive load by grouping related panels into 5-6 top-level views.
- Meet WCAG 2.1 AA accessibility baseline across all views.
- Eliminate performance bottlenecks (unbounded lists, redundant polling, continuous D3 ticking).
- Provide consistent drill-down, filtering, empty state, and action patterns.

## Non-Goals

- Rewriting the Go backend or changing the API surface.
- Adding new data sources or panels beyond what exists.
- Supporting mobile/tablet as a primary target (desktop-first is fine).
- Replacing Svelte with another framework.

## Users / Stakeholders

- **Primary**: Developer using HUD on macOS desktop to monitor agent activity.
- **Secondary**: Developer using overlay mode in Ghostty for glanceable status.
- **Tertiary**: Future users who discover HUD via `loom hud` and need onboarding.

## Requirements

### Functional

#### R1: Design system foundation
- Define a type scale: 10, 12, 14, 16, 20, 24px with clear semantic roles.
- Define a spacing scale: 4px baseline (4, 8, 12, 16, 24, 32, 48px).
- Centralize all hardcoded magic numbers into named tokens.
- Create shared primitive components: `PanelShell`, `DataTable`, `FilterBar`, `EmptyState`, `DetailDrawer`, `ConfirmAction`.

#### R2: Navigation restructure
- Group 13 panels into 5-6 top-level views:

| View | Contains | Key |
|------|----------|-----|
| **Agents** | Fleet + Presence + Topology + Lifecycle | `1` |
| **Servers** | Server health + tunnels + cache | `2` |
| **Tasks** | Tasks + Workflows | `3` |
| **Knowledge** | Memory + Graph + Reasoning | `4` |
| **Activity** | Timeline + Stream | `5` |
| **Sandbox** | Devbox sandbox management | `6` |

- Each view uses internal tabs or segmented controls for sub-navigation.
- Overview (`o`/backtick) remains a top-level KPI dashboard.
- Keyboard shortcuts preserved: `1`-`6` for views, sub-view switching via `a`-`d` within a view.

#### R3: Consistent interaction patterns
- **Drill-down**: Click any row/card to open a `DetailDrawer` (slide-in from right).
- **Filtering**: Every list view gets a `FilterBar` with search + status/type filters.
- **Bulk actions**: Checkbox selection on table rows, bulk toolbar appears.
- **Destructive actions**: Always use `ConfirmAction` dialog (memory demote, claim release, task delete).
- **Empty states**: Every list uses `EmptyState` component with icon, message, and primary action CTA.

#### R4: Accessibility
- All interactive elements use semantic HTML (`<button>`, `<a>`, `<th scope>`, `<nav aria-label>`).
- `aria-current="page"` on active nav item.
- `aria-sort` on sortable table headers.
- Status indicators pair color with text label or icon (not color-only).
- Focus management: panel switch moves focus to panel heading.
- Keyboard: arrow keys navigate within tables/lists, Enter activates, Escape closes drawers.

#### R5: Performance
- Apply `VirtualList` to all scrollable lists with >50 potential items.
- Suppress polling when SSE is connected and receiving events.
- Diff incoming SSE snapshots before replacing store state (prevent unnecessary re-renders).
- Pause D3 force simulation after layout stabilizes (no continuous ticking).
- Lazy-load heavy panels (Topology, Graph) on first navigation.

### Non-Functional

- Build time: `npm run build` completes under 10s.
- Bundle size: total JS+CSS under 200KB gzipped.
- First paint: HUD loads and shows skeleton in under 500ms.
- No external CDN dependencies (all assets embedded).

## UX / Flows

### Primary flow: Monitor agent activity
1. Open HUD (`loom hud` or `http://localhost:3333`).
2. Land on Overview (KPI dashboard with at-a-glance metrics).
3. Notice an agent has high task count -> press `1` to switch to Agents view.
4. See Fleet sub-tab with session list. Click a session row.
5. DetailDrawer slides in showing session entries, token usage, task list.
6. Press `Escape` to close drawer. Press `d` to switch to Topology sub-tab within Agents view.
7. See force-directed graph of agent relationships.

### Secondary flow: Approve a workflow
1. From Overview, see pending workflow badge on Tasks tile.
2. Press `3` to switch to Tasks view.
3. Press `b` to switch to Workflows sub-tab.
4. Click pending workflow row -> DetailDrawer shows steps and approval gate.
5. Click "Approve" -> ConfirmAction dialog -> confirm -> toast notification.

### Overlay flow
1. Ghostty overlay activated via hotkey.
2. OverlayShell shows compact KPI strip (same data as Overview but single-row).
3. Press `1`-`6` to peek into a view (compact mode, no drawer).
4. Press Escape or hotkey to dismiss.

## Data / APIs

No API changes required. All existing endpoints are sufficient. Changes are frontend-only.

One optimization: add `If-None-Match`/`ETag` support on `/api/fleet` and `/api/health` to reduce payload when data hasn't changed (backend enhancement, optional).

## Rollout / Migration

- **Phase 1** (Design system): Ship as internal refactor. No visible behavior change. All panels still work.
- **Phase 2** (Navigation): Ship with URL hash migration (`#fleet` -> `#agents/fleet`). Old hashes redirect.
- **Phase 3** (Interactions): Ship per-feature behind no flag (detail drawer, confirm dialogs).
- **Phase 4** (Performance): Ship transparently. No user-visible change except speed.

## Observability

- Logs: Frontend errors logged to console with structured format.
- Metrics: Track panel switch frequency, drawer open/close, command palette usage (via SSE event stream to backend).
- Traces: Not applicable (frontend-only).

## Risks

- **Navigation restructure breaks muscle memory**: Mitigate with keyboard shortcut compatibility and hash redirect.
- **Design system upfront cost delays features**: Mitigate by keeping Phase 1 small (tokens + 3-4 primitives).
- **Overlay mode breaks during restructure**: Mitigate by keeping OverlayShell as a separate code path that consumes stores directly.

## Open Questions

- Should grouped views use tabs, segmented controls, or sidebar sub-nav?
- Should the DetailDrawer be a slide-over or a modal? (Slide-over preserves context, modal is simpler.)
- Should we add user preference persistence (last active view, collapsed sections)?
- Should the Overview tile grid be user-configurable (drag-to-reorder)?

## Sources

- [S1] `App.svelte` — 13 panels, routing, shortcuts (`App.svelte:28-41`)
- [S2] `theme.css` — design tokens, color palette
- [S3] `layout.css` — grid system, utility classes
- [S4] Research findings F1-F8 (see `10-research.md`)
- [S5] `ROADMAP.md` — current project priorities

---

## Addendum (2026-02-16): Agent Operator Ergonomics

### Summary

Introduce two operator-facing capabilities inspired by comparative research:

1. **Agent Context Budget Inspector**
2. **Lane-Aware Nudge Queue Policy**

### Goals

- Give agents and operators direct insight into context pressure before token-window failures.
- Make nudge delivery predictable under load with explicit overflow policy.
- Preserve existing heartbeat-based delivery model and daemon fallback behavior.

### Non-Goals (current slice)

- Full runtime prompt reconstruction (including every provider wrapper token).
- New UI panels in this first iteration (API/CLI first).
- Cross-agent global scheduling overhaul.

### Requirements

#### R-CTX-1: Context budget inspection

- Expose an API endpoint to inspect context weight by session/agent.
- Expose a CLI command for the same capability.
- Include totals + breakdown by entry type.
- Include optional top-entry detail mode.
- Include task and memory snapshot context for operational triage.

#### R-QUEUE-1: Lane-aware nudge queue

- Queue supports lane priority ordering.
- Queue supports cap + drop policy (`drop_old`, `drop_new`, `summarize`).
- Queue supports optional debounce before delivery.
- Queue status is queryable via API for observability.

### Acceptance Criteria

- `GET /api/agent/context-inspect` returns JSON with:
  - `entry_count`, `estimated_tokens`, `by_entry_type`, `tasks`, `memory`
- `loom agent context-inspect` resolves through HUD and daemon fallback.
- Nudge queue supports lane assignment and deterministic lane-priority drain.
- Queue overflow behavior is covered by tests for all drop policies.
- `GET /api/agent/nudge-queue?agent_id=...` returns pending/dropped/by-lane policy state.

### Sources (addendum)

- OpenClaw context docs: `13-research-agentic-workflows-openclaw.md`
- OpenClaw queue docs/code: `13-research-agentic-workflows-openclaw.md`
- Loom implementation files:
  - `internal/hud/api_agent.go`
  - `internal/hud/nudge.go`
  - `internal/hud/bridge/agent.go`
  - `cmd/loom/cmd_agent.go`
