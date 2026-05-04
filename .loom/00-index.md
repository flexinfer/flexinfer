# Loom Context Pack

## Quick Links

- **Loom Mills v2 — implementation status (2026-05-02)**: `95-implementation-status-mills-v2-2026-05-02.md` ← **current status + spec-driven next steps**
- **Loom Mills v2 — Hierarchical Swarm + Adversarial Audit + Cross-Repo (2026-05-02)**: `92-research-mills-v2-hierarchical-swarm-2026-05-02.md`, `93-product-spec-mills-v2-hierarchical-swarm-2026-05-02.md`, `94-implementation-plan-mills-v2-hierarchical-swarm-2026-05-02.md` ← **active planning slice**
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

## Current Planning Addendum (2026-05-02)

- Active planning slice: **Loom Mills v2 — Hierarchical Swarm.** Promote the v1 flat two-tier system (Council → Pipeline) to a true three-tier hierarchical swarm with persistent domain-owning **Squads**, an independent **Adversarial Audit** ensemble, **Cross-Repo Federation** with atomic merge, **Council Debate Mode** (v1.1 deferral #4), bounded **Pipeline Recursion** ≤ 2 (v1.1 deferral #7), an **Adaptive Policy** engine, **Cost Preview**, and **Mobile Mills parity** (v1.1 deferral #1). V1 phase 6.5 (docs/runbook/skill) and 6.6 (default-on flip) are folded into v2 Phase 0.
- **Implementation status (2026-05-02)** — see `95-implementation-status-mills-v2-2026-05-02.md`:
  - Phase 0.1 docs/runbook 🟡 partial (`docs/MILLS.md`, `docs/MILLS_RUNBOOK.md` shipped; `mcp/skills/mills-ops/SKILL.md` empty)
  - Phase 0.2 default-on 🟡 partial (ConfigMap `enabled: true`; no compile-time default)
  - Phase 1 substrate ✅ shipped (commit `20870464`)
  - Phase 2 squads ✅ shipped (commits `5222a95d`, `c4bd9f72`, `1655d1ac`)
  - Phase 3 audit ✅ shipped (commits `97549c97`, `174e0c14`, `a30d943f`)
  - Phases 4–8 ❌ not started
- **Open gaps in shipped work** (block Phase 8 flip; address before Phase 4):
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
