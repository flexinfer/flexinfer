# Loom Core Developer Guide

This guide is for contributors working on Loom Core internals.

For system architecture, see `docs/ARCHITECTURE.md`.

## Repository Layout

- `cmd/loom/`: CLI command tree (`sync`, `generate`, `hud`, `agent`, etc.)
- `cmd/loomd/`: daemon process (routing, lifecycle, health monitoring)
- `cmd/mcp-*/`: MCP server binaries
- `internal/daemon/`: daemon core orchestration
- `internal/hud/`: HUD API + web app integration
- `internal/tui/`: terminal dashboard components
- `internal/devbox/`: devbox detection, Dockerfile generation, backends, state
- `pkg/`: shared reusable packages (`env`, `validate`, `mcperror`, `mcpotel`, etc.)

## Build, Test, Lint

```bash
make build
go test ./...
golangci-lint run
```

Restricted/sandboxed environments:

```bash
make test-sandbox
```

Useful aggregate checks:

```bash
make check
make check-quick
```

## Local Developer Loop

Safe binary upgrade without breaking running agents:

```bash
make dev-upgrade
```

First-time local onboarding:

```bash
make bootstrap-local
```

This rebuilds, installs atomically to `~/.local/bin`, regenerates/syncs configs in loom-mode, and restarts daemon only when idle.

## Adding or Updating an MCP Server

1. Add/update implementation in `cmd/mcp-<name>/`.
2. Keep tool schemas backward compatible (additive changes preferred).
3. Use shared packages where possible:
   - `pkg/validate` for argument parsing/validation
   - `pkg/mcperror` for structured tool errors
   - `pkg/httpclient` for timeout/retry-safe external calls
4. Run quality gates (`go test`, `golangci-lint`, `make build`).
5. Regenerate and sync configs:
   - `./bin/loom generate configs --target all --loom-mode`
   - `./bin/loom sync all --regen --loom-mode`
   - `sync --regen` now prefers workspace-local registries discovered from repo ancestors before home-level defaults.

## Devbox Development Notes

`mcp-devbox` spans:

- `cmd/mcp-devbox/`: tool schemas + manager wiring
- `internal/devbox/detect/`: runtime/dependency fingerprinting
- `internal/devbox/dockerfile/`: generated build plan
- `internal/devbox/backend/`: Docker and K8s execution backends
- `internal/devbox/state/`: persisted sandbox metadata/cache

Recent behavior to preserve:

- Monorepo-aware mounts (`workspaceRoot` mounted at `/workspace`)
- Per-project lifecycle locking to avoid TOCTOU races
- Idle pause/reap loop with active-exec safeguards
- Async execution (`devbox_exec_async` / `devbox_exec_poll`)

## HUD Development Notes

Run HUD locally:

```bash
./bin/loom hud --port 3333
```

Development mode (frontend hot reload):

```bash
./bin/loom hud --port 3333 --dev
```

Terminal mode:

```bash
./bin/loom hud --tui
```

Native overlay (macOS, CGO build):

```bash
./bin/loom hud --overlay --edge right --width 380
```

Sandbox panel data path:

- HUD endpoint: `GET /api/sandbox`
- Backing tool call: `devbox_summary`
- Behavior when devbox missing: returns `{"available": false}`

## Observability

### Tracing (`pkg/mcpotel`)

Tracing is opt-in and no-op when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset.

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
./bin/loomd --debug
```

Instrumented servers currently include `mcp-agent-context`, `mcp-git`, `mcp-gitlab`, and `mcp-prometheus`.

### Metrics

- `mcp-agent-context`: `agent_context_stats` tool exposes counters and memory/workflow stats.
- `mcp-devbox`: `devbox_metrics` and `devbox_summary` support runtime visibility and HUD integration.

## Pre-PR Checklist

- `go test ./...`
- `golangci-lint run`
- `make ci-guardrails` (docs drift + CLI help smoke)
- `make build`
- `./bin/loom sync all --regen --loom-mode` (if tool schemas changed)
- Update docs/CHANGELOG for user-visible behavior changes
