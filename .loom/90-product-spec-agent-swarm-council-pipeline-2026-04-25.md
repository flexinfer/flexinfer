# Product Spec: Loom Hive — Planning Council + Deterministic Execution Pipeline

**Date**: 2026-04-25
**Research**: `.loom/89-research-agent-swarm-council-pipeline-2026-04-25.md`
**Implementation Plan**: `.loom/91-implementation-plan-agent-swarm-council-pipeline-2026-04-25.md`

## Goal

Stand up the meta-orchestration layer above weaver/spawn so Loom can run **continuous software development** with minimal human steering. The system has two tiers:

- **Council** — scheduled planning ensemble that emits durable, version-controlled artifacts (`.loom/` docs, GitLab issues, structured backlog deltas).
- **Pipeline** — continuous, event-driven, gated execution flow that converts each backlog item into a merged change without per-stage human approval.

Together: **Loom Hive** — a cluster-resident control plane (a new `loom-hive-operator` deployment in k3s) that schedules, dispatches, gates, budgets, and reports on autonomous software development. The hive runs always-on in the cluster; the Mac-side `loom` CLI is a client only.

## Non-goals

- Replacing weaver, spawn, or MentatLab. The hive *uses* them; it does not duplicate them.
- Replacing `ROADMAP.md` or human roadmap ownership. Humans still set priorities; the hive proposes, humans dispose.
- Building a new DAG runtime. Pipeline runs are MentatLab flows with new templates and gate types.
- Replacing GitLab as the durable backlog. Issues and MRs live in GitLab; `.loom/backlog/*.yaml` is the working copy synced to GitLab.
- Building a new model router. The hive uses the existing weaver router for inline subagent calls and the spawn bridge for headless work, both already shipped per `.loom/87-` and `.loom/88-`.
- Adding a new auth path. Hive runs reuse `cluster-agent-auth` / `cluster-agent-api-keys` (`.loom/87-` AUTH-*).

## Architecture at a glance

```
                ┌────────────────────────────────────────────┐
   Mac CLI ───► │  loom-hive-operator  (k3s deployment)      │
   (loom hive)  │                                            │
                │  ┌─────────────────┐  ┌─────────────────┐  │
                │  │ Scheduler       │  │ Reconciler      │  │
                │  │  (cron + events)│  │ (desired state) │  │
                │  └────────┬────────┘  └────────┬────────┘  │
                │           │                    │           │
                │           ▼                    ▼           │
                │   ┌────────────────┐  ┌────────────────┐   │
                │   │ Council Runner │  │ Pipeline Runner│   │
                │   │ (ensemble)     │  │ (DAG per item) │   │
                │   └────────┬───────┘  └────────┬───────┘   │
                │            │                   │           │
                │            └────────┬──────────┘           │
                │                     ▼                      │
                │   ┌─────────────────────────────────────┐  │
                │   │  Persistence (canonical)            │  │
                │   │  SQLite @ /var/lib/loom-hive/*.db   │  │
                │   │  WAL mode, k3s PVC, single-writer   │  │
                │   └─────────────────────────────────────┘  │
                │                     │                      │
                │      derive ▼       │       mirror ▼       │
                │   .loom/backlog/*.yaml   GitLab issues     │
                │   (commit on council    (federated)        │
                │    run, git audit)                         │
                │                                            │
                │  Calls (over MCP Streamable HTTP):         │
                │    spawn controller, weaver, mentatlab,    │
                │    agent-context, mcp-gitlab, mcp-git,     │
                │    mcp-auth-refresher (cluster auth)       │
                └────────────────────────────────────────────┘
                                     │
                                     ▼
                k3s cluster (GPU + spawn pods + flexinfer + agent-context)
```

Mac-side: `loom hive ...` CLI hits the operator's REST surface (cluster-local Service or ingress-exposed). Mac never runs the reconciler; safe to close the laptop lid.

## Decisions (carried from research §6, resolved 2026-04-25)

| # | Decision | Choice |
|---|---|---|
| D1 | Council runtime | **Hybrid, cluster-only.** Editor = frontier (Claude/Codex via spawn) running in k3s; reviewers = FlexInfer-backed weaver subagents in k3s. Mac never executes hive logic — operator's MacBook Air sleeps; the hive must not. |
| D2 | Deliverable contract | **Markdown + JSON sidecar.** Sidecar is the machine-readable contract; markdown is the human-readable view. |
| D3 | Backlog representation | **Three tiers.** Canonical = SQLite in cluster; exported = `.loom/backlog/*.yaml` (regenerated, git-tracked); federated = GitLab issues (synced). Resilient to GitLab outages. |
| D4 | Pipeline runtime | **Extend MentatLab** with hive flow templates + new gate hooks. |
| D5 | Worker pickup model | **Reconcile loop.** |
| D6 | Worker isolation | **Per-DAG worktree**; parallel slices fan out via `agent_worktree_allocate`. |
| D7 | Frontier spend | **Per-tier daily $ cap + queue.** |
| D8 | Human override | **Policy-driven** by label + path glob; default-deny new path classes. |
| D9 | Council ensemble | **Heterogeneous** — Claude + Codex + local Qwen via FlexInfer. |
| D10 | Council synthesis | **Editor pattern** — one editor, N reviewers. |
| D11 | Council triggers | **Cron (daily 0500) + roadmap-change + incident**; individually disable-able. |
| D12 | Pipeline failure | **Retry ≤ 3 → escalate via handoff + filed issue with failure record.** |

## Persistence layer

Three-tier model. **Canonical** is the only store the hive writes to in real time; the others are derived projections.

### Tier 1 — Canonical: SQLite in cluster

- Path: `/var/lib/loom-hive/state.db` on a k3s PVC (Longhorn `storageClass: longhorn`, RWO, 5Gi).
- Mode: `PRAGMA journal_mode=WAL` for concurrent reads + durable single-writer.
- Migrations: `pkg/hive/store/migrations/*.sql`; applied at operator startup; `goose` or `golang-migrate` (decision deferred to slice 1.0).
- Ownership: only the `loom-hive-operator` pod writes; HUD reads via the operator's REST API (no direct DB access from outside).
- Backups: nightly dump to MinIO bucket `loom-hive-backups/` via existing `mcp-minio` tools. Retention 30 days.

#### Schema (v1)

```sql
-- Source of intent (extracted/edited from ROADMAP.md)
CREATE TABLE roadmap_intents (
    id          INTEGER PRIMARY KEY,
    theme       TEXT NOT NULL,
    priority    INTEGER NOT NULL,           -- 1=highest
    summary     TEXT NOT NULL,              -- 1-3 sentences
    constraints TEXT,                       -- JSON
    last_seen_in_roadmap_sha TEXT,
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL
);

-- Backlog items (canonical)
CREATE TABLE backlog_items (
    id                  TEXT PRIMARY KEY,           -- HIVE-YYYY-MM-DD-NNN
    gitlab_issue_iid    INTEGER,
    title               TEXT NOT NULL,
    labels              TEXT NOT NULL,              -- JSON array
    state               TEXT NOT NULL,              -- queued|running|merged|escalated|paused
    priority            TEXT NOT NULL,              -- P0|P1|P2|P3
    spec_doc            TEXT,                       -- .loom/ path
    spec_anchor         TEXT,
    success_json        TEXT NOT NULL,              -- structured success block
    budget_json         TEXT NOT NULL,
    policy_json         TEXT NOT NULL,
    slices_json         TEXT NOT NULL,              -- per-slice file/test scope
    dependencies        TEXT,                       -- JSON array of backlog_item.id
    council_run_id      TEXT REFERENCES council_runs(id),
    created_by          TEXT NOT NULL,              -- "council" | "human" | "pipeline_escalation"
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL
);
CREATE INDEX idx_backlog_state ON backlog_items(state);
CREATE INDEX idx_backlog_council ON backlog_items(council_run_id);

-- Council runs
CREATE TABLE council_runs (
    id                  TEXT PRIMARY KEY,           -- COUNCIL-YYYY-MM-DD[-N]
    trigger             TEXT NOT NULL,              -- cron|roadmap|incident|manual
    started_at          TIMESTAMP NOT NULL,
    ended_at            TIMESTAMP,
    outcome             TEXT NOT NULL,              -- success|partial|error|conflict
    cost_frontier_usd   REAL NOT NULL DEFAULT 0,
    cost_local_usd      REAL NOT NULL DEFAULT 0,
    artifacts_json      TEXT NOT NULL,              -- list of {kind,path}
    backlog_deltas_json TEXT NOT NULL,              -- {created:[],updated:[],closed:[]}
    sidecar_json        TEXT NOT NULL,              -- full sidecar
    branch_name         TEXT,                       -- council/<date>
    commit_sha          TEXT,
    notes               TEXT
);

-- Pipeline runs
CREATE TABLE pipeline_runs (
    id                  TEXT PRIMARY KEY,           -- PIPE-<ulid>
    backlog_id          TEXT NOT NULL REFERENCES backlog_items(id),
    template            TEXT NOT NULL,              -- hive-default-pipeline
    state               TEXT NOT NULL,              -- queued|planning|...|done|escalated
    current_stage       TEXT,
    attempts            INTEGER NOT NULL DEFAULT 0,
    worktree_path       TEXT,
    mr_iid              INTEGER,
    started_at          TIMESTAMP NOT NULL,
    ended_at            TIMESTAMP,
    cost_usd            REAL NOT NULL DEFAULT 0,
    parent_session_id   TEXT,                       -- LOOM_PARENT_SESSION_ID
    UNIQUE(backlog_id, attempts)
);
CREATE INDEX idx_pipeline_state ON pipeline_runs(state);

-- Stage results (one per stage execution, idempotent on retry)
CREATE TABLE stage_results (
    id                  INTEGER PRIMARY KEY,
    pipeline_run_id     TEXT NOT NULL REFERENCES pipeline_runs(id),
    stage               TEXT NOT NULL,
    attempt             INTEGER NOT NULL,
    started_at          TIMESTAMP NOT NULL,
    ended_at            TIMESTAMP,
    outcome             TEXT,                       -- success|gate_fail|error
    spawn_id            TEXT,                       -- if stage was a spawn
    cost_usd            REAL DEFAULT 0,
    artifacts_json      TEXT,                       -- stage-specific
    log_tail            TEXT                        -- last ~200 lines
);

-- Gate decisions (one per gate evaluation)
CREATE TABLE gate_outcomes (
    id                  INTEGER PRIMARY KEY,
    pipeline_run_id     TEXT NOT NULL REFERENCES pipeline_runs(id),
    after_stage         TEXT NOT NULL,
    gate_name           TEXT NOT NULL,
    outcome             TEXT NOT NULL,              -- pass|fail|skip
    reasons_json        TEXT,                       -- []string
    judged_by           TEXT,                       -- "go" | "flexinfer:<model>"
    evaluated_at        TIMESTAMP NOT NULL
);

-- KPI snapshots (rolled-up, written every reconcile tick)
CREATE TABLE kpi_snapshots (
    id                  INTEGER PRIMARY KEY,
    snapshot_at         TIMESTAMP NOT NULL,
    window_seconds      INTEGER NOT NULL,           -- 86400, 604800, 2592000
    metrics_json        TEXT NOT NULL               -- {cost_per_merged, p50_latency, gate_pass_rate, ...}
);

-- Evaluation scores (council artifact + pipeline outcome eval)
CREATE TABLE eval_scores (
    id                  INTEGER PRIMARY KEY,
    subject_kind        TEXT NOT NULL,              -- council_run | pipeline_run | cross_run
    subject_id          TEXT NOT NULL,
    rubric              TEXT NOT NULL,
    score               REAL NOT NULL,              -- 0..1
    breakdown_json      TEXT NOT NULL,              -- per-criterion scores
    judged_by           TEXT NOT NULL,              -- "flexinfer:<model>" | "rule"
    evaluated_at        TIMESTAMP NOT NULL,
    notes               TEXT
);

-- Generic event log (audit + debug)
CREATE TABLE events (
    id          INTEGER PRIMARY KEY,
    occurred_at TIMESTAMP NOT NULL,
    actor       TEXT NOT NULL,                      -- "scheduler" | "reconciler" | "editor" | etc.
    kind        TEXT NOT NULL,                      -- council_run.start, gate.fail, ...
    subject_kind TEXT,
    subject_id  TEXT,
    payload_json TEXT
);
CREATE INDEX idx_events_subject ON events(subject_kind, subject_id);
```

### Tier 2 — Exported: `.loom/backlog/*.yaml` and `.loom/hive/runs/<id>.yaml`

- Generated by an `Exporter` after each council run + on `loom hive backlog export` CLI.
- Filename: `.loom/backlog/HIVE-YYYY-MM-DD-NNN.yaml` with the same shape we drafted in v1 (see preserved YAML below).
- Committed to `council/<date>` branch alongside the markdown artifacts.
- Read but not edited by the hive at runtime: a human edit to a YAML file is a desired-state delta that the reconciler applies on next tick (similar to Flux). Conflict resolution: **canonical store wins** for state transitions (`queued` → `running` etc.); **YAML wins** for human-supplied fields (priority, success criteria edits).

### Tier 3 — Federated: GitLab

- Each backlog item with `state != queued || created_by == council` mirrors as a GitLab issue.
- Sync runs every 5 minutes (configurable) and on every state transition.
- GitLab outage tolerance: hive operates fully on canonical store; `gitlab_sync_lag_seconds` metric reports drift; backlog of pending sync ops persists in `events` table.

### Backlog item YAML (Tier 2 export shape, unchanged from v1)

```yaml
id: HIVE-2026-04-25-001          # stable hive id
gitlab_issue_iid: 312             # GitLab issue IID (post-sync)
title: "Refactor SpawnPanel to use shared DataTable"
labels: [debt, hud, auto]
state: queued                     # queued | running | merged | escalated | paused
priority: P2
spec_doc: .loom/91-implementation-plan-…md
spec_anchor: "Slice 4"
dependencies: [HIVE-2026-04-25-000]
slices:
  - name: refactor-table
    files: [internal/hud/frontend/src/lib/components/SpawnPanel.svelte]
    tests: [internal/hud/frontend/src/lib/components/SpawnPanel.test.ts]
    parallel_with: []
success:
  tests: ["pnpm --dir internal/hud/frontend test -- SpawnPanel"]
  metrics: []
  manual_check: ""
budget:
  max_cost_usd: 2.50
  max_turns: 60
  max_pipeline_minutes: 45
policy:
  require_human_review: false
  auto_merge: true
  protected_paths_touched: []
created_by: council
created_at: 2026-04-25T05:01:00Z
council_run_id: COUNCIL-2026-04-25
```

## Concepts and types

(Backlog item shape and DB schema are defined in §"Persistence layer" above.)

### Council artifact sidecar (`.loom/<NN>-…-sidecar.json`)

```json
{
  "council_run_id": "COUNCIL-2026-04-25",
  "models": ["claude-opus-4-7", "gpt-5-codex", "qwen3.5-9b"],
  "started_at": "2026-04-25T05:00:00Z",
  "ended_at":   "2026-04-25T05:14:00Z",
  "cost_usd": { "frontier": 8.42, "local": 0.10 },
  "artifacts": [
    { "kind": "research",            "path": ".loom/89-…md" },
    { "kind": "product_spec",        "path": ".loom/90-…md" },
    { "kind": "implementation_plan", "path": ".loom/91-…md" },
    { "kind": "backlog_create",      "id": "HIVE-2026-04-25-001" }
  ],
  "backlog_deltas": { "created": 4, "updated": 1, "closed": 0 },
  "signals_consumed": ["roadmap@d4d2f389", "alerts@2026-04-24", "merges@7d"],
  "notes": "Editor: Claude. Reviewers: Codex (security lens), Qwen (tech-debt lens)."
}
```

### Pipeline flow template (extension to MentatLab)

A new MentatLab flow template, `hive-default-pipeline`, with stages:

| Stage | Type | Worker | Gate to next |
|---|---|---|---|
| `plan-slice` | spawn (Claude or Codex, low budget) | reads spec + sidecar; emits per-stage scope | sidecar slice list ≥ 1 |
| `research` | weaver subagent (codebase domain) | targeted code reading + recall | search results non-empty |
| `implement` | spawn (Claude default), per-slice in worktree | writes code + tests | files changed ⊆ slice scope |
| `tests` | tool gate (`devbox_quality_gate`) | run fmt/lint/test | exit 0 |
| `pr-self-review` | spawn (low budget) using `pr-self-review` skill | structured checklist (`.loom/78-` Slice 6) | issues==0 or self-fixed |
| `mr` | tool gate (gitlab create_merge_request) | open MR linked to issue | mr_iid set |
| `ci-watch` | tool gate (poll GitLab pipeline) | wait for terminal | success |
| `merge` | tool gate (gitlab merge_merge_request) **policy-checked** | auto-merge per policy | merged_sha set |
| `cleanup` | tool gate | delete remote branch + release worktree + close handoff | done |
| `failure-escalation` | spawn + tool gate | on retry-cap exceed: file failure issue + create handoff | always last |

Stages with `parallel: true` fan out into per-slice sub-flows; an `integrator` stage joins them.

### Hive policy file (`platform/gitops/hive/policy.yaml`, SOPS-encrypted only if it contains tokens)

```yaml
version: 1
budgets:
  council:
    max_usd_per_run: 15
    max_usd_per_day: 50
  pipeline:
    max_usd_per_run: 5
    max_usd_per_day: 75
    max_concurrent_runs: 4
council:
  schedule_cron: "0 5 * * *"           # daily 0500
  triggers:
    on_roadmap_change: true
    on_incident: true
    on_merge_drift_hours: 48           # if no merges in 48h, run a planning round
  ensemble:
    editor:    { model: claude-opus-4-7,       backend: claude-code }
    reviewers:
      - { name: security,   model: gpt-5-codex,         backend: codex }
      - { name: tech-debt,  model: qwen3.5-9b,          backend: flexinfer }
      - { name: user-impact,model: claude-sonnet-4-6,   backend: claude-code }
  artifacts_branch: "council/{date}"
  artifacts_merge_strategy: "fast-merge-loom-only"   # fast-merge .loom/ ; MR for ROADMAP/skills-registry
pipeline:
  default_template: hive-default-pipeline
  per_label_overrides:
    - { label: docs,     auto_merge: true,  human_review: false }
    - { label: debt,     auto_merge: true,  human_review: false }
    - { label: security, auto_merge: false, human_review: true  }
    - { label: critical, auto_merge: false, human_review: true  }
  protected_paths:
    - "platform/gitops/**"
    - "cmd/loomd/**"
    - "**/*auth*.go"
    - "**/secret*.yaml"
  retry:
    max_attempts: 3
    cooldown_seconds: 300
human_handoff:
  on_escalation_create_handoff: true
  on_escalation_create_issue:   true
  notify_agent_id: "claude-code"
```

## Cluster deployment (`loom-hive-operator`)

Manifests live at `platform/gitops/k3s/hive/`. GitOps-reconciled by Flux.

| Resource | Purpose |
|---|---|
| `Namespace: loom-hive` | Isolation; per-namespace RBAC |
| `Deployment: loom-hive-operator` | Single replica (singleton scheduler), image `registry.harbor.lan/library/loom-hive-operator:<tag>`, resource requests/limits set, `nodeSelector` for amd64 |
| `PersistentVolumeClaim: hive-state` | 5Gi, `storageClass: longhorn`, RWO; mounted at `/var/lib/loom-hive` |
| `ServiceAccount + Role + RoleBinding` | Read `cluster-agent-auth` / `cluster-agent-api-keys` (cross-namespace if needed); patch own ConfigMap for runtime policy reloads; create/list/delete spawn pods (or call `loomd` MCP); read `mcp-auth-refresher` status |
| `Service: loom-hive-operator` | ClusterIP for in-cluster MCP/REST; optional Ingress for HUD/Mac CLI access (admin-token + mTLS gated) |
| `ConfigMap: hive-policy` | Mounted at `/etc/loom-hive/policy.yaml`; reloaded on file change (fsnotify) |
| `ServiceMonitor: loom-hive-operator` | Prometheus scraping `/metrics` |
| `CronJob: hive-backup` | Nightly SQLite dump → MinIO bucket `loom-hive-backups/` (retain 30d) |

The operator binary is `cmd/loom-hive-operator/main.go`; built with the standard workspace Makefile (`make build/loom-hive-operator`) and Dockerfile pattern from `services/AGENTS.md`. Auth identity = the cluster credentials per `.loom/87-` AUTH-002 / AUTH-006; no host-side state.

Observability:
- Liveness/readiness probes on `/healthz` / `/readyz`
- Structured logs (`slog` JSON) → stdout → Loki via existing pipeline
- Prometheus metrics from §"KPIs / new telemetry contract" (see `.loom/89-` §8)

The Mac-side `loom hive` CLI (slice 2.6) talks to the operator over the existing Streamable HTTP transport (`docs/STREAMABLE_HTTP.md`), authenticating with an admin token.

## Evaluation framework

Two-loop evaluation that scores both the council's plans and the pipeline's outcomes, persisted in `eval_scores`.

### Loop A — Synchronous: council artifact eval

Runs as the **last** stage of every council run, before any backlog mutation is committed.

A FlexInfer-backed "judge" agent scores the candidate sidecar + markdown set against this rubric:

| Criterion | Weight | Pass condition |
|---|---|---|
| Sidecar JSON validity | 0.20 | parses + schema-valid against `pkg/hive/eval/sidecar_schema.json` |
| Slice independence | 0.20 | no overlapping `files[]` between slices marked `parallel_with` peers |
| Success-criteria machine-checkability | 0.15 | every `success.tests[]` is a runnable command (heuristic: starts with a known runner) |
| Plan completeness | 0.15 | every slice has `files`, `tests`, `budget` populated; no orphan dependencies |
| Roadmap alignment | 0.15 | each new backlog item references at least one `roadmap_intents` row |
| Contradiction-free against last 14d | 0.15 | no slice contradicts any merged backlog item from last 14d (judge reads `events` + recent merges) |

Aggregate score below `0.7` → council run marked `partial`; backlog mutations skipped; the editor's artifacts still commit (auditable record), but no GitLab issues are created.

The judge prompt is fixed (`pkg/hive/eval/prompts/council_artifact_judge.md`), versioned, and uses a small FlexInfer model — never the frontier.

### Loop B — Asynchronous: pipeline outcome eval

When a `pipeline_run.state` transitions to `merged`:

1. Look up `backlog_items.council_run_id`. Attribute the merge back to that council run.
2. Compute per-merge metrics: time-to-merge, retry count, gate pass-rate, post-merge regression (correlated within 30min via Alertmanager).
3. Persist as `eval_scores{subject_kind: "pipeline_run"}` with breakdown.
4. Aggregate per `council_run_id` daily into `eval_scores{subject_kind: "council_run", rubric: "downstream"}`. This becomes the "Council ROI" KPI.

### Loop C — Weekly: cross-run consistency eval

Scheduled job (Sunday 0600) reads the last 7 days of council outputs + merged MRs and runs a meta-evaluator that flags:

- Plans that were never picked up (stale); proposed action: re-prioritize or close.
- Plans that contradict one another (two slices touching the same files with different intents).
- Plans where pipeline runs consistently fail at the same gate (signal that the plan structure is wrong, not the implementation).

Output is a small `cross_run` `eval_scores` row plus a council-brief addendum that the next daily council reads in §"Council brief" step 7. This is the feedback loop that closes the dark-factory cycle described in `.loom/78-` §"Dark Factory Thesis".

### Eval API

```
GET /api/hive/eval/scores?subject_kind=council_run&since=…     # list scores
GET /api/hive/eval/scores/{id}                                 # detail with breakdown
POST /api/hive/eval/run-cross                                  # trigger ad-hoc cross-run eval
```

HUD `Hive` view (slice 4.2) gains a fourth panel **"Eval"** that renders score trends, top-3 contradictions, and stale plans.

## REST + MCP surface (exposed by `loom-hive-operator`)

All endpoints are admin-token-gated (existing scheme). Mobile parity is post-v1.

```
# Council
GET    /api/hive/council/runs                        # list past runs
GET    /api/hive/council/runs/{id}                   # run detail (artifacts, sidecar, cost)
POST   /api/hive/council/run                         # trigger ad-hoc council run (body: { trigger: "manual", reason })
POST   /api/hive/council/dryrun                      # plan-only; emits sidecar to scratch dir, no commit/push

# Pipeline
GET    /api/hive/pipeline/runs                       # list active + recent
GET    /api/hive/pipeline/runs/{id}                  # detail with stages, gates, costs
POST   /api/hive/pipeline/runs/{backlog_id}/start    # manual start (e.g., for label-override testing)
POST   /api/hive/pipeline/runs/{id}/pause            # pause at next gate
POST   /api/hive/pipeline/runs/{id}/resume
POST   /api/hive/pipeline/runs/{id}/escalate         # force human handoff

# Backlog
GET    /api/hive/backlog                             # list local copy
POST   /api/hive/backlog/sync                        # pull from GitLab → local YAML and back
GET    /api/hive/backlog/{id}                        # detail

# Status / KPIs
GET    /api/hive/status                              # one-shot snapshot: budgets remaining, queue depth, last council run
GET    /api/hive/kpis?window=1d|7d|30d
GET    /api/hive/policy                              # current effective policy (read-only)
```

MCP tools (registered in `loomd` so frontier agents can drive the hive):

```
hive_council_dryrun(trigger, reason)
hive_council_run(trigger, reason)
hive_pipeline_start(backlog_id)
hive_pipeline_pause(run_id, reason)
hive_pipeline_resume(run_id)
hive_pipeline_escalate(run_id, reason)
hive_backlog_list(filter)
hive_backlog_get(id)
hive_status()
hive_kpis(window)
```

## Stage gates — required v1 set

A gate is a deterministic function `gate(stage_artifacts, sidecar) → {pass | fail, reasons[]}`. Gates run in pure-Go where possible; LLM-judged gates use a small FlexInfer call with a strict rubric prompt and never the frontier model.

| Gate | Type | What it checks | Fail action |
|---|---|---|---|
| `lint` | shell | language-appropriate lint (devbox quality gate) | retry stage |
| `tests` | shell | language-appropriate tests (devbox quality gate) | retry stage |
| `diff-size` | pure-Go | added+removed lines ≤ N (default 800; override per-label) | escalate |
| `scope` | pure-Go | files touched ⊆ slice files (allowing tests) | escalate |
| `path-policy` | pure-Go | no protected paths touched unless `require_human_review` | escalate |
| `secret-scan` | pure-Go (regex) + tool (`mcp-secret-scanner` if available) | no API keys / tokens in diff | escalate |
| `commit-format` | pure-Go | conventional commits | retry stage |
| `pr-self-review` | LLM (FlexInfer) | runs the `pr-self-review` checklist; reports issue count | retry once, then escalate |
| `spec-conformance` | LLM (FlexInfer) | does diff satisfy success criteria from sidecar? | retry once, then escalate |
| `regression` | post-merge | correlate alert burst within 30 min of merge → flag | post-hoc; auto-revert if `policy.auto_revert_on_regression` |

## Council details

### Council brief (deterministic prompt header)

The brief is assembled by the editor and shared to all reviewers verbatim. Includes:

1. The current `ROADMAP.md` excerpt (Recently Shipped + Active Workstreams sections).
2. The most recent `.loom/00-index.md` Quick Links + Current Planning Addendum.
3. Open backlog (titles + labels + ages) from `.loom/backlog/`.
4. Last 7 days of merged MRs (titles + paths touched + author).
5. Open incidents and alert summary (Alertmanager + recent Loki errors).
6. Current hive KPIs (cost / merged change, regression rate).
7. The lens for *this* reviewer ("security" / "tech-debt" / "user-impact").

### Council output contract

Every council run produces a single commit on a `council/<date>` branch with:

- `.loom/<NN>-research-…md`
- `.loom/<NN>-product-spec-…md`
- `.loom/<NN>-implementation-plan-…md`
- `.loom/<NN>-…-sidecar.json`
- `.loom/backlog/<id>.yaml` (one file per new/updated item)
- Updated `.loom/00-index.md` (new entries appended in the active addendum)

GitLab side effects (via `mcp-gitlab` tools, executed by the editor agent in the final stage):

- `create_issue` for each new backlog item, body templated from sidecar
- `update_issue` for re-prioritizations
- `close_issue` (with comment) for retired items

If `artifacts_merge_strategy: "fast-merge-loom-only"`, the editor pushes to `main` for files under `.loom/` and `.gitlab/issue_templates/` only; everything else opens an MR for human review.

### Council failure modes

- **Reviewer timeout** → editor proceeds with available reviewers if quorum (≥ 2) met; otherwise marks run `partial` and skips backlog mutation.
- **Editor hits cost cap** → graceful stop; commit whatever artifacts are written so far; no GitLab mutations.
- **Lock contention on `council/<date>` branch** → next run aborts and emits `loom_hive_council_runs_total{outcome=conflict}`.

## Pipeline details

### Pipeline run lifecycle

```
queued ─▶ planning ─▶ slicing ─▶ implementing ─▶ testing ─▶ reviewing ─▶ mr ─▶ ci ─▶ merging ─▶ done
                                                                                                    │
                                                                                                    └─▶ regressing? ─▶ reverting
                                              │ any stage gate fail (after retries)
                                              └─▶ escalating ─▶ handoff_open
```

Stage `implementing` may fan out into N parallel sub-runs (one per slice), then a synthetic stage `integrating` collects all sub-run outputs and resolves any merge conflicts before continuing to `testing`.

### Per-stage worker contract

Each worker is invoked via the existing spawn / weaver path with:

- `LOOM_PARENT_SESSION_ID` (existing, `.loom/87-` SESS-005)
- `LOOM_HIVE_RUN_ID` (new) — pipeline run id
- `LOOM_HIVE_STAGE` (new) — stage name
- `LOOM_HIVE_BACKLOG_ID` (new)
- Bounded budget (`Request.MaxCostUSD`, `Request.MaxTurns`) populated from sidecar

The worker emits its result through the existing telemetry channel; the pipeline runner reads `SpawnTelemetry` and decides next-stage entry.

### Reconciler (every 60s)

Pseudo-code:

```go
func reconcile() {
    backlog := readLocalBacklog()
    runs   := listActivePipelineRuns()
    for _, item := range backlog where item.state == "queued" {
        if item.dependencies satisfied && capacityAvailable() && policyAllows(item) {
            startPipelineRun(item)
        }
    }
    for _, run := range runs {
        if run.stage_done {
            gate, ok := evaluateGate(run.stage)
            if !ok && run.attempts >= policy.retry.max_attempts {
                escalate(run)
            } else if !ok {
                retry(run)
            } else {
                advanceToNextStage(run)
            }
        }
    }
}
```

## HUD surface (v1)

A new top-level view "Hive" with three panels (reuses `PanelShell`, `DataTable`, `DetailDrawer`):

1. **Council** — recent runs (date, trigger, cost, artifacts count, status); detail drawer renders the markdown artifacts with collapsible sections.
2. **Pipelines** — active + recent runs (backlog id, current stage, % complete, cost, ETA); drawer with per-stage timeline (reuse `Traces` panel pattern).
3. **Backlog** — list of `.loom/backlog/*.yaml` items with state badges; click → drawer with full sidecar fields and a "Start now" / "Pause" / "Escalate" action set (gated by admin token).

A new metric card row at the top of the Overview view: "Cost per merged change", "Slice→merge p50", "Auto-merge rate", "Regression rate", "Council ROI".

## Mobile surface (v1.1, post-v1)

Out of scope for v1. Desktop HUD only. Mobile parity follows the pattern in `.loom/82-` Track D.

## Success criteria

A v1 release of the hive is "done" when **all** of these are true:

1. **Cluster-resident.** `loom-hive-operator` runs in `loom-hive` namespace under Flux; restart of the pod resumes in-flight runs from SQLite WAL within 60s; nightly backup to MinIO succeeds.
2. **Persistence canonical.** Backlog items, council runs, pipeline runs, gate outcomes, KPI snapshots, and eval scores all live in SQLite; queries from the REST API hit the DB; `.loom/backlog/*.yaml` and GitLab issues are derived/synced views; GitLab outage does not block hive operation (drift visible in `loom_hive_gitlab_sync_lag_seconds`).
3. **Council dryrun.** `loom hive council dryrun` (Mac CLI → operator REST) produces a sidecar JSON + markdown plans in `tmp/` from the current repo state, on demand, in < 8 minutes, costing < $5.
4. **Scheduled council.** A cron-triggered council run lands a `council/<date>` commit with valid markdown + sidecar files, with no human prompts. The eval-judge score is recorded; runs scoring < 0.7 are marked `partial` and skip backlog mutation.
5. **Pipeline auto-flow.** A backlog item created by the council with `auto: true` is picked up within one reconciler tick, walks `plan-slice → implement → tests → pr-self-review → mr → ci-watch → merge → cleanup`, and produces a merged MR linked to the issue + persisted `pipeline_runs` row.
6. **Protected-path policy.** A backlog item that touches a `protected_path` (e.g., `platform/gitops/`) flows through `mr` and stops at the `merge` gate awaiting human review (no auto-merge); state stays `running` with `current_stage: merge_pending_review`.
7. **Escalation.** A pipeline run that fails a gate three times escalates: handoff created via `agent_handoff_create`, follow-up issue filed with full failure record, item moves to `escalated`, and the reconciler stops auto-retrying until the issue or backlog YAML is human-edited.
8. **APIs.** `GET /api/hive/status` returns `{budgets_remaining_usd, queue_depth, last_council_at, active_pipeline_runs}`; `GET /api/hive/kpis?window=7d` returns the five KPIs from `.loom/89-` §8; `GET /api/hive/eval/scores?…` returns the council + pipeline eval rows.
9. **HUD.** `Hive` view renders four panels (Council, Pipelines, Backlog, Eval) and the new metric card row; updates within 2s of state change via existing SSE path.
10. **Telemetry.** Hive metrics from `.loom/89-` §8 are visible in Prometheus; Grafana dashboard `platform/gitops/monitoring/dashboards/hive.json` imports cleanly with panels for KPIs, gate pass-rates, eval scores, and budget burn.
11. **No regressions.** All existing weaver, spawn, mentatlab, and agent-context tests pass. Gate: `go build ./... && go test ./pkg/hive/... ./pkg/weaver/... ./internal/spawn/... ./internal/hud/... ./cmd/mcp-mentatlab/... ./cmd/loom-hive-operator/... ./pkg/agentcontext/...`
12. **Docs.** `docs/HIVE.md` covers cluster deployment, policy file, council brief, pipeline stages, gate semantics, persistence schema, eval rubric, and a runbook for pause/resume + recover-from-corrupted-DB scenarios.

## Acceptance

- **Backward compatible.** Existing weaver domains, MentatLab flows, agent-context workflows, and spawn requests work unchanged. The hive is *additive*: a new `pkg/hive/` package, new `cmd/mcp-hive/` server, new HUD view, new policy file. Nothing existing changes shape.
- **No new external dependencies for v1.** Hive is pure Go + existing MCP servers + existing FlexInfer client.
- **Configurable & disable-able.** A single env / flag (`LOOM_HIVE_ENABLED`) and a kill switch in `policy.yaml` (`enabled: false`) freeze all hive activity. Reconciler exits cleanly.
- **Observable end-to-end.** Every pipeline stage emits at least one Prometheus increment and one structured log line at `INFO` with the run id + stage + outcome.
- **Non-leaky.** No frontier credentials are read from outside `cluster-agent-auth` / `cluster-agent-api-keys` (`.loom/87-` AUTH-006). No host-side state mutated.

## Risks (carried from `.loom/89-` §9, with mitigation owners)

| # | Risk | Mitigation in v1 |
|---|---|---|
| R1 | Council low-quality plans | Daily $ cap (`policy.budgets.council.max_usd_per_day`); reviewers + editor pattern |
| R2 | Pipeline regressions | Conservative gate set; `protected_paths` default; `regression` post-merge gate logs and optionally auto-reverts |
| R3 | Heterogeneous council failure | Quorum (≥ 2 of 3 reviewers) before backlog mutation |
| R4 | Backlog explosion | Per-run cap (10 new issues); dedup against open issues by title similarity |
| R5 | Cluster outage | Reconciler is resumable from `.loom/backlog/*.yaml`; no in-memory state lost |
| R6 | 24×7 cost burn | Idle-aware reconciler; FlexInfer scale-to-zero; per-day cap |
| R7 | Operator context loss | HUD `Hive` view + daily digest issue |
| R8 | Auto-merge to protected paths | `protected_paths` glob default-deny; require human review for new path classes |
| R9 | Council plan contradictions | Editor reads last 14 days of council outputs; flags contradictions in sidecar `notes` |
| R10 | Loop (pipeline fail → council "fix" → fail again) | Per-issue retry cap; on cap-exceed, freeze auto-retries until issue is human-edited |

## Open decisions for v1.1+ (deferred)

1. Mobile HUD parity for hive panels.
2. Per-stage scoped credentials (today: pod inherits cluster credentials; future: short-lived per-run tokens).
3. Cross-repo hive (today: single repo; future: multi-repo backlog and policy).
4. Council "debate mode" — multi-round refinement via `auto_compose` (`pkg/weaver/auto_compose.go`). v1 ships with single-pass editor + reviewers.
5. Auto-revert on regression — feature-flag off in v1; turn on after KPIs prove low-noise.
6. Operator override "always-on dashboard" with single-click pause / resume of the entire hive.
7. Replay UI: re-run a past council brief with a different ensemble for A/B.
8. Sub-sub-agent spawning (pod calls back to daemon to spawn another pod) — listed out-of-scope in `.loom/87-`. Hive doesn't need it for v1 because every spawn is initiated from `loomd`.

## Sources

- `.loom/89-research-agent-swarm-council-pipeline-2026-04-25.md`
- `.loom/82-plan-headless-agent-fullstack-2026-04-07.md` (Track A multi-turn; §3 telemetry mapping)
- `.loom/87-product-spec-session-spawning-weaver-2026-04-19.md` (Architecture; AUTH-* slices; SESS-005 parent-session propagation)
- `.loom/88-implementation-plan-session-spawning-weaver-2026-04-19.md`
- `.loom/78-plan-dark-factory-patterns-2026-04-05.md` (Slice 4 auto-quality-gate; Slice 6 pr-self-review)
- `pkg/weaver/router.go`, `pkg/weaver/spawn_bridge.go`, `pkg/weaver/domain.go`
- `internal/spawn/{controller,types,reconciler,store,mentatlab_adapter}.go`
- `internal/hud/spawn.go:879` (`runBudgetWatcher`); `internal/hud/bridge/spawn_telemetry.go:8-58`
- `cmd/mcp-mentatlab/{flows,agents,runs,main}.go`
- `pkg/agentcontext/svc_workflow_*.go`
- `mcp/context/skills-registry.yaml`
- `.agents/workflows/{feature-dev,bugfix,code-review}.yaml`
- `ROADMAP.md`
