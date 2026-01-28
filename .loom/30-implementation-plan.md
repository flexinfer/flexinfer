# Implementation Plan

## Scope

- Establish canonical planning entry points:
  - `docs/planning/` for repo-facing planning docs
  - `.loom/` for deeper context pack artifacts
- Produce a near-term roadmap with prioritized milestones for “next series” work.

## Milestones

1) Planning docs shipped and discoverable (nav updated)
2) Feature inventory + gap analysis documented
3) Next implementation series defined (controller hardening → activator hardening → routing/perf)

## Plan

1. Create `docs/planning/` and add index + key planning docs.
2. Update `docs/nav.yaml` and `docs/README.md` to include Planning.
3. Populate `.loom/` context pack (MCP inventory + product spec + implementation plan).
4. Iterate on the roadmap into implementation-ready work items (acceptance criteria + test plan).

## Test Plan

- Docs build/render sanity:
  - Ensure all referenced docs paths exist.
  - Ensure `docs/nav.yaml` paths include new planning docs.

## Rollout / Backout

- Rollout: merge docs-only change; no runtime impact.
- Backout: revert commits; remove planning section from `docs/nav.yaml`.

## Acceptance Criteria

- `docs/planning/README.md` exists and points to canonical docs.
- `docs/nav.yaml` includes a Planning section.
- `.loom/` exists with an updated MCP inventory + product spec + plan.

## Risks / Dependencies

- Low risk; docs-only.

## Sources

- [S1] `docs/nav.yaml`
- [S2] `docs/README.md`
