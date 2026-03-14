# Product Spec: HUD/UX Continuation (2026-03-13)

## Goal

Create a clean continuation lane for loom-core HUD and adjacent UX work so the next implementation slice can improve operator workflows without inheriting stale branch state or overwriting mobile-planning docs. [S1] [S2]

## Users

- Loom operators using the HUD for fleet status, task flow, server health, and agent coordination. [S3]
- Contributors resuming HUD/TUI UI work across multiple worktrees and agents. [S4]

## In Scope

- Establish a fresh main-based worktree and planning baseline for HUD/UX work.
- Preserve prior HUD-specific context while keeping mobile companion docs untouched.
- Define the next continuation areas:
  - shared HUD interaction polish
  - high-cardinality list/table ergonomics
  - fleet orchestration UX follow-through
  - catalog/discovery UX follow-through

## Non-Goals

- Replacing the mobile companion planning thread currently living in `.loom/10`, `.loom/20`, and `.loom/30`.
- Broad backend contract rewrites unless they directly block UX delivery.
- Blindly merging `codex/hud-view-fixes` into `main`.

## Product Requirements

### R1: Clean execution lane

- HUD/UX work must live on a fresh worktree/branch that starts from `main`. [S5]
- The branch must remain easy to compare with both `main` and `codex/hud-view-fixes`.

### R2: Discoverable planning context

- `.loom/00-index.md` must link to a HUD-specific research/spec/implementation trio for this branch.
- `.loom/40-decisions.md` and `.loom/50-worklog.md` must record why the new branch exists and how to continue from it.

### R3: Reuse before reinvention

- The next code slice should review carry-forward candidates from `codex/hud-view-fixes`, especially shared primitives and panel layout work. [S6]
- Shared UX improvements should be preferred over panel-specific forks when possible. [S7] [S8]

### R4: Roadmap alignment

- The branch should bias toward open roadmap UX work, especially fleet orchestration, catalog/discovery, and visibility unification. [S9] [S10] [S11]
- Prior HUD/TUI parity follow-ups remain valid candidates if they support those themes. [S12]

## Acceptance Criteria

- New worktree exists at `/Users/cblevins/workspace/services/loom-core-hud-ux` on `codex/hud-ux`.
- New HUD/UX continuation docs exist and are linked from `.loom/00-index.md`.
- The branch strategy is documented as "fresh main-based worktree plus selective carry-forward from `codex/hud-view-fixes`."
- The implementation plan names concrete file areas and validation commands for the next slice.

## Success Signals

- Another agent can enter this worktree and immediately identify the next HUD/UX slice to execute.
- Cherry-pick or reimplementation decisions against `codex/hud-view-fixes` become explicit instead of ad hoc.
- Future HUD work stays aligned with roadmap UX gaps instead of drifting back into already-shipped overhaul work.

## Sources

- [S1] Command: `git worktree add -b codex/hud-ux ../loom-core-hud-ux main`
- [S2] `ROADMAP.md:257`
- [S3] `ROADMAP.md:13`
- [S4] `ROADMAP.md:14`
- [S5] `git status --short --branch` on `/Users/cblevins/workspace/services/loom-core-hud-ux`
- [S6] Command: `git diff --stat main..codex/hud-view-fixes`
- [S7] `internal/hud/frontend/src/lib/components/shared/FilterBar.svelte:15`
- [S8] `internal/hud/frontend/src/lib/components/TasksPanel.svelte:110`
- [S9] `ROADMAP.md:212`
- [S10] `ROADMAP.md:219`
- [S11] `ROADMAP.md:237`
- [S12] `.loom/55-ralph-slice-handoff-hud-tui-presence-2026-02-17.md:36`
