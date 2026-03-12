# MCP Inventory

_Last verified: 2026-03-11_

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
| activeProxySessions | `7` |
| running local servers | `agent_context`, `codebase_memory`, `devbox`, `k8s_harvester_infra`, `longhorn_k3s`, `ops_mcp`, `server_mgmt` |

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
- `k8s_apps_k3s`
- `flux`
- `quality`
- `devbox`

## Codebase Index Readiness

Current session status:
- `codebase_memory__codebase_stats(repo_id="loom-core")` returned `7861` indexed chunks.
- Indexed chunk counts were `4533` functions, `1880` methods, `790` classes, and `626` modules.
- The current index is Go-only (`7861` Go chunks, `0` TypeScript/JavaScript/Python/Rust chunks), which matches the primary backend scope of this repo.

Planning implication:
- Semantic code search is available and healthy for Go backend work.
- HUD frontend artifacts under `internal/hud/frontend/dist` should still be inspected with direct file reads and repo commands because the current index is not carrying TypeScript chunks.

## Constraints

- Some servers are registered but idle until first use; `running: false` in `loom://servers` does not imply unavailable.
- Deployment verification still requires live GitLab/Kubernetes calls because loom inventory only reports tool availability, not repo pipeline state.

## Sources

- Tool call: `list_mcp_resources` (2026-03-11)
- Tool call: `list_mcp_resource_templates` (2026-03-11)
- Tool call: `read_mcp_resource(server="loom", uri="loom://config")` (2026-03-11)
- Tool call: `read_mcp_resource(server="loom", uri="loom://servers")` (2026-03-11)
- Tool call: `read_mcp_resource(server="loom", uri="loom://tools/index")` (2026-03-11)
- Tool call: `codebase_memory__codebase_stats(repo_id="loom-core")` (2026-03-11)
