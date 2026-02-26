# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Research (mobile companion): `10-research.md`
- Research addendum (mobile roadmap/features, external): `13-research-mobile-roadmap-features-2026-02-19.md`
- Product spec (mobile companion): `20-product-spec.md`
- Implementation plan (mobile companion): `30-implementation-plan.md`
- Implementation plan (agent trace + telemetry dashboards): `34-agent-trace-telemetry-dashboard-plan-2026-02-26.md`
- Mobile API draft: `../docs/MOBILE_COMPANION_API.md`
- Mobile security draft: `../docs/MOBILE_COMPANION_SECURITY.md`
- Historical roadmap mapping: `31-gap-to-backlog-map.md`
- Mobile backlog mapping: `32-mobile-gap-to-backlog-map.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`
- Tech debt inventory: `tech-debt-inventory.md` (all items resolved)
- Tech debt plan: `tech-debt-plan.md` (all 3 waves complete)
- Tech debt priority: `tech-debt-priority.md`

## Current Goal

Kick off an agent-focused observability track that delivers robust trace + telemetry dashboards using:
1. `pkg/mcpotel` for span-level tool-call visibility,
2. `pkg/mcplog` plus structured logging improvements for cross-signal correlation,
3. reproducible Grafana dashboard packs and rollout docs.

## Near-Term Success Criteria

- A verified baseline exists for current tracing coverage and logging format limitations.
- A staged rollout plan is documented for expanding `mcpotel` adoption across high-value MCP servers.
- Dashboard requirements are defined with concrete signal sources (traces/logs/metrics) and correlation paths.
- Planning artifacts are source-backed and implementation-ready.

## Risks

- Only a subset of MCP servers currently wire `mcpotel`, creating blind spots in cross-server analysis.
- `mcplog` currently emits text logs only, limiting structured correlation in Loki/Grafana pipelines.
- Dashboard location/ownership spans repos (`loom-core` + `platform/gitops`), so rollout can drift without explicit handoff.

## Notes

- This workstream was started in `codex/agent-trace-telemetry` from `origin/main` on 2026-02-26.
- Immediate objective is to lock the telemetry/dashboard execution plan before broad instrumentation changes.

## Sources

- `.loom/34-agent-trace-telemetry-dashboard-plan-2026-02-26.md`
- `pkg/mcpotel/tracer.go:38`
- `pkg/mcpotel/middleware.go:14`
- `pkg/mcplog/logger.go:17`
- `docs/DEVELOPER_GUIDE.md:149`
- Command: `cd ../loom-core-agent-trace-telemetry && rg -n 'github.com/crb2nu/loom/pkg/mcpotel' cmd/mcp-*/main.go`
