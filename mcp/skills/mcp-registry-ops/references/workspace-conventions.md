# MCP Registry Ops (Workspace Conventions)

## Canonical Registry

- Primary: `mcp/context/registry.yaml`

## Generators

The workspace migrated Python generators to the Go `loom` CLI:

- Build/run: `services/loom-core/bin/loom`
- Generator package: `services/loom-core/pkg/generator/`

## Related Repos

- Gateway service: `services/mcp-gateway/` (registry-driven; supports profiles)
- Registry tooling: `libs/fi-mcp-kit/` (registry validation + generators; `fi-mcp` CLI)
