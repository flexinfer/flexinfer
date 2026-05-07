---
type: planning
date: 2026-05-07
title: Roadmap reconciliation and next-epic plan
related:
  - .loom/00-index.md
  - ROADMAP.md
  - .loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md
  - .loom/104-implementation-plan-unify-visibility-2026-05-06.md
  - .loom/35-simplification-epics.md
---

# Roadmap reconciliation + next-epic plan (2026-05-07)

> Today: 2026-05-07. Last roadmap refresh: 2026-05-06. Last `.loom/00-index.md` update: 2026-05-06.

## Purpose

Reconcile shipped work against open epics, identify integration gaps, and pick
the next 2–3 epics to plan in detail. Planning baseline for the next 1–2 weeks.

## Method

- Walked `git log` on `main` from `166468e6` back to `eb246ff5` (covers 2026-05-06
  → 2026-05-07).
- Cross-referenced `glab mr list --merged` (page 1) against open issues
  (`glab issue list`).
- Walked `git worktree list --porcelain` (74 worktrees, 50+ locked claude-*).
- Re-read `ROADMAP.md`, `.loom/00-index.md`, and the in-flight plan docs:
  - `.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md`
  - `.loom/104-implementation-plan-unify-visibility-2026-05-06.md`
  - `.loom/35-simplification-epics.md`
  - `.loom/101-harbor-incident-followup-plan.md`

## Integration state

### Just-shipped (since 2026-05-06 roadmap refresh)

| MR | Title | Phase / Slice |
|---|---|---|
| !307 | Claude Code hooks → `loom agent event-emit` | Spectator Phase 2.2 |
| !309 | Gemini + Codex event-emit | Spectator Phase 2.2b/c |
| !310 | HUD `LiveSessionsCard` | Spectator Phase 3 |
| !311 | iOS `LiveSessionsView` | Spectator Phase 5 |
| !304 | UNIFY-1a contracts + UNIFY-2a embed docs + UNIFY-4a render helpers | EPIC 2 Batch A (S1, S5, S8) |
| !305 | UNIFY-1c CLI status migration + UNIFY-2b `--embed` flag + UNIFY-4b `loom cost`/`rbac` | EPIC 2 Batch B (S3, S6, S9) |
| !308 | UNIFY-1b golden coverage + UNIFY-2c OpenAPI + UNIFY-4c `loom health` | EPIC 2 Batch C (S2, S7, S10 partial) |
| !312 | UNIFY-4c list cmds + UNIFY-5 TUI panels + S14 runbook | EPIC 2 Batch D (S10 finish, S11–S14) |
| !302 | `mobile-app-run-device` iOS auth bootstrap | Mobile reliability |
| !306 | HUD Operations Fleet view polish | HUD UX cleanup |
| d97cf28b | bridge.* deprecated alias migration | EPIC 2 cleanup (unblocked CI) |

### Epic completion status

| Epic | Status | Remaining |
|---|---|---|
| **Spectator (event bus + live sessions)** | Phases 0/1/2.1/2.2/2.2b/2.2c/2.3/3/4/5 ✅ | **Phase 6** — multi-platform CLI `loom spectate` + hardening |
| **EPIC 2 — Unify Visibility (#66)** | 13 of 14 slices ✅ | **Slice S4 (UNIFY-1d)** — migrate HUD handlers to contracts package (only deprecated-alias cleanup landed; full handler migration pending) |
| **Harbor 401 incident followups** | #2/#3/#4 ✅, #1 CI prereq ✅ | **#1 final** — loom-core `ImageRepository`/`ImagePolicy`/`ImageUpdateAutomation` CRDs (one design choice; ~30 min) |
| **Mills v2 (formerly hive-v2)** | Phases 1–8 ✅ | None on critical path; v2.1 audit-blocking + default-on cross-repo deferred |

### Worktree audit

- 74 linked worktrees in `loom-core`. ~50 are `claude/*` agent worktrees, mostly
  locked. Sample shows most have `<` upstream tracking (behind `origin/main`),
  meaning their work is integrated and the worktree is safe to remove.
- `workspace-clean --report --worktrees --repo loom-core` reports **0 MB
  reclaimable** (filter is conservative — locked worktrees are skipped). To
  actually reclaim disk, an explicit pass is needed:

  ```bash
  # 1. Inspect locked worktrees and unlock those whose branches are merged
  git worktree list --porcelain | grep -E "^worktree|^locked" | paste - -
  # 2. Drop the lock for merged claude/* worktrees
  for wt in $(git worktree list --porcelain | grep -B1 "^locked" | awk '/^worktree /{print $2}'); do
    branch=$(git -C "$wt" symbolic-ref --short HEAD 2>/dev/null)
    if git merge-base --is-ancestor "$branch" main 2>/dev/null; then
      echo "would unlock $wt (branch $branch merged)"
    fi
  done
  ```
  Recommend running this as a **separate hygiene task** rather than in this
  planning slice — moving locked-worktree state is risky to do during a planning
  pass.

### Stranded branches

- 88 branches not merged into `main`. Spot check: most `claude/*` branches
  (`<` upstream) carry MR-merged work via squash; the branch tip is "ahead" of
  `main` because squash merges leave the source branch dangling. No
  `commits_at_risk` flagged by `workspace-salvage` semantics in the dry-run
  pass.
- A handful of pre-Mills v2 `feat/hive-v2-*` branches still exist. They were
  superseded by the hive→mills rename and the v2 phase 8 cleanup. Safe to
  delete after one final visual pass.

## Remaining slices in active epics (small)

These are < 1 day each and close out epics already 80–95% shipped. Land these
**before** kicking off new epics so the cognitive overhead per session stays
low.

### S4 — UNIFY-1d: migrate HUD handlers to contracts package
- Source: `.loom/104-implementation-plan-unify-visibility-2026-05-06.md` slice S4.
- What's done: bridge.* deprecated aliases migrated (commit `d97cf28b`); CI is
  green on staticcheck.
- What remains: walk `internal/hud/handlers/*.go` and convert direct
  `bridge.{ServerHealth,StatusResult,CostStatsResult,...}` references to the
  `internal/visibility/contracts/{health,status,cost,presence}` packages.
  Estimated 1 short session.
- Gate: `golangci-lint run ./...` clean; HUD smoke loads identical payloads.

### Spectator Phase 6 — CLI spectate + hardening
- Source: `.loom/99-implementation-plan-agent-telemetry-spectator-2026-05-04.md`
  Phase 6.
- What remains: `loom spectate <session>` CLI streaming events from the daemon;
  rate-limit + redaction validation tests; multi-platform parity check across
  Claude/Codex/Gemini event emit. Estimated 1 day.
- Gate: integration test that runs all three CLIs side-by-side and verifies
  consistent event shape in the bus.

### Harbor incident followup #1 (final)
- Source: `.loom/101-harbor-incident-followup-plan.md`.
- What remains: add `ImageRepository`/`ImagePolicy`/`ImageUpdateAutomation` CRDs
  for loom-core under `platform/gitops/k3s/flux/image-automation/`. CI
  timestamp tag prereq shipped in !299 — Flux can now sort. **One design
  choice blocks**: writeback target (loom-core repo vs gitops-only). The
  recommendation in `.loom/101` is gitops-only writeback to keep loom-core
  commit history clean. Estimated ~30 min once decided.

## Next epics to plan

Three candidates, in recommended priority order. Pick **one or two** to write
research+spec+plan trios for next.

### Candidate 1 (revised 2026-05-07): EPIC 1 — Simplify Agent Context tool surface (#65) — *MOSTLY SHIPPED*

**Correction**: my initial framing here was wrong. Verified via `git log` on 2026-05-07: SIMP-1..SIMP-12 *did* ship as the external tool-reduction epic, not just service decomposition. Issue #65 is a stale tracker.

| Slice | Commit | What |
|---|---|---|
| SIMP-1 | `6bd95261` | Unified `agent_recall` (scope: context/memory/all); deprecated 3 legacy recall tools |
| SIMP-2 | `47ff9fd6` | Removed 5 manual memory lifecycle tools |
| SIMP-3 | `aa5d2847` | Removed memory export/import tools |
| SIMP-4 | `7c5f27dc` | Knowledge graph 13→9 tools |
| SIMP-5 | `b24dc1e1` | Merged code annotations into context entries |
| SIMP-6 | `e256ac35` | Removed compaction/reconcile (CLI-only) |
| SIMP-7 | `ea222ca8` | Removed template tools (CLI-only) |
| SIMP-8 | `242794f4` | Removed low-utility context tools |
| SIMP-9 | `af45bf39` | Unified recall facade across context/memory/graph |
| SIMP-10 | `04665153` | Unified store facade with durability routing |
| SIMP-11 | `26bc96c7` | gofmt cleanup |
| SIMP-12 | `e050516f` | Qdrant 14→12+1 collections |

**Current tool count**: 64 MCP tools registered (counted `server.AddTool` across `cmd/mcp-agent-context/tools_*.go`). Original target was ~45; ~19 tools above target. The high-value low-hanging consolidations are done.

**Next move on #65**: close the issue with a status comment linking the SIMP commits, OR scope a small "residual consolidation" follow-up if the 45 target is still meaningful. Either way, this is **not** a research-trio-class epic anymore.

**Research questions**:
1. Current tool inventory: which 80 tools, and how often is each one called?
   (Pull from daemon audit log — 30-day window.)
2. Overlap map: which tools have functional duplicates (e.g.
   `agent_context_recall` vs `agent_context_recall_enhanced` vs
   `agent_context_recall_since` — already 3 variants of one operation).
3. Facade design: is `agent_recall` (one tool, scoped backends) viable, or do
   the existing variants serve genuinely different access patterns?
4. Deprecation path: which tools to mark `// Deprecated:` first, with
   compatibility shims for downstream consumers (loom VS Code extension,
   mobile, Claude/Codex/Gemini configs).

**Estimated effort**: 1 research session + 1 spec session + 4–6 implementation
slices over ~1 week.

### Candidate 2: EPIC 3 — Reduce config complexity with data-driven profiles (#67)

**Why second**: every new platform integration today requires a new
`pkg/generator/<platform>` Go package with hand-written serialization. A
data-driven profile system (YAML manifests + a single generator) collapses
that to "add a YAML file." High leverage for future platform support
(Antigravity, Kilocode, future agents).

**State today**:
- 6 platforms generated today: Claude, Codex, Gemini, Zed, VS Code, Kilocode,
  Antigravity (and partial Cursor).
- `services/loom-core/mcp/context/registry.yaml` is already partially
  data-driven for tool listings.
- Generator code is the residual hand-written piece that needs flattening.

**Research questions**:
1. What's the canonical MCP config shape across all 6 platforms? Where do
   they truly diverge (TOML vs JSON vs YAML, hook lifecycle, env
   propagation)?
2. Can a single profile schema cover all of them, or do we need a
   platform-class taxonomy (CLI/IDE/editor)?
3. What's the migration sequence — one platform at a time or big-bang?

**Estimated effort**: 1 research session + 1 spec session + 4 CONFIG-N
implementation slices.

### Candidate 3: OTel trace export expansion (#12)

**Why third**: audit-backed trace summaries shipped 2026-04-14 (HUD `Traces`
panel works). Industry expects OTel-compatible export so traces flow into
Datadog/Jaeger/Honeycomb. This is the smallest remaining chunk on the Tier 2
"capture market gaps" line.

**State today**:
- `pkg/mcpotel` instrumentation across all `cmd/mcp-*/main.go` ✅
- `pkg/mcplog` `MCP_LOG_FORMAT=json` with trace_id/span_id correlation ✅
- Daemon audit-backed summaries ✅ (HUD Traces panel)
- **Missing**: OTLP exporter wired to a real collector; HUD percentile +
  waterfall views; tool-call latency histograms.

**Research questions**:
1. OTLP gRPC vs HTTP — pick one for v1 based on collector deployment
   ergonomics.
2. Span schema: what attributes does the daemon emit today vs what enterprise
   collectors expect (semantic conventions for AI)?
3. HUD percentile/waterfall UI — extend `Traces` panel or new panel?

**Estimated effort**: 1 research session + 1 spec session + 3–4
implementation slices.

## Recommendation (revised 2026-05-07)

1. **Land remaining-epic close-out slices first** (UNIFY-1d, Spectator Phase
   6, Harbor #1 final). 1.5–2 sessions total. Frees mental space.
2. **Close stale issue #65 (EPIC 1)** with a status comment linking SIMP-1..12
   commits and the 64-tool current count. Decide separately whether the
   residual 64→45 gap merits a smaller follow-up.
3. **Plan EPIC 3 (Reduce Config Complexity, #67)** next — research + spec +
   plan trio in `.loom/106`/`107`/`108`. Truly unstarted, high leverage for
   future platform integrations.
4. **Defer OTel** (#12) to follow-on trio after EPIC 3 has its first batch on
   `main`.

## Stretch / hygiene side-tasks

These are **not** epic-class but are worth scheduling alongside whatever
plans next:

- **Worktree triage**: identify the ~50 merged-but-locked `claude/*`
  worktrees, unlock + remove (or write a `workspace-clean` extension that
  unlocks merged worktrees automatically).
- **Stale `feat/hive-v2-*` branch cleanup**: 12+ branches superseded by
  Mills v2 phase 8. Delete after one visual review.
- **Refresh `.loom/00-index.md`**: append today's planning doc and update the
  "Open backlog" addendum to reflect this triage.

## Acceptance criteria for this planning slice

- [ ] This doc (`.loom/105`) lands on `main` (or a `docs/planning-…` branch).
- [ ] `.loom/00-index.md` updated with `105` link and current addendum.
- [ ] `ROADMAP.md` "Recently Shipped" section gains a 2026-05-07 entry for
      the spectator + UNIFY batch closeout.
- [ ] User picks one of (Candidate 1, Candidate 2, Candidate 3) to plan
      next; that selection unblocks a research-doc starter
      (`.loom/106-research-…`).
