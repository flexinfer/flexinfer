# API Stability Policy

This document describes the stability guarantees for loom-core APIs.

## Versioning

Loom-core follows [Semantic Versioning 2.0.0](https://semver.org/):

- **MAJOR** (1.x.x): Breaking changes to stable APIs
- **MINOR** (x.1.x): New features, backwards-compatible changes
- **PATCH** (x.x.1): Bug fixes, documentation updates

## API Categories

### Stable APIs (`pkg/`)

These packages are intended for external use and have stability guarantees:

| Package | Description | Stability |
|---------|-------------|-----------|
| `pkg/agentcontext` | Agent context and session management | Stable |
| `pkg/codebase` | Codebase indexing and search | Stable |
| `pkg/context` | Context configuration | Stable |
| `pkg/generator` | Config file generation | Stable |
| `pkg/httpclient` | HTTP client utilities | Stable |
| `pkg/lifecycle` | Process lifecycle management | Stable |
| `pkg/mcperror` | MCP error types | Stable |
| `pkg/pathsec` | Path security validation | Stable |
| `pkg/poll` | Polling utilities | Stable |
| `pkg/profiles` | Server profile management | Stable |
| `pkg/registry` | Registry loading and parsing | Stable |
| `pkg/secrets` | Secret management | Stable |
| `pkg/skills` | Skills registry | Stable |
| `pkg/sync` | Configuration sync | Stable |
| `pkg/testutil` | Test utilities | Stable |
| `pkg/tunnel` | SSH tunnel management | Stable |
| `pkg/validate` | Input validation | Stable |

**Stability Guarantees:**

1. No breaking changes without major version bump
2. Deprecated APIs marked with `// Deprecated:` comments
3. Minimum 1 minor version deprecation period before removal
4. All changes documented in CHANGELOG.md

### Internal APIs (`internal/`)

These packages are for internal use only and may change without notice:

| Package | Description |
|---------|-------------|
| `internal/daemon` | Daemon orchestrator |
| `internal/integration` | Integration tests |
| `internal/pool` | Connection pool |
| `internal/process` | Process management |
| `internal/router` | Request routing |

**No Stability Guarantees:**

- May change between any versions
- Not intended for external import
- Go compiler enforces this via import restrictions

### MCP Servers (`cmd/mcp-*`)

MCP servers expose tools via the Model Context Protocol:

| Server | Tools | Stability |
|--------|-------|-----------|
| `mcp-agent-context` | Session/context management | Stable |
| `mcp-codebase-memory` | Code indexing and search | Stable |
| `mcp-git` | Git operations | Stable |
| `mcp-github` | GitHub API | Stable |
| `mcp-gitlab` | GitLab API | Stable |
| `mcp-k8s` | Kubernetes operations | Stable |
| All others | Various integrations | Stable |

**Tool Stability:**

1. Tool names are stable (no renaming)
2. Required parameters are stable
3. New optional parameters may be added
4. Return schemas are stable (fields may be added)

## Breaking Change Process

When a breaking change is necessary:

1. **Announce** in CHANGELOG.md under "Breaking Changes"
2. **Deprecate** the old API with `// Deprecated:` comment
3. **Document** migration path in the deprecation notice
4. **Wait** at least one minor version before removal
5. **Remove** in next major version

### Example Deprecation

```go
// Deprecated: Use NewClientWithConfig instead.
// This function will be removed in v2.0.0.
// Migration: Replace NewClient(url) with NewClientWithConfig(Config{URL: url})
func NewClient(url string) *Client {
    return NewClientWithConfig(Config{URL: url})
}
```

## CLI Stability

The `loom` CLI commands are stable:

| Command | Stability |
|---------|-----------|
| `loom daemon start/stop/restart/status` | Stable |
| `loom servers` | Stable |
| `loom tools list/call` | Stable |
| `loom sync <platform>` | Stable |
| `loom profiles` | Stable |
| `loom secrets` | Stable |

**CLI Guarantees:**

1. Command names are stable
2. Required flags are stable
3. New optional flags may be added
4. Output format may change (use `--json` for stable output)
5. Exit codes: 0=success, 1=error, 2=usage error

## Configuration File Stability

Configuration files have backwards compatibility:

| File | Format | Stability |
|------|--------|-----------|
| `~/.config/loom/config.yaml` | YAML | Stable |
| `registry.yaml` | YAML | Stable |
| `skills-registry.yaml` | YAML | Stable |
| `.vscode/mcp.json` | JSON | Stable (VS Code spec) |

**Config Guarantees:**

1. New fields may be added (ignored by older versions)
2. Field types don't change
3. Required fields documented in schema
4. Defaults documented in code/docs

## Protocol Stability

### MCP Protocol

Loom implements [Model Context Protocol](https://modelcontextprotocol.io/):

- Protocol version: `2024-11-05`
- Stable transport: stdio, WebSocket
- Tool schemas follow JSON Schema

### Hub WebSocket Protocol

Hub communication uses JSON-RPC 2.0 over WebSocket:

```
wss://mcp.flexinfer.ai/ws?profile=<profile>
```

Messages follow MCP message format.

## Testing Against Stability

To verify your code against loom-core APIs:

```bash
# Run all tests
go test ./...

# Check for deprecated usage
go vet ./...

# Integration tests (requires services)
LOOM_RUN_MCP_SMOKE=1 go test ./internal/integration/...
```

## Reporting Compatibility Issues

If you find a breaking change that wasn't documented:

1. Check CHANGELOG.md for known changes
2. Search existing issues
3. File an issue with:
   - Version before/after
   - Code that broke
   - Expected vs actual behavior

## Version History

| Version | Date | Notes |
|---------|------|-------|
| v1.0.0 | TBD | Initial stable release |
| v0.9.0 | 2026-02 | Pre-release, feature complete |
| v0.8.0 | 2026-01 | Error recovery, offline mode |
| v0.7.0 | 2026-01 | Multi-platform support |
