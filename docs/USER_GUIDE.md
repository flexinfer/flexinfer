# Loom Core User Guide

This guide is for day-to-day usage on a developer machine: setup, config sync, daemon operations, HUD visibility, and sandbox execution.

For architecture details, see `docs/ARCHITECTURE.md`.

## What Is Ready Today

For the latest shipped/in-progress snapshot, read `docs/IMPLEMENTATION_STATUS.md`.

Practical current state:

- Stable local workflow: `loom` + `loomd` + `loom proxy`.
- Multi-platform config sync in `--loom-mode`.
- HUD visibility for servers, agents, tasks, and sandboxes.
- Devbox sandbox execution via Docker or Kubernetes backend.

## Core Concepts

- `loom`: CLI for config generation/sync, daemon control, and utility commands.
- `loomd`: local daemon that aggregates and routes MCP tool calls.
- `loom proxy`: stdio MCP entrypoint used by AI clients in `--loom-mode`.

## 5-Minute First-Time Setup

From `services/loom-core/`:

```bash
make bootstrap-local
./bin/loom start
./bin/loom status
```

If you prefer manual startup without launchd:

```bash
make build
./bin/loom sync all --regen --loom-mode
./bin/loomd
```

## Daily Commands

```bash
# Safe upgrade (restarts daemon only when idle)
make dev-upgrade

# Force upgrade + restart even when active
make dev-reload

# Health check
curl http://localhost:9876/health

# Launch HUD
./bin/loom hud --port 3333
```

## Config Generation and Sync

Generate configs into `generated/`:

```bash
./bin/loom generate configs --target all --loom-mode
```

Sync to platform-specific destinations:

```bash
./bin/loom sync all --regen --loom-mode
```

Common targets: `codex`, `vscode`, `kilocode`, `claude`, `claude_desktop`, `gemini`, `antigravity`.

`sync --regen` resolves registries from the nearest workspace tree first (including ancestor `platform/gitops/mcp/context/registry.yaml`), then falls back to home defaults.

## Daemon Operations

### launchd commands (macOS)

```bash
./bin/loom start
./bin/loom status
./bin/loom reload
./bin/loom restart
./bin/loom stop
```

### Health and logs

```bash
curl http://localhost:9876/health
```

Default log files:

- `~/.config/loom/logs/daemon.log`
- `~/.config/loom/logs/daemon.err`

## HUD (Agent Command Center)

Launch HUD:

```bash
./bin/loom hud --port 3333
```

Optional modes:

- Dev CORS mode: `./bin/loom hud --port 3333 --dev`
- Terminal dashboard: `./bin/loom hud --tui`
- Native overlay (macOS): `./bin/loom hud --overlay --edge right --width 380 --opacity 0.92`

### Sandbox panel

HUD calls `devbox_summary` via `/api/sandbox`.
If `mcp-devbox` is unavailable, HUD shows `available=false`.

## Devbox Sandbox Workflows

`mcp-devbox` provides project-aware, persistent sandbox execution.

Key tools:

- `devbox_detect`: detect runtimes/dependencies for a project.
- `devbox_build`: build/rebuild the sandbox image.
- `devbox_exec`: run commands with bounded output.
- `devbox_exec_async` + `devbox_exec_poll`: long-running command workflow.
- `devbox_status` / `devbox_stop`: lifecycle operations.
- `devbox_summary` / `devbox_metrics`: HUD + observability data.

Relevant environment variables:

- `DEVBOX_WORKSPACE_ROOT` (default `~/workspace`)
- `DEVBOX_BACKEND` (`docker` or `k8s`)
- `DEVBOX_CACHE_DIR` (default `~/.cache/loom/devbox`)
- `DEVBOX_IDLE_TIMEOUT` (default `30m`)
- `DEVBOX_KUBECONFIG`, `DEVBOX_K8S_NAMESPACE`, `DEVBOX_K8S_STORAGE_CLASS` (K8s backend)

## Secrets and Template Variables

Registry env templates often use:

- `${env:KEY}`
- `${keychain:KEY}`
- `${secret:KEY}`

Set a Loom-managed secret:

```bash
./bin/loom secrets set GITLAB_TOKEN
```

Validate local setup:

```bash
./bin/loom check
```

## Agent Hooks and Lifecycle (Advanced)

`loom agent ...` commands prefer HUD API calls (default port `3333`) and fall back to daemon socket tool calls when HUD is unavailable.

For hook-only clients that do not emit explicit session-start events (for example Codex `notify`), use heartbeat bootstrap mode:

```bash
loom agent heartbeat --agent-id codex --status active --ensure-session --agent-type codex --quiet
```

Optional environment overrides:

- `LOOM_HUD_PORT`: HUD API port
- `LOOM_SOCKET`: daemon socket path (fallback path)

Context budget inspection:

```bash
loom agent context-inspect --agent-id codex --detail --limit 200
```

Hook reliability diagnostics:

```bash
loom agent hook-status --agent-id codex --window 5m
curl "http://127.0.0.1:3333/api/timeline?agent_id=codex&event_type=agent.heartbeat&limit=50"
```

Nudge queue status and policy:

```bash
curl "http://127.0.0.1:3333/api/agent/nudge-queue?agent_id=codex"
loom agent nudge-queue-policy
loom agent nudge-queue-policy --cap 96 --drop-policy summarize --debounce-ms 50 --lane-priority control,handoff,advice,default
```

Optional auth env for policy mutation:

- `LOOM_HUD_ADMIN_TOKEN` (preferred)
- `HUD_ADMIN_TOKEN` (fallback)

## Response Size and Pagination

Many list/search tools support `page` + `per_page` (capped at 100). Several servers also enforce response-size limits.

Selected env controls:

- `LOKI_MAX_RESPONSE_BYTES`
- `PROMETHEUS_MAX_RESPONSE_BYTES`
- `GRAFANA_MAX_RESPONSE_BYTES`
- `TAVILY_MAX_RESPONSE_BYTES`
- `ALERTMANAGER_MAX_RESPONSE_BYTES`

## Troubleshooting

- Daemon offline: `loom status`, then `loom restart`
- Binary drift after rebuild: `make install-core`, then `loom restart`
- Stale tool list: `loom reload`
- Client cannot find servers: `loom sync all --regen --loom-mode`
- GUI apps miss shell env vars: run `loom check` and move secrets into `loom secrets`
- Hook calls fail with both HUD and daemon errors: verify either `loom hud` is reachable (`LOOM_HUD_PORT`) or daemon socket exists (`LOOM_SOCKET` / `~/.config/loom/loom.sock`)
