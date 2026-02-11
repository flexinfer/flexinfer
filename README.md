![Loom Core Banner](assets/banner.png)

# Loom Core

Go backend for the Loom ecosystem:

- MCP server implementations (`cmd/mcp-*/`)
- `loom` CLI for config generation, sync, and local operations (`cmd/loom/`)
- `loomd` daemon for MCP server lifecycle and routing (`cmd/loomd/`)

## Documentation

- Docs hub: `docs/README.md`
- User guide: `docs/USER_GUIDE.md`
- Developer guide: `docs/DEVELOPER_GUIDE.md`
- Architecture: `docs/ARCHITECTURE.md`
- Build lifecycle: `docs/DEV_BUILD_LIFECYCLE.md`
- API stability policy: `docs/API_STABILITY.md`
- Roadmap: `ROADMAP.md`

## Recent changes (post `v0.9.7`)

- Added and hardened `mcp-devbox` (project-aware sandbox execution, async exec/poll, metrics, summary).
- Expanded HUD with sandbox visibility and improved TUI/web polish.
- Added atomic local upgrade workflow (`make dev-upgrade`) for safer developer iteration.

## Quickstart

```bash
make build

# Recommended: one loom-proxy entry per MCP client
./bin/loom sync all --regen --loom-mode

# Run daemon in foreground
./bin/loomd

# Or manage daemon with launchd (macOS)
./bin/loom start
./bin/loom status
```

First-time local bootstrap (build + install core binaries + sync + check):

```bash
make bootstrap-local
```

## Daemon Management (launchd on macOS)

```bash
./bin/loom start
./bin/loom status
./bin/loom reload
./bin/loom restart
./bin/loom stop
```

## Notable MCP Servers

- `mcp-devbox`: project-aware sandbox executor with Docker/K8s backends.
- `mcp-agent-context`: persistent memory, tasks, annotations, and workflows for agent sessions.
- `mcp-k8s`: Go-native Kubernetes API operations.
- `mcp-k8s-ops`: `kubectl`-exact operations (`exec`, `apply`, context-aware workflows).

### Kubernetes timeouts (important for agents)

Many MCP clients enforce tool-call deadlines (often ~60s). To avoid hangs:

- Prefer bounded queries (`tail` logs, paginate list calls).
- For `mcp-k8s-ops`, pass `timeoutSeconds` on long operations.
- Defaults:
  - `mcp-k8s`: `MCP_K8S_TIMEOUT_SECONDS` (default `55`)
  - `mcp-k8s-ops`: `MCP_K8S_OPS_TIMEOUT_SECONDS` (default `55`)
  - `mcp-ops`: `MCP_OPS_KUBECTL_TIMEOUT_SECONDS` / `MCP_OPS_SSH_TIMEOUT_SECONDS` (default `55`)

### Kubeconfig selection

- `mcp-k8s`: `MCP_K8S_KUBECONFIG` (preferred) or `KUBECONFIG`, default `~/.kube/config`
- `mcp-k8s-ops`: `MCP_K8S_KUBECONFIG` (preferred) or `KUBECONFIG`, default `~/.kube/k3s.yaml` (if present) else `~/.kube/config`

## Developer Upgrade Loop

Use atomic install + safe daemon restart for local development:

```bash
make dev-upgrade
```

For first-time setup, use:

```bash
make bootstrap-local
```

For full details and rollback, see `docs/DEV_BUILD_LIFECYCLE.md`.

## Development Quality Gates

```bash
go test ./...
golangci-lint run
```

In restricted environments, use:

```bash
make test-sandbox
```

See `AGENTS.md` for agent-specific guidance.
