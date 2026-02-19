# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory (loom-mode + workflow tooling): `00-mcp-inventory.md`
- Research brief (core workflow upgrades): `10-research.md`
- Product spec (universal skills v2): `20-product-spec.md`
- Implementation plan (propagation + adoption): `30-implementation-plan.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`

## Current Goal

Standardize repeatable day-to-day engineering loops across Codex/Claude/Kilocode/Gemini so agents:

1. start with context recall + codebase index validation,
2. execute to ship-ready outcomes by default (hooks/tests/lint, commit/push, CI watch/fix),
3. leave durable agent-context handoff state.

## Success Criteria (Near-Term)

- Core skill definitions in `platform/gitops/mcp/context/skills-registry.yaml` encode repeatable loops for:
  - research
  - technical writing/planning
  - delivery/testing
  - troubleshooting/incidents
  - cross-agent context coordination
- Codebase indexing/search is explicit in planning/research/exploration skills.
- Backlog delivery includes executable local verification + CI retry loop.
- Skills are regenerated and synced across supported platforms.

## Current Risks

- Policy can drift if registry updates are not followed by `loom generate skills` + `loom sync skills all`.
- Hook/test parity varies by repo; helper scripts reduce but do not eliminate project-specific setup variance.
- Some servers remain lazy-start/not-running until first invocation; workflows must tolerate startup latency.

## Notes

- This pack was refreshed on 2026-02-19 for "universal workflow + propagation" work.
- Previous JobSearch roadmap artifacts remain in history/worklog for reference.
