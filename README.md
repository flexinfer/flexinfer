![Loom Core Banner](assets/banner.png)

# Loom Core

Go backend for the Loom ecosystem:

- MCP server implementations (`cmd/mcp-*/`)
- `loom` CLI for config generation and sync (`cmd/loom/`)
- `loomd` daemon for MCP server lifecycle management (`cmd/loomd/`)

## Documentation

- User guide: `docs/USER_GUIDE.md`
- Developer guide: `docs/DEVELOPER_GUIDE.md`
- Architecture: `docs/ARCHITECTURE.md`

## Quickstart

```bash
make build
./bin/loom generate configs --target all
./bin/loom sync all --regen
./bin/loomd
```

## Daemon management (launchd on macOS)

```bash
./bin/loom start
./bin/loom status
./bin/loom reload
./bin/loom restart
./bin/loom stop
```

## Kubernetes MCP Servers

This repo ships two Kubernetes-focused servers:

- `mcp-k8s`: Go-native Kubernetes API client (client-go). Great for listing resources and reading logs.
- `mcp-k8s-ops`: `kubectl`-backed operations (exec/apply/context-aware workflows). Useful when you need `kubectl`-exact behavior.

### Timeouts (Important for Agents)

Many MCP clients enforce tool-call deadlines (often ~60s). To avoid hanging calls:

- Prefer small, bounded queries (limit output; use `tail` for logs).
- For `mcp-k8s-ops` tools, you can pass `timeoutSeconds` on most calls (including `k8s_exec`).
- Server defaults:
  - `mcp-k8s`: `MCP_K8S_TIMEOUT_SECONDS` (default `55`)
  - `mcp-k8s-ops`: `MCP_K8S_OPS_TIMEOUT_SECONDS` (default `55`)
  - `mcp-ops`: `MCP_OPS_KUBECTL_TIMEOUT_SECONDS` / `MCP_OPS_SSH_TIMEOUT_SECONDS` (default `55`)

### Kubeconfig Selection

- `mcp-k8s`: `MCP_K8S_KUBECONFIG` (preferred) or `KUBECONFIG`, default `~/.kube/config`
- `mcp-k8s-ops`: `MCP_K8S_KUBECONFIG` (preferred) or `KUBECONFIG`, default `~/.kube/k3s.yaml` (if present) else `~/.kube/config`

## Development

- Tests: `go test ./...`
- Lint: `golangci-lint run`

See `AGENTS.md` for agent-specific guidance and `ROADMAP.md` for project status.
