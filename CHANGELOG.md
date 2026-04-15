# Changelog

All notable changes to loom-core will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **HUD traces panel and daemon trace feed** (`internal/daemon`, `internal/hud`): Loom now records stage timing breakdowns (`route_ms`, `build_ms`, `execute_ms`, `send_ms`, `recv_ms`) on audit entries, exposes recent trace summaries through the daemon `loom/audit-traces` RPC and HUD `GET /api/traces`, and adds a `Traces` activity panel for recent tool calls, status filters, and latency inspection.
- **Workflow `auto_verify` gates** (`pkg/agentcontext`, `.agents/workflows`): workflows can now run a built-in `devbox_quality_gate` verification step that preserves structured gate failures on the step state and reuses existing workflow retry policy. The shipped `feature-dev-auto` workflow now uses the new step type instead of a manual approval gate.
- **Registry-driven proxy policy engine** (`pkg/policy`): Proxy enforcement now reads blocked commands and denial messages from `registry.yaml` guardrails instead of hard-coded Go constants, enabling new policies via YAML without recompilation.
- **Platform policy refs** (`pkg/generator`): All 8+ platforms in `platform_profiles.yaml` now declare `policy_refs` and an `enforcement` mode (native, proxy, or plugin). Claude and Gemini use native hook enforcement; Codex, Kilocode, Antigravity, VS Code, and Zed annotate proxy-level enforcement.
- **Policy health tracking** (`pkg/sync`, `pkg/generator/doctor.go`): `loom doctor` reports per-platform policy status and enforcement mode. `loom sync status` tracks guardrails hash staleness.
- **Standalone orchestra MCP binary** (`cmd/mcp-orchestra`): New standalone binary with `HubToolLister` and `HubToolCaller` that route tool calls through the MCP gateway, enabling orchestra to run as an independent k3s service decoupled from `loomd`.
- **Orchestra k3s deployment** (`k8s/base/servers/orchestra`): Deployment, service, and configmap manifests following the agent-context pattern; registered in gateway registry for service discovery.
- **Orchestra domains** (`pkg/orchestra`): Add `agent-fleet` (presence/session/task tools) and `infra-ops` (flux/helm tools) domains, bringing total from 4 to 6. New `orchestra__fleet_status` compound tool.
- **Agent recall graph scope** (`pkg/agentcontext`): `agent_recall` now supports `scope=graph` and `scope=all` to query the in-memory knowledge graph backend via `FindEntities()`.
- **Skills registry entries** (`mcp/context/skills-registry.yaml`): Add `testing-guidelines`, `deployment-practices`, and `rust-acceleration` (56→59 entries).

### Fixed
- **HUD live-agent unification** (`internal/hud/frontend`): desktop Fleet, Presence, Overview, and status-bar surfaces now derive from a shared merged agent model that combines live sessions, presence heartbeats, and spawned agents. Presence-only and session-only agents now stay visible consistently, Fleet rows expose direct `Session` and `Traces` drilldowns, and the Traces panel can be filtered directly to a selected agent.
- **HUD attention drilldowns** (`internal/hud/frontend`): Overview attention lanes and Lifecycle side-rail cards now open the most useful next surface instead of just switching tabs. Attention agents resolve to Fleet session detail when a live session exists and fall back to agent-filtered traces otherwise, while namespace and relation hotspots jump into Dispatch.
- **Codex keepalive and recall degradation visibility** (`cmd/loom`, `pkg/generator`, `pkg/agentcontext`): generated Codex hooks now bootstrap the background keepalive helper via `loom agent keepalive-wrap`, idle sessions retain presence more reliably, and `agent_recall` now surfaces backend-scoped latency and degraded-backend warnings through `recall_meta` instead of silently hiding partial failures.
- **Devbox Go base image rebuild compatibility** (`internal/devbox/baseimage/dockerfiles`): pin `golangci-lint` and `goimports` versions per Go toolchain so weekly devbox base image rebuilds do not break when upstream `@latest` tools raise their minimum supported Go version.
- **Mobile dashboard attention lanes** (`internal/hud/domain/mobile`, `internal/hud/frontend`, `apps/loom-companion-ios`): mobile dashboard payloads now expose labeled attention lanes with route and severity metadata, the iOS companion surfaces them as first-class dashboard actions, and the HUD overview rail prioritizes the same shared attention targets for triage.
- **Orchestra token metrics** (`pkg/orchestra`): `loom_orchestra_tokens_total` now increments with real prompt/completion token counts from FlexInfer `resp.Usage` instead of the `len(toolResults)*512` estimate. `DomainResult.Tokens` reflects actual usage.
- **Orchestra classify span** (`pkg/orchestra/router.go`): Remove dead `defer func(){}()` and redundant `SpanFromContext` re-fetch; use captured span variable directly.
- **MCP server scaffold package** (`pkg/mcpscaffold`): `NewServer()` factory and `AddTracedTool()` helper that eliminate ~65 lines of duplicated initialization boilerplate per MCP server. Five servers migrated as proof (time, filesystem, asus-router, youtube, confluence).
- **Pipeline-aware agent lifecycle** (`internal/hud/bridge`): Explicit `PipelineRef` foreign keys on Session and Task, replacing branch-name heuristics; `WorkflowID` on Task for workflow-step tracing; `pipeline_event` context entries; auto-detection in WorkStart.
- **Mobile session activity endpoint** (`internal/hud/domain/mobile`): `GET /api/sessions/{id}/activity` returns unified tasks + pipelines with correlation type.
- **Mobile pipelines endpoint** (`internal/hud/domain/mobile`): `GET /api/mobile/v1/pipelines` with cold-start refresh, fallback to recent pipelines, and agent-branch correlation.
- **Pipeline monitor** (`internal/hud/monitor`): `PipelineMonitor` with adaptive 10s/60s polling intervals and 10s detail cache TTL.
- **Task projection** (`internal/hud/domain/mobile`): Synthesize tasks from agent presence `current_task` field into the unified task feed with deterministic SHA1-based IDs.
- **Workflow deprecation** (`internal/hud/domain/mobile`): Migration flags on approval surface to prepare transition away from workflow endpoints.

### Fixed
- **Agent session contract parity** (`internal/hud/bridge`, `internal/hud/domain/fleet`, `cmd/loom`): session-start, session-end, and heartbeat flows now share normalized request validation across the bridge contract layer, HUD fleet handlers, and CLI fallback commands, reducing transport-specific whitespace/default drift.
- **Daemon connection saturation recovery** (`internal/daemon`, `cmd/loom`): Cancelled daemon and hub calls now return pooled connections through the pool so capacity counters do not leak; proxy retry classification also treats retryable route/connect pressure errors as transport resets, and `loom/status` now surfaces local and hub pool pressure snapshots for diagnosis.
- **Daemon OTel runtime wiring** (`internal/daemon`): daemon startup now initializes the OpenTelemetry provider from env/file config when present, shutdown flushes the configured tracer once, and `loom/otel-status` reports runtime OTel state alongside the existing coverage summary.
- **Codex Loom auto-approval and mobile health severity** (`pkg/generator`, `internal/hud/monitor`): Codex loom-proxy configs now emit `always_allow = ["*"]` so Loom tools stay auto-approved after sync, and transient monitor/router health gaps now surface as degraded instead of down to avoid false critical server-down states in the mobile dashboard.
- **Home-authoritative CLI approvals/config** (`pkg/sync`, `cmd/loom`): workspace-wide sync can now strip home-managed Claude/Gemini approval settings from project copies, and it cleans stale home-managed Codex/Kilo config files from workspace projects so Loom tool approvals stop drifting per project.
- **iOS export CI checkout** (`.gitlab-ci.yml`): `ios:archive-export` now overrides the global `GIT_STRATEGY: none` setting with `fetch` so the signing and export scripts are present when the manual macOS job runs on `main` or tag pipelines.
- **Mobile HUD live-state filtering** (`internal/hud/domain/mobile`, `internal/hud/coordination`): Agent and coordination surfaces now ignore ended and summarized sessions when building live mobile snapshots, preventing ghost offline agents, stale session metadata on active agents, and inflated namespace counts after daemon/HUD refresh races.
- **Mobile dashboard agent totals** (`internal/hud/domain/mobile`): Dashboard headline counts now use the same unified live-agent view as the Agents tab, so active session-only agents are reflected consistently after daemon reloads.
- **Local dev reload smoke** (`scripts/dev`): `make dev-upgrade` and `make dev-reload` now wait on the embedded HUD/mobile API readiness path instead of treating the heavier mobile task feed as the reload health gate, reducing false unhealthy warnings after daemon fallback restarts.

### Security
- **gRPC security upgrade** (`go.mod`): Upgrade `google.golang.org/grpc` v1.78.0 → v1.79.3 to fix GO-2026-4762 (authorization bypass via missing leading slash in `:path` header).

### Changed
- **schema.go split** (`pkg/agentcontext`): Split 1,257-line `schema.go` into `schema_workflow.go` (311), `schema_graph.go` (196), `schema_memory.go` (204), and `schema_presence.go` (119). Residual schema.go retains core types and utility functions (447 lines). DEBT-050.
- **svc_context.go split** (`pkg/agentcontext`): Split 1,098-line `svc_context.go` into `svc_context_add.go` (364), `svc_context_search.go` (173), `svc_context_summary.go` (238), and `svc_context_annotations.go` (322). Residual retains struct and constructor (43 lines). DEBT-051.
- **daemon_dispatch.go split** (`internal/daemon`): Split 928-line `daemon_dispatch.go` into `daemon_dispatch_status.go` (375), `daemon_dispatch_ops.go` (237), and `daemon_dispatch_otel.go` (217). Residual retains message router (129 lines). DEBT-052.
- **ops.go split** (`pkg/sync`): Split 969-line `ops.go` into `ops_sync.go` (466), `ops_regen.go` (331), and `ops_validate.go` (146). Residual retains discovery helpers (55 lines). DEBT-053.
- **fleet.go split** (`internal/tui/panels`): Split 1,004-line `fleet.go` into `fleet_render.go` (344 lines), `fleet_filtering.go` (127 lines), and `fleet_status.go` (298 lines). Residual fleet.go retains types, model, and interactive state (264 lines). DEBT-045.
- **noctx consolidation** (`pkg/launchctl`): Extract `pkg/launchctl` package with context-aware `Load`/`Unload`/`Start`/`Stop`/`Kill`/`FindProcessByPort`/`KillPID` helpers wrapping `exec.CommandContext`. Eliminates 14 `//nolint:noctx` suppressions across `daemon_control.go`, `hud_control.go`, `cmd_sync_agent_tokens.go`, and `bridge/daemon.go`. DEBT-048.
- **mcp-godot tests** (`cmd/mcp-godot`): Add 21 unit tests covering `NewGodotClient`, `NewLogReader`, `ReadRecent`, `TailStream`, and `Close` idempotency. DEBT-049.
- **memory_export.go split** (`pkg/agentcontext`): Split 895-line `memory_export.go` into `memory_export_types.go` (122), `memory_export_import.go` (380). Residual retains exporter, Mem0 format, and helpers (402 lines). DEBT-055.
- **compaction_scheduler.go split** (`pkg/agentcontext`): Split 893-line `compaction_scheduler.go` into `compaction_strategy.go` (176), `compaction_execution.go` (408). Residual retains types, lifecycle, and scheduler loop (328 lines). DEBT-056.
- **app.go split** (`internal/tui`): Split 816-line `app.go` into `app_commands.go` (325), `app_run.go` (124). Residual retains Model struct, Init, Update, View (384 lines). DEBT-057.
- **coordination.go split** (`internal/hud/coordination`): Split 724-line `coordination.go` into `coordination_helpers.go` (185). Residual retains types and Build function (539 lines). DEBT-058.
- **callpipeline.go split** (`internal/daemon`): Split 1,055-line `callpipeline.go` into `callpipeline_stages.go` (parse/auth/policy/cache/route/build/execute), `callpipeline_routing.go` (connection/retry), `callpipeline_errors.go` (error builders/metrics/audit), and `callpipeline_timeout.go` (timeout resolution). DEBT-043.
- **daemon_toolcache.go split** (`internal/daemon`): Split 1,073-line `daemon_toolcache.go` into `daemon_tools_handlers.go` (MCP handlers), `daemon_tools_cache.go` (refresh logic), `daemon_resources.go` (resource handling), and `daemon_tools_fetch.go` (server I/O). DEBT-042.
- **qdrant client.go split** (`pkg/codebase/qdrant`): Split 1,052-line `client.go` into `collections.go` (lifecycle), `search.go` (query/scroll), and `serialize.go` (filters/conversion). DEBT-044.
- **mcpscaffold migration** (`cmd/mcp-*`): Migrated 7 MCP servers (quality, codebase-memory, jobsearch, gitlab, devbox, itchio, crypto) to `pkg/mcpscaffold.NewServer()`, eliminating ~245 lines of duplicated initialization boilerplate. DEBT-047.
- **app.go route group split** (`internal/hud`): Split 1,445-line `app.go` into `app_routes_fleet.go` (agent fleet/sessions/tasks), `app_routes_observability.go` (pipeline/metrics/health), and `app_routes_operations.go` (config/sync/devbox/system). DEBT-027.
- **memory_hierarchy.go split** (`pkg/agentcontext`): Split 1,427-line `memory_hierarchy.go` into `memory_hierarchy_core.go`, `memory_hierarchy_recall.go`, `memory_hierarchy_promotion.go`, `memory_hierarchy_dedup.go`, and `memory_hierarchy_persist.go`. DEBT-028.
- **configs.go platform split** (`pkg/generator`): Split 1,580-line `configs.go` into per-platform files: `configs_targets.go`, `configs_formats.go`, `configs_hooks.go`, `configs_claude.go`, `configs_codex.go`, and `configs_gemini.go`. DEBT-026.
- **Deprecated agent-context tool migration** (`internal/hud/bridge`, `cmd/loom`, `.agents/workflows`): Migrated all callers of 5 deprecated tools (`agent_context_recall_enhanced`, `annotation_add/get`, `memory_recall`, `workflow_define`) to current equivalents; fixed fleet refresh storms with coalescing and background startup warmup. DEBT-035.
- **MCP server per-resource splits** (`cmd/mcp-google-workspace`, `cmd/mcp-terraform`): Extracted handler functions into per-resource modules (auth, calendar, docs, drive, gmail for google-workspace; tfc_request for terraform). DEBT-036.
- **proxy.go monolith split** (`cmd/loom`): Split 1,686-line `proxy.go` into `proxy_transport.go` (RPC/stdio), `proxy_session.go` (lease/epoch), `proxy_heartbeat.go` (HUD heartbeat), and `proxy_handlers.go` (MCP dispatch). DEBT-023.
- **workflow.go monolith split** (`pkg/agentcontext`): Split 1,641-line `workflow.go` into `workflow_engine.go` (public API), `workflow_executor.go` (DAG execution), `workflow_expr.go` (condition eval), and `workflow_persist.go` (Qdrant persistence). DEBT-024.
- **sync/ops.go platform split** (`pkg/sync`): Split 1,609-line `ops.go` into per-platform files: `ops_gemini.go`, `ops_claude.go`, `ops_codex.go`, and `ops_helpers.go`. DEBT-025.
- **HUD domain test coverage** (`internal/hud`): Added tests for 4 previously untested domain packages: autofix, alerting, handoff, and memory. DEBT-033.
- **CI lint now blocking** (`.gitlab-ci.yml`): Removed `allow_failure: true` from golangci-lint job; lint failures now block merges. All 37 first-party `//nolint` suppressions annotated with justification comments.
- **CI coverage threshold raised** (`.gitlab-ci.yml`): Coverage threshold increased from 24% to 35% (actual coverage is ~40.7%).
- **Roadmap reconciliation files archived** (`docs/`): 20 older reconciliation files moved to `.loom/archive/roadmap-reconciliations/`.
- **daemon.go monolith split** (`internal/daemon`): Split 1,098-line `daemon.go` into `daemon_new.go` (config/constructor), `daemon_lifecycle.go` (start/stop/serve), `daemon_transport.go` (dial/connect), and `daemon_reload.go` (watcher/signal). DEBT-041.
- **generator.go monolith split** (`pkg/skills`): Split 1,342-line `generator.go` into `generator_validation.go`, `generator_codex_gemini.go`, `generator_claude_kilocode.go`, and `generator_instructions.go`. DEBT-038.
- **knowledge_graph.go monolith split** (`pkg/agentcontext`): Split 1,250-line `knowledge_graph.go` into `knowledge_graph_entities.go`, `knowledge_graph_relations.go`, `knowledge_graph_query.go`, `knowledge_graph_reasoning.go`, and `knowledge_graph_persistence.go`. DEBT-039.
- **qdrant.go monolith split** (`pkg/agentcontext`): Split 1,229-line `qdrant.go` into `qdrant_collection.go`, `qdrant_operations.go`, and `qdrant_convert.go`. DEBT-040.
- **domain_adapters.go decomposition** (`internal/hud`): Split 825-line, 117-function `domain_adapters.go` into `domain_adapters_fleet.go`, `domain_adapters_workflow.go`, `domain_adapters_mobile.go`, and `domain_adapters_ops.go` by domain group. DEBT-046.
- **Daemon restart tracing surfaces** (`internal/daemon`): health-monitor auto-restart attempts now emit dedicated restart spans, and `loom/otel-status` reports `server_restart_lifecycle` as an explicit runtime trace surface instead of a generic runtime placeholder.
- **Devbox K8s workspace planning** (`internal/devbox/backend`): runtime and build pods now share a single workspace-mode planner for `tar-pipe`, `git-clone`, and PVC-backed execution, with added build-side coverage for tar-pipe and default NFS behavior.
- **CI pipeline parallelism** (`.gitlab-ci.yml`): `build:binaries`, `test:unit`, `test:race`, and `test:enterprise-smoke` now use `needs: [prepare:go-cache]` to fan out in parallel after prepare instead of waiting for full stage gates (~3-8 min saved).
- **MCP build parallelism** (`.gitlab-ci.yml`): `MCP_BUILD_JOBS` default increased from 2 to 4 to match CPU limit.
- **Dockerfile optimization** (`Dockerfile`): Combined 3 binary build `RUN` layers into 1 with parallel MCP server builds (`xargs -P4`) and `-trimpath`.
- **BuildKit DRY** (`scripts/ci/buildkit-build.sh`): Extracted duplicated registry-failover logic from 2 image jobs into shared helper.
- **Docker build context** (`.dockerignore`): Excluded `.go*`, `.tmp`, `.loom`, `.agents`, `apps/`, `tools/`, `docs/`, `*.md` to reduce transfer size.
- **Adaptive session heartbeat** (`cmd/loom/proxy.go`): Proxy heartbeat switches between 5s (active) and 30s (idle) intervals based on recent RPC activity, configurable via `proxy.heartbeat_interval_ms` and `proxy.idle_heartbeat_interval_ms`.
- **Daemon drain gate** (`internal/daemon`): Daemon-level drain mode rejects new `loom/call` requests with a retryable `DAEMON_DRAINING` error; `loom/status` and `loom/session/status` report actual drain state.
- **RBAC** (`internal/daemon/rbac.go`): Role-based tool access control with glob patterns, per-agent bindings, and deny-wins evaluation.
- **Audit Trail** (`internal/daemon/audit.go`): Append-only JSONL logging of all tool calls with agent, server, tool, duration, and status fields.
- **Cost Tracking** (`internal/daemon/cost.go`): Usage attribution by agent/server/tool with aggregation buckets and snapshot API endpoint.
- **OAuth 2.1** (`internal/daemon/oauth.go`): Built-in authorization server with PKCE (S256), dynamic client registration (RFC 7591), AS metadata (RFC 8414), and token revocation (RFC 7009).
- `docs/ENTERPRISE_SECURITY.md`: Configuration guide for all enterprise security features.
- Agent lifecycle hooks bridge with nudge system and HUD API endpoints.
- **Configurable session-start recall strategies** (`cmd/loom`): Agents can select recall behavior (full, summary, or skip) at session init via `--recall-strategy`.
- **Async summarize on session end**: Agent hooks now perform background summarization when a session ends, avoiding blocking the host CLI.
- **Hub failover** (`internal/daemon/daemon.go`): `prefer-hub` routing with automatic local fallback and 30s backoff when hub is unavailable.
- **`mcp-hub-wrapper`** (`cmd/mcp-hub-wrapper`): Hub binary resolution from env, workspace, `~/.local/bin`, and PATH with multi-source discovery.
- **`QdrantRegistry`** (`internal/agentcontext`): Consolidated registry replacing 14 individual Qdrant client fields with a single shared registry.
- **Workflow engine enhancements**: RLM recursive context strategies, `map_reduce` step type, conditional gating, and deep-copy in clone.
- MCP server smoke tests for 10 additional servers (youtube, itchio, crypto, release, morph-fast-apply, alertmanager, minio, substack, qdrant, morph-embeddings).
- Edge-case tests for daemon enterprise features (RBAC, audit, cost, OAuth) and agentcontext (workflows, memory hierarchy, service).
- `mcp-devbox`: project-aware sandbox executor with Docker/K8s backends.
- New devbox tools: `devbox_exec_async`, `devbox_exec_poll`, `devbox_metrics`, `devbox_summary`, `devbox_read_file`, `devbox_write_file`.
- **`mcp-devbox` tar-pipe workspace sync** (`cmd/mcp-devbox`): Direct tar-pipe streaming via SPDY exec replaces NFS for local K8s sandbox pods; auto-discovers sibling deps from `go.mod` replace directives.
- **`mcp-devbox` git-clone initContainers** (`cmd/mcp-devbox`): Optional `DEVBOX_K8S_GIT_BASE_URL` mode populates sandbox pods from a shallow git clone into local emptyDir storage, eliminating NFS from the critical path.
- **Devbox K8s reliability** (`cmd/mcp-devbox`): Parallel builds (no shared cache mutex), per-agent pod isolation, warm pool (`DEVBOX_WARM_PROJECTS`), NFS cache flush (`DEVBOX_NFS_FLUSH`), and base image pipeline (python-3.12, node-20).
- HUD sandbox API/panel integration backed by `devbox_summary` for live sandbox visibility.
- **iOS companion**: Sandbox start flow in ops agents tab, expanded ops workflows, push diagnostics.
- **Mobile HUD API**: Sandbox/devbox tab exposing `devbox_summary`, `devbox_build`, and `devbox_stop`; control-plane read APIs with auto-sync gateway token.
- **HUD enterprise dashboard** (`web/`): Cost dashboard (CostMonitor, SSE `hud.cost`, OverviewPanel KPI tile), RBAC visibility (denied-calls ring buffer, ServersPanel RBAC card), OTel status (traced/total coverage, ServersPanel Observability card).
- **Skills generation improvements** (`cmd/loom`): Priority-based composite instruction assembly (#59), `\${VAR}` escaping in instructions (#57), auto-update registry date (#56), and validation of script/reference/asset existence (#55).
- **`always_allow` safety validation** (`pkg/skills`): Skill generation now rejects `always_allow` entries that point at write-capable scripts unless those scripts advertise a `--dry-run` default (#58).
- Atomic local upgrade workflow via `make dev-upgrade`, `scripts/dev/upgrade_local.sh`, and `scripts/install_atomic.sh`.
- `docs/DEV_BUILD_LIFECYCLE.md` for agent-safe local upgrade and rollback procedures.
- `docs/FLEXINFER_SITE_INTEGRATION.md` runbook describing how Loom Core docs are synced/published through `services/flexinfer-site` to `flexinfer.ai`.
- CI guardrails for docs/command drift:
  - docs drift check (`scripts/ci/check_docs_guardrails.sh`)
  - flexinfer-site integration check (`scripts/ci/check_flexinfer_site_integration.sh`)
  - CLI help smoke checks (`go run ./cmd/loom --help`, `go run ./cmd/loom proxy --help`)
- `loom completion bash|zsh|fish|powershell` command for shell completion generation.
- `Long` and `Example` fields for `status`, `start`, `stop`, `servers`, and `check` commands.
- `LOOM_SOCKET` env var support for `--socket` flag.
- Error handling guardrail (`scripts/ci/check_error_handling.sh`) prevents `return nil, err` count from increasing in MCP handler files.
- Migration tracker in `docs/ERROR_HANDLING.md` covering all 40 MCP servers.
- OTel tracing wrappers now cover all `cmd/mcp-*/main.go` servers via `pkg/mcpotel`.
- `loom hud install|start|stop|status|uninstall` launchd management commands, with `~/.config/loom/hud.env` bootstrap for HUD-specific secrets/env.
- Sync/hook generation updates for worktree-first workflows:
  - Session start hooks now include a non-blocking main-branch worktree nudge.
  - Antigravity profile sync now includes `settings.json` (hooks stub) alongside `mcp.json`.
- Test suites for MCP servers: `mcp-docker` (60% coverage), `mcp-cloudflare` (70%), `mcp-grafana` (73%), `mcp-helm` (22%), `mcp-redis` (22%).
- TUI test foundation: pure function tests for panels, widgets, helpers, layout, and bubbletea Update routing.
- MCP server test coverage Batch 1: `mcp-git`, `mcp-memory`, `mcp-sequentialthinking`.
- **Autogenerated changelog** (`.changelog-ai.yaml`): `py-changelog-ai` integration with `make changelog` target for Keep-a-Changelog output from conventional commits.

### Changed
- CI now seeds Go dependency cache in a dedicated `prepare` stage (`pull-push`) using `.go/pkg/mod/cache/download` (runner-size-safe) plus `.go-build`; lint jobs skip eager `go mod download` prewarm and use shallow clones for `../../libs/*` replacements.
- CI static-analysis jobs now target first-party packages (`./cmd`, `./internal`, `./pkg`) to avoid scanning `.go/pkg/mod`; `golangci-lint` timeout increased to 10m with explicit runner memory overrides for lint (`4Gi` request / `8Gi` limit), `gosec` runs with reduced concurrency, `govulncheck` runs in module-scan mode (invoked from `cmd/loom`) with `GOMEMLIMIT=6GiB`, heavyweight security scans are scoped to `main`/tag/scheduled pipelines to keep feature-branch CI fast, and unit tests now auto-skip `pkg/agentcontext` when `fi_accel` native libs are absent on the runner.
- HUD M3/M4 completion: BulkToolbar in PresencePanel Claims tab, lazy-loaded LifecyclePanel, color-blind safe StatusDot in OverlayShell, row pagination via `maxRows` in Fleet/Tasks DataTables, GitLab issue-reference linking in task titles.
- Devbox now mounts workspace root and sets project-relative container workdirs for better monorepo support.
- HUD web/TUI surfaces were refined (polish, interactions, and Ghostty palette alignment).
- `loom agent` lifecycle commands now prefer HUD API but automatically fall back to daemon socket `tools/call` when HUD is unavailable.
- `docs/STREAMABLE_HTTP.md` expanded with OAuth 2.1 auth type documentation.
- `loom status` now appends HUD launchd/health status, including cache backend when available.
- `make dev-upgrade` now attempts a HUD restart (launchd-first, process fallback) before proxy initialize smoke tests.
- `pkg/mcplog` now supports `MCP_LOG_FORMAT=json` with automatic `trace_id` / `span_id` correlation fields when logs are emitted from traced contexts.
- CI image builds migrated from Kaniko to remote BuildKit for faster and more reliable container image production.
- Daemon keeps `neo4j` and `substack` MCP servers alive in degraded mode instead of stopping them on health-check failure.
- Daemon retries local tool calls once after a transport-closed send failure before returning an error.

### Fixed
- Session lifecycle and status reporting now reuse persisted active sessions, dedupe duplicate active identities without undercounting anonymous legacy sessions, surface HUD pipeline summaries in `loom status`, and keep Claude hook subprocesses grouped under the same CLI session identity.
- Loom Companion iOS widgets and Live Activities now share App Group data correctly, persist dashboard snapshots before timeline reloads, and register the workflow Live Activity view from the widget extension target.
- `mcp-alertmanager` now detects HTML error responses and returns a clear error message instead of an unmarshal panic.
- TasksPanel row shifting caused by `display: flex` on `<td>` elements in the blocked-by column.
- `mcp-devbox` lifecycle hardening around sandbox state, async execution, and backend reliability.
- Hook-only clients (for example Codex `notify`) can now bootstrap agent session/presence via `loom agent heartbeat --ensure-session`, preventing repeated heartbeat failures when no explicit session-start hook exists.
- `loom sync <profile|all> --regen` now prefers workspace-local registry discovery across ancestor directories (including `platform/gitops/mcp/context/registry.yaml`), avoiding stale regeneration from `~/.config/loom/registry.yaml` when run from `services/*` repos.
- TUI log bleed-through: stderr redirected during bubbletea rendering to prevent daemon reconnection messages from overlaying the fleet panel.
- TUI Tasks panel broken borders: column width calculation now accounts for border+padding overhead.
- TUI Fleet panel column misalignment: replaced rune-width truncation with ANSI-aware truncation from `charmbracelet/x/ansi`.
- `loom-agent`: Support remote HUD with `LOOM_HUD_URL` env and `config.yaml hud.url` for internal ingress access; auto-inject CF Access headers.
- `mcp-k8s-ops`: Place kubectl subcommand before flags in all handlers for kubectl v1.31+ compatibility.
- `mcp-devbox`: Update sync script NFS target to `nfs-media-v2` VM.
- Mobile gateway preflight/bootstrap resilient to CF Access gating.
- Hub container image now includes `kubectl` and supports in-cluster ServiceAccount auth when no kubeconfig is present.
- CI: Fetch sources for `lint:mcp-godot` job; handle unset `CI_COMMIT_TAG` in BuildKit jobs; quote `buildctl` image name list for multi-tag pushes.

## [0.9.7] - 2026-02-10

### Fixed
- `loomd` now best-effort raises `RLIMIT_NOFILE` on startup (configurable via `LOOM_NOFILE`) to avoid `EMFILE` / "Too many open files" when spawning many MCP servers from launchd/GUI contexts (which often start with a soft limit of 256).
- Tool cache refresh is now fetched with bounded concurrency to reduce burst FD usage during startup.

## [0.9.6] - 2026-02-10

### Fixed
- GitHub Actions cross-compiles for `darwin` with `CGO_ENABLED=0` again by providing `darwin && !cgo` stubs for the native overlay window package.
- macOS hotkey/overlay sources are now explicitly tagged `darwin && cgo` to avoid accidental inclusion in non-CGO builds.

### Changed
- Refreshed branding banner asset.

## [0.9.5] - 2026-02-10

### Fixed
- GitHub Actions now checks out private dependency repos using `LOOM_DEPS_TOKEN` (HTTPS) to avoid intermittent SSH key parsing failures on runners.
- The release workflow now skips publishing a GitHub release when run via `workflow_dispatch` on a non-tag ref.

## [0.9.4] - 2026-02-10

### Fixed
- GitHub Actions attempted to check out private dependency repos using per-repo deploy keys stored as Actions secrets; superseded by the `LOOM_DEPS_TOKEN` approach in `0.9.5` due to runner key parsing failures.

## [0.9.3] - 2026-02-10

### Fixed
- GitHub Actions workflows now use a repo secret (`LOOM_DEPS_TOKEN`) so they can check out private dependency mirrors needed for `go.mod` replaces.

## [0.9.2] - 2026-02-10

### Fixed
- GitHub Actions release builds now vendor local workspace dependencies (`fi-mcp-kit`, `mcp-go`) by checking out their GitHub mirrors and rewiring `go.mod` `replace` paths during CI.

## [0.9.1] - 2026-02-10

### Added
- API stability documentation in `docs/API_STABILITY.md`
- **OpenTelemetry tracing** across `mcp-git`, `mcp-prometheus`, and `mcp-gitlab` via `pkg/mcpotel` (noop when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset)
- Test suites for `pkg/mcpotel` (tracer init, middleware, span attributes)
- Metrics unit tests for `pkg/agentcontext` (counters, histogram, Prometheus format)
- Metrics wiring in `mcp-agent-context`: sessions, recall, embeddings, graph, workflows, and memory tier counters now increment; `agent_context_stats` includes metrics snapshot

### Fixed
- ~20 silent error drops (`_ = err`) replaced with `logger.Warn()` across agent-context subsystems (compaction, workflow rollback, file claims, codebase sync, memory export, worktree prune)

## [0.9.0] - 2026-02-01

### Added
- **Agent Context MCP Server**: Full session, context, task, and workflow management
- **Codebase Memory**: Code indexing with semantic search via Qdrant
- **50 MCP Servers**: Complete suite of integrations
- **Profile System**: Tool filtering with dev, k8s-ops, research, and full profiles
- **Response Cache**: Intelligent caching for read-only tools
- **Health Monitoring**: Server health tracking with metrics
- **Tunnel Manager**: SSH tunnel support for remote servers

### Changed
- Improved daemon startup with config file support
- Enhanced tool descriptions with usage hints
- Better error messages across all servers

## [0.8.0] - 2026-01-31

### Added
- **Hub Fallback**: Automatic fallback to MCP hub when local servers unavailable
- **Manifest Cache**: Persistent tool caching for faster startup
- **Metrics Collection**: Prometheus-compatible metrics endpoint

### Changed
- Daemon now supports graceful shutdown
- Improved connection pooling for stdio transports

### Fixed
- Memory leak in long-running stdio connections
- Race condition in tool cache refresh

## [0.7.0] - 2026-01-30

### Added
- **Multi-platform sync**: VS Code, Antigravity, Gemini, Claude, Codex, Kilocode
- **Platform detection**: Auto-detect VS Code vs Antigravity
- **Sync command**: `loom sync <platform>` for config generation

### Changed
- Registry format now supports platform-specific configurations
- Improved secret resolution with keychain fallback

## [0.6.0] - 2026-01-28

### Added
- **Skills Registry**: Centralized skill definitions with scripts and resources
- **Secrets Management**: Keychain integration for credential storage
- **Registry Validation**: YAML schema validation for registry files

## [0.5.0] - 2026-01-25

### Added
- **Daemon Mode**: Background service for MCP aggregation
- **Tool Routing**: Smart routing between local and hub servers
- **Connection Pool**: Efficient connection management for servers

## [0.4.0] - 2026-01-20

### Added
- Initial MCP server implementations
- Registry-based configuration
- Basic CLI commands

---

[Unreleased]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.7...HEAD
[0.9.7]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.6...v0.9.7
[0.9.6]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.5...v0.9.6
[0.9.5]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.4...v0.9.5
[0.9.4]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.3...v0.9.4
[0.9.3]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.2...v0.9.3
[0.9.2]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.1...v0.9.2
[0.9.1]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.0...v0.9.1
[0.9.0]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.8.0...v0.9.0
[0.8.0]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.7.0...v0.8.0
[0.7.0]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.6.0...v0.7.0
[0.6.0]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.5.0...v0.6.0
[0.5.0]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.4.0...v0.5.0
[0.4.0]: https://gitlab.flexinfer.ai/services/loom-core/-/releases/v0.4.0
