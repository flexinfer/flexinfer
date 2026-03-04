# Loom Core Developer Guide

This guide is for contributors working on Loom Core internals.

For architecture details, see `docs/ARCHITECTURE.md`.

## Current Implementation Priorities

Before starting feature work, align with:

- Current shipped/in-progress status: `docs/IMPLEMENTATION_STATUS.md`
- Strategic roadmap: `ROADMAP.md`
- Active refactor sequencing: `docs/planning/2026-02-17-architecture-refactor-opportunities.md`

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

Force-restart variant:

```bash
make dev-reload
```

First-time local onboarding:

```bash
make bootstrap-local
```

This rebuilds, installs atomically to `~/.local/bin`, regenerates/syncs configs in loom-mode, restarts daemon only when idle (or always for `dev-reload`), then restarts HUD when configured/running on port `3333`.

## Adding or Updating an MCP Server

1. Implement changes in `cmd/mcp-<name>/`.
2. Keep tool schemas backward compatible (additive changes preferred).
3. Use shared packages where possible:
   - `pkg/validate` for argument parsing/validation.
   - `pkg/mcperror` for structured tool errors.
   - `pkg/httpclient` for timeout/retry-safe external calls.
4. Add or update tests for success and failure paths.
5. Run quality gates (`go test`, `golangci-lint`, `make build`).
6. Regenerate and sync configs when tool schemas change:
   - `./bin/loom generate configs --target all --loom-mode`
   - `./bin/loom sync all --regen --loom-mode`

`sync --regen` prefers workspace-local registries discovered from repo ancestors before home-level defaults.

Antigravity sync now maintains both `mcp.json` and `settings.json` (hooks stub), so keep both generated artifacts in sync when updating hook-generation behavior.

For inventory-oriented tooling/tests in loom-mode, prefer paged proxy resources:

- `loom://tools/index`
- `loom://tools/page/{page}`
- `loom://tools/server/{server}/page/{page}`

Keep `loom://tools` behavior backward compatible (including truncation semantics).

CLI fallback for scripts/automation:

- `loom tools list --json`
- `loom tools list --json --server <server> --page <n> --limit <n>`

## Devbox Development Notes

`mcp-devbox` spans:

- `cmd/mcp-devbox/`: tool schemas + manager wiring
- `internal/devbox/detect/`: runtime/dependency fingerprinting
- `internal/devbox/dockerfile/`: generated build plan
- `internal/devbox/backend/`: Docker and K8s execution backends
- `internal/devbox/state/`: persisted sandbox metadata/cache

Behavior to preserve:

- Monorepo-aware mounts (`workspaceRoot` mounted at `/workspace`)
- Per-project lifecycle locking to avoid TOCTOU races
- Idle pause/reap loop with active-exec safeguards
- Async execution (`devbox_exec_async` / `devbox_exec_poll`)

## HUD Development Notes

Run HUD locally:

```bash
./bin/loom hud --port 3333
```

Manage HUD as a launchd service (macOS):

```bash
./bin/loom hud install
./bin/loom hud start
./bin/loom hud status
```

Launchd mode loads optional secrets/env overrides from `~/.config/loom/hud.env` and defaults cache backend to Redis via the launchd plist.

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

Tracing wrappers now cover every `cmd/mcp-*/main.go` server entrypoint.

Quick verification:

```bash
rg --files cmd | rg 'cmd/mcp-.*/main\.go' | wc -l
rg -l 'InitTracer\(|TracedToolHandler\(' cmd/mcp-*/main.go | wc -l
```

### Structured logging (`pkg/mcplog`)

`mcplog` supports:
- `MCP_LOG_FORMAT=text` (default)
- `MCP_LOG_FORMAT=json`

When logs are emitted with context (for example via `slog.ErrorContext` in traced handlers), `trace_id` and `span_id` are attached when an active OTel span is present.

### Metrics

- `mcp-agent-context`: `agent_context_stats` exposes counters and memory/workflow stats.
- `mcp-devbox`: `devbox_metrics` and `devbox_summary` support runtime visibility and HUD integration.

## Docs Maintenance Expectations

When behavior changes are user-visible:

1. Update `docs/IMPLEMENTATION_STATUS.md` if shipped/in-progress status changes.
2. Update `README.md`, `docs/USER_GUIDE.md`, or this guide if workflows changed.
3. Update `CHANGELOG.md` for release-note visibility.
4. Follow `docs/DOCS_MAINTENANCE.md` ownership/cadence checklist.
5. Run `make ci-guardrails` before PR when possible.

## Pre-PR Checklist

- `go test ./...`
- `golangci-lint run`
- `make ci-guardrails` (docs drift + flexinfer-site integration + CLI help smoke)
- `make build`
- `./bin/loom sync all --regen --loom-mode` (if tool schemas changed)
- Update docs/CHANGELOG for user-visible behavior changes
- If docs changed, sync to site repo: `docs/FLEXINFER_SITE_INTEGRATION.md`
