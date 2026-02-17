# MCP Inventory

_Last verified: 2026-02-17 (`list_mcp_resources`, `list_mcp_resource_templates`)_

## Why

Capture MCP capability availability so planning decisions reference real tool availability.

## Checklist

- [x] List MCP servers
- [x] List resources per server
- [x] Record auth/permission/availability constraints
- [x] Record best-tool-for-job notes

## Inventory

### Loom Proxy (aggregated)

The `loom` proxy aggregates tools from all downstream MCP servers. Tools are namespaced as `server__toolname`.

#### Resources (via `ListMcpResourcesTool`)

| Resource | URI | MIME | Description |
|----------|-----|------|-------------|
| Loom servers | `loom://servers` | application/json | List MCP servers managed by daemon |
| Loom tools | `loom://tools` | application/json | Cached aggregated tools |
| Loom health | `loom://health` | application/json | Health summary for all servers |
| Loom config | `loom://config` | application/json | Active profile and daemon config |

### Available Tool Categories (from `ToolSearch`)

| Category | Server Prefix | Example Tools |
|----------|--------------|---------------|
| Search | `tavily__` | `tavily__search`, `tavily__search_news`, `tavily__extract` |
| GitHub | `github__` | `github__list_repos`, `github__get_pr`, `github__search_code` |
| GitLab | `gitlab__` | `gitlab__list_projects`, `gitlab__create_merge_request`, `gitlab__pipeline_summary` |
| Cloudflare | `cloudflare__` | `cloudflare__cf_list_zones`, `cloudflare__cf_list_dns_records` |
| K8s (apps) | `k8s_apps_k3s__` | `k8s_apply`, `k8s_getPods`, `k8s_logs`, `k8s_exec` |
| K8s (longhorn) | `longhorn_k3s__` | Same k8s toolset for longhorn context |
| K8s (harvester) | `k8s_harvester_infra__` | Same k8s toolset for harvester context |
| Ops | `ops_mcp__` | `k8s_get_nodes`, `harvester_vms_list`, `stabilize_cluster` |
| Server mgmt | `server_mgmt__` | `server_listHosts`, `server_sshCommand` |
| Router | `asus_router__` | `router_status`, `router_reboot` |
| Git | `git__` | `git_status`, `git_diff`, `git_log`, `git_commit` |
| Git worktree | `git_worktree__` | `git_worktree_list`, `git_worktree_add` |
| Codebase memory | `codebase_memory__` | `codebase_search`, `codebase_get_definition`, `codebase_call_graph` |
| Agent context | `agent_context__` | `agent_session_start`, `agent_context_recall_enhanced`, `agent_task_add` |
| Memory (KG) | `memory__` | `create_entities`, `search_nodes`, `read_graph` |
| Sequential thinking | `sequentialthinking__` | `start_thinking`, `add_thought` |
| Time | `time__` | `get_current_time`, `convert_timezone` |
| Docker | `docker__` | `docker_ps`, `docker_logs`, `docker_exec` |
| Helm | `helm__` | `helm_list`, `helm_status`, `helm_template` |
| Flux | `flux__` | `flux_get_sources`, `flux_reconcile`, `flux_logs` |
| Prometheus | `prometheus__` | `query`, `query_range`, `list_metrics` |
| Loki | `loki__` | `loki_query`, `loki_query_range` |
| Grafana | `grafana__` | `grafana_search`, `grafana_get_dashboard` |
| Alertmanager | `alertmanager__` | `am_list_alerts`, `am_create_silence` |
| Jira | `jira__` | `jira_get_issue`, `jira_search` |
| Redis | `redis__` | `redis_info`, `redis_keys` |
| Neo4j | `neo4j__` | `neo4j_query`, `neo4j_schema` |
| Qdrant | `qdrant__` | `qdrant_search`, `qdrant_upsert` |
| MinIO | `minio__` | `minio_list_buckets`, `minio_get_object_text` |
| Devbox | `devbox__` | `devbox_exec`, `devbox_build`, `devbox_status` |
| Morph | `morph_fast_apply__`, `morph_embeddings__` | `edit_file`, `morph_embeddings_search` |
| YouTube | `youtube__` | `get_transcript`, `get_video_info` |
| Browserkit | `browserkit__` | `screenshot` |
| Godot | `godot_debug__` | `godot_scene_tree`, `godot_eval`, `godot_screenshot` |
| Release | `release__` | `release_validate`, `release_changelog` |
| Substack | `substack__` | `substack_create_draft`, `substack_publish` |
| Itch.io | `itchio__` | `itchio_upload`, `itchio_status` |

## Constraints and Notes

- All tools are accessed via the `loom` proxy (single MCP connection aggregating all servers).
- Tool names in Claude Code appear as `mcp__loom__<server>__<tool>`.
- The proxy daemon (`loomd`) must be running for tools to work. LaunchAgent auto-start is now supported.
- Image payloads from `browserkit__screenshot` are subject to the proxy's image truncation budget (default 1.5MB).

## Best Tools for Current Work

| Task | Recommended Tool |
|------|-----------------|
| Verify daemon status | `loom://health` resource, `curl localhost:9876/health` |
| Run tests | Bash: `go test ./...` |
| Check git state | `git__git_status`, `git__git_diff` |
| Search codebase | `codebase_memory__codebase_search` or Grep tool |
| Record decisions | `agent_context__agent_context_add` |
