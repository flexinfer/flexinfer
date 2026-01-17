Agent Working Notes (loom-core)

Scope

- This file applies to the `services/loom-core` repository.

Repository Purpose

Go backend for the loom ecosystem:

- MCP server implementations (git, gitlab, github, k8s, prometheus, etc.)
- `loom` CLI for config generation and sync
- `loomd` daemon for MCP server lifecycle management

Workspace Structure

This repo is part of the `services/` GitLab group:

```text
gitlab.flexinfer.ai/
├── platform/gitops    ← K8s manifests, Flux, CI infrastructure
└── services/
    ├── loom           ← VSCode extension (TypeScript)
    └── loom-core      ← YOU ARE HERE (Go backend)
```

Deployment (GitOps)

MCP servers can be deployed to Kubernetes via Flux. Manifests live in:

- `platform/gitops/k3s/mcp-hub/servers/` - Individual MCP server deployments

To deploy an MCP server:

1. Build binaries: `make build`
2. Build container: `docker build -t registry.harbor.lan/library/loom:TAG .`
3. Push to Harbor
4. Update image tag in `platform/gitops/k3s/mcp-hub/servers/<server>/`
5. Commit and push to `platform/gitops`

Local Usage

The CLI and daemon typically run on developer machines:

```bash
# Build all binaries
make build

# Generate MCP configs for all targets
./bin/loom generate configs --target all

# Sync configs to home directory
./bin/loom sync all --regen

# Start daemon (manages MCP server processes)
./bin/loomd
```

MCP Servers Included

- `mcp-git` - Git operations
- `mcp-gitlab` - GitLab API
- `mcp-github` - GitHub API
- `mcp-k8s` - Kubernetes operations
- `mcp-prometheus` - Prometheus queries
- `mcp-grafana` - Grafana dashboards
- `mcp-loki` - Log queries
- And more in `cmd/mcp-*/`

Code Style

- Run `golangci-lint run` before committing
- Run `go test ./...` to verify changes
- In restricted/sandboxed environments, prefer `make test-sandbox` to force `GOCACHE`/`GOMODCACHE` into `/tmp`.
- Keep MCP server implementations in `cmd/mcp-*/main.go`
- Shared non-server utilities live under `pkg/` (configs, registry, profiles, sync, validation)

Agent Tips

- Tool-call deadlines: many clients time out around ~60s; prefer bounded operations and use `tail` for logs.
- Kubernetes:
  - Use `mcp-k8s` for resource reads + logs (client-go, structured JSON outputs).
  - Use `mcp-k8s-ops` for `kubectl`-exact workflows (notably `k8s_exec` / `k8s_apply`).
  - `mcp-k8s-ops` supports per-call `timeoutSeconds` on most tools; set it below the client deadline when debugging hangs.
- Kubeconfig:
  - Prefer setting `MCP_K8S_KUBECONFIG` (works for both `mcp-k8s` and `mcp-k8s-ops`).

## Planning
- See `ROADMAP.md` for project status and plans.
