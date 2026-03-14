# Research: HUD/UX Continuation (2026-03-13)

## Question

What is the cleanest way to resume HUD and broader UX work from a fresh worktree without losing useful prior iteration context?

## Findings

### F1: The large HUD overhaul is already shipped, but roadmap UX follow-through remains open

- The roadmap marks the M1-M4 HUD overhaul as complete, including grouped navigation, shared primitives, accessibility, and panel migrations. [S1]
- The next open UX-heavy roadmap slices are fleet orchestration UX, catalog/discovery UX, and the broader "Unify Visibility" simplification epic. [S2] [S3] [S4]

### F2: The app shell still concentrates a lot of navigation and coordination behavior

- `App.svelte` is still the central place for top-level HUD bootstrapping, global keyboard shortcuts, router navigation, and badge derivation. [S5] [S6]
- That makes it a natural leverage point for UX polish, but it also means broad UI changes can sprawl quickly unless shared behavior moves into more focused stores or primitives.

### F3: Shared filter UX exists, but higher-level interaction patterns are still panel-owned

- `FilterBar.svelte` provides debounced search, dropdown filters, and a result counter, but it does not own clear/reset patterns or richer filter affordances. [S7]
- `TasksPanel.svelte` still manages search/filter/reset state locally and layers additional grouped/flat presentation logic on top. [S8] [S9]
- This suggests the next HUD UX slice should favor small shared-behavior upgrades over one-off per-panel tweaks.

### F4: There is useful unfinished work on `codex/hud-view-fixes`, but it should be treated as reference material

- The reference branch is `10` commits ahead and `1` commit behind `main`, so it is close enough to mine for work but not clean enough to treat as the new baseline. [S10]
- Its diff is concentrated in `KnowledgePanel`, `SandboxPanel`, `ServersPanel`, `TasksPanel`, shared `FilterBar`, shared `DataTable`, and HUD CI/build outputs. [S11]

### F5: The last HUD/TUI parity slice already identified the next high-value UX continuation points

- The February HUD/TUI slice completed presence diagnostics store extraction and conflict surfacing, but explicitly left broader Presence modal decomposition and VirtualList-backed rendering as next actions. [S12] [S13]
- Those next actions still line up with the current roadmap focus on operator visibility and high-cardinality orchestration UX.

### F6: Tooling for this branch is good for backend research and adequate for frontend planning, with one caveat

- Loom is running in loom-mode with `46` servers and `483` tools available to the session, including `git_worktree`, `agent_context`, `codebase_memory`, `quality`, and optional `browserkit`. [S14] [S15]
- `codebase_memory` is healthy for Go (`7861` chunks) but has no TypeScript/Svelte coverage, so HUD frontend work will continue to rely on direct file inspection until the index is expanded. [S16]

## Implications

- Start implementation from `codex/hud-ux`, not from the older HUD branch.
- Review `codex/hud-view-fixes` commit-by-commit and selectively carry forward the shared primitive and panel improvements that still fit `main`.
- Prioritize work that improves reuse and operator flow: shared filters/search, dense table ergonomics, high-cardinality rendering, and workflow/catalog discoverability.

## Assumptions

- "Continue work on the HUD and other UX work" means creating a fresh execution lane plus an updated planning baseline, not immediately rebasing or merging `codex/hud-view-fixes`.
- The first implementation slice should stay within HUD/TUI UX and avoid reopening the mobile companion planning track.

## Open Questions

- Which parts of `codex/hud-view-fixes` are still worth carrying forward unchanged?
- Should the next slice target shared primitives first, or go directly at one roadmap feature such as dispatch/catalog UX?
- Is it worth refreshing codebase indexing to include TypeScript/Svelte before a larger HUD refactor?

## Sources

- [S1] `ROADMAP.md:36`
- [S2] `ROADMAP.md:212`
- [S3] `ROADMAP.md:219`
- [S4] `ROADMAP.md:237`
- [S5] `internal/hud/frontend/src/App.svelte:35`
- [S6] `internal/hud/frontend/src/App.svelte:148`
- [S7] `internal/hud/frontend/src/lib/components/shared/FilterBar.svelte:15`
- [S8] `internal/hud/frontend/src/lib/components/TasksPanel.svelte:66`
- [S9] `internal/hud/frontend/src/lib/components/TasksPanel.svelte:159`
- [S10] Command: `git rev-list --left-right --count main...codex/hud-view-fixes`
- [S11] Command: `git diff --stat main..codex/hud-view-fixes`
- [S12] `.loom/54-ralph-iteration-plan-hud-tui-presence-2026-02-17.md:16`
- [S13] `.loom/55-ralph-slice-handoff-hud-tui-presence-2026-02-17.md:27`
- [S14] Tool call: `read_mcp_resource(server="loom", uri="loom://config")` (2026-03-13)
- [S15] Tool call: `read_mcp_resource(server="loom", uri="loom://servers")` (2026-03-13)
- [S16] Tool call: `codebase_memory__codebase_stats(repo_id="loom-core")` (2026-03-13)
