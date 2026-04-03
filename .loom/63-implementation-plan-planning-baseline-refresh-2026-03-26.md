# Implementation Plan: Planning Baseline Refresh (2026-03-26)

## Goal

Refresh the repo-local Loom Context Pack so the next planning or implementation pass starts from live workspace state, live loom-mode inventory, and current roadmap priorities instead of stale assumptions.

## Non-Goals

- Re-baseline the mobile companion track stored in `.loom/20-product-spec.md` and `.loom/30-implementation-plan.md`.
- Start implementation on a roadmap item during this planning-only pass.
- Reindex the repo unless codebase-memory health or coverage is inadequate for the intended slice.

## Acceptance Criteria

- `.loom/00-workspace-snapshot.md` reflects the current working tree.
- `.loom/00-mcp-inventory.md` reflects live loom-mode resources and current in-session health.
- `.loom/00-index.md` points future agents to this refresh and to the most relevant current planning docs.
- The next execution slice is narrowed to roadmap-backed candidates rather than an open-ended “figure out what to do next.”

## Facts Found

- The current checkout is `main...origin/main` with local changes in `mcp/context/skills-registry.yaml` and untracked planning docs including `.loom/62-implementation-plan-universal-hooks-gitops-2026-03-25.md`, `docs/roadmap-reconciliation-2026-03-25.md`, and `docs/roadmap-reconciliation-2026-03-26.md`. [S1]
- Loom-mode is active in this session, with `46` registered servers and `498` tools on the `full` profile. [S2]
- The local daemon currently has active processes for `gitlab`, `codebase_memory`, `devbox`, and `agent_context`. [S2]
- `codebase_memory__codebase_stats(repo_id="loom-core")` succeeded and reported `7861` indexed Go chunks, so Go-focused research and symbol tracing are immediately available. [S3]
- `agent_context`, `gitlab`, `codebase_memory`, and `devbox` each expose their expected paged tool inventories through loom resources, which means planning can use resource-backed inventory rather than CLI fallback. [S4]
- The roadmap still marks agent contract convergence (`#21`), test coverage (`#2`), onboarding/docs consistency (`#6`), MCP catalog/discovery (`#14`), and unify visibility (`#66`) as active/open tracks. [S5]

## Assumptions

- The untracked `.loom/62-...` and `docs/roadmap-reconciliation-2026-03-2x.md` files are intentional user work and should remain untouched during this refresh. [S1]
- The most useful outcome of this run is a clean planning baseline, not another top-level spec rewrite for older threads.

## Recommended Next Slice

Choose the next implementation thread from these roadmap-backed options:

1. Agent contract convergence (`#21`)
   - Best fit if we want a high-leverage refactor inside `cmd/loom/cmd_agent.go`, `internal/hud/bridge/agent.go`, and `internal/hud/api_agent.go`. [S5]
2. Onboarding and docs consistency (`#6`)
   - Best fit if the goal is reducing setup friction and reconciling the docs/runtime mismatch hinted by the current workspace changes and planning artifacts. [S1] [S5]
3. MCP catalog and discovery (`#14`)
   - Best fit if we want a user-facing CLI/HUD slice that capitalizes on the now-verified paged loom inventory surfaces. [S2] [S4] [S5]

## Execution Plan

### Phase 1: Preserve the baseline

- Treat current uncommitted workspace changes as baseline context and keep future edits scoped to the chosen slice. [S1]
- Re-run `workspace_snapshot.py` before substantial implementation if the working tree changes materially. [S1]

### Phase 2: Pick the execution lane

- If the next task is internal contract cleanup, start from issue `#21` and the shared contract seam noted in the roadmap. [S5]
- If the next task is operator/developer UX, prefer `#6` or `#14` because the refreshed tool inventory now gives a concrete source of truth for setup/catalog surfaces. [S2] [S4] [S5]

### Phase 3: Validate index needs early

- For Go-heavy slices, use `codebase_memory` search/definition/context calls immediately. [S3] [S4]
- For frontend or doc-heavy slices, rely on direct file reads first and only reindex if broader non-Go semantic coverage is required. [S3]

### Phase 4: Keep context pack aligned during execution

- Record new decisions in `.loom/40-decisions.md`.
- Append execution notes in `.loom/50-worklog.md`.
- Add a dated research/spec/plan trio only when the next slice diverges materially from the existing named threads.

## Sources

- [`.loom/00-workspace-snapshot.md`](/Users/cblevins/workspace/services/loom-core/.loom/00-workspace-snapshot.md)
- [`ROADMAP.md`](/Users/cblevins/workspace/services/loom-core/ROADMAP.md)
- Tool call: `read_mcp_resource(server="loom", uri="loom://config")` (2026-03-26)
- Tool call: `read_mcp_resource(server="loom", uri="loom://tools/index")` (2026-03-26)
- Tool call: `read_mcp_resource(server="loom", uri="loom://tools/server/agent_context/page/1")` (2026-03-26)
- Tool call: `read_mcp_resource(server="loom", uri="loom://tools/server/gitlab/page/1")` (2026-03-26)
- Tool call: `read_mcp_resource(server="loom", uri="loom://tools/server/codebase_memory/page/1")` (2026-03-26)
- Tool call: `read_mcp_resource(server="loom", uri="loom://tools/server/devbox/page/1")` (2026-03-26)
- Tool call: `codebase_memory__codebase_stats(repo_id="loom-core")` (2026-03-26)

## Source Notes

- [S1] [`.loom/00-workspace-snapshot.md`](/Users/cblevins/workspace/services/loom-core/.loom/00-workspace-snapshot.md)
- [S2] Tool call: `read_mcp_resource(server="loom", uri="loom://config")` (2026-03-26)
- [S3] Tool call: `codebase_memory__codebase_stats(repo_id="loom-core")` (2026-03-26)
- [S4] Tool calls: `read_mcp_resource(server="loom", uri="loom://tools/index")`, `read_mcp_resource(server="loom", uri="loom://tools/server/agent_context/page/1")`, `read_mcp_resource(server="loom", uri="loom://tools/server/gitlab/page/1")`, `read_mcp_resource(server="loom", uri="loom://tools/server/codebase_memory/page/1")`, `read_mcp_resource(server="loom", uri="loom://tools/server/devbox/page/1")` (2026-03-26)
- [S5] [`ROADMAP.md`](/Users/cblevins/workspace/services/loom-core/ROADMAP.md)
