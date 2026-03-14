# Implementation Plan: HUD/UX Continuation (2026-03-13)

## Outcome

Turn `codex/hud-ux` into the working branch for the next HUD/UX slice, beginning with selective reuse from `codex/hud-view-fixes` and then landing roadmap-aligned interaction improvements. [S1] [S2]

## Phase 0: Baseline and Carry-Forward Review

1. Review the delta on `codex/hud-view-fixes` commit-by-commit.
2. Classify each change as one of:
   - cherry-pick directly
   - reimplement on top of `main`
   - drop as stale
3. Prioritize shared-component and panel-layout changes before build-output churn.

Evidence already gathered:
- The reference branch is `10` ahead / `1` behind `main`. [S3]
- The diff is concentrated in `KnowledgePanel`, `SandboxPanel`, `ServersPanel`, `TasksPanel`, `FilterBar`, `DataTable`, and HUD CI/build files. [S4]

## Phase 1: Shared UX Primitives

Target files:
- `internal/hud/frontend/src/App.svelte`
- `internal/hud/frontend/src/lib/components/shared/FilterBar.svelte`
- `internal/hud/frontend/src/lib/components/shared/DataTable.svelte`

Goals:
- tighten keyboard/search affordances from the app shell
- make filter reset/clear behavior more reusable
- improve dense table ergonomics before panel-specific work

Why this first:
- `App.svelte` still owns global navigation and shortcut behavior. [S5]
- `FilterBar` remains intentionally minimal, so small shared improvements can benefit several panels at once. [S6]

## Phase 2: Panel Follow-Through

Primary panel candidates:
- `internal/hud/frontend/src/lib/components/TasksPanel.svelte`
- `internal/hud/frontend/src/lib/components/ServersPanel.svelte`
- `internal/hud/frontend/src/lib/components/KnowledgePanel.svelte`
- `internal/hud/frontend/src/lib/components/SandboxPanel.svelte`

Goals:
- carry forward still-valid layout improvements from `codex/hud-view-fixes`
- reduce duplicated filter/view-state glue
- improve readability and actionability in high-signal operator panels

Notes:
- `TasksPanel.svelte` is a good first candidate because it already mixes search, grouped/flat display, sorting, and bulk actions in one surface. [S7]

## Phase 3: Roadmap UX Slices

Once shared primitives and carry-forward cleanup are stable, choose one of these as the next bounded slice:

1. Fleet orchestration UX
   - dispatch flow
   - parallel progress visibility
   - claim-conflict cues
2. Catalog/discovery UX
   - browse/enable/disable flow
   - health visibility
3. Visibility parity
   - continue HUD/TUI follow-through
   - consider VirtualList or similar handling for high-cardinality datasets

Roadmap basis:
- Fleet orchestration UX remains open. [S8]
- Catalog/discovery UX remains open. [S9]
- Visibility unification remains an active simplification epic. [S10]
- Prior HUD/TUI slice explicitly recommended VirtualList-backed rendering as a next action. [S11]

## Validation

Before code changes:
- `git log --oneline --decorate --no-merges main..codex/hud-view-fixes`
- `git diff --stat main..codex/hud-view-fixes`

After each frontend slice:
- `pnpm --dir internal/hud/frontend build`

After mixed frontend/backend slices:
- `go test ./internal/hud/... -count=1`
- `go test ./internal/tui/... -count=1`

If shared primitives change broadly:
- `go test ./...`

## Risks

- Carrying forward too much from `codex/hud-view-fixes` at once will mix branch triage with UX work and slow feedback.
- TypeScript/Svelte files are not currently represented in `codebase_memory`, so broad frontend refactors will require more manual inspection. [S12]
- The roadmap still references top-level HUD docs that now point to the mobile track, so future doc updates should keep the dated HUD/UX continuation set visible. [S13]

## Sources

- [S1] Command: `git worktree add -b codex/hud-ux ../loom-core-hud-ux main`
- [S2] `.loom/22-product-spec-hud-ux-continuation-2026-03-13.md`
- [S3] Command: `git rev-list --left-right --count main...codex/hud-view-fixes`
- [S4] Command: `git diff --stat main..codex/hud-view-fixes`
- [S5] `internal/hud/frontend/src/App.svelte:48`
- [S6] `internal/hud/frontend/src/lib/components/shared/FilterBar.svelte:27`
- [S7] `internal/hud/frontend/src/lib/components/TasksPanel.svelte:66`
- [S8] `ROADMAP.md:212`
- [S9] `ROADMAP.md:219`
- [S10] `ROADMAP.md:237`
- [S11] `.loom/55-ralph-slice-handoff-hud-tui-presence-2026-02-17.md:38`
- [S12] Tool call: `codebase_memory__codebase_stats(repo_id="loom-core")` (2026-03-13)
- [S13] `ROADMAP.md:257`
