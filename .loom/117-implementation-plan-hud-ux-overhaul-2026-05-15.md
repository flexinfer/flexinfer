# Implementation Plan — HUD UI/UX Overhaul (2026-05-15)

Companion: [115-brainstorm-hud-ux-improvements-2026-05-15.md](115-brainstorm-hud-ux-improvements-2026-05-15.md), [116-product-spec-hud-ux-overhaul-2026-05-15.md](116-product-spec-hud-ux-overhaul-2026-05-15.md)
Status: proposed
Cycle: 2026-05-15 → ~2026-06-12

## Execution Order

```
A1  Action toolkit primitives                      (2-3 days, foundational)
A2  Triage Overview rebuild + inbox card catalog   (3-4 days)
A3  Theme sweep (F4-full)                          (1-2 days, parallel-safe with A2)
A4  Slice A merge + screenshot + soak              (0.5 day)
--- ship Slice A ---
B1  Decomp pattern + one canary panel              (3 days, FleetPanel as canary)
B2  Decomp remaining 4 panels                      (4-6 days, parallel-shippable)
B3  SSE migration across decomposed stores         (2-3 days)
B4  Keyboard nav + Cmd+P fuzzy palette             (2 days)
B5  Responsive reflow + embed subset               (2-3 days)
B6  Slice B merge + cross-panel soak               (1 day)
```

Each lettered sub-slice ends with `pnpm build && pnpm lint && go test ./internal/hud/...` green + a merge request.

## Slice A — Operator Inbox

### A1 — Action Toolkit Primitives

**Files to add / change**

- Add `internal/hud/frontend/src/lib/components/shared/action/ConfirmDialog.svelte` — wraps existing `shared/ConfirmDialog.svelte` if present; otherwise new. Props: `open`, `title`, `message`, `confirmLabel`, `confirmVariant`, `onConfirm`, `onCancel`. Focus-trap + Esc + click-outside.
- Add `internal/hud/frontend/src/lib/components/shared/action/ActionToast.svelte` — single-instance toast feeder; subscribes to `actionStore` events; auto-dismiss 4 s; pause on hover.
- Add `internal/hud/frontend/src/lib/components/shared/action/ErrorCard.svelte` — inline card with message + retry + dismiss; replaces ad hoc error blocks.
- Add `internal/hud/frontend/src/lib/utils/useAction.svelte.ts` — exposes `useAction(fn)` returning `{ run, pending, error, retry, lastResult }` with optimistic + rollback. Routes failures into `actionStore.recordError`.
- Add `internal/hud/frontend/src/lib/stores/action.svelte.ts` — session-local ring buffer (last 50 actions) + Svelte $state; `sessionStorage` mirror.
- Add `internal/hud/frontend/src/lib/components/shared/action/AuditDrawer.svelte` — slide-out from right; lists recent actions with status + retry link.

**Validation**

- Unit-level: a vitest harness that drives `useAction` through success / failure / retry / rollback paths (or, if vitest isn't wired here, a `pnpm dev` smoke checklist).
- Visual smoke: open `OverviewPanel`, trigger one stub action, see Toast → see entry in AuditDrawer → click retry.
- `pnpm build && pnpm lint` clean.

### A2 — Triage Overview Rebuild

**Files to add**

- `internal/hud/frontend/src/lib/components/overview/InboxCard.svelte` — severity badge + headline + detail + actions row + optional drill row.
- `internal/hud/frontend/src/lib/components/overview/InboxDeck.svelte` — virtualized stack of `InboxCard`; empty state = "System nominal".
- `internal/hud/frontend/src/lib/components/overview/HeroSummary.svelte` — extracted from current `OverviewPanel:233-289`.
- `internal/hud/frontend/src/lib/components/overview/InstrumentStrip.svelte` — extracted from current `OverviewPanel:177-210`.
- `internal/hud/frontend/src/lib/utils/inbox.ts` — typed `CardSpec` + 7 selector functions (one per `D2` card kind) that consume stores.

**Files to change**

- `internal/hud/frontend/src/lib/components/OverviewPanel.svelte` — reduce to <500 lines: shell that composes `HeroSummary`, `InboxDeck`, `InstrumentStrip`, plus the existing `MillsKPIRow` + `LiveSessionsCard`. Logic stays in selectors.

**Card wiring**

- `file_conflict` → drill to Dispatch with conflict pre-selected.
- `blocked_task` → primary action: open inline task detail (no destructive); secondary: route to Dispatch.
- `pending_approval` → primary action: `useAction(() => fetch('/api/workflows/{id}/approve', { method:'POST' }))` via `useAction`; confirms via dialog.
- `server_down` → drill to filtered Servers.
- `orphan_session` → primary action: `useAction(() => fetch('/api/sessions/{id}/end'))` with ConfirmDialog.
- `rbac_denied_spike` → drill only (no action endpoint).
- `stale_session` → primary action: recover; fallback: end.

**Validation**

- Render each of the 7 card kinds against seeded fixture data (mock store flag) — manual checklist:
  - Conflicts present → conflict card appears, dispatch drill works.
  - Blocked task present → card + drill works.
  - Pending approval present → approve button fires action + toast + audit entry.
  - Down server present → drill works.
  - Orphan session present → reap action with confirm dialog.
  - RBAC denial spike → drill card visible.
  - Stale session → end action with confirm.
  - All cards empty → "System nominal" empty state.
- `wc -l internal/hud/frontend/src/lib/components/OverviewPanel.svelte` < 500.

### A3 — Theme Sweep (F4-full)

**Files to change**

- `internal/hud/frontend/src/lib/styles/theme.css` — collapse `--glow-success`, `--glow-info`, `--glow-warning` to neutral; keep `--glow-error`, `--glow-accent`. Add `--agent-1/2/3` derived from `--accent` via `color-mix`. Keep legacy `--agent-claude/codex/gemini/copilot` as aliases with deprecation comment.
- `internal/hud/frontend/src/lib/styles/layout.css` — flatten `.panel-area` radial wash background; ensure card surfaces still have one tonal lift.
- `internal/hud/frontend/src/App.svelte` — remove default scanlines (already off); ensure Cmd+K → "Toggle scanlines" remains in `CommandPalette.svelte` items.
- Audit panels for `box-shadow: 0 0 X var(--glow-success|--glow-info)` and drop or replace with flat border. Use `grep -rn "glow-success\|glow-info" internal/hud/frontend/src/` and pass through each hit.

**Validation**

- Visual review: every panel under all 7 view groups + Overview. Capture before/after screenshots into `.loom/local/hud-theme-sweep-2026-05-15/`.
- A11y: contrast remains AA on alert states; check with browser devtools.
- `pnpm build` clean.

### A4 — Slice A Merge + Soak

- Squash A1+A2+A3 if shipped together, or ship as separate MRs with the action-toolkit MR landing first.
- Update `.loom/00-index.md` with brainstorm + spec + plan links.
- Update `ROADMAP.md` HUD section to reflect Slice A shipped.
- Soak 24–48 h on mobile-hud + at least one developer-local HUD before declaring done.

---

## Slice B — Foundations

### B1 — Decomp Pattern + Canary (FleetPanel)

**Pattern doc:** Add `docs/HUD_PANEL_DECOMP.md` (short — under 100 lines). Defines:

- Folder layout: `lib/components/<panel>/{PanelRoot.svelte, *.svelte}` plus state in `lib/stores/<panel>.svelte.ts`.
- Required composition: `<PanelShell><FilterBar/><DataTable/><DetailDrawer/></PanelShell>`.
- Store contract: every store exposes `filter`, `search`, `sortKey`, `sortDir`, `selected: Set<id>` as `$state`; `filtered` and `visible` as `$derived`.

**Canary:** `FleetPanel.svelte` 1777 → <300 lines.

- Extract `fleet/FleetTable.svelte`, `fleet/SessionDetail.svelte`, `fleet/NamespaceGroup.svelte`, `fleet/AgentSummary.svelte`.
- Move filter/search/sort/select state into `lib/stores/fleet.svelte.ts` getters.
- Keep current behavior identical; verify against `?legacy=1` query param that still renders the old monolith.

**Validation**

- `wc -l internal/hud/frontend/src/lib/components/FleetPanel.svelte` < 300.
- Side-by-side screenshot comparison legacy vs new at the same viewport.
- `pnpm build && pnpm lint && go test ./internal/hud/...` clean.
- Bookmark URL `#agents/fleet` still resolves; `#agents/fleet/<sessionId>` still drills.

### B2 — Decompose Remaining Panels

Apply the B1 pattern to:

| Panel | Lines now | Target | Subcomponents (suggested) |
|---|---|---|---|
| `TasksPanel.svelte` | 1483 | <300 | `tasks/TasksTable.svelte`, `tasks/TaskDetail.svelte`, `tasks/TaskGroups.svelte` |
| `SandboxPanel.svelte` | 1479 | <300 | `sandbox/SandboxList.svelte`, `sandbox/SandboxDetail.svelte`, `sandbox/ExecDrawer.svelte` |
| `SpawnPanel.svelte` | 1369 | <300 | `spawn/SpawnList.svelte`, `spawn/SpawnFilters.svelte` (`SpawnDetailPanel` already exists) |
| `ServersPanel.svelte` | 1123 | <300 | `servers/ServersTable.svelte`, `servers/ServerDetail.svelte`, `servers/HealthBadges.svelte` |
| `GraphPanel.svelte` | 1030 | <300 | `graph/GraphCanvas.svelte`, `graph/EntityDrawer.svelte` |

Each panel ships in its own MR with a legacy fallback. Order recommendation: SpawnPanel first (Labs P0/P1 from `.loom/31` benefits most), then ServersPanel, TasksPanel, SandboxPanel, GraphPanel.

**Validation per panel**

- `<300` lines.
- All current routes/keyboard shortcuts still work.
- Manual smoke for each panel against live daemon.
- Quality gate green.

### B3 — SSE Migration

**Files to change**

- `internal/hud/frontend/src/lib/stores/events.svelte.ts` — add typed `subscribe<T>(eventType, handler)` returning unsubscribe; document supported `hud.*` events.
- For each decomposed panel's store (`fleet`, `tasks`, `sandbox`, `spawn`, `servers`): add SSE subscriptions in `init()`; reduce polling interval from 10–30 s to ≥60 s (acts as watchdog + cold-load fallback).
- Add `staleAfter: number` per store; `staleness` derived state; show "stale" pill in `ConnectionBanner.svelte` when any store flips.

**Validation**

- Manually kill the SSE connection (block `/api/events` in devtools); confirm:
  - Stale pill appears within `staleAfter`.
  - Polling fallback restores data on next tick.
  - Restoring SSE clears the pill.
- Prometheus `hud_api_requests_total` (or daemon log line count) shows reduced poll rate over 5-minute window — capture before/after numbers in MR description.

### B4 — Keyboard Nav + Cmd+P

**Files to change**

- `internal/hud/frontend/src/lib/components/shared/DataTable.svelte` — add `onkeydown` for j/k/Enter/x/g g/G; track `selectedIndex` via `$state`; `selectionMode` prop toggles `x` bulk-select.
- `internal/hud/frontend/src/lib/components/CommandPalette.svelte` — add fuzzy index over (a) routes, (b) recently seen entities (last 100 sessions/tasks/spawns from stores), (c) actions registered via `actionStore`. Trigger via `Cmd+P` (existing `Cmd+K` stays for command-only).
- `internal/hud/frontend/src/App.svelte` — register `Cmd+P` shortcut alongside `Cmd+K`.

**Validation**

- Visit each decomposed panel; verify j/k row nav, Enter drill, Esc back.
- `Cmd+P`, type partial route/session id, Enter → navigates.
- `?` help overlay updated to list new keys.

### B5 — Responsive Reflow + Embed Subset

**Responsive (≤800 px)**

- `internal/hud/frontend/src/App.svelte` — extend existing `@media (max-width: 768px)` to 800 px; move `.nav-tabs` to a bottom fixed bar; ensure tap targets ≥44 px.
- Each decomposed panel: at `≤800px`, `DataTable` switches to stacked-card mode (label-above-value); `DetailDrawer` becomes full-screen.
- Test in iPhone 17 simulator + iPad simulator (per `MEMORY.md` Simulator names).

**Embed subset**

- `internal/hud/embed.go` — extend `NewEmbedded(opts)` to accept `Subset string` with values `"full"` (default) and `"operator"` (Overview + Fleet + Stream only).
- Backend route allowlist enforced at handler register time.
- Frontend route guard: when running under `--subset=operator`, hide nav tabs for groups outside the allowlist; `router.navigate` to a hidden route falls back to Overview.

**Validation**

- `loom hud --embed --subset operator` starts; nav shows only Overview + Operations (Fleet) + Activity (Stream).
- Mobile viewport (iPhone 17): Overview → Fleet → Stream all legible without horizontal scroll.
- Backend test: `internal/hud/embed_test.go` covers subset allowlist.

### B6 — Slice B Merge + Cross-Panel Soak

- Confirm five panels all <300 lines.
- Update `docs/HUD_PANEL_DECOMP.md` with any pattern adjustments learned during decomp.
- Update `.loom/00-index.md` with this trio.
- Update `ROADMAP.md` HUD section.
- Soak 48 h on mobile-hud + dev laptops before declaring done.

---

## Cross-Slice Validation Commands

```bash
# Frontend build / lint
cd internal/hud/frontend
pnpm install
pnpm build
pnpm lint

# Backend
go test ./internal/hud/... -count=1 -race

# Quality gate (preferred when available)
devbox_quality_gate(project="loom-core", agent_id="claude-code")

# Contract drift check (must stay zero across the whole epic)
go test ./internal/contracts/... -count=1
make ci-contracts

# Monolith size verification
wc -l internal/hud/frontend/src/lib/components/{Fleet,Overview,Tasks,Sandbox,Spawn,Servers,Graph}Panel.svelte

# Polling vs SSE check (rough)
grep -rn "startPolling" internal/hud/frontend/src/lib/stores/ | wc -l    # should drop slice B
grep -rn "subscribe(" internal/hud/frontend/src/lib/stores/ | wc -l      # should rise slice B
```

## Risk Register (with mitigations)

| Risk | Likelihood | Mitigation |
|---|---|---|
| Inbox actions fire by mistake | Medium | ConfirmDialog gates every destructive action; audit drawer surfaces what fired |
| F4-full theme rejected aesthetically | Medium | Land behind a `--theme-tight` toggle in first release if needed; deprecated agent aliases preserved |
| Panel decomp regression in B2 | Medium | One panel per MR, screenshots, `?legacy=1` fallback for one minor release |
| SSE store leaks subscriptions | Medium | Standard `init()` returns `dispose()`; `$effect` cleanup audited in code review |
| Cmd+P collides with browser | Low | Already overridden in many ops UIs; if user opts out, keyboard help shows fallback |
| Responsive reflow on undecomposed panels looks broken | Medium | Document in B5 that only the five decomposed panels are guaranteed responsive; others remain best-effort |
| `codex/hud-view-fixes` carry-forward duplicates new work | Low | Review commit-by-commit at start of B2; drop superseded panel edits |
| Mobile golden drift | Low | `make ci-contracts` covers; no `/api/mobile/v1/*` changes planned |

## Open Questions (to resolve in execution)

- Does the daemon already emit `hud.workflow.approved` and `hud.task.unblocked`? If not, B3 store wiring stops at fleet/health/sessions and the rest stay on polling for this cycle.
- Is `useAction.svelte.ts` better as a function or as a Svelte 5 rune helper? Decide during A1 spike (≤2 h).
- Should AuditDrawer be visible in embed mode? Default no; revisit if operators ask.

## Definition of Done (Epic)

- Slice A and Slice B both merged to `main`.
- Brainstorm + spec + plan trio archived under `.loom/archive/` once shipped.
- `ROADMAP.md` updated to mark HUD UI/UX overhaul complete.
- One short retro doc added at `.loom/local/retros/hud-ux-overhaul-2026-XX-XX.md` capturing what regressed and what we'd do differently.

## Sources

- Brainstorm: [.loom/115-brainstorm-hud-ux-improvements-2026-05-15.md](115-brainstorm-hud-ux-improvements-2026-05-15.md)
- Product spec: [.loom/116-product-spec-hud-ux-overhaul-2026-05-15.md](116-product-spec-hud-ux-overhaul-2026-05-15.md)
- Monolith inventory: command `wc -l internal/hud/frontend/src/lib/components/*.svelte` (2026-05-15)
- Existing primitives: `internal/hud/frontend/src/lib/components/shared/`
- Existing triage logic: `internal/hud/frontend/src/lib/components/OverviewPanel.svelte:233-289`
- Workflow approve handler: `internal/hud/domain/workflow/handler_workflow.go`
- Embed precedent: [.loom/103-product-spec-unify-visibility-2026-05-06.md §D3](103-product-spec-unify-visibility-2026-05-06.md)
- SSE substrate: [.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md](99-implementation-plan-agent-telemetry-spectator-2026-05-04.md)
- Labs gaps reference: [.loom/31-hud-labs-prime-time-plan.md](31-hud-labs-prime-time-plan.md)
- Prior HUD/UX trio: [.loom/18](18-research-hud-ux-continuation-2026-03-13.md), [.loom/22](22-product-spec-hud-ux-continuation-2026-03-13.md), [.loom/41-implementation-plan-hud-ux-continuation-2026-03-13.md](41-implementation-plan-hud-ux-continuation-2026-03-13.md)
- Carry-forward reference branch: `codex/hud-view-fixes` (per .loom/18 §F4)
