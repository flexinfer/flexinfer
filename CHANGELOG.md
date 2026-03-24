# Changelog

All notable changes to loom-core will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Pipeline-aware agent lifecycle** (`internal/hud/bridge`): Explicit `PipelineRef` foreign keys on Session and Task, replacing branch-name heuristics; `WorkflowID` on Task for workflow-step tracing; `pipeline_event` context entries; auto-detection in WorkStart.
- **Mobile session activity endpoint** (`internal/hud/domain/mobile`): `GET /api/sessions/{id}/activity` returns unified tasks + pipelines with correlation type.
- **Mobile pipelines endpoint** (`internal/hud/domain/mobile`): `GET /api/mobile/v1/pipelines` with cold-start refresh, fallback to recent pipelines, and agent-branch correlation.
- **Pipeline monitor** (`internal/hud/monitor`): `PipelineMonitor` with adaptive 10s/60s polling intervals and 10s detail cache TTL.
- **Task projection** (`internal/hud/domain/mobile`): Synthesize tasks from agent presence `current_task` field into the unified task feed with deterministic SHA1-based IDs.
- **Workflow deprecation** (`internal/hud/domain/mobile`): Migration flags on approval surface to prepare transition away from workflow endpoints.

### Changed
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
