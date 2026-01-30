# Project Roadmap

> Last Updated: January 30, 2026

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
- [x] **`mcp-confluence`**: Knowledge base search.
- [x] **`mcp-postgres`**: Database schema and query inspector.
- [x] **`mcp-github-actions`**: GitHub Actions workflow management (trigger, cancel, rerun, logs).
- [x] **`mcp-slack`**: Slack integration (search, channels, messages, reactions).

### Phase 2: Core Infrastructure

- [x] **Concurrency**: Parallel tool execution with configurable backpressure.
- [x] **Secure Tunneling**: SSH tunneling for remote MCP server access.
  - `pkg/tunnel`: SSHTunnel, SSHTransport for secure remote connections.
  - Registry support: `ssh:` config block in TargetSpec for host, user, key, etc.
  - Process manager integration: Auto-detects SSH config and connects via tunnel.
  - Auth methods: SSH agent, key files, known_hosts verification.
- [x] **Context Caching**: Persistent tool manifest caching for instant startup.

### Phase 3: Additional Integrations

Planned MCP servers for broader ecosystem coverage:

- [x] **`mcp-linear`**: Linear issue tracking (alternative to Jira for modern teams).
- [x] **`mcp-notion`**: Notion pages and databases.
- [x] **`mcp-sentry`**: Error tracking and issue management.
- [x] **`mcp-pagerduty`**: Incident management and on-call scheduling.
- [x] **`mcp-elasticsearch`**: Full-text search and log analysis.
- [x] **`mcp-mongodb`**: MongoDB document queries.
- [x] **`mcp-argocd`**: ArgoCD application management (GitOps for K8s).
- [x] **`mcp-terraform`**: Terraform Cloud/Enterprise state and plan management.
- [x] **`mcp-vault`**: HashiCorp Vault secrets access.
- [x] **`mcp-aws`**: AWS resource access (S3, Lambda, EC2).
- [x] **`mcp-gcp`**: Google Cloud Platform (Storage, Compute, Functions).

## References

| Document               | Purpose          |
| ---------------------- | ---------------- |
| [README.md](README.md) | Project overview |
| [AGENTS.md](AGENTS.md) | Agent guidance   |
