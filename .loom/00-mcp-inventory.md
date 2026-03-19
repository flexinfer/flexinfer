# MCP Inventory

_Last verified: 2026-03-18_

## Why

Capture the MCP/runtime baseline used for planning so later decisions are grounded in the actual session capabilities, not assumptions from prior runs.

## Runtime Mode Detection

This planning session did not expose top-level loom-mode MCP resources through the desktop resource APIs.

Observed behavior:
- `list_mcp_resources` returned an empty result.
- `list_mcp_resource_templates` returned an empty result.
- A direct `codebase_memory__codebase_stats(repo_id="loom-core")` probe failed with `Transport closed`.

Planning implication:
- Prefer the CLI fallback path (`loom ...`) for current-session inventory claims.
- Do not assume in-session MCP resource/template discovery is healthy just because the daemon is up.

## Loom Inventory Snapshot

Snapshot from `loom status --json`:

| Field | Value |
|---|---|
| daemon running | `true` |
| serverCount | `46` |
| activeProxySessions | `1` |
| HUD reachable | `false` |
| running local processes | `neo4j`, `docker`, `k8s_apps_k3s`, `agent_context`, `devbox`, `flux`, `git` |

Snapshot from `loom tools list --json --page 1 --limit 5`:

| Field | Value |
|---|---|
| server | `all` |
| totalTools | `498` |
| totalPages | `50` |
| pageSize | `10` |

Relevant planning tools confirmed available in this session:
- `gitlab`
- `codebase_memory`
- `agent_context`
- `git_worktree`
- `k8s_apps_k3s`
- `flux`
- `quality`
- `devbox`
- `browserkit`

## Codebase Index Readiness

Current session status:
- The `codebase_memory` server is registered in tool inventory and exposes indexing/stat/search tools through `loom tools list --json --server codebase_memory --page 1 --limit 20`.
- A direct in-session `codebase_memory__codebase_stats(repo_id="loom-core")` call failed with `Transport closed`, so current index-health claims should be treated as unavailable in this session unless revalidated later.

Planning implication:
- Use direct file reads and CLI commands as the default evidence source for current planning work.
- Before codebase-index-dependent implementation work, revalidate `codebase_memory` health explicitly.
- For visual regression or layout verification, prefer activating `browserkit` on demand rather than assuming it is already warm.

## Constraints

- Some servers are registered but idle until first use; `running: false` in `loom://servers` does not imply unavailable.
- Deployment verification still requires live GitLab/Kubernetes calls because loom inventory only reports tool availability, not repo pipeline state.
- In this session specifically, resource/template discovery is unavailable through the desktop MCP resource APIs, so CLI fallback is the reliable inventory mechanism.
- Setup/onboarding planning should assume mixed tooling: direct file reads plus local `loom` CLI commands first, and MCP tool calls only after verifying transport health.

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
- Command: `read_mcp_resource(server="loom", uri="loom://config")` -> active profile `full`, `46` servers, `483` tools
- Command: `codebase_memory__codebase_stats(repo_id="loom-core")` -> `7861` indexed Go chunks
- `internal/daemon/daemon_toolcache.go:176`
- `internal/daemon/daemon_toolcache.go:244`
- `internal/daemon/schema_validate.go:134`
- `internal/daemon/daemon_call.go:26`

## Sources

- Tool call: `list_mcp_resources` (2026-03-18)
- Tool call: `list_mcp_resource_templates` (2026-03-18)
- Tool call error: `codebase_memory__codebase_stats(repo_id="loom-core")` -> `Transport closed` (2026-03-18)
- Command: `loom status --json` (2026-03-18)
- Command: `loom tools list --json --page 1 --limit 5` (2026-03-18)
- Command: `loom tools list --json --server codebase_memory --page 1 --limit 20` (2026-03-18)
