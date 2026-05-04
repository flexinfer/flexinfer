# Implementation Plan: Loom Mills v2 — Hierarchical Swarm (v2.0)

**Date**: 2026-05-02
**Research**: `.loom/92-research-mills-v2-hierarchical-swarm-2026-05-02.md`
**Product Spec**: `.loom/93-product-spec-mills-v2-hierarchical-swarm-2026-05-02.md`

## Sequencing summary

V2 ships in **eight phases**. Phase 0 is two leftover v1 slices (6.5 docs + 6.6 default-on). Phases 1–7 deliver v2.0; v2.1 follow-ups are listed at end. Each phase is independently shippable behind a policy flag and has a concrete acceptance test against a dev k3s cluster.

| Phase | Theme | Outcome | Time estimate |
|---|---|---|---|
| **0** | Close v1 (slices 6.5 + 6.6) | Mills docs + runbook + skill; default-on flip | 1 cycle |
| **1** | v2 substrate (migrations + DAOs) | `002_v2.sql` migration; new DAOs for squads/audit/cross-repo/debate/policy_proposals/recursion; tests | 1 cycle |
| **2** | Squad layer + routing | Squad manifests in GitOps; loader + router + planner; outcome recorder; HUD `Squads` panel; CLI | 2 cycles |
| **3** | Audit swarm | `pkg/mills/audit/` package; pool dispatcher; rubrics; persistence; HUD `Audit` panel; CLI | 1–2 cycles |
| **4** | Cross-repo coordination | Repo registry; cross-repo integrator; per-repo timeout + atomic merge; HUD card; CLI | 1–2 cycles |
| **5** | Council Debate Mode | Debate runner; moderator; sidecar extension; policy gates; tests | 1 cycle |
| **6** | Bounded recursion | Recursion guards; new MCP tool; budget tree; tests; HUD recursion histogram | 1 cycle |
| **7** | Adaptive policy + cost preview + mobile parity | Sunday job; proposal writer + apply endpoint; HUD card; estimator; mobile screen | 1 cycle |
| **8** | Hardening + v2.0 default-on flip | Cross-cutting smoke; rollback playbook; docs; flip cross-repo / audit / debate flags after dogfood | 1 cycle |

Each v2 feature ships behind its own `policy.<feature>.enabled` flag, default false. Phase 8 flips them in order with one-week soak between flips.

---

## Phase 0 — Close v1 (slices 6.5 + 6.6)

### Slice 0.1 — Docs + runbook + skill

**Files (new):**
- `docs/MILLS.md` — architecture, cluster deployment, policy reference, council brief composition, pipeline stages, gate semantics, persistence schema, eval rubric, common operator scenarios. Pulls from `.loom/89-…2026-04-25.md`, `.loom/90-…2026-04-25.md`, current code state.
- `docs/MILLS_RUNBOOK.md` — pause/resume, force-escalate, replay a council run, audit a merge, recover-from-corrupted-DB.
- `mcp/skills/mills-ops/SKILL.md` (+ `references/`, `scripts/` if needed) — operator skill for `loom mills` flows. Mirrors workspace pattern of other skills under `mcp/skills/`.
- `mcp/skills/mills-ops/scripts/mills_status.sh` — quick read-only status dump.

**Acceptance:** Docs render correctly via `pnpm --dir docs dev` (if applicable; otherwise `mkdocs build`). Skill is registered after `loom sync claude --regen`.

### Slice 0.2 — Default-on flip + production rollout playbook

**Files (modified):**
- `cmd/loom-mills-operator/config.go` — default `LOOM_MILLS_ENABLED=true` (kill switch via `policy.enabled: false` remains).
- `docs/MILLS_RUNBOOK.md` — append "production rollout staging" section: local → dev cluster (kc-k3s) → production.
- `platform/gitops/k3s/mills/configmap-policy.yaml` — set `enabled: true` for the dev cluster path; production overlay unchanged until staging soak completes.

**Acceptance:** All 12 v1 spec acceptance criteria green. Production opt-in is a one-line policy edit. Phase 0 quality gate: `go build ./... && go test ./pkg/mills/... ./cmd/loom-mills-operator/...` green; `flux reconcile` rolls out cleanly on dev cluster.

---

## Phase 1 — v2 substrate (migrations + DAOs)

### Slice 1.1 — Migration `002_v2.sql`

**Files (new):**
- `pkg/mills/store/migrations/002_v2.sql` — full DDL from spec §"Persistence layer". Append-only after `001_initial.sql`.

**Files (modified):**
- `pkg/mills/store/migrate.go` — verify the migration runner picks up `002_v2.sql`; no behavior change.

**Acceptance:** Fresh `:memory:` DB applies both migrations cleanly; existing DB upgrades cleanly without data loss; idempotent re-apply succeeds.

### Slice 1.2 — DAOs (squads, audit, cross-repo, debate, recursion, policy proposals)

**Files (new):**
- `pkg/mills/store/dao_squad.go` — CRUD on `squads`, `squad_memory`, `squad_outcomes`; query helpers `MemoryRecall(squad, kind, limit)`, `SuccessRate(squad, pathClass, window)`.
- `pkg/mills/store/dao_audit.go` — CRUD on `audit_findings`; queries by subject_kind/subject_id.
- `pkg/mills/store/dao_crossrepo.go` — CRUD on `cross_repo_runs`.
- `pkg/mills/store/dao_debate.go` — CRUD on `council_debate_rounds`.
- `pkg/mills/store/dao_policy_proposal.go` — CRUD on `policy_proposals`.
- `pkg/mills/store/types.go` — append new struct types (Squad, SquadMemory, SquadOutcome, AuditFinding, CrossRepoRun, CouncilDebateRound, PolicyProposal).
- `pkg/mills/store/dao_pipeline.go` (modified) — extend `PipelineRun` struct + queries with `parent_run_id` and `depth`.
- `pkg/mills/store/store_test.go` (modified) — add table tests for every new DAO using `:memory:`.

**Acceptance:** All new DAOs have round-trip insert→query→equal tests; concurrency test (16 goroutines) shows no `database is locked`; `parent_run_id` foreign-key cascade behaves correctly.

**Phase 1 quality gate:** `go build ./... && go test -tags=mills_store ./pkg/mills/store/...`.

---

## Phase 2 — Squad layer + routing

### Slice 2.1 — Squad manifest schema + loader

**Files (new):**
- `pkg/mills/squads/types.go` — Go struct for `Squad` matching the YAML schema in spec §"Squad manifest (YAML)".
- `pkg/mills/squads/loader.go` — fsnotify-watched loader for `platform/gitops/k3s/mills/squads/*.yaml`. On change, validate, then reflect into the canonical store (`squads` table). Mirrors the policy hot-reload pattern in `pkg/mills/policy.go`.
- `pkg/mills/squads/loader_test.go` — table tests for valid/invalid manifests.

**Files (new, GitOps):**
- `platform/gitops/k3s/mills/squads/_default.yaml` (= v1 behavior)
- `platform/gitops/k3s/mills/squads/hud-frontend.yaml`
- `platform/gitops/k3s/mills/squads/gitops.yaml`
- `platform/gitops/k3s/mills/kustomization.yaml` (modified) — append the new squad manifests as a ConfigMap mount.

**Files (modified):**
- `cmd/loom-mills-operator/main.go` — boot the squad loader after policy + budget; gate behind `policy.squads.enabled`.

**Acceptance:** Operator reads manifests on boot; hot-reload picks up edits within 5s; invalid manifests rejected with clear error and operator stays running on last-good config.

### Slice 2.2 — Squad router + confidence calculator

**Files (new):**
- `pkg/mills/squads/router.go` — `Router.Pick(item) → (squad, confidence)`. Path-glob match (`doublestar/v4`), then confidence from `squad_outcomes` over last 30 outcomes in matching path-class. Respects `policy.squads.routing.min_confidence`.
- `pkg/mills/squads/router_test.go` — golden-input tests with seeded outcomes.

**Files (modified):**
- `pkg/mills/reconciler.go` — when picking up a queued backlog item, if `policy.squads.enabled` and a squad scores ≥ `min_confidence`, call into the squad path; else fall back to v1 generic path.

**Acceptance:** Router reproducibly picks `hud-frontend` for items with paths `internal/hud/frontend/**` when squad has ≥ 0.6 success rate; falls back to `_default` when below threshold; admin endpoint `POST /api/mills/squads/{name}/route-test` returns same decision as the live router.

### Slice 2.3 — Squad planner + outcome recorder

**Files (new):**
- `pkg/mills/squads/planner.go` — `Plan(item) → squadPlan`. Loads top-20 squad memory by importance, composes prompt from `pkg/mills/squads/prompts/squad_planner.md` + squad conventions, dispatches via spawn (per squad's `ensemble.editor`). Returns refined sidecar slices, gates list, budget request.
- `pkg/mills/squads/prompts/squad_planner.md` — prompt template.
- `pkg/mills/squads/outcome_recorder.go` — subscribes to `pipeline_runs.state→merged` (via existing event log); writes `squad_outcomes` row with regression status (deferred 24h via existing regression gate event).
- `pkg/mills/squads/planner_test.go`, `outcome_recorder_test.go`.

**Files (modified):**
- `pkg/mills/pipeline/runner.go` — when a squad plan is supplied (via reconciler), use it instead of the default planner; plumb `squad_name` into `pipeline_runs.metadata`.
- `pkg/mills/eval/outcome_attributor.go` — when attributing a merge to a council run, also attribute to the squad if present.

**Acceptance:** End-to-end test: a fixture backlog item with HUD-frontend paths gets planned by the hud-frontend squad; the resulting pipeline run is recorded with `squad_name="hud-frontend"`; on merge, `squad_outcomes` row appears with `outcome=merged_clean`; on regression alert in the next 24h, the row updates to `merged_regressed`.

### Slice 2.4 — REST + MCP surface

**Files (new):**
- `cmd/loom-mills-operator/handlers_squads.go` — implement endpoints from spec §"REST + MCP surface".
- `cmd/loom-mills-operator/handlers_squads_test.go`.

**Files (modified):**
- `cmd/loom-mills-operator/server.go` — register squad handlers + admin-token gate on mutating endpoints (`route-test` is admin-only since it can mention sensitive paths).
- `cmd/mcp-mills/main.go` (or `cmd/loom-mills-operator/mcp.go` if MCP server is embedded — verify per current code) — register `mills_squads_list`, `mills_squad_memory_recall`.

**Acceptance:** `GET /api/mills/squads` returns both seed squads with current outcomes; `mills_squad_memory_recall` returns rows ordered by importance.

### Slice 2.5 — HUD `Squads` panel

**Files (new):**
- `internal/hud/frontend/src/lib/components/Mills/SquadsPanel.svelte` — per-squad cards (success rate, avg cost, in-flight, top memory items, latest audit score).
- `internal/hud/frontend/src/lib/stores/mills_squads.svelte.ts` — store with polling.

**Files (modified):**
- `internal/hud/frontend/src/routes/mills/+page.svelte` — add `Squads` tab/section.
- `internal/hud/domain/mills/proxy.go` — proxy `/api/mills/squads*` to operator.

**Acceptance:** Panel renders against live operator with seeded fixture; empty state shows when no squads have outcomes yet.

### Slice 2.6 — `loom mills squads` CLI

**Files (new):**
- `cmd/loom/cmd_mills_squads.go` — subcommands `list`, `show <name>`, `memory <name>`, `route-test <backlog_id>`.
- `cmd/loom/cmd_mills_squads_test.go`.

**Files (modified):**
- `cmd/loom/cmd_mills.go` — register subcommands.

**Acceptance:** `loom mills squads list` prints both seed squads with outcome stats; `loom mills squads route-test MILLS-2026-05-02-001` prints `chosen=hud-frontend confidence=0.74`.

**Phase 2 quality gate:** `go build ./... && go test ./pkg/mills/squads/... ./cmd/loom-mills-operator/... ./cmd/loom/...`; HUD `pnpm typecheck && pnpm build && pnpm test`; cluster smoke: `flux reconcile` picks up squad manifests; `loom mills squads list` returns 200 from a Mac.

---

## Phase 3 — Audit swarm

### Slice 3.1 — `pkg/mills/audit/` package skeleton + rubrics

**Files (new):**
- `pkg/mills/audit/types.go` — `AuditRequest`, `AuditFinding`, `AuditPool`, `AuditResult`.
- `pkg/mills/audit/rubric/audit_v1_council.md` — adversarial council rubric.
- `pkg/mills/audit/rubric/audit_v1_pipeline.md` — adversarial pipeline rubric.
- `pkg/mills/audit/rubric.go` — rubric loader; embeds files via `//go:embed`.

**Acceptance:** Rubrics load deterministically; rubric file changes require a code change (not hot-reload) to keep the audit contract stable.

### Slice 3.2 — Pool dispatcher + escalation

**Files (new):**
- `pkg/mills/audit/dispatcher.go` — runs the pool concurrently; aggregates by median; escalates when 0.4 < median < 0.7.
- `pkg/mills/audit/dispatcher_test.go` — fakes for spawn + flexinfer.

**Files (modified):**
- `pkg/mills/clients/flexinfer.go` — confirm reuse; add a small `AuditCall(model, prompt, artifact)` helper if needed.

**Acceptance:** Pool runs in parallel; escalation path triggers correctly under fake conditions; cost-tracked under `loom_mills_audit_cost_usd_total`.

### Slice 3.3 — Triggers + persistence

**Files (new):**
- `pkg/mills/audit/triggers.go` — listens for council artifact commits and pipeline merges via existing event log; enqueues audit work in a small in-memory queue (operator-local; not persisted because audits are best-effort).
- `pkg/mills/audit/triggers_test.go`.

**Files (modified):**
- `pkg/mills/council/artifacts.go` — emit a `council_artifact_committed` event into the event log on commit.
- `pkg/mills/pipeline/runner.go` — emit `pipeline_run_merged` event on terminal-merged transition (event log already used by attribution; add a typed event).

**Acceptance:** Each council commit produces exactly one audit attempt; each pipeline merge produces exactly one audit attempt. Audits never block the producing path.

### Slice 3.4 — REST + MCP surface

**Files (new):**
- `cmd/loom-mills-operator/handlers_audit.go` — `GET /api/mills/audit/findings`, `POST /api/mills/audit/run` (admin).
- `cmd/loom-mills-operator/handlers_audit_test.go`.

**Files (modified):**
- `cmd/mcp-mills/main.go` (or operator MCP wiring) — register `mills_audit_findings_list`.

### Slice 3.5 — HUD `Audit` panel

**Files (new):**
- `internal/hud/frontend/src/lib/components/Mills/AuditPanel.svelte` — survival trend, top findings, severity histogram.
- `internal/hud/frontend/src/lib/stores/mills_audit.svelte.ts`.

**Files (modified):**
- `internal/hud/frontend/src/routes/mills/+page.svelte` — add `Audit` tab.
- `internal/hud/frontend/src/lib/components/Mills/EvalPanel.svelte` — add an `Audit` sub-tab.

**Acceptance:** Audit findings appear within ≤ 2 minutes of producing event; advisory-only banners show on items with `survival_score < 0.6`.

### Slice 3.6 — Advisory-only follow-up issues

**Files (new):**
- `pkg/mills/audit/followup.go` — when `survival_score < 0.6` for a council artifact: open MR overriding `.loom/` fast-merge with summary referencing audit findings; for a pipeline merge: open follow-up GitLab issue tagged P1 with audit findings.
- `pkg/mills/audit/followup_test.go` (fake gitlab).

**Phase 3 quality gate:** `go test ./pkg/mills/audit/... ./cmd/loom-mills-operator/...`; HUD typecheck + build; smoke: trigger a council run on a dev cluster and verify an `audit_findings` row appears within 2 minutes.

---

## Phase 4 — Cross-repo coordination

### Slice 4.1 — Repo registry loader

**Files (new):**
- `pkg/mills/crossrepo/registry.go` — loader for `platform/gitops/k3s/mills/repos.yaml`; hot-reload via fsnotify.
- `pkg/mills/crossrepo/registry_test.go`.

**Files (new, GitOps):**
- `platform/gitops/k3s/mills/repos.yaml` — initial entries (loom-core, loom, flexdeck `auto_merge: false`).

**Files (modified):**
- `platform/gitops/k3s/mills/kustomization.yaml` — mount registry as ConfigMap.

### Slice 4.2 — Cross-repo planner + worktree allocation

**Files (new):**
- `pkg/mills/crossrepo/planner.go` — for a backlog item with `repos[]`, allocates one worktree per repo via `agent_worktree_allocate`; returns a `MultiRepoPlan` consumed by the pipeline runner.
- `pkg/mills/crossrepo/planner_test.go`.

**Files (modified):**
- `pkg/mills/pipeline/runner.go` — branch on `item.repos != nil`: cross-repo path runs per-repo stages in parallel, awaits all CIs, then calls `crossrepo.AtomicMerge`.

### Slice 4.3 — Atomic merge + revert

**Files (new):**
- `pkg/mills/crossrepo/integrator.go` — `WaitForGreen(crossRepoRun)`, `AtomicMerge(crossRepoRun)`. Per-repo timeout from `policy.cross_repo.per_repo_timeout_minutes`; on any failure, revert all open MRs and any already-merged in reverse order.
- `pkg/mills/crossrepo/integrator_test.go` (fake gitlab with race conditions).

**Acceptance:** Fault-injection test: repo-2 CI fails after repo-1 merged → revert MR opened on repo-1 within 60s; cross_repo_runs row marked `reverted`.

### Slice 4.4 — REST + MCP surface

**Files (new):**
- `cmd/loom-mills-operator/handlers_crossrepo.go` — `GET /api/mills/cross-repo/runs`, `POST /api/mills/cross-repo/runs/{id}/abort`.

### Slice 4.5 — HUD card + CLI

**Files (new):**
- `internal/hud/frontend/src/lib/components/Mills/CrossRepoCard.svelte` — atomicity rate, in-flight runs.
- `cmd/loom/cmd_mills_crossrepo.go` — `loom mills cross-repo list`, `... abort <id>`.

**Files (modified):**
- `internal/hud/frontend/src/routes/mills/+page.svelte` — wire card.

**Phase 4 quality gate:** End-to-end test on a tmp git workspace with two fake repos: a backlog item with two `repos[]` entries reaches `state=merged` with both MRs merged; injected failure produces full rollback.

---

## Phase 5 — Council Debate Mode

### Slice 5.1 — Debate runner + moderator

**Files (new):**
- `pkg/mills/council/debate.go` — round-by-round runner per spec §"Council Debate Mode".
- `pkg/mills/council/moderator.go` — moderator agent that decides convergence between rounds.
- `pkg/mills/council/prompts/moderator.md`.
- `pkg/mills/council/debate_test.go` — golden inputs for a 2-round + a 3-round debate.

**Files (modified):**
- `pkg/mills/council/editor.go` — accept an optional `revisionContext` (prior critiques + focus areas).
- `pkg/mills/council/reviewer.go` — accept an optional `focusAreas` for refocused round-2 critiques.
- `pkg/mills/council/runner.go` — branch on `policy.council.debate.enabled[trigger]`: debate mode vs. single-pass.
- `pkg/mills/council/sidecar.go` — extend sidecar with `debate{}` field.

### Slice 5.2 — Persistence + budget cap

**Files (modified):**
- `pkg/mills/council/runner.go` — write `council_debate_rounds` rows; honor `policy.council.debate.max_usd` and `early_exit_threshold`.

**Files (new):**
- `pkg/mills/budget/debate.go` — small budget tracker tying into existing `pkg/mills/budget.go`.

### Slice 5.3 — HUD + sidecar surface

**Files (modified):**
- `internal/hud/frontend/src/lib/components/Mills/CouncilPanel.svelte` — add a "Debate Rounds" expandable view per council run.

**Acceptance:** Incident-triggered council run surfaces a 2-round debate; sidecar `debate.rounds` populated; total cost ≤ $8.00 in test fixture.

**Phase 5 quality gate:** `go test ./pkg/mills/council/...`; HUD smoke; verify debate metrics increment.

---

## Phase 6 — Bounded recursion

### Slice 6.1 — Recursion guards + MCP tool

**Files (new):**
- `pkg/mills/pipeline/recursion.go` — `SubrunCreate(parent, sliceSpec, depth)` with depth/budget/cycle guards.
- `pkg/mills/pipeline/recursion_test.go` — tests for depth cap, budget tree, cycle detection.

**Files (modified):**
- `cmd/loom-mills-operator/server.go` (or MCP wiring) — register `mills_pipeline_subrun_create` (admin-token only).
- `pkg/mills/store/dao_pipeline.go` — `CreateSubrun(parentID, …)` helper consistent with v1 transactional pattern.

### Slice 6.2 — Worker integration

**Files (modified):**
- `tools/spawn-driver/src/control.ts` — surface `mills_pipeline_subrun_create` as an available tool when running under mills (env `LOOM_MILLS_RUN_ID` set).
- `pkg/mills/pipeline/dispatcher.go` — when a worker creates a subrun, the dispatcher's reconcile next tick picks it up like any other queued pipeline run.

### Slice 6.3 — Telemetry + KPI

**Files (modified):**
- `pkg/mills/metrics.go` — register `loom_mills_pipeline_recursion_depth_histogram`.
- `internal/hud/frontend/src/lib/components/Mills/PipelinesPanel.svelte` — show depth indicator on each run; expand to show parent → child tree.

**Acceptance:** A fixture run exercises depth=1; depth=3 attempt rejected with `recursion_depth_exceeded`; over-budget subrun rejected with `budget_subrun_too_large`.

**Phase 6 quality gate:** `go test ./pkg/mills/pipeline/...`; smoke: dev cluster shows depth-1 subrun in HUD with budget tree honored.

---

## Phase 7 — Adaptive policy + cost preview + mobile parity

### Slice 7.1 — Sunday adaptive policy job

**Files (new):**
- `pkg/mills/adaptive/proposals.go` — reads kpi_snapshots, eval_scores, audit_findings, gate_outcomes; emits proposals; writes `policy_proposals` rows + `.loom/mills/policy_proposals/<date>.md`.
- `pkg/mills/adaptive/proposals_test.go` — fixture-driven tests for relax/tighten/rotate triggers.

**Files (modified):**
- `pkg/mills/scheduler.go` — register Sunday 0500 job.

### Slice 7.2 — REST endpoints (manual apply)

**Files (new):**
- `cmd/loom-mills-operator/handlers_policy_proposals.go` — `GET /api/mills/policy/proposals`, `POST /api/mills/policy/proposals/{id}/apply` (admin).

**Files (modified):**
- `cmd/loom-mills-operator/server.go` — register handlers.

### Slice 7.3 — Cost preview estimator

**Files (new):**
- `pkg/mills/budget/estimator.go` — pre-spawn cost estimate from path-class median + sidecar slice count + ensemble caps + recursion plan.
- `pkg/mills/budget/estimator_test.go`.

**Files (modified):**
- `cmd/loom-mills-operator/handlers_backlog.go` — add `GET /api/mills/cost-preview?backlog_id=` (read-only, no admin token required).

### Slice 7.4 — HUD additions

**Files (new):**
- `internal/hud/frontend/src/lib/components/Mills/PolicyProposalsCard.svelte`.

**Files (modified):**
- `internal/hud/frontend/src/routes/+page.svelte` — append card on Overview.
- `internal/hud/frontend/src/lib/components/Mills/BacklogPanel.svelte` — render cost preview per backlog item with confidence band.

### Slice 7.5 — Mobile Mills screen

**Files (new):**
- `apps/loom-companion-ios/Sources/LoomCompanion/Views/MillsScreen.swift` — three KPI cards + in-flight pipelines list, read-only.
- `apps/loom-companion-ios/Sources/LoomCompanionKit/Mills/MillsAPI.swift` — minimal client over existing operator REST.
- `apps/loom-companion-ios/Tests/LoomCompanionTests/MillsScreenTests.swift`.

**Files (modified):**
- `apps/loom-companion-ios/Sources/LoomCompanion/Navigation/RootTabs.swift` — add Mills tab.
- `apps/loom-companion-ios/project.yml` — pick up new files; run `make mobile-ios-project-sync` per memory `iOS Companion App (xcodeproj)`.

**Acceptance:** Build + run on iPhone 17 simulator; live data from operator dev cluster; pull-to-refresh works.

**Phase 7 quality gate:** `go test ./pkg/mills/adaptive/... ./pkg/mills/budget/...`; HUD + mobile build green; one fixture proposal applied via admin API and reflected in policy ConfigMap.

---

## Phase 8 — Hardening + v2.0 default-on flip

### Slice 8.1 — Cross-cutting smoke + chaos pass

- E2E test against dev k3s cluster: enable squads, run a HUD-frontend backlog item end-to-end with audit + cost preview + (optional) cross-repo (loom-core + loom).
- Inject failure cases: cross-repo timeout, audit pool partial outage, recursion depth violation, debate budget exhaustion, adaptive proposal applied + reverted.
- Capture results in `.loom/95-mills-v2-soak-results-2026-MM-DD.md`.

### Slice 8.2 — Rollback playbook

**Files (new):**
- `docs/MILLS_V2_ROLLBACK.md` — step-by-step: disable a feature flag, revert a policy proposal, restore canonical DB from MinIO backup.

**Files (modified):**
- `docs/MILLS_RUNBOOK.md` — link rollback playbook; add v2-specific scenarios.

### Slice 8.3 — Default-on flips

After 1-week soak each, in this order:

1. `policy.squads.enabled: true` — squads route 30%+ of items.
2. `policy.audit.enabled: true` (already advisory-only by default) — audits run on every artifact + merge.
3. `policy.council.debate.enabled.incident: true` (already default) — confirm metrics.
4. `policy.cross_repo.enabled: true` — only after 3 successful dogfood atomic merges.
5. `policy.adaptive_policy.enabled: true` — manual-apply only.

### Slice 8.4 — Docs final

**Files (modified):**
- `docs/MILLS.md` — append v2 architecture, features, KPIs, acceptance criteria.
- `docs/MILLS_RUNBOOK.md` — append squad debug, audit triage, cross-repo abort, debate inspect.
- `mcp/skills/mills-ops/SKILL.md` — add v2 flow examples.

**Phase 8 quality gate:** All 10 v2 success criteria from spec §"Success criteria" green over a 4-week measurement window. v1 acceptance criteria remain green.

---

## Cross-cutting test plan

| Test | What it covers |
|---|---|
| `pkg/mills/store/...` migration tests | `002_v2.sql` upgrade path |
| `pkg/mills/squads/...` unit + integration | manifest load, routing, planner, outcome recording |
| `pkg/mills/audit/...` tests | rubric loading, pool dispatch + escalation, follow-up creation |
| `pkg/mills/crossrepo/...` tests | atomic merge happy path + revert path |
| `pkg/mills/council/debate_test.go` | 2-round + 3-round + early-exit-on-budget |
| `pkg/mills/pipeline/recursion_test.go` | depth cap, budget tree, cycle detection |
| `pkg/mills/adaptive/...` tests | proposal generation triggers; round-trip via REST apply endpoint |
| `pkg/mills/budget/estimator_test.go` | confidence computation; ±30% target on fixture data |
| `cmd/loom-mills-operator/handlers_*_test.go` | all new endpoints; admin-token gate; healthz/readyz unchanged |
| `internal/hud/frontend/...` Svelte tests | `SquadsPanel`, `AuditPanel`, `CrossRepoCard`, `PolicyProposalsCard` |
| iOS `MillsScreenTests.swift` | mobile screen renders against fake operator |
| **Cluster smoke** | `flux reconcile` rolls out v2 manifests; squads visible in canonical store; audit fires on test artifact; cross-repo dogfood produces 1 atomic merge between loom-core + loom |
| **End-to-end fixture** | `pkg/mills/v2_e2e_test.go` (-tags=e2e): squad routes → squad plan → pipeline run → audit fires → merged → policy proposal job picks it up |

Quality gate per slice:
- Go: `go build ./... && go test ./pkg/mills/... ./cmd/loom-mills-operator/... ./cmd/loom/...`
- Frontend: `pnpm --dir internal/hud/frontend typecheck && pnpm --dir internal/hud/frontend build && pnpm --dir internal/hud/frontend test`
- Mobile: `xcodebuild -project apps/loom-companion-ios/LoomCompanion.xcodeproj -scheme LoomCompanion -sdk iphonesimulator build` (and `make mobile-ios-project-sync` first)
- Cluster: apply manifests via Flux on dev cluster; verify pod Ready and `loom mills squads list` 200
- Sync: `loom sync --all-projects --regen` after any registry change

---

## Parallelization notes

- **Phase 1 slices** — 1.1 blocks 1.2; both file-disjoint from any v1 path.
- **Phase 2 slices** — 2.1 + 2.2 parallel; 2.3 depends on 2.2; 2.4 depends on 2.3; 2.5 + 2.6 depend on 2.4.
- **Phase 3 slices** — 3.1, 3.2 parallel; 3.3 depends on 3.2; 3.4 depends on 3.3; 3.5 depends on 3.4; 3.6 parallel with 3.5.
- **Phase 4 slices** — 4.1 blocks; 4.2, 4.3, 4.4, 4.5 file-disjoint and shippable in parallel after 4.1.
- **Phase 5 slices** — 5.1 blocks 5.2 (persistence depends on data shape); 5.3 parallel with 5.2.
- **Phase 6 slices** — 6.1 blocks; 6.2, 6.3 parallel after 6.1.
- **Phase 7 slices** — 7.1, 7.3, 7.5 file-disjoint and parallel; 7.2 depends on 7.1; 7.4 depends on 7.1 + 7.3.
- **Phase 8** — sequential by design (one feature flip per week).

This plan ships 8 phases over an estimated 9–11 cycles. The first three phases (substrate, squads, audit) are the headline value and account for ~5 cycles; the remaining phases are individually small and largely parallelizable across worktrees.

---

## Default-off rollout

- Every v2 feature ships with its `policy.<feature>.enabled` defaulting **false** (except `audit.advisory_only: true`, which is enabled-but-advisory).
- Phase 8 flips one flag per week with rollback playbook ready.
- Each flip is gated on KPIs from the prior week's soak; no flip happens automatically.
- Kill switch remains the v1 `policy.enabled: false` master flag.

---

## Open items deferred to v2.1

Carried forward from spec §"Out of scope":

1. Auto-apply of `relax` policy proposals with revert window.
2. Audit blocking (currently advisory-only).
3. Cross-repo enabled by default.
4. Per-stage scoped credentials (v1.1 deferral #2).
5. Council "replay" UI for A/B ensemble comparison (v1.1 deferral #6).
6. New squads beyond hud-frontend + gitops (e.g., weaver, mobile, mcp-fabric).
7. Recursion depth > 2 (requires formal verification of cycle classes).

---

## Sources

- `.loom/92-research-mills-v2-hierarchical-swarm-2026-05-02.md`
- `.loom/93-product-spec-mills-v2-hierarchical-swarm-2026-05-02.md`
- `.loom/91-implementation-plan-agent-swarm-council-pipeline-2026-04-25.md`
- `.loom/90-product-spec-agent-swarm-council-pipeline-2026-04-25.md`
- `pkg/mills/store/{store,migrate,types}.go` (current pattern)
- `pkg/mills/policy.go` (fsnotify hot-reload)
- `pkg/mills/pipeline/{runner,dispatcher,integrator}.go`
- `pkg/mills/eval/{outcome_attributor,council_roi,cross_run}.go`
- `pkg/mills/council/{runner,editor,reviewer,artifacts,sidecar}.go`
- `pkg/mills/clients/{flexinfer,gitlab,worktree,handoff,git_merger}.go`
- `cmd/loom-mills-operator/{server,handlers_*}.go`
- `internal/hud/frontend/src/lib/components/Mills/`
- `cmd/mcp-mentatlab/templates/mills-default-pipeline.yaml`
- `platform/gitops/k3s/mills/configmap-policy.yaml` (extend with v2 keys)
- Memory: `iOS Companion App (xcodeproj)` (xcodegen project sync after adding Swift files)
- Memory: `Atomic File Writes for Watched Files` (use `writeFileAtomic` for new artifact writes; `pkg/skills/fileops.go`)
- Memory: `Build Commands` (Go and iOS commands)
- Anthropic Agent SDK: <https://docs.anthropic.com/en/api/agent-sdk/overview>
- OpenAI Codex SDK: <https://platform.openai.com/docs/codex>
- Temporal child workflows: <https://docs.temporal.io/concepts/what-is-a-workflow#child-workflows>
- Argo DAG `dependsOn`: <https://argo-workflows.readthedocs.io/en/latest/walk-through/dag/>
