# MCP Inventory

_Last verified: 2026-02-27_

## Why

Capture the active MCP/runtime baseline for planning and context continuity.

## Runtime Mode Detection

Loom-mode resource endpoints are not available in this runtime (Claude Code uses deferred tool loading via `ToolSearch`).

Evidence:
- `list_mcp_resources` returned an empty set (2026-02-27).

## Fallback Inventory Strategy

Used CLI fallback per `plan-loom-core` workflow:

- `~/.local/bin/loom tools list --json`

## Tool Inventory Snapshot

| Field | Value |
|---|---|
| server | `all` |
| totalTools | `472` |
| totalPages | `1` |
| pageSize | `472` |

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

Codebase-memory MCP is reachable. Index state at session start:
- `codebase_stats(repo_id="loom-core")` returned `total_chunks: 0` (empty index).
- Full re-index initiated: job `607b05b6df3422a9`, 590 Go files, embeddings enabled.
- Index is building asynchronously; semantic search will be available after completion.

Planning implication:
- Use `Grep`/`Glob` tools for immediate code discovery while index builds.
- Semantic search (`codebase_search`) will become available once indexing completes.

## Constraints

- Loom-mode resource/template discovery is not available in Claude Code runtime.
- CLI inventory is sufficient for tool availability counting.
- Codebase index is rebuilding (was empty).

## Sources

- Tool call: `list_mcp_resources` (2026-02-27, empty)
- Tool call: `codebase_stats(repo_id="loom-core")` (2026-02-27, 0 chunks)
- Tool call: `codebase_index_start(repo_id="loom-core", full_refresh=true, languages=["go"])` (2026-02-27, job 607b05b6df3422a9)
- Command: `~/.local/bin/loom tools list --json` piped through python prefix counter
