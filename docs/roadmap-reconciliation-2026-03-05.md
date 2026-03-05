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

Deleted corresponding fully-integrated local branches.

## Remaining Active Local Worktrees

1. `issue-12-20260303` (`codex/otel-trace-export-12-20260303`)
- Status: clean
- Delta vs `origin/main`: `d8e9538` (now integrated as `8413fb8`)
- Action: safe to remove after `main` push/CI success.

2. `issue-6-20260303` (`codex/docs-entrypoint-6`)
- Status: dirty (`docs/roadmap-reconciliation-2026-03-04.md` untracked)
- Delta vs `origin/main`: one patch-equivalent commit integrated, one docs-patch not patch-identical (`5ec7d78`).
- Action: retain for now; if needed, re-land doc-only deltas explicitly.

3. `loom-core-26-20260302` (`codex/rbac-dry-run-sim-26-wip-20260303`)
- Status: dirty (mixed staged/unstaged edits on RBAC files)
- Delta vs `origin/main`: base RBAC feature already integrated; this worktree has uncommitted WIP follow-ups.
- Action: requires focused follow-up branch or explicit discard decision.

4. `openai-hybrid-simplification-20260304` (`codex/openai-hybrid-simplification-20260304`)
- Status: clean
- Delta vs `origin/main`: commit `4c44537` is not patch-identical; key pieces were integrated via `996694e` and existing daemon/tool-search implementation.
- Action: keep temporarily until we explicitly close/supersede this branch in backlog tracking.

5. `openai-responses-tool-context-20260304` (`codex/openai-responses-tool-context-plan`)
- Status: dirty (`.loom` planning/index/worklog edits)
- Delta vs `origin/main`: no unique commits; local edits are planning metadata variants.
- Action: preserve as planning scratch or fold selective docs into a fresh planning update.

6. `mcp-tools-as-code-api-20260304` (`codex/integration-seq-main`)
- Status: clean integration branch used for this reconciliation.

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
- Issue #26 RBAC dry-run simulation: implemented baseline merged (`d4df318`), follow-up WIP remains in `loom-core-26-20260302`.
- Issue #12 OTel trace export: runtime/event instrumentation merged (`8413fb8`), issue status should be re-evaluated after CI.
- OpenAI Responses planning track: research/spec/implementation artifacts exist; implementation slices for CLI tool discovery/protocol hardening are partially landed; branch-level supersession still needed.

## Evidence
- `git cherry -v origin/main <branch>` across remaining worktrees
- `git worktree list --porcelain`
- `glab api /projects/services%2Floom-core/issues/<iid>` for roadmap-linked issues
- integration commits listed above from `codex/integration-seq-main`
