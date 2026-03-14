# MCP Inventory

_Last verified: 2026-03-13_

## Why

Capture the MCP/runtime baseline used for planning so later decisions are grounded in the actual session capabilities, not assumptions from prior runs.

## Runtime Mode Detection

This session is exposing top-level loom-mode MCP resources.

Observed behavior:
- `list_mcp_resources` returned `loom://config`, `loom://servers`, `loom://tools/index`, `loom://tools`, and `loom://health`.
- `list_mcp_resource_templates` returned paged loom templates including `loom://tools/page/{page}` and `loom://tools/server/{server}/page/{page}`.

Planning implication:
- Prefer loom resource pagination over CLI fallback for inventory.
- Treat loom daemon state and tool counts as directly inspectable in-session.

## Loom Inventory Snapshot

Snapshot from `read_mcp_resource(server="loom", uri="loom://config")`:

| Field | Value |
|---|---|
| active profile | `full` |
| serverCount | `46` |
| toolCount | `483` |
| activeProxySessions | `3` |
| daemon running | `true` |
| drainReady | `true` |
| running local processes | `morph_embeddings`, `codebase_memory`, `prometheus`, `agent_context`, `devbox`, `git_worktree` |

Snapshot from `read_mcp_resource(server="loom", uri="loom://tools/index")`:

| Field | Value |
|---|---|
| server | `all` |
| totalTools | `483` |
| totalPages | `5` |
| pageSize | `100` |

Relevant planning tools confirmed available in this session:
- `gitlab`
- `codebase_memory`
- `agent_context`
- `git_worktree`
- `k8s_apps_k3s`
- `flux`
- `quality`
- `devbox`
- `browserkit` (registered, currently idle until first use)

## Codebase Index Readiness

Current session status:
- `codebase_memory__codebase_stats(repo_id="loom-core")` returned `7861` indexed chunks.
- Indexed chunk counts were `4533` functions, `1880` methods, `790` classes, and `626` modules.
- The current index is Go-only (`7861` Go chunks, `0` TypeScript/JavaScript/Python/Rust chunks).

Planning implication:
- Semantic code search is available and healthy for Go backend work.
- HUD frontend work under `internal/hud/frontend/src` still requires direct file reads and repo commands because the current index is not carrying TypeScript/Svelte chunks.
- For visual regression or layout verification, prefer activating `browserkit` on demand rather than assuming it is already warm.

## Constraints

- Some servers are registered but idle until first use; `running: false` in `loom://servers` does not imply unavailable.
- Deployment verification still requires live GitLab/Kubernetes calls because loom inventory only reports tool availability, not repo pipeline state.
- HUD/UX planning in this session should assume mixed tooling: Go search through `codebase_memory`, frontend inspection through direct file reads, and optional browser screenshots when visual confirmation becomes necessary.

## Sources

- Tool call: `list_mcp_resources` (2026-03-13)
- Tool call: `list_mcp_resource_templates` (2026-03-13)
- Tool call: `read_mcp_resource(server="loom", uri="loom://config")` (2026-03-13)
- Tool call: `read_mcp_resource(server="loom", uri="loom://servers")` (2026-03-13)
- Tool call: `read_mcp_resource(server="loom", uri="loom://tools/index")` (2026-03-13)
- Tool call: `codebase_memory__codebase_stats(repo_id="loom-core")` (2026-03-13)
