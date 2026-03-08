# MCP Inventory

_Last verified: 2026-03-07_

## Why

Capture the MCP/runtime baseline used for planning so later decisions are grounded in the actual session capabilities, not assumptions from prior runs.

## Runtime Mode Detection

This session is not exposing top-level loom-mode MCP resources.

Observed behavior:
- `list_mcp_resources` returned no resources.
- `list_mcp_resource_templates` returned no templates.

Planning implication:
- Use CLI fallback for tool inventory.
- Treat codebase-memory status as unknown when the MCP transport is unavailable in-session.

## CLI Fallback Inventory

Inventory command used:

```bash
loom tools list --json
```

Snapshot from the returned JSON:

| Field | Value |
|---|---|
| server | `all` |
| totalTools | `472` |
| totalPages | `1` |
| page | `1` |

This confirms the repo/runtime still has the same broad MCP surface area noted in prior planning, but the current session requires CLI inspection instead of loom resource pagination.

## Codebase Index Readiness

Current session status:
- `codebase_memory__codebase_stats(repo_id="loom-core")` failed twice with `Transport closed`.
- No repo-index freshness could be verified from MCP in this session.

Planning implication:
- Use `rg`, direct file reads, and local test commands as the primary evidence path.
- Do not assume semantic search/index-backed lookups are reliable until the codebase-memory transport is healthy again.

## Constraints

- MCP inventory is available only through CLI fallback in this session.
- Codebase-memory MCP transport is degraded or unavailable in this session.
- Planning outputs for this pass should cite local files and commands directly.

## Sources

- Tool call: `list_mcp_resources` (2026-03-07, returned empty)
- Tool call: `list_mcp_resource_templates` (2026-03-07, returned empty)
- Command: `loom tools list --json` (2026-03-07)
- Tool call: `codebase_memory__codebase_stats(repo_id="loom-core")` (2026-03-07, `Transport closed`)
