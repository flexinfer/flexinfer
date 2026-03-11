# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Roadmap review + task carve-out (2026-03-07): `39-roadmap-review-and-task-carveout-2026-03-07.md`
- Research (mobile companion): `10-research.md`
- Research (OpenAI Responses + Loom tool/context integration): `15-research-openai-responses-tool-context-2026-03-04.md`
- Research addendum (mobile roadmap/features, external): `13-research-mobile-roadmap-features-2026-02-19.md`
- Product spec (mobile companion): `20-product-spec.md`
- Product spec (OpenAI Responses orchestration): `21-product-spec-openai-responses-orchestration-2026-03-04.md`
- Implementation plan (mobile companion): `30-implementation-plan.md`
- Implementation plan (agent trace + telemetry dashboards): `34-agent-trace-telemetry-dashboard-plan-2026-02-26.md`
- Implementation plan (OpenAI Responses orchestration): `36-implementation-plan-openai-responses-orchestration-2026-03-04.md`
- Mobile API draft: `../docs/MOBILE_COMPANION_API.md`
- Mobile security draft: `../docs/MOBILE_COMPANION_SECURITY.md`
- Historical roadmap mapping: `31-gap-to-backlog-map.md`
- Mobile backlog mapping: `32-mobile-gap-to-backlog-map.md`
- Simplification EPICs (3 EPICs, 21 issues): `35-simplification-epics.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`
- Tech debt inventory: `tech-debt-inventory.md` (all items resolved)
- Tech debt plan: `tech-debt-plan.md` (all 3 waves complete)
- Tech debt priority: `tech-debt-priority.md`

## Current State (2026-03-11)

**Branch**: `codex/google-workspace-finish` at `b220ba9` (main at latest)

**Worktree reconciliation (2026-03-11)**:
- Cleaned 43 worktrees down to 6 (including main checkout and codex-tmp).
- Removed 7 already-merged worktrees, 2 malformed-path worktrees, and 26 stale worktrees (100-335 commits behind main).
- Deleted 39 merged local branches and 34 orphaned stale branches.
- Pruned remote tracking refs.

**Active worktrees**:

| Worktree | Branch | Ahead/Behind | Purpose |
|----------|--------|-------------|---------|
| `services/loom-core` | `codex/google-workspace-finish` | +2/−0 | Google Workspace MCP auth (current) |
| `.worktrees/loom-core-53-rollout-20260309b` | `codex/backlog/53-rbac-audit-rollout2` | +5/−3 | RBAC audit rollout |
| `.worktrees/loom-core-60` | `codex/backlog/21` | +3/−39 | Agent contract convergence |
| `services/loom-core-codex-fi-accel-adoption` | `codex/fi-accel-adoption` | +2/−12 | FI accel adoption (original) |
| `services/loom-core-codex-fi-accel-adoption-clean` | `codex/fi-accel-adoption-rebase` | +1/−0 | FI accel adoption (clean rebase) |
| `.codex-tmp/loom-core-main` | detached HEAD | — | Codex working copy |

**Local branch** `codex/agent-context-consistency` (+3/−0) preserved but not checked out.

**Planning baseline** (carried from 2026-03-07):
- HUD cost/RBAC/OTel visibility already implemented on `main`; stale worktrees for those items are now cleaned up.
- Agent lifecycle contract/surface decomposition remains the highest-value refactor target.
- Coverage at `39.7%` toward `40%+` goal.

## Active Workstreams

| Track | Status | Key Docs |
|-------|--------|----------|
| Google Workspace MCP auth | In progress (current branch) | `codex/google-workspace-finish` |
| Agent lifecycle contract convergence | Open, highest-value refactor | `39-roadmap-review-and-task-carveout-2026-03-07.md`, ROADMAP `#21`, `codex/backlog/21` |
| RBAC audit rollout | Near-main, review pending | `codex/backlog/53-rbac-audit-rollout2` |
| FI accel adoption | Clean rebase ready | `codex/fi-accel-adoption-rebase` |
| Agent context consistency | Fresh branch, not checked out | `codex/agent-context-consistency` |
| Daemon pipeline hardening | Narrow finish pass remains | `39-roadmap-review-and-task-carveout-2026-03-07.md`, ROADMAP `#20` |
| Coverage push | Finish-line: `39.7%` toward `40%+` | ROADMAP `#2` |
| OpenAI Responses M2 | Foundation shipped, bounded follow-up | `36-implementation-plan-openai-responses-orchestration-2026-03-04.md` |

## Risks

- `codex/backlog/21` is 39 commits behind main — needs rebase before merge.
- `codex/fi-accel-adoption` (original) is 12 behind; prefer the clean rebase copy.
- Codebase-memory MCP transport availability still needs follow-up.
- Agent lifecycle surface (`cmd/loom/cmd_agent.go`, `internal/hud/api_agent.go`, `internal/hud/bridge/agent.go`) remains large and high-churn.

## Sources

- Worktree reconciliation: `git worktree list`, `git branch --merged/--no-merged main`, `git rev-list --left-right --count`
- `39-roadmap-review-and-task-carveout-2026-03-07.md`
- `ROADMAP.md`
- `docs/IMPLEMENTATION_STATUS.md`
