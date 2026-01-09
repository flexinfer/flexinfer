# Project Roadmap

> Last Updated: January 2026

## Current Status

Loom Core provides the high-performance Go backend components for the Loom ecosystem, including the CLI, daemon, and specialized MCP servers.

### Implemented Features

- ✅ **CLI**: `loom` command for managing context and connections.
- ✅ **Daemon Mode**: Background process (`loomd`) managing persistent connections and tool aggregation.
- ✅ **Smart Routing**: Support for prefix-less tool calls and argument-based routing.
- ✅ **Hub Bridging**: Automatic discovery and transparent access to remote MCP Hub tools.
- ✅ **MCP Servers**:
  - `mcp-gitlab`: GitLab integration.
  - `mcp-k8s`: Kubernetes resource access.
  - `mcp-grafana`: Dashboard search and querying.
  - `mcp-loki`: Log querying.
  - `mcp-minio`: S3/MinIO file access.
- ✅ **Shared Utils**: Delegation to `fi-mcp-kit` for enterprise orchestration.

## Upcoming Work

### Phase 1: Server Expansion

- [x] **`mcp-jira`**: Jira issue tracking integration.
- [ ] **`mcp-confluence`**: Knowledge base search.
- [ ] **`mcp-postgres`**: Database schema and query inspector.

### Phase 2: Core Infrastructure

- [x] **Concurrency**: Parallel tool execution with configurable backpressure.
- [ ] **Secure Tunneling**: SSH/WireGuard wrapping for remote server access.
- [x] **Context Caching**: Persistent tool manifest caching for instant startup.

## References

| Document               | Purpose          |
| ---------------------- | ---------------- |
| [README.md](README.md) | Project overview |
| [AGENTS.md](AGENTS.md) | Agent guidance   |