# Loom Context Pack

## Quick Links

- **HUD UI/UX Overhaul — Operator Inbox + Foundations planning trio (2026-05-15)**: `115-brainstorm-hud-ux-improvements-2026-05-15.md`, `116-product-spec-hud-ux-overhaul-2026-05-15.md`, `117-implementation-plan-hud-ux-overhaul-2026-05-15.md` — two-slice plan: Slice A (Action Toolkit + Triage Overview + F4-full theme sweep, ~1w) ships first; Slice B (decompose 5 monolith panels <300 lines, SSE migration, keyboard density, mobile/embed reflow, ~2w) follows. F6 IA reorg explicitly deferred.
- **Weaver ↔ Qwen3 (FlexInfer) ↔ HUD/App/Extension/Agent-Context/Mills planning trio (2026-05-08)**: `110-research-weaver-qwen3-integration-2026-05-08.md`, `111-product-spec-weaver-qwen3-integration-2026-05-08.md`, `112-implementation-plan-weaver-qwen3-integration-2026-05-08.md` — fixes the model-name mismatch (`gemma-4-turboquant`/`qwen3-8b-instruct`/`qwen3-8b` defaults vs. only `qwen3-8b-fast-7900xtx` Ready), introduces `pkg/aimodels` role resolver, brings up `qwen3-1p7b-tools-radeonvii` as Ready router, adds LiteLLM alias `qwen3-8b`, daemon model preflight + degraded surface, Mills research stage delegates to Weaver router (shadow → on), HUD/iOS/VS Code Weaver surfaces, agent-context tie-in. Eight slices, ~10d + 1wk soak.
- **EPIC 3 — Reduce Config Complexity planning trio (2026-05-07)**: `106-research-reduce-config-complexity-2026-05-07.md`, `107-product-spec-reduce-config-complexity-2026-05-07.md`, `108-implementation-plan-reduce-config-complexity-2026-05-07.md` ([Issue #67](https://gitlab.flexinfer.ai/services/loom-core/-/issues/67) — chassis already 70% data-driven via `pkg/generator/platform_profiles.yaml`; ~1,050 LOC residual hand-written tail across 4 slices: CONFIG-1 hooks templates / CONFIG-2 extras / CONFIG-3 policies YAML / CONFIG-4 Codex preamble template).
- **Roadmap reconciliation + next-epic plan (2026-05-07)**: `105-planning-roadmap-reconciliation-and-next-epics-2026-05-07.md` — confirms Spectator Phases 0–5 + EPIC 2 Batches A–D shipped (13/14 UNIFY slices); identifies S4 (UNIFY-1d HUD handler migration) + Spectator Phase 6 + Harbor #1 final as close-out slices. **Correction**: SIMP-1..SIMP-12 actually shipped 2026-03-01..03 (commits `6bd95261`..`e050516f`); EPIC 1 #65 is a stale tracker, not a fresh planning target. Pivoted next-epic recommendation to **EPIC 3 (#67)**.
- **EPIC 2 — Unify Visibility planning trio (2026-05-06)**: `102-research-unify-visibility-2026-05-06.md`, `103-product-spec-unify-visibility-2026-05-06.md`, `104-implementation-plan-unify-visibility-2026-05-06.md` ([Issue #66](https://gitlab.flexinfer.ai/services/loom-core/-/issues/66) — UNIFY-3 shipped `d0d1518`; Batches A–D shipped via [!304](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/304), [!305](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/305), [!308](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/308), [!312](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/312); only **S4 (UNIFY-1d HUD handler migration)** remains)
- **✅ Incident: Harbor 401 + ops hygiene arc (2026-05-05 → 2026-05-06)**: original incident runbook in `100-incident-harbor-401-deployment-chain-2026-05-05.md`; followup plan + status in `101-harbor-incident-followup-plan.md`. **Followups #2/#3/#4 LIVE; #1 CI prereq landed; remaining loom-core Image* CRDs await design choice (see .loom/101).** Bonus arcs in same window: workspace-clean v2 + new `workspace-salvage` tool ([services/workspace-tooling](https://gitlab.flexinfer.ai/services/workspace-tooling) — new repo) and 27 stranded codex branches salvaged across the workspace.
- **Agent telemetry event bus + live spectator (2026-05-04)**: `97-research-agent-telemetry-spectator-2026-05-04.md`, `98-product-spec-agent-telemetry-spectator-2026-05-04.md`, `99-implementation-plan-agent-telemetry-spectator-2026-05-04.md` (Phase 0+1+2.3+4 shipped via [!284](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/284), [!286](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/286), [!288](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/288), [!290](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/290), [!291](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/291), [!292](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/292) — deployed to mobile-hud at `loom-core:28b64b65` after Harbor incident cleared)
- **Loom Mills v2 — Hierarchical Swarm + Adversarial Audit + Cross-Repo (2026-05-02)**: `92-research-mills-v2-hierarchical-swarm-2026-05-02.md`, `93-product-spec-mills-v2-hierarchical-swarm-2026-05-02.md`, `94-implementation-plan-mills-v2-hierarchical-swarm-2026-05-02.md` (✅ phases 1–8 shipped 2026-05-02 → 2026-05-04 + hive→mills rename; status snapshot archived as `.loom/archive/95-implementation-status-mills-v2-2026-05-02.md`)
- **Loom Mills v1 — Council + Pipeline (agent swarm meta-orchestration, 2026-04-25)**: `89-research-agent-swarm-council-pipeline-2026-04-25.md`, `90-product-spec-agent-swarm-council-pipeline-2026-04-25.md`, `91-implementation-plan-agent-swarm-council-pipeline-2026-04-25.md` (phases 1–6 shipped except 6.5 docs + 6.6 default-on flip; both rolled into v2 phase 0)
- **Session mgmt + spawn auth + weaver integration (2026-04-19)**: `86-research-session-spawning-weaver-integration-2026-04-19.md`, `87-product-spec-session-spawning-weaver-2026-04-19.md`, `88-implementation-plan-session-spawning-weaver-2026-04-19.md`
- **Phase 4 plan — Headless agent UX parity (2026-04-07)**: `83-plan-headless-agent-ux-parity-2026-04-07.md`
- **Phase 3 plan — Headless agent full-stack drive + canonical telemetry (2026-04-07)**: `82-plan-headless-agent-fullstack-2026-04-07.md`
- **Headless agent telemetry + SDK research (2026-04-06)**: `79-research-headless-agent-telemetry-sdk-2026-04-06.md`
- **Headless agent telemetry + SDK product spec (2026-04-06)**: `80-product-spec-headless-agent-telemetry-sdk-2026-04-06.md`
- **Headless agent telemetry + SDK implementation plan (2026-04-06)**: `81-implementation-plan-headless-agent-telemetry-sdk-2026-04-06.md`
- **Agentic engineering / dark factory patterns (2026-04-05)**: `77-research-agentic-engineering-patterns-2026-04-05.md`, `78-plan-dark-factory-patterns-2026-04-05.md`
- **Weaver hardening research (2026-04-04)**: `74-research-weaver-hardening-2026-04-04.md`
- **Weaver hardening product spec (2026-04-04)**: `75-product-spec-weaver-hardening-2026-04-04.md`
- **Weaver hardening implementation plan (2026-04-04)**: `76-implementation-plan-weaver-hardening-2026-04-04.md`
- **Productivity unlock plan (2026-04-03)**: `73-planning-productivity-unlocks-2026-04-03.md`
- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Technical debt inventory (cycle 6): `tech-debt-inventory-cycle6.md`
- Technical debt ranking (cycle 6): `tech-debt-priority-cycle6.md`
- Technical debt plan (cycle 6): `tech-debt-plan-cycle6.md`
- Research (mobile app + HUD polish, 2026-03-31): `70-research-mobile-hud-polish-2026-03-31.md`
- Product spec (mobile app + HUD polish, 2026-03-31): `71-product-spec-mobile-hud-polish-2026-03-31.md`
- Implementation plan (mobile app + HUD polish, 2026-03-31): `72-implementation-plan-mobile-hud-polish-2026-03-31.md`
- Implementation plan (universal hooks GitOps first, 2026-03-25): `62-implementation-plan-universal-hooks-gitops-2026-03-25.md`
- Implementation plan (planning baseline refresh, 2026-03-26): `63-implementation-plan-planning-baseline-refresh-2026-03-26.md`
- Planning (next-gen skills, agents, orchestration, 2026-03-29): `64-planning-next-gen-skills-agents-orchestration-2026-03-29.md`
- Technical debt plan (cycle 4): `tech-debt-plan-cycle4.md`
- Technical debt plan (cycle 5): `tech-debt-plan-cycle5.md`
- Research (developer setup flow, 2026-03-18): `19-research-developer-setup-flow-2026-03-18.md`
- Product spec (developer setup flow, 2026-03-18): `23-product-spec-developer-setup-flow-2026-03-18.md`
- Implementation plan (developer setup flow, 2026-03-18): `42-implementation-plan-developer-setup-flow-2026-03-18.md`
- Research (HUD/UX continuation, 2026-03-13): `18-research-hud-ux-continuation-2026-03-13.md`
- Product spec (HUD/UX continuation, 2026-03-13): `22-product-spec-hud-ux-continuation-2026-03-13.md`
- Implementation plan (HUD/UX continuation, 2026-03-13): `41-implementation-plan-hud-ux-continuation-2026-03-13.md`
- Research (codebase indexer benchmark operability, 2026-03-11): `17-research-codebase-indexer-benchmark-operability-2026-03-11.md`
- Research (mobile companion): `10-research.md`
- Research (OpenAI Responses + Loom tool/context integration): `15-research-openai-responses-tool-context-2026-03-04.md`
- Product spec (mobile companion): `20-product-spec.md`
- Product spec (OpenAI Responses orchestration): `21-product-spec-openai-responses-orchestration-2026-03-04.md`
- Implementation plan (mobile companion): `30-implementation-plan.md`
- Implementation plan (codebase indexer benchmark phase 2, 2026-03-11): `39-implementation-plan-codebase-indexer-benchmark-phase2-2026-03-11.md`
- Prior HUD/TUI slice plan: `54-ralph-iteration-plan-hud-tui-presence-2026-02-17.md`
- Prior HUD/TUI slice handoff: `55-ralph-slice-handoff-hud-tui-presence-2026-02-17.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`

## Current Planning Addendum (2026-05-07)

- **Just shipped (2026-05-06 → 2026-05-07): Spectator + EPIC 2 Unify closeout arc:**
  - Spectator Phase 2.2 ([!307](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/307)) Claude Code hooks → `loom agent event-emit`
  - Spectator Phase 2.2b/c ([!309](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/309)) Gemini + Codex event-emit
  - Spectator Phase 3 ([!310](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/310)) HUD `LiveSessionsCard`
  - Spectator Phase 5 ([!311](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/311)) iOS `LiveSessionsView`
  - EPIC 2 Batch C ([!308](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/308)) UNIFY-1b golden + UNIFY-2c OpenAPI + UNIFY-4c health
  - EPIC 2 Batch D ([!312](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/312)) UNIFY-4c list cmds + UNIFY-5 TUI panels + S14 runbook
  - HUD Operations Fleet polish ([!306](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/306))
  - bridge.* deprecated alias migration ([d97cf28b](https://gitlab.flexinfer.ai/services/loom-core/-/commit/d97cf28b)) — unblocked CI
- **Close-out slices (< 2 sessions total)** — recommended **before** the next epic:
  - **UNIFY-1d (S4)**: migrate `internal/hud/handlers/*.go` to `internal/visibility/contracts/{health,status,cost,presence}` packages. CI already green via the `d97cf28b` alias migration; this is the deeper handler walk. ~1 short session.
  - **Spectator Phase 6**: `loom spectate <session>` CLI streaming + multi-platform parity tests. ~1 day.
  - **Harbor incident followup #1 (final)**: loom-core `ImageRepository`/`ImagePolicy`/`ImageUpdateAutomation` CRDs in `platform/gitops/k3s/flux/image-automation/`. CI prereq shipped !299 — **one design choice (writeback target) blocks**; recommendation in `.loom/101`. ~30 min.
- **Open backlog (next-session candidates)** — pick from:
  - **Followup #1 of Harbor incident** (`.loom/101`): add loom-core `ImageRepository`/`ImagePolicy`/`ImageUpdateAutomation` to `platform/gitops/k3s/flux/image-automation/`. CI prereq (timestamp tag) shipped in [!299](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/299). One design choice (writeback target) blocks; recommendation in `.loom/101`. ~30 min.
  - **EPIC 1 — Simplify Agent Context** ([Issue #65](https://gitlab.flexinfer.ai/services/loom-core/-/issues/65)). 80 → ~45 MCP tools across 12 SIMP-N issues. Largest tool-surface debt cleanup.
  - **EPIC 2 — Unify Visibility** ([Issue #66](https://gitlab.flexinfer.ai/services/loom-core/-/issues/66)). Shared API contracts + embedded HUD across HUD/CLI/TUI. 5 UNIFY-N issues.
  - **OTel trace export from daemon** ([Issue #12](https://gitlab.flexinfer.ai/services/loom-core/-/issues/12)). Audit-backed summaries shipped 2026-04-14; next slice extends to OTel-compatible export + HUD percentile/waterfall views.
  - **Fleet orchestration UX** ([Issue #13](https://gitlab.flexinfer.ai/services/loom-core/-/issues/13)). Dispatch panel, file-claim conflict surfacing, merge orchestration assistance.
  - **Tier-3 / next-gen agentic** features from Mills v2 (audit-blocking promotion, default-on cross-repo, mobile Mills parity).
- **Just shipped (2026-05-05 → 2026-05-06): Harbor incident response + ops hygiene arc:**
  - Incident runbook + followup plan (`.loom/100`, `.loom/101`)
  - Followup #2 stale-pin alert ([gitops!89](https://gitlab.flexinfer.ai/platform/gitops/-/merge_requests/89))
  - Followup #3 robot expiry monitor ([gitops!88](https://gitlab.flexinfer.ai/platform/gitops/-/merge_requests/88) + [gitops!90](https://gitlab.flexinfer.ai/platform/gitops/-/merge_requests/90))
  - Followup #4 runner buildkit fallback ([gitops!87](https://gitlab.flexinfer.ai/platform/gitops/-/merge_requests/87))
  - Followup #1 CI timestamp tag prereq ([!299](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/299))
  - workspace-clean v2 (drift filter, unpushed-commit detection, noise-path skip, JSON `commits_at_risk`) + new `workspace-salvage` companion script
  - 27 stranded codex branches across the workspace pushed to origin (no work lost)
  - 52 dead worktrees swept (~1.3 GB reclaimed)
  - Skill registry updates ([!295](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/295), [!296](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/296)) — feature-dev/bugfix/repo-intake teach about `commits_at_risk`
  - New repo: [services/workspace-tooling](https://gitlab.flexinfer.ai/services/workspace-tooling) (workspace-clean + workspace-salvage + sundries, no longer laptop-bound)

## Prior Planning Addendum (2026-05-04, historical)

- **Active planning slice:** Agent telemetry event bus + live spectator (`97`/`98`/`99`). Foundation that future backchannel between sessions, adversarial-pair workflow, and skill-effectiveness leaderboard subscribe to. Reuses existing daemon EventBus + HUD SSE Hub + iOS SSE client; net new work is event schema additions, redaction primitive, hook wiring across Claude Code/Codex/Gemini, and a HUD `LiveSessionsCard`.
- **Just shipped (2026-05-02 → 2026-05-04):** Loom Mills v2 phases 1–8 + hive→mills rename. Plan was `92`/`93`/`94`; per-phase status snapshot archived at `.loom/archive/95-implementation-status-mills-v2-2026-05-02.md`. Closed gaps G-1..G-4 in [`7190754f`](https://gitlab.flexinfer.ai/services/loom-core/-/commit/7190754f) + subsequent slices.

### Prior Mills v2 planning addendum (historical, kept for reference)

- Active planning slice was: **Loom Mills v2 — Hierarchical Swarm.** Promote the v1 flat two-tier system (Council → Pipeline) to a true three-tier hierarchical swarm with persistent domain-owning **Squads**, an independent **Adversarial Audit** ensemble, **Cross-Repo Federation** with atomic merge, **Council Debate Mode** (v1.1 deferral #4), bounded **Pipeline Recursion** ≤ 2 (v1.1 deferral #7), an **Adaptive Policy** engine, **Cost Preview**, and **Mobile Mills parity** (v1.1 deferral #1). V1 phase 6.5 (docs/runbook/skill) and 6.6 (default-on flip) folded into v2 Phase 0. **All 8 phases shipped.**
- **Open gaps in shipped work** (resolved 2026-05-02 → 2026-05-04):
  - G-1: `pkg/mills/policy.go` `Policy` struct missing v2 fields (`Squads`, `Audit`, `CrossRepo`, `Debate`, `Recursion`, `AdaptivePolicy`)
  - G-2: `platform/gitops/k3s/mills/configmap-policy.yaml` still v1 schema (no v2 sections)
  - G-3: `mcp/skills/mills-ops/SKILL.md` is empty
  - G-4: policy schema version not bumped to 2
- **Spec-driven next sequence**: Slice A (G-1+G-2+G-4) ⫽ Slice B (G-3) → Slice C (Phase 4.1 registry) → Slices D/E/F (Phase 4.2–4.5 parallel) → Phase 5 → Phase 6 → Phase 7 → Phase 8.
- New planning docs:
  - `92-research-mills-v2-hierarchical-swarm-2026-05-02.md` — V1 ship state inventory, hierarchy thesis, capability gaps, prior-art mapping, v2 architecture, telemetry deltas, risks
  - `93-product-spec-mills-v2-hierarchical-swarm-2026-05-02.md` — V2-D1..V2-D10 decisions resolved; new SQLite migration `002_v2.sql`; squad manifests; audit rubric; cross-repo state machine; debate flow; recursion guards; adaptive proposal kinds; REST + MCP surface; success criteria
  - `94-implementation-plan-mills-v2-hierarchical-swarm-2026-05-02.md` — eight phases (0: close v1; 1: substrate; 2: squads; 3: audit; 4: cross-repo; 5: debate; 6: recursion; 7: adaptive + cost preview + mobile; 8: hardening + default-on)
- Locked V2 decisions (V2-D1..V2-D10):
  - **Two squads to start**: `hud-frontend` and `gitops`. Add others empirically.
  - **Squad memory in canonical store**, not agent-context. Mills remains self-contained.
  - **Hybrid audit pool**: FlexInfer (Llama 4 70B + Qwen 3 32B) bulk; escalate to frontier (Claude Opus + Codex GPT-5) only for ambiguous (0.4–0.7 band).
  - **Cross-repo disabled by default** in v2.0; flip after 4-week loom-core+loom dogfood.
  - **Debate enabled for incident triggers only** in v2.0; disabled for cron/roadmap-change.
  - **Recursion opt-in per squad** via squad manifest; depth cap 2.
  - **Adaptive policy is human-applied** in v2.0; v2.1 will auto-relax with 24h revert window and protected-path denylist (`platform/gitops/`, `pkg/security/`, `cmd/loomd/`).
  - **Audit advisory-only** in v2.0; blocking deferred to v2.1 once survival KPIs prove low-noise.
  - **Mobile parity in v2.0** (one read-only Mills screen).

## Current Planning Addendum (2026-04-25)

- Active planning slice (closed; folded into v2): **Loom Mills — Council + Pipeline.** Phases 1–6 shipped except 6.5 (docs) + 6.6 (default-on flip).
- New planning docs:
  - `89-research-agent-swarm-council-pipeline-2026-04-25.md` — current-state inventory, gap analysis, decisions (resolved 2026-04-25), persistence + cluster topology rationale, evaluation framework, risks
  - `90-product-spec-agent-swarm-council-pipeline-2026-04-25.md` — `cmd/loom-mills-operator/`, SQLite schema (canonical), policy file, MentatLab `mills-default-pipeline` template, REST + MCP surface, gates, eval framework, KPIs
  - `91-implementation-plan-agent-swarm-council-pipeline-2026-04-25.md` — six phases: (0) prerequisites, (1) cluster substrate (persistence + operator), (2) mills primitives, (3) council MVP + Eval Loop A, (4) pipeline MVP + Eval Loop B, (5) HUD + telemetry, (6) hardening + Eval Loop C + docs + default-on
- Locked decisions (D1–D12 resolved):
  - **Cluster-only.** Operator runs in k3s as `loom-mills-operator` deployment with PVC; Mac CLI is client only (laptop sleeps; mills doesn't).
  - **Persistence is canonical SQLite**, not YAML. `.loom/backlog/*.yaml` is a derived export; GitLab issues are a federated mirror; resilient to GitLab outages.
  - **Hybrid council ensemble**: frontier editor (Claude/Codex via spawn) + FlexInfer reviewers (heterogeneous models), editor pattern.
  - **Pipeline runs on MentatLab** (extend, not replace); per-DAG worktrees; reconcile-loop pickup; cap+queue budget.
  - **Three eval loops**: synchronous artifact judge (gates backlog mutation at score < 0.7), per-merge outcome attribution (Council ROI KPI), weekly cross-run consistency check (feeds next council brief).
  - **Policy-driven human override**: protected-paths default-deny; per-label `auto_merge` and `human_review` knobs.
  - **Council fast-merges `.loom/` only**; `ROADMAP.md`, skills-registry, code → MR for human review.
  - **Conservative v1 gate set**: lint/tests/diff-size/scope/path-policy/secret-scan/commit-format (pure-Go) + spec-conformance/pr-self-review (FlexInfer); no frontier inside gates.

## Current Planning Addendum (2026-04-06)

- Active planning slice: **Headless agent driving + SDK-based telemetry mapping**
- New planning docs:
  - `79-research-headless-agent-telemetry-sdk-2026-04-06.md` — Research on Claude Agent SDK, Codex SDK, current gaps, canonical model extensions
  - `80-product-spec-headless-agent-telemetry-sdk-2026-04-06.md` — Product spec with user journeys, technical design, metrics
  - `81-implementation-plan-headless-agent-telemetry-sdk-2026-04-06.md` — 8-slice implementation plan (Phase 1: Go JSONL parsing, Phase 2: SDK driver)
- Key insight: Both Claude Agent SDK (`@anthropic-ai/claude-agent-sdk`) and Codex SDK (`@openai/codex-sdk`) expose rich typed streaming event models. Loom currently discards all of this by treating agents as opaque CLI subprocesses.
- Approach: Hybrid — start with Go JSONL parsing of `--output-format stream-json` (Claude) and `--json` (Codex) for immediate telemetry, then add Node.js SDK drivers for multi-turn orchestration.
- Phase 1 (Slices 1-6): Pure Go, no new runtime deps, ~2 weeks
- Phase 2 (Slices 7-8): Node.js SDK sidecar for multi-turn control, ~1 week
- Execution stance:
  - Slices 1, 2+3, 6 parallelizable via worktrees
  - All new types are additive (`omitempty`) — no breaking changes to existing spawn API
  - Feature-flag SSE broadcast behind `Config.SpawnTelemetryEnabled` until stable

## Current Planning Addendum (2026-04-02)

- Active planning slice: Cycle 6 technical debt execution, with HUD/runtime decomposition now leading the queue.
- New planning docs:
  - `tech-debt-inventory-cycle6.md`
  - `tech-debt-priority-cycle6.md`
  - `tech-debt-plan-cycle6.md`
- Recent execution status:
  - `DEBT-062` landed: race CI no longer pulls `cmd/mcp-orchestra` into the headerless `fi-accel` lane
  - `DEBT-063` landed: iOS SwiftPM tests run cleanly again and the generated Xcode project now includes `AttentionLanesCard.swift`
  - `DEBT-064` is the next active slice: reduce `internal/hud/app.go` to a small composition root by moving startup wiring into focused helpers
- Execution stance for this wave:
  - keep structural changes mostly mechanical first
  - preserve HUD startup flags, route behavior, and monitor ordering while splitting code
  - continue verifying every slice with real local checks plus GitLab CI before merge

## Current Planning Addendum (2026-03-31)

- Active planning slice: UX and functionality polish across the iOS companion app and the HUD web dashboard.
- New planning docs:
  - `70-research-mobile-hud-polish-2026-03-31.md`
  - `71-product-spec-mobile-hud-polish-2026-03-31.md`
  - `72-implementation-plan-mobile-hud-polish-2026-03-31.md`
- Planning stance for this slice:
  - prioritize information architecture, operator flow clarity, and shared interaction patterns over adding net-new risky controls
  - keep mobile mutations scoped to already-approved high-confidence flows unless the product spec explicitly re-baselines that boundary
  - use direct file reads for HUD/mobile frontend evidence because `codebase_memory` still has Go-only coverage in this session
  - 2026-04-01 continuation slice landed on top of this plan: backend attention-lane contract enrichment, iOS dashboard "Next Attention" surfacing, and a more action-oriented HUD overview rail

## Current Planning Addendum (2026-03-18)

- Active planning slice: developer setup/onboarding improvement centered on a proposed first-class `loom setup` command.
- New planning docs:
  - `19-research-developer-setup-flow-2026-03-18.md`
  - `23-product-spec-developer-setup-flow-2026-03-18.md`
  - `42-implementation-plan-developer-setup-flow-2026-03-18.md`
- This session used CLI fallback for tool/runtime inventory because top-level MCP resource discovery returned empty results.

## Current State (2026-03-13)

**Branch**: `codex/hud-ux` at `df5707b`

**Current planning thread**:
- Fresh main-based worktree for continuing HUD and broader UX work without touching unrelated local edits in the primary checkout.
- This branch starts from `main` and keeps `codex/hud-view-fixes` as a reference lane instead of reusing it blindly.
- Immediate focus is shared HUD interaction polish plus roadmap-backed UX follow-through in fleet orchestration, server catalog, and HUD/TUI visibility parity.
- HUD layout/data-alignment slice `db3aa85` is now carried into the `codex/hud-ux` working tree and has passed a frontend build plus focused `internal/hud` Go tests.

**HUD/UX reference branch**:
- `codex/hud-view-fixes` remains available at `3b4949f` and is `10` commits ahead / `1` commit behind `main`.
- Its current diff touches `KnowledgePanel`, `SandboxPanel`, `ServersPanel`, `TasksPanel`, shared `FilterBar`, shared `DataTable`, plus HUD CI/build files, so it is a useful cherry-pick/review pool rather than something to merge wholesale.

**Tooling baseline**:
- Loom is running in loom-mode with `46` registered servers and `483` tools.
- `codebase_memory` is healthy for Go (`7861` indexed chunks) but still has `0` TypeScript/JavaScript chunks, so HUD frontend work needs direct file reads in addition to semantic search.

## Active Workstreams

| Track | Status | Key Docs |
|-------|--------|----------|
| HUD/UX continuation | New branch, planning ready | `18-research-hud-ux-continuation-2026-03-13.md`, `22-product-spec-hud-ux-continuation-2026-03-13.md`, `41-implementation-plan-hud-ux-continuation-2026-03-13.md` |
| HUD/TUI presence follow-through | Prior slice completed, follow-ups open | `54-ralph-iteration-plan-hud-tui-presence-2026-02-17.md`, `55-ralph-slice-handoff-hud-tui-presence-2026-02-17.md` |
| Agent lifecycle contract convergence | Open, highest-value refactor | `ROADMAP.md` issue `#21`, `codex/backlog/21` |
| Fleet orchestration UX | Open roadmap slice | `ROADMAP.md`, issue `#13` |
| Catalog/discovery UX | Open roadmap slice | `ROADMAP.md`, issue `#14` |
| Unify visibility epic | Planned | `35-simplification-epics.md`, issue `#66` |

## Risks

- `codex/hud-view-fixes` is not merge-ready as-is; it needs selective carry-forward or a rebase/cherry-pick pass.
- `ROADMAP.md` still points `.loom/10-research.md`, `.loom/20-product-spec.md`, and `.loom/30-implementation-plan.md` at HUD overhaul docs even though those files currently serve the mobile companion thread.
- Current semantic code indexing does not cover the HUD frontend, which raises the cost of broad TypeScript refactors unless the indexer is refreshed for JS/TS.

## Sources

- Command: `git worktree add -b codex/hud-ux ../loom-core-hud-ux main`
- Command: `git log --oneline --decorate --no-merges main..codex/hud-view-fixes`
- Command: `git diff --stat main..codex/hud-view-fixes`
- Command: `git cherry-pick --no-commit db3aa85`
- Command: `pnpm --dir internal/hud/frontend install --frozen-lockfile`
- Command: `pnpm --dir internal/hud/frontend build`
- Command: `go test ./internal/hud/... -count=1`
- Command: `git rev-list --left-right --count main...codex/hud-view-fixes`
- `ROADMAP.md:36`
- `ROADMAP.md:212`
- `ROADMAP.md:219`
- `ROADMAP.md:237`
- `internal/hud/frontend/src/App.svelte:35`
- `internal/hud/frontend/src/App.svelte:148`
