# Product Spec

## Summary

Create and maintain a single planning home for FlexInfer that:

1) consolidates existing roadmap/status docs into an easy index (`docs/planning/`), and
2) produces a prioritized “next series” feature plan grounded in real k3s operations.

## Goals

- Make it fast to answer: “what exists today?”, “what’s stable?”, “what should we build next?”
- Establish a repeatable planning workflow (`docs/planning/` + `.loom/`) for ongoing iteration.
- Prioritize reliability and operational clarity for v1alpha2 workflows.

## Non-Goals

- Rewrite the entire documentation set.
- Decide long-term multi-tenancy / enterprise scope prematurely.

## Users / Stakeholders

- Homelab operator (primary): wants reliable GPU scheduling, model routing, and low-toil ops.
- Contributors: want clear backlog slices and acceptance criteria.

## Requirements

### Functional

- Provide a feature inventory + next roadmap docs under `docs/planning/`.
- Produce a concrete near-term implementation plan with milestones and acceptance checks.

### Non-Functional

- Planning docs should be easy to “site-sync” (plain Markdown; consistent structure).
- Changes should be incremental and minimize churn across existing docs.

## UX / Flows

- “Where do I start?” → `docs/README.md` → `docs/planning/README.md` → `docs/planning/next-roadmap.md`

## Data / APIs

- Plans should reference the active APIs:
  - v1alpha2 `Model` (`ai.flexinfer/v1alpha2`)

## Rollout / Migration

- Add new planning docs without moving/renaming existing canonical docs.
- Update navigation (`docs/nav.yaml`) to include Planning section.

## Observability

- Logs:
- Metrics:
- Traces:

## Risks

- Planning docs drift from reality if not updated with releases/ops learnings.

## Open Questions

- What is the “compatibility contract” for v1alpha2 (breaking vs non-breaking changes)?

## Sources

- [S1] `ROADMAP.md`
- [S2] `docs/IMPLEMENTATION_STATUS.md`
- [S3] `docs/README.md`
- [S4] `docs/nav.yaml`
