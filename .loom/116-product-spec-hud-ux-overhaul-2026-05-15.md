# Product Spec — HUD UI/UX Overhaul (2026-05-15)

Companion: [115-brainstorm-hud-ux-improvements-2026-05-15.md](115-brainstorm-hud-ux-improvements-2026-05-15.md)
Status: proposed
Cycle: 2026-05-15 → ~2026-06-12 (≈3–4 weeks)

## Goal / Outcome

Lift the HUD from a data-rich dashboard into a triage-first operator console: every visible pressure point becomes an actionable card backed by safe action primitives; the five 1k+ panel monoliths are decomposed into composed views; polling is replaced with SSE-pushed updates; the visual language is tightened so glow/decoration carry meaning instead of texture; and the same layout reflows cleanly on mobile + embed.

## Users

- **Operators on call.** Need to see what is wrong and act on it in seconds. Today's HUD shows the data but buries the resolution path.
- **Multi-agent orchestrators.** Run dispatch / approve workflows / drain orphans across many agents. Need consistent action verbs across panels.
- **Mobile / on-the-go responders.** Use mobile-hud + iOS companion. Need legible reflow, not just a shrunk desktop view.
- **HUD developers (us).** Adding a new panel today means cloning a 1k+ monolith. Need composed primitives.

## In Scope

**Slice A — Operator Inbox (~1 week, ships first)**

1. Action toolkit primitives (`F9`): shared `ConfirmDialog`, `ActionToast`, `ErrorCard`, `useAction()` hook, in-session action audit drawer.
2. Triage-first Overview (`F2`): pressure-stack rebuild around the existing `heroSummary` + `attentionLanes` derivation, with 7 first-class card types.
3. Theme tightening (`F4-full`): tokens + every panel — glow reserved for alert states, scanlines opt-in only, agent palette reduced to accent + 3 variants.

**Slice B — Foundations (~2 weeks, follows A)**

4. Monolith decomposition (`F5`): five panels (`FleetPanel`, `OverviewPanel`, `TasksPanel`, `SandboxPanel`, `SpawnPanel`) reduced to <300 lines each via shared primitives; logic into stores.
5. Streaming-first stores (`F7`): wire the five panels' stores to the `hud.*` SSE bus; polling becomes the fallback, not the default.
6. Keyboard densification (`F3`): uniform j/k/Enter/x/g g/G across composed panels; `Cmd+P` quick-jump.
7. Responsive + embed reflow (`F8`): each composed panel collapses to stacked layout under 800 px; `--embed` mode renders Overview + Fleet + Stream subset only.

## Non-Goals

- **No IA reorg.** Keep the seven top-level views (Operations / Infra / Work / Context / Activity / Labs / Mills) and the standalone Overview. `F6` is deferred — revisit after we see how the new Overview is used.
- **No new domains.** Do not add Mills/Spawn/Weaver/etc. visibility beyond what already exists.
- **No backend contract rewrites.** Reuse existing HTTP handlers; if an inbox card needs a missing action endpoint, scope it tightly to that one handler.
- **No replacement of `/api/mobile/v1/*`.** Mobile contract stays frozen.
- **No removal of polling stores.** SSE migration is per-store, with polling kept as fallback.

## Decision Matrix

| ID | Decision | Choice | Rationale |
|---|---|---|---|
| D1 | Where do action primitives live? | `internal/hud/frontend/src/lib/components/shared/action/{ConfirmDialog,ActionToast,ErrorCard}.svelte` + `lib/utils/useAction.svelte.ts` | Co-locate with existing shared primitives; no new top-level. |
| D2 | What kinds of "pressure" appear in the Inbox? | Initial 7: file conflicts, blocked tasks, pending workflow approvals, down servers, orphan sessions, RBAC denials, stale (silent) sessions | Each maps to an existing store + an existing or near-existing action endpoint. |
| D3 | Inbox card data source | Each card has a `from(stores) → CardSpec[]` selector in `lib/utils/inbox.ts`; Overview composes the deck | Single typed contract; easy to add card types later. |
| D4 | Action audit drawer scope | Session-local ring buffer (last 50 actions in memory + `sessionStorage`); not persisted to backend | Avoids new backend tables. Future Mills/audit integration is its own slice. |
| D5 | Monolith decomp target | Each panel becomes a `<300-line PanelRoot.svelte>` + sibling subcomponents under `lib/components/<panel-name>/` | Mirrors existing `Mills/` and `dispatch/` sub-folders. |
| D6 | Store extraction target | All filter/search/sort/select state moves into the matching `*.svelte.ts` store; templates stay declarative | Already-applied pattern in `presenceDiagnostics.svelte.ts`; generalize. |
| D7 | SSE migration shape | Add `subscribe(eventType, handler)` helper in `lib/stores/events.svelte.ts` consumers; polling stays as a `staleAfter` watchdog | Avoids "is the data live?" ambiguity. |
| D8 | Theme glow rule | Glow tokens (`--glow-*`) used only on `--error` / `--warning` / `--accent` semantic states; success/info states render flat | Glow becomes a signal, not a texture. |
| D9 | Scanlines | Off by default; opt-in via Cmd+K → "Toggle scanlines" only | Existing toggle in `App.svelte:148-150` preserved as gear-menu entry. |
| D10 | Agent palette | One accent + 3 variant ramps (claude/codex/gemini → tints of the accent); copilot collapsed into "other" | Reduces palette from 4 to 1+3; identity preserved by row/badge position, not full color. |
| D11 | Keyboard scheme | j/k row nav, Enter drill, Esc back, x select, g g top, G bottom, / focus search (kept), Cmd+P fuzzy jump, ? help (kept) | Vim-familiar; non-conflicting with browser; does not break existing 1-7/a-z view nav. |
| D12 | Mobile breakpoint | 800 px: nav-tabs → bottom-bar; panel content stacks; sub-view chip strip becomes horizontal scroll | Matches existing `@media (max-width: 768px)` block in `App.svelte:878-897` extended. |
| D13 | Embed mode | Library-API `hud.NewEmbedded(opts{Subset: "operator"})` returns curated routes only (Overview + Fleet + Stream); same Svelte build, gated by route allowlist | Reuses UNIFY-2 embed work. |
| D14 | Backward compatibility | `OverviewPanel.svelte` keeps its current route + props; internal rebuild is opaque to consumers | No bookmark/route churn. |
| D15 | Carry-forward from `codex/hud-view-fixes` | Review commit-by-commit during Slice B; pull primitive/layout improvements, drop panel-specific edits superseded by decomp | `.loom/18` already identified this branch as reference. |

## Inbox Card Catalog (Slice A scope)

Each card: `kind`, `severity` (alert/warn/calm), `headline`, `detail`, primary `action` (label + handler), optional `secondaries` (drill, ignore-for-session), optional `groupBy`.

| Card kind | Trigger source | Primary action | Endpoint / handler |
|---|---|---|---|
| `file_conflict` | `coordinationStore.summary.conflict_files > 0` | "Resolve in Dispatch" | Route → Dispatch with conflict pre-selected |
| `blocked_task` | `taskStore.blockedCount > 0` | "Unblock" | `POST /api/tasks/{id}/update` (existing) |
| `pending_approval` | `workflowStore.activeWorkflows.filter(status=waiting_approval)` | "Approve / Reject" | `POST /api/workflows/{id}/approve` (handler exists: `internal/hud/domain/workflow/handler_workflow.go`) |
| `server_down` | `healthStore.downCount > 0` | "Open server diagnostics" | Route → Servers panel filtered |
| `orphan_session` | `fleetStore.unifiedSummary.orphans > 0` | "Reap orphan" | `POST /api/sessions/{id}/end` (existing) |
| `rbac_denied_spike` | `rbacStore.deniedCount > 0 within window` | "Inspect policy" | Route → RBAC (when present) or audit drawer |
| `stale_session` | `liveSessionsStore.staleCount > 0` | "End or recover" | `POST /api/sessions/{id}/end` or `…/recover` |

If a card's endpoint doesn't exist yet, the card still appears with a drill action only (no destructive action). The catalog grows in later slices.

## Visual Language (F4-full)

- **Glow tokens** (`--glow-success`, `--glow-info`) collapse to `0` opacity on neutral/success/info states. Only `--glow-error`, `--glow-warning`, `--glow-accent` remain perceptible. [D8]
- **Scanlines** removed from default body; reachable via Cmd+K. [D9]
- **Agent palette** ramps tied to `--accent`: `--agent-1`, `--agent-2`, `--agent-3` derived via `color-mix()` from accent. Old `--agent-claude/codex/gemini/copilot` kept as deprecated aliases for one minor release. [D10]
- **Panel-area background gradient** (`internal/hud/frontend/src/App.svelte:701-704`) flattened — radial washes off, single tonal lift on cards only.
- **Nav-bar `::after` glow line** kept (it carries identity); panel-internal glows audited and dropped where decorative.

## Acceptance Criteria

**Slice A**
- `lib/components/shared/action/` contains `ConfirmDialog`, `ActionToast`, `ErrorCard`; `useAction.svelte.ts` provides `{ run, pending, error, retry }` with optimistic + rollback support.
- `OverviewPanel.svelte` is reduced from 1504 lines to <500 lines; logic lives in `lib/components/overview/InboxCard.svelte`, `Deck.svelte`, and an `inbox.ts` selector module.
- 7 card kinds render against live stores; at least 4 ship with a primary action wired end-to-end; the remaining 3 ship with a drill action.
- Theme sweep (F4-full) merged: `pnpm build` + screenshots show flat success/info, glow only on alert states; agent badges use 3-variant ramp.
- Manual smoke checklist passes for each card kind (in plan §Validation).
- All shipping changes pass: `pnpm build`, `pnpm lint`, `go test ./internal/hud/...`.

**Slice B**
- Each of `FleetPanel`, `OverviewPanel` (already done in A), `TasksPanel`, `SandboxPanel`, `SpawnPanel` is <300 lines and composes `shared/PanelShell`, `shared/DataTable`, `shared/DetailDrawer`, `shared/FilterBar`.
- Each panel's filter/search/sort/select state lives in its matching store, not the component.
- The five panels' primary stores subscribe to `hud.*` SSE events; the polling tick interval ≥60 s (was 10–30 s), serving as fallback only.
- Keyboard nav: j/k/Enter/x/g g/G works on every `DataTable`; `Cmd+P` opens a fuzzy jump-to-anything palette covering routes + recent entities.
- Responsive: under 800 px, nav becomes a bottom-bar; each panel stacks; manual test on iPhone 17 simulator + iPad simulator.
- Embed subset: `loom hud --embed --subset operator` renders only Overview + Fleet + Stream; verified by route allowlist test in `internal/hud/embed.go`.
- All shipping changes pass quality gate per slice; no `make ci-contracts` mobile-golden diff.

## Success Signals

- Operator triage path ("what's wrong → act") is one-click for ≥4 of the 7 card kinds.
- Adding an 8th panel costs <300 lines instead of ~1000.
- Daemon-side polling load (per Prometheus `hud_api_requests_total` if available; otherwise grep daemon logs) drops measurably after Slice B SSE migration.
- iOS companion + mobile-hud users report layout reads cleanly without zoom.
- A casual operator opening HUD for the first time sees only pressure they need to act on; an empty inbox correctly displays "system nominal".

## Risks (mitigations)

- **Action mis-fires** — confirm dialogs gate destructive operations (D1); audit drawer reveals what was triggered in this session (D4).
- **SSE drift** — `staleAfter` watchdog per store; visible "stale" pill on the connection banner; polling fallback if SSE drops (D7).
- **Theme backlash** — F4-full sweep is bigger than F4-lite. Mitigation: behind a one-line `--theme-tight` toggle for the first release if needed; deprecated agent palette aliases preserved one release.
- **Monolith decomp regressions** — land one panel per merge request with screenshots + a manual smoke checklist; keep old panel as `*.legacy.svelte` for one minor release behind a `?legacy=1` query param.
- **Mobile reflow churn** — only the five decomposed panels guaranteed responsive in Slice B; other panels remain best-effort until their own decomp.
- **Carry-forward conflicts from `codex/hud-view-fixes`** — review explicitly in Slice B kickoff (per `.loom/18`); don't merge wholesale.

## Sources

- Brainstorm: [.loom/115-brainstorm-hud-ux-improvements-2026-05-15.md](115-brainstorm-hud-ux-improvements-2026-05-15.md)
- Prior HUD/UX research: [.loom/18-research-hud-ux-continuation-2026-03-13.md](18-research-hud-ux-continuation-2026-03-13.md)
- Prior HUD/UX spec: [.loom/22-product-spec-hud-ux-continuation-2026-03-13.md](22-product-spec-hud-ux-continuation-2026-03-13.md)
- Labs Prime-Time gaps: [.loom/31-hud-labs-prime-time-plan.md](31-hud-labs-prime-time-plan.md)
- Unify Visibility spec (D13 embed precedent): [.loom/103-product-spec-unify-visibility-2026-05-06.md](103-product-spec-unify-visibility-2026-05-06.md)
- Spectator (SSE substrate): [.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md](99-implementation-plan-agent-telemetry-spectator-2026-05-04.md)
- Shell + routing: `internal/hud/frontend/src/App.svelte:181-470`, `internal/hud/frontend/src/lib/stores/router.svelte.ts:22-114`
- Monolith inventory: Command `wc -l internal/hud/frontend/src/lib/components/*.svelte` (2026-05-15)
- Existing triage logic: `internal/hud/frontend/src/lib/components/OverviewPanel.svelte:233-289`
- Workflow approve handler: `internal/hud/domain/workflow/handler_workflow.go:handleWorkflowApprove`
- Theme tokens: `internal/hud/frontend/src/lib/styles/theme.css:1-80`
