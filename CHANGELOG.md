# Changelog

All notable changes to loom-core will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.9.4] - 2026-02-10

### Fixed
- GitHub Actions can now check out private dependency repos using per-repo deploy keys stored as Actions secrets.

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

[Unreleased]: https://gitlab.flexinfer.ai/services/loom-core/-/compare/v0.9.4...HEAD
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
