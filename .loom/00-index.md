# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Research (mobile companion): `10-research.md`
- Research (OpenAI Responses + Loom tool/context integration): `15-research-openai-responses-tool-context-2026-03-04.md`
- Research addendum (mobile roadmap/features, external): `13-research-mobile-roadmap-features-2026-02-19.md`
- Product spec (mobile companion): `20-product-spec.md`
- Product spec (OpenAI Responses orchestration): `21-product-spec-openai-responses-orchestration-2026-03-04.md`
- Implementation plan (mobile companion): `30-implementation-plan.md`
- Implementation plan (agent trace + telemetry dashboards): `34-agent-trace-telemetry-dashboard-plan-2026-02-26.md`
- Implementation plan (OpenAI Responses orchestration): `36-implementation-plan-openai-responses-orchestration-2026-03-04.md`
- Mobile API draft: `../docs/MOBILE_COMPANION_API.md`
- Mobile security draft: `../docs/MOBILE_COMPANION_SECURITY.md`
- Historical roadmap mapping: `31-gap-to-backlog-map.md`
- Mobile backlog mapping: `32-mobile-gap-to-backlog-map.md`
- Simplification EPICs (3 EPICs, 21 issues): `35-simplification-epics.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`
- Tech debt inventory: `tech-debt-inventory.md` (all items resolved)
- Tech debt plan: `tech-debt-plan.md` (all 3 waves complete)
- Tech debt priority: `tech-debt-priority.md`

## Current State (2026-02-27)

**Branch**: `main` at `7ac4131` (QdrantRegistry refactor)

**Active dirty workstream**: Hub-failover resilience + mcp-hub-wrapper integration
- `internal/daemon/callpipeline.go` — prefer-hub routing with automatic local fallback and backoff
- `internal/daemon/routing.go` — backoff suppression methods for hub routing
- `pkg/generator/configs.go` — hub wrapper binary resolution with multi-source candidate discovery
- `Makefile` — adds `mcp-hub-wrapper` to build/install targets
- Comprehensive test coverage for all new paths
- Appears commit-ready

**Recently shipped (last 5 commits on main)**:
1. QdrantRegistry refactor for agent-context (#51)
2. Workflow deep-copy fixes + decomp hints for HUD
3. Call pipeline unit tests (DEBT-016)
4. Workflow false-condition gating + recursive item injection
5. Decomp hints for large responses + map_reduce clone

## Active Workstreams

| Track | Status | Key Docs |
|-------|--------|----------|
| Hub-failover resilience | In progress (dirty) | `callpipeline.go`, `routing.go` |
| Call pipeline hardening (DEBT-016) | Stage 2 complete | ROADMAP.md, Issue #20 |
| Agent trace/telemetry dashboards | Phase 1-2 complete (59/59 traced, JSON logs) | `34-agent-trace-telemetry-dashboard-plan-2026-02-26.md` |
| Mobile companion (iOS) | M2 in progress, M0-M4 backend done | `30-implementation-plan.md`, `20-product-spec.md` |
| Test coverage push | 30.4%, target 40% | ROADMAP.md, Issue #2 |
| Agent contract convergence | Stage 1 complete | ROADMAP.md, Issue #21 |

## Risks

- Hub-failover dirty changes need to land before further daemon work to avoid conflicts.
- Codebase index was empty at session start (rebuilding now); semantic search unavailable until complete.
- Telemetry dashboard Phase 3 (Grafana packs) spans repos (`loom-core` + `platform/gitops`).

## Sources

- `git log --oneline -15` (2026-02-27)
- `git diff --stat HEAD` (2026-02-27)
- `ROADMAP.md` (2026-02-27)
- `.loom/34-agent-trace-telemetry-dashboard-plan-2026-02-26.md`
- Subagent exploration of dirty changes (2026-02-27)
