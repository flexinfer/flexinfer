# Workspace Snapshot

- Generated: 2026-02-17T10:04:46-05:00
- Root: `/Users/cblevins/workspace/services/loom-core`
- Git toplevel: `/Users/cblevins/workspace/services/loom-core`
- Platform: `macOS-26.3-arm64-arm-64bit`
- Python: `3.12.11`

## Git
```
## main...origin/main
 M ROADMAP.md
 M docs/USER_GUIDE.md
 M internal/hud/bridge/agent.go
 M internal/hud/bridge/agent_test.go
 M internal/hud/frontend/dist/assets/index-DLdeH65w.js
?? .loom/00-mcp-inventory.md
?? .loom/00-workspace-snapshot.md
?? .loom/10-research.md
?? .loom/11-research-daemon-proxy.md
?? .loom/12-research-market-trends-2026-02.md
?? .loom/13-research-agentic-workflows-openclaw.md
?? .loom/20-product-spec.md
?? .loom/40-decisions.md
?? .opencode/
?? .zed/
?? docs/roadmap-reconciliation-2026-02-12.md
?? docs/roadmap-reconciliation-2026-02-14.md
?? docs/roadmap-reconciliation-2026-02-15.md
?? docs/roadmap-reconciliation-2026-02-16.md
?? docs/roadmap-reconciliation-2026-02-17.md
```

### Remotes
```
github	https://github.com/crb2nu/loom-core.git (fetch)
github	https://github.com/crb2nu/loom-core.git (push)
gitlab-vm	https://gitlab.flexinfer.ai/services/loom-core.git (fetch)
gitlab-vm	https://gitlab.flexinfer.ai/services/loom-core.git (push)
origin	https://gitlab.flexinfer.ai/services/loom-core.git (fetch)
origin	https://gitlab.flexinfer.ai/services/loom-core.git (push)
```

### HEAD
```
f1a9ca7 hud: add runtime nudge policy controls and sectioned context accounting
```

## Top-Level Layout

### Directories
- `.agents/`
- `.antigravity/`
- `.claude/`
- `.codex/`
- `.gemini/`
- `.git/`
- `.github/`
- `.go/`
- `.kilocode/`
- `.loom/`
- `.opencode/`
- `.vscode/`
- `.vscode-mcp/`
- `.zed/`
- `assets/`
- `bin/`
- `claude_desktop_config/`
- `cmd/`
- `contrib/`
- `docs/`
- `generated/`
- `internal/`
- `launchd/`
- `pkg/`
- `scripts/`

### Files
- `.editorconfig`
- `.gitignore`
- `.gitlab-ci.yml`
- `.golangci.yml`
- `.mcp.json`
- `.pre-commit-config.yaml`
- `.secrets.baseline`
- `AGENTS.md`
- `CHANGELOG.md`
- `coverage-internal.out`
- `coverage.ci-sim.out`
- `coverage.internal-pkg.out`
- `coverage.tests-only.out`
- `Dockerfile`
- `Dockerfile.custom-server`
- `Dockerfile.custom-server.local`
- `Dockerfile.local`
- `go.mod`
- `go.sum`
- `loom`
- `loomd`
- `Makefile`
- `mcp-alertmanager`
- `mcp-argocd`
- `mcp-asus-router`
- `mcp-aws`
- `mcp-browserkit`
- `mcp-cloudflare`
- `mcp-codebase-memory`
- `mcp-confluence`
- `mcp-crypto`
- `mcp-devbox`
- `mcp-docker`
- `mcp-elasticsearch`
- `mcp-filesystem`
- `mcp-flux`
- `mcp-gcp`
- `mcp-git`
- `mcp-git-worktree`
- `mcp-github`
- `mcp-github-actions`
- `mcp-gitlab`
- `mcp-grafana`
- `mcp-helm`
- `mcp-k8s`
- `mcp-k8s-ops`
- `mcp-linear`
- `mcp-loki`
- `mcp-memory`
- `mcp-minio`
- `mcp-mongodb`
- `mcp-morph-embeddings`
- `mcp-morph-fast-apply`
- `mcp-ops`
- `mcp-pagerduty`
- `mcp-postgres`
- `mcp-prometheus`
- `mcp-qdrant`
- `mcp-sentry`
- `mcp-slack`
- `mcp-tavily`
- `mcp-terraform`
- `mcp-vault`
- `mcp-youtube`
- `mcp-zep`
- `MCP_CONVERSION_PLAN.md`
- `README.md`
- `ROADMAP.md`
- `test-coverage.out`

## Key Files Detected
- `README.md`
- `AGENTS.md`
- `go.mod`
- `Makefile`
- `Dockerfile`

## Tracked / Indexed Files (sample)
- `.agents/workflows/auto-edit.yaml`
- `.agents/workflows/feature-dev.yaml`
- `.codex/skills/browserkit-screenshots/SKILL.md`
- `.codex/skills/flux-gitops-operator/SKILL.md`
- `.codex/skills/mcp-registry-ops/SKILL.md`
- `.codex/skills/plan-loom-core/SKILL.md`
- `.codex/skills/polyglot-release-bumper/SKILL.md`
- `.codex/skills/workspace-branding-maintenance/SKILL.md`
- `.editorconfig`
- `.github/workflows/ci.yml`
- `.github/workflows/release.yml`
- `.gitignore`
- `.gitlab-ci.yml`
- `.golangci.yml`
- `.loom/00-index.md`
- `.loom/30-implementation-plan.md`
- `.loom/31-gap-to-backlog-map.md`
- `.loom/50-worklog.md`
- `.mcp.json`
- `.pre-commit-config.yaml`
- `AGENTS.md`
- `CHANGELOG.md`
- `Dockerfile`
- `Dockerfile.custom-server`
- `Dockerfile.custom-server.local`
- `Dockerfile.local`
- `MCP_CONVERSION_PLAN.md`
- `Makefile`
- `README.md`
- `ROADMAP.md`
- `assets/banner.png`
- `assets/header.svg`
- `assets/icon.png`
- `cmd/custom-server/main.go`
- `cmd/loom/auth.go`
- `cmd/loom/check.go`
- `cmd/loom/cmd_agent.go`
- `cmd/loom/cmd_doctor.go`
- `cmd/loom/daemon.go`
- `cmd/loom/daemon_control.go`
- `cmd/loom/hud.go`
- `cmd/loom/main.go`
- `cmd/loom/proxy.go`
- `cmd/loom/proxy_autostart_test.go`
- `cmd/loom/proxy_heartbeat_test.go`
- `cmd/loom/proxy_resources_test.go`
- `cmd/loom/proxy_templates_test.go`
- `cmd/loom/proxy_truncate.go`
- `cmd/loom/proxy_truncate_test.go`
- `cmd/loom/repl.go`
- `cmd/loom/status.go`
- `cmd/loomd/main.go`
- `cmd/loomd/rlimit_unix.go`
- `cmd/loomd/rlimit_windows.go`
- `cmd/mcp-agent-context/main.go`
- `cmd/mcp-agent-context/tools.go`
- `cmd/mcp-agent-context/tools_annotations.go`
- `cmd/mcp-agent-context/tools_compaction.go`
- `cmd/mcp-agent-context/tools_context.go`
- `cmd/mcp-agent-context/tools_fileclaims.go`
- `cmd/mcp-agent-context/tools_graph.go`
- `cmd/mcp-agent-context/tools_handoffs.go`
- `cmd/mcp-agent-context/tools_memory.go`
- `cmd/mcp-agent-context/tools_presence.go`
- `cmd/mcp-agent-context/tools_session.go`
- `cmd/mcp-agent-context/tools_tasks.go`
- `cmd/mcp-agent-context/tools_templates.go`
- `cmd/mcp-agent-context/tools_test.go`
- `cmd/mcp-agent-context/tools_workflows.go`
- `cmd/mcp-agent-context/tools_worktree.go`
- `cmd/mcp-alertmanager/main.go`
- `cmd/mcp-alertmanager/main_test.go`
- `cmd/mcp-argocd/main.go`
- `cmd/mcp-asus-router/main.go`
- `cmd/mcp-aws/main.go`
- `cmd/mcp-browserkit/main.go`
- `cmd/mcp-browserkit/main_test.go`
- `cmd/mcp-browserkit/screenshot_helper.py`
- `cmd/mcp-cloudflare/main.go`
- `cmd/mcp-cloudflare/main_test.go`
- `cmd/mcp-codebase-memory/README.md`
- `cmd/mcp-codebase-memory/ROADMAP.md`
- `cmd/mcp-codebase-memory/main.go`
- `cmd/mcp-codebase-memory/tools.go`
- `cmd/mcp-confluence/main.go`
- `cmd/mcp-crypto/main.go`
- `cmd/mcp-crypto/main_test.go`
- `cmd/mcp-devbox/async.go`
- `cmd/mcp-devbox/events.go`
- `cmd/mcp-devbox/handlers.go`
- `cmd/mcp-devbox/main.go`
- `cmd/mcp-devbox/manager.go`
- `cmd/mcp-devbox/manager_test.go`
- `cmd/mcp-devbox/metrics.go`
- `cmd/mcp-devbox/tools.go`
- `cmd/mcp-devbox/tools_test.go`
- `cmd/mcp-docker/main.go`
- `cmd/mcp-docker/main_test.go`
- `cmd/mcp-elasticsearch/main.go`
- `cmd/mcp-elasticsearch/main_test.go`
- `cmd/mcp-filesystem/main.go`
- `cmd/mcp-flux/detect_test.go`
- `cmd/mcp-flux/helmreleases.go`
- `cmd/mcp-flux/kustomizations.go`
- `cmd/mcp-flux/main.go`
- `cmd/mcp-flux/operations.go`
- `cmd/mcp-flux/probe_test.go`
- `cmd/mcp-flux/sources.go`
- `cmd/mcp-gcp/main.go`
- `cmd/mcp-git-worktree/main.go`
- `cmd/mcp-git/main.go`
- `cmd/mcp-git/main_test.go`
- `cmd/mcp-github-actions/main.go`
- `cmd/mcp-github-actions/main_test.go`
- `cmd/mcp-github/main.go`
- `cmd/mcp-github/main_test.go`
- `cmd/mcp-gitlab/issues.go`
- `cmd/mcp-gitlab/main.go`
- `cmd/mcp-gitlab/main_test.go`
- `cmd/mcp-gitlab/merge_requests.go`
- `cmd/mcp-gitlab/pipelines.go`
- `cmd/mcp-gitlab/repositories.go`
- `cmd/mcp-godot/main.go`
- `cmd/mcp-grafana/main.go`
- `cmd/mcp-grafana/main_test.go`
- `cmd/mcp-helm/main.go`
- `cmd/mcp-helm/main_test.go`
- `cmd/mcp-itchio/main.go`
- `cmd/mcp-itchio/main_test.go`
- `cmd/mcp-jira/main.go`
- `cmd/mcp-k8s-ops/main.go`
- `cmd/mcp-k8s-ops/main_test.go`
- `cmd/mcp-k8s/main.go`
- `cmd/mcp-k8s/main_test.go`
- `cmd/mcp-linear/main.go`
- `cmd/mcp-loki/main.go`
- `cmd/mcp-loki/main_test.go`
- `cmd/mcp-memory/main.go`
- `cmd/mcp-memory/main_test.go`
- `cmd/mcp-minio/main.go`
- `cmd/mcp-minio/main_test.go`
- `cmd/mcp-mongodb/main.go`
- `cmd/mcp-morph-embeddings/main.go`
- `cmd/mcp-morph-embeddings/main_test.go`
- `cmd/mcp-morph-fast-apply/main.go`
- `cmd/mcp-morph-fast-apply/main_test.go`
- `cmd/mcp-neo4j/main.go`
- `cmd/mcp-notion/main.go`
- `cmd/mcp-ops/main.go`
- `cmd/mcp-ops/main_test.go`
- `cmd/mcp-pagerduty/main.go`
- `cmd/mcp-postgres/main.go`
- `cmd/mcp-prometheus/main.go`
- `cmd/mcp-prometheus/main_test.go`
- `cmd/mcp-qdrant/main.go`
- `cmd/mcp-qdrant/main_test.go`
- `cmd/mcp-redis/main.go`
- `cmd/mcp-redis/main_test.go`
- `cmd/mcp-release/main.go`
- `cmd/mcp-release/main_test.go`
- `cmd/mcp-sentry/main.go`
- `cmd/mcp-sequentialthinking/main.go`
- `cmd/mcp-sequentialthinking/main_test.go`
- `cmd/mcp-server-mgmt/main.go`
- `cmd/mcp-server-mgmt/main_test.go`
- `cmd/mcp-slack/main.go`
- `cmd/mcp-substack/main.go`
- `cmd/mcp-substack/main_test.go`
- `cmd/mcp-tavily/main.go`
- `cmd/mcp-tavily/main_test.go`
- `cmd/mcp-terraform/main.go`
- `cmd/mcp-time/main.go`
- `cmd/mcp-time/main_test.go`
- `cmd/mcp-vault/main.go`
- `cmd/mcp-vault/main_test.go`
- `cmd/mcp-youtube/main.go`
- `cmd/mcp-youtube/main_test.go`
- `cmd/mcp-zep/main.go`
- `cmd/mcp-zep/main_test.go`
- `contrib/ghostty/loom-vibrancy.glsl`
- `docs/API_STABILITY.md`
- `docs/ARCHITECTURE.md`
- `docs/DEVELOPER_GUIDE.md`
- `docs/DEV_BUILD_LIFECYCLE.md`
- `docs/ENTERPRISE_SECURITY.md`
- `docs/ERROR_HANDLING.md`
- `docs/FLEXINFER_SITE_INTEGRATION.md`
- `docs/README.md`
- `docs/STREAMABLE_HTTP.md`
- `docs/USER_GUIDE.md`
- `docs/diagrams/README.md`
- `docs/diagrams/component.mmd`
- `docs/diagrams/config-flow.mmd`
- `docs/diagrams/internal-modules.mmd`
- `docs/diagrams/pkg-modules.mmd`
- `docs/diagrams/tool-call-sequence.mmd`
- `docs/planning/2026-01-improvements.md`
- `docs/planning/2026-02-quality-onboarding-opportunities.md`
- `docs/planning/README.md`
- `go.mod`
- `…`

## AGENTS.md Files
- `AGENTS.md`

### AGENTS.md Contents (head)

#### `AGENTS.md`
```
Agent Working Notes (loom-core)

Scope

- This file applies to the `services/loom-core` repository.

Repository Purpose

Go backend for the loom ecosystem:

- MCP server implementations (git, gitlab, github, k8s, prometheus, etc.)
- `loom` CLI for config generation and sync
- `loomd` daemon for MCP server lifecycle management

Workspace Structure

This repo is part of the `services/` GitLab group:

```text
gitlab.flexinfer.ai/
├── platform/gitops    ← K8s manifests, Flux, CI infrastructure
└── services/
    ├── loom           ← VSCode extension (TypeScript)
    └── loom-core      ← YOU ARE HERE (Go backend)
```

Deployment (GitOps)

MCP servers can be deployed to Kubernetes via Flux. Manifests live in:

- `platform/gitops/k3s/mcp-hub/servers/` - Individual MCP server deployments

To deploy an MCP server:

1. Build binaries: `make build`
2. Build container: `docker build -t registry.harbor.lan/library/loom:TAG .`
3. Push to Harbor
4. Update image tag in `platform/gitops/k3s/mcp-hub/servers/<server>/`
5. Commit and push to `platform/gitops`

Local Usage

The CLI and daemon typically run on developer machines:

```bash
# Build all binaries
make build

# Generate MCP configs for all targets
./bin/loom generate configs --target all

# Sync configs to home directory
./bin/loom sync all --regen

# Start daemon (manages MCP server processes)
./bin/loomd

# Check daemon health (includes per-server status)
curl http://localhost:9876/health

# Check SSH tunnel status
./bin/loom tunnel status
```

## Development Workflow

### Iterating on loom-core

After making code changes, use one of these targets to rebuild, install, and reload:

```bash
# Safe reload — skips daemon restart if active proxy connections exist
make dev-upgrade

# Force reload — always restarts daemon; all proxy clients auto-reconnect
make dev-reload
```

Both targets execute the same pipeline:
1. Build `loom` + `loomd` binaries
2. Atomic install to `~/.local/bin` (no window where binaries are missing)
3. Regenerate + sync platform configs (`loom sync all --regen --loom-mode`)
4. Restart daemon (`dev-upgrade` skips if busy; `dev-reload` always restarts)
5. Restart HUD if running on port 3333
6. Smoke test (proxy initialize round-trip)

### How proxy reconnection works

Each platform client (Claude Code, Codex, Zed, Gemini, etc.) spawns its own `loom proxy` process. The proxy connects to `loomd` via Unix socket. When the daemon restarts:

1. The proxy detects a broken pipe or EOF on the next tool call
2. It clears its daemon connection and calls `ensureDaemon()` on the next message
3. `ensureDaemon()` re-dials the socket (with autostart fallback)
4. The client sees no interruption — the tool call succeeds after a brief reconnect

No manual action is needed from any connected agent or IDE.

### First-time setup

```bash
make bootstrap-local    # Build + install + sync + environment check
```

### Individual platform config sync

```bash
loom sync claude --regen      # Regenerate .claude/mcp.json + .claude/settings.json
loom sync codex --regen       # Regenerate .codex/config.toml
loom sync gemini --regen      # Regenerate .gemini/config.toml + .gemini/settings.json
loom sync zed --regen         # Regenerate .zed/mcp.json
loom sync all --regen         # All platforms at once
```

### Platform permissions

Platform-specific allow/deny lists and settings are defined in the registry YAML under `platform_permissions`. Changes to permissions take effect after `loom sync` (no daemon restart required — only the platform config files change).

Registry location: `platform/gitops/mcp/context/registry.yaml`

## Daemon Features
…
```

## Notes
- Add MCP inventory via the plan-loom-core workflow (see `.loom/00-mcp-inventory.md`).
