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

# Check daemon health (includes per-server status)
curl http://localhost:9876/health

# Check SSH tunnel status
./bin/loom tunnel status
```

## Daemon Features

### Health Monitoring

The daemon includes a HealthMonitor that:
- Periodically probes each MCP server for health
- Auto-restarts failed servers with exponential backoff
- Tracks uptime, restart counts, and error messages
- Exposes status via `/health` HTTP endpoint and `loom/health` IPC

### SSH Tunnel Management

For remote K8s access via jump hosts:
- TunnelManager auto-connects tunnels on daemon start
- Reconnects on failure with exponential backoff
- Query status via `loom tunnel status` or `loom/tunnels` IPC

Registry config example:
```yaml
servers:
  - name: remote_k8s
    targets:
      vscode:
        ssh:
          host: "jump.example.com"
          user: "admin"
        command: "kubectl"
        env:
          KUBECONFIG_REMOTE_HOST: "k8s-api.internal:6443"
```

MCP Servers Included

- `mcp-git` - Git operations
- `mcp-gitlab` - GitLab API
- `mcp-github` - GitHub API
- `mcp-k8s` - Kubernetes operations
- `mcp-prometheus` - Prometheus queries
- `mcp-grafana` - Grafana dashboards
- `mcp-loki` - Log queries
- `mcp-agent-context` - Persistent memory, task tracking, code annotations
- And more in `cmd/mcp-*/`

## Agent Context System

The `mcp-agent-context` server provides persistent memory for AI agents across sessions. Use it for:
- Recording decisions, findings, and file reads for later recall
- Tracking tasks discovered during sessions
- Annotating code locations with notes
- Handing off context between agents

### Prerequisites

| Variable | Fallback | Description |
|----------|----------|-------------|
| `AGENT_CONTEXT_EMBED_API_KEY` | `MORPH_API_KEY`, `OPENAI_API_KEY` | Embedding API key |
| `AGENT_CONTEXT_QDRANT_URL` | `QDRANT_URL`, `http://localhost:6333` | Qdrant vector DB URL |
| `AGENT_CONTEXT_DEFAULT_AGENT_ID` | - | Default agent ID (optional) |
| `AGENT_CONTEXT_DEFAULT_NAMESPACE` | - | Default namespace (optional) |

### Quick Start Workflow

```
1. agent_session_start(namespace="project/feature-x")
2. agent_context_recall_enhanced(query="previous work on this feature")
3. agent_context_add(entries=[{entry_type: "decision", title: "...", content: "..."}])
4. agent_task_add(tasks=[{title: "Add tests", priority: "medium"}])
5. agent_session_end(summarize=true)
```

### Tool Categories

#### Session Management
| Tool | Description |
|------|-------------|
| `agent_session_start` | Start or resume a session. Returns session_id. |
| `agent_session_end` | End session, optionally summarize context. |
| `agent_session_list` | List sessions by agent/namespace/status. |

#### Context Storage
| Tool | Description |
|------|-------------|
| `agent_context_add` | Add entries (file_read, decision, finding, etc.). |
| `agent_context_get` | Retrieve entries by ID. |
| `agent_context_delete` | Delete entries (requires confirm=true). |
| `agent_context_summarize` | Generate summary of session context. |
| `agent_context_link_codebase` | Link to codebase-memory entries. |
| `agent_context_stats` | Get storage statistics. |

#### Context Retrieval
| Tool | Description |
|------|-------------|
| `agent_context_search` | Semantic search across entries. |
| `agent_context_recall` | Token-efficient retrieval (prioritizes decisions/summaries). |
| `agent_context_recall_enhanced` | Enhanced recall with tasks, recency weighting, symbol context. |

#### Task Tracking
| Tool | Description |
|------|-------------|
| `agent_task_add` | Add tasks with priority, file_path, blocked_by. |
| `agent_task_update` | Update status (pending/in_progress/completed/blocked). |
| `agent_task_list` | List tasks, filter by status. |

#### Code Annotations
| Tool | Description |
|------|-------------|
| `agent_code_annotate` | Create annotation at file:line (todo, fixme, bug, etc.). |
| `agent_code_annotations_get` | Get annotations for file/range. |

#### Cross-Agent Coordination
| Tool | Description |
|------|-------------|
| `agent_context_share` | Share entries with other agents. |
| `agent_context_query_shared` | Query context shared by others. |
| `agent_handoff_create` | Create handoff package (full, selective, summary_only). |
| `agent_handoff_accept` | Accept handoff, optionally import entries. |

#### Templates
| Tool | Description |
|------|-------------|
| `agent_template_create` | Create reusable session template. |
| `agent_template_list` | List available templates. |

### Entry Types

| Type | Use For |
|------|---------|
| `file_read` | Recording files read with line ranges |
| `decision` | Architectural choices, implementation decisions |
| `finding` | Discoveries during exploration |
| `question` | Open questions to revisit |
| `note` | General observations |
| `error` | Errors encountered and resolution |
| `code_context` | Links to codebase-memory symbols |

### Best Practices

1. **Start sessions with recall**: Always check for previous context before starting work
2. **Record decisions immediately**: Don't wait until end of session
3. **Use namespaces**: Group related work (e.g., `project/feature-x`)
4. **Add tasks as you discover them**: Track TODOs/FIXMEs in the task system
5. **End sessions with summary**: Generates compressed context for future recall

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
