# Research: Hierarchical Agent Swarms — Planning Council + Deterministic Execution Pipeline

> Date: 2026-04-25
> Status: Draft v1 — research baseline for the next-layer abstraction above weaver/spawn
> Scope: A "CI above CI" for agents — autonomous continuous software development driven by a planning council that produces planning docs + GitLab backlog deltas and dispatches to a deterministic, gated worker pipeline
> Predecessors: `.loom/64-planning-next-gen-skills-agents-orchestration-2026-03-29.md`, `.loom/77-research-agentic-engineering-patterns-2026-04-05.md`, `.loom/78-plan-dark-factory-patterns-2026-04-05.md`, `.loom/82-plan-headless-agent-fullstack-2026-04-07.md`, `.loom/87-product-spec-session-spawning-weaver-2026-04-19.md`, `.loom/88-implementation-plan-session-spawning-weaver-2026-04-19.md`

---

## 1. The ask, in one paragraph

Take Loom one layer up. Today, an operator (human or frontier agent) drives a single headless agent for a single task. We want a meta-orchestrator that runs **continuous software development** with minimal human involvement: a *council* of planning agents reads roadmap + repo + telemetry and emits structured artifacts (planning docs, GitLab issues, prioritized backlog), and a *deterministic pipeline* of specialized workers picks up backlog items and converts them into merged changes through CI-style stage gates. This is "**CI above CI**" — agent jobs flow through stages (research → spec → slice → tests → review → MR → CI watch → merge) the same way build jobs flow through pipeline stages, with each stage automated and gated.

---

## 2. What we already have (sourced inventory)

The platform is mature; the meta-layer is a coordinator on top of existing primitives.

| Layer | Purpose | Key files |
|---|---|---|
| **Spawn controller** | K8s-native single-agent pod lifecycle (Claude / Codex / Gemini headless) | `internal/spawn/controller.go`, `internal/spawn/types.go`, `internal/hud/spawn.go` |
| **Spawn driver (Node)** | TS bundle wrapping `@anthropic-ai/claude-agent-sdk` and `@openai/codex-sdk`, multi-turn control via `--control-file` (planned in `82-` Track A) | `tools/spawn-driver/src/{cli,claude-driver,codex-driver,control}.ts` |
| **Spawn telemetry** | Canonical `SpawnTelemetry`: turns, tokens, cost, tool calls, file changes, errors, stop reason | `internal/hud/bridge/spawn_telemetry.go:8-58`, parsers in `internal/hud/spawn_{claude,codex}_parser.go` |
| **Weaver (router + subagents)** | FlexInfer-backed inline subagent dispatch with per-subagent tool subsets and budgets; recently extended with `SpawnBridge` so a domain can dispatch to a real headless agent (`Backend != "flexinfer"`) | `pkg/weaver/{router,domain,domain_yaml,spawn_bridge,executor}.go`, `internal/daemon/weaver_spawn_bridge.go` |
| **MentatLab** | DAG flows with steps, gates, agents, runs, templates; persisted runs | `cmd/mcp-mentatlab/{flows,agents,runs,main}.go`, `internal/spawn/mentatlab_adapter.go` |
| **Workflow engine** | Agent-context workflow definitions + approval gates + `auto_verify` gates backed by `devbox_quality_gate` (shipped 2026-04-14, see ROADMAP "Recently Shipped") | `pkg/agentcontext/svc_workflow_*.go`, `.agents/workflows/*.yaml` |
| **Coordinator domain** | LLM-powered summarize / compress / plan inside HUD | `internal/hud/domain/coordinator/coordinator.go` |
| **Responses orchestrator** | Tool-use loop with compaction, OpenAI Responses-API style | `pkg/openairesponses/orchestrator.go` |
| **Agent context** | Sessions, tasks, presence, file claims, worktrees, memory, recipes, handoffs, Qdrant + Neo4j persistence | `pkg/agentcontext/*.go`, `cmd/mcp-agent-context/*.go` |
| **Cluster auth** | Cluster-scoped OAuth + API keys, in-cluster `mcp-auth-refresher` CronJob (planned in `87-`/`88-`) | `cmd/mcp-auth-refresher/`, `platform/gitops/k3s/devbox/cluster-agent-{auth,api-keys}.yaml` |
| **Backlog integrations** | GitLab + GitHub MCP servers with issue/MR/CI tools | `cmd/mcp-gitlab/`, `cmd/mcp-github/` |
| **HUD** | Real-time observability for sessions, fleet, spawns, weaver, traces, presence | `internal/hud/frontend/`, `internal/hud/domain/{spawn,fleet,traces,…}` |

**The corollary:** the building blocks exist. What is missing is a **scheduling, decomposition, and gating layer** that turns "ongoing intent" (roadmap, signals, telemetry, alerts) into a **continuously-flowing stream of pipeline jobs** without human steering each one.

---

## 3. Gap analysis: what's missing for "CI above CI"

| Capability | Today | Needed |
|---|---|---|
| **Persistent intent / north star** | `ROADMAP.md` + `.loom/` planning docs are human-curated and re-read each session | A machine-readable intent model (priorities, constraints, success metrics) that the council consumes deterministically |
| **Backlog as queue** | GitLab issues exist; the workflow engine dispatches by handoff/task in agent-context, not by scanning the backlog | A backlog-shaped queue with explicit pickup criteria (label, priority, dependencies, blockers) and exit criteria |
| **Council (planning tier)** | Single ad-hoc Claude/Codex sessions produce one-off plans; coordinator domain summarizes but does not author backlog | A scheduled, multi-perspective ensemble whose deliverable is *artifacts* (planning docs in `.loom/`, GitLab issues with structured templates), not raw text |
| **Pipeline (execution tier)** | A workflow per agent session, with approval gates and `auto_verify` (one stage = the whole feature) | A **per-backlog-item DAG** of stages, each stage a bounded specialized agent, with deterministic gates between stages and the ability to fan out (parallel slices) and fan in (integrate) |
| **Stage gates** | `step_type: approval` (human) and the new `auto_verify` (devbox quality gate) | A library of **machine-judged gates**: lint, test, coverage, security, diff-size, scope-drift, spec-conformance, regression, public-API check |
| **Decomposition policy** | One planner, one big plan; no automatic slice splitting | Council emits slices with explicit dependencies; pipeline scheduler runs independent slices in parallel worktrees and integrates |
| **Cost budget at the mills level** | Per-spawn `MaxCostUSD` + `MaxTurns` (Phase 1 round 4 work landed: `internal/hud/spawn.go:879` `runBudgetWatcher`) | Mills-level **rolling budget** with caps per tier (council vs. pipeline) and per-day; auto-throttle and pause |
| **Outcome telemetry / feedback** | Spawn telemetry, traces, weaver dispatch counters | Mills-level KPIs (mean cost / merged change, gate pass rate, regression rate, slice-to-merge latency) and a feedback loop into the council's next planning round |
| **Human-in-the-loop scope** | Default: humans drive sessions, optionally accept handoffs | Default: agents drive; humans **interrupt** specific stages they want to gate (e.g., merge-to-main on protected branches, security-sensitive paths). Configurable via policy |

This list is the design surface for §6.

---

## 4. External prior art (canonical references)

External patterns to learn from. URLs are stable canonical sources.

### 4.1 Multi-agent orchestration patterns

- **Anthropic — How we built our multi-agent research system** (2025): orchestrator + lead-researcher + sub-researchers; key insight that *agent isolation by tool-set + small context outperforms one big agent*. Lines up with §4.2 of `.loom/64-`. URL: <https://www.anthropic.com/engineering/built-multi-agent-research-system>
- **Anthropic Agent SDK** — typed streaming events (`SDKMessage`, `tool_use`, `result.permission_denials`) which Loom already maps to canonical telemetry per `.loom/82-` §3. URL: <https://docs.anthropic.com/en/api/agent-sdk/overview>
- **OpenAI Codex SDK** — `Thread.runStreamed`, `ThreadEvent` items; multi-turn re-invocation pattern. URL: <https://platform.openai.com/docs/codex>
- **MCP spec v1.0** — Streamable HTTP, OAuth 2.1, sessions; Loom is already on this transport per `docs/STREAMABLE_HTTP.md`. URL: <https://modelcontextprotocol.io/specification>

### 4.2 Pipeline-as-code DAG runtimes (for the worker tier)

- **Temporal** — durable execution with deterministic replay, signals, child workflows. The dataflow model is the closest off-the-shelf analog to "agent CI." URL: <https://docs.temporal.io/concepts/what-is-a-workflow>
- **Argo Workflows** — K8s-native DAG with templates, gates, suspend-on-event. URL: <https://argo-workflows.readthedocs.io/en/latest/>
- **GitLab CI `needs` graph** — the operator's mental model for "CI above CI"; stage gates without a global stage barrier. URL: <https://docs.gitlab.com/ci/yaml/#needs>
- **Flux + Kustomize** — GitOps reconciliation, the model we use today for cluster state. We can borrow the *desired-state* pattern for backlog reconciliation. URL: <https://fluxcd.io/flux/concepts/>

### 4.3 Production "agent factory" patterns

- **Devin / Cognition** — IC-style autonomous SWE agent with internal planner + executor split; no public protocol but the architecture is well-described in vendor blogs. URL: <https://www.cognition.ai/blog/introducing-devin>
- **GitHub Agent HQ** — agent fleet with policy engine, audit, and budget controls; closest commercial analog to what we are designing. URL: <https://github.blog/news-insights/product-news/github-agent-hq/>
- **Sourcegraph Cody Code Agents** — multi-agent code search → plan → patch flow. URL: <https://sourcegraph.com/blog/code-agents>

### 4.4 Council / ensemble patterns

- **Society-of-mind / Debate** (Du, Li, Torralba et al., 2023) — multi-agent debate improves factuality on planning tasks. URL: <https://arxiv.org/abs/2305.14325>
- **Self-Refine** (Madaan et al., 2023) — generate → critique → revise loop, generalizable to any planning artifact. URL: <https://arxiv.org/abs/2303.17651>
- **AutoGen "GroupChat" pattern** — Microsoft's reference for council-style ensembles. URL: <https://microsoft.github.io/autogen/stable/user-guide/core-user-guide/design-patterns/group-chat.html>

### 4.5 Internal prior art to reuse

- `.loom/64-planning-next-gen-skills-agents-orchestration-2026-03-29.md` — router + specialized subagents (FlexInfer-backed) is the **inline** equivalent of the council; we extend the *same model* to scheduled / multi-step execution.
- `.loom/77-research-agentic-engineering-patterns-2026-04-05.md` and `.loom/78-plan-dark-factory-patterns-2026-04-05.md` — "dark factory" thesis: deterministic gates, structured knowledge, feedback loops. The council + pipeline is the realization of the dark factory at the program level (not just per-session).
- `.loom/82-plan-headless-agent-fullstack-2026-04-07.md` Track A (multi-turn control plane) — required for the pipeline to *steer* a running agent across stage transitions.
- `.loom/87-product-spec-session-spawning-weaver-2026-04-19.md` — `SpawnBridge` + cluster auth + parent-session correlation; the substrate the pipeline runs on.

---

## 5. The shape of the system

```
                              ┌────────────────────────────────────────────┐
                              │                  MILLS                      │
                              │       (control plane in loomd)             │
                              │                                            │
   roadmap, telemetry,        │  ┌──────────────────────────────────────┐  │
   signals, alerts,           │  │           COUNCIL TIER               │  │
   open issues  ────────────► │  │  scheduled (e.g., daily)             │  │
                              │  │  ensemble of planner agents          │  │
                              │  │  emits artifacts:                    │  │
                              │  │   - .loom/<NN>-research-…md          │  │
                              │  │   - .loom/<NN>-product-spec-…md      │  │
                              │  │   - .loom/<NN>-implementation-plan…  │  │
                              │  │   - GitLab issues (templated)        │  │
                              │  │   - backlog ranking deltas           │  │
                              │  └─────────────┬────────────────────────┘  │
                              │                │ enqueue (label, deps,    │
                              │                │  priority, success crit) │
                              │                ▼                          │
                              │  ┌──────────────────────────────────────┐ │
                              │  │           PIPELINE TIER              │ │
                              │  │  continuous (event-driven)           │ │
                              │  │  per-issue DAG, gated stages:        │ │
                              │  │   spec → slice → impl (n parallel)   │ │
                              │  │   → tests → review → MR → CI → merge │ │
                              │  │  workers = headless spawns / weaver  │ │
                              │  │  gates = devbox_quality_gate, lint,  │ │
                              │  │   coverage, sec, diff-size, scope    │ │
                              │  └─────────────┬────────────────────────┘ │
                              │                │                          │
                              │                ▼                          │
                              │  ┌──────────────────────────────────────┐ │
                              │  │           OUTCOMES                   │ │
                              │  │  - merged MRs                        │ │
                              │  │  - closed issues                     │ │
                              │  │  - mills KPIs (cost/merged, latency,  │ │
                              │  │     gate pass rate, regression rate) │ │
                              │  │  - feedback into next council round  │ │
                              │  └──────────────────────────────────────┘ │
                              └────────────────────────────────────────────┘
```

Two scheduling models, intentionally different:

- **Council** = batched and infrequent (e.g., daily 0500, on-roadmap-change, on-major-incident). Tolerates cost. Output is *durable artifacts*, not actions.
- **Pipeline** = event-driven and continuous. Each backlog item is one DAG instance. Gates between stages. No human in the steady-state path.

**Key invariant:** every artifact the council emits is *human-readable, version-controlled, and editable*. Humans steer by rewriting plans / re-prioritizing backlog, not by clicking "approve" inside a workflow.

---

## 6. Design space — decisions

**Status: resolved 2026-04-25.** Each row is now a commitment carried into `90-product-spec-…md`.

| # | Decision | Resolution |
|---|---|---|
| D1 | Where does the council run? | **Hybrid (cluster-only).** Frontier editor (Claude / Codex via headless spawn) + FlexInfer reviewers, all running in k3s. The Mac can trigger and watch via REST/MCP but is never the runtime — operator's MacBook Air sleeps; the mills must not. |
| D2 | Council deliverable contract | **Markdown + structured JSON sidecar.** |
| D3 | Backlog representation | **Three-tier with canonical persistence.** Canonical = internal datastore (SQLite, see §6.5). `.loom/backlog/*.yaml` = git-tracked export for humans + multi-agent visibility. GitLab issues = federated mirror for assignment / cross-tool workflows. Reliable when GitLab is down because canonical store is local-to-cluster. |
| D4 | Pipeline runtime | **Extend MentatLab.** New flow templates + new gate hooks; if MentatLab proves insufficient (e.g., gate types or fan-out semantics), absorb the missing primitives into MentatLab rather than building a parallel engine. |
| D5 | Worker pickup model | **Reconcile loop.** |
| D6 | Worker isolation | **Per-DAG worktree** (per-slice sub-worktrees on demand). |
| D7 | Frontier spend | **Cap + queue per tier.** |
| D8 | Human override | **Policy-driven** by label + path glob; default-deny for new path classes. |
| D9 | Council ensemble | **Heterogeneous** (Claude + Codex + local Qwen via FlexInfer). |
| D10 | Council synthesis | **Editor pattern** — one editor, N reviewers. |
| D11 | Council triggers | Cron + roadmap-change + incident; daily 0500 to start. |
| D12 | Pipeline failure | Retry ≤ 3 → escalate via handoff + filed issue. |

### 6.5 New: persistence layer (D3 elaboration)

Three tiers, with the **canonical** tier being a real datastore — not YAML. Rationale: backlog state, run history, KPIs, and council telemetry need transactional integrity, fast queries, and resilience to GitLab outages. YAML is fragile under concurrent writes and slow to query.

| Tier | Store | What it holds | Authority |
|---|---|---|---|
| **Canonical** | SQLite at `/var/lib/loom-mills/state.db` (k3s PVC) | `backlog_items`, `council_runs`, `pipeline_runs`, `stage_results`, `gate_outcomes`, `kpi_snapshots`, `eval_scores` | Source of truth for the mills |
| **Exported** | `.loom/backlog/*.yaml`, `.loom/mills/runs/<id>.yaml` | Snapshot of canonical state at last council commit (or on-demand export) | Human/git readable; regenerated from canonical store, never edited directly except via PR (which the reconciler will treat as a desired-state delta) |
| **Federated** | GitLab issues + MR labels | Backlog items mirrored as issues for assignment / discussion; pipeline runs linked to issues via `weaver_query_id`-style metadata | Multi-tool visibility; eventual consistency with canonical |

Why SQLite, not Postgres: zero infra (single PVC), excellent write durability (`PRAGMA journal_mode=WAL`), embeds in the operator pod, scales easily into the 100k-row regime we expect. Migration path to Postgres is straightforward if scale demands.

Why not Neo4j/Qdrant (existing stack): wrong shape. Backlog/runs are relational + transactional; the agent-context graph is for entities and relationships across long-lived agent activity. The mills can *reference* agent-context entities (via session/task ids) but its primary data is tabular.

### 6.6 New: deployment topology (D1 elaboration)

The mills runs **only** in k3s. Concretely:

- New deployment `loom-mills-operator` in `platform/gitops/k3s/mills/` — single replica (singleton scheduler), PVC for SQLite, RBAC for `cluster-agent-{auth,api-keys}` and the spawn pod namespace.
- The operator embeds the council scheduler, pipeline reconciler, gate registry, persistence layer, and the mills REST/MCP server.
- It calls existing primitives over the network: spawns via the existing `mcp-spawn` / spawn controller path (today managed by `loomd`); weaver via `mcp-weaver` (extracted from `loomd` if not already exposed remotely); agent-context via `mcp-agent-context`; GitLab via `mcp-gitlab`. All MCP traffic uses the existing Streamable HTTP transport (`docs/STREAMABLE_HTTP.md`).
- The Mac-side `loom` CLI is a *client only* — `loom mills ...` commands hit the cluster operator's REST surface. The CLI never runs the reconciler.
- Failure mode: if the operator pod restarts, SQLite WAL replays in-flight runs; the reconciler picks back up on the next tick.

Implication: phase 1 of the implementation plan must include a deployable operator and the persistence layer before any council/pipeline logic ships.

---

## 7. Tier-by-tier capability list

### 7.1 Council tier — what it must do

1. **Read the world.** Pull current `ROADMAP.md`, latest `.loom/00-index.md`, recent worklog, agent-context recall (`agent_context_recall_enhanced` against `loom-core/roadmap`), GitLab open issues, recent merged MRs (last 7 days), incident reports (Alertmanager + recent Loki errors).
2. **Reason in parallel.** N planner agents run with the same brief but different perspectives (e.g., "system architect", "tech-debt reducer", "user-impact maximizer", "security/risk", "delivery rate") — see D9. Heterogeneous models (Claude + Codex + Qwen).
3. **Synthesize.** An editor agent reads all proposals, asks reviewers tool-style follow-ups, and emits the final artifacts.
4. **Produce structured outputs:**
   - One or more `.loom/<NN>-research-…md`
   - One `.loom/<NN>-product-spec-…md`
   - One `.loom/<NN>-implementation-plan-…md`
   - JSON sidecar (`.loom/<NN>-…sidecar.json`) with: `slices[]`, `dependencies[]`, `priority`, `policy_labels`, `success_criteria`, `cost_estimate`
   - Backlog deltas: GitLab issue creates / updates / re-rankings, posted via `mcp-gitlab` tools
5. **Be reviewable.** All output is committed to a council branch (`council/<date>`) and opened as an MR for human review *or* fast-merged if policy allows. Mirrors how humans currently use `.loom/`.
6. **Bound cost.** Per-run frontier $ cap (e.g., $5–$15), per-day cap (D7).

### 7.2 Pipeline tier — what it must do

1. **Pick up work.** Reconcile loop reads desired runs (= backlog items with `auto: true` label and unmet success criteria) and creates DAG instances.
2. **Decompose deterministically.** For each item, a stage 0 "plan slice" agent reads the linked spec/implementation-plan and emits a per-stage list with tool / file / test scope.
3. **Execute stages with bounded scope.** Each stage = one specialized agent type with a fixed tool subset (mirrors `.loom/64-` §4.2 subagent table) and a per-stage budget. Stage examples:
   - `research` — context recall + targeted code reading
   - `slice` — produce the change set boundaries (files, tests)
   - `implement` — apply the change in a worktree
   - `test` — run language-appropriate test suite via `devbox_quality_gate`
   - `lint` — fmt + lint + diff-check
   - `pr-self-review` — apply checklist from `.loom/78-` slice 6
   - `mr` — push, open MR, link to backlog issue
   - `ci-watch` — poll GitLab CI to terminal state, fix-and-retry on red (already a skill: `ci-failure-recovery`)
   - `merge` — request auto-merge per policy
4. **Gate every transition.** Before stage N+1 starts, an automated gate consumes stage N's artifacts and decides go/no-go. Gates are pure-Go where possible (size limits, scope checks, file path regex, secret scanning) and LLM-judge where necessary (spec conformance, code-review-style critique).
5. **Fan out / fan in.** When the spec marks slices as parallel, the pipeline allocates per-slice worktrees (existing `agent_worktree_allocate` tool) and runs them concurrently. An integrator stage merges results.
6. **Surface to operators.** Each DAG instance shows up in the HUD `Spawn` and `Traces` panels, plus a new `Pipelines` panel with stage status, cost so far, and projected completion.

### 7.3 Mills tier — what coordinates them

1. **Policy engine** (D8) — labels + path globs decide which gates need humans.
2. **Budget enforcer** — Prometheus-tracked rolling spend; throttles or pauses tiers.
3. **Reconcile loop** — desired-state for council schedule, desired-state for pipeline runs.
4. **Feedback loop** — pipeline outcomes (gate fail rate, merge time, regression rate) feed into the next council brief as structured signals.
5. **Telemetry contract** — new metric set (§8).

---

## 8. New telemetry contract (proposed)

Extends the existing `loom_spawn_*`, `loom_weaver_*`, and `loom_session_*` namespaces.

```
loom_mills_council_runs_total{trigger="cron|roadmap|incident", outcome="success|partial|error"}
loom_mills_council_cost_usd_total{model, agent_role}            # editor vs reviewer
loom_mills_council_artifacts_total{kind="research|spec|plan|backlog_issue"}
loom_mills_pipeline_runs_total{stage, outcome="success|gate_fail|error|escalated"}
loom_mills_pipeline_stage_duration_seconds{stage}
loom_mills_pipeline_gate_decisions_total{gate, outcome="pass|fail|skip"}
loom_mills_pipeline_cost_usd_total{stage}
loom_mills_backlog_items_total{label, state="queued|running|merged|escalated"}
loom_mills_merge_to_main_total{auto="true|false", path_class}
loom_mills_regression_count_total                                # post-merge alert correlation
loom_mills_budget_remaining_usd{tier="council|pipeline", window="day|week"}
```

Derived KPIs to render in HUD:

| KPI | Formula | Target |
|---|---|---|
| **Cost per merged change** | `pipeline_cost_usd_total / merge_to_main_total{auto=true}` | trend ↓ |
| **Slice-to-merge p50** | percentile of `pipeline_runs_total` durations end-to-end | trend ↓ |
| **Gate pass rate** | `gate_decisions_total{outcome=pass} / sum` | > 0.85 steady state |
| **Auto-merge rate** | `merge_to_main_total{auto=true} / merge_to_main_total` | depends on policy mix |
| **Regression rate** | `regression_count_total / merge_to_main_total{auto=true}` | < 0.02 |
| **Council ROI** | `merged_changes_traceable_to_council / council_cost_usd_total` | trend ↑ |

---

## 9. Risks and mitigations

| # | Risk | Impact | Mitigation |
|---|---|---|---|
| R1 | Council emits low-quality plans → pipeline burns money on bad work | High | Daily $ cap (D7); editor-pattern critique (D10); humans review council MR before backlog gets created in `auto:` mode |
| R2 | Pipeline gate gaps let regressions through | High | Conservative gate set v1 (lint + tests + diff size + scope); humans review until regression rate < 2%; staged rollout (D8 policy starts strict) |
| R3 | Heterogeneous council fails (one model returns garbage) | Medium | Voting + min-quorum (e.g., 2 of 3 must agree on slice list); editor can drop a reviewer mid-run |
| R4 | Backlog explosion (council writes too many issues) | Medium | Per-run issue cap (e.g., ≤ 10) + dedup against existing open issues |
| R5 | Cluster auth or network outage stalls pipeline | Medium | Reconcile loop is resumable; existing `mcp-auth-refresher` (`87-`) handles credentials; Spawn budgets bound blast radius |
| R6 | Weaver/spawn cost doubles when running 24×7 | Medium | Idle-aware throttle: pipeline pauses if no new backlog after X minutes; FlexInfer scale-to-zero already exists |
| R7 | Humans lose context (don't know what changed) | Medium | Every council run posts a digest issue/MR; HUD `Pipelines` panel; new `loom mills status` CLI |
| R8 | Auto-merges to protected paths cause incident | Critical | D8 policy *requires* human review for `platform/gitops/`, `cmd/loomd/`, security-critical paths. Default-deny for new paths. |
| R9 | Council writes plans that conflict with each other across runs | Medium | Each run reads recent council outputs (last 14 days) and an "active intents" file; editor tracks contradictions and flags |
| R10 | Loops: pipeline fails → council writes a fix plan → pipeline fails again | Medium | Per-issue retry cap = 3; on cap-exceed, escalate via handoff (D12) + freeze auto-retries on that issue until human edits the plan |

---

## 10. Open questions — resolved 2026-04-25

1. **Council branch vs. fast-merge.** Resolved: **hybrid.** Fast-merge to `main` for files under `.loom/` and `.gitlab/issue_templates/` only. Anything else (`ROADMAP.md`, `mcp/context/skills-registry.yaml`, code) opens an MR for human review.
2. **Issue template location.** Resolved: `.gitlab/issue_templates/auto-backlog.md`.
3. **Success-criteria schema.** Resolved: structured `success` block in the canonical store *and* an **evaluation framework** (§10.x below) that scores both the council artifacts (planning quality) and the pipeline outcomes (delivery quality).
4. **Council cadence.** Resolved: start with **daily 0500** + roadmap-change + incident. Tune after we have 30 days of data.
5. **Roadmap as machine-readable.** Resolved: yes, with explicit persistence. A `roadmap` table in the canonical store (not just `roadmap.yaml`) holds priorities, themes, constraints, and the prose excerpts the council should read. A small extractor seeds the table from `ROADMAP.md` on each council run; humans can edit either side and the next reconcile makes them coherent.
6. **Stale-issue retirement.** Resolved: **yes.** Council does backlog hygiene each run — closes resolved-but-still-open issues, downgrades stale ones, dedupes near-duplicates against the canonical store.
7. **Privacy / audit.** Resolved: **reuse** the existing secret-scan gate (`pkg/mills/gates/secret_scan.go`) before any artifact write. No new redaction layer.
8. **Pipeline concurrency cap.** Resolved: hard-cap `policy.budgets.pipeline.max_concurrent_runs` (default 4) and `max_runs_per_day` (default 20) — sounds good and keeps GPU+frontier $$ bounded.

### 10.x Evaluation framework (new)

The council's deliverable is **planning artifacts**, but their value is measured downstream — does the pipeline ship slices that match the plan? do those merged changes regress in the next 24h? do future council runs avoid redoing the same work?

A small `eval` subsystem inside the operator answers these questions:

- **Council artifact eval (synchronous, per-run).** A FlexInfer "judge" agent scores each council output against a fixed rubric: (a) sidecar JSON validity, (b) slice independence (no overlapping file scope between parallel slices), (c) success-criteria machine-checkability (each `success.tests` actually runs), (d) plan completeness (every slice has files+tests+budget). Score range 0–1; below 0.7 marks the run `partial` and skips backlog mutation.
- **Pipeline outcome eval (asynchronous, per-merge).** When a pipeline run hits `merged`, attribute the merge back to the originating council run. Track: time-to-merge, gate-pass-rate, regression in 24h post-merge. Aggregate per council run → "council ROI" KPI.
- **Cross-run consistency eval (weekly, scheduled).** A meta-evaluator reads the last 7 days of council outputs and flags contradictions (e.g., one run says "deprecate FooSvc", a later run extends FooSvc). Flagged contradictions become the next council brief's "watch out for" section.

The evaluator persists scores in the `eval_scores` table; HUD `Mills` view exposes them as a separate panel. Concretely the schema and slices appear in `90-product-spec-…md` §"Evaluation framework" and `91-implementation-plan-…md` Phase 5.

---

## 11. Sources

- `ROADMAP.md` (top of file)
- `.loom/00-index.md`
- `.loom/64-planning-next-gen-skills-agents-orchestration-2026-03-29.md`
- `.loom/77-research-agentic-engineering-patterns-2026-04-05.md`
- `.loom/78-plan-dark-factory-patterns-2026-04-05.md`
- `.loom/82-plan-headless-agent-fullstack-2026-04-07.md` (especially §3 telemetry mapping, §4 Track A multi-turn)
- `.loom/87-product-spec-session-spawning-weaver-2026-04-19.md` (especially §Architecture, AUTH-* slices)
- `.loom/88-implementation-plan-session-spawning-weaver-2026-04-19.md`
- `pkg/weaver/{router,domain,domain_yaml,spawn_bridge,executor}.go`
- `internal/spawn/{controller,types,reconciler,store,mentatlab_adapter}.go`
- `internal/hud/spawn.go:879` (`runBudgetWatcher`)
- `internal/hud/bridge/spawn_telemetry.go:8-58` (`SpawnTelemetry`)
- `cmd/mcp-mentatlab/{flows,agents,runs,main}.go`
- `pkg/agentcontext/svc_workflow_*.go`, `.agents/workflows/*.yaml`
- Anthropic multi-agent research: <https://www.anthropic.com/engineering/built-multi-agent-research-system>
- Anthropic Agent SDK: <https://docs.anthropic.com/en/api/agent-sdk/overview>
- OpenAI Codex SDK: <https://platform.openai.com/docs/codex>
- MCP spec: <https://modelcontextprotocol.io/specification>
- Temporal: <https://docs.temporal.io/concepts/what-is-a-workflow>
- Argo Workflows: <https://argo-workflows.readthedocs.io/en/latest/>
- GitLab CI `needs`: <https://docs.gitlab.com/ci/yaml/#needs>
- Flux CD concepts: <https://fluxcd.io/flux/concepts/>
- Du et al., *Improving factuality and reasoning via multiagent debate* (arXiv:2305.14325): <https://arxiv.org/abs/2305.14325>
- Madaan et al., *Self-Refine* (arXiv:2303.17651): <https://arxiv.org/abs/2303.17651>
- AutoGen GroupChat docs: <https://microsoft.github.io/autogen/stable/user-guide/core-user-guide/design-patterns/group-chat.html>
- Cognition / Devin announcement: <https://www.cognition.ai/blog/introducing-devin>
- GitHub Agent HQ: <https://github.blog/news-insights/product-news/github-agent-hq/>
- Sourcegraph Cody Code Agents: <https://sourcegraph.com/blog/code-agents>
