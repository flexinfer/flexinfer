# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Research: `10-research.md`
- Product spec: `20-product-spec.md`
- Implementation plan: `30-implementation-plan.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`

## Current Goal

- [ ] Consolidate planning docs into `docs/planning/`.
- [ ] Build a current feature/status inventory grounded in repo docs + real k3s learnings.
- [ ] Define the next implementation series (controller hardening → activator hardening → routing/perf).

## Open Questions

- [ ] What is the v1alpha2 stability promise (what fields are safe to change without disruption)?
- [ ] Do we want a first-class “ModelSet” / “ReplicaPolicy” concept, or keep it “replicas on Model”?
- [ ] What is the proxy’s strict compatibility target (OpenAI endpoints/streaming semantics)?

## Risks

- [ ] Drift/immutability loops (Services/Deployments) causing reconciler errors → codify immutable field handling patterns.
- [ ] Serverless activation behavior is user-visible and easy to get wrong → add metrics + explicit budgets/backoff.
- [ ] Multi-replica scheduling correctness across heterogenous nodes → ensure selectors/affinities are stable.
