# Research: Hive v2 — Hierarchical Agent Swarm Coordination

> Date: 2026-05-02
> Status: Draft v1 — research baseline for the next layer of Loom Hive
> Scope: Promote Hive from a flat two-tier (Council → Pipeline) to a true **three-tier hierarchical swarm** (Council → Squads → Pipeline) with adversarial audit, cross-repo federation, and adaptive routing. Carries forward the v1.1 deferrals from `.loom/91-implementation-plan-agent-swarm-council-pipeline-2026-04-25.md` §"Open items".
> Predecessors: `.loom/89-…council-pipeline-2026-04-25.md`, `.loom/90-…2026-04-25.md`, `.loom/91-…2026-04-25.md`, `.loom/82-plan-headless-agent-fullstack-2026-04-07.md`, `.loom/77-research-agentic-engineering-patterns-2026-04-05.md`

---

## 1. The ask, in one paragraph

V1 stood up the meta-orchestration substrate: a council that emits planning artifacts and a pipeline that takes a backlog item to a merged MR, both gated by automated checks and three eval loops. It works, but it is **organizationally flat**. Every backlog item is routed by the same FIFO reconciler to a generic per-DAG worktree; every council run uses the same five-perspective ensemble; every gate verdict is judged once by the same FlexInfer rubric; every change targets one repo (`loom-core`). To turn Hive from "CI above CI" into a **true hierarchical agent organization** we need (a) a tactical mid-tier between Council and Pipeline that owns domains, accumulates expertise, and routes work based on evidence; (b) an independent **adversarial audit** swarm so the system does not grade its own homework; (c) **cross-repo coordination** so coupled changes (e.g., loom-core ↔ loom VS Code extension) are atomic; (d) **bounded recursion** in the pipeline so big slices can decompose into sub-swarms without blowing budget; (e) an **outcome-driven adaptive policy** that closes the loop between merge outcomes and future routing/gating decisions.

---

## 2. What V1 actually shipped (sourced inventory)

The full v1 design space is in `.loom/89-…2026-04-25.md`. The current ship state, verified by `git log --grep='hive\|Hive' --since='2026-04-25'` from this worktree:

| Phase | Slice | Status | Commit |
|---|---|---|---|
| 1 | 1.0 SQLite store + DAOs | shipped | `19f0f3cc feat(hive): canonical SQLite persistence layer` |
| 1 | 1.1 Policy + budget primitives | shipped | `9f2ee287` |
| 1 | 1.2 `cmd/loom-hive-operator/` skeleton | shipped | `effcacee` |
| 1 | 1.3a Dockerfile + Make targets | shipped | `45572180` |
| 1 | 1.3b GitOps manifests | shipped | (verify in `platform/gitops/k3s/hive/`) |
| 1 | 1.4 `loom hive` CLI | shipped | `cf6f6af8` |
| 2 | 2.1 MentatLab `hive-default-pipeline` template | shipped | `1b938b16` |
| 2 | 2.2 Pure-Go gate library v1 | shipped | `2ea283a0` |
| 2 | 2.3 Reconciler + scheduler skeleton | shipped | `2a7572b7` |
| 2 | 2.4 Full REST + admin-token middleware | shipped | `9bb37046` |
| 3 | 3.1–3.6 Council MVP + Eval Loop A | shipped | `1d42beeb`..`5beb7c13` |
| 3 | 3.7 Cron + roadmap-change triggers | shipped | `556397aa` |
| 3 | 3.8 `loom hive council/backlog/eval` CLI | shipped | `c04d0e15` |
| 4 | 4.1 Pipeline run engine | shipped | `3f163a7b` |
| 4 | 4.2 Per-stage worker dispatcher | shipped | `b0c54b61` |
| 4 | 4.3 Fan-out / fan-in integrator | shipped | `40b4d1f5` |
| 4 | 4.4 Pipeline escalation + handoff | shipped | `264317e3` |
| 4 | 4.5 LLM-judged gates (spec_conformance, pr_self_review) | shipped | `33c7c1bc` |
| 4 | 4.6 Eval Loop B (per-merge attribution + council ROI) | shipped | `9f79775b` |
| 4 | 4.7 Pipeline runs wired into reconciler | shipped | `551e06e2` |
| 4 | + real FlexInfer + GitLab + MCP-hub clients (devbox/handoff/worktree/git) | shipped | `d9914899`..`2b8e438d` |
| 5 | 5.1 Prometheus metrics | shipped | `af8a4c0a` |
| 5 | 5.2 HUD `Hive` view (4 panels) | shipped | `4b2f3418` |
| 5 | 5.3 Overview KPI cards | shipped | `54bc0787` |
| 5 | 5.4 Grafana dashboard | (verify in `platform/gitops/monitoring/dashboards/hive.json`) |
| 6 | 6.1 Idle-aware throttling | shipped | `5cef634c` |
| 6 | 6.2 Canonical-store dedup | shipped | `57579a97` |
| 6 | 6.3 Regression gate + Alertmanager webhook | shipped | `164c7f7a` |
| 6 | 6.4 Eval Loop C (cross-run consistency) | shipped | `980dc57e` |
| 6 | 6.5 Docs + runbook (`docs/HIVE.md`, `docs/HIVE_RUNBOOK.md`, `mcp/skills/hive-ops/`) | **not shipped** |
| 6 | 6.6 Default-on flip + production rollout playbook | **not shipped** |

Code surface today (verified via `wc -l pkg/hive/...`): ~18.2k lines under `pkg/hive`, 14 handler files under `cmd/loom-hive-operator/`, 4 panels under `internal/hud/frontend/src/lib/components/Hive/`, 1 MentatLab template, 1 Grafana dashboard slot.

Every gate, every eval loop, every reconciler tick is in production-ready shape. The substrate is real.

---

## 3. What "really special" means — the hierarchical-swarm thesis

V1 organizes agents like a single team meeting weekly: same five perspectives, one editor, one queue, one repo. The next-order win is to organize them like a **small engineering org**:

| Layer | Role | Cadence | Persistence | What it owns |
|---|---|---|---|---|
| **Council** (existing) | Strategy. Reads roadmap + telemetry, emits research/spec/plan + backlog deltas. | Daily 0500 + roadmap change + incident. | Per-run; brief carries forward via `roadmap_intents` and Eval Loop C. | Cross-cutting program direction. |
| **Squads** (new) | Tactics. Each squad owns a *domain* (HUD-frontend, weaver-router, gitops-platform, mobile-ios, mcp-server-fabric, …). Decomposes Council slices into squad-shaped sub-slices, picks gates, suggests parallelism. | Continuous; activated on backlog enqueue. | **Persistent working memory** per squad: conventions, recent merges, tech-debt list, specialization manifest, success-rate-by-gate. | Domain expertise + routing. |
| **Pipeline** (existing) | Execution. Per-DAG worktree, stages, gates, merges. | Event-driven. | Per-run rows in `pipeline_runs`. | Mechanical work. |
| **Audit** (new, lateral) | Independent red team. Adversarially attacks Council artifacts and Pipeline outputs; emits a separate score that **never** uses the same model or rubric as the original judge. | Async on every Council artifact + every Pipeline merge. | Per-attempt rows in `audit_findings`. | Independent verification. |

This is the canonical engineering-org metaphor (Strategy → Tactics → Execution + Independent Audit) realized as an agent system. It maps cleanly onto patterns from prior art:

- Anthropic's "lead-researcher + sub-researchers" structure (their published multi-agent research system) is a one-time instantiation of Council → Squad → Worker. We make it *persistent* per domain.
- The Society-of-Mind / Debate literature (Du et al., 2023, arXiv:2305.14325) shows multi-round critique improves factuality. Our Council Debate Mode operationalizes this.
- AutoGen's GroupChat + role agents are functionally squads, but in-process and ephemeral; we make them stateful and cluster-resident.
- Devin/Cognition publicly describe a "planner + executor" split with an internal critic; we make the critic an independent swarm so it cannot be co-opted by the editor's biases.

The aggregate effect is to turn the hive from "five agents in a daily meeting" into "three teams plus an external auditor with continuous standups," which is the smallest org structure that is empirically known to ship complex software at scale.

---

## 4. Gap analysis — what V1 cannot do, that V2 must

| Capability | V1 status | V2 must |
|---|---|---|
| **Domain expertise** | Every council run starts from zero on every domain. Reviewers are role-based (architect, security, …), not domain-based. | Persistent per-domain squads with a working memory and specialization manifest. |
| **Routing by evidence** | FIFO with dependency check. `policy.budgets.pipeline.max_concurrent_runs` is the only knob. | Outcome-driven routing: a backlog item with `path: internal/hud/frontend/**` is offered to the HUD-frontend squad first; if the squad's recent success rate in that path region is < 0.6, it falls back to the generic queue. |
| **Cross-repo coupling** | Single repo. Cross-repo work is invisible to the canonical store; coupled MRs require manual coordination. | Multi-repo backlog items with atomic merge gates; each repo gets a project ID; canonical store tracks `(backlog_id, repo, mr_iid)` tuples. |
| **Recursive decomposition** | Single fan-out level (`pkg/hive/pipeline/integrator.go`). Sub-slices must fit one worktree each. | Bounded recursion (depth ≤ 2): a fan-out worker can itself fan out, with budget attributed up the tree and cycle detection. |
| **Independent audit** | The same FlexInfer judge that scores artifacts also informs the next council brief (Eval Loop C feeds back). Self-reinforcing biases possible. | Separate audit swarm with: different model pool (Claude Opus + Codex GPT-5 + local Llama 4 70B), different rubric, no read access to Council's prompt or sidecar (only the artifact). |
| **Council debate** | Single-pass editor + reviewers. | Multi-round refinement (≤ 3 rounds), budget-capped, falls back to single-pass when tight. (V1.1 deferral #4.) |
| **Adaptive gate thresholds** | Static thresholds in `policy.gates.*`. | Per-gate auto-tuning: gates with > 95% pass rate over 100 runs auto-relax to advisory; gates with > 50% fail rate auto-promote to require human review. Decisions persisted in `policy_history`. |
| **Council replay / A-B** | Replay only via re-running the cron with the same `roadmap.sha`. | First-class replay: re-run a council brief with a different ensemble (e.g., swap Claude Opus for Codex GPT-5) and compare sidecar deltas + downstream outcomes. (V1.1 deferral #6.) |
| **Pre-spawn cost estimate** | Cost is logged after the fact. | Pre-flight estimator (uses `eval_scores` history + sidecar slice count + path-class budget priors) renders an estimated $ in the HUD before "Approve". (V1.1 deferral #9.) |

---

## 5. External prior art (pinned URLs)

- Anthropic — *How we built our multi-agent research system* (orchestrator + lead-researcher + sub-researchers; the canonical industry analog of Council → Squad → Worker): <https://www.anthropic.com/engineering/built-multi-agent-research-system>
- Anthropic Agent SDK — typed streaming events: <https://docs.anthropic.com/en/api/agent-sdk/overview>
- OpenAI Codex SDK — `Thread.runStreamed`: <https://platform.openai.com/docs/codex>
- Microsoft AutoGen — GroupChat + role agents (close to squads but ephemeral): <https://microsoft.github.io/autogen/stable/user-guide/core-user-guide/design-patterns/group-chat.html>
- Du, Li, Torralba et al., *Improving Factuality and Reasoning in Language Models through Multiagent Debate*, arXiv:2305.14325 (basis for Council Debate Mode): <https://arxiv.org/abs/2305.14325>
- Madaan et al., *Self-Refine*, arXiv:2303.17651 (per-round refinement contract): <https://arxiv.org/abs/2303.17651>
- Cognition / Devin announcement (planner + executor + internal critic split): <https://www.cognition.ai/blog/introducing-devin>
- GitHub *Agent HQ* (production fleet w/ policy + budget + audit; closest commercial analog of v2): <https://github.blog/news-insights/product-news/github-agent-hq/>
- Sourcegraph *Cody Code Agents* (multi-agent search → plan → patch): <https://sourcegraph.com/blog/code-agents>
- Temporal *child workflows* (canonical model for bounded recursion with replay): <https://docs.temporal.io/concepts/what-is-a-workflow#child-workflows>
- Argo Workflows *DAG with `dependsOn`* (cross-repo atomicity reference): <https://argo-workflows.readthedocs.io/en/latest/walk-through/dag/>
- Kubernetes *operator pattern* — multi-cluster operator approaches via `cluster.x-k8s.io` (cross-repo federation analog): <https://cluster-api.sigs.k8s.io/>
- LangGraph *supervisor + workers* pattern (analogous to squad structure in graph runtime): <https://langchain-ai.github.io/langgraph/concepts/multi_agent/>
- Karpathy on "agent harnesses" and the move from per-task to per-domain context (talk transcripts at canonical YouTube URLs vary; cite Karpathy's *State of AI 2024* — concept reference, not load-bearing).

---

## 6. The shape of Hive v2

```
                ┌─────────────────────────────────────────────────────────────┐
                │                       LOOM HIVE v2                          │
                │                  (loom-hive-operator @ k3s)                 │
                │                                                             │
   ROADMAP +    │   ┌────────────────┐                                        │
   incidents +  │   │   COUNCIL      │  daily; emits brief + sidecar          │
   merges       │   │ (debate mode)  │──────────────────────┐                 │
   ───────────► │   └────────┬───────┘                      │                 │
                │            ▼                              ▼                 │
                │   ┌────────────────────────────────────────────────────┐    │
                │   │                  SQUAD ROUTER                      │    │
                │   │  consults specialization_manifests +               │    │
                │   │  evidence_index (success-rate-by-(squad,path,gate))│    │
                │   └────────┬─────────┬───────────┬───────────┬─────────┘    │
                │            ▼         ▼           ▼           ▼              │
                │   ┌────────────┐ ┌────────────┐ ┌──────────┐ ┌──────────┐   │
                │   │ HUD-FRONT  │ │ WEAVER     │ │ GITOPS   │ │ MOBILE   │   │
                │   │  squad     │ │  squad     │ │  squad   │ │  squad   │   │
                │   │ ┌────────┐ │ │            │ │          │ │          │   │
                │   │ │ memory │ │ │  …         │ │  …       │ │  …       │   │
                │   │ │ + spec │ │ │            │ │          │ │          │   │
                │   │ └────────┘ │ │            │ │          │ │          │   │
                │   └────┬───────┘ └────┬───────┘ └────┬─────┘ └────┬─────┘   │
                │        │              │              │            │         │
                │        ▼              ▼              ▼            ▼         │
                │   ┌──────────────────────────────────────────────────────┐  │
                │   │              PIPELINE (per backlog item)             │  │
                │   │   plan-slice → impl₁ … implₙ (recursive ≤ 2) →       │  │
                │   │   tests → gates → mr → ci-watch → merge              │  │
                │   │   budget tree attributed up                          │  │
                │   └─────────────────────┬────────────────────────────────┘  │
                │                         │                                   │
                │                         ▼                                   │
                │   ┌──────────────────────────────────────────────────────┐  │
                │   │         CROSS-REPO INTEGRATOR (atomic merge)         │  │
                │   │   coordinates (loom-core, loom, flexdeck, …) MRs;    │  │
                │   │   blocks merge until all per-repo gates green        │  │
                │   └─────────────────────┬────────────────────────────────┘  │
                │                         │                                   │
                │                         ▼                                   │
                │   ┌──────────────────────────────────────────────────────┐  │
                │   │   AUDIT SWARM (lateral, independent ensemble)        │  │
                │   │   adversarially attacks: artifacts, sidecars,        │  │
                │   │   gate verdicts, merged diffs                        │  │
                │   │   → audit_findings table + audit_survival_rate KPI   │  │
                │   └──────────────────────────────────────────────────────┘  │
                │                         │                                   │
                │                         ▼                                   │
                │   ┌──────────────────────────────────────────────────────┐  │
                │   │   ADAPTIVE POLICY ENGINE                             │  │
                │   │   reads kpi_snapshots + audit_findings + eval_scores │  │
                │   │   suggests policy diffs for human apply              │  │
                │   │   (v2.0: human-applied; v2.1: auto-applied with      │  │
                │   │    revert window)                                    │  │
                │   └──────────────────────────────────────────────────────┘  │
                └─────────────────────────────────────────────────────────────┘
```

Key invariants kept from v1:
- Cluster-only runtime; the Mac is a client.
- Canonical SQLite is the source of truth; YAML/GitLab are derived.
- Every artifact human-readable + version-controlled.
- Eval Loops A/B/C still run; Audit is **additional**, not a replacement.
- All new agent calls go through the existing `weaver` router or `spawn` controller — no new auth path.

---

## 7. Tier-by-tier capability list (V2)

### 7.1 Squad layer — what it must do

1. **Own a domain manifest.** Each squad has a YAML (`platform/gitops/k3s/hive/squads/<name>.yaml`) declaring:
   - `paths`: glob patterns (e.g., `internal/hud/frontend/**`, `mcp/skills/hud-*`)
   - `tests`: which `devbox_quality_gate` lanes apply (e.g., `pnpm-typecheck`, `pnpm-vitest`)
   - `gates`: which gates are mandatory vs. advisory for this domain (e.g., `pr_self_review` mandatory, `coverage` advisory)
   - `default_ensemble`: which models lead vs. review (squads can diverge: HUD-frontend prefers Claude Opus for editor; gitops prefers Codex for terraform-style precision)
   - `budget_share`: fraction of the daily pipeline budget reserved (sum across squads ≤ 1.0; remainder is generic)
2. **Maintain working memory.** Per-squad rows in a new `squad_memory` table holding: recent merges (last 30), tech-debt items (last 50), conventions (e.g., "always use `writeFileAtomic` for watched files" — sourced from `pkg/skills/fileops.go`), open follow-ups. Working memory is appended on every merge attributed to the squad; pruned by importance score on a weekly job.
3. **Decompose Council slices into squad-shaped sub-slices.** When the Council emits a slice that lands inside a squad's `paths`, the squad's planner agent re-decomposes it using the squad's conventions and recent context. Output: a refined sidecar slice list, gates, and budget request.
4. **Self-veto when out of confidence.** If the squad's recent success rate in this path-class is < 0.6, it returns a `route:generic` recommendation. The router falls back to the unspecialized pipeline.
5. **Surface to humans.** Each squad gets a HUD card showing: success rate over last 30 merges, in-flight DAGs, top tech-debt items, top recent decisions.

### 7.2 Audit swarm — what it must do

1. **Be independent.** Different model pool than the editor and the Eval Loop A judge. Default v2.0 audit pool: Claude Opus 4.7 + Codex GPT-5 + local Llama 4 70B Instruct via FlexInfer. The exact list is policy-driven so it can rotate.
2. **Read only artifacts, not prompts.** Audit agents see the *output* (council artifact + sidecar, pipeline merged diff, gate verdict explanations) — never the prompts that produced them. This forces them to evaluate the artifact on its own merits.
3. **Use a different rubric.** The audit rubric (in `pkg/hive/audit/rubric/audit_v1.md`) emphasizes failure modes the editor judge does not score: hidden assumptions, deletion-by-omission, missing tests for edge cases the spec mentions, accidental coupling between slices, plan-vs-actual drift on merged work.
4. **Emit a survivability score.** Each artifact gets `audit_survival_rate` ∈ [0, 1]. Below 0.6, the artifact is flagged for human review (council artifact: opens an MR even if `.loom/`-only auto-merge is on; pipeline merged diff: opens a follow-up issue with the audit findings).
5. **Be cheap.** The audit swarm runs on FlexInfer for 80% of its calls; only the most ambiguous cases (audit verdict in 0.4–0.7 band) escalate to a frontier review. Per-day audit budget capped at 20% of council budget.

### 7.3 Cross-repo integrator — what it must do

1. **Multi-repo backlog item.** A backlog item gets a `repos: [{ project_id: 47, branch: "feat/x" }, …]` block. Stored as JSON in `backlog_items.repos_json` (new column).
2. **Per-repo MR + cross-repo gate.** The pipeline opens an MR per repo (one worktree per repo). After all per-repo CIs are green, a new `cross_repo_atomic_merge` gate fires; it merges all MRs in dependency order or rolls back atomically (revert MRs created in reverse order if any fails).
3. **Repo registry.** `platform/gitops/k3s/hive/repos.yaml` lists known repos with their GitLab project IDs, default branch, and an `auto_merge` flag. Gated behind `policy.cross_repo.enabled: false` until v2.1.
4. **Reuse `mcp-gitlab` for everything.** No new auth path. Canonical store gains a `cross_repo_runs` table to track atomicity state.

### 7.4 Council Debate Mode — what it must do

1. **Round 0.** Editor proposes (as today).
2. **Round 1.** Each reviewer critiques (as today). At end of round, an independent FlexInfer "moderator" reads all critiques and decides whether the editor's draft already addresses them; if yes, exit early (saves cost).
3. **Rounds 2–3.** Editor revises in light of critiques; reviewers may issue *focused* re-critiques limited to a narrower section of the artifact.
4. **Budget cap.** Total debate budget is `policy.council.debate.max_usd` (default $8); when 80% consumed, debate exits and emits the latest editor draft.
5. **Sidecar carries debate transcript.** New sidecar field `debate.rounds[]` with each round's critique → revision deltas (paths + line ranges, not full diffs, to keep the sidecar small).
6. **Single-pass fallback.** If `policy.council.debate.enabled: false`, behavior is exactly v1.

### 7.5 Bounded recursion — what it must do

1. **Pipeline workers can call back into the operator.** A `tools/spawn-driver` worker has access to `mcp-hive::pipeline_subrun_create(parent_run_id, slice_spec, depth)` (new MCP tool).
2. **Depth cap.** `policy.pipeline.max_recursion_depth` (default 2). Attempts beyond cap fail loudly with `recursion_depth_exceeded`.
3. **Budget tree.** Each subrun's budget is allocated from the parent's remaining budget; no subrun can consume more than 60% of the parent's remainder. Budget exhaustion auto-escalates the parent to handoff.
4. **Cycle detection.** Subrun cannot re-enter a stage already in its parent's stack; checked via `parent_chain` field in `pipeline_runs`.
5. **Telemetry attribution.** New `parent_run_id` column on `pipeline_runs`; KPIs aggregate up the tree.

### 7.6 Adaptive policy engine — what it must do

1. **Read-only inputs.** `kpi_snapshots`, `eval_scores`, `audit_findings`, `gate_outcomes`.
2. **Output: policy diffs.** A scheduled job (Sunday 0500) produces a markdown diff suggesting policy changes (e.g., "relax `coverage` gate from required to advisory for `internal/hud/frontend/` based on 100/100 pass rate"). Diff is committed as `.loom/hive/policy_proposals/<date>.md`.
3. **Human apply (v2.0).** A maintainer reviews and merges the policy diff. The current policy file is the source of truth.
4. **Auto-apply with revert window (v2.1).** After 4 weeks of v2.0 stability, the engine can apply non-restrictive changes (relaxations only) automatically, with a 24h revert window during which any post-merge regression auto-reverts the policy diff.
5. **Never auto-restricts.** Tightening (advisory → required, lower budget caps) always requires human approval.

---

## 8. New telemetry contract (extends v1 §8)

```
loom_hive_squad_runs_total{squad, outcome="planned|delegated|self_vetoed|merged|failed"}
loom_hive_squad_success_rate{squad, path_class}
loom_hive_squad_budget_usd_total{squad}
loom_hive_audit_findings_total{severity="info|warn|critical", subject_kind="council|pipeline"}
loom_hive_audit_survival_rate{subject_kind, window="day|week"}
loom_hive_cross_repo_runs_total{outcome="success|partial|reverted"}
loom_hive_cross_repo_atomicity_violations_total
loom_hive_council_debate_rounds_total{round_index}
loom_hive_council_debate_cost_usd_total{round_index}
loom_hive_pipeline_recursion_depth_histogram
loom_hive_policy_proposals_total{kind="relax|tighten", applied="auto|human|pending"}
```

Derived KPIs for HUD:

| KPI | Formula | Target |
|---|---|---|
| **Squad ROI** | `(merged & not regressed in 24h) / squad_cost_usd_total` | trend ↑ per squad |
| **Audit survival rate** | `audit_survival_rate{window=week}` | > 0.85 steady state |
| **Cross-repo atomicity** | `1 - cross_repo_atomicity_violations / cross_repo_runs_total{outcome=*}` | > 0.99 |
| **Debate cost share** | `debate_cost_usd_total / council_cost_usd_total` | < 0.30 |
| **Recursion utilization** | p95 of `pipeline_recursion_depth_histogram` | between 1 and 2 |

---

## 9. Risks and mitigations

| # | Risk | Impact | Mitigation |
|---|---|---|---|
| R1 | Squads accumulate biases (squad-X always picks Claude → blind to Codex strengths) | Medium | Adaptive policy reviews squad ensemble choices weekly; force ensemble rotation if `audit_survival_rate` drops > 5pp vs. baseline |
| R2 | Audit swarm is itself biased (its rubric just reframes editor's biases) | High | Rubric review every release; rubric is publicly diffable in git; rubric-author and editor-author models must be from different vendors |
| R3 | Cross-repo atomic merge gate stalls indefinitely on a long CI in one repo | Medium | Per-repo timeout (default 60min); on timeout, full rollback (revert MRs); escalate to handoff |
| R4 | Bounded recursion creates fork-bomb under bug | Critical | Hard depth cap (2); budget tree enforced before any subrun spawn; reconciler kills runs whose budget tree exceeds parent allocation |
| R5 | Council debate burns cost without converging | Medium | Budget cap (`max_usd`) + early-exit heuristic (moderator); single-pass fallback |
| R6 | Adaptive policy auto-relaxes a gate that prevented a critical regression | Critical | v2.0: human-applied only; v2.1: auto-apply gated to relaxations + 24h revert window + path-class denylist (`platform/gitops/`, `pkg/security/`, `cmd/loomd/`) |
| R7 | Squad working memory drifts from reality (e.g., references a deleted file) | Low | Weekly memory pruner job validates references; stale entries auto-archived |
| R8 | Cross-repo work hits permissions issue (missing project token) | Low | Canonical store includes per-repo permission probe; missing access is a hard pre-check before any MR is opened |

---

## 10. Open decisions to resolve before spec

These are the v2 equivalents of v1's D1–D12. **Status: proposed; resolve in `93-product-spec-…md`.**

| # | Decision | Options | Recommendation |
|---|---|---|---|
| V2-D1 | Squad seed list | (a) HUD-frontend, weaver, gitops, mobile, mcp-fabric (5 squads) (b) start with 2 (HUD-frontend + gitops) and add others empirically | (b) — start with two well-bounded squads, add others when their backlog volume justifies |
| V2-D2 | Squad memory storage | (a) New tables in canonical store (b) Reuse `agent-context` memory APIs | (a) — keep hive self-contained; agent-context is for agent sessions, hive squads are operator-owned |
| V2-D3 | Audit pool composition | Frontier-only / FlexInfer-only / hybrid | hybrid — bulk on FlexInfer, escalate to frontier for ambiguous |
| V2-D4 | Cross-repo enabled by default | yes / no in v2.0 | no — ships behind `policy.cross_repo.enabled` flag, default false; flip to true after 4 weeks of dual-repo dogfooding (loom-core + loom) |
| V2-D5 | Council debate default | enabled / disabled | disabled by default; enable per-trigger (incident triggers always debate; cron triggers don't) |
| V2-D6 | Recursion default | enabled / disabled | disabled by default; enabled per-squad in `default_ensemble.recursion: true` |
| V2-D7 | Adaptive policy auto-apply | v2.0 manual / v2.1 auto-relax | v2.0 manual ships first; auto-relax behind a feature flag in v2.1 |
| V2-D8 | New tables location | new file `pkg/hive/store/migrations/002_v2.sql` / extend 001 | new file to keep migrations append-only and reviewable |
| V2-D9 | Audit failure mode | block merge / advisory only | advisory only in v2.0 (audit findings open follow-up issues); blocking in v2.1 once survivability KPIs prove low-noise |
| V2-D10 | Mobile parity (v1.1 deferral #1) | in v2 / defer | in v2 — Hive HUD view exists; mobile parity is one screen, ships in slice 5.x |

---

## 11. Sources

- `.loom/89-research-agent-swarm-council-pipeline-2026-04-25.md`
- `.loom/90-product-spec-agent-swarm-council-pipeline-2026-04-25.md`
- `.loom/91-implementation-plan-agent-swarm-council-pipeline-2026-04-25.md`
- `.loom/82-plan-headless-agent-fullstack-2026-04-07.md`
- `.loom/77-research-agentic-engineering-patterns-2026-04-05.md`
- Command: `git log --grep='hive\|Hive' --since='2026-04-25' --oneline`
- `pkg/hive/` (18.2k Go LOC; verified via `wc -l pkg/hive/**/*.go`)
- `cmd/loom-hive-operator/` (14 handler files)
- `cmd/mcp-mentatlab/templates/hive-default-pipeline.yaml`
- `internal/hud/frontend/src/lib/components/Hive/` (4 panels)
- `pkg/skills/fileops.go` (`writeFileAtomic` convention referenced)
- Anthropic multi-agent research: <https://www.anthropic.com/engineering/built-multi-agent-research-system>
- Anthropic Agent SDK: <https://docs.anthropic.com/en/api/agent-sdk/overview>
- OpenAI Codex SDK: <https://platform.openai.com/docs/codex>
- Du et al. arXiv:2305.14325: <https://arxiv.org/abs/2305.14325>
- Madaan et al. arXiv:2303.17651: <https://arxiv.org/abs/2303.17651>
- Microsoft AutoGen GroupChat: <https://microsoft.github.io/autogen/stable/user-guide/core-user-guide/design-patterns/group-chat.html>
- Cognition Devin: <https://www.cognition.ai/blog/introducing-devin>
- GitHub Agent HQ: <https://github.blog/news-insights/product-news/github-agent-hq/>
- Sourcegraph Cody Code Agents: <https://sourcegraph.com/blog/code-agents>
- Temporal child workflows: <https://docs.temporal.io/concepts/what-is-a-workflow#child-workflows>
- Argo DAG: <https://argo-workflows.readthedocs.io/en/latest/walk-through/dag/>
- Cluster API: <https://cluster-api.sigs.k8s.io/>
- LangGraph multi-agent: <https://langchain-ai.github.io/langgraph/concepts/multi_agent/>
