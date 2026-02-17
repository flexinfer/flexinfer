# RALPH Iteration Plan

## Review

- Roadmap milestone:
  - `ROADMAP.md` → Immediate Architecture Refactor Focus: "Split oversized HUD panel/state surfaces"
  - `.loom/30-implementation-plan.md` → M4/M5 follow-through on diagnostics/polling ergonomics
- Spec section(s):
  - `.loom/20-product-spec.md` R3/R5 consistency + performance behavior (predictable polling + diagnostics UX)
- Prior decisions to preserve:
  - Presence Diagnostics tab remains the operator surface for context/queue inspection + runtime policy mutation.
  - Queue policy form must avoid polling clobber while the operator is editing.

## Align

- Slice name: HUD/TUI Presence diagnostics isolation + claim-conflict visibility
- Scope in:
  - Move diagnostics API/polling/mutation state out of `PresenceDiagnosticsTab.svelte` into a dedicated store.
  - Keep HUD behavior/API contracts unchanged.
  - Add explicit claim-conflict count/markers in TUI Presence panel to match HUD conflict cues.
  - Add targeted Go tests for new TUI conflict helpers/rendering.
- Scope out:
  - Full Presence panel modal/action refactor.
  - New backend APIs.
  - Broad TUI tab parity (handoffs/diagnostics tabs).
- Acceptance criteria:
  - `PresenceDiagnosticsTab.svelte` no longer owns fetch/polling/mutation orchestration logic.
  - New store drives diagnostics polling + nudge policy form behavior without regressions.
  - TUI presence summary includes conflict count and claims table highlights conflicting files.
  - New/updated tests pass; HUD frontend build succeeds.
- Dependencies/blockers:
  - No external blockers; only local Svelte/Go build correctness.

## Land

- Planned file areas:
  - `internal/hud/frontend/src/lib/stores/presenceDiagnostics.svelte.ts`
  - `internal/hud/frontend/src/lib/components/presence/PresenceDiagnosticsTab.svelte`
  - `internal/tui/panels/presence.go`
  - `internal/tui/panels/presence_test.go`
- Implementation steps:
  1. Extract diagnostics fetch/poll/update state into a dedicated HUD store and expose tab-facing methods.
  2. Rewire diagnostics tab to bind directly to the store and keep existing UI interactions.
  3. Add TUI claim-conflict helpers, render conflict signals, and backfill tests.

## Prove

- Tests to run:
  - `go test ./internal/tui/panels -count=1`
  - `go test ./internal/tui/... -count=1`
- Lint/static checks:
  - `gofmt -w internal/tui/panels/presence.go internal/tui/panels/presence_test.go`
  - `pnpm --dir internal/hud/frontend build`
- CI checks:
  - Local slice verification only in this pass (no remote pipeline trigger).

## Handoff/Harvest

- Docs to update:
  - `ROADMAP.md`
  - `.loom/00-index.md`
  - `.loom/50-worklog.md`
- Agent-context entries to add:
  - `decision`: diagnostics orchestration moved to dedicated store.
  - `finding`: TUI lacked explicit claim-conflict surfacing.
- Next-slice candidates:
  - Continue PresencePanel decomposition by extracting modal action/mutation flows into focused stores/clients.
  - Add VirtualList integration to high-cardinality HUD tables.
