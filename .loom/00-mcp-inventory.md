# MCP Inventory

## Purpose

Capture current MCP/runtime capabilities and constraints for FlexInfer planning and implementation work.

## Runtime Mode Detection

### Session 2026-03-05 (current)

- `ListMcpResourcesTool({})` returned `No resources found` — same as 2026-02-27.
- `loom tools call` CLI remains the reliable path.
- Result: **MCP partially degraded**. Resource API empty, CLI fallback functional.

### Session 2026-02-27

- `ListMcpResourcesTool({})` returned `No resources found` — loom resources not discoverable via MCP resource API.
- `loom tools call` CLI works reliably for all servers. **474 tools** across **40+ servers**.
- `codebase_memory` fully operational via CLI: index, search, stats all working.
- Result: **MCP partially degraded**. Resource API empty, but CLI fallback fully functional.

### Session 2026-02-26

### Session 2026-02-22

- `ListMcpResourcesTool({})` returned 5 loom resources: `loom://servers`, `loom://tools`, `loom://tools/index`, `loom://health`, `loom://config`.
- Result: **loom-mode active**. Resources discoverable via MCP resource API.
- `ReadMcpResourceTool(server="loom", uri="loom://config")` returned:
  - Profile: `full`, servers: `43`, tools: `459`
  - All 43 servers running (42 running + jira stopped)

### Session 2026-02-19/20

- `functions.list_mcp_resources({})` returned `[]` — loom resources were not discoverable.
- Fallback used: `loom` CLI inventory commands.

## Server Inventory (loom-mode)

- Source: `ReadMcpResourceTool(server="loom", uri="loom://servers")`
- Total servers: **43** (42 running, 1 stopped: `jira`)
- Total tools: **459** (across 5 pages at 100/page)

### Tool Counts by Server (from loom://tools/index, 459 total)

Key server groups for FlexInfer work:

| Server | Category | Tools |
|--------|----------|-------|
| `agent_context` | memory/agents | ~78 |
| `k8s_apps_k3s` | kubernetes/ops | 7 |
| `gitlab` | devops/scm | 30 |
| `flux` | gitops | 8 |
| `helm` | k8s/deployment | 6 |
| `prometheus` | monitoring | 7 |
| `loki` | logging | 8 |
| `grafana` | dashboards | 6 |
| `codebase_memory` | code/search | 17 |
| `devbox` | sandbox/dev | 11 |
| `flexinfer` | ai/inference | ~15 |

## Codebase Index/Search Readiness (`codebase_memory`)

### Baseline

- `functions.mcp__loom__codebase_memory__codebase_stats({})` -> `repo_id is required`.
- `functions.mcp__loom__codebase_memory__codebase_stats({repo_id:\"flexinfer\"})` reported `total_chunks: 0`.

### Re-index Attempts

1. `codebase_index_start(root=/Users/cblevins/workspace/services/flexinfer, repo_id=flexinfer, full_refresh=true)`  
   Poll result: `failed` with  
   `vector size=1 expected=1536`.
2. `codebase_index_start(..., embeddings=false)`  
   Poll result: `failed` with  
   `value bc784903f84dcc9d is not a valid point ID (must be unsigned integer or UUID)`.

### Root Cause Evidence

- `qdrant__qdrant_get_collection(collection=codebase_memory_v1)` reported `vectors.size: 1` (schema mismatch for embedding mode).
- Existing collection had `points_count: 0`, so recreate was safe for this run.
- Source in `/Users/cblevins/workspace/services/loom-core/pkg/codebase/qdrant/client.go` already includes UUID conversion (`toPointID`), indicating running binary was stale when point-id errors appeared.

### Recovery Actions (2026-02-19)

1. Recreated collection with expected vector size:
   - delete `codebase_memory_v1`
   - create `codebase_memory_v1` with `vector_size=1536`, `distance=Cosine`
2. Rebuilt `mcp-codebase-memory` binary from current source:
   - `go build -o /Users/cblevins/workspace/services/loom-core/bin/mcp-codebase-memory /Users/cblevins/workspace/services/loom-core/cmd/mcp-codebase-memory`
3. Restarted Loom daemon:
   - `loom restart`

### Current Status (2026-02-27)

- **Full re-index completed** via `loom tools call` CLI. Indexed as 9 sharded repos:

  | Repo ID | Directory | Files | Chunks |
  |---------|-----------|-------|--------|
  | `flexinfer-controllers` | controllers/ | 26 | 523 |
  | `flexinfer-api` | api/ | 17 | 338 |
  | `flexinfer-cmd` | cmd/ | 29 | 201 |
  | `flexinfer-internal` | internal/ | 23 | 269 |
  | `flexinfer-pkg` | pkg/ | 23 | 220 |
  | `flexinfer-scheduler` | scheduler/ | 2 | 28 |
  | `flexinfer-agents` | agents/ | 22 | 188 |
  | `flexinfer-backend` | backend/ | 22 | 227 |
  | `flexinfer-e2e` | e2e/ | 5 | 78 |
  | **Total** | | **169** | **2,072** |

- Text search verified working:
  - `codebase_text_search(query=chooseSharedGroupLeader, repo_id=flexinfer-controllers)` -> `matched_chunks: 5`
  - `codebase_text_search(query=ModelSpec, repo_id=flexinfer-api)` -> `matched_chunks: 29`
- Root cause of previous 0-file indexing: `--hub-prefer` daemon flag routed calls to remote hub (no local filesystem access). Fixed by adding `local-only` category in registry.yaml.
- Full-repo single-index (170 files from root) hangs at `files_done: 0` — workaround: shard by directory.
- `loom tools call` is the reliable path; direct MCP bridge still returns empty resources.

## Best Tool for Job (FlexInfer)

- Local code understanding: `rg`, `git`, direct file reads.
- Git repository ops: `git`, `gitlab`, `github`.
- Cluster and GitOps state: `k8s_apps_k3s`, `flux`, `helm`, `longhorn_k3s`.
- Observability and incident checks: `prometheus`, `loki`, `grafana`, `alertmanager`.
- Agent memory/task handoff: `agent_context`.

## Constraints and Permissions

- As of 2026-02-27, MCP resource API returns empty but `loom tools call` CLI works reliably.
- `loom` CLI uses local socket `/Users/cblevins/.config/loom/loom.sock`.
- `codebase_memory` requires `local-only` category to prevent hub routing (fixed in registry.yaml 2026-02-27).
- Full-repo indexing (170+ files) has a known hang bug; use directory sharding as workaround.
- Fallback: `rg`, `git`, direct file reads, `loom tools call ...` via shell when MCP bridge is unreliable.

## Sources

- [C1] `ListMcpResourcesTool({})` -> `No resources found` (2026-02-27)
- [C2] `loom tools list` -> 474 tools across 40+ servers (2026-02-27)
- [C3] `codebase_memory__codebase_stats({repo_id:"flexinfer-controllers"})` -> `total_chunks: 523` (2026-02-27)
- [C4] `codebase_memory__codebase_index_poll` -> all 9 directory indexes `status: done` (2026-02-27)
- [C5] `codebase_memory__codebase_text_search({query:"chooseSharedGroupLeader"})` -> `matched_chunks: 5` (2026-02-27)
- [C6-C18] Previous session sources preserved in git history
