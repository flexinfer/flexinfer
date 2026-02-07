# Loom Core Architecture

This document describes Loom Core’s architecture at a “how the pieces fit together” level. For day-to-day commands, see:

- User guide: `docs/USER_GUIDE.md`
- Developer guide: `docs/DEVELOPER_GUIDE.md`

## High-level components

```mermaid
flowchart LR
  subgraph Clients
    Codex[Codex CLI]
    VSCode[VS Code MCP]
    Claude[Claude / Claude Desktop]
    Gemini[Gemini CLI]
    Kilo[Kilo Code]
    Other[Other MCP clients]
  end

  subgraph LocalMachine[Developer machine]
    LoomProxy["loom proxy<br/>(stdio MCP server)"]
    Loomd["loomd<br/>(local MCP hub + router)"]

    subgraph LocalServers[Local MCP server processes]
      GitLab["mcp-gitlab"]
      GitHub["mcp-github"]
      Loki["mcp-loki"]
      Prom["mcp-prometheus"]
      K8s["mcp-k8s / mcp-k8s-ops"]
      OtherMCP["mcp-*"]
    end
  end

  Codex -->|stdio MCP| LoomProxy
  VSCode -->|stdio MCP| LoomProxy
  Claude -->|stdio MCP| LoomProxy
  Gemini -->|stdio MCP| LoomProxy
  Kilo -->|stdio MCP| LoomProxy
  Other -->|stdio MCP| LoomProxy

  LoomProxy -->|unix socket| Loomd

  Loomd -->|spawn + stdio MCP| GitLab
  Loomd -->|spawn + stdio MCP| GitHub
  Loomd -->|spawn + stdio MCP| Loki
  Loomd -->|spawn + stdio MCP| Prom
  Loomd -->|spawn + stdio MCP| K8s
  Loomd -->|spawn + stdio MCP| OtherMCP

  GitLab -->|HTTP| GitLabAPI[(GitLab API)]
  GitHub -->|HTTP| GitHubAPI[(GitHub API)]
  Loki -->|HTTP| LokiAPI[(Loki)]
  Prom -->|HTTP| PromAPI[(Prometheus)]
  K8s -->|HTTPS| K8sAPI[(Kubernetes API)]
```

Notes:

- **Clients** talk to a single local entrypoint (`loom proxy`) when using `--loom-mode` configs.
- **`loomd`** owns routing, lifecycle, and policy (what servers exist, env/secrets, etc.).
- **MCP servers** are typically separate binaries (`cmd/mcp-*/`) spawned as local child processes and spoken to over stdio MCP.

## Tool call flow (sequence)

```mermaid
sequenceDiagram
  participant Client as MCP client
  participant Proxy as loom proxy (stdio)
  participant Loomd as loomd (daemon)
  participant Router as router
  participant Server as mcp-<server> (child process)
  participant API as external API

  Client->>Proxy: tools/call server__tool(params)
  Proxy->>Loomd: loom/call {server, tool, params}
  Loomd->>Router: resolve + route call
  Router->>Server: MCP tools/call tool(params)
  Server->>API: HTTP/SDK request(s)
  API-->>Server: response
  Server-->>Router: result (+ pagination/metadata)
  Router-->>Loomd: result
  Loomd-->>Proxy: result
  Proxy-->>Client: result
```

## Registry and configuration artifacts

Loom uses a shared registry (`registry.yaml`) to generate downstream configs and manifests.

```mermaid
flowchart TB
  Registry["registry.yaml<br/>(canonical server + tool metadata)"]
  Gen["loom generate configs --loom-mode"]
  Out["generated/mcp/<profile>/..."]
  Sync["loom sync all --regen --loom-mode"]
  Home["Client configs in $HOME<br/>(.codex/.vscode/.claude/etc.)"]
  Daemon["loomd"]
  Reload["loom reload"]

  Registry --> Gen --> Out --> Sync --> Home
  Registry --> Daemon
  Sync --> Reload --> Daemon
```

## Observability

```mermaid
flowchart LR
  subgraph MCPServers[MCP Servers]
    Git["mcp-git"]
    GL["mcp-gitlab"]
    Prom["mcp-prometheus"]
    AC["mcp-agent-context"]
    Others["mcp-*"]
  end

  subgraph Observability
    Logs["Structured Logs<br/>(slog/JSON)"]
    Traces["OTel Traces<br/>(pkg/mcpotel)"]
    Metrics["Prometheus Metrics<br/>(atomic counters)"]
  end

  subgraph Backends
    Loki["Loki"]
    Jaeger["Jaeger / Langfuse"]
    PromBE["Prometheus"]
  end

  Git & GL & Prom & AC & Others -->|slog| Logs --> Loki
  Git & GL & Prom & AC & Others -->|OTLP gRPC| Traces --> Jaeger
  AC -->|/metrics| Metrics --> PromBE
```

### Tracing (`pkg/mcpotel`)

All MCP servers use `TracedToolHandler` middleware to create per-tool-call spans. The tracer initializes as a noop when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset, so tracing adds zero overhead by default. Spans include `agent_id`, `session_id`, and `namespace` attributes when present in tool arguments.

Servers with tracing: `mcp-agent-context`, `mcp-git`, `mcp-gitlab`, `mcp-prometheus`.

### Metrics (`pkg/agentcontext`)

`mcp-agent-context` maintains atomic counters for sessions, embedding calls, recall hit/miss rates, graph operations, workflows, compression, and per-tier memory items. The `agent_context_stats` tool returns a live snapshot. Prometheus-format export is available via `PrometheusFormat()`.

### Logging (`pkg/mcplog`)

All servers use `slog`-based structured logging. Error paths log warnings with contextual fields (IDs, operation names, error details) rather than silently discarding errors.

## Reliability and safety design notes

- **Stdio concurrency**: requests to local stdio-backed MCP servers are serialized per-server in `loomd` to avoid transport corruption (stdio is a single shared byte stream).
- **Pagination + bounded output**: list/search tools in API-backed MCPs expose `page`/`per_page` and return pagination metadata; large responses are capped to avoid client timeouts and OOMs.
- **Secrets hygiene**: generated configs are validated for plaintext secrets (`loom validate configs`); registry values should use `${env:...}` / `${secret:...}` indirections.
- **Best-effort error handling**: Internal cleanup operations (compaction, TTL sweeps, rollbacks) log failures as warnings but do not abort the parent operation. This prevents cascading failures while keeping errors visible.

## Diagram sources

Source `.mmd` files (including auto-generated internal/package dependency graphs) live under `docs/diagrams/`.
