# MCP Inventory

_Last verified: 2026-03-04_

## Why

Capture the active MCP/runtime baseline for planning and context continuity.

## Runtime Mode Detection

Loom-mode is active in this runtime.

Evidence:
- `list_mcp_resources` returned top-level loom resources: `loom://config`, `loom://servers`, `loom://tools/index`, `loom://health`.
- `list_mcp_resource_templates` returned paginated templates: `loom://tools/page/{page}` and `loom://tools/server/{server}/page/{page}`.

## Loom-Mode Inventory Strategy

Used loom proxy resources directly:

- `read_mcp_resource(server="loom", uri="loom://config")`
- `read_mcp_resource(server="loom", uri="loom://servers")`
- `read_mcp_resource(server="loom", uri="loom://tools/index")`
- `read_mcp_resource(server="loom", uri="loom://tools/page/1")`
- `read_mcp_resource(server="loom", uri="loom://tools/server/agent_context/page/1")`

CLI cross-check:
- `./loom tools list --json --page 1 --limit 500`

## Tool Inventory Snapshot

From `loom://tools/index`:

| Field | Value |
|---|---|
| server | `all` |
| totalTools | `472` |
| totalPages | `5` |
| pageSize | `100` |

From `loom://config`:

| Field | Value |
|---|---|
| active profile | `full` |
| configured servers | `44` |
| tool count | `472` |

CLI cross-check (`--limit 500`):

| Field | Value |
|---|---|
| totalTools | `472` |
| totalPages | `1` |
| pageSize | `500` |
| serverCount | `44` |

## Tool Distribution by Server Prefix (Top 20)

- `agent_context`: 80
- `jobsearch`: 66
- `gitlab`: 30
- `flexinfer`: 19
- `codebase_memory`: 17
- `github`: 11
- `git`: 11
- `devbox`: 11
- `ops_mcp`: 10
- `neo4j`: 10
- `redis`: 10
- `sequentialthinking`: 9
- `memory`: 9
- `godot_debug`: 9
- `prometheus`: 9
- `loki`: 9
- `grafana`: 9
- `flux`: 9
- `cloudflare`: 8
- `k8s_apps_k3s`: 8

## Codebase Index Readiness

Current status:
- `codebase_stats(repo_id="loom-core")` initially returned `total_chunks: 0`.
- Embeddings-enabled full refresh failed with backend error:
  - job `3131f65d0181762d`
  - error: `morph API HTTP 400: The decoder prompt cannot be empty`
- Fallback index started with `embeddings=false`:
  - job `8141921e222489e6`
  - status at latest poll during planning: `running` (partial progress; poll for current counters).

Planning implication:
- Proceed with `rg` + direct file reads while fallback indexing completes.
- Use semantic search only after index finishes successfully.

## Constraints

- Loom-mode inventory is available and preferred over CLI.
- Tool index pagination differs by endpoint:
  - `loom://tools/index` defaults to 100/page.
  - CLI can be forced to larger page size.
- Codebase semantic embeddings are currently unavailable due upstream embed error; lexical index fallback is in progress.

## Sources

- Tool call: `list_mcp_resources` (2026-03-04)
- Tool call: `list_mcp_resource_templates` (2026-03-04)
- Tool call: `read_mcp_resource("loom://config")` (2026-03-04)
- Tool call: `read_mcp_resource("loom://servers")` (2026-03-04)
- Tool call: `read_mcp_resource("loom://tools/index")` (2026-03-04)
- Tool call: `read_mcp_resource("loom://tools/page/1")` (2026-03-04)
- Tool call: `read_mcp_resource("loom://tools/server/agent_context/page/1")` (2026-03-04)
- Command: `./loom tools list --json --page 1 --limit 500` (2026-03-04)
- Tool call: `codebase_stats(repo_id="loom-core")` (2026-03-04)
- Tool call: `codebase_index_start(repo_id="loom-core", full_refresh=true)` job `3131f65d0181762d` (2026-03-04)
- Tool call: `codebase_index_poll(job_id="3131f65d0181762d")` (failed, 2026-03-04)
- Tool call: `codebase_index_start(repo_id="loom-core", full_refresh=true, embeddings=false)` job `8141921e222489e6` (2026-03-04)
- Tool call: `codebase_index_poll(job_id="8141921e222489e6")` (running, 2026-03-04)
