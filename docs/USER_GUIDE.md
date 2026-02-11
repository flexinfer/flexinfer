# Loom Core User Guide

This guide covers daily usage on a developer machine: config sync, daemon operations, HUD visibility, and sandboxed execution.

For architecture details, see `docs/ARCHITECTURE.md`.

## Concepts

- `loom`: CLI for configuration, daemon control, and utility commands.
- `loomd`: local daemon that aggregates and routes MCP tool calls.
- `loom proxy`: stdio MCP entrypoint used by AI clients in `--loom-mode`.

## Quickstart

From `services/loom-core/`:

```bash
make build
./bin/loom sync all --regen --loom-mode
./bin/loomd
```

Or run the one-shot first-time bootstrap:

```bash
make bootstrap-local
```

Or run daemon via launchd on macOS:

```bash
./bin/loom start
./bin/loom status
```

## Install Loom Binaries (recommended)

For fast local iteration while preserving agent stability:

```bash
make install-core
```

For a full update including all `mcp-*` binaries:

```bash
make install-all
```

## Safe Local Upgrade Loop

Use atomic install + controlled restart flow:

```bash
make dev-upgrade
```

For initial setup (build + install + sync + check), use:

```bash
make bootstrap-local
```

See `docs/DEV_BUILD_LIFECYCLE.md` for rollback and restart policy.

## Generate and Sync MCP Configs

Generate config artifacts into `generated/`:

```bash
./bin/loom generate configs --target all --loom-mode
```

Sync into client-specific destinations:

```bash
./bin/loom sync all --regen --loom-mode
```

`sync --regen` resolves registries from the nearest workspace tree first (including ancestor `platform/gitops/mcp/context/registry.yaml`), then falls back to home defaults.

Common targets: `codex`, `vscode`, `kilocode`, `claude`, `claude_desktop`, `gemini`, `antigravity`.

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

The HUD sandbox panel queries `devbox_summary` from `mcp-devbox` and shows `available=false` when the server is not configured/running.

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

## Agent Hooks and Lifecycle

`loom agent ...` commands are hook-friendly wrappers that prefer HUD API calls (default port `3333`) and fall back to daemon socket tool calls when HUD is unavailable.

For hook-only clients that do not emit explicit session start events (for example Codex `notify`), use heartbeat bootstrap mode:

```bash
loom agent heartbeat --agent-id codex --status active --ensure-session --agent-type codex --quiet
```

Optional environment overrides:

- `LOOM_HUD_PORT`: HUD API port
- `LOOM_SOCKET`: daemon socket path (fallback path)

## Response Size and Pagination

Many list/search tools support `page` + `per_page` (capped at 100). Several servers also enforce response size limits to prevent tool-call timeouts.

Selected env controls:

- `LOKI_MAX_RESPONSE_BYTES`
- `PROMETHEUS_MAX_RESPONSE_BYTES`
- `GRAFANA_MAX_RESPONSE_BYTES`
- `TAVILY_MAX_RESPONSE_BYTES`
- `ALERTMANAGER_MAX_RESPONSE_BYTES`

## Troubleshooting

- Daemon offline: `loom status`, then `loom restart`
- Binary drift after rebuild: run `make install-core`, then `loom restart`
- Stale tool list: `loom reload`
- Client cannot find servers: `loom sync all --regen --loom-mode`
- GUI apps miss shell env vars: run `loom check` and move secrets into `loom secrets`
- Hook calls fail with both HUD and daemon errors: verify either `loom hud` is reachable (`LOOM_HUD_PORT`) or daemon socket exists (`LOOM_SOCKET` / `~/.config/loom/loom.sock`)
