# Project Roadmap

> Last Updated: February 16, 2026

## Current Status

Loom Core is the production backend for Loom's local MCP runtime:

- `loom` CLI for config generation/sync, daemon control, HUD launch, and agent hooks
- `loomd` daemon for routing, process lifecycle, health monitoring, and tunnel management
- `loom proxy` aggregating proxy for multi-platform agent support (Claude, Codex, Gemini, Zed, VS Code, Kilocode)
- 40+ `mcp-*` server catalog in Go (Git, GitLab, GitHub, K8s, observability, memory, sandbox, and more)
- HUD web dashboard with real-time agent observability, fleet monitoring, and workflow management
- Agent context system with presence, file claims, worktree allocation, and workflow orchestration

## Market Context

> Based on research conducted 2026-02-15. Full analysis: `.loom/12-research-market-trends-2026-02.md`

MCP is now the de facto standard for AI-tool integration (8M+ downloads, 5,800+ servers, Linux Foundation governance). The AI coding market has segmented into IDE assistants (Cursor, Copilot), terminal agents (Claude Code, Codex CLI), and orchestration platforms (GitHub Agent HQ, Augment Intent). Loom Core serves the orchestration layer — the fastest-growing, least commoditized segment.

**Key competitive differentiators:**
- Only tool unifying runtime, proxy, observability, agent orchestration, and multi-platform config in one binary
- 6-platform config sync (no competitor matches this breadth)
- Runtime-integrated HUD (zero-config observability, unlike bolt-on alternatives)
- Worktree allocation + file claims (conflict prevention nobody else has)

**Key market gaps to address:**
- Remote MCP transport + OAuth 2.1 (MCP v1.0 standard, all gateways support it)
- RBAC / access control (enterprise requirement across gateway market)
- OTel trace export (industry standard for enterprise observability integration)
- Cost tracking and audit trails (compliance and visibility)

## Recently Shipped (post `v0.9.7`)

- ✅ **HUD UI/UX overhaul (M1 complete, M2 complete, M3 ~80%, M4 ~85%)**
  - Shipped design system foundation: tokens, type scale, spacing scale, elevation (`tokens.ts`, `theme.css`).
  - Shipped shared primitives: `PanelShell`, `DataTable`, `FilterBar`, `DetailDrawer`, `EmptyState`, `MetricCard`.
  - Shipped navigation restructure: 13 panels grouped into 6 views with badge counts, sub-tabs, keyboard shortcuts.
  - Shipped panel migrations: Tasks, Servers, Fleet, Memory, Presence, Knowledge, Graph panels all use shared components.
  - Shipped DataTable with sortable columns, expandable rows, skeleton loading, `aria-sort`, row click handlers.
  - Shipped DetailDrawer integration in Fleet, Servers, Tasks, Memory, and Graph panels.
  - Shipped FilterBar in Tasks, Memory, Graph, Servers panels.
  - Shipped EmptyState adoption across all panels.
  - Shipped accessibility: semantic HTML, `aria-current`, focus trapping in drawers, keyboard navigation, skip link.
  - Shipped SSE circuit breaker with exponential backoff, incremental fetching, deduplication.

- ✅ **Remote MCP transport (Streamable HTTP)**
  - Added Streamable HTTP listener to `loomd` (`--http-addr` flag) per MCP v1.0 spec.
  - Added bearer token, OIDC, and mTLS authentication modes.
  - Added session management with idle timeout and background reaper.
  - Added `loom auth token-generate/show/revoke` CLI commands.
  - Added `loom proxy --remote` for hybrid local+remote topology.
  - Added origin restriction (DNS rebinding protection) and TLS enforcement for non-localhost.
  - Docs: `docs/STREAMABLE_HTTP.md`.

- ✅ **Agent orchestration**
  - Added presence state machine with nudge system.
  - Added worktree lifecycle reconciler with TTL, disk scan, and orphan removal.
  - Added workflow engine with tool executor via daemon loopback.
  - Added FlexInfer embeddings provider with morph_fast_apply awareness.

- ✅ **Daemon reliability hardening**
  - Added `CloseOnExec` on lock FD to prevent child process lock leaks.
  - Added flock-based singleton enforcement with stale socket detection.
  - Added `EnsureRunning` helper for proxy autostart.
  - Added LaunchAgent kickstart with direct-spawn fallback.

- ✅ **Devbox sandboxing**
  - Added `mcp-devbox` with project fingerprinting, Dockerfile generation, and persistent sandbox lifecycle.
  - Added K8s backend with Kaniko in-cluster builds (replacing Docker build).
  - Added async tools (`devbox_exec_async`, `devbox_exec_poll`) and observability tools (`devbox_metrics`, `devbox_summary`).
  - Added HUD sandbox controls.

- ✅ **Multi-platform config generation**
  - Added registry-driven platform permissions for Claude, Codex, Gemini, Zed.
  - Added Claude permission rule validation and Codex web_search support.
  - Added shell completion command and `LOOM_SOCKET` env support.

- ✅ **MCP server quality**
  - Added MCP server tests and OTel instrumentation.
  - Added error handling guardrails with `pkg/mcperror`.
  - Added config schema validation and upstream spec tracking.

- ✅ **Developer lifecycle**
  - Added atomic install scripts and `make dev-upgrade` / `make dev-reload` workflow.
  - Added rollback-friendly `.prev` binary flow and safer restart behavior.

## Tier 1: Strengthen Existing Moats (Current)

These build on shipped work and address immediate quality and reliability.

- [ ] **Finish HUD M3/M4** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/7))
  - ~~DetailDrawer integration for all views (Fleet, Servers, Tasks, Memory, Graph).~~ ✅ Done
  - ~~FilterBar across major panels (Tasks, Memory, Graph, Servers).~~ ✅ Done
  - ~~Accessibility: semantic HTML, `aria-sort`, focus trapping, skip link, keyboard nav.~~ ✅ Done
  - ~~SSE circuit breaker, incremental fetching, D3 simulation pause.~~ ✅ Done
  - Add bulk actions toolbar for Tasks, Memory, and Claims.
  - Add VirtualList adoption for large lists (Fleet 100+ sessions, Tasks).
  - Ship color-blind safe status indicators (shape variants alongside color).
  - Lazy-load heavy panels (Topology, Graph, Lifecycle).

- [ ] **Raise test coverage to 40%+** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/2))
  - Add happy-path + error-path + mcperror shape tests for `mcp-devbox`, `mcp-agent-context`, and newest servers.
  - Add daemon lifecycle tests: flock contention, socket cleanup, proxy autostart, graceful shutdown.
  - Add integration tests for Docker + K8s devbox backends under monorepo layouts.
  - Target: 40% overall coverage (up from 21.2%).

- [ ] **Onboarding and docs consistency** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/6))
  - Keep README/docs/changelog synchronized with shipped command and tool surface.
  - Maintain one canonical docs entrypoint for user/developer/operator tasks.
  - Polish `make bootstrap-local` first-run experience.

## Tier 2: Capture Market Gaps (Next)

These address capabilities the market now expects from production MCP infrastructure.

- [ ] **OTel trace export from daemon**
  - Broaden `pkg/mcpotel` adoption across all high-traffic MCP servers.
  - Instrument tool call latency, server spawn/restart, proxy connection lifecycle in `loomd`.
  - Add OTLP gRPC export to configurable endpoint (Prometheus, Grafana, Jaeger).
  - Expose OTel-compatible metrics in HUD health views.
  - *Rationale: Industry standard. Positions Loom alongside (not against) Langfuse, Datadog, Splunk.*

- [x] **Remote MCP transport + auth** ✅ Shipped
  - ~~Add Streamable HTTP transport to `loomd` (MCP v1.0 spec compliance).~~ ✅ Done
  - ~~Add bearer token, OIDC, and mTLS authentication for remote access.~~ ✅ Done
  - ~~Enable hybrid local+remote topology (local proxy connecting to remote daemon).~~ ✅ Done
  - Remaining: OAuth 2.1 dynamic client registration ([#11](https://gitlab.flexinfer.ai/services/loom-core/-/issues/11)) (OIDC covers static clients today).
  - *Rationale: Transforms Loom from local dev tool into team/org infrastructure. Every MCP gateway supports this. Biggest single unlock for adoption.*

- [ ] **Lightweight RBAC for tool access** ([#8](https://gitlab.flexinfer.ai/services/loom-core/-/issues/8))
  - Add role-to-tool mapping in daemon config (e.g., restrict destructive tools per agent).
  - Enforce per-agent tool scoping at the proxy layer.
  - Log access decisions for audit.
  - *Rationale: #1 enterprise requirement. All gateway competitors (Kong, Lunar.dev, TrueFoundry) offer this.*

- [ ] **Cost tracking and attribution** ([#10](https://gitlab.flexinfer.ai/services/loom-core/-/issues/10))
  - Track token usage per agent session, per tool, per MCP server at the proxy layer.
  - Expose cost dashboard in HUD (new KPI on Overview panel).
  - Export cost metrics via OTel.
  - *Rationale: The proxy already sees all traffic. Adding token counting is incremental. No local tool provides this today.*

- [ ] **Structured audit trail** ([#9](https://gitlab.flexinfer.ai/services/loom-core/-/issues/9))
  - Produce structured JSON event for every tool call through the proxy (agent_id, session, tool, server, duration, status).
  - Store in append-only log file, exportable to SIEM/observability tools.
  - *Rationale: Compliance requirement for enterprise adoption. Addresses "Shadow MCP" concerns.*

## Tier 3: Strategic Differentiation (Future)

These position Loom Core in ways competitors cannot easily replicate.

- [ ] **Fleet orchestration UX**
  - Add dispatch panel in HUD for assigning tasks to agents and tracking parallel progress.
  - Surface file claim conflicts in HUD when agents touch overlapping files.
  - Add merge orchestration assistance after parallel agents complete work.
  - Improve cross-agent context transfer via the handoff system.
  - *Rationale: Market is moving to "developer as fleet commander" pattern (Augment Intent, GitHub Agent HQ, Cursor Parallel Agents).*

- [ ] **MCP server catalog and discovery**
  - Add `loom catalog list` for browsable server catalog with capabilities and env requirements.
  - Add `loom catalog enable <server>` for one-command server activation.
  - Add HUD catalog view for browse, enable/disable, and per-server health.
  - *Rationale: 40+ curated Go servers is a unique asset. Docker MCP Toolkit has a catalog; Loom should too.*

- [ ] **Security hardening layer**
  - Add input schema validation at proxy before forwarding to servers.
  - Add output scanning for PII/secrets in tool responses.
  - Add per-agent, per-tool rate limiting.
  - Add deny-list for blocking tool calls based on policy.
  - *Rationale: MCP security is enterprise-critical. Lasso, MCP Manager, MCP Total are emerging competitors.*

## Ongoing Engineering Goals

- Keep tool-call latency bounded under typical client deadlines (~60s).
- Preserve backwards compatibility for `loom proxy` and generated client configs.
- Maintain secure defaults around secrets interpolation and config validation.
- Monitor A2A Protocol (Google) and MCP Code Mode (Cloudflare) for future integration.

## References

- `README.md`
- `docs/README.md`
- `docs/ARCHITECTURE.md`
- `docs/DEV_BUILD_LIFECYCLE.md`
- `docs/STREAMABLE_HTTP.md` — Streamable HTTP transport setup and configuration
- `.loom/12-research-market-trends-2026-02.md` — Market & platform strategic analysis (2026-02-15)
- `.loom/10-research.md` — HUD UI/UX research brief
- `.loom/20-product-spec.md` — HUD overhaul product spec
- `.loom/30-implementation-plan.md` — HUD overhaul implementation plan
- `docs/planning/2026-02-quality-onboarding-opportunities.md` — Quality and onboarding opportunities
