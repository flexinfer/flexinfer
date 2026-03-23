# Agent Auth Sync

This repo owns the operator-facing launchd workflow for keeping cluster agent credentials aligned
with the authenticated files on a Mac workstation.

## Canonical flow

1. Install the launchd job with `loom sync agent-tokens install`.
2. That installs `com.loom.agent-token-sync` on macOS.
3. The launchd job runs `platform/gitops/bin/sync-agent-tokens`.
4. The GitOps helper refreshes `k3s/devbox/agent-auth-tokens.yaml` from:
   - `~/.codex/auth.json`
   - `~/.gemini/oauth_creds.json`
   - `~/.gemini/google_accounts.json`
5. Flux applies the resulting `agent-auth-tokens` secret for cluster agents that use file-backed auth.

## Claude path

Claude does not use the file-backed sync above.

- HUD/devbox launches wire Claude through `ANTHROPIC_API_KEY`.
- The cluster source of truth for that key is `k3s/devbox/agent-api-keys.yaml`.
- The Claude launcher path is implemented in `internal/hud/spawn.go`.

That means `~/.claude/auth.json` is not part of the supported cluster sync story for loom-core.

## Relevant code

- `cmd/loom/cmd_sync_agent_tokens.go`
- `internal/hud/spawn.go`
- `platform/gitops/bin/sync-agent-tokens`
- `platform/gitops/k3s/devbox/agent-auth-tokens.yaml`
- `platform/gitops/k3s/devbox/agent-api-keys.yaml`
