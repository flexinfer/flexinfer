# MCP Inventory

_Last verified: 2026-03-31_

## Why

Capture the MCP/runtime baseline used for planning so later decisions are grounded in the actual session capabilities, not assumptions from prior runs.

## Runtime Mode Detection

This planning session is running in loom-mode.

Observed behavior:
- `list_mcp_resources` returned top-level loom resources including `loom://config`, `loom://servers`, `loom://tools`, and `loom://tools/index`.
- `list_mcp_resource_templates` returned loom paged templates including `loom://tools/page/{page}` and `loom://tools/server/{server}/page/{page}`.
- Direct loom resource reads succeeded for configuration, server inventory, and tool inventory.

Planning implication:
- Prefer loom resource reads as the primary inventory source for this session.
- CLI fallback remains useful, but it is not required for baseline runtime claims in this round.

## Loom Inventory Snapshot

Snapshot from `read_mcp_resource(server="loom", uri="loom://config")`:

| Field | Value |
|---|---|
| active profile | `full` |
| daemon running | `true` |
| registered servers | `46` |
| aggregated tools | `498` |
| active proxy sessions | `3` |
| drain ready | `true` |
| running managed processes | `agent_context` |

Snapshot from `read_mcp_resource(server="loom", uri="loom://tools/index")`:

| Field | Value |
|---|---|
| server | `all` |
| totalTools | `498` |
| totalPages | `5` |
| pageSize | `100` |

Relevant planning tools confirmed available in this session:
- `agent_context`
- `codebase_memory`
- `git`
- `git_worktree`
- `quality`
- `browserkit`
- `tavily`
- `context7`

Inventory caveat:
- `running: false` on `loom://servers` means the server is not currently warm, not that it is unavailable for use.

## Codebase Index Readiness

Current session status from `codebase_memory__codebase_stats(repo_id="loom-core")`:

| Metric | Value |
|---|---|
| total chunks | `7861` |
| Go chunks | `7861` |
| TypeScript chunks | `0` |
| JavaScript chunks | `0` |
| Python chunks | `0` |
| Rust chunks | `0` |

Planning implication:
- Go/backend discovery is index-ready.
- HUD frontend (`internal/hud/frontend`) and iOS companion app (`apps/loom-companion-ios`) still require direct file reads because JS/TS/Svelte/Swift are not indexed in `codebase_memory`.
- For UI-heavy planning or implementation, pair semantic Go lookup with direct source inspection and optional browser/screenshot tooling.

## Constraints

- Some servers are registered but idle until first use; `running: false` in `loom://servers` does not imply unavailable.
- Deployment verification still requires live GitLab/Kubernetes calls because loom inventory only reports tool availability, not repo pipeline state.
- Frontend/mobile code search is still mostly lexical/manual in this session because `codebase_memory` is Go-only.
- Any planning claims about mobile/HUD API shape should prefer current source files and golden contracts over older `.loom/` summaries.

## Planning Implications For Mobile + HUD Polish

- The runtime has enough tooling to support source-backed planning without additional bootstrap work.
- Backend/domain planning can rely on `codebase_memory` plus Go tests and contract fixtures.
- Mobile and HUD polish work should expect more manual file inspection and tighter doc sourcing because those surfaces are not indexed semantically yet.
- If this planning track turns into implementation, consider either expanding codebase indexing coverage or keeping slices intentionally small around well-known files.

## 2026-03-16 Addendum: Bulk Mutation Inventory

Goal:
- Add a context-conserving bulk execution surface for MCP servers that expose meaningful mutating operations, without cloning the same batching code into every `cmd/mcp-*` entrypoint.

Facts found:
- The active loom daemon inventory at planning time reported `46` servers and `483` tools before the bulk work started.
- Tool discovery is centralized in the daemon cache and already feeds `loom://tools`, tool search, and tool get flows, which makes the daemon the narrowest integration point for synthetic tool exposure.
- Schema validation resolves tool definitions from the daemon cache before forwarding calls, so a synthetic tool needs to participate there as well to avoid validation failures.
- The existing daemon call path already carries agent/session metadata, audit hooks, metrics, and output scanning, which are all desirable for bulk execution too.

Planning implication:
- The most leverage comes from a daemon-level synthetic tool pattern (`server__bulk`) that reads a manifest file and fans out to existing server tools internally.
- Servers should opt in heuristically based on their discovered tool surfaces instead of a hand-maintained allowlist of every single mutating server.
- A conservative exclusion list is still needed for servers where batching is low-value, risky, or already covered by richer domain-specific primitives.

Expected server classes for bulk support:
- API-style CRUD/integration servers such as `gitlab`, `github`, `jira`, `google-workspace`, `cloudflare`, `substack`, `linkedin`, `jobsearch`, and similar mutation-oriented integrations.

Expected exclusions:
- Low-level infrastructure/debug/search servers such as `git`, `git_worktree`, `k8s_*`, `prometheus`, `loki`, `grafana`, `time`, `devbox`, `codebase_memory`, and comparable tools where a file-driven bulk wrapper would either add little value or create unclear operational risk.

Sources:
- Tool call: `read_mcp_resource(server="loom", uri="loom://config")` -> active profile `full`, `46` servers, `483` tools
- Tool call: `codebase_memory__codebase_stats(repo_id="loom-core")` -> `7861` indexed Go chunks
- `internal/daemon/daemon_toolcache.go:176`
- `internal/daemon/daemon_toolcache.go:244`
- `internal/daemon/schema_validate.go:134`
- `internal/daemon/daemon_call.go:26`

## Sources

- Tool call: `list_mcp_resources` (2026-03-31)
- Tool call: `list_mcp_resource_templates` (2026-03-31)
- Tool call: `read_mcp_resource(server="loom", uri="loom://config")` (2026-03-31)
- Tool call: `read_mcp_resource(server="loom", uri="loom://servers")` (2026-03-31)
- Tool call: `read_mcp_resource(server="loom", uri="loom://tools/index")` (2026-03-31)
- Tool call: `codebase_memory__codebase_stats(repo_id="loom-core")` (2026-03-31)
