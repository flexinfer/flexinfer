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

MCP servers deploy to Kubernetes via Flux. Manifests live in this repo:

- `k8s/base/` - All server deployment manifests (47 servers)
- `k8s/base/kustomization.yaml` - Shared kustomize patches (JSON logging, OTel export for all `component=mcp-server` deployments)
- `platform/gitops/clusters/k3s/flux-system/kustomization-loom-hub-servers.yaml` - Image tags, node affinity

The base kustomization injects `MCP_LOG_FORMAT=json` and `OTEL_EXPORTER_OTLP_ENDPOINT` (Langfuse in-cluster) into all MCP server pods. Keep cross-cutting env vars in `k8s/base/kustomization.yaml` patches to avoid drift across repos.

To deploy: `make deploy` (builds, pushes, updates image tag in gitops, reconciles Flux)

To update images only: `make deploy-update-images` (updates single Flux Kustomization CRD)

To add a new server: create `k8s/base/servers/<name>/` with deployment.yaml, configmap.yaml, service.yaml,
then add to `k8s/base/kustomization.yaml` and `k8s/base/servers/gateway/registry-configmap.yaml`.

Local Usage

The CLI and daemon typically run on developer machines:

```bash
# Build all binaries
make build

# Generate MCP configs for all targets
./bin/loom generate configs --target all --hub-mode

# Sync configs to home directory
for profile in vscode antigravity codex claude gemini kilocode; do
  ./bin/loom sync "$profile" --regen --hub-mode --all-projects --skip-worktrees
done
./bin/loom sync skills all

# Start daemon (manages MCP server processes)
./bin/loomd

# Check daemon health (includes per-server status)
curl http://localhost:9876/health

# Check SSH tunnel status
./bin/loom tunnel status
```

## Development Workflow

### Iterating on loom-core

After making code changes, use one of these targets to rebuild, install, and reload:

```bash
# Safe reload — skips daemon restart if active proxy connections exist
make dev-upgrade

# Force reload — always restarts daemon; all proxy clients auto-reconnect
make dev-reload
```

Both targets execute the same pipeline:
1. Build `loom` + `loomd` binaries
2. Atomic install to `~/.local/bin` (no window where binaries are missing)
3. Regenerate + sync platform configs (hub mode + all projects, skipping `.worktrees/`)
4. Restart daemon (`dev-upgrade` skips if busy; `dev-reload` always restarts)
5. Restart HUD if running on port 3333
6. Smoke test (proxy initialize round-trip)

### How proxy reconnection works

Each platform client (Claude Code, Codex, Zed, Gemini, etc.) spawns its own `loom proxy` process. The proxy connects to `loomd` via Unix socket. When the daemon restarts:

1. The proxy detects a broken pipe or EOF on the next tool call
2. It clears its daemon connection and calls `ensureDaemon()` on the next message
3. `ensureDaemon()` re-dials the socket (with autostart fallback)
4. The client sees no interruption — the tool call succeeds after a brief reconnect

No manual action is needed from any connected agent or IDE.

### First-time setup

```bash
make bootstrap-local    # Build + install + sync + environment check
```

### Individual platform config sync

```bash
loom sync claude --regen --hub-mode --all-projects --skip-worktrees
loom sync codex --regen --hub-mode --all-projects --skip-worktrees
loom sync gemini --regen --hub-mode --all-projects --skip-worktrees
loom sync vscode --regen --hub-mode --all-projects --skip-worktrees
loom sync antigravity --regen --hub-mode --all-projects --skip-worktrees
loom sync kilocode --regen --hub-mode --all-projects --skip-worktrees
loom sync skills all
```

### Platform permissions

Platform-specific allow/deny lists and settings are defined in the registry YAML under `platform_permissions`. Changes to permissions take effect after `loom sync` (no daemon restart required — only the platform config files change).

Registry location: `platform/gitops/mcp/context/registry.yaml`

## Daemon Features

### Health Monitoring

The daemon includes a HealthMonitor that:
- Periodically probes each MCP server for health
- Auto-restarts failed servers with exponential backoff
- Tracks uptime, restart counts, and error messages
- Exposes status via `/health` HTTP endpoint and `loom/health` IPC

### OTel Status

The daemon reports observability configuration via `loom/otel-status` IPC.
Resolution order for the OTLP endpoint:
1. `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable
2. `otel.endpoint` in `~/.config/loom/config.yaml`

Example `config.yaml`:
```yaml
otel:
  endpoint: "https://langfuse.flexinfer.ai/api/public/otel"
  protocol: "http"
  service_name: "loomd"
```

The companion app and HUD read this to display OTel on/off status.

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
1. agent_presence_register(agent_id="claude-1", agent_type="claude-code", description="Working on auth")
2. agent_session_start(namespace="project/feature-x")
3. agent_recall(query="previous work on this feature", scope="context")
4. agent_task_add(tasks=[{title: "...", context: "...", priority: "high", file_path: "..."}])
5. agent_task_update(task_id="...", status="in_progress")
6. agent_context_add(entries=[{entry_type: "decision", title: "...", content: "..."}])
7. agent_task_update(task_id="...", status="completed", resolution="Done: ...")
8. agent_session_end(summarize=true)  # Auto-cleans presence
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
| `agent_recall` | Unified recall across context/memory/graph (`scope=context|memory|all`). |
| `agent_context_recall` | Deprecated compatibility alias routed to unified recall internals. |
| `agent_context_recall_enhanced` | Deprecated compatibility alias routed to unified recall internals. |
| `agent_memory_recall` | Deprecated compatibility alias routed to unified recall internals. |

#### Task Tracking
| Tool | Description |
|------|-------------|
| `agent_task_add` | Add tasks with priority, context, file_path, line_number, tags, blocked_by. |
| `agent_task_update` | Update status (pending/in_progress/completed/blocked) with resolution. |
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
- In restricted/sandboxed environments, prefer `make test-sandbox`, then run `../../bin/workspace-clean --tmp-agent` to clear temporary Go caches.
- Keep MCP server implementations in `cmd/mcp-*/main.go`
- Shared non-server utilities live under `pkg/` (configs, registry, profiles, sync, validation)

## Shared Packages

Use these packages to reduce duplication across MCP servers:

### `pkg/env` - Environment Variables
```go
import "github.com/crb2nu/loom/pkg/env"

// Get string with fallback
url := env.String("API_URL", "http://localhost:8080")

// Get int with fallback (only positive values)
port := env.Int("PORT", 8080)

// Get bool (accepts "1", "true", "yes", "on")
debug := env.Bool("DEBUG", false)

// Get duration
timeout := env.Duration("TIMEOUT", 30*time.Second)

// Token fallback chains (e.g., GITHUB_PERSONAL_ACCESS_TOKEN, GITHUB_TOKEN)
token := env.StringWithFallbacks("GITHUB_PERSONAL_ACCESS_TOKEN", "GITHUB_TOKEN")

// Required values (returns error if missing)
token, err := env.MustString("API_TOKEN")
```

### `pkg/mcperror` - Structured Errors
```go
import "github.com/crb2nu/loom/pkg/mcperror"

// API errors (sets appropriate code based on HTTP status)
return nil, mcperror.APIError("GitLab", resp.StatusCode, bodyText)

// Check error types
if mcperror.IsNotFound(err) { ... }
if mcperror.IsServerError(err) { ... }

// Configuration errors
return nil, mcperror.NotConfigured("GITHUB_TOKEN", "set via environment variable")

// Parameter validation
return mcp.ErrorResult(mcperror.RequiredParam("project")), nil
return mcp.ErrorResult(mcperror.InvalidParam("format", "must be json or yaml")), nil
```

### `pkg/poll` - Polling and Retry
```go
import "github.com/crb2nu/loom/pkg/poll"

// Context-aware sleep (respects cancellation)
if err := poll.WaitWithContext(ctx, 5*time.Second); err != nil {
    return err // context cancelled
}

// Retry with exponential backoff
err := poll.RetryWithBackoff(ctx, 3, time.Second, 10*time.Second, func(ctx context.Context) error {
    return doRequest(ctx)
})
```

### `pkg/strutil` - String Utilities
```go
import "github.com/crb2nu/loom/pkg/strutil"

// Truncate with ellipsis ("..." included in max length)
strutil.Truncate(s, 100)  // "very long text..." (100 chars total)

// Truncate without ellipsis
strutil.TruncateNoEllipsis(s, 100)

// Truncate multiline to single line
strutil.TruncateSingleLine(s, 100)

// UTF-8 aware byte truncation
strutil.TruncateBytes(s, 1024)
```

### `pkg/validate` - Input Validation
```go
import "github.com/crb2nu/loom/pkg/validate"

v := validate.NewArgs(args)
project := v.Required("project")
perPage := v.Int("per_page", 30)
labels := v.StringSlice("labels")

if err := v.Validate(); err != nil {
    return mcp.ErrorResult(err), nil
}

// Pagination helpers
page := validate.NormalizePage(v.Int("page", 1))      // Defaults to 1, min 1
perPage := validate.NormalizePerPage(v.Int("per_page", 30), 30, 100)  // default, max

// Or use the integrated method
p := v.GetPagination()  // Returns Pagination{Page, PerPage}
```

### `pkg/httpclient` - HTTP Client
```go
import "github.com/crb2nu/loom/pkg/httpclient"

client := httpclient.NewDefault()  // With retry, timeouts from env vars
resp, err := client.Do(req)

// Or with custom config
client := httpclient.New(httpclient.Config{
    Timeout:     30 * time.Second,
    MaxRetries:  3,
})
```

### `pkg/lifecycle` - Signal Handling
```go
import "github.com/crb2nu/loom/pkg/lifecycle"

func main() {
    if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
        fmt.Fprintf(os.Stderr, "Error: %v\n", err)
        os.Exit(1)
    }
}

func run(ctx context.Context) error {
    // ctx is cancelled on SIGINT/SIGTERM
    return server.Run(ctx)
}
```

### `pkg/mcplog` - Logging
```go
import "github.com/crb2nu/loom/pkg/mcplog"

logger := mcplog.NewDefault()  // Respects MCP_DEBUG env var
logger.Info("starting server", "name", serverName, "version", version)
```

### `pkg/mcpotel` - OpenTelemetry Tracing
```go
import "github.com/crb2nu/loom/pkg/mcpotel"

// Initialize tracer (noop when OTEL_EXPORTER_OTLP_ENDPOINT is unset)
tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-myserver", logger)
if err != nil { logger.Warn("OTel tracer init failed", "error", err) }
defer func() { _ = shutdownTracer(ctx) }()
tracer := mcpotel.Tracer(tp, "mcp-myserver")

// Wrap tool handlers with tracing middleware
server.AddTool(tool, mcpotel.TracedToolHandler(tracer, "tool_name", handler))
```

The middleware automatically:
- Creates a span per tool call with tool name and arguments
- Extracts `agent_id`, `session_id`, `namespace` from args as span attributes
- Records errors and sets span status on failure
- Compatible with Jaeger, Langfuse, and any OTLP-compatible collector

In k8s, `OTEL_EXPORTER_OTLP_ENDPOINT` is injected into all MCP server pods via the kustomize patch in `k8s/base/kustomization.yaml`. Locally, the daemon reads `otel.endpoint` from `~/.config/loom/config.yaml` as a fallback (see Daemon Features → OTel Status above).

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

<!-- BEGIN LOOM:AGENT-SAFETY -->
## Loom Agent Safety Policy (Generated)

- Pre-existing uncommitted/untracked files are baseline context, not an automatic blocker.
- Continue on the current branch/worktree by default.
- Stage and commit only files intentionally changed for the active task.
- Escalate only when new unexpected changes appear in files you are editing, or when a branch/worktree switch is explicitly requested.
- Dirty-worktree mode: `continue_scoped_commits`.

Canonical nudge for CLI hooks:
> Dirty worktree detected. Treat pre-existing changes as baseline context, continue work, and stage/commit only files for the active task. Escalate only if new unexpected changes appear in files you are editing.

<!-- END LOOM:AGENT-SAFETY -->
