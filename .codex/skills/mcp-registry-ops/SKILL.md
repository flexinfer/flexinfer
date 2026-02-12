---
name: mcp-registry-ops
description: "Edit and operate MCP registry.yaml (especially platform/gitops/mcp/context/registry.yaml): validate registry changes, manage per-server allowlists (always_allow), and generate MCP client configs and hub manifests via loom-core's loom CLI. Use when updating MCP servers, hub deployments, gateway registry, or Codex/VSCode/Kilo configs."
---

# MCP Registry Ops

Maintain the workspace's canonical MCP server registry and regenerate downstream artifacts (client configs + hub manifests) safely and repeatably.

## Quick Start (Workspace Layout)

- Find the registry:
  - `python ${CODEX_HOME:-$HOME/.codex}/skills/mcp-registry-ops/scripts/registry_discover.py --root .`
- Generate client configs (Go generator via `loom`):
  - `bash ${CODEX_HOME:-$HOME/.codex}/skills/mcp-registry-ops/scripts/loom_generate_configs.sh . --target all --output-dir generated/mcp --hub-mode`
- Generate hub manifests (when changing hub deployments):
  - `bash ${CODEX_HOME:-$HOME/.codex}/skills/mcp-registry-ops/scripts/loom_generate_manifests.sh . --output-dir platform/gitops/k3s/mcp-hub/servers`

## Core Workflow

### 1) Edit `registry.yaml`

- Prefer editing `common` first.
- Use `targets.<platform>` only for platform-specific overrides.
- Keep `always_allow` (auto-approve) lists minimal and intentional.

### 2) Update an Allowlist (always_allow)

- Edit `common.always_allow`:
  - `python ${CODEX_HOME:-$HOME/.codex}/skills/mcp-registry-ops/scripts/registry_allowlist_edit.py --registry platform/gitops/mcp/context/registry.yaml --server gitlab --scope common --add create_issue`
- Edit `targets.<target>.always_allow`:
  - `python ${CODEX_HOME:-$HOME/.codex}/skills/mcp-registry-ops/scripts/registry_allowlist_edit.py --registry platform/gitops/mcp/context/registry.yaml --server gitlab --scope target:codex --set verify_token list_projects`

Use `--dry-run` to preview and `--sort` to normalize.

### 3) Generate Configs / Manifests

- Generate client configs:
  - `services/loom-core/bin/loom generate configs --registry platform/gitops/mcp/context/registry.yaml --target all --output-dir generated/mcp --hub-mode`
- Generate hub manifests:
  - `services/loom-core/bin/loom generate manifests --registry platform/gitops/mcp/context/registry.yaml --output-dir platform/gitops/k3s/mcp-hub/servers`

If `services/loom-core/bin/loom` doesn't exist, build it:
- `cd services/loom-core && go build -o bin/loom ./cmd/loom`

### 4) Validate Generated Configs (Secrets Hygiene)

- `services/loom-core/bin/loom validate configs --dir generated/mcp`

## References / Templates

- Conventions: `references/workspace-conventions.md`
- Change checklist: `assets/templates/registry-change-checklist.md`

## Bundled Resources

- `scripts/registry_discover.py`
- `scripts/registry_allowlist_edit.py`
- `scripts/loom_generate_configs.sh`
- `scripts/loom_generate_manifests.sh`
- `references/workspace-conventions.md`
- `assets/templates/registry-change-checklist.md`
