# Product Spec: Loom Hive v2 — Hierarchical Swarm + Adversarial Audit + Cross-Repo Federation

**Date**: 2026-05-02
**Research**: `.loom/92-research-hive-v2-hierarchical-swarm-2026-05-02.md`
**Implementation Plan**: `.loom/94-implementation-plan-hive-v2-hierarchical-swarm-2026-05-02.md`

## Goal

Promote Loom Hive from a flat two-tier system (Council → Pipeline) to a true hierarchical swarm:

1. **Squad layer** between Council and Pipeline: persistent, domain-owning ensembles with working memory, specialization manifests, and outcome-driven routing.
2. **Adversarial audit swarm**: lateral, independent ensemble that adversarially scores Council artifacts and merged Pipeline diffs with a different model pool and rubric than the editor/judge.
3. **Cross-repo coordination**: a single backlog item can span multiple repos with atomic merge gates.
4. **Council Debate Mode** (deferred from v1.1 #4): multi-round editor↔reviewer refinement with budget cap.
5. **Bounded recursion** in the pipeline (deferred from v1.1 #7): pipeline workers can fan out into sub-runs to depth ≤ 2 with budget tree.
6. **Adaptive policy engine**: outcome-driven policy proposals (relaxations human-applied in v2.0; auto-relax with revert window in v2.1).
7. **Cost preview** (deferred from v1.1 #9): pre-spawn $ estimate in HUD.
8. **Mobile Hive parity** (deferred from v1.1 #1): one screen.

V1.1 deferral #2 (per-stage scoped credentials) is **not** in v2 — kept as v2.1 follow-up.

## Non-goals

- New runtime substrate. Same `loom-hive-operator` deployment, same canonical SQLite, same MentatLab pipeline engine.
- New auth path. Reuse `cluster-agent-{auth,api-keys}` and existing admin token.
- Replacing Eval Loops A/B/C. Audit is **additional**, not a replacement.
- Replacing the FIFO + dependency reconciler. Squad routing is a *bias* layer in front of the existing reconciler; on miss/veto, the reconciler proceeds as today.
- A new graph DB, queue, or message bus. SQLite + reconcile-loop semantics are sufficient.

## Decisions (resolved 2026-05-02)

| # | Decision | Choice |
|---|---|---|
| V2-D1 | Squad seed list | **Two squads to start: `hud-frontend` and `gitops`.** Add others empirically when their backlog volume justifies. |
| V2-D2 | Squad memory storage | **New tables in canonical store** (not agent-context). Hive remains self-contained. |
| V2-D3 | Audit pool composition | **Hybrid.** Bulk on FlexInfer (Llama 4 70B + local Qwen 3 32B); escalate to frontier (Claude Opus + Codex GPT-5) for ambiguous (audit verdict in 0.4–0.7 band). |
| V2-D4 | Cross-repo default | **Disabled.** Ships behind `policy.cross_repo.enabled` flag; flip after 4 weeks of dogfooding. |
| V2-D5 | Council debate default | **Disabled by default; enabled for `incident` trigger only** in v2.0. |
| V2-D6 | Recursion default | **Disabled by default; opt-in per-squad** via `default_ensemble.recursion: true` in squad manifest. |
| V2-D7 | Adaptive policy auto-apply | **Manual in v2.0**, auto-relax (relaxations only, with 24h revert window, denylist for protected paths) in v2.1. |
| V2-D8 | New migrations | **`pkg/hive/store/migrations/002_v2.sql`**, append-only, reviewable. |
| V2-D9 | Audit failure mode | **Advisory only in v2.0** (open follow-up issue + score in HUD); blocking in v2.1 once `audit_survival_rate` proves low-noise (> 0.85 over 100 runs). |
| V2-D10 | Mobile parity | **In v2.0** (one screen). |

## Architecture (delta from v1)

```
v1:   Council ────────────────────► Pipeline ──► Outcomes
                                       │
                                       └─ Eval Loops A/B/C ──► next council brief

v2:   Council (debate) ──► Squad Router ──► Squad{N} ──► Pipeline (recursive ≤2)
              │                  │                │
              │                  │                ▼
              │                  │         Cross-repo Integrator (atomic merge)
              │                  │                │
              ├─ Audit Swarm ◄──┴────────────────┘
              │  (lateral, independent)
              │
              └─► Adaptive Policy Engine ──► policy diff proposals
                                              (.loom/hive/policy_proposals/)
```

Concretely, every v2 piece is implemented as a new Go package under `pkg/hive/` plus migrations and HUD additions; nothing about the v1 reconciler, eval, or pipeline runner changes its public contract.

## Persistence layer (v2 deltas)

New SQLite migration `pkg/hive/store/migrations/002_v2.sql` adds:

```sql
-- Squads: configuration mirror of platform/gitops/k3s/hive/squads/<name>.yaml
CREATE TABLE squads (
    name              TEXT PRIMARY KEY,
    paths_json        TEXT NOT NULL,           -- glob array
    tests_json        TEXT NOT NULL,           -- devbox quality_gate lanes
    gates_json        TEXT NOT NULL,           -- {required: [...], advisory: [...]}
    ensemble_json     TEXT NOT NULL,           -- editor, reviewers, judge selectors
    budget_share      REAL NOT NULL,           -- fraction of daily pipeline budget
    recursion_enabled BOOLEAN NOT NULL DEFAULT 0,
    enabled           BOOLEAN NOT NULL DEFAULT 1,
    last_loaded_sha   TEXT,                    -- git SHA of squad manifest
    created_at        TIMESTAMP NOT NULL,
    updated_at        TIMESTAMP NOT NULL
);

-- Squad working memory: append-on-merge, prune-by-importance weekly
CREATE TABLE squad_memory (
    id              INTEGER PRIMARY KEY,
    squad_name      TEXT NOT NULL REFERENCES squads(name),
    kind            TEXT NOT NULL,             -- merge | tech_debt | convention | followup
    title           TEXT NOT NULL,
    body            TEXT NOT NULL,             -- markdown
    refs_json       TEXT,                      -- file:line and commit refs
    importance      REAL NOT NULL DEFAULT 0.5, -- 0..1; weekly prune below 0.3 if older than 30d
    created_at      TIMESTAMP NOT NULL,
    last_seen_at    TIMESTAMP NOT NULL,
    UNIQUE(squad_name, kind, title)
);
CREATE INDEX idx_squad_memory_lookup ON squad_memory(squad_name, kind, importance);

-- Squad outcomes: rolling success rate per (squad, path_class, gate)
CREATE TABLE squad_outcomes (
    id              INTEGER PRIMARY KEY,
    squad_name      TEXT NOT NULL REFERENCES squads(name),
    path_class      TEXT NOT NULL,             -- e.g., "internal/hud/frontend/**"
    pipeline_run_id TEXT NOT NULL REFERENCES pipeline_runs(id),
    outcome         TEXT NOT NULL,             -- merged_clean | merged_regressed | failed | self_vetoed
    cost_usd        REAL NOT NULL,
    duration_seconds INTEGER NOT NULL,
    created_at      TIMESTAMP NOT NULL
);
CREATE INDEX idx_squad_outcomes_lookup ON squad_outcomes(squad_name, path_class, created_at);

-- Audit findings: independent adversarial scores
CREATE TABLE audit_findings (
    id              INTEGER PRIMARY KEY,
    subject_kind    TEXT NOT NULL,             -- council_artifact | pipeline_merge
    subject_id      TEXT NOT NULL,             -- council_run_id or pipeline_run_id
    severity        TEXT NOT NULL,             -- info | warn | critical
    rubric_id       TEXT NOT NULL,             -- e.g., "audit_v1"
    survival_score  REAL NOT NULL,             -- 0..1
    findings_json   TEXT NOT NULL,             -- structured list of finding objects
    auditor_pool    TEXT NOT NULL,             -- JSON of which models ran
    cost_usd        REAL NOT NULL,
    created_at      TIMESTAMP NOT NULL
);
CREATE INDEX idx_audit_findings_lookup ON audit_findings(subject_kind, subject_id);

-- Cross-repo run state
CREATE TABLE cross_repo_runs (
    id                  TEXT PRIMARY KEY,
    backlog_item_id     TEXT NOT NULL REFERENCES backlog_items(id),
    repos_json          TEXT NOT NULL,         -- [{project_id, branch, mr_iid, ci_status, gate_status}]
    state               TEXT NOT NULL,         -- planning | open | gates_green | merging | merged | reverted | failed
    atomicity_strategy  TEXT NOT NULL DEFAULT 'all_or_revert',
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL
);

-- Pipeline recursion: parent chain
ALTER TABLE pipeline_runs ADD COLUMN parent_run_id TEXT;
ALTER TABLE pipeline_runs ADD COLUMN depth INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_pipeline_runs_parent ON pipeline_runs(parent_run_id);

-- Council debate transcripts
CREATE TABLE council_debate_rounds (
    id                  INTEGER PRIMARY KEY,
    council_run_id      TEXT NOT NULL REFERENCES council_runs(id),
    round_index         INTEGER NOT NULL,
    role                TEXT NOT NULL,         -- editor_proposes | reviewer_critiques | moderator_decision | editor_revises
    cost_usd            REAL NOT NULL,
    summary             TEXT NOT NULL,         -- short markdown
    artifact_deltas_json TEXT,                 -- list of {path, line_range, action}
    created_at          TIMESTAMP NOT NULL
);

-- Adaptive policy proposals (.loom/hive/policy_proposals/<date>.md is the human-facing copy)
CREATE TABLE policy_proposals (
    id              INTEGER PRIMARY KEY,
    proposal_date   TEXT NOT NULL,             -- YYYY-MM-DD
    kind            TEXT NOT NULL,             -- relax | tighten | rotate_ensemble
    target          TEXT NOT NULL,             -- e.g., "gates.coverage", "squads.hud-frontend.ensemble.editor"
    diff            TEXT NOT NULL,             -- yaml/text diff
    rationale       TEXT NOT NULL,             -- markdown citing kpi_snapshots
    state           TEXT NOT NULL,             -- pending | applied_human | applied_auto | rejected | reverted
    applied_at      TIMESTAMP,
    revert_deadline TIMESTAMP,
    created_at      TIMESTAMP NOT NULL
);
```

DAO additions live in `pkg/hive/store/dao_squad.go`, `dao_audit.go`, `dao_crossrepo.go`, `dao_debate.go`, `dao_policy_proposal.go`. Each is a new file; no modifications to existing DAOs except the new columns on `pipeline_runs` accessed via existing `dao_pipeline.go` queries (verify in implementation plan slice 1).

## Squad manifest (YAML)

`platform/gitops/k3s/hive/squads/hud-frontend.yaml`:

```yaml
apiVersion: hive.loom.dev/v1
kind: Squad
metadata:
  name: hud-frontend
spec:
  paths:
    - "internal/hud/frontend/**"
    - "mcp/skills/hud-*"
    - "docs/hud/**"
  tests:
    - pnpm-typecheck
    - pnpm-vitest
    - pnpm-build
  gates:
    required: [pr_self_review, scope, secret_scan, commit_format]
    advisory: [coverage]
  ensemble:
    editor:
      backend: spawn
      driver: claude-opus
      max_cost_usd: 4.0
    reviewers:
      - { backend: flexinfer, model: llama-4-70b-instruct, lens: ux }
      - { backend: flexinfer, model: qwen-3-32b,           lens: a11y }
      - { backend: spawn,     driver: codex-gpt5,          lens: bundle_size }
    judge:
      backend: flexinfer
      model: llama-4-70b-instruct
  budget_share: 0.30
  recursion_enabled: false
```

`platform/gitops/k3s/hive/squads/gitops.yaml`:

```yaml
apiVersion: hive.loom.dev/v1
kind: Squad
metadata:
  name: gitops
spec:
  paths:
    - "platform/gitops/**"
  tests:
    - kustomize-build
    - kubeval
  gates:
    required: [diff_size, scope, path_policy, secret_scan, pr_self_review, spec_conformance]
    advisory: []
  ensemble:
    editor:
      backend: spawn
      driver: codex-gpt5            # codex preferred for terraform/kustomize precision
      max_cost_usd: 3.0
    reviewers:
      - { backend: flexinfer, model: llama-4-70b-instruct, lens: cluster_safety }
      - { backend: spawn,     driver: claude-opus,         lens: drift_check }
    judge:
      backend: flexinfer
      model: llama-4-70b-instruct
  budget_share: 0.20
  recursion_enabled: false
```

`platform/gitops/k3s/hive/squads/_default.yaml` (used when no squad matches; identical to v1 behavior):

```yaml
apiVersion: hive.loom.dev/v1
kind: Squad
metadata:
  name: _default
spec:
  paths: ["**"]
  tests: [auto]
  gates:
    required: [pr_self_review, scope, secret_scan, commit_format, diff_size]
    advisory: [coverage, spec_conformance]
  ensemble: {}                       # operator falls back to policy.council.ensemble
  budget_share: 0.50
  recursion_enabled: false
```

## Squad routing flow

```
backlog_item (state=queued)
  ↓
SquadRouter.Pick(item):
  1. score each squad against item.spec_doc paths + sidecar.slices[].files
  2. for matching squads, compute confidence:
       conf = success_rate_window(squad, path_class, last 30 outcomes)
  3. if best squad conf ≥ 0.6 → route to that squad
  4. else → route to _default (= v1 behavior)
  ↓
Squad.Plan(item):
  - load squad_memory (top 20 by importance)
  - run squad's planner (single-shot Claude/Codex via spawn) with:
      prompt = base_planner + squad.conventions + squad.tech_debt
      context = item.spec_doc + sidecar
  - emit refined sidecar slices, gates list, budget request
  ↓
Pipeline.Run(item, squad_plan):
  - identical to v1, but with squad.gates and squad-allocated budget
  - on merge → SquadOutcomeRecorder writes to squad_outcomes
  - on regression in 24h → outcome marked merged_regressed (lowers conf)
```

## Audit swarm flow

```
trigger: council artifact committed OR pipeline_run.state→merged
  ↓
AuditDispatcher:
  1. compose audit task: { subject_kind, subject_id, artifact_text_or_diff, rubric_id="audit_v1" }
  2. select pool from policy.audit.pool (default: llama-4-70b + qwen-3-32b on FlexInfer)
  3. run pool concurrently; each emits findings JSON + survival_score 0..1
  4. aggregate: pool_score = median; if 0.4 < pool_score < 0.7 → escalate to frontier (claude-opus + codex-gpt5)
  5. final: max(pool_score, frontier_score) when escalated, else pool_score
  ↓
PersistFindings → audit_findings row
  ↓
if survival_score < 0.6:
  - council artifact: open MR (override .loom/ fast-merge), summary references audit findings
  - pipeline merge: open follow-up issue + tag P1; HUD shows red badge on the merge
else: no action; HUD shows green badge
```

Audit prompts (in `pkg/hive/audit/prompts/`):

- `audit_v1_council.md` — adversarial rubric for Council artifacts: hidden assumptions, deletion-by-omission, tests-vs-spec gap, slice independence violations, cost realism.
- `audit_v1_pipeline.md` — adversarial rubric for merged diffs: spec drift, scope creep, behavior change vs. plan, regression risk, test coverage realism.

The agent is told: "You are an adversarial reviewer. Your job is to find what is missing or wrong, not to agree." The scoring rubric is fixed, version-controlled, and never authored by the same model that wrote Council prompts.

## Cross-repo coordination flow

```
backlog_item.repos = [
  { project_id: 47, branch: "feat/x-loom-core" },     # loom-core
  { project_id: 51, branch: "feat/x-loom-vscode" }    # loom (VS Code ext)
]
  ↓
Pipeline.Run(item):
  - allocate one worktree per repo via agent_worktree_allocate
  - per-repo stages run in parallel (plan-slice, impl, tests)
  - each repo gets its own MR via mcp-gitlab
  ↓
CrossRepoIntegrator.WaitForGreen():
  - poll each MR's CI status via mcp-gitlab
  - timeout per repo: policy.cross_repo.per_repo_timeout_minutes (default 60)
  - if any timeout → revert all open MRs (RevertStrategy=all_or_revert)
  ↓
CrossRepoIntegrator.AtomicMerge():
  - merge in dependency order (declared in cross_repo_runs.repos_json)
  - on per-repo merge failure (race, conflict, etc.):
      open revert MR for each already-merged repo (reverse order)
      mark cross_repo_runs.state=reverted
      escalate via handoff
  - on success: mark state=merged
```

Repo registry `platform/gitops/k3s/hive/repos.yaml`:

```yaml
apiVersion: hive.loom.dev/v1
kind: RepoRegistry
metadata: { name: workspace }
spec:
  repos:
    - name: loom-core
      project_id: 47
      default_branch: main
      auto_merge: true
    - name: loom
      project_id: 51
      default_branch: main
      auto_merge: true
    - name: flexdeck
      project_id: 53
      default_branch: main
      auto_merge: false              # dogfood loom-core+loom first
    # ... add others as auto_merge becomes safe
```

## Council Debate Mode

Round structure:

```
Round 0 (always):
  editor.propose(brief) → draft_v0

Round 1 (always when debate enabled):
  parallel: each reviewer.critique(draft_v0) → critique_v0
  moderator.assess(critiques) → { converged: bool, focus_areas: [...] }
  if converged: emit draft_v0, exit; else continue

Round 2:
  editor.revise(draft_v0, critiques, focus_areas) → draft_v1
  parallel: each reviewer.refocused_critique(draft_v1, focus_areas) → critique_v1
  moderator.assess → if converged: emit draft_v1, exit

Round 3 (max):
  editor.revise(draft_v1, critique_v1) → draft_final
  emit draft_final
```

Budget cap: `policy.council.debate.max_usd` (default $8.00). When 80% consumed mid-round, exit early with current best draft and a sidecar `debate.early_exit_reason: "budget"`.

Triggers:
- `policy.council.debate.enabled.cron`: false (default)
- `policy.council.debate.enabled.roadmap_change`: false (default)
- `policy.council.debate.enabled.incident`: true (default — incident plans benefit from debate)
- `policy.council.debate.enabled.manual`: true (operator can `loom hive council run --debate`)

Sidecar gains a top-level `debate` field:

```json
{
  "debate": {
    "enabled": true,
    "rounds": [
      { "round": 0, "role": "editor_proposes", "cost_usd": 0.42 },
      { "round": 1, "role": "reviewer_critiques", "cost_usd": 1.18 },
      { "round": 1, "role": "moderator_decision", "cost_usd": 0.06, "converged": false },
      { "round": 2, "role": "editor_revises", "cost_usd": 0.51, "deltas": [{ "path": ".loom/95-...", "line_range": "120-145" }] }
    ],
    "early_exit_reason": null,
    "total_cost_usd": 2.17
  }
}
```

## Bounded recursion (sub-runs)

New MCP tool exposed by `loom-hive-operator`:

- `mcp-hive::pipeline_subrun_create(parent_run_id, slice_spec, depth)` — creates a child `pipeline_runs` row with `parent_run_id` set; returns child run id. Caller (a worker pod with admin token) gets back a per-subrun budget.

Enforcement (in `pkg/hive/pipeline/recursion.go`):

```
on subrun_create:
  parent = pipeline_runs.get(parent_run_id)
  if parent.depth + 1 > policy.pipeline.max_recursion_depth (default 2):
    reject "recursion_depth_exceeded"
  if requested_budget > 0.6 * parent.budget_remaining_usd:
    reject "budget_subrun_too_large"
  if slice_spec.stages overlap with parent's stage stack:
    reject "cycle_detected"
  ok → create child with depth=parent.depth+1
```

Subrun outcomes roll up into the parent: parent's `success` requires all subruns merged or rolled back atomically (handled via existing integrator).

KPI: `loom_hive_pipeline_recursion_depth_histogram` (p95 expected between 1 and 2).

## Adaptive policy engine

Scheduled job (Sunday 0500). Reads:
- `kpi_snapshots` (last 4 weeks)
- `eval_scores` (last 4 weeks)
- `audit_findings` (last 4 weeks)
- `gate_outcomes` (last 4 weeks; rolling pass rate per gate)

Emits:
- A markdown file `.loom/hive/policy_proposals/<YYYY-MM-DD>.md` summarizing proposals with citations to KPI rows.
- One row per proposal in `policy_proposals` (state=`pending`).

Proposal kinds (v2.0):

| Kind | Trigger | Example |
|---|---|---|
| **relax** | gate pass rate ≥ 0.95 over last 100 runs in a path-class | "Demote `coverage` to advisory for `internal/hud/frontend/**` (105/106 pass)." |
| **tighten** | gate pass rate < 0.50 over last 100 runs OR audit_survival_rate < 0.70 in a path-class | "Promote `pr_self_review` to required + human-review for `pkg/security/**`." |
| **rotate_ensemble** | squad audit_survival_rate drops > 5pp vs. baseline OR cost-per-merge increases > 25% | "Rotate hud-frontend.editor from claude-opus to codex-gpt5; baseline ROI restored after 30d." |

v2.0: All proposals are `state=pending` until human edit applies the policy. Human applies by editing `platform/gitops/k3s/hive/configmap-policy.yaml` (and `squads/*.yaml` for ensemble changes); operator hot-reloads (existing `pkg/hive/policy.go` fsnotify path).

v2.1 (deferred): Auto-apply for `kind=relax` only, with:
- 24h revert window: any post-merge regression in the affected path-class auto-reverts the policy diff.
- Path-class denylist: `platform/gitops/`, `pkg/security/`, `cmd/loomd/` cannot be auto-relaxed.

## Cost preview (pre-spawn estimate)

Surface in HUD `Backlog` panel and `loom hive backlog show <id>`:

```
Estimated cost: $4.20 (council: $0.30, squad-plan: $0.40, pipeline: $3.50)
                ±30% confidence based on 47 prior items in path-class internal/hud/frontend/**
```

Implementation: `pkg/hive/budget/estimator.go` consults:
- Path-class median cost from `pipeline_runs` last 30 days
- Sidecar slice count
- Squad manifest's editor/reviewer max_cost_usd
- Recursion plan (estimator multiplies by expected sub-run depth)

Confidence is the standard deviation of historical costs / median.

## Mobile Hive parity

One screen in iOS companion app (`apps/loom-companion-ios/`). Renders three KPI cards and a list of in-flight pipelines. Read-only in v2.0; "Pause Hive" button gated to v2.1.

API: existing operator REST endpoints already cover the data. Mobile view consumes:
- `GET /api/hive/status`
- `GET /api/hive/pipeline/runs?state=running`
- `GET /api/hive/audit/findings?since=24h`

## REST + MCP surface (v2 additions)

### REST

- `GET    /api/hive/squads` — list squads with config and 30-day outcomes
- `GET    /api/hive/squads/{name}` — full squad detail
- `GET    /api/hive/squads/{name}/memory` — paginated working memory
- `POST   /api/hive/squads/{name}/route-test` — admin: simulate routing for a backlog id and return chosen squad + confidence
- `GET    /api/hive/audit/findings?subject_kind=&subject_id=&since=` — filter
- `POST   /api/hive/audit/run` — admin: re-run audit on a subject id
- `GET    /api/hive/cross-repo/runs` — list with state filter
- `POST   /api/hive/cross-repo/runs/{id}/abort` — admin: abort + revert
- `GET    /api/hive/policy/proposals?state=pending|applied|rejected`
- `POST   /api/hive/policy/proposals/{id}/apply` — admin v2.0 manual apply (writes to ConfigMap; logs proposal as state=applied_human)
- `GET    /api/hive/cost-preview?backlog_id=` — pre-spawn estimate

### MCP (via `mcp-hive` server, served by the operator)

- `hive_squads_list`
- `hive_squad_memory_recall(squad, query, limit)` — semantic search over `squad_memory`
- `hive_audit_findings_list(subject_kind, subject_id)`
- `hive_pipeline_subrun_create(parent_run_id, slice_spec, depth)` — bounded-recursion entry point (admin-token only)
- `hive_cost_preview(backlog_id)`
- `hive_policy_proposals_list(state)`

## Policy file additions

```yaml
# v2 keys appended to platform/gitops/k3s/hive/configmap-policy.yaml
hive:
  enabled: true
  cross_repo:
    enabled: false
    per_repo_timeout_minutes: 60
    revert_strategy: all_or_revert
  squads:
    enabled: true
    routing:
      min_confidence: 0.6
      fallback: _default
  audit:
    enabled: true
    pool_default:
      - backend: flexinfer
        model:   llama-4-70b-instruct
      - backend: flexinfer
        model:   qwen-3-32b
    pool_escalation:                  # used when 0.4 < score < 0.7
      - backend: spawn
        driver:  claude-opus
      - backend: spawn
        driver:  codex-gpt5
    daily_budget_usd: 12.0            # ≈ 20% of council
    advisory_only: true               # v2.0; flip to false in v2.1
    survival_threshold: 0.6
  council:
    debate:
      enabled:
        cron: false
        roadmap_change: false
        incident: true
        manual: true
      max_usd: 8.0
      max_rounds: 3
      early_exit_threshold: 0.8        # 80% budget consumed → exit
  pipeline:
    max_recursion_depth: 2
    subrun_max_budget_share: 0.6
  adaptive_policy:
    enabled: true
    auto_apply: false                  # v2.0; v2.1 enables for relax only
    relax_path_denylist:
      - "platform/gitops/**"
      - "pkg/security/**"
      - "cmd/loomd/**"
    revert_window_hours: 24
```

## Success criteria (v2 acceptance)

1. **Squads route at least 30% of backlog items** to a non-`_default` squad after 4 weeks, with squad routing producing **≥ 5pp lower regression rate** than `_default` in the same path-class.
2. **Audit survival rate ≥ 0.85** averaged across council artifacts and pipeline merges over a 4-week window.
3. **Cross-repo dogfood**: at least 3 successful atomic-merge runs spanning loom-core + loom (VS Code extension) within the first week of `policy.cross_repo.enabled=true`.
4. **Council debate** active for incident triggers: at least one incident-driven debate run with sidecar showing converged before round 3 and total cost ≤ `policy.council.debate.max_usd`.
5. **Bounded recursion**: at least one pipeline run in v2.0 dogfood produces a depth-1 subrun with budget tree honored; no recursion-depth violations in the first 30 days.
6. **Adaptive policy**: at least one `relax` proposal applied (human) in the first 4 weeks, with no regression in the affected path-class within 30 days.
7. **Cost preview**: HUD `Backlog` panel shows estimate within ±30% on 80% of finalized items over a 4-week window.
8. **Mobile parity**: iOS Hive screen renders three KPI cards + in-flight pipelines list end-to-end against the cluster operator (read-only).
9. **No v1 regression**: all v1 acceptance criteria (1–12 from `.loom/91-…2026-04-25.md`) remain green.
10. **Default-off rollout**: each v2 feature ships behind its policy flag; flips happen one at a time with 1-week soak between flips.

## KPIs (HUD `Hive` view extensions)

New panel: `Squads`. Shows per-squad cards with:
- Success rate (last 30 days)
- Avg cost per merged item
- In-flight runs
- Top 3 working memory items by importance
- Latest audit survival score

New panel: `Audit`. Shows:
- Survival rate trend (24h / 7d / 30d)
- Top 5 critical findings (last 7d)
- Findings histogram by severity

New card on Overview: `Adaptive Policy Proposals` — pending count, link to `.loom/hive/policy_proposals/`.

Existing `Eval` panel grows a third tab `Audit` showing audit findings alongside Loop A/B/C scores.

## Out of scope for v2.0 (v2.1+ work)

- Auto-apply of policy proposals (relaxations) with revert window — v2.1.
- Audit-driven *blocking* (currently advisory-only) — v2.1.
- Cross-repo enabled by default — v2.1 after dogfood KPIs land.
- Per-stage scoped credentials (v1.1 deferral #2) — v2.2.
- Council "replay" UI for A/B ensemble comparison (v1.1 deferral #6) — v2.1.
- Squads beyond hud-frontend + gitops — added empirically when backlog volume justifies.
- Sub-recursion depth > 2 — not in scope without new design (depth=3+ adds cycle classes that need formal verification).

## Sources

- `.loom/92-research-hive-v2-hierarchical-swarm-2026-05-02.md`
- `.loom/91-implementation-plan-agent-swarm-council-pipeline-2026-04-25.md` §"Open items"
- `.loom/90-product-spec-agent-swarm-council-pipeline-2026-04-25.md` §"Persistence layer"
- `.loom/89-research-agent-swarm-council-pipeline-2026-04-25.md` §"10.x Evaluation framework"
- `pkg/hive/store/types.go`, `pkg/hive/store/migrate.go` (current migration pattern)
- `pkg/hive/policy.go` (fsnotify hot-reload pattern)
- `pkg/hive/pipeline/integrator.go` (existing fan-out pattern)
- `pkg/hive/pipeline/dispatcher.go` (worker dispatch pattern)
- `internal/hud/frontend/src/lib/components/Hive/` (existing 4 panels)
- `cmd/mcp-mentatlab/templates/hive-default-pipeline.yaml`
- `platform/gitops/k3s/hive/configmap-policy.yaml`
- Anthropic multi-agent research: <https://www.anthropic.com/engineering/built-multi-agent-research-system>
- AutoGen GroupChat: <https://microsoft.github.io/autogen/stable/user-guide/core-user-guide/design-patterns/group-chat.html>
- Du et al. arXiv:2305.14325: <https://arxiv.org/abs/2305.14325>
- Madaan et al. arXiv:2303.17651: <https://arxiv.org/abs/2303.17651>
- Temporal child workflows: <https://docs.temporal.io/concepts/what-is-a-workflow#child-workflows>
- Argo DAG `dependsOn`: <https://argo-workflows.readthedocs.io/en/latest/walk-through/dag/>
- Cluster API: <https://cluster-api.sigs.k8s.io/>
- LangGraph supervisor pattern: <https://langchain-ai.github.io/langgraph/concepts/multi_agent/>
