# MCP Inventory

## Purpose

Capture current MCP/runtime capabilities and constraints for FlexInfer planning and implementation work.

## Runtime Mode Detection

### Update (2026-05-21): Loom full profile, codebase index ready

- `functions.list_mcp_resources({})` exposes `loom://config`,
  `loom://servers`, `loom://tools`, `loom://tools/index`, and
  `loom://health`.
- `functions.list_mcp_resource_templates({})` exposes paged tool inventory
  templates, including `loom://tools/page/{page}` and
  `loom://tools/server/{server}/page/{page}`.
- `functions.read_mcp_resource(server="loom", uri="loom://config")` reports
  active profile `full`, `51` servers, and `514` tools.
- `functions.read_mcp_resource(server="loom", uri="loom://tools/index")`
  reports `514` tools across `6` pages.
- `mcp__loom__.codebase_memory__codebase_stats({"repo_id":"flexinfer"})`
  reports collection `codebase_memory_v1` with `2831` total chunks.
- Planning implication:
  - Loom-mode inventory is available for roadmap refreshes.
  - Use `codebase_memory` for targeted code search, but keep `rg` and direct
    file reads as the fastest source-backed planning path.
  - Relevant execution tools for the unblock roadmap include `codebase_memory`,
    `gitlab`, `k8s_apps_k3s`, `flux`, `helm`, `prometheus`, `grafana`,
    `quality`, `devbox`, and the FlexInfer MCP server.

### Update (2026-05-06): Loom full profile and codebase index ready

- `functions.list_mcp_resources({})` exposes `loom://config`, `loom://servers`, `loom://tools`, `loom://tools/index`, and `loom://health`.
- `functions.list_mcp_resource_templates({})` exposes paged tool inventory templates, including `loom://tools/page/{page}` and `loom://tools/server/{server}/page/{page}`.
- `functions.read_mcp_resource(server="loom", uri="loom://config")` reports active profile `full`, `48` servers, and `510` tools.
- `functions.read_mcp_resource(server="loom", uri="loom://tools/index")` reports `510` tools across `6` pages.
- `mcp__loom__.codebase_memory__codebase_stats({"repo_id":"flexinfer"})` reports collection `codebase_memory_v1` with `2831` total chunks.
- Planning implication:
  - Loom-mode inventory is available for planning refreshes.
  - `codebase_memory` is healthy enough for targeted semantic search, while `rg` remains the fastest source of local truth.
  - Relevant implementation tools for this plan include `codebase_memory`, `gitlab`, `k8s_apps_k3s`, `flux`, `prometheus`, `grafana`, `quality`, `devbox`, and the FlexInfer MCP server.

### Update (2026-05-02): Loom resource mode and codebase index are healthy

- `functions.list_mcp_resources({})` exposes `loom://config`, `loom://servers`, `loom://tools`, `loom://tools/index`, and `loom://health`.
- `functions.list_mcp_resource_templates({})` exposes paged tool inventory templates, including `loom://tools/page/{page}` and `loom://tools/server/{server}/page/{page}`.
- `functions.read_mcp_resource(server="loom", uri="loom://config")` reports:
  - active profile: `full`
  - server count: `48`
  - tool count: `504`
- `functions.read_mcp_resource(server="loom", uri="loom://tools/index")` reports:
  - page size: `100`
  - total tools: `504`
  - total pages: `6`
- `functions.read_mcp_resource(server="loom", uri="loom://health")` reports `codebase_memory` healthy with `consecFails: 0`.
- `loom tools call codebase_memory__codebase_stats --args '{"repo_id":"flexinfer"}' --json` reports:
  - collection: `codebase_memory_v1`
  - `total_chunks: 2831`
- Planning implication:
  - Prefer Loom resource inventory for planning refreshes.
  - Use `loom tools call` for direct codebase-memory checks when a callable MCP tool is not exposed in the chat.
  - Keep `rg` and direct file reads as the fastest source of code truth.

### Historical Baseline

- `functions.list_mcp_resources({})` returned `[]`.
- `functions.list_mcp_resource_templates({})` returned `[]`.
- Result: loom resource-proxy mode (`loom://config`, `loom://servers`, `loom://tools/index`) was not discoverable via MCP resource APIs in this session.
- Fallback used: `loom` CLI inventory commands.

### Update (2026-04-25): Loom resource mode is available

- `functions.list_mcp_resources({})` now exposes loom resources:
  `loom://config`, `loom://servers`, `loom://tools`, `loom://tools/index`, and
  `loom://health`.
- `functions.list_mcp_resource_templates({})` exposes paged loom templates:
  `loom://tools/page/{page}` and
  `loom://tools/server/{server}/page/{page}`.
- `functions.read_mcp_resource(server="loom", uri="loom://config")` reported:
  - active profile: `full`
  - server count: `47`
  - tool count: `504`
- `functions.read_mcp_resource(server="loom", uri="loom://tools/index")`
  reported:
  - page size: `100`
  - total tools: `504`
  - total pages: `6`
- `loom tools call codebase_memory__codebase_stats --args '{"repo_id":"flexinfer"}' --json`
  reported `total_chunks: 2831` in `codebase_memory_v1`.
- Planning implication: prefer loom-resource inventory for future planning
  refreshes; keep CLI fallback available for direct tool calls or long outputs.

### Update (2026-04-25): Semantic search degraded during Gemma4 planning

- `codebase_memory__codebase_stats(repo_id=flexinfer)` reported:
  - collection: `codebase_memory_v1`
  - `total_chunks: 2831`
- Two `codebase_memory__codebase_search` attempts failed with Morph embeddings
  HTTP 521 `origin_down`.
- Planning implication:
  - The index exists and lexical/local discovery remains usable.
  - Do not gate the Gemma4 26B/31B plan on semantic search until the Morph
    embeddings endpoint recovers.
  - Use `rg`, direct file reads, git history, and Tavily-backed external
    research for the current planning pass.

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

- MCP resource/template discovery is available in this session through Loom resource mode.
- The `brand-kit` and `godot_mcp` local server health entries are degraded in `loom://health`; they are unrelated to this planning task.
- The `time` hub path shows transient websocket failures, but local target health is green.
- `loom tools call` remains a useful fallback when individual MCP tools are not directly exposed in the chat.

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
- [C27] `functions.list_mcp_resources({})` -> `loom://config`, `loom://servers`, `loom://tools`, `loom://tools/index`, `loom://health`
- [C28] `functions.list_mcp_resource_templates({})` -> paged Loom tool inventory templates
- [C29] `functions.read_mcp_resource(server="loom", uri="loom://config")` -> `serverCount: 48`, `toolCount: 510`, active profile `full`
- [C30] `functions.read_mcp_resource(server="loom", uri="loom://tools/index")` -> `totalTools: 510`, `totalPages: 6`
- [C31] `functions.read_mcp_resource(server="loom", uri="loom://health")` -> `codebase_memory` healthy, `consecFails: 0`
- [C32] `mcp__loom__.codebase_memory__codebase_stats({"repo_id":"flexinfer"})` -> `total_chunks: 2831`

## Update (2026-04-09): Research / Planning Reset

### Runtime Detection

- `functions.list_mcp_resources({})` still returns no resources.
- `functions.list_mcp_resource_templates({})` still returns no templates.
- Direct loom MCP calls used in this chat returned `Transport closed` for:
  - `agent_context__agent_recall`
  - `codebase_memory__codebase_stats`
  - `context7__resolve_library_id`
  - `tavily__search`
- CLI fallback remains the reliable path for inventory in this session.

### CLI Inventory Snapshot

- `loom tools list --json` reports:
  - `totalTools=502`
  - `totalPages=1`
- Top tool groups in this session:
  - `jobsearch=67`
  - `agent_context=62`
  - `gitlab=33`
  - `flexinfer=20`
  - `mentatlab=18`
  - `codebase_memory=17`

### Planning Implication

- For this Gemma4 stabilization round, the dependable tool mix is:
  - local shell + repo search for code truth,
  - `kubectl` / `flux` for live-cluster truth,
  - web/official docs for external validation,
  - `loom` CLI fallback for tool inventory only.
- Do not gate the planning loop on direct MCP bridge recovery; treat bridge instability as an environment constraint, not the task.

### Sources

- [C19] `functions.list_mcp_resources({})` -> `resources: []`
- [C20] `functions.list_mcp_resource_templates({})` -> `resourceTemplates: []`
- [C21] `functions.mcp__loom__agent_context__agent_recall(...)` -> `Transport closed`
- [C22] `functions.mcp__loom__codebase_memory__codebase_stats(...)` -> `Transport closed`
- [C23] `functions.mcp__loom__context7__resolve_library_id(...)` -> `Transport closed`
- [C24] `functions.mcp__loom__tavily__search(...)` -> `Transport closed`
- [C25] `loom tools list --json | sed -n '1,220p'`
- [C26] `loom tools list --json | jq -r '.tools[].name' | awk -F'__' '{print $1}' | sort | uniq -c | sort -nr | sed -n '1,20p'`
