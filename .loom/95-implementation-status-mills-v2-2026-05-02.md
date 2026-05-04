# Implementation Status: Loom Mills v2 — 2026-05-02

**Plan**: `.loom/94-implementation-plan-mills-v2-hierarchical-swarm-2026-05-02.md`
**Spec**: `.loom/93-product-spec-mills-v2-hierarchical-swarm-2026-05-02.md`
**Research**: `.loom/92-research-mills-v2-hierarchical-swarm-2026-05-02.md`

## Status by phase

| Phase | Theme | Status | Evidence |
|---|---|---|---|
| **0.1** | Docs + runbook + skill | 🟡 Partial | `docs/MILLS.md`, `docs/MILLS_RUNBOOK.md` shipped; `mcp/skills/mills-ops/SKILL.md` is empty |
| **0.2** | Default-on flip | 🟡 Partial | ConfigMap `policy.yaml: enabled: true`; operator code has no compile-time default — `EnableReconciler` defers to policy when `LOOM_MILLS_ENABLED` unset |
| **1** | v2 substrate (migrations + DAOs) | ✅ Shipped | Commit `20870464` — `feat(mills/store): v2 substrate — migrations + 5 new DAOs (Phase 1)` |
| **2** | Squad layer + routing | ✅ Shipped | Commits `5222a95d` (manifest loader + router), `c4bd9f72` (planner + REST + CLI + HUD), `1655d1ac` (reconciler integration + outcome recorder) |
| **3** | Audit swarm | ✅ Shipped | Commits `97549c97` (foundation 3.1+3.2), `174e0c14` (triggers+REST+wiring 3.3+3.4), `a30d943f` (followup + HUD 3.5+3.6) |
| **4** | Cross-repo coordination | ❌ Not started | No `pkg/mills/crossrepo/`; no `platform/gitops/k3s/mills/repos.yaml` |
| **5** | Council Debate Mode | ❌ Not started | No `pkg/mills/council/debate.go`; `policy.council.debate` keys absent |
| **6** | Bounded recursion | ❌ Not started | No `pkg/mills/pipeline/recursion.go`; `docs/MILLS.md:59` only forward-references it |
| **7** | Adaptive policy + cost preview + mobile parity | ❌ Not started | No `pkg/mills/adaptive/`, no `pkg/mills/budget/estimator.go`, no `apps/loom-companion-ios/.../MillsScreen.swift` |
| **8** | Hardening + default-on flips | ❌ Not started | No soak doc; no `docs/MILLS_V2_ROLLBACK.md` |

## Gaps in shipped phases (must close before Phase 4)

These are concrete deviations from the spec / plan that surfaced when comparing shipped code against `.loom/94-…2026-05-02.md`:

### G-1. v2 policy struct fields missing
**What**: `pkg/mills/policy.go` `Policy` struct has only v1 fields (`Enabled`, `Budgets`, `Council`, `Pipeline`, `HumanHandoff`). No `Squads`, `Audit`, `CrossRepo`, `Debate`, `Recursion`, `AdaptivePolicy` fields.
**Why it matters**: Spec calls for `policy.squads.enabled`, `policy.squads.routing.min_confidence`, `policy.audit.enabled`, `policy.audit.advisory_only`, etc. (`.loom/93-…2026-05-02.md`). Today, squad routing and audit triggers ship "always on" (or off-by-other-means) instead of behind hot-reloaded flags. Phase 8 default-on flip cannot work without these gates.
**Evidence**: `grep -nE "Squads|Audit|CrossRepo|Debate|Recursion" pkg/mills/policy.go` returns nothing; `grep -rnE "policy\.Squads|policy\.Audit" pkg/mills cmd/loom-mills-operator` returns nothing.

### G-2. ConfigMap policy.yaml not extended for v2
**What**: `platform/gitops/k3s/mills/configmap-policy.yaml` still has `version: 1` and only v1 keys.
**Why it matters**: Pairs with G-1. Even after struct fields land, the GitOps overlay must declare defaults (`squads.enabled: false`, `audit.enabled: true`, `audit.advisory_only: true`) so Phase 8 flips are one-line edits.
**Evidence**: `cat /Users/cblevins/workspace/platform/gitops/k3s/mills/configmap-policy.yaml` (file ends at line 65, no v2 sections).

### G-3. `mcp/skills/mills-ops/SKILL.md` empty
**What**: Skill directory has `references/workflow.md` and `scripts/mills_status_snapshot.sh`, but `SKILL.md` is a 0-byte file.
**Why it matters**: Phase 0 slice 0.1 acceptance is "Skill is registered after `loom sync claude --regen`" — a registry generator that ingests skills with empty `SKILL.md` will either skip or fail validation, and operators have no entry point for `loom mills` flows.
**Evidence**: `head -25 mcp/skills/mills-ops/SKILL.md` returns no output; `ls mcp/skills/mills-ops/` shows only directories.

### G-4. Policy schema version not bumped
**What**: ConfigMap declares `version: 1`. v2 added six top-level sections; spec implicitly raises this to `version: 2`.
**Why it matters**: When G-1/G-2 land, downgrade safety needs a schema marker so the policy loader can refuse v1 ConfigMaps under a v2 binary (or vice versa) instead of silently dropping unknown keys.

## Spec-driven next steps

Spec discipline: every code slice cites a section in `.loom/93-…2026-05-02.md` or `.loom/94-…2026-05-02.md`. Sequencing prefers closing gaps in shipped phases before opening Phase 4 surface area, then takes Phase 4's blocker (slice 4.1) to unblock 4.2–4.5 parallelism.

### Slice A — close gaps G-1 / G-2 / G-4 (1 worktree, ~½ cycle)
- Spec ref: `.loom/93-…2026-05-02.md` §"Policy schema (v2 additions)" + `.loom/94-…2026-05-02.md` Phase 2 slice 2.1 (loader is policy-aware) and Phase 3 slice 3.4 (audit handlers honor `advisory_only`).
- Plan: extend `pkg/mills/policy.go` with `SquadsPolicy`, `AuditPolicy`, `CrossRepoPolicy`, `DebatePolicy`, `RecursionPolicy`, `AdaptivePolicy` structs (defaults: squads.enabled=false, audit.enabled=true+advisory_only=true, others false). Bump `version: 2`. Update `platform/gitops/k3s/mills/configmap-policy.yaml` with new sections at safe defaults. Wire one read site each in `pkg/mills/squads/router.go` (skip when `policy.Squads.Enabled == false`) and `pkg/mills/audit/triggers.go` (skip when `policy.Audit.Enabled == false`).
- Acceptance: existing `pkg/mills/policy_test.go` cases stay green; new test loads a `version: 2` policy with v2 sections; squad routing returns "fallback" when `enabled: false` even with high confidence; audit triggers no-op when `enabled: false`.
- Quality gate: `go build ./... && go test ./pkg/mills/... ./cmd/loom-mills-operator/...`; `flux reconcile` rolls out updated ConfigMap on dev cluster.

### Slice B — close gap G-3 (parallel with Slice A)
- Spec ref: `.loom/94-…2026-05-02.md` Phase 0 slice 0.1.
- Plan: write `mcp/skills/mills-ops/SKILL.md` per workspace skill template (frontmatter + when-to-use + workflow steps + reference + scripts). Pull operator scenarios from `docs/MILLS_RUNBOOK.md`. Keep ≤ 200 lines.
- Acceptance: `loom sync claude --regen` succeeds; skill listed in registry; `Skill` tool can invoke `mills-ops`.

### Slice C — Phase 4.1 cross-repo registry loader (after Slice A)
- Spec ref: `.loom/94-…2026-05-02.md` Phase 4 slice 4.1; `.loom/93-…2026-05-02.md` §"Cross-Repo Federation".
- Plan: add `pkg/mills/crossrepo/registry.go` with fsnotify hot-reload mirroring `pkg/mills/squads/loader.go`; create `platform/gitops/k3s/mills/repos.yaml` seeded with loom-core, loom, flexdeck (`auto_merge: false`); mount via `kustomization.yaml`.
- Acceptance: registry loads three entries on operator boot; edit + flux reconcile picks up new entry within 5s; invalid YAML rejected with clear error and operator stays running on last-good config.
- Quality gate: `go test ./pkg/mills/crossrepo/...`; cluster smoke: `loom mills cross-repo list` (CLI from Slice F) returns 3 entries.

### Slice D / E / F — Phase 4.2–4.5 in parallel after Slice C
Slices `4.2 planner + worktree allocation`, `4.3 atomic merge + revert`, `4.4 REST handlers`, `4.5 HUD card + CLI` are file-disjoint and parallelizable per `.loom/94-…2026-05-02.md` §"Parallelization notes". Recommended worktree split:
- worktree 1: 4.2 + 4.3 (`pkg/mills/crossrepo/planner.go`, `integrator.go`, fault-injection tests)
- worktree 2: 4.4 (`cmd/loom-mills-operator/handlers_crossrepo.go`)
- worktree 3: 4.5 (HUD `CrossRepoCard.svelte` + `cmd/loom/cmd_mills_crossrepo.go`)

Phase 4 quality gate is unchanged from plan: end-to-end test on tmp git workspace with two fake repos reaches `state=merged`; injected failure produces full rollback within 60s.

### After Phase 4 — sequence
Plan order remains: 5 (debate, single cycle), 6 (recursion, single cycle), 7 (adaptive + cost preview + mobile, single cycle, three parallelizable sub-tracks), 8 (hardening + flips, sequential). No re-sequencing needed.

## Risks observed since plan was written

| Risk | Mitigation |
|---|---|
| Squads/audit shipped without policy gates means a Phase 8 flip is currently impossible (Slice A above) | Land Slice A before any new Phase 4+ code |
| Spec-driven approach assumes spec is current; no spec amendment yet for the unflagged squads/audit landing | After Slice A merges, append a spec addendum noting the path-class fallback semantics so the contract is durable |
| `version: 2` bump may surprise operators reconciling from older binaries during the upgrade window | Add a one-paragraph "policy schema v2 upgrade" subsection to `docs/MILLS_RUNBOOK.md` in Slice A |
| `mcp/skills/mills-ops/SKILL.md` empty for unknown duration; possible silent skip in registry | Slice B closes; verify `loom sync claude --regen` afterward |

## Sources

- `git log --oneline --since="2026-04-15"` (commit ids cited above)
- `pkg/mills/policy.go:22-114` — Policy struct shape (no v2 fields)
- `pkg/mills/squads/`, `pkg/mills/audit/` directory listings
- `cmd/loom-mills-operator/config.go:38-132` — `EnableReconciler *bool` + `LOOM_MILLS_ENABLED` env
- `/Users/cblevins/workspace/platform/gitops/k3s/mills/configmap-policy.yaml:1-65` — v1-only schema
- `mcp/skills/mills-ops/` directory listing (empty `SKILL.md`)
- `docs/MILLS.md:5,314-320`, `docs/MILLS_RUNBOOK.md:257-267` — v2 forward references
- `.loom/94-implementation-plan-mills-v2-hierarchical-swarm-2026-05-02.md`
- `.loom/93-product-spec-mills-v2-hierarchical-swarm-2026-05-02.md`
