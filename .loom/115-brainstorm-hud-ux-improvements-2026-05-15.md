# Brainstorm — HUD UI/UX Improvements (2026-05-15)

## Question

What is the highest-leverage set of HUD UI/UX improvements to plan next, given the current shell (`App.svelte`), the surface area (7 view groups × ~50 sub-views), the panel-monolith debt, and the prior overhaul that already shipped grouped nav + shared primitives?

## Baseline (Current State)

- **Shell:** `App.svelte:181-470` runs hash routing, keybindings (`1-7`, `a-z`, `/`, `?`, `Cmd+K`), nav-tabs, status bar. Operator-friendly labels but heavy concentration of logic at top level. [S1]
- **Views:** 7 grouped top-level views (Operations, Infrastructure, Work, Context, Activity, Labs, Mills) + standalone Overview. ~50 sub-views total. [S2]
- **Panel monoliths:** 5 components > 1000 lines — `FleetPanel.svelte` 1777, `OverviewPanel.svelte` 1504, `TasksPanel.svelte` 1483, `SandboxPanel.svelte` 1479, `SpawnPanel.svelte` 1369. 21K total component LOC. [S3]
- **Stores:** 41 `*.svelte.ts` stores; `OverviewPanel.svelte:117-146` starts 12 polling loops on mount; intervals span 10–30 s. [S4]
- **Shared primitives exist:** `FilterBar`, `DataTable`, `DetailDrawer`, `BulkToolbar`, `ViewShell`, `MetricCard`, `EmptyState`, `PanelShell` in `lib/components/shared/`. Adoption uneven — `TasksPanel` and `FleetPanel` still own local filter/search state. [S5]
- **Theme:** "Instrument-panel" dark — Outfit + JetBrains Mono, CRT-style gradient washes, scanlines class, 4 agent colors, 5 status colors, glow tokens for every semantic color. [S6]
- **Triage already started:** `OverviewPanel.svelte` already derives a `heroSummary` (conflicts → infra → blockers → approvals → nominal) + `attentionLanes`. Foundation for "inbox" exists. [S7]
- **Action UX gaps from prior plan:** Labs Prime-Time plan (`.loom/31`) called out missing confirm dialogs, no retry/error recovery, no spawn activity tab, no filtering, opaque token-auth errors. P0–P1 items still partially open. [S8]
- **Streaming substrate:** Spectator phases 0–5 shipped; SSE `hud.*` events available. Most panels still poll instead of subscribing. [S9]

## Framings (Distinct Lenses)

### F1 — "Less to Look At" (Surface Reduction)
Collapse the 50-subview surface area. Hide Mills + Labs behind a feature-flag gear menu; promote a 3-tab default IA (Now / Run / Watch). Move advanced views to a "More" overflow.
- Pro: Lowers cognitive load for casual operators; matches the "embed HUD" use case.
- Con: Power users feel buried; muscle memory breaks; pushes complexity into a flat hidden menu.
- Cost: Small (config + nav refactor).

### F2 — "Operator Inbox" (Triage-First Overview)
Promote Overview into a true triage console. Each "pressure" becomes a card with a defined fix flow (confirm dialog → fire action → toast → audit). Hero already exists in `OverviewPanel.svelte:233-289`; extend it to a stack of actionable cards with one-click resolutions (resolve conflict, approve workflow, drain orphan, reap zombie session, retry failed job).
- Pro: Converts data into action. Highest perceived user value. Builds on existing logic.
- Con: Requires backend action wiring for resolutions (some already exist, some don't).
- Cost: Medium. ~6–10 cards × wiring + shared confirm/toast/action surfaces.

### F3 — "Keyboard Densification" (Power-User Polish)
Existing keyboard nav is good at the shell level; per-panel it's mouse-heavy. Add j/k row nav, Enter to drill, `x` to select, `g g` / `G` to jump, `e` to edit, `Cmd+P` quick-jump-to-anything (extends `CommandPalette.svelte`).
- Pro: Operator delight; lifts every panel without redesign.
- Con: Requires uniform panel pattern (data + selection + actions). Many panels would need refactor first.
- Cost: Medium-high. Coupled to F5.

### F4 — "Theme Discipline" (Visual Calm)
Reduce decoration: drop CRT gradient washes from `panel-area::after`-style backgrounds, tighten glow tokens to alert states only, collapse agent color palette into a single accent + 3 variants. Provide a "calm mode" toggle for ambient/embed contexts.
- Pro: Reduces visual noise; the instrument-panel character survives in motif but not on every surface.
- Con: Aesthetic backlash. Loses the "Loom feel" that operators may have grown to like.
- Cost: Small. Tokens + a handful of CSS edits.

### F5 — "Decompose the Monoliths" (Structural Refactor)
Break the five 1k+ panels into <300-line composed panels via shared primitives (`PanelShell`, `DataTable`, `DetailDrawer`, `FilterBar`). Move stateful logic into stores; templates stay declarative.
- Pro: Unblocks every future UX iteration. Reduces drift. Already-built primitives finally get used.
- Con: Big internal refactor, little visible user win. Risk of regressions across all major panels.
- Cost: High. ~2 weeks across 5 panels.

### F6 — "IA Re-Anchor" (Re-Org Around Intent)
Re-organize top-level around what operators *do*: "Run Work" / "Watch System" / "Tune Platform" / "Debug". Or flatten around the unified visibility surfaces (status/health/cost/RBAC/catalog/sessions/tasks/presence).
- Pro: Models user mental flow; removes confusion about where Mills vs Operations vs Sandbox lives.
- Con: Significant route + muscle-memory churn. Affects bookmarks, docs, screenshots.
- Cost: Medium. Mostly relabel + reorder + legacy redirect tables.

### F7 — "Streaming First" (Polling → SSE)
Wire panels to the existing `hud.*` SSE bus. Replace 10–30 s polling with push. Add a connection-health pill that visibly explains stale data when SSE disconnects.
- Pro: Real-time feel; cuts daemon polling load; better mobile battery.
- Con: Backend ↔ frontend coordination; subscription lifecycles; need fallback for non-supporting events.
- Cost: Medium. ~per-store migration + shared subscription helper.

### F8 — "Mobile/Embed Parity" (Responsive Reflow)
Treat mobile + embed as first-class. Reflow panels into stack layouts at narrow widths; embed mode renders a curated subset (Overview + Fleet + Activity Stream only) with reduced chrome.
- Pro: Multiplies utility for on-call mobile + sidecar use cases. Aligns with mobile companion direction.
- Con: Wide scope; every panel needs responsive review.
- Cost: High. ~each panel + theme breakpoints.

### F9 — "Action Toolkit" (Safe Operations)
Cross-HUD action infrastructure: one `ConfirmDialog`, one optimistic-action + rollback pattern, one toast taxonomy, one retry/error-card pattern, one audit side-drawer for actions taken in-session. Closes the P0–P1 gaps from the Labs Prime-Time plan and generalizes them.
- Pro: Foundational. Lifts every panel that mutates state. Reduces accidental destructive ops.
- Con: Coordination work — needs to integrate with each domain handler.
- Cost: Medium. Could ship in 2–3 days as primitives + 1 week to retrofit panels.

## Cross-Pollination (Pairings + Tensions)

| Pair | Relationship | Notes |
|---|---|---|
| **F2 + F9** | Reinforcing | Triage cards must trigger safe actions — F9 is the substrate F2 spends. Ship together. |
| **F5 + F3** | Reinforcing | Keyboard densification (F3) needs uniform panel structure (F5). Decompose first, then layer keys. |
| **F5 + F7** | Reinforcing | Decomposing forces store extraction, which is the natural moment to swap polling for SSE. |
| **F4 + F2** | Reinforcing | Calmer theme + assertive triage cards = signal-to-noise lift. Glow reserved for hot states. |
| **F1 + F6** | Tension | Both touch IA. F1 hides; F6 restructures. Don't do both in one slice. |
| **F8 + F5** | Tension | Responsive review on top of monoliths is painful — easier post-decomp. F8 best after F5. |
| **F9 + everything** | Foundational | Almost every other framing depends on the action toolkit existing. Sequence first. |

## Convergence

Two coherent slices emerge, each shippable in 1–2 weeks:

### Slice A — "Operator Inbox" (F9 + F2 + F4-lite)
Highest user-visible value, low IA churn.

- **F9 first** as a 2–3 day primitive sub-slice (shared `ConfirmDialog`, `ActionToast`, `ErrorCard`, `useAction()` helper, audit drawer).
- **F2 second** rebuilds `OverviewPanel` around an inbox of pressure cards backed by F9. Each card links to (a) detail, (b) one-click safe action, (c) "send to Dispatch" handoff.
- **F4-lite** lands as a token tightening pass: glow reserved for alert states, scanlines off by default, agent palette reduced to accent + variants — only in the new Overview + shell chrome to start.

### Slice B — "Foundations" (F5 + F7 + F3)
Internal refactor that makes everything cheaper from here on. Best after Slice A so it inherits the F9 primitives.

- **F5** decomposes the 5 monolith panels into <300-line composed views using the existing shared primitives. Move filter/search/sort/select state into stores. Land panel-by-panel.
- **F7** swaps polling for SSE during the same decomposition window — natural time to extract store internals.
- **F3** layers uniform keyboard nav (j/k/Enter/x/g g/G) on top of the now-uniform panels.

### Deferred / Not Recommended Now
- **F1 + F6 (IA changes):** No clear user signal that the current grouped nav is broken. Defer until F2 + F5 reveal what people actually use.
- **F8 (Responsive):** Wait until F5 leaves the panels in a state where reflow is cheap.

## Recommendation

Ship **Slice A (Operator Inbox)** next. ~1 week. User-visible. Closes prior Labs Prime-Time P0/P1 gaps as a side effect. Establishes the action substrate every later slice will reuse. Slice B follows as a 2-week foundations push.

**Tradeoff:** Slice A is mostly additive surface area, not the structural cleanup the codebase needs. If we ship Slice A without committing to Slice B next, the panel monoliths keep accruing and the third UX slice will be expensive.

## Open Questions

- Which "pressure" types are in scope for the first inbox? Suggested: conflicts, blocked tasks, pending approvals, down servers, orphan sessions, RBAC denials, stale sessions. (~7 cards.)
- Do we still want CRT scanlines as an opt-in (`document.body.classList.toggle('scanlines')` exists in `App.svelte:148-150`) or remove entirely?
- F6 / F8 — confirm we are explicitly deferring, not dropping.

## Sources

- [S1] `internal/hud/frontend/src/App.svelte:181-470`
- [S2] `internal/hud/frontend/src/lib/stores/router.svelte.ts:22-114`
- [S3] Command: `wc -l internal/hud/frontend/src/lib/components/*.svelte`
- [S4] `internal/hud/frontend/src/lib/components/OverviewPanel.svelte:117-146`; `ls internal/hud/frontend/src/lib/stores/` (41 entries)
- [S5] `internal/hud/frontend/src/lib/components/shared/` (BulkToolbar, ConfirmDialog, DataTable, DetailDrawer, EmptyState, FilterBar, LabsAccessBar, MetricCard, PanelShell, ViewShell); `.loom/18-research-hud-ux-continuation-2026-03-13.md:21-23`
- [S6] `internal/hud/frontend/src/lib/styles/theme.css:1-80`
- [S7] `internal/hud/frontend/src/lib/components/OverviewPanel.svelte:233-289` (`heroSummary` derivation) and `292-...` (`attentionLanes`)
- [S8] `.loom/31-hud-labs-prime-time-plan.md:35-50` (P0/P1 table)
- [S9] `.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md` (Spectator phases 0–5 shipped); `internal/hud/frontend/src/lib/stores/events.svelte.ts`
