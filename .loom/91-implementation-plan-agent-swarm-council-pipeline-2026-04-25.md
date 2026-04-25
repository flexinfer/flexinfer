# Implementation Plan: Loom Hive — Council + Pipeline (v1)

**Date**: 2026-04-25
**Research**: `.loom/89-research-agent-swarm-council-pipeline-2026-04-25.md`
**Product Spec**: `.loom/90-product-spec-agent-swarm-council-pipeline-2026-04-25.md`

## Sequencing summary

The plan splits into **six phases**. Phases 1–3 are the v1 critical path; phases 4–5 are launch-readiness and tightening; phase 6 is hardening + docs.

| Phase | Theme | Outcome | Time estimate |
|---|---|---|---|
| **0** | Prerequisites | Track A multi-turn (`.loom/82-`), AUTH-* (`.loom/87-`) shipped; baseline hive package and policy file | 0–1 cycle (largely already in flight) |
| **1** | Cluster substrate | Persistence layer (SQLite schema + DAO + migrations), `cmd/loom-hive-operator/` deployable to k3s with PVC, ServiceAccount/RBAC, ConfigMap policy, healthz, metrics; CLI shell with `loom hive status` hitting the operator | 2–3 cycles |
| **2** | Hive primitives | MentatLab template `hive-default-pipeline`, gate library v1 (pure-Go gates), reconciler skeleton, MCP/REST surface | 1–2 cycles |
| **3** | Council MVP | Editor + reviewer ensemble (running as cluster spawns), artifact writer, exporter to `.loom/backlog/*.yaml`, GitLab federated sync, dryrun CLI, eval-loop A (synchronous council judge) | 1–2 cycles |
| **4** | Pipeline MVP | Per-stage workers, gate evaluator (pure-Go + LLM-judged), fan-out/in for parallel slices, escalation path, eval-loop B (per-merge attribution) | 2–3 cycles |
| **5** | HUD + telemetry | `Hive` view (4 panels including Eval), KPI cards, Grafana dashboard, Prometheus metrics | 1 cycle |
| **6** | Hardening + docs | Idle-aware throttling, dedup, regression gate, eval-loop C (cross-run consistency), runbook, docs, default-on flip | 1 cycle |

Each phase ships behind `LOOM_HIVE_ENABLED=false` until phase 5 ships. Default-off until phase 6 hardening is green.

---

## Phase 0 — Prerequisites

These are already in flight or recently shipped. Track them, don't redo them.

| Item | Status | Reference |
|---|---|---|
| Multi-turn control plane (`tools/spawn-driver` `--control-file`) | Planned, in `82-` Track A | `.loom/82-plan-headless-agent-fullstack-2026-04-07.md` slices 8a–8c |
| Cluster auth (`cluster-agent-auth`, `cluster-agent-api-keys`) | Planned in `87-`/`88-` | `.loom/87-product-spec-session-spawning-weaver-2026-04-19.md` AUTH-001..AUTH-010 |
| Weaver `SpawnBridge` for non-flexinfer backends | Planned in `87-`/`88-` | `87-` WVR-001..WVR-006 |
| Parent session propagation (`LOOM_PARENT_SESSION_ID`) | Planned in `87-` | `87-` SESS-005 |
| Pipeline-precursor skills (`pr-self-review`, `auto-quality-gate`, `tdd-dev`, `agent-recipes`, `session-retro`, `test-health-inject`) | Shipped per `.loom/78-` | `.loom/78-plan-dark-factory-patterns-2026-04-05.md` |
| `devbox_quality_gate` integration | Shipped 2026-04-14 (`auto_verify` workflow gates) | `ROADMAP.md` "Recently Shipped" |

**Blocker rule**: Pipeline phase 3 depends on Track A multi-turn shipping. Council phase 2 does **not** depend on multi-turn (single-shot spawns are sufficient for council artifact emission).

---

## Phase 1 — Cluster substrate (persistence + operator)

Goal: stand up the always-on cluster operator with its persistent data store, before any hive-specific logic ships. Everything in phase 2+ assumes this is in place.

### Slice 1.0 — Persistence layer (SQLite + DAO)

**Files (new):**
- `pkg/hive/store/store.go` — DB handle, lifecycle, `PRAGMA journal_mode=WAL`, `PRAGMA foreign_keys=ON`
- `pkg/hive/store/migrations/001_initial.sql` — full schema from spec §"Persistence layer §Schema (v1)"
- `pkg/hive/store/migrate.go` — applies migrations using `golang-migrate/migrate/v4` (already a workspace dep; verify) or `pressly/goose`. Decision: prefer `goose` for SQL-only embeds.
- `pkg/hive/store/types.go` — Go types matching the schema rows
- `pkg/hive/store/dao_backlog.go` — CRUD for `backlog_items`; query helpers (`ListByState`, `GetWithDeps`)
- `pkg/hive/store/dao_council.go` — CRUD for `council_runs`
- `pkg/hive/store/dao_pipeline.go` — CRUD for `pipeline_runs`, `stage_results`, `gate_outcomes`
- `pkg/hive/store/dao_kpi.go` — `RecordSnapshot`, `Latest`, `Range`
- `pkg/hive/store/dao_eval.go` — `RecordScore`, `LatestPerSubject`, `Aggregate`
- `pkg/hive/store/dao_events.go` — append-only event log
- `pkg/hive/store/dao_roadmap.go` — CRUD for `roadmap_intents`
- `pkg/hive/store/store_test.go` — table tests using `:memory:` SQLite

**Acceptance:**
- Migrations apply forward and idempotently; `go test -tags=hive_store ./pkg/hive/store/...` runs migrations against `:memory:` and exercises every DAO.
- DAO operations are transactional where multi-row (`StartPipelineRun` writes `pipeline_runs` + `stage_results` initial row in one tx).
- Round-trip tests: insert → query → equal for every type.
- Concurrency: 16-goroutine load test confirms no `database is locked` errors with WAL.

**Independence:** fully new files; no conflicts with anything else.

### Slice 1.1 — Policy + budget primitives

**Files (new):**
- `pkg/hive/policy.go` — `Policy` struct, YAML load + validate, fsnotify hot-reload
- `pkg/hive/policy_test.go`
- `pkg/hive/budget.go` — token + USD accounting per tier with rolling window backed by `kpi_snapshots`
- `pkg/hive/budget_test.go`

**Acceptance:**
- `Policy.Validate()` rejects malformed schemas; rejects unknown labels.
- Hot-reload swaps active policy without restart; in-flight runs continue under their captured policy.
- `Budget.Allow(tier, estimate_usd)` consults the canonical store; rejects when daily cap exceeded.

### Slice 1.2 — `cmd/loom-hive-operator/` skeleton

**Files (new):**
- `cmd/loom-hive-operator/main.go` — process bootstrap, signal handling, `/healthz` + `/readyz` + `/metrics` listeners
- `cmd/loom-hive-operator/server.go` — REST + MCP servers (Streamable HTTP)
- `cmd/loom-hive-operator/config.go` — env + flags (`--db-path`, `--policy-path`, `--listen`, `--metrics-listen`)
- `cmd/loom-hive-operator/main_test.go` — startup/shutdown smoke
- `Dockerfile.loom-hive-operator` (workspace-pattern multi-stage build)
- `Makefile` targets: `build/loom-hive-operator`, `docker/loom-hive-operator`, `test/loom-hive-operator`

**Acceptance:**
- Local: `make build/loom-hive-operator && ./bin/loom-hive-operator --db-path /tmp/hive.db --policy-path testdata/policy.yaml` boots, applies migrations, serves `/healthz` returning 200, `/metrics` returning Prometheus text.
- Docker: image builds cleanly under the `7900xtx` Docker context per workspace `CLAUDE.md`.

### Slice 1.3 — Cluster manifests + GitOps wiring

**Files (new):**
- `platform/gitops/k3s/hive/namespace.yaml` — `loom-hive`
- `platform/gitops/k3s/hive/serviceaccount.yaml` + `role.yaml` + `rolebinding.yaml` — RBAC for cross-namespace secret read (`cluster-agent-auth`, `cluster-agent-api-keys`) and own-namespace ConfigMap patch
- `platform/gitops/k3s/hive/pvc.yaml` — `hive-state` 5Gi Longhorn RWO
- `platform/gitops/k3s/hive/deployment.yaml` — single-replica Deployment with `nodeSelector` (amd64), liveness/readiness probes, resource requests, `imagePullSecrets: [harbor-creds]`
- `platform/gitops/k3s/hive/service.yaml` — ClusterIP for in-cluster MCP/REST
- `platform/gitops/k3s/hive/configmap-policy.yaml` — initial policy from spec §"Hive policy file"
- `platform/gitops/k3s/hive/servicemonitor.yaml` — Prometheus scraping
- `platform/gitops/k3s/hive/cronjob-backup.yaml` — nightly SQLite dump → MinIO
- `platform/gitops/k3s/hive/kustomization.yaml`
- `platform/gitops/k3s/kustomization.yaml` — append `hive/` overlay

**Acceptance:**
- `flux reconcile kustomization apps -n flux-system` rolls out the operator.
- Pod becomes Ready within 60s; `kubectl logs deploy/loom-hive-operator -n loom-hive` shows migrations applied.
- Pod restart resumes (SQLite WAL replays); no data loss.
- Backup CronJob run produces a `loom-hive-backups/<ts>.db` object in MinIO.

### Slice 1.4 — `loom hive` CLI shell (Mac client)

**Files (new):**
- `cmd/loom/cmd_hive.go` — `loom hive status` (initial); points at the operator's REST endpoint via existing admin-token auth
- `cmd/loom/cmd_hive_test.go`

**Files (modified):**
- `cmd/loom/main.go` — register hive subcommands

**Acceptance:**
- `loom hive status` from the Mac hits the cluster operator and prints a one-line summary `{db_ok=true, last_council_at=null, queue_depth=0}`.
- Wrong/missing token surfaces a clear error; honors `LOOM_HIVE_OPERATOR_URL` and `LOOM_ADMIN_TOKEN` env.

**Phase 1 quality gate:** `go build ./... && go test ./pkg/hive/store/... ./pkg/hive/... ./cmd/loom-hive-operator/... ./cmd/loom/...`; cluster smoke: pod reaches Ready, `loom hive status` returns 200.

---

## Phase 2 — Hive primitives (templates, gates, reconciler, MCP/REST)

Goal: pure-substrate logic that doesn't yet run real council/pipeline work. Sets up the surface that phase 3 and 4 fill in.

### Slice 2.1 — MentatLab `hive-default-pipeline` template + gate hooks

**Files (new):**
- `cmd/mcp-mentatlab/templates/hive-default-pipeline.yaml` — DAG with stages from spec §"Pipeline flow template"
- `cmd/mcp-mentatlab/templates/hive_gates.go` — gate registry hookable by the operator over MCP

**Files (modified):**
- `cmd/mcp-mentatlab/flows.go` — load templates from disk on startup; expose `flows_get_template` MCP tool

**Acceptance:**
- `loom flows list-templates` lists `hive-default-pipeline`.
- A dry-run `flows_run` against the template with mock inputs walks all stages without panicking.

### Slice 2.2 — Gate library v1 (pure-Go gates)

**Files (new):**
- `pkg/hive/gates/gates.go` — `Gate` interface; `GateRegistry`
- `pkg/hive/gates/{diff_size,scope,path_policy,secret_scan,commit_format}.go`
- `pkg/hive/gates/{name}_test.go` for each

**Acceptance:**
- Each gate is pure-Go; takes `StageResult + Sidecar` returns `GateOutcome{Pass bool, Reasons []string}`.
- Outcomes are persisted to `gate_outcomes` via the DAO.
- Table tests cover positive + negative cases per gate.

### Slice 2.3 — Reconciler skeleton

**Files (new):**
- `pkg/hive/reconciler.go` — main reconcile loop, ticker, dependency resolver
- `pkg/hive/reconciler_test.go` — table-driven with fake DAO + fake spawn bridge
- `pkg/hive/scheduler.go` — cron + event-driven trigger source for council; tick source for pipeline

**Files (modified):**
- `cmd/loom-hive-operator/main.go` — start the reconciler when `LOOM_HIVE_ENABLED=true`

**Acceptance:**
- Reconciler runs at 60s cadence (tickable for tests); reads `backlog_items` where `state=queued`; respects dependencies; skips when budget exhausted; logs structured events to `events` table.
- Disabled by default; `LOOM_HIVE_ENABLED=true` activates.

### Slice 2.4 — REST + MCP surface (full)

**Files (new/modified):**
- `cmd/loom-hive-operator/server.go` — register all endpoints from spec §"REST + MCP surface"
- `cmd/loom-hive-operator/handlers_*.go` — one file per surface (`handlers_council.go`, `handlers_pipeline.go`, `handlers_backlog.go`, `handlers_status.go`, `handlers_eval.go`)
- `cmd/loom-hive-operator/handlers_*_test.go`

**Acceptance:**
- All read-only endpoints return DAO-backed responses.
- Mutating endpoints (`POST /api/hive/council/run`, `/pipeline/runs/.../start|pause|resume|escalate`, `/backlog/sync`) require admin token; smoke tests assert 401 without token.

**Phase 2 quality gate:** `go build ./... && go test ./pkg/hive/... ./cmd/loom-hive-operator/... ./cmd/mcp-mentatlab/...`

---

## Phase 3 — Council MVP

Goal: a working council that produces real artifacts on demand and on cron, with synchronous artifact-eval gating backlog mutations.

### Slice 3.1 — Roadmap intent extractor

**Files (new):**
- `pkg/hive/council/roadmap.go` — reads `ROADMAP.md`, segments by section, extracts themes/priorities/constraints, upserts into `roadmap_intents` (idempotent by `(theme, summary)` hash)
- `pkg/hive/council/roadmap_test.go`

**Acceptance:**
- Two consecutive runs against the same `ROADMAP.md` produce 0 row changes.
- A diff edit to `ROADMAP.md` produces only the rows whose content actually changed; `last_seen_in_roadmap_sha` updates.

### Slice 3.2 — Council brief assembler

**Files (new):**
- `pkg/hive/council/brief.go` — pulls `roadmap_intents`, `.loom/00-index.md`, recent backlog state from canonical store, recent merges via `mcp-git`/`mcp-gitlab`, alerts via `mcp-alertmanager`, recent Loki errors, current KPIs from `kpi_snapshots`
- `pkg/hive/council/brief_test.go` (mocks all upstreams)

**Acceptance:**
- `BuildBrief()` returns a deterministic markdown string for a fixed input snapshot.
- Brief is < 16k tokens; oversized inputs truncated by section age.

### Slice 3.3 — Reviewer dispatcher

**Files (new):**
- `pkg/hive/council/reviewer.go` — wraps weaver + spawn for FlexInfer + Claude/Codex reviewer agents (running as cluster spawns, not Mac processes)
- `pkg/hive/council/reviewer_test.go`

**Behavior:**
- Reads `policy.council.ensemble.reviewers`; for each reviewer:
  - If `backend == "flexinfer"`: dispatch via existing `pkg/weaver/router.go`
  - Else: spawn via the daemon's spawn controller with multi-turn enabled (cluster auth from `cluster-agent-{auth,api-keys}`); pass the lens-specific brief addendum
- Collects outputs with per-reviewer timeout and budget cap.

### Slice 3.4 — Editor + artifact writer

**Files (new):**
- `pkg/hive/council/editor.go` — runs the editor as a multi-turn spawn (Claude default); receives reviewer outputs as tool-result content
- `pkg/hive/council/artifacts.go` — writes markdown + sidecar; opens `council/<date>` branch; commits via `mcp-git`
- `pkg/hive/council/artifacts_test.go`

**Acceptance:**
- Editor produces a sidecar JSON conforming to spec §"Council artifact sidecar".
- Markdown files use the next free `.loom/<NN>-…md` index.
- Atomic writes via `writeFileAtomic` (memory: `Atomic File Writes for Watched Files`).

### Slice 3.5 — Eval Loop A: synchronous council artifact judge

**Files (new):**
- `pkg/hive/eval/judge.go` — FlexInfer call with the fixed `pkg/hive/eval/prompts/council_artifact_judge.md` rubric
- `pkg/hive/eval/sidecar_schema.json` — JSON Schema for sidecar validation
- `pkg/hive/eval/scoring.go` — combines schema validation + LLM rubric → `eval_scores` row
- `pkg/hive/eval/judge_test.go` (golden inputs)

**Acceptance:**
- Score < 0.7 → council run marked `partial`; backlog mutations skipped; artifacts still committed for audit.
- Schema-invalid sidecars score 0 on the validity criterion automatically (no LLM call needed for that criterion).
- Judge always uses FlexInfer (configured model from `policy.council.ensemble.judge`); never the frontier.

### Slice 3.6 — Council backlog mutator (canonical-first)

**Files (new):**
- `pkg/hive/council/backlog_mutator.go` — translates sidecar `backlog_deltas` into canonical-store writes first, then GitLab sync, then YAML export
- `pkg/hive/council/backlog_mutator_test.go`

**Acceptance:**
- Per-run cap enforced (≤ 10 new items in v1).
- GitLab outage: canonical writes succeed; sync queued; pending state visible via `loom_hive_gitlab_sync_lag_seconds`.
- Round-trip test: canonical → YAML export → GitLab create → canonical update → equal.

### Slice 3.7 — Cron + event triggers

**Files (modified):**
- `pkg/hive/scheduler.go` — add cron parser; poll for `ROADMAP.md` changes via `mcp-git`; subscribe to Alertmanager webhook (or poll `mcp-alertmanager`) for incidents

**Acceptance:**
- Cron `0 5 * * *` triggers a council run.
- A push to `main` that changes `ROADMAP.md` triggers a council run within 5 minutes.
- Triggers individually disable-able via `policy.council.triggers.*: false`.

### Slice 3.8 — `loom hive council` CLI extensions

**Files (modified):**
- `cmd/loom/cmd_hive.go` — add `loom hive council dryrun`, `loom hive council run`, `loom hive backlog list/sync`, `loom hive eval list`

**Acceptance:**
- `loom hive council dryrun` from the Mac triggers an operator-side dryrun; operator runs the full council pipeline against a scratch DB; returns sidecar JSON + plan paths to the CLI; no commits, no GitLab mutations.
- Cost printed at end (frontier + local).

**Phase 3 quality gate:** dryrun produces a valid sidecar + 3 markdown docs in < 8 minutes for < $5 with eval score ≥ 0.7 (acceptance criterion 3 + part of 4 from spec §"Success criteria").

---

## Phase 4 — Pipeline MVP

Goal: pick up backlog items and ship merges; wire eval-loop B for per-merge attribution.

### Slice 4.1 — Pipeline run engine

**Files (new):**
- `pkg/hive/pipeline/runner.go` — drives a backlog item through MentatLab `hive-default-pipeline`; persists `pipeline_runs` + `stage_results` rows to canonical store
- `pkg/hive/pipeline/runner_test.go` (fake gate registry + fake spawn bridge + in-memory DAO)

**Acceptance:**
- Given a backlog item, runner creates a MentatLab run, advances stages, evaluates gates between, persists state transitions transactionally.
- Operator restart: an in-progress run is resumed from `pipeline_runs.current_stage` on next tick.

### Slice 4.2 — Per-stage worker dispatcher

**Files (new):**
- `pkg/hive/pipeline/dispatcher.go` — picks the right worker for a stage:
  - `plan-slice`, `pr-self-review`, `failure-escalation` → spawn (low-budget Claude/Codex)
  - `research` → weaver subagent (codebase domain)
  - `implement` → spawn with worktree allocation
  - `tests` → `devbox_quality_gate` tool call
  - `mr`, `ci-watch`, `merge`, `cleanup` → tool calls (`mcp-gitlab`, `mcp-git`)
- `pkg/hive/pipeline/dispatcher_test.go`

**Acceptance:**
- Dispatcher injects `LOOM_PARENT_SESSION_ID`, `LOOM_HIVE_RUN_ID`, `LOOM_HIVE_STAGE`, `LOOM_HIVE_BACKLOG_ID` into spawn requests.
- Worker budgets populated from `backlog_items.budget_json`.

### Slice 4.3 — Fan-out / fan-in for parallel slices

**Files (new):**
- `pkg/hive/pipeline/integrator.go` — per-slice sub-runs (one worktree each via `agent_worktree_allocate`); conflict-resolution stage merging sub-run branches
- `pkg/hive/pipeline/integrator_test.go`

**Acceptance:**
- Sidecar `slices` with independent file lists run concurrently.
- Integrator auto-merges or escalates on conflict.

### Slice 4.4 — Escalation + handoff

**Files (new):**
- `pkg/hive/pipeline/escalate.go` — on retry-cap exceed: open GitLab issue with failure record (stages + costs + last logs); call `agent_handoff_create`; canonical-store transition to `escalated`
- `pkg/hive/pipeline/escalate_test.go`

**Acceptance:**
- Failure record includes stage stack, last 200 lines of worker output, gate verdicts, total cost so far.
- Reconciler skips escalated items until human edits unblock them (YAML edit re-imported via desired-state diff).

### Slice 4.5 — LLM-judged gates (FlexInfer-only)

**Files (new):**
- `pkg/hive/gates/spec_conformance.go` — strict rubric prompt; FlexInfer call (no frontier)
- `pkg/hive/gates/pr_self_review_gate.go`
- `*_test.go`

**Acceptance:**
- LLM gates always use FlexInfer; never frontier.
- Failures reported with `reasons[]` from rubric output, persisted in `gate_outcomes`.

### Slice 4.6 — Eval Loop B: per-merge outcome attribution

**Files (new):**
- `pkg/hive/eval/outcome_attributor.go` — listens for `pipeline_runs.state→merged`; computes time-to-merge, retry count, gate-pass-rate; records `eval_scores{subject_kind:"pipeline_run"}`
- `pkg/hive/eval/council_roi.go` — daily aggregation per `council_run_id` → `eval_scores{subject_kind:"council_run", rubric:"downstream"}`
- `*_test.go`

**Acceptance:**
- For each merge, exactly one `pipeline_run` eval row is written.
- Council ROI rows aggregate correctly across multiple pipeline runs sharing a `council_run_id`.

### Slice 4.7 — Wire pipeline runs into the reconciler

**Files (modified):**
- `pkg/hive/reconciler.go` — invoke `pipeline.Runner` on queued items; honor `policy.budgets.pipeline.max_concurrent_runs` and `max_runs_per_day`

**Acceptance:**
- End-to-end test (fake gitlab + fake spawn): a backlog item with `state: queued` and `auto: true` ends as `state: merged` with referenced `mr_iid` and a populated outcome eval row.

**Phase 4 quality gate:** acceptance criteria 5, 6, 7 from spec §"Success criteria" pass against a fixture repo.

---

## Phase 5 — HUD + telemetry

### Slice 5.1 — Prometheus metrics

**Files (new):**
- `pkg/hive/metrics.go` — register all metrics from `.loom/89-` §8

**Acceptance:**
- Each metric increments where stated; smoke test asserts label cardinality is bounded.

### Slice 5.2 — `Hive` HUD view (4 panels)

**Files (new):**
- `internal/hud/frontend/src/routes/hive/+page.svelte`
- `internal/hud/frontend/src/lib/components/Hive/{CouncilPanel,PipelinesPanel,BacklogPanel,EvalPanel}.svelte`

**Files (modified):**
- `internal/hud/frontend/src/lib/nav.ts` — add `Hive` to top-level views
- `internal/hud/domain/hive/proxy.go` — HUD-side proxy that forwards `/api/hive/*` to the cluster operator (admin-token from existing config)

**Acceptance:**
- Four panels render via existing primitives (`PanelShell`, `DataTable`, `DetailDrawer`).
- Eval panel renders the score trends, top-3 contradictions (from Loop C), stale plans.

### Slice 5.3 — KPI cards on Overview

**Files (modified):**
- `internal/hud/frontend/src/routes/+page.svelte` — append metric card row
- `internal/hud/frontend/src/lib/components/MetricCard.svelte` — reuse existing primitive

**Acceptance:**
- Five KPIs render with trend arrows where applicable.

### Slice 5.4 — Grafana dashboard

**Files (new):**
- `platform/gitops/monitoring/dashboards/hive.json` — KPIs / cost / per-stage durations / gate pass rates / eval scores

**Acceptance:** imports cleanly; queries pull from the new metrics namespace.

**Phase 5 quality gate:** acceptance criteria 8, 9, 10 from spec.

---

## Phase 6 — Hardening + docs

### Slice 6.1 — Idle-aware throttling

**Files (modified):**
- `pkg/hive/reconciler.go` — backoff to 5-minute cadence when no queued items > X minutes; resume 60s on event

**Acceptance:** cluster GPU utilization drops to baseline when no queued items remain.

### Slice 6.2 — Canonical-store dedup

**Files (modified):**
- `pkg/hive/council/backlog_mutator.go` — title-similarity dedup (Jaccard over normalized tokens) against `backlog_items` (canonical, not GitLab); skip-or-update existing

**Acceptance:** running the council twice with the same brief produces 0 new items on the second run.

### Slice 6.3 — Regression gate (post-merge)

**Files (new):**
- `pkg/hive/gates/regression.go` — subscribes to Alertmanager webhook; correlates with merge events via canonical store; emits `loom_hive_regression_count_total`

**Files (modified):**
- `pkg/hive/policy.go` — add `policy.pipeline.auto_revert_on_regression: false` (default off)

**Acceptance:** alert burst within 30 min of an auto-merge increments the metric; auto-revert opens a revert MR if the policy flag is true.

### Slice 6.4 — Eval Loop C: weekly cross-run consistency

**Files (new):**
- `pkg/hive/eval/cross_run.go` — scheduled job (Sunday 0600) reads last 7 days of council outputs + merged MRs; flags contradictions, stale plans, repeated gate failures
- `pkg/hive/eval/cross_run_test.go`

**Files (modified):**
- `pkg/hive/scheduler.go` — register the cross-run job
- `pkg/hive/council/brief.go` — append the latest cross-run findings to the next council brief

**Acceptance:** running against a fixture of 7 days of plans flags ≥ 1 known contradiction; flagged items appear in next-day council brief.

### Slice 6.5 — Docs + runbook

**Files (new):**
- `docs/HIVE.md` — architecture, cluster deployment, policy reference, council brief composition, pipeline stages, gate semantics, persistence schema, eval rubric, common operator scenarios
- `docs/HIVE_RUNBOOK.md` — pause/resume, force-escalate, replay a council run, audit a merge, recover-from-corrupted-DB
- `mcp/skills/hive-ops/` — new skill documenting `loom hive` flows (`status`, `pause`, `resume`, `kpis`, `force-escalate`)

**Acceptance:** acceptance criterion 12 from spec.

### Slice 6.6 — Default-on flip + production rollout playbook

**Files (modified):**
- `cmd/loom-hive-operator/main.go` — flip `LOOM_HIVE_ENABLED` default to true; kill switch via `policy.enabled: false` remains
- `docs/HIVE_RUNBOOK.md` — append "production rollout staging" section (local → dev cluster → production)

**Acceptance:** all 12 spec acceptance criteria green; production opt-in is a one-line policy edit.

**Phase 6 quality gate:** spec §"Success criteria" all green end-to-end on a clean cluster.

---

## Cross-cutting test plan

| Test | What it covers |
|---|---|
| `pkg/hive/store/...` table tests | DAO operations against `:memory:` SQLite; migration up/down; concurrency under WAL |
| `pkg/hive/...` unit tests | Policy, budget, gates, reconciler logic |
| `pkg/hive/council/...` integration tests | Roadmap extractor, brief, reviewer dispatch, editor, judge with fake spawn + fake weaver |
| `pkg/hive/pipeline/...` integration tests | Per-stage flow with fake gate registry + fake spawn bridge + fake gitlab |
| `pkg/hive/eval/...` tests | Loops A, B, C with golden inputs |
| `cmd/loom-hive-operator/...` HTTP + MCP tests | All endpoints; admin-token gating; healthz/readyz; metrics shape |
| `internal/hud/frontend/...` Svelte tests | Four `Hive` panels render with mock fixtures |
| **Cluster smoke** | `flux reconcile` rolls out the operator on a dev k3s cluster; pod reaches Ready; `loom hive status` from Mac returns 200; SQLite migrations applied; backup CronJob completes once successfully |
| **End-to-end fixture** | Council → backlog item → pipeline → merged MR using a tmp git repo + fake GitLab + fake spawn driver. Lives in `pkg/hive/e2e_test.go`, gated by `-tags=e2e` |

Quality gate per slice:
- Go: `go build ./... && go test ./pkg/hive/... ./cmd/loom-hive-operator/... ./cmd/mcp-mentatlab/... ./internal/hud/... ./cmd/loom/...`
- Frontend (phase 5): `pnpm --dir internal/hud/frontend typecheck && pnpm --dir internal/hud/frontend build && pnpm --dir internal/hud/frontend test`
- Cluster (phases 1, 2, 4, 6): apply manifests via Flux on a dev cluster; verify pod Ready and `loom hive status` 200
- Sync: after any registry change, `loom sync --all-projects --regen`

---

## Parallelization notes

- **Phase 1 slices 1.0, 1.1, 1.2, 1.3, 1.4** — 1.0 (persistence) blocks 1.1–1.4 because they all consume the DAO. After 1.0 lands, 1.1 + 1.2 + 1.3 are file-disjoint and can ship in parallel; 1.4 (CLI) depends on 1.2 (operator with REST handlers).
- **Phase 2 slices 2.1, 2.2, 2.3, 2.4** — 2.1, 2.2 independent; 2.3 depends on 2.2; 2.4 depends on 2.3.
- **Phase 3 slices 3.1, 3.2, 3.3, 3.4, 3.5, 3.6, 3.7, 3.8** — 3.1, 3.2 are independent; 3.3 depends on 3.2; 3.4 depends on 3.3; 3.5 is independent (judge); 3.6 depends on 3.4 + 3.5; 3.7 depends on 3.6; 3.8 depends on operator + 3.6.
- **Phase 4 slices 4.1–4.7** — 4.1, 4.2 must land first; 4.3, 4.4, 4.5, 4.6 are file-disjoint and parallel-shippable; 4.7 depends on all.
- **Phase 5 slices 5.1–5.4** — file-disjoint, fully parallel.
- **Phase 6 slices 6.1–6.6** — file-disjoint except 6.5 referencing slice content, can ship in parallel.

---

## Default-off rollout

- Phases 1–4 ship with `LOOM_HIVE_ENABLED` defaulting **false** in `cmd/loom-hive-operator/config.go`.
- Phase 5 ships HUD; panels show a "Hive disabled" empty state when the env is off.
- Phase 6 slice 6.6 flips the default to **true**; production rollout is operator-driven via `policy.enabled: false` kill switch.
- Kill switch freezes everything regardless of env: reconciler exits cleanly within one tick; in-flight pipeline runs are paused (state preserved in `pipeline_runs`).

---

## Open items + decisions deferred to v1.1

Carried forward from `.loom/89-` §10 and `.loom/90-` §"Open decisions":

1. Mobile parity for `Hive` view.
2. Per-stage scoped credentials (today: cluster credentials inherited).
3. Cross-repo hive (today: single repo).
4. Council debate mode (multi-round refinement) — currently single-pass editor + reviewers.
5. `auto_revert_on_regression` defaults to false in v1; flip after KPIs prove low-noise (slice 5.3).
6. Council "replay" UI (re-run a brief with a different ensemble for A/B).
7. Sub-sub-agent spawning is explicitly deferred per `.loom/87-` "Out of Scope".
8. Tiered priority queue inside the reconciler (today: FIFO with dependency check).
9. Pre-spawn cost estimate UI surface for human-reviewed items.

---

## Sources

- `.loom/89-research-agent-swarm-council-pipeline-2026-04-25.md`
- `.loom/90-product-spec-agent-swarm-council-pipeline-2026-04-25.md`
- `.loom/82-plan-headless-agent-fullstack-2026-04-07.md` (Track A)
- `.loom/87-product-spec-session-spawning-weaver-2026-04-19.md`
- `.loom/88-implementation-plan-session-spawning-weaver-2026-04-19.md`
- `.loom/78-plan-dark-factory-patterns-2026-04-05.md`
- `pkg/weaver/{router,domain,domain_yaml,spawn_bridge,executor}.go`
- `internal/spawn/{controller,reconciler,types,mentatlab_adapter}.go`
- `internal/hud/spawn.go:879` (`runBudgetWatcher`)
- `cmd/mcp-mentatlab/{flows,agents,runs,main}.go`
- `pkg/agentcontext/svc_workflow_*.go`
- `mcp/context/registry.yaml`
- `mcp/context/skills-registry.yaml`
- `ROADMAP.md`
- Memory: `Atomic File Writes for Watched Files` (use `writeFileAtomic` for `.loom/backlog/*.yaml` and council artifact writes)
