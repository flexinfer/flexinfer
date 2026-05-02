# Loom Hive — Operator Reference

Loom Hive is the cluster-resident meta-orchestration layer above weaver, spawn, and MentatLab. It runs continuous software development with a planning **Council** that emits `.loom/` artifacts + backlog deltas and a deterministic, gated execution **Pipeline** that turns each backlog item into a merged change. "CI above CI for agents."

This document is the architecture and operator reference for Hive v1. For day-2 procedures (pause/resume, recovery, replay, rollout staging), see `docs/HIVE_RUNBOOK.md`. The active forward-looking design is `.loom/92-…hive-v2…md` through `.loom/94-…hive-v2…md`.

## High-Level Architecture

```mermaid
flowchart LR
  subgraph Mac[Mac client]
    CLI["loom hive ..."]
  end

  subgraph K3s[k3s cluster]
    Op["loom-hive-operator (Deployment)"]
    PVC[("Longhorn PVC<br/>/var/lib/loom-hive")]
    CM[("ConfigMap<br/>policy.yaml")]
    Op --- PVC
    Op --- CM

    subgraph Calls[Operator calls these existing primitives]
      Mentatlab["mcp-mentatlab"]
      Weaver["weaver router"]
      Spawn["spawn controller"]
      AgentCtx["mcp-agent-context"]
      Gitlab["mcp-gitlab"]
      Git["mcp-git"]
      Devbox["mcp-devbox"]
      FlexInfer["FlexInfer"]
    end

    Op --> Mentatlab
    Op --> Weaver
    Op --> Spawn
    Op --> AgentCtx
    Op --> Gitlab
    Op --> Git
    Op --> Devbox
    Op --> FlexInfer
  end

  CLI -- "REST + admin token" --> Op
  HUD["loom hud (frontend)"] -- "REST" --> Op
  Mac --- HUD
```

The Mac is read-mostly; the operator pod is the only writer. SQLite + WAL + Longhorn RWO PVC is the canonical store. `.loom/backlog/*.yaml` is a derived export; GitLab issues are a federated mirror.

## Components

| Component | Source | Purpose |
|---|---|---|
| **Operator binary** | `cmd/loom-hive-operator/` | Process lifecycle, REST + MCP server, healthz/readyz, /metrics. |
| **Canonical store** | `pkg/hive/store/` | SQLite + WAL; migrations under `pkg/hive/store/migrations/`. DAOs per surface (`dao_backlog.go`, `dao_council.go`, `dao_pipeline.go`, `dao_eval.go`, …). |
| **Policy + budget** | `pkg/hive/policy.go`, `pkg/hive/policy_manager.go`, `pkg/hive/budget.go` | YAML policy with fsnotify hot-reload; per-tier rolling budgets. |
| **Reconciler + scheduler** | `pkg/hive/reconciler.go`, `pkg/hive/scheduler.go` | 60s tick (idle-throttled to 5min); cron + event triggers. |
| **Council** | `pkg/hive/council/` | `roadmap.go` extractor, `brief.go` assembler, `reviewer.go` dispatcher, `editor.go`, `artifacts.go`, `backlog_mutator.go`. |
| **Pipeline** | `pkg/hive/pipeline/` | `runner.go` per-DAG engine, `dispatcher.go` per-stage workers, `integrator.go` fan-out, `escalate.go` handoff path, `recursion.go` (v2). |
| **Gates** | `pkg/hive/gates/` | Pure-Go gates (`diff_size`, `scope`, `path_policy`, `secret_scan`, `commit_format`) and LLM-judged gates (`spec_conformance`, `pr_self_review`, `regression`). |
| **Eval** | `pkg/hive/eval/` | Loop A (artifact judge), Loop B (per-merge attribution + Council ROI), Loop C (cross-run consistency). |
| **Clients** | `pkg/hive/clients/` | Wrappers for FlexInfer, GitLab, Git branch merger, MCP hub (devbox/handoff/worktree), HUD spawn API. |
| **MentatLab template** | `cmd/mcp-mentatlab/templates/hive-default-pipeline.yaml` | Default DAG: `plan_slice → research → implement → tests → pr_self_review → mr → ci_watch → merge → cleanup`. |
| **HUD `Hive` view** | `internal/hud/frontend/src/lib/components/Hive/` | Four panels: `CouncilPanel`, `PipelinesPanel`, `BacklogPanel`, `EvalPanel`. |
| **Mac CLI** | `cmd/loom/cmd_hive*.go` | `loom hive status`, `loom hive council {dryrun,run}`, `loom hive backlog {list,sync}`, `loom hive eval list`, `loom hive pipelines list`. |

## Deployment

The operator runs as a single-replica Deployment in the `loom-hive` namespace on k3s.

| Manifest | Purpose |
|---|---|
| `platform/gitops/k3s/hive/namespace.yaml` | `loom-hive` namespace |
| `platform/gitops/k3s/hive/serviceaccount.yaml` + `role.yaml` + `rolebinding.yaml` | RBAC for cross-namespace secret read (`cluster-agent-auth`, `cluster-agent-api-keys`) and own-namespace ConfigMap patch |
| `platform/gitops/k3s/hive/pvc.yaml` | `hive-state` Longhorn RWO 5Gi |
| `platform/gitops/k3s/hive/deployment.yaml` | Deployment with amd64 `nodeSelector`, liveness/readiness probes, `imagePullSecrets: [harbor-creds]` |
| `platform/gitops/k3s/hive/service.yaml` | ClusterIP for in-cluster MCP/REST |
| `platform/gitops/k3s/hive/configmap-policy.yaml` | Mounted at `/etc/loom-hive/policy.yaml` |
| `platform/gitops/k3s/hive/servicemonitor.yaml` | Prometheus scrape |
| `platform/gitops/k3s/hive/cronjob-backup.yaml` | Nightly SQLite dump → MinIO `loom-hive-backups/` |

Bring-up:

```bash
# From the platform/gitops repo:
flux reconcile kustomization apps -n flux-system
kubectl get deploy -n loom-hive loom-hive-operator
kubectl logs -n loom-hive deploy/loom-hive-operator --tail=200
```

Healthz and readyz live on the metrics listener (`:9090` by default); `/healthz` returns 200 once the process is up, `/readyz` flips to 200 only after migrations + policy load + initial reconciler tick complete.

## Configuration

Environment variables (canonical prefix `LOOM_HIVE_*`); see `cmd/loom-hive-operator/config.go` for the authoritative list.

| Var | Default | Purpose |
|---|---|---|
| `LOOM_HIVE_DB_PATH` | `/var/lib/loom-hive/state.db` | SQLite path (Longhorn-backed). |
| `LOOM_HIVE_POLICY_PATH` | `/etc/loom-hive/policy.yaml` | YAML policy; fsnotify hot-reloaded. |
| `LOOM_HIVE_HTTP_ADDR` | `:8090` | REST + MCP listener. |
| `LOOM_HIVE_METRICS_ADDR` | `:9090` | `/healthz`, `/readyz`, `/metrics`. |
| `LOOM_HIVE_REPO_ROOT` | `/workspace/loom-core` | Council artifact write root + brief reader root. |
| `LOOM_HIVE_ENABLED` | unset | `true`/`false` overrides the policy's `enabled` bit; unset defers to the YAML. |
| `LOOM_HIVE_DEBUG` | unset | Enables debug-level slog. |
| `FLEXINFER_PROXY_URL` | unset | Required for LLM-judged gates and the research stage. |
| `FLEXINFER_TOKEN` / `FLEXINFER_JUDGE_MODEL` / `FLEXINFER_WEAVER_MODEL` | unset | FlexInfer client tuning. |
| `GITLAB_API_URL` / `GITLAB_TOKEN` / `GITLAB_PROJECT` | unset | Required for `mr/ci_watch/merge/cleanup` stages and escalation issues. |
| `LOOM_HUD_URL` / `LOOM_HUD_TOKEN` | unset | Required for `plan_slice/implement/pr_self_review` spawn-driven stages. |
| `LOOM_MCP_HUB_URL` / `LOOM_MCP_PROFILE` | unset | Required for devbox/handoff/worktree clients. |
| Admin token | env (operator-side) | Required for mutating endpoints; check `cmd/loom-hive-operator/auth.go`. |

When a backing service env is missing, the operator boots in a degraded mode: affected stages fall back to a NoOp dispatcher and the gap is logged at startup. The reconciler still runs; reads still serve.

## Policy reference

`pkg/hive/policy.go` defines the schema. The on-disk YAML maps 1:1.

```yaml
version: 1
enabled: true                            # kill switch (defaults to enabled when omitted)
budgets:
  council:
    max_usd_per_run:  15.00
    max_usd_per_day:  50.00
  pipeline:
    max_usd_per_run:   5.00
    max_usd_per_day:  75.00
    max_concurrent_runs: 4
    max_runs_per_day:   20
council:
  schedule_cron: "0 5 * * *"
  triggers:
    on_roadmap_change:  true
    on_incident:        true
    on_merge_drift_hours: 48
  ensemble:
    editor:    { name: editor,   model: claude-opus,           backend: spawn }
    reviewers:
      - { name: architect, model: claude-opus,         backend: spawn,     lens: architecture }
      - { name: security,  model: codex-gpt5,          backend: spawn,     lens: security }
      - { name: tech_debt, model: llama-4-70b-instruct, backend: flexinfer, lens: tech_debt }
    judge:     { name: judge,    model: llama-4-70b-instruct,  backend: flexinfer }
  artifacts_branch:           "council/{date}"
  artifacts_merge_strategy:   "fast-merge-loom-only"   # or "always-mr"
pipeline:
  default_template: hive-default-pipeline
  protected_paths:
    - "platform/gitops/**"
    - "cmd/loomd/**"
    - "**/*auth*.go"
    - "**/secret*.yaml"
  per_label_overrides:
    - { label: "auto",         auto_merge: true,  human_review: false }
    - { label: "human_review", auto_merge: false, human_review: true }
  retry:
    max_attempts:     3
    cooldown_seconds: 300
  auto_revert_on_regression: false
human_handoff:
  on_escalation_create_handoff: true
  on_escalation_create_issue:   true
  notify_agent_id: ""
```

Edits to the mounted ConfigMap are picked up via fsnotify within seconds; the operator logs `policy reloaded` on success and continues on the prior version on parse error. In-flight runs continue under the policy they captured at start; new runs use the latest.

## Persistence schema

`pkg/hive/store/migrations/001_initial.sql` is the v1 schema. Key tables:

| Table | Purpose |
|---|---|
| `roadmap_intents` | Themes/priorities/constraints extracted from `ROADMAP.md`; idempotent by content hash. |
| `backlog_items` | Canonical backlog with id `HIVE-YYYY-MM-DD-NNN`, state machine `queued|running|merged|escalated|paused`, label set, priority, spec doc/anchor, success criteria. |
| `council_runs` | Per-run rows: trigger, ensemble snapshot, sidecar JSON, eval verdict, cost. |
| `pipeline_runs` | Per-DAG rows: backlog item, current stage, retry count, integrator parent (when fan-out), MR iid, total cost. |
| `stage_results` | Per-stage row attached to a run: stage id, output JSON, started/ended/cost. |
| `gate_outcomes` | Per-gate verdicts: `pass`/`fail`/`skip` with reasons. |
| `kpi_snapshots` | Rolling KPI samples. |
| `eval_scores` | Loop A artifact scores, Loop B per-merge outcomes, Loop C cross-run findings. Subject types: `council_run`, `pipeline_run`, `cross_run`. |
| `events` | Append-only structured event log used by reconciler + attribution. |

Backups: nightly CronJob dumps `state.db` to MinIO bucket `loom-hive-backups/<UTC>.db`. Retention 30 days.

## Council brief composition

Each Council run assembles a deterministic brief, scoped to ≤16k tokens:

1. `roadmap_intents` (latest snapshot) — themes, priorities, constraints.
2. `.loom/00-index.md` — current planning index.
3. Recent worklog (`.loom/50-worklog.md`) — last 7 days, summarized if too large.
4. `agent-context` recall: `agent_context_recall_enhanced(query="loom-core/roadmap recent")`.
5. Open GitLab issues with hive labels.
6. Recent merged MRs (last 7 days) via `mcp-gitlab`.
7. Alertmanager active alerts via `mcp-alertmanager`.
8. Recent Loki errors via `mcp-loki`.
9. Current KPI snapshot (`kpi_snapshots` latest row).
10. Cross-run findings (Eval Loop C) — most recent contradictions or stale plans.

The brief is wrapped with a fixed system prompt (in `pkg/hive/council/prompts/`); reviewers receive a lens-specific addendum; the editor receives the full brief plus reviewer outputs as tool-result content.

## Pipeline stages

The default DAG is `cmd/mcp-mentatlab/templates/hive-default-pipeline.yaml`:

```
plan_slice → research → implement → tests → pr_self_review → mr → ci_watch → merge → cleanup
                                              ↓
                                     (auto_gate: each transition runs gates)
```

| Stage | Worker backend | Notes |
|---|---|---|
| `plan_slice` | spawn (Claude/Codex) | Reads spec doc + sidecar; emits per-stage list with file/test scope. |
| `research` | weaver (FlexInfer) | Domain-bounded subagent; populates context. |
| `implement` | spawn + worktree | Allocates a per-DAG worktree via `agent_worktree_allocate`; commits with conventional format. |
| `tests` | `devbox_quality_gate` MCP tool | Auto-detects language; fmt → lint → test. |
| `pr_self_review` | spawn (Claude/Codex) | Pre-MR self-review per `mcp/skills/pr-self-review`. |
| `mr` | mcp-gitlab | Opens MR; links backlog issue. |
| `ci_watch` | mcp-gitlab | Polls CI to terminal state; fix-and-retry on red (`ci-failure-recovery` skill). |
| `merge` | mcp-gitlab | Auto-merge if policy allows (label + path policy). |
| `cleanup` | mcp-gitlab + mcp-git | Delete remote branch, release worktree, delete local branch. |

Fan-out: when the council sidecar marks slices as parallel, the runner fans out one sub-run per slice (each with its own worktree); the integrator merges sub-run branches in dependency order. See `pkg/hive/pipeline/integrator.go`.

Escalation: per-issue retry cap from `policy.pipeline.retry.max_attempts` (default 3). On exceed, the escalator opens a GitLab issue with the failure record (stage stack, last 200 lines of worker output, gate verdicts, total cost), creates an `agent-context` handoff, and transitions the canonical row to `escalated`.

## Gate semantics

Gates are evaluated between every stage transition. Pure-Go gates run inline; LLM-judged gates only run when FlexInfer is configured.

| Gate | File | What it checks |
|---|---|---|
| `diff_size` | `pkg/hive/gates/diff_size.go` | Diff line count vs. policy threshold. |
| `scope` | `pkg/hive/gates/scope.go` | Files touched fall inside the sidecar slice's declared scope. |
| `path_policy` | `pkg/hive/gates/path_policy.go` | None of the touched paths match `policy.pipeline.protected_paths`; protected paths force human review. |
| `secret_scan` | `pkg/hive/gates/secret_scan.go` | Heuristic regex scan for tokens/keys/PEMs in the diff. |
| `commit_format` | `pkg/hive/gates/commit_format.go` | Conventional Commits header check. |
| `spec_conformance` | `pkg/hive/gates/spec_conformance.go` | LLM-judged: diff implements the slice as specified. FlexInfer only. |
| `pr_self_review_gate` | `pkg/hive/gates/pr_self_review_gate.go` | LLM-judged: PR matches `pr_self_review_v1` rubric. FlexInfer only. |
| `regression` | `pkg/hive/gates/regression.go` | Post-merge: subscribes to Alertmanager webhook; correlates alert bursts with merges in last 30 minutes. |

Each verdict is persisted to `gate_outcomes` with `pass`/`fail`/`skip` and a `reasons[]` array. A `fail` halts the run at the current stage; the reconciler retries (with cooldown) up to `policy.pipeline.retry.max_attempts`.

## Evaluation framework (Loops A / B / C)

Three independent loops persist scores into `eval_scores`.

| Loop | When | What | Effect |
|---|---|---|---|
| **A — synchronous artifact judge** | Inline at the end of every council run | Schema-validates the sidecar; scores the artifact against `pkg/hive/eval/criteria.go` (validity, slice independence, success-criteria machine-checkability, plan completeness) using a FlexInfer rubric. | Score < 0.7 marks the run `partial`; backlog mutations skipped; artifacts still committed for audit. |
| **B — per-merge outcome attribution** | Async on `pipeline_runs.state→merged` | Computes time-to-merge, retry count, gate-pass-rate; rolls up to a Council ROI score per `council_run_id`. | Records `eval_scores{subject_kind:"pipeline_run"}` and aggregated `eval_scores{subject_kind:"council_run", rubric:"downstream"}`. |
| **C — weekly cross-run consistency** | Sunday 0600 UTC scheduled job | Reads last 7 days of council outputs + merged MRs; flags contradictions, stale plans, repeated gate failures. | Findings appended to next council brief's "watch out for" section. |

The judge model is always FlexInfer; never the frontier (cost + bias control). Rubrics are version-controlled in `pkg/hive/eval/prompts/`.

## REST + MCP surface

Authoritative source: `cmd/loom-hive-operator/handlers_*.go`. All mutating endpoints require the admin token (`Authorization: Bearer …`).

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/hive/status` | Quick state summary. |
| GET | `/api/hive/council/runs` | List council runs with pagination. |
| GET | `/api/hive/council/runs/{id}` | Single run + sidecar. |
| POST | `/api/hive/council/run` | Trigger a council run (admin). |
| POST | `/api/hive/council/dryrun` | Run against a scratch DB; return sidecar + plan paths (admin). |
| GET | `/api/hive/pipeline/runs` | List pipeline runs. |
| GET | `/api/hive/pipeline/runs/{id}` | Detail with stages + gates. |
| POST | `/api/hive/pipeline/runs/{id}/{start,pause,resume,escalate}` | Lifecycle controls (admin). |
| GET | `/api/hive/backlog` | List backlog items. |
| POST | `/api/hive/backlog` | Direct create (admin). |
| POST | `/api/hive/backlog/sync` | Force sync to GitLab (admin). |
| GET | `/api/hive/eval` | Eval scores by subject. |
| POST | `/api/hive/regression/webhook` | Alertmanager webhook target. |
| GET | `/healthz`, `/readyz`, `/metrics` | Operability (no auth). |

MCP tools served by the operator (when MCP listener is enabled): `hive_status`, `hive_council_runs`, `hive_pipeline_runs`, `hive_backlog_list`, `hive_eval_list`. Schemas are auto-generated from the Go handlers.

## CLI

```
loom hive status
loom hive council dryrun
loom hive council run
loom hive backlog list [--state=queued|running|merged|escalated]
loom hive backlog sync
loom hive eval list [--subject=council|pipeline|cross_run]
loom hive pipelines list [--state=running|merged|escalated]
```

`LOOM_HIVE_OPERATOR_URL` (default cluster Service URL) and `LOOM_ADMIN_TOKEN` are honored.

## Telemetry

Every Prometheus metric registered by the operator is in `pkg/hive/metrics.go`. Dashboards live at `platform/gitops/monitoring/dashboards/hive.json`. Headline KPIs:

- `loom_hive_pipeline_cost_usd_total / loom_hive_merge_to_main_total{auto=true}` — cost per merged change.
- `histogram_quantile(0.5, loom_hive_pipeline_stage_duration_seconds)` — slice-to-merge p50.
- `sum(loom_hive_pipeline_gate_decisions_total{outcome=pass}) / sum(loom_hive_pipeline_gate_decisions_total)` — gate pass rate.
- `loom_hive_regression_count_total / loom_hive_merge_to_main_total{auto=true}` — regression rate.
- Council ROI from Eval Loop B (in `eval_scores`, surfaced via HUD).

## Common operator scenarios

- **Trigger a council run on demand.** `loom hive council run` (admin). Outputs paths to new artifacts and the sidecar. Useful when a roadmap change has just landed and you don't want to wait for the daily cron.
- **Dry-run a council change without committing.** `loom hive council dryrun`. Runs the full pipeline against a scratch DB; nothing is committed and no GitLab mutations happen.
- **Pause the pipeline.** Edit ConfigMap to set `enabled: false`; operator hot-reloads; reconciler exits cleanly within one tick. In-flight runs are paused (state preserved). See `docs/HIVE_RUNBOOK.md` for the full procedure.
- **Investigate a failed run.** `loom hive pipelines list --state=escalated`, then `kubectl logs deploy/loom-hive-operator -n loom-hive --since=2h | jq 'select(.run_id=="…")'` for structured slog output.
- **Replay a council run with a different ensemble.** Edit `policy.council.ensemble` (e.g., swap editor model), commit, reconcile. Trigger via `loom hive council run` and compare sidecars in HUD `Eval` panel. (Pre-v2.1; v2.1 adds first-class A/B replay UI.)
- **Investigate eval drift.** HUD `Hive` view → `Eval` panel; sort by score ascending. Cross-reference subjects in `pipeline_runs`/`council_runs` via the link.

## Forward direction (Hive v2)

The v2 design promotes Hive from a flat two-tier system to a true hierarchical swarm with persistent **Squads**, an independent **Adversarial Audit** swarm, **Cross-Repo** atomic merges, **Council Debate Mode**, bounded pipeline **Recursion**, an **Adaptive Policy** engine, **Cost Preview**, and **Mobile Hive parity**. See:

- `.loom/92-research-hive-v2-hierarchical-swarm-2026-05-02.md`
- `.loom/93-product-spec-hive-v2-hierarchical-swarm-2026-05-02.md`
- `.loom/94-implementation-plan-hive-v2-hierarchical-swarm-2026-05-02.md`

## Sources

- `cmd/loom-hive-operator/` (operator binary)
- `pkg/hive/` (~18.2k Go LOC; verified via `wc -l pkg/hive/**/*.go`)
- `cmd/mcp-mentatlab/templates/hive-default-pipeline.yaml`
- `internal/hud/frontend/src/lib/components/Hive/`
- `cmd/loom/cmd_hive*.go`
- `.loom/89-research-agent-swarm-council-pipeline-2026-04-25.md`
- `.loom/90-product-spec-agent-swarm-council-pipeline-2026-04-25.md`
- `.loom/91-implementation-plan-agent-swarm-council-pipeline-2026-04-25.md`
- Anthropic multi-agent research: <https://www.anthropic.com/engineering/built-multi-agent-research-system>
- MCP Streamable HTTP: <https://modelcontextprotocol.io/specification>
- Flux GitOps: <https://fluxcd.io/flux/concepts/>
