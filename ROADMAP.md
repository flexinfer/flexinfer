# Project Roadmap

> Last Updated: February 11, 2026

## Current Status

Loom Core is the production backend for Loom’s local MCP runtime:

- `loom` CLI for config generation/sync, daemon control, HUD launch, and agent hooks
- `loomd` daemon for routing, process lifecycle, health monitoring, and tunnel management
- Broad `mcp-*` server catalog (Git, GitLab, GitHub, K8s, observability, memory, sandbox, and more)

## Recently Shipped (post `v0.9.7`)

- ✅ **Devbox sandboxing**
  - Added `mcp-devbox` with project fingerprinting, Dockerfile generation, and persistent sandbox lifecycle.
  - Added K8s backend support for sandbox execution.
  - Added async tools (`devbox_exec_async`, `devbox_exec_poll`) and observability tools (`devbox_metrics`, `devbox_summary`).
  - Improved monorepo support (workspace-root mount + project-aware workdir).

- ✅ **HUD improvements**
  - Added sandbox panel integration (via `devbox_summary`).
  - Added richer TUI/web polish and notification/UX refinements.
  - Added Ghostty palette/shader integration helpers.

- ✅ **Developer lifecycle hardening**
  - Added atomic install scripts and `make dev-upgrade` workflow.
  - Added rollback-friendly `.prev` binary flow and safer restart behavior.

## Near-Term Priorities

- [ ] **Quality gates for new MCP servers**
  - Ensure each newly added `mcp-*` server has baseline tests and lint-clean handlers.
  - Standardize error handling (`pkg/mcperror`) and argument validation (`pkg/validate`).

- [ ] **Devbox maturity**
  - Expand integration tests for Docker + K8s backends under realistic monorepo layouts.
  - Add stronger safeguards for long-running async exec cleanup/recovery.

- [ ] **Onboarding and docs consistency**
  - Keep README/docs/changelog synchronized with shipped command and tool surface.
  - Maintain one canonical docs entrypoint for user/developer/operator tasks.

- [ ] **Observability expansion**
  - Broaden `pkg/mcpotel` adoption across additional MCP servers.
  - Keep HUD health/sandbox/fleet views aligned with backend metrics and events.

## Ongoing Engineering Goals

- Keep tool-call latency bounded under typical client deadlines (~60s).
- Preserve backwards compatibility for `loom proxy` and generated client configs.
- Maintain secure defaults around secrets interpolation and config validation.

## References

- `README.md`
- `docs/README.md`
- `docs/ARCHITECTURE.md`
- `docs/DEV_BUILD_LIFECYCLE.md`
