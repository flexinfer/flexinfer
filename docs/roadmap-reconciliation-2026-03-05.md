# Roadmap/Backlog Reconciliation - 2026-03-05

## Scope
- Repository: `services/loom-core`
- Baseline: `origin/main` as of 2026-03-05
- Goal: reconcile local worktrees/backlog slices with implemented code now on `main`.

## Integration Completed This Session

Commits integrated to `main` from local backlog/worktree branches:
- `996694e` test(loom): add tools search/get param coverage and null protocol guards
- `333892f` docs(roadmap): add observability expansion issue backlink
- `44c28b8` feat(generator): data-driven platform profiles in YAML (CONFIG-1)
- `9fcaa73` feat(generator): unified config generator interface with GenerateParams and profile-driven dispatch (CONFIG-2)
- `d0d1518` feat(cli): unified platform status with agent/session counts (UNIFY-3)
- `35f4601` docs: remove hardcoded MCP server counts
- `a6d62b3` fix(daemon): remove hardcoded OTel traced server count
- `5b9d012` test(ci): add enterprise smoke suite entrypoint for issue #29
- `d4df318` feat(rbac): add dry-run simulation mode and CLI tooling
- `18e9596` hud: operationalize stale mobile push token cleanup
- `8413fb8` feat(otel): derive traced server coverage and emit transport span events
- `aa85978` docs: reconcile roadmap backlog against integrated implementation
- `94c1a08` test(daemon): align OTel coverage fixture with traced server detection
- `87cc127` feat(rbac): add daemon simulation decision tracing

## Worktree Cleanup Completed

Removed stale/merged worktrees:
- `.worktrees/backlog-45-20260304`
- `.worktrees/backlog-CONFIG-1`
- `.worktrees/backlog-CONFIG-2`
- `.worktrees/backlog-UNIFY-3`
- `.worktrees/codex-ci-throughput-20260304`
- `.worktrees/issue-29-20260303`
- `.worktrees/loom-core-26-20260303`
- `.worktrees/loom-core-36-20260304`
- `.worktrees/antigravity-tool-limit-100-20260304`
- `.worktrees/backlog-SIMP-9`
- `.worktrees/backlog-SIMP-10`
- `.worktrees/backlog-SIMP-11`
- `.worktrees/backlog-SIMP-12`
- `.worktrees/loom-core-simp-session-lifecycle-20260303`
- `.worktrees/issue-12-20260303`
- `.worktrees/issue-6-20260303`
- `.worktrees/openai-hybrid-simplification-20260304`
- `.worktrees/openai-responses-tool-context-20260304`
- `.worktrees/loom-core-26-20260302`

Deleted corresponding fully-integrated local branches.

## Remaining Active Local Worktrees

1. `mcp-tools-as-code-api-20260304` (`codex/integration-seq-main`)
- Status: clean integration branch used for this reconciliation.

Note: repo-local stale RBAC worktree `loom-core-26-20260302` was integrated via a clean follow-up commit (`87cc127`) and then removed. The only remaining repo-local worktree is the clean integration worktree above. Additional workspace-level worktrees still exist outside `services/loom-core/.worktrees/`; they were not part of this repo-local cleanup pass.

## Roadmap Issue Status Snapshot (from GitLab)

Referenced issues in `ROADMAP.md` currently `opened`:
- #2 Quality gates for new MCP servers
- #5 Observability expansion
- #6 Onboarding and docs consistency
- #12 OTel trace export from daemon
- #13 Fleet orchestration UX
- #14 MCP server catalog and discovery
- #20 Harden daemon call pipeline extraction
- #21 Converge agent contracts across HUD/CLI/bridge
- #23 Decompose devbox K8s backend responsibilities
- #25 Gateway policy hooks
- #26 RBAC dry-run/simulation tooling
- #27 RBAC policy lint/conflict detection
- #29 Enterprise smoke suite
- #52 HUD cost dashboard integration

Referenced issues currently `closed`: #7, #8, #9, #10, #11, #22, #24.

## Backlog vs Implementation (Current)

- `CONFIG-1`: implemented and merged (`44c28b8`).
- `CONFIG-2`: implemented and merged (`9fcaa73`).
- `UNIFY-3`: implemented and merged (`d0d1518`).
- Issue #29 smoke suite: implemented and merged (`5b9d012`).
- Issue #26 RBAC dry-run simulation: baseline merged (`d4df318`) and follow-up daemon decision-tracing/simulation metadata merged (`87cc127`).
- Issue #12 OTel trace export: runtime/event instrumentation merged (`8413fb8`), issue status should be re-evaluated after CI.
- OpenAI Responses planning track: research/spec/implementation artifacts exist; implementation slices for CLI tool discovery/protocol hardening are partially landed; branch-level supersession still needed.

## Open Issue Reconciliation Matrix

### A. Open issues with evidence already on `main` and likely needing status cleanup

- `#26` RBAC dry-run/simulation tooling
  - Mainline evidence: `cmd/loom/cmd_rbac.go`, `internal/daemon/daemon_dispatch.go`, `internal/daemon/rbac.go`, `internal/daemon/daemon_dispatch_rbac_test.go`, `cmd/loom/cmd_rbac_test.go`.
  - Assessment: implementation intent is now on `main`; backlog state lags reality.

- `#52` HUD cost dashboard integration
  - Mainline evidence:
    - HUD monitor + API: `internal/hud/monitor/cost.go`, `internal/hud/app.go`
    - SSE event: `hud.cost` in `internal/hud/app.go`
    - frontend store + Overview tile wiring: `internal/hud/frontend/src/lib/stores/cost.svelte.ts`, `internal/hud/frontend/src/lib/components/OverviewPanel.svelte`
    - daemon cost stats types: `internal/hud/bridge/daemon.go`, `internal/daemon/cost.go`
  - Assessment: most acceptance criteria appear implemented on `main`; local branch `codex/hud-cost-kpi-52` now mostly represents tests/docs cleanup rather than core feature delivery.

- `#56` Skills registry auto-update `updated:` date
  - Mainline evidence: `pkg/skills/generator.go` (`UpdateRegistryDate`, invoked from `Generate`) plus `pkg/skills/generator_test.go` coverage.
  - Assessment: behavior exists on `main`; branch `codex/skills-updated-date-56` is superseded by different landed commits.

### B. Open issues with partial implementation on `main` but still-missing acceptance pieces

- `#12` OTel trace export from daemon
  - Mainline evidence:
    - daemon OTel status surface: `loom/otel-status` and HUD `/api/otel`
    - traced-server coverage instrumentation merged (`8413fb8`)
  - Gaps still reflected in roadmap:
    - configurable OTLP export from daemon runtime
    - broader daemon lifecycle spans/metrics beyond current coverage
    - richer HUD operator views

- `#53` HUD RBAC and audit visibility surfaces
  - Mainline evidence:
    - `/api/rbac` exists in `internal/hud/app.go`
    - RBAC store/panels exist in `internal/hud/frontend/src/lib/stores/rbac.svelte.ts` and `ServersPanel.svelte`
    - mobile API already exposes `denied_count` in `internal/hud/api_mobile.go`
  - Gaps on `main`:
    - `internal/hud/bridge/daemon.go` `RBACConfigResult` does not yet carry `audit_enabled` or explicit `denied_count`
    - unmerged local branches remain: `codex/hud-rbac-bridge-53`, `codex/hud-rbac-audit-53`, `codex/hud-rbac-audit-53b`, `codex/hud-rbac-audit-53c`

### C. Open issues with unmerged local branches and still-valid backlog scope

- `#14` MCP server catalog and discovery
  - Unmerged local branch: `codex/mcp-catalog-discovery-14` (`8daae39` `feat(cli): add loom catalog list command`)
  - Mainline evidence: no `catalog` command present under `cmd/loom`.

- `#23` Devbox K8s backend decomposition
  - Unmerged local branch: `codex/devbox-k8s-decompose-23` (`c483939`)
  - Mainline evidence: `internal/devbox/backend/k8s.go` remains monolithic in roadmap terms.

- `#50` Split large MCP server `main.go` files
  - Unmerged local branch: `codex/mcp-gitlab-split-projects-50` (`d6e0c4e`, `0a76a2b`, `5bc3679`, `09759b2`, `a76f757`)
  - Mainline evidence: extraction work is not yet merged.

- `#55` Skills resource validation
  - Unmerged local branch: `codex/skills-validate-resources-55` (`26ce14b`)
  - Mainline evidence: `pkg/skills/generator.go` validates only explicit `script.Path` and does not implement the branch's named-resource fallback validation path described in the issue.

- `#57` Skills template escape support
  - Unmerged local branch: `codex/skill-template-escape-57` (`e925118`, `7c04c62`, `b360fa2`)
  - Mainline evidence: `pkg/skills/registry.go` preserves escaped `${...}` references but does not yet fully resolve `${HOME}` / `${CODEX_HOME}` across non-Codex targets as described in the branch issue notes.

- `#58` Skills always-allow safety audit
  - Unmerged local branch: `codex/skills-always-allow-audit-58`
  - Branch contains two distinct buckets:
    - still-valid skills hardening commit: `2d1e514`
    - unrelated follow-up slices spun out into issues `#60`, `#61`, `#62`
  - Assessment: keep issue open until `2d1e514` is reconciled separately from the split integration issues.

### D. New open integration issues not yet reflected in `ROADMAP.md`

- `#60` Integration slice: devbox K8s workspace sync (git-clone + tar-pipe)
- `#61` Integration slice: mobile control-plane APIs + iOS sandbox ops
- `#62` Integration slice: iOS diagnostics + remote HUD ingress + godot lint guard

These are active backlog items created after the roadmap snapshot and should be considered during the next roadmap refresh or folded into the relevant parent roadmap lines.

## Evidence
- `git cherry -v origin/main <branch>` across remaining worktrees
- `git worktree list --porcelain`
- `glab api /projects/services%2Floom-core/issues/<iid>` for roadmap-linked issues
- integration commits listed above from `codex/integration-seq-main`
