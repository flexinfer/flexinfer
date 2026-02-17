# Loom Core Architecture

This document explains how Loom Core components fit together at runtime.

For operational commands, see:

- `docs/USER_GUIDE.md`
- `docs/DEVELOPER_GUIDE.md`
- `docs/IMPLEMENTATION_STATUS.md` for shipped vs in-progress status

## High-Level Components

```mermaid
flowchart LR
  subgraph Clients
    Codex[Codex CLI]
    VSCode[VS Code MCP]
    Claude[Claude / Claude Desktop]
    Gemini[Gemini CLI]
    Other[Other MCP clients]
  end

  subgraph LocalMachine[Developer machine]
    LoomProxy["loom proxy\n(stdio MCP server)"]
    Loomd["loomd\n(local MCP hub + router)"]
    HUD["loom hud\n(web/TUI/overlay)"]

    subgraph LocalServers[Local MCP servers]
      Devbox["mcp-devbox"]
      AgentCtx["mcp-agent-context"]
      K8s["mcp-k8s / mcp-k8s-ops"]
      APIBacked["mcp-gitlab / mcp-github / mcp-loki / ..."]
    end

    subgraph SandboxRuntime[Sandbox runtime]
      Docker[Docker backend]
      K8sBackend[Kubernetes backend]
    end
  end

  Codex -->|stdio MCP| LoomProxy
  VSCode -->|stdio MCP| LoomProxy
  Claude -->|stdio MCP| LoomProxy
  Gemini -->|stdio MCP| LoomProxy
  Other -->|stdio MCP| LoomProxy

  LoomProxy -->|unix socket| Loomd
  HUD -->|unix socket + HTTP APIs| Loomd

  Loomd -->|spawn + stdio MCP| Devbox
  Loomd -->|spawn + stdio MCP| AgentCtx
  Loomd -->|spawn + stdio MCP| K8s
  Loomd -->|spawn + stdio MCP| APIBacked

  Devbox --> Docker
  Devbox --> K8sBackend
```

## Tool Call Flow

```mermaid
sequenceDiagram
  participant Client as MCP client
  participant Proxy as loom proxy
  participant Loomd as loomd daemon
  participant Router as router
  participant Server as mcp-<server>
  participant API as external API/backend

  Client->>Proxy: tools/call server__tool(params)
  Proxy->>Loomd: loom/call {server, tool, params}
  Loomd->>Router: resolve + route
  Router->>Server: MCP tools/call
  Server->>API: request(s)
  API-->>Server: response
  Server-->>Router: result
  Router-->>Loomd: result
  Loomd-->>Proxy: result
  Proxy-->>Client: result
```

## Sandbox Execution Flow (`mcp-devbox`)

```mermaid
sequenceDiagram
  participant Client as MCP client
  participant Loomd as loomd daemon
  participant Devbox as mcp-devbox
  participant Detect as fingerprint/detect
  participant Build as dockerfile/build
  participant Runtime as Docker or K8s backend

  Client->>Loomd: devbox_exec(project, command)
  Loomd->>Devbox: tools/call devbox_exec
  Devbox->>Detect: fingerprint project
  alt image/container reusable
    Devbox->>Runtime: resume/start existing sandbox
  else rebuild required
    Devbox->>Build: generate Dockerfile + build image
    Devbox->>Runtime: start sandbox
  end
  Devbox->>Runtime: exec command in project workdir
  Runtime-->>Devbox: exit code + output tail
  Devbox-->>Loomd: structured result
  Loomd-->>Client: tool response
```

Design details:

- Workspace-root mounting enables monorepo workflows and sibling module access.
- Per-project lifecycle locks prevent concurrent ensure/start races.
- Idle sandboxes pause/stop via reaper loop; active execs are protected from reaping.

## Registry and Configuration Flow

```mermaid
flowchart TB
  Registry["registry.yaml\n(canonical metadata)"]
  Generate["loom generate configs --loom-mode"]
  Generated["generated/mcp/<profile>/..."]
  Sync["loom sync all --regen --loom-mode"]
  Clients["Client configs in $HOME"]
  Daemon[loomd]
  Reload["loom reload"]

  Registry --> Generate --> Generated --> Sync --> Clients
  Registry --> Daemon
  Sync --> Reload --> Daemon
```

## HUD Architecture

`loom hud` is a local API + UI layer for:

- server health and inventory
- agent sessions, tasks, workflows, and memory views
- sandbox summary visibility via `/api/sandbox` (backed by `devbox_summary`)
- optional macOS native overlay and terminal UI mode

`loom agent ...` commands prefer HUD REST endpoints and fall back to daemon socket tool calls, so hook/automation flows continue when HUD is not running.

## Observability

```mermaid
flowchart LR
  subgraph Servers[Selected instrumented servers]
    Git[mcp-git]
    GitLab[mcp-gitlab]
    Prom[mcp-prometheus]
    AgentContext[mcp-agent-context]
    Devbox[mcp-devbox]
  end

  subgraph Signals
    Logs[Structured logs]
    Traces[OTel traces]
    Stats[Tool metrics/stats]
  end

  subgraph Backends
    Loki[Loki]
    Jaeger[Jaeger/Langfuse]
    Prometheus[Prometheus]
    HUD[HUD panels]
  end

  Git & GitLab & Prom & AgentContext & Devbox --> Logs --> Loki
  Git & GitLab & Prom & AgentContext --> Traces --> Jaeger
  AgentContext --> Stats --> Prometheus
  Devbox --> Stats --> HUD
```

Notes:

- Tracing currently ships on selected servers, not every `mcp-*` binary.
- `pkg/mcpotel` is noop unless `OTEL_EXPORTER_OTLP_ENDPOINT` is set.

## Reliability and Safety Notes

- Per-server stdio calls are serialized to avoid transport corruption.
- Pagination and response-size caps limit timeout/OOM risk for large APIs.
- Secrets should be referenced indirectly (`${env:...}` / `${secret:...}`), not stored plaintext.
- Best-effort cleanup paths log warnings instead of crashing parent workflows.

## Diagram Sources

Diagram source files live under `docs/diagrams/`.
