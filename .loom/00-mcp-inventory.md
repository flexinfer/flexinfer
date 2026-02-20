# MCP Inventory

_Last verified: 2026-02-19T18:52:32Z_

## Why

Capture MCP/runtime/tooling baseline before planning a companion iPhone/iPad app for loom-core.

## Runtime Mode Detection

Loom-mode is active.

Evidence:
- `list_mcp_resources` returned loom-scoped resources including `loom://config`, `loom://servers`, `loom://tools/index`, `loom://health`.
- `list_mcp_resource_templates` returned paged templates including `loom://tools/page/{page}` and `loom://tools/server/{server}/page/{page}`.

## Loom Runtime Snapshot

From `read_mcp_resource(server="loom", uri="loom://config")` and `read_mcp_resource(server="loom", uri="loom://tools/index")`:

| Field | Value |
|---|---|
| Active profile | `full` |
| Server count | `42` |
| Tool count | `445` |
| Running | `true` |
| Running processes (count) | `21` |
| Tool index page size | `100` |
| Tool index total pages | `5` |

## Tool Inventory (Paged Capture)

Paged verification completed using CLI fallback (`./bin/loom tools list --json --page N --limit 100`):

| Page | Tools |
|---|---|
| 1 | 100 |
| 2 | 100 |
| 3 | 100 |
| 4 | 100 |
| 5 | 45 |

Total confirmed: `445` tools.

## Tool Distribution by Server Prefix

From command:
- `./bin/loom tools list --json | jq -r '.tools[]?.name | split("__")[0]' | sort | uniq -c | sort -nr`

Top-heavy servers for this workspace:
- `agent_context`: 78
- `jobsearch`: 66
- `gitlab`: 30
- `codebase_memory`: 17
- `github`, `git`, `devbox`: 11 each

## Codebase Index Readiness (codebase_memory)

Baseline check:
- `codebase_memory__codebase_stats(repo_id="loom-core")` initially returned `total_chunks: 0`.

Indexing attempts:
1. `codebase_index_start(..., embeddings=true)` failed.
   - Error: `morph API HTTP 400: "The decoder prompt cannot be empty"`
2. `codebase_index_start(..., embeddings=false)` completed and is usable as lexical/definition baseline while embedding pipeline is fixed.
   - Final poll: `files_done: 1717 / 1717`, `chunks_total: 26930`, `status: done`.

Planning impact:
- Use `codebase_text_search`, `codebase_get_definition`, `codebase_get_context`, and file/rg reads for this planning phase.
- Treat semantic ranking quality as degraded until embeddings are restored.

## Constraints Relevant to Mobile Companion Planning

- HUD currently listens on loopback (`127.0.0.1`), not LAN/public interfaces.
- Most HUD APIs are open to local callers; only select mutation endpoints enforce admin token today.
- SSE is first-class (`/api/events`), making real-time mobile monitoring feasible once remote-safe auth/transport is added.

## Recommended Tooling for This Planning Slice

- Runtime/mode inventory: `loom://config`, `loom://servers`, `loom://tools/index`, `loom://health`.
- Workspace facts: `workspace_snapshot.py` output (`.loom/00-workspace-snapshot.md`).
- API surface validation: `internal/hud/app.go`, `internal/hud/api_agent.go`, `internal/hud/bridge/agent.go`.
- Incremental architecture research: `codebase_memory` lexical/definition tools + `rg`.

## Sources

- `list_mcp_resources` (2026-02-19): loom resources present including `loom://config`, `loom://servers`, `loom://tools/index`, `loom://health`.
- `list_mcp_resource_templates` (2026-02-19): includes paged templates `loom://tools/page/{page}` and `loom://tools/server/{server}/page/{page}`.
- `read_mcp_resource(server="loom", uri="loom://config")` (2026-02-19).
- `read_mcp_resource(server="loom", uri="loom://tools/index")` (2026-02-19).
- `read_mcp_resource(server="loom", uri="loom://servers")` (2026-02-19).
- `read_mcp_resource(server="loom", uri="loom://health")` (2026-02-19).
- Command: `./bin/loom tools list --json | jq -r '.tools | length as $n | "total_tools=\($n)"'`.
- Command: `./bin/loom tools list --json --page {1..5} --limit 100 | jq ...`.
- Command: `./bin/loom tools list --json | jq -r '.tools[]?.name | split("__")[0]' | sort | uniq -c | sort -nr`.
- `codebase_memory__codebase_stats(repo_id="loom-core")` and `codebase_index_poll(job_id=...)` outputs captured 2026-02-19.
- `internal/hud/app.go:317`
- `internal/hud/app.go:528`
- `internal/hud/api_agent.go:735`
- `internal/hud/api_agent.go:829`
