# MCP Inventory

_Last verified: 2026-02-25_

## Why

Capture the active MCP/runtime baseline before planning iOS build/signing/publishing work.

## Runtime Mode Detection

Loom-mode resource endpoints are not available in this runtime.

Evidence:
- `list_mcp_resources` returned an empty set.
- `list_mcp_resource_templates` returned an empty set.

## Fallback Inventory Strategy

Used CLI fallback per `plan-loom-core` workflow:

- `~/.local/bin/loom tools list --json > /tmp/loom-tools-list.json`

## Tool Inventory Snapshot

From `/tmp/loom-tools-list.json`:

| Field | Value |
|---|---|
| server | `all` |
| totalTools | `472` |
| totalPages | `1` |
| pageSize | `472` |

## Tool Distribution by Server Prefix (Top 15)

From:
`jq -r '.tools[]?.name | split("__")[0]' /tmp/loom-tools-list.json | sort | uniq -c | sort -nr | head -15`

- `agent_context`: 80
- `jobsearch`: 66
- `gitlab`: 30
- `flexinfer`: 19
- `codebase_memory`: 17
- `github`: 11
- `git`: 11
- `devbox`: 11
- `redis`: 10
- `ops_mcp`: 10
- `neo4j`: 10
- `sequentialthinking`: 9
- `prometheus`: 9
- `memory`: 9
- `loki`: 9

## Codebase Index Readiness

Direct codebase-memory MCP call was unavailable in this runtime:
- `mcp__loom__codebase_memory__codebase_stats(repo_id="loom-core")` failed with `Transport closed`.

Planning implication:
- Use repository sources + shell-based discovery (`rg`, `xcodebuild`, `gitlab-ci`, docs) as primary evidence for this planning slice.

## Constraints Relevant to This Slice

- Live MCP resource/template discovery is not currently accessible.
- CLI inventory is sufficient for tool availability counting, but not for per-resource metadata introspection.

## Sources

- Tool call: `list_mcp_resources` (2026-02-25)
- Tool call: `list_mcp_resource_templates` (2026-02-25)
- Command: `~/.local/bin/loom tools list --json > /tmp/loom-tools-list.json`
- Command: `wc -c /tmp/loom-tools-list.json`
- Command: `jq -r '.tools[]?.name | split("__")[0]' /tmp/loom-tools-list.json | sort | uniq -c | sort -nr | head -15`
- Tool call: `mcp__loom__codebase_memory__codebase_stats(repo_id="loom-core")` (Transport closed)
