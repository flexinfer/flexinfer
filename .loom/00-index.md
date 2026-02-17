# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Research (HUD): `10-research.md`
- Research (Daemon/Proxy): `11-research-daemon-proxy.md`
- Research (Agentic workflows/OpenClaw): `13-research-agentic-workflows-openclaw.md`
- Research (Architecture refactor focus): `../docs/planning/2026-02-17-architecture-refactor-opportunities.md`
- Product spec: `20-product-spec.md`
- Implementation plan: `30-implementation-plan.md`
- Gap-to-backlog map: `31-gap-to-backlog-map.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`

## Current Goal

Three active near-term workstreams:

1. **Architecture stabilization after Feb 15-16 feature surge**
  - Decompose daemon tool-call pipeline (`internal/daemon/daemon.go`) to reduce cross-cutting churn.
  - Unify agent API contracts across HUD handlers, CLI fallback commands, and bridge adapters.
2. **Quality floor and observability**
  - Close remaining Roadmap Issue #2 coverage gaps (daemon lifecycle + devbox integration paths).
  - Start Roadmap Issue #12 (OTel trace export) on top of pipeline extraction.
3. **HUD maintainability for Fleet UX**
  - Split oversized Presence panel into tab-level components/stores to support Issue #13 safely.

## Success Criteria (Near-Term)

- Daemon call path has isolated middleware components with unit tests.
- Agent context/nudge endpoints share one contract model across HUD and CLI.
- Presence diagnostics and mutation logic are isolated from unrelated tab concerns.
- Coverage trend remains upward while these refactors land (no regression in current 30%+ baseline).

## Risks

- Refactoring cross-cutting daemon logic without preserving current behavior around cache/routing/recovery can introduce subtle regressions.
- HUD tab decomposition can introduce transient UI state bugs if polling ownership is not moved carefully.
- OTel rollout before contract stabilization may create duplicate instrumentation paths.

## Notes

- Workspace snapshot refreshed on 2026-02-17.
- Planning focus is now anchored in `docs/planning/2026-02-17-architecture-refactor-opportunities.md`.
