# Project Roadmap

> Last Updated: January 2026

## Current Status

Loom Core provides the high-performance Go backend components for the Loom ecosystem, including the CLI, daemon, and specialized MCP servers.

### Implemented Features
- ✅ **CLI**: `loom` command for managing context and connections.
- ✅ **MCP Servers**:
    - `mcp-gitlab`: GitLab integration.
    - `mcp-k8s`: Kubernetes resource access.
    - `mcp-grafana`: Dashboard search and querying.
    - `mcp-loki`: Log querying.
    - `mcp-minio`: S3/MinIO file access.
- ✅ **Shared Utils**: `pkg/mcp` for building standard MCP servers in Go.

## Upcoming Work

### Phase 1: Server Expansion
- [ ] **`mcp-jira`**: Jira issue tracking integration.
- [ ] **`mcp-confluence`**: Knowledge base search.
- [ ] **`mcp-postgres`**: Database schema and query inspector.

### Phase 2: Core Infrastructure
- [ ] **Daemon Mode**: Background process to manage persistent connections.
- [ ] **Secure Tunneling**: SSH/WireGuard wrapping for remote server access.
- [ ] **Context Caching**: Local caching of frequently accessed MCP resources.

## References

| Document | Purpose |
|----------|---------|
| [README.md](README.md) | Project overview |
| [AGENTS.md](AGENTS.md) | Agent guidance |