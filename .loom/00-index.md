# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Research (mobile companion): `10-research.md`
- Research addendum (mobile roadmap/features, external): `13-research-mobile-roadmap-features-2026-02-19.md`
- Product spec (mobile companion): `20-product-spec.md`
- Implementation plan (mobile companion): `30-implementation-plan.md`
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

Plan and de-risk a companion iPhone/iPad app for loom-core that supports:
1. real-time monitoring of agents/sessions/health,
2. safe session control actions,
3. new session creation from mobile,
4. both LAN mode and gateway mode depending on deployment/use case.

## Near-Term Success Criteria

- Mobile scope is clearly bounded to operator workflows.
- Backend auth and policy gaps are explicit and prioritized before mutation rollout.
- API contracts and rollout milestones are documented and testable.
- Planning artifacts are source-backed and ready for implementation handoff.

## Risks

- HUD API is currently localhost-first and mostly unauthenticated for remote use.
- SSE/reconnect behavior on mobile networks may require additional resilience work.
- Mutation scope could expand too quickly without role/policy guardrails.

## Notes

- Context pack refreshed on 2026-02-23.
- Codebase indexing with embeddings failed (Morph 400); lexical fallback indexing completed successfully (`1717` files, `26930` chunks).
- This planning slice intentionally focuses on architecture/specs and does not include code implementation yet.

## Sources

- `.loom/00-mcp-inventory.md`
- `.loom/10-research.md`
- `.loom/20-product-spec.md`
- `.loom/30-implementation-plan.md`
- `internal/hud/app.go:317`
- `internal/hud/api_agent.go:79`
- `internal/hud/bridge/agent.go:1443`
