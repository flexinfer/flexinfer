# MCP Inventory

## Why

Capture the available MCP servers/resources/templates so planning and implementation can use the right tools without guesswork.

## Checklist

- [ ] List MCP servers
- [ ] List resource templates per server
- [ ] List resources per server (if available)
- [ ] Record any auth/permission constraints
- [ ] Record “best tool for job” notes

## Inventory (paste outputs)

### Servers

- `loom://servers` (via `functions.list_mcp_resources` + `functions.read_mcp_resource`)
  - Loom daemon reports **31** MCP servers (some may be unavailable).
  - Notable servers for FlexInfer planning/implementation:
    - `git`, `gitlab`, `github` (SCM operations)
    - `k8s_apps_k3s`, `flux`, `helm`, `longhorn_k3s`, `ops_mcp` (k8s/gitops ops)
    - `prometheus`, `loki`, `grafana`, `alertmanager` (observability)
    - `codebase_memory` (semantic code search)

### Resource Templates

- Loom exposes templates for:
  - `loom://servers`
  - `loom://tools`
  - `loom://health`
  - `loom://config`

### Resources

- `loom://health` shows most servers are healthy; notable unavailable servers:
  - `context7` (down)
  - `postgres` (down)
- `loom://tools` is very large and may be truncated by the proxy resource size limit.

## Notes

- For this repo’s planning work, prefer local repo inspection (shell + `rg`) and only use MCP tools for:
  - cross-repo planning (GitLab/GitHub queries)
  - cluster state/metrics (k8s/Flux/Prometheus/Loki)
  - dashboards/alerts (Grafana/Alertmanager)
