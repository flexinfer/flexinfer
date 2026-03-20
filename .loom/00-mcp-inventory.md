# MCP Inventory

## Purpose

Capture current MCP/runtime capabilities and constraints for FlexInfer planning and implementation work.

## Runtime Mode Detection

- `functions.list_mcp_resources({})` returned `[]`.
- `functions.list_mcp_resource_templates({})` returned `[]`.
- Result: loom resource-proxy mode (`loom://config`, `loom://servers`, `loom://tools/index`) was not discoverable via MCP resource APIs in this session.
- Fallback used: `loom` CLI inventory commands.

## Server Inventory (CLI fallback)

- `loom servers --json | jq '.servers | length'` -> `42`
- `loom servers --json | jq '[.servers[] | select(.running==false)] | length'` -> `0`
- `loom tools list --json --limit 500 --page 1 | jq '{server,page,pageSize,totalTools,totalPages,serverCount,cachedAt}'` ->
  - `totalTools=445`
  - `totalPages=1`
  - `serverCount=42`
  - `cachedAt=2026-02-19T09:24:19.657908-05:00`

### Tool Counts by Server Prefix

From:

`loom tools list --json --limit 500 --page 1 | jq -r '.tools[].name' | awk -F'__' '{print $1}' | sort | uniq -c | sort -nr`

Top groups:

- `agent_context`: 78
- `jobsearch`: 66
- `gitlab`: 30
- `codebase_memory`: 17
- `github`: 11
- `git`: 11
- `devbox`: 11

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

### Current Status

- Indexing now succeeds via CLI path:
  - index job `1869e8aca6a0ab14` finished with `status: done`, `chunks_total: 1877`, `errors: 0`.
- Semantic queries now return results:
  - `codebase_stats(repo_id=flexinfer)` -> `total_chunks: 1877`
  - `codebase_get_definition(symbol=ModelReconciler)` -> `found: true`
  - `codebase_text_search(query=ModelReconciler)` -> `matched_chunks: 56`
- Remaining caveat:
  - In this chat session, direct `functions.mcp__loom__*` calls still fail with `Transport closed`.
  - `loom tools call ...` works and is the active fallback path.

## Best Tool for Job (FlexInfer)

- Local code understanding: `rg`, `git`, direct file reads.
- Git repository ops: `git`, `gitlab`, `github`.
- Cluster and GitOps state: `k8s_apps_k3s`, `flux`, `helm`, `longhorn_k3s`.
- Observability and incident checks: `prometheus`, `loki`, `grafana`, `alertmanager`.
- Agent memory/task handoff: `agent_context`.

## Constraints and Permissions

- MCP resource/template discovery is empty in-session; inventory depends on CLI fallback.
- `loom` CLI uses local socket `/Users/cblevins/.config/loom/loom.sock` (default from CLI help).
- Direct MCP bridge for this chat session remains unstable (`Transport closed`) despite daemon-side recovery.

## Sources

- [C1] `functions.list_mcp_resources({})` -> `resources: []`
- [C2] `functions.list_mcp_resource_templates({})` -> `resourceTemplates: []`
- [C3] `loom servers --json | jq '.servers | length'` -> `42`
- [C4] `loom servers --json | jq '[.servers[] | select(.running==false)] | length'` -> `0`
- [C5] `loom tools list --json --limit 500 --page 1 | jq '{server,page,pageSize,totalTools,totalPages,serverCount,cachedAt}'`
- [C6] `loom tools list --json --limit 500 --page 1 | jq -r '.tools[].name' | awk -F'__' '{print $1}' | sort | uniq -c | sort -nr`
- [C7] `functions.mcp__loom__codebase_memory__codebase_stats({})` -> `repo_id is required`
- [C8] `functions.mcp__loom__codebase_memory__codebase_stats({repo_id:\"flexinfer\"})` -> `total_chunks: 0`
- [C9] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"5380e4246b4b7cf1\"})` -> `vector size=1 expected=1536`
- [C10] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"237b41f443376c18\"})` -> `invalid point ID`
- [C11] `loom tools call qdrant__qdrant_get_collection --args '{"collection":"codebase_memory_v1"}' --json` -> `vectors.size: 1` (before fix)
- [C12] `loom tools call qdrant__qdrant_delete_collection --args '{"collection":"codebase_memory_v1"}' --json`
- [C13] `loom tools call qdrant__qdrant_create_collection --args '{"collection":"codebase_memory_v1","vector_size":1536,"distance":"Cosine"}' --json`
- [C14] `go build -o /Users/cblevins/workspace/services/loom-core/bin/mcp-codebase-memory /Users/cblevins/workspace/services/loom-core/cmd/mcp-codebase-memory`
- [C15] `loom restart`
- [C16] `loom tools call codebase_memory__codebase_index_poll --args '{"job_id":"1869e8aca6a0ab14"}' --json` -> `status: done, chunks_total: 1877`
- [C17] `loom tools call codebase_memory__codebase_stats --args '{"repo_id":"flexinfer"}' --json` -> `total_chunks: 1877`
- [C18] `loom tools call codebase_memory__codebase_get_definition --args '{"repo_id":"flexinfer","symbol":"ModelReconciler","limit":5}' --json` -> `found: true`
