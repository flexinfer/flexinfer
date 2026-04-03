# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Technical debt inventory (cycle 6): `tech-debt-inventory-cycle6.md`
- Technical debt ranking (cycle 6): `tech-debt-priority-cycle6.md`
- Technical debt plan (cycle 6): `tech-debt-plan-cycle6.md`
- Research (mobile app + HUD polish, 2026-03-31): `70-research-mobile-hud-polish-2026-03-31.md`
- Product spec (mobile app + HUD polish, 2026-03-31): `71-product-spec-mobile-hud-polish-2026-03-31.md`
- Implementation plan (mobile app + HUD polish, 2026-03-31): `72-implementation-plan-mobile-hud-polish-2026-03-31.md`
- Implementation plan (universal hooks GitOps first, 2026-03-25): `62-implementation-plan-universal-hooks-gitops-2026-03-25.md`
- Implementation plan (planning baseline refresh, 2026-03-26): `63-implementation-plan-planning-baseline-refresh-2026-03-26.md`
- Planning (next-gen skills, agents, orchestration, 2026-03-29): `64-planning-next-gen-skills-agents-orchestration-2026-03-29.md`
- Technical debt plan (cycle 4): `tech-debt-plan-cycle4.md`
- Technical debt plan (cycle 5): `tech-debt-plan-cycle5.md`
- Research (developer setup flow, 2026-03-18): `19-research-developer-setup-flow-2026-03-18.md`
- Product spec (developer setup flow, 2026-03-18): `23-product-spec-developer-setup-flow-2026-03-18.md`
- Implementation plan (developer setup flow, 2026-03-18): `42-implementation-plan-developer-setup-flow-2026-03-18.md`
- Research (HUD/UX continuation, 2026-03-13): `18-research-hud-ux-continuation-2026-03-13.md`
- Product spec (HUD/UX continuation, 2026-03-13): `22-product-spec-hud-ux-continuation-2026-03-13.md`
- Implementation plan (HUD/UX continuation, 2026-03-13): `41-implementation-plan-hud-ux-continuation-2026-03-13.md`
- Research (codebase indexer benchmark operability, 2026-03-11): `17-research-codebase-indexer-benchmark-operability-2026-03-11.md`
- Research (mobile companion): `10-research.md`
- Research (OpenAI Responses + Loom tool/context integration): `15-research-openai-responses-tool-context-2026-03-04.md`
- Product spec (mobile companion): `20-product-spec.md`
- Product spec (OpenAI Responses orchestration): `21-product-spec-openai-responses-orchestration-2026-03-04.md`
- Implementation plan (mobile companion): `30-implementation-plan.md`
- Implementation plan (codebase indexer benchmark phase 2, 2026-03-11): `39-implementation-plan-codebase-indexer-benchmark-phase2-2026-03-11.md`
- Prior HUD/TUI slice plan: `54-ralph-iteration-plan-hud-tui-presence-2026-02-17.md`
- Prior HUD/TUI slice handoff: `55-ralph-slice-handoff-hud-tui-presence-2026-02-17.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`

## Current Planning Addendum (2026-04-02)

- Active planning slice: Cycle 6 technical debt execution, with HUD/runtime decomposition now leading the queue.
- New planning docs:
  - `tech-debt-inventory-cycle6.md`
  - `tech-debt-priority-cycle6.md`
  - `tech-debt-plan-cycle6.md`
- Recent execution status:
  - `DEBT-062` landed: race CI no longer pulls `cmd/mcp-orchestra` into the headerless `fi-accel` lane
  - `DEBT-063` landed: iOS SwiftPM tests run cleanly again and the generated Xcode project now includes `AttentionLanesCard.swift`
  - `DEBT-064` is the next active slice: reduce `internal/hud/app.go` to a small composition root by moving startup wiring into focused helpers
- Execution stance for this wave:
  - keep structural changes mostly mechanical first
  - preserve HUD startup flags, route behavior, and monitor ordering while splitting code
  - continue verifying every slice with real local checks plus GitLab CI before merge

## Current Planning Addendum (2026-03-31)

- Active planning slice: UX and functionality polish across the iOS companion app and the HUD web dashboard.
- New planning docs:
  - `70-research-mobile-hud-polish-2026-03-31.md`
  - `71-product-spec-mobile-hud-polish-2026-03-31.md`
  - `72-implementation-plan-mobile-hud-polish-2026-03-31.md`
- Planning stance for this slice:
  - prioritize information architecture, operator flow clarity, and shared interaction patterns over adding net-new risky controls
  - keep mobile mutations scoped to already-approved high-confidence flows unless the product spec explicitly re-baselines that boundary
  - use direct file reads for HUD/mobile frontend evidence because `codebase_memory` still has Go-only coverage in this session
  - 2026-04-01 continuation slice landed on top of this plan: backend attention-lane contract enrichment, iOS dashboard "Next Attention" surfacing, and a more action-oriented HUD overview rail

## Current Planning Addendum (2026-03-18)

- Active planning slice: developer setup/onboarding improvement centered on a proposed first-class `loom setup` command.
- New planning docs:
  - `19-research-developer-setup-flow-2026-03-18.md`
  - `23-product-spec-developer-setup-flow-2026-03-18.md`
  - `42-implementation-plan-developer-setup-flow-2026-03-18.md`
- This session used CLI fallback for tool/runtime inventory because top-level MCP resource discovery returned empty results.

## Current State (2026-03-13)

**Branch**: `codex/hud-ux` at `df5707b`

**Current planning thread**:
- Fresh main-based worktree for continuing HUD and broader UX work without touching unrelated local edits in the primary checkout.
- This branch starts from `main` and keeps `codex/hud-view-fixes` as a reference lane instead of reusing it blindly.
- Immediate focus is shared HUD interaction polish plus roadmap-backed UX follow-through in fleet orchestration, server catalog, and HUD/TUI visibility parity.
- HUD layout/data-alignment slice `db3aa85` is now carried into the `codex/hud-ux` working tree and has passed a frontend build plus focused `internal/hud` Go tests.

**HUD/UX reference branch**:
- `codex/hud-view-fixes` remains available at `3b4949f` and is `10` commits ahead / `1` commit behind `main`.
- Its current diff touches `KnowledgePanel`, `SandboxPanel`, `ServersPanel`, `TasksPanel`, shared `FilterBar`, shared `DataTable`, plus HUD CI/build files, so it is a useful cherry-pick/review pool rather than something to merge wholesale.

**Tooling baseline**:
- Loom is running in loom-mode with `46` registered servers and `483` tools.
- `codebase_memory` is healthy for Go (`7861` indexed chunks) but still has `0` TypeScript/JavaScript chunks, so HUD frontend work needs direct file reads in addition to semantic search.

## Active Workstreams

| Track | Status | Key Docs |
|-------|--------|----------|
| HUD/UX continuation | New branch, planning ready | `18-research-hud-ux-continuation-2026-03-13.md`, `22-product-spec-hud-ux-continuation-2026-03-13.md`, `41-implementation-plan-hud-ux-continuation-2026-03-13.md` |
| HUD/TUI presence follow-through | Prior slice completed, follow-ups open | `54-ralph-iteration-plan-hud-tui-presence-2026-02-17.md`, `55-ralph-slice-handoff-hud-tui-presence-2026-02-17.md` |
| Agent lifecycle contract convergence | Open, highest-value refactor | `ROADMAP.md` issue `#21`, `codex/backlog/21` |
| Fleet orchestration UX | Open roadmap slice | `ROADMAP.md`, issue `#13` |
| Catalog/discovery UX | Open roadmap slice | `ROADMAP.md`, issue `#14` |
| Unify visibility epic | Planned | `35-simplification-epics.md`, issue `#66` |

## Risks

- `codex/hud-view-fixes` is not merge-ready as-is; it needs selective carry-forward or a rebase/cherry-pick pass.
- `ROADMAP.md` still points `.loom/10-research.md`, `.loom/20-product-spec.md`, and `.loom/30-implementation-plan.md` at HUD overhaul docs even though those files currently serve the mobile companion thread.
- Current semantic code indexing does not cover the HUD frontend, which raises the cost of broad TypeScript refactors unless the indexer is refreshed for JS/TS.

## Sources

- Command: `git worktree add -b codex/hud-ux ../loom-core-hud-ux main`
- Command: `git log --oneline --decorate --no-merges main..codex/hud-view-fixes`
- Command: `git diff --stat main..codex/hud-view-fixes`
- Command: `git cherry-pick --no-commit db3aa85`
- Command: `pnpm --dir internal/hud/frontend install --frozen-lockfile`
- Command: `pnpm --dir internal/hud/frontend build`
- Command: `go test ./internal/hud/... -count=1`
- Command: `git rev-list --left-right --count main...codex/hud-view-fixes`
- `ROADMAP.md:36`
- `ROADMAP.md:212`
- `ROADMAP.md:219`
- `ROADMAP.md:237`
- `internal/hud/frontend/src/App.svelte:35`
- `internal/hud/frontend/src/App.svelte:148`
