# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Research (HUD): `10-research.md`
- Research (Daemon/Proxy): `11-research-daemon-proxy.md`
- Research (Agentic workflows/OpenClaw): `13-research-agentic-workflows-openclaw.md`
- Product spec: `20-product-spec.md`
- Implementation plan: `30-implementation-plan.md`
- Gap-to-backlog map: `31-gap-to-backlog-map.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`

## Current Goal

Five active workstreams:

1. **HUD UI/UX Overhaul**: M1/M2 complete, M3/M4 nearing finish.
2. **Remote MCP Transport**: shipped; remaining hardening work.
3. **RBAC + Audit Trail**: RBAC shipped, audit trail in-flight.
4. **Agent Context Budget Inspector (in progress)**: API + CLI inspection path implemented (`/api/agent/context-inspect`, `loom agent context-inspect`) with sectioned prompt accounting (system prompt, tools/schema, context entries, file injections, response budget) and HUD diagnostics rendering.
5. **Lane-Aware Nudge Queue (in progress)**: queue policy implemented with lane priority, cap/drop policies, debounce, queue status endpoint (`/api/agent/nudge-queue`), runtime policy controls (`/api/agent/nudge-queue-policy`), and HUD diagnostics + mutation controls in Presence.

## Success Criteria (Near-Term)

- Context inspector:
  - Stable API contract for HUD/CLI.
  - Prompt-section/tool-schema/file-level breakdown delivered in baseline.
  - Frontend surface shipped in HUD Presence diagnostics tab.
- Nudge queue:
  - Queue policy visible in HUD.
  - Per-lane metrics and dropped-summary behavior validated under load.
  - Optional runtime policy controls (admin/API + in-HUD actions) added.

## Risks

- Context inspector currently estimates tokens from stored context entries; it does not yet include full model prompt composition overhead.
- Debounce on nudge delivery can hide queued nudges briefly if heartbeat cadence is too fast.

## Notes

- This context pack was updated to include OpenClaw-informed operator ergonomics work and the initial implementation slice for backlog items 1 and 2.
