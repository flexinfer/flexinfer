# 2026-02 Architecture and Refactor Opportunities

> **Status:** In Progress  
> **Reviewed:** 2026-02-17  
> **Window:** Commits since 2026-02-15

## Executive Summary

The last two days delivered major capability wins (OAuth/RBAC/audit/cost, HUD M3/M4 completion, devbox backend shifts), but the same period shows concentrated churn in a small set of large files. The Feb 17 commit (`8c2c50d`) already started the daemon call-pipeline and shared agent-contract refactor, so the next focus is to harden and finish those seams before expanding Tier 3 scope.

## Evidence Snapshot

- 41 commits landed since 2026-02-15.
- Top churn areas: `internal/hud` (187 touches), `internal/daemon` (49), `internal/tui` (26), `cmd/loom` (21).
- Most frequently touched files in this window include:
  - `internal/hud/frontend/dist/index.html` (12)
  - `internal/hud/app.go` (6)
  - `internal/daemon/daemon.go` (7)
  - `internal/hud/frontend/src/lib/components/PresencePanel.svelte` (6)
  - `internal/hud/frontend/src/lib/components/TasksPanel.svelte` (6)

## Priority Opportunities

### 1) Harden extracted daemon call pipeline (P0)

**Why now**
- Stage 1 has landed in `8c2c50d`: `handleCall` now delegates through `internal/daemon/callpipeline.go`.
- Remaining risk is correctness drift and future coupling: side effects (audit/cost/cache/metrics) are still tightly bound to daemon internals and need stronger stage-level tests.

**Refactor target**
- Keep `handleCall` orchestration thin in `daemon.go` and continue decomposing the pipeline internals:
  - isolate side effects (`audit`, `cost`, cache stats) behind stage helpers
  - standardize error mapping per stage (parse/auth/cache/route/transport)
  - add focused tests for send/recv failure and fallback behavior without touching full daemon boot paths

**Definition of done**
- `internal/daemon/callpipeline.go` has unit coverage for all failure classes.
- `internal/daemon/daemon.go` call-path churn drops materially versus previous week.
- Existing daemon integration tests remain green.

### 2) Finish agent contract convergence across HUD/CLI/bridge (P0)

**Why now**
- Stage 1 has landed in `8c2c50d`: shared contracts were introduced in `internal/hud/bridge/agent_contracts.go` and adopted for context-inspect + nudge policy surfaces.
- Remaining churn is concentrated in still-large surfaces (`internal/hud/bridge/agent.go` at 1773 LOC, `cmd/loom/cmd_agent.go` at 1117 LOC, `internal/hud/api_agent.go` at 912 LOC).

**Refactor target**
- Continue contract unification by concern:
  - split `cmd/loom/cmd_agent.go` subcommands into per-feature files (`context`, `nudge`, `dispatch`, `session`)
  - split `internal/hud/bridge/agent.go` into feature files while retaining one exported bridge API
  - align error envelope and status-code mapping between HUD handlers and CLI fallback paths

**Definition of done**
- Cross-surface contracts live in one place and are reused by HUD handlers and CLI.
- Duplicate request parsing/error messages are removed from HUD/CLI leaf paths.
- Negative-path tests cover HTTP and daemon fallback for each shared contract.

### 3) Split `PresencePanel.svelte` by feature modules (P1)

**Why now**
- `internal/hud/frontend/src/lib/components/PresencePanel.svelte` is 1782 LOC and currently owns handoffs, dispatch, nudges, claim bulk actions, diagnostics polling, and policy mutation UI.
- This combines unrelated state machines and makes incremental changes risky.

**Refactor target**
- Break into focused components/stores:
  - `PresenceAgentsTab`
  - `PresenceClaimsTab`
  - `PresenceWorktreesTab`
  - `PresenceHandoffsTab`
  - `PresenceDiagnosticsTab`
- Move API calls into typed client utilities (shared with other panels where possible).

**Definition of done**
- Parent panel owns tab routing only.
- Each tab has isolated state and tests for polling/mutation behavior.

### 4) Decompose devbox K8s backend responsibilities (P1)

**Why now**
- `internal/devbox/backend/k8s.go` (760 LOC) handles configmap lifecycle, build pod specs, wait/poll logic, log parsing, exec wiring, and Kubernetes object construction.
- Recent Kaniko -> Buildah and watch-related changes were fast-moving and concentrated here.

**Refactor target**
- Split by concern:
  - `k8s_build.go` (image build path)
  - `k8s_runtime.go` (start/stop/exec/status)
  - `k8s_objects.go` (pod/configmap spec builders)
  - `k8s_wait.go` (watch/poll helpers)

**Definition of done**
- Build flow and runtime flow test independently.
- Monorepo/NFS path assumptions tested in isolated unit tests.

### 5) Reduce generated HUD dist churn in feature commits (P2)

**Why now**
- Dist artifacts are being touched repeatedly alongside feature commits, which increases review noise and obscures behavior changes.

**Refactor target**
- Decide one policy and enforce it:
  - always separate `dist` regeneration commit, or
  - generate/verify dist in CI and avoid frequent checked-in rebuild noise.

**Definition of done**
- Team follows one documented policy in `docs/DEV_BUILD_LIFECYCLE.md`.
- CI verifies policy adherence.

## Recommended Focus Order (Next 2 Weeks)

1. **Week 1: Lock in P0 refactor gains**
   - Harden `callpipeline` with stage-focused tests and error-mapping cleanup.
   - Finish contract convergence split for `cmd/loom/cmd_agent.go` and `internal/hud/bridge/agent.go`.
2. **Week 1.5: Close coverage gap for Issue #2**
   - Add daemon lifecycle contention/cleanup coverage.
   - Add devbox Docker+K8s integration coverage on monorepo/NFS paths.
3. **Week 2: Observability + UI decomposition**
   - Execute Issue #12 OTel daemon export pass atop the stabilized call pipeline.
   - Split `PresencePanel.svelte` into tab modules to unblock Fleet orchestration UX (Issue #13).

## Sources

- Commands run:
  - `git rev-list --count --since='2026-02-15 00:00' HEAD`
  - `git log --since='2026-02-15 00:00' --pretty=format: --name-only | ... | sort | uniq -c | sort -nr`
  - `git log --since='2026-02-15 00:00' --oneline -- <hotspot files>`
  - `wc -l internal/daemon/*.go internal/hud/*.go internal/hud/bridge/*.go cmd/loom/*.go`
  - `wc -l internal/hud/frontend/src/App.svelte internal/hud/frontend/src/lib/components/*.svelte ...`
- Code references:
  - `internal/daemon/daemon.go:1482`
  - `internal/daemon/callpipeline.go:1`
  - `internal/hud/api_agent.go:81`
  - `internal/hud/api_agent.go:580`
  - `internal/hud/api_agent.go:665`
  - `cmd/loom/cmd_agent.go:400`
  - `cmd/loom/cmd_agent.go:734`
  - `cmd/loom/cmd_agent.go:816`
  - `internal/hud/bridge/agent.go:968`
  - `internal/hud/frontend/src/lib/components/PresencePanel.svelte:21`
  - `internal/hud/frontend/src/lib/components/PresencePanel.svelte:355`
  - `internal/devbox/backend/k8s.go:115`
  - `internal/devbox/backend/k8s.go:507`
  - `ROADMAP.md:103`
  - `ROADMAP.md:123`
