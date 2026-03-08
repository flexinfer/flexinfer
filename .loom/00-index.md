# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Roadmap review + task carve-out (2026-03-07): `39-roadmap-review-and-task-carveout-2026-03-07.md`
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

## Current State (2026-03-07)

**Branch**: `main` at `4e57746`

**Planning baseline**:
- Workspace snapshot was regenerated on 2026-03-07.
- This session did not expose loom-mode MCP resources; inventory used CLI fallback via `loom tools list --json`.
- `codebase_memory__codebase_stats` was unavailable in-session (`Transport closed`), so this review used `rg`, direct file reads, and local test commands.

**Most important current review outcome**:
- The biggest remaining architecture gap is agent lifecycle contract/surface decomposition, not the old `PresencePanel.svelte` or `internal/devbox/backend/k8s.go` split targets.
- HUD cost/RBAC/OTel visibility is already implemented on `main`; those items need backlog/status cleanup more than feature delivery.
- Total repo statement coverage is now `39.7%`, so the coverage goal is a finish-line push rather than a large gap-fill program.

## Active Workstreams

| Track | Status | Key Docs |
|-------|--------|----------|
| Agent lifecycle contract convergence | Still open, highest-value refactor target | `39-roadmap-review-and-task-carveout-2026-03-07.md`, ROADMAP Issue `#21` |
| Daemon pipeline hardening | Narrow finish pass remains | `39-roadmap-review-and-task-carveout-2026-03-07.md`, ROADMAP Issue `#20` |
| Coverage push | Finish-line state: `39.7%` toward `40%+` | `39-roadmap-review-and-task-carveout-2026-03-07.md`, ROADMAP Issue `#2` |
| Daemon telemetry completion | Still open on daemon-side export/instrumentation | `39-roadmap-review-and-task-carveout-2026-03-07.md`, ROADMAP Issue `#12` |
| OpenAI Responses M2 | Foundation shipped, bounded follow-up remains | `36-implementation-plan-openai-responses-orchestration-2026-03-04.md`, `39-roadmap-review-and-task-carveout-2026-03-07.md` |
| Mobile companion | Historical parallel track; not the active repo-alignment focus for this pass | `30-implementation-plan.md`, `20-product-spec.md` |

## Risks

- Planning docs have drifted far enough from `main` to mis-prioritize work unless reconciled soon.
- Codebase-memory MCP transport was unavailable in this session, so future planning still needs an index-health follow-up.
- The agent lifecycle surface (`cmd/loom/cmd_agent.go`, `internal/hud/api_agent.go`, `internal/hud/bridge/agent.go`) remains large and high-churn.

## Sources

- `39-roadmap-review-and-task-carveout-2026-03-07.md`
- `ROADMAP.md` (2026-03-07 review)
- `docs/IMPLEMENTATION_STATUS.md` (2026-03-07 review)
- Command: `loom tools list --json`
- Command: `go test ./...`
- Command: `go test ./... -coverprofile=/tmp/loom-cover.out`
- Command: `go tool cover -func=/tmp/loom-cover.out | tail -1`
