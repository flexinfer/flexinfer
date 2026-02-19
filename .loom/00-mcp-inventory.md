# MCP Inventory (Universal Workflow Program)

_Last verified: 2026-02-19_

## Why

Ground workflow-skill upgrades in the active loom runtime so "repeatable loops" align with real tool availability (especially `agent_context`, `codebase_memory`, and CI APIs).

## Runtime Mode Detection

- `list_mcp_resources` (no server) returned no direct resources.
- `list_mcp_resource_templates` returned loom templates (`loom://config`, `loom://servers`, `loom://tools/index`, paged tool URIs).
- Conclusion: operate in loom-mode via `read_mcp_resource(server="loom", uri=...)`.

## Commands Run

- `list_mcp_resources` (no server filter)
- `list_mcp_resource_templates` (no server filter)
- `read_mcp_resource(server="loom", uri="loom://config")`
- `read_mcp_resource(server="loom", uri="loom://servers")`
- `read_mcp_resource(server="loom", uri="loom://tools/index")`
- `read_mcp_resource(server="loom", uri="loom://health")`
- `read_mcp_resource(server="loom", uri="loom://tools/server/agent_context/page/1")`
- `read_mcp_resource(server="loom", uri="loom://tools/server/codebase_memory/page/1")`
- `read_mcp_resource(server="loom", uri="loom://tools/server/gitlab/page/1")`

## Inventory Summary

### Global Runtime

- Active profile: `full`
- Registered servers: `42`
- Total tools: `379`
- Tool index pages: `4`

### Key Server Tool Surfaces for Core Loops

| Server | Tools | Why it matters |
|---|---:|---|
| `agent_context` | 78 | Session/task/context/handoff lifecycle for multi-agent continuity |
| `codebase_memory` | 17 | Index/search/definition/references/context/call graph baseline |
| `gitlab` | 30 | Pipeline list/summary/poll + job logs for CI watch/fix loops |

### Health Notes

- `loom://health` reports healthy targets for key workflow servers.
- Some servers are not pre-running and will lazy-start on first use; workflows should tolerate startup/reconnect latency.

## Constraints and Implications

1. Paged loom tool inventory is required to avoid truncation on large tool surfaces.
2. Index-first workflow is feasible now: `codebase_memory` exposes async index start/poll and search APIs.
3. Ship-loop automation is feasible now: `gitlab` exposes pipeline polling and failed job log retrieval.
4. Durable cross-session workflows are feasible now: `agent_context` has full session/task/presence/handoff capabilities.

## Tooling Strategy for This Program

| Workflow | Core MCP tools |
|---|---|
| Planning/specs | `loom://config`, `loom://servers`, `loom://tools/index`, `codebase_memory__codebase_stats` |
| Research | `codebase_memory__codebase_search`, `tavily__search`, `context7__get-library-docs`, `agent_context__agent_context_add` |
| Delivery loop | `git` + `gitlab__poll_pipeline` + `gitlab__pipeline_summary` + `agent_context__agent_task_update` |
| Troubleshooting | `k8s_*`, `loki_*`, `prometheus_*`, `agent_context__agent_context_add`, `agent_handoff_*` |

## Sources

- Runtime config snapshot: `read_mcp_resource(server="loom", uri="loom://config")` (2026-02-19)
- Server catalog: `read_mcp_resource(server="loom", uri="loom://servers")` (2026-02-19)
- Tool index: `read_mcp_resource(server="loom", uri="loom://tools/index")` (2026-02-19)
- Agent context tool page: `read_mcp_resource(server="loom", uri="loom://tools/server/agent_context/page/1")` (2026-02-19)
- Codebase memory tool page: `read_mcp_resource(server="loom", uri="loom://tools/server/codebase_memory/page/1")` (2026-02-19)
- GitLab tool page: `read_mcp_resource(server="loom", uri="loom://tools/server/gitlab/page/1")` (2026-02-19)
