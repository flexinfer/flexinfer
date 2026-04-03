# Universal Hooks Plan (GitOps/Flux First)

Date: 2026-03-25
Repo: `services/loom-core`
Scope: universal hook support across CLI targets, with first enforcement focused on GitOps/Flux-safe workflows, plus baked-in support for the `skills.sh` ecosystem

## Goal

Build a shared Loom hook/policy layer that works across all supported CLIs without pushing runtime hook state into repo/project config directories. Repository output remains limited to generated defaults plus planning/docs in `.loom`; hook state and user-specific enforcement artifacts stay in home-managed config.

## Current State

- Claude Code and Gemini generate lifecycle hooks through `pkg/generator/configs.go` into `settings.json`.
- OpenCode generates a plugin at `plugins/loom-hooks.ts`.
- Codex only gets `notify`, which bootstraps heartbeat but cannot enforce pre-tool policy.
- Antigravity, Kilocode, VS Code, and Zed are effectively hookless today.
- GitOps/Flux guardrails are hard-coded inside Claude-only `PreToolUse` logic.
- `pkg/sync` already has the right home-only direction for `settings.json`: merge hooks into home, strip hooks from repo copies, and ignore hook-only drift.
- `loom proxy` is the only truly universal choke point across CLI clients because all supported platforms can be configured to talk through it.
- Loom skills generation is currently source-of-truth local-only: it reads `mcp/context/skills-registry.yaml` and bundled `mcp/skills/`, but it does not consume the open `skills.sh` catalog or `/.well-known/agent-skills/index.json` endpoints.

## Constraints

- Keep user/runtime hook configuration home-scoped.
- Avoid adding project or repo-local hook config as source-of-truth.
- `.loom` may contain planning/decision docs only.
- Home-scoped skills should stay home-managed too; repo-local planning/docs are fine, but imported catalog state should not drift into per-project skill mirrors by default.
- First policy target is GitOps/Flux enforcement:
  - discourage or block `kubectl edit` / `kubectl set env`
  - steer users toward manifest edits plus `flux reconcile`
  - prefer policy that works even for CLIs without native pre-tool hooks

## Proposed Architecture

1. Treat native CLI hooks as lifecycle adapters.
   - Keep using native hooks for `session-start`, `session-end`, heartbeat, keepalive, and lightweight client-specific extras.

2. Move cross-CLI policy enforcement into Loom-owned shared logic.
   - Add a universal policy evaluation layer at the `loom proxy` boundary so every CLI gets the same enforcement even when the client lacks native `PreToolUse`.
   - Keep platform-native pre-tool hooks as optional early nudges where available, but make proxy policy the authoritative enforcement layer.

3. Generate home-only hook assets from a common model.
   - Centralize hook/policy definitions so Claude, Gemini, OpenCode, Codex, and future clients consume the same policy catalog.
   - Extend `sync` and `doctor` so generated hook artifacts can live in home without repo drift noise.

4. Make GitOps/Flux policy data-driven.
   - Move GitOps guardrail rules out of Claude-only hard-coded logic into registry-backed or generator-backed policy definitions that can be reused by native hooks and proxy middleware.

5. Add first-class `skills.sh` provider support.
   - Support the open agent-skills ecosystem as an input source alongside Loom's local registry.
   - Prefer well-known endpoints (`/.well-known/agent-skills/index.json` with legacy fallback) and hosted SKILL bundles over ad hoc scraping.
   - Map imported skills into Loom-managed home skill directories for supported agents without requiring the upstream `npx skills` CLI as a runtime dependency.

## Slice Plan

### Slice 1. Shared Hook Policy Model

- Title: extract a reusable hook/policy definition layer
- Goal: replace per-platform hard-coded GitOps guardrails with a shared policy model consumed by generator and proxy paths
- Expected files:
  - `pkg/generator/platform_profile.go`
  - `pkg/generator/platform_profiles.yaml`
  - `pkg/generator/configs.go`
  - `mcp/context/registry.yaml`
  - tests under `pkg/generator/*`
- Branch: `codex/universal-hooks-slice-1-policy-model`
- Acceptance criteria:
  - GitOps/Flux enforcement rules live in shared config/data, not Claude-only command strings
  - Claude/Gemini/OpenCode hook generation can reference the shared policy model
  - Codex and other non-native-hook clients can read the same policy metadata later
- Test strategy:
  - generator unit tests for policy expansion
  - snapshot or string-content assertions for generated hook commands/config

### Slice 2. Proxy-Level Universal Enforcement

- Title: enforce hook policy inside `loom proxy`
- Goal: add a pre-dispatch policy gate that can inspect outgoing tool calls and block or rewrite with an actionable Flux-first message
- Expected files:
  - `cmd/loom/cmd_proxy.go`
  - proxy runtime files under `cmd/loom/` responsible for request handling
  - `cmd/loom/proxy_*_test.go`
  - possible shared helper under `pkg/` if extraction is cleaner
- Branch: `codex/universal-hooks-slice-2-proxy-enforcement`
- Acceptance criteria:
  - a `kubectl edit` or `kubectl set env` tool invocation is consistently denied or intercepted for all proxy-backed CLIs
  - the response explains the GitOps-safe alternative: edit manifests, commit, then `flux reconcile`
  - policy attribution can be keyed by agent/platform when useful, but enforcement works even without native hooks
- Test strategy:
  - unit tests for request matching and denial payloads
  - proxy integration tests for blocked and allowed commands

### Slice 3. Home-Scoped Artifact Sync and Drift Controls

- Title: make universal hook assets home-first and drift-safe
- Goal: extend sync/status/doctor so additional hook assets can be generated and synced to home without repo churn
- Expected files:
  - `pkg/sync/manager.go`
  - `pkg/sync/ops.go`
  - `pkg/sync/status.go`
  - `pkg/sync/merge.go`
  - `pkg/generator/doctor.go`
  - tests under `pkg/sync/*` and `pkg/generator/doctor_test.go`
- Branch: `codex/universal-hooks-slice-3-home-sync`
- Acceptance criteria:
  - new shared hook assets can sync to home cleanly
  - repo copies do not become the runtime source-of-truth for hook state
  - `loom doctor` reports hook health for more than Claude/Gemini/OpenCode/Codex heartbeat-only
- Test strategy:
  - sync merge/status tests
  - doctor health tests for new artifact patterns

### Slice 4. `skills.sh` Provider and Import Flow

- Title: add `skills.sh` ecosystem support to Loom skills
- Goal: let Loom discover, list, and import hosted/open agent skills from `skills.sh`-compatible sources into Loom-managed skill homes
- Expected files:
  - `pkg/skills/registry.go`
  - `pkg/skills/generator.go`
  - new provider/import helpers under `pkg/skills/`
  - `cmd/loom/cmd_sync.go`
  - tests under `pkg/skills/*`
- Branch: `codex/universal-hooks-slice-4-skills-sh`
- Acceptance criteria:
  - Loom can consume a `skills.sh`-compatible source without shelling out to `npx skills`
  - well-known endpoint discovery is supported for CLI-agnostic skill hosting
  - imported skills are normalized into Loom's managed skill layout for supported home targets
  - home-scoped install behavior remains the default for imported skills
- Test strategy:
  - provider parsing and discovery tests
  - import/install tests against fixture skill bundles and well-known indexes
  - sync command coverage for new source flows

### Slice 5. Platform Adapters and Documentation

- Title: wire each supported CLI to the shared hook model
- Goal: update per-platform generation and docs so each CLI either uses native hooks or explicitly relies on proxy enforcement plus lifecycle notify/heartbeat, and document the `skills.sh` import story
- Expected files:
  - `pkg/generator/configs.go`
  - `pkg/generator/opencode.go`
  - `pkg/generator/doctor.go`
  - `README.md`
  - `docs/USER_GUIDE.md`
  - `docs/DEVELOPER_GUIDE.md`
- Branch: `codex/universal-hooks-slice-4-platform-adapters`
- Acceptance criteria:
  - each target has a documented hook mode: native lifecycle hooks, plugin hooks, or proxy-enforced universal hooks
  - Codex guidance clearly explains that `notify` remains lifecycle bootstrap while policy enforcement happens in proxy
  - docs preserve the home-only config contract
- Test strategy:
  - config generation tests per platform
  - doctor expectations
  - doc pass for consistency and command examples

## Integration Order

1. Slice 1 first because it defines the shared policy contract.
2. Slice 2 next because proxy enforcement is the first user-visible GitOps win.
3. Slice 3 can run in parallel once the artifact contract is stable enough.
4. Slice 4 can run in parallel with Slice 3 because it is mostly isolated to `pkg/skills` and CLI plumbing.
5. Slice 5 lands last to wire all platforms and update docs after implementation settles.

## Recommended Worker Layout

1. Worker A owns Slice 1.
2. Worker B owns Slice 2.
3. Worker C owns Slice 3.
4. Worker D owns Slice 4.
5. Main thread integrates Slice 5 and conflict resolution.

## Risks

- Overfitting policy to shell-command tools only. We should match both native shell tools and MCP tool surfaces where practical.
- Confusing “native hooks” with “universal enforcement.” The design should keep those separate.
- Creating repo/home drift if new generated artifacts are copied both places without explicit strip/merge behavior.
- Blocking legitimate incident response workflows. The policy should be targeted at default GitOps flows first, with room for controlled overrides later.
- Overbuilding a second package manager for skills. Loom should focus on source ingestion, normalization, and home-sync, not recreate every interactive feature of the upstream `skills` CLI.
- Diverging from the upstream agent-skills spec. Import logic should stay centered on SKILL.md plus well-known endpoint formats that `skills.sh` already documents.

## Immediate Deliverable

First milestone:

- shared GitOps/Flux policy definition
- proxy-level denial path for `kubectl edit` and `kubectl set env`
- preserved home-only config semantics
- initial `skills.sh` source ingestion path for Loom-managed skills
- updated doctor/tests/docs to reflect the new enforcement model
