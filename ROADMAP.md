# Project Roadmap

> Last Updated: April 14, 2026

## Current Status

Loom Core is the production backend for Loom's local MCP runtime:

- `loom` CLI for config generation/sync, daemon control, HUD launch, and agent hooks
- `loomd` daemon for routing, process lifecycle, health monitoring, and tunnel management
- `loom proxy` aggregating proxy for multi-platform agent support (Claude, Codex, Gemini, Zed, VS Code, Kilocode)
- `cmd/mcp-*` server binaries in Go (Git, GitLab, GitHub, K8s, observability, memory, sandbox, and more)
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

- ✅ **HUD UI/UX overhaul (M1-M4 complete)**
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
  - 2026-04-14: added Codex keepalive bootstrap via `loom agent keepalive-wrap`, surfaced degraded-backend recall metadata/warnings, and introduced workflow `auto_verify` gates backed by `devbox_quality_gate`.

- ✅ **HUD traces + live-agent coherence (2026-04-14)**
  - Added daemon audit-backed trace summaries plus HUD `Traces` panel drilldowns for recent tool-call latency and status inspection.
  - Unified desktop Fleet, Presence, Overview, and footer/status-bar agent counts around one merged live-agent model spanning sessions, presence heartbeats, and spawned agents.
  - Added direct `Session` and agent-filtered `Traces` drilldowns from live-agent surfaces so operators can move from "who is active" to "what just happened" without context loss.

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

- ✅ **Observability expansion (2026-02-26 slices)** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/5))
  - Added `pkg/mcpotel` tracing wrappers across all `cmd/mcp-*/main.go` handlers.
  - Added JSON log formatting in `pkg/mcplog` with `trace_id`/`span_id` correlation for context-aware logs.

- ✅ **HUD launchd operations**
  - Added `loom hud install|start|stop|status|uninstall`.
  - Added `~/.config/loom/hud.env` loading for launchd-started HUD secrets.
  - Added HUD status output (including cache backend) to `loom status`.
  - Added `make hud-install-service` and HUD restart wiring in `make dev-upgrade`.

- ✅ **Mobile + HUD information architecture polish (2026-03-31)**
  - Reframed the companion app around `Dashboard`, `People`, `Work`, `Alerts`, and `Connection`, with `People` explicitly split into `Agents` and `Sessions`.
  - Routed mobile deep links through clearer destination groups and staged session/connection mutations behind advanced controls.
  - Reframed the HUD landing tab as `Now`, shifted overview toward attention-first triage, and renamed top-level groups to a more operator-oriented vocabulary.
  - Elevated Dispatch and Server Catalog into clearer first-class operator surfaces with stronger summaries, hierarchy, and empty-state guidance.
  - Follow-up mobile polish now degrades missing pipeline data into a local unavailable state and groups agents by recovered project/runtime context so unscoped Claude processes stop reading as a flat list.

- ✅ **Sync/worktree workflow polish**
  - Added session-start nudge recommending worktree allocation on `main`/`master`.
  - Added Antigravity `settings.json` hooks stub generation and sync parity.

- ✅ **Developer lifecycle**
  - Added atomic install scripts and `make dev-upgrade` / `make dev-reload` workflow.
  - Added rollback-friendly `.prev` binary flow and safer restart behavior.

## Tier 1: Strengthen Existing Moats (Current)

These build on shipped work and address immediate quality and reliability.

- [x] **Finish HUD M3/M4** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/7)) ✅ Complete
  - ~~DetailDrawer integration for all views (Fleet, Servers, Tasks, Memory, Graph).~~ ✅ Done
  - ~~FilterBar across major panels (Tasks, Memory, Graph, Servers).~~ ✅ Done
  - ~~Accessibility: semantic HTML, `aria-sort`, focus trapping, skip link, keyboard nav.~~ ✅ Done
  - ~~SSE circuit breaker, incremental fetching, D3 simulation pause.~~ ✅ Done
  - ~~Add bulk actions toolbar for Tasks, Memory, and Claims.~~ ✅ Done
  - ~~Add row pagination (maxRows) for large lists (Fleet, Tasks).~~ ✅ Done
  - ~~Ship color-blind safe status indicators (shape variants alongside color).~~ ✅ Done
  - ~~Lazy-load heavy panels (Topology, Graph, Lifecycle).~~ ✅ Done

- [x] **Raise test coverage to 40%+** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/2)) ✅ Target met
  - ~~Add smoke tests for 10 MCP servers (youtube, itchio, crypto, release, morph-fast-apply, alertmanager, minio, substack, qdrant, morph-embeddings).~~ ✅ Done
  - ~~Add enterprise edge-case tests (RBAC, audit, cost, OAuth).~~ ✅ Done
  - ~~Add agentcontext gap-fill tests (workflows, memory hierarchy, service).~~ ✅ Done
  - Target reached: 40.7% coverage on branch pipeline `#5635` (merge commit `bc2c5b99`).
  - Follow-on hardening still worth doing: daemon lifecycle tests (flock contention, socket cleanup, proxy autostart, graceful shutdown).
  - Follow-on hardening still worth doing: integration tests for Docker + K8s devbox backends under monorepo layouts.

- [ ] **Onboarding and docs consistency** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/6))
  - ~~Add `docs/ENTERPRISE_SECURITY.md` covering RBAC, audit, cost, OAuth.~~ ✅ Done
  - ~~Expand `docs/STREAMABLE_HTTP.md` with OAuth 2.1 auth type.~~ ✅ Done
  - ~~Update README, docs hub, CHANGELOG, and ROADMAP.~~ ✅ Done
  - Keep README/docs/changelog synchronized with shipped command and tool surface.
  - Maintain one canonical docs entrypoint for user/developer/operator tasks.
  - Polish `make bootstrap-local` first-run experience.

## Immediate Architecture Refactor Focus (Current)

Derived from commit-window review (`2026-02-15` to `2026-02-17`) to reduce regression risk before expanding Tier 3 scope.

- [x] **Harden daemon tool-call pipeline extraction** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/20)) ✅ Complete
  - ✅ Stage 1 complete: `handleCall` orchestration now delegates to `internal/daemon/callpipeline.go`.
  - ✅ Stage 2 complete: isolated side effects (audit/cost/cache/metrics) into dedicated pipeline helpers and added targeted stage-failure tests for route/connect + transport paths.
  - ✅ 2026-03-27: centralized parse/build/route error-envelope construction and added pipeline stage-boundary regression coverage.
  - Target outcome achieved: lower conflict/churn in `internal/daemon/daemon.go` and clearer test seams.

- [x] **Finish agent contract convergence across HUD/CLI/bridge** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/21)) ✅ Complete
  - ✅ Stage 1 complete: shared contracts for context-inspect + nudge policy in `internal/hud/bridge/agent_contracts.go`.
  - ✅ 2026-03-24: agent-context sessions/tasks now carry explicit `project`, `pipeline_ref`, and `workflow_id` links through MCP tools, HUD bridge, and mobile task projections; workspace-style namespaces such as `services/loom-core/...` now preserve full repo identity during orchestration grouping.
  - ✅ 2026-03-27: session-start, session-end, and heartbeat now share normalized request validation across the bridge contract layer, HUD fleet handlers, and CLI fallback commands.
  - Target outcome achieved: single contract model for context-inspect, session lifecycle, nudge queue, and policy mutation surfaces.

- [x] **Split oversized HUD panel/state surfaces** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/22)) ✅ Complete
  - ✅ 2026-02-17: moved diagnostics polling/fetch/mutation logic into `presenceDiagnosticsStore` and kept `PresenceDiagnosticsTab.svelte` view-only; added TUI claim-conflict visibility in `internal/tui/panels/presence.go` for HUD/TUI parity.
  - ✅ 2026-02-21: extracted dispatch/nudge/handoff modals from `PresencePanel.svelte` into `DispatchTaskModal`, `NudgeAgentModal`, `CreateHandoffModal` components; moved `fileConflicts` derived logic into `presenceStore` getter.
  - Target outcome: safer iteration for Fleet orchestration UX work ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/13)).

- [ ] **Refactor devbox K8s backend by concern** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/23))
  - `internal/devbox/backend/` is now partially decomposed (`k8s_build.go`, `k8s_runtime.go`, `k8s_sync.go`, `k8s_wait.go`, `k8s_objects.go`), but further seam cleanup and integration coverage still remain.
  - Continue separating build pod orchestration from runtime lifecycle logic where `K8sBackend` still couples concerns across files/tests.
  - Target outcome: faster iteration and stronger integration-test coverage for Roadmap Issue #2 remaining devbox work.

- [x] **Reduce HUD dist artifact churn in feature commits** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/24)) ✅ Complete
  - ✅ 2026-02-21: added `.gitattributes` marking dist JS/CSS as `linguist-generated -diff` to collapse in MR diffs; added `make hud-dist-check` target for local/CI freshness verification.
  - Target outcome: cleaner review diffs and faster root-cause analysis during regressions.

## Tier 2: Capture Market Gaps (Next)

These address capabilities the market now expects from production MCP infrastructure.

- [ ] **Mobile Companion (MBL-8): Scope discipline enforcement (cross-cutting)** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/37))
  - Add a mandatory milestone exit scope-gate check for M0-M5 mobile companion work.
  - Route desktop-parity and other out-of-scope asks into post-v1 backlog issues before implementation.
  - Keep v1 constrained to monitoring + session lifecycle controls unless explicitly re-baselined.
  - 2026-03-31 IA pass: mobile navigation now biases toward `People` and `Work`, and advanced session/connection mutations are visually staged behind the primary monitoring workflow.

- [x] **Codex keepalive + recall observability hardening** ✅ Shipped
  - ✅ 2026-04-14 slice: added a wrapper-capable `loom agent keepalive-wrap` path and moved generated Codex notify hooks to bootstrap the background keepalive helper for idle-session visibility.
  - ✅ 2026-04-14 slice: unified recall now records backend-scoped latency summaries and explicitly surfaces degraded-backend warnings/tests alongside `recall_meta`.
  - Follow-on: Codex config still relies on `notify` to launch the helper because native TOML cannot yet describe a true long-lived process wrapper for the CLI itself.

- [ ] **OTel trace export from daemon** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/12))
  - ✅ 2026-02-26 slice: added `pkg/mcpotel` tracing wrappers to `mcp-alertmanager`, `mcp-grafana`, and `mcp-loki` (tool spans + error status propagation).
  - ✅ 2026-02-26 slice: expanded `pkg/mcpotel` tracing to `mcp-github`, `mcp-github-actions`, `mcp-jira`, and `mcp-slack`.
  - ✅ 2026-02-26 slice: completed `pkg/mcpotel` adoption across the remaining MCP binaries (`cmd/mcp-*/main.go` handlers traced).
  - ✅ 2026-02-26 slice: added `pkg/mcplog` `MCP_LOG_FORMAT` (`text`/`json`) plus automatic `trace_id`/`span_id` enrichment for context-aware logs.
  - ✅ 2026-03-27 slice: daemon runtime now initializes/shuts down its tracer from env/config and `loom/otel-status` reports runtime state instead of config-only intent.
  - ✅ 2026-04-14 slice: daemon audit entries now capture stage timing breakdowns for proxied tool calls, `loom/audit-traces` / `GET /api/traces` expose recent trace summaries, and the HUD `Traces` panel surfaces recent calls with status filters and latency inspection.
  - Next: instrument tool call latency, server spawn/restart, and proxy connection lifecycle in `loomd`.
  - Next: extend the trace surface from audit-backed summaries into fuller OTel-compatible export and HUD percentile/waterfall views.
  - *Rationale: Industry standard. Positions Loom alongside (not against) Langfuse, Datadog, Splunk.*

- [ ] **OpenAI Responses orchestration (experimental track)** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/63), [Plan](.loom/36-implementation-plan-openai-responses-orchestration-2026-03-04.md))
  - ✅ 2026-03-04 M0 slice landed: `pkg/openairesponses` contract scaffolding (`ContextStrategy`, tool/turn interfaces, validation) and environment-driven feature gate config (`LOOM_EXPERIMENTAL_OPENAI_RESPONSES`).
  - ✅ 2026-03-04 CLI surface landed: `loom responses status` exposes gate/runtime loop settings without changing existing proxy behavior.
  - ✅ 2026-03-04 M1 package slice landed: non-stream loop orchestrator in `pkg/openairesponses/orchestrator.go` with deterministic multi-turn tool execution tests.
  - ✅ 2026-03-04 runtime entrypoint slice landed: `loom responses run` now invokes the orchestrator through a gated runtime dependency factory.
  - ✅ 2026-03-06 M1 runtime wiring landed: `pkg/openairesponses/client.go` adds a production Responses HTTP client with bounded retries, and `cmd/loom/cmd_responses_runtime.go` routes tool inventory + tool execution through the daemon socket with identity propagation.
  - Next: add M2 token preflight and compaction controls, then cover RBAC/policy/audit behavior with end-to-end orchestration tests.
  - *Rationale: Adds policy/audit-compatible OpenAI Responses support without destabilizing existing MCP proxy paths.*

- [x] **Remote MCP transport + auth** ✅ Shipped
  - ~~Add Streamable HTTP transport to `loomd` (MCP v1.0 spec compliance).~~ ✅ Done
  - ~~Add bearer token, OIDC, and mTLS authentication for remote access.~~ ✅ Done
  - ~~Enable hybrid local+remote topology (local proxy connecting to remote daemon).~~ ✅ Done
  - ~~OAuth 2.1 authorization server with PKCE, dynamic client registration, AS/resource metadata ([#11](https://gitlab.flexinfer.ai/services/loom-core/-/issues/11)).~~ ✅ Done
  - *Rationale: Transforms Loom from local dev tool into team/org infrastructure. Every MCP gateway supports this. Biggest single unlock for adoption.*

- [x] **Lightweight RBAC for tool access** ([#8](https://gitlab.flexinfer.ai/services/loom-core/-/issues/8)) ✅ Shipped
  - ~~Add role-to-tool mapping in daemon config (e.g., restrict destructive tools per agent).~~ ✅ Done
  - ~~Enforce per-agent tool scoping at the proxy layer.~~ ✅ Done
  - ~~Log access decisions for audit.~~ ✅ Done
  - *Rationale: #1 enterprise requirement. All gateway competitors (Kong, Lunar.dev, TrueFoundry) offer this.*

- [x] **Cost tracking and attribution** ([#10](https://gitlab.flexinfer.ai/services/loom-core/-/issues/10)) ✅ Shipped
  - ~~Track token usage per agent session, per tool, per MCP server at the proxy layer.~~ ✅ Done
  - HUD baseline landed on `main`: CostMonitor polling, `GET /api/cost`, SSE `hud.cost`, and Overview KPI tile are now present; [Issue #52](https://gitlab.flexinfer.ai/services/loom-core/-/issues/52) remains open for backlog/status cleanup and any remaining telemetry parity gaps.
  - Export cost metrics via OTel ([Issue #52](https://gitlab.flexinfer.ai/services/loom-core/-/issues/52)).
  - *Rationale: The proxy already sees all traffic. Adding token counting is incremental. No local tool provides this today.*

- [x] **Structured audit trail** ([#9](https://gitlab.flexinfer.ai/services/loom-core/-/issues/9)) ✅ Shipped
  - ~~Produce structured JSON event for every tool call through the proxy (agent_id, session, tool, server, duration, status).~~ ✅ Done
  - ~~Store in append-only log file, exportable to SIEM/observability tools.~~ ✅ Done
  - *Rationale: Compliance requirement for enterprise adoption. Addresses "Shadow MCP" concerns.*

## Tier 3: Strategic Differentiation (Future)

These position Loom Core in ways competitors cannot easily replicate.

- [ ] **Fleet orchestration UX** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/13))
  - ✅ 2026-04-14: desktop Fleet, Presence, Overview, and status-bar counts now share a merged live-agent model across presence, sessions, and spawned agents; Fleet/Presence rows also deep-link directly into session detail and agent-filtered traces.
  - Add dispatch panel in HUD for assigning tasks to agents and tracking parallel progress.
  - Surface file claim conflicts in HUD when agents touch overlapping files.
  - Add merge orchestration assistance after parallel agents complete work.
  - Improve cross-agent context transfer via the handoff system.
  - 2026-03-31 IA pass: Dispatch now reads as a coordination-first operator surface, but merge orchestration and deeper action flows remain future work.
  - *Rationale: Market is moving to "developer as fleet commander" pattern (Augment Intent, GitHub Agent HQ, Cursor Parallel Agents).*

- [ ] **MCP server catalog and discovery** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/14))
  - Add `loom catalog list` for browsable server catalog with capabilities and env requirements.
  - Add `loom catalog enable <server>` for one-command server activation.
  - Add HUD catalog view for browse, enable/disable, and per-server health.
  - 2026-03-31 IA pass: HUD catalog framing now better supports discovery and trust, while CLI parity and deeper capability metadata remain open.
  - *Rationale: 40+ curated Go servers is a unique asset. Docker MCP Toolkit has a catalog; Loom should too.*

- [ ] **Security hardening layer** ([#25](https://gitlab.flexinfer.ai/services/loom-core/-/issues/25), [#26](https://gitlab.flexinfer.ai/services/loom-core/-/issues/26), [#27](https://gitlab.flexinfer.ai/services/loom-core/-/issues/27), [#29](https://gitlab.flexinfer.ai/services/loom-core/-/issues/29))
  - Add input schema validation at proxy before forwarding to servers.
  - Add output scanning for PII/secrets in tool responses.
  - Add per-agent, per-tool rate limiting.
  - Add deny-list for blocking tool calls based on policy.
  - Note: prior umbrella issue `#15` is closed; active work is tracked in the concrete slice issues above.
  - *Rationale: MCP security is enterprise-critical. Lasso, MCP Manager, MCP Total are emerging competitors.*

## Tier 4: Architecture Simplification (Planned)

Derived from architectural review identifying tool surface bloat, visibility sprawl, and config complexity. Full backlog: `.loom/35-simplification-epics.md`.

- [ ] **EPIC 1: Simplify Agent Context** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/65)) — Reduce 80 MCP tools to ~45 via deprecation, facade unification, and service decomposition (12 issues: SIMP-1 through SIMP-12)
- [ ] **EPIC 2: Unify Visibility** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/66)) — Shared API contracts, embedded HUD, richer CLI/TUI (5 issues: UNIFY-1 through UNIFY-5)
- [ ] **EPIC 3: Reduce Config Complexity** ([Issue](https://gitlab.flexinfer.ai/services/loom-core/-/issues/67)) — Data-driven platform profiles replacing hardcoded generators (4 issues: CONFIG-1 through CONFIG-4)

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
- `docs/planning/2026-02-19-enterprise-gateway-rbac-devbox-plan.md` — Scoped enterprise delivery plan for gateway, RBAC, and devbox executor
- `.loom/12-research-market-trends-2026-02.md` — Market & platform strategic analysis (2026-02-15)
- `.loom/10-research.md` — HUD UI/UX research brief
- `.loom/20-product-spec.md` — HUD overhaul product spec
- `.loom/30-implementation-plan.md` — HUD overhaul implementation plan
- `docs/planning/2026-02-quality-onboarding-opportunities.md` — Quality and onboarding opportunities
- `docs/planning/2026-02-17-architecture-refactor-opportunities.md` — Commit-window architecture/refactor focus and sequencing
