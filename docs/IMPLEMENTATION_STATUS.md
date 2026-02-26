# Loom Core Implementation Status

> Last Updated: February 26, 2026
> Canonical status source for shipped vs in-progress work.

## Current State

Loom Core is production-ready as a local MCP runtime and orchestration backend.

Shipped and actively used:

- `loom` CLI for config generation/sync, daemon lifecycle, HUD launch, and agent hooks.
- `loomd` daemon for routing, server lifecycle management, health monitoring, and tunneling.
- `loom proxy` single-entry MCP bridge for multi-platform client support.
- 59 Go MCP servers spanning Git, GitLab/GitHub, Kubernetes, observability, agent memory, and sandbox execution.
- HUD command center (web, TUI, macOS overlay) for server/agent/sandbox visibility.

## Recently Shipped (Post `v0.9.7`)

- HUD M3/M4 UX foundation and panel migrations.
- Streamable HTTP transport with bearer/OIDC/mTLS auth.
- Enterprise controls: RBAC, audit trail, cost tracking, OAuth 2.1.
- `mcp-devbox` sandbox runtime with Docker and Kubernetes backends.
- Agent orchestration enhancements (presence, worktrees, workflows).
- Developer-safe local upgrade flow (`make dev-upgrade`, `make dev-reload`).
- OTel tracing across all `cmd/mcp-*/main.go` servers (59/59) plus JSON log correlation (`trace_id`, `span_id`) via `pkg/mcplog`.
- HUD launchd lifecycle management (`loom hud install|start|stop|status`) with `hud.env` loading and Redis-first cache defaults in launchd mode.
- Worktree-first agent workflow nudges at session start and Antigravity `settings.json` sync parity.

## In Progress Now

These are active priorities and should be treated as implementation gaps until complete:

1. Coverage growth from ~30% toward 40%+, focused on daemon lifecycle and devbox integration paths.
2. Daemon call pipeline hardening after extraction into `internal/daemon/callpipeline.go`.
3. Agent contract convergence across CLI, HUD API, and bridge layers.
4. Refactor decomposition of large surfaces (`PresencePanel.svelte`, `internal/devbox/backend/k8s.go`).
5. Daemon/runtime telemetry expansion (tool routing, server spawn/restart, proxy connection lifecycle) and OTLP export hardening.

## Next After Current Focus

- Fleet orchestration UX for multi-agent coordination.
- MCP server catalog/discovery workflows.
- Additional proxy-layer security hardening (input/output policy controls).

## Where to Verify Status

- Strategic roadmap and checklists: `ROADMAP.md`
- Refactor sequencing details: `docs/planning/2026-02-17-architecture-refactor-opportunities.md`
- User-visible changes log: `CHANGELOG.md`
- Docs ownership and refresh cadence: `docs/DOCS_MAINTENANCE.md`

## Quick Local Verification

```bash
# CLI and command surface
./bin/loom --help
./bin/loom agent --help

# Runtime health
./bin/loom status
curl http://localhost:9876/health

# Build and quality baseline
make build
go test ./...
golangci-lint run
```
