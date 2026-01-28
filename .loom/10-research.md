# Research Brief

## Problem

Planning and “what’s next” information is currently spread across `ROADMAP.md`, `docs/IMPLEMENTATION_STATUS.md`, and various spec/user/dev docs. We need a single planning entry point and a next-slice roadmap grounded in real operations.

## Questions

- What planning docs already exist, and which ones should remain canonical?
- What is the current feature inventory (shipped vs partial vs missing)?
- What should the next implementation series prioritize?

## Constraints

- Keep existing canonical docs in place; add planning docs without renames/moves.
- Keep planning docs “site-syncable” (plain Markdown).

## Method

- What you inspected (files/commands/tools) and why

- Listed existing docs and roadmap/status docs under `docs/`.
- Identified where navigation is defined (`docs/nav.yaml`) so planning docs are discoverable.

## Findings

- Canonical “status + roadmap” already exists:
  - `ROADMAP.md` (high-level)
  - `docs/IMPLEMENTATION_STATUS.md` (detail)
- There was no `docs/planning/` section; adding it improves discoverability and reduces “where should planning live?” ambiguity.

## Options

### Option A

- Pros:
- Cons:
- Risks:

### Option B

- Pros:
- Cons:
- Risks:

## Recommendation

Add `docs/planning/` as the canonical forward-looking planning home (index + feature inventory + next roadmap), and maintain deeper planning artifacts in `.loom/` for structured work and handoffs.

## Sources

Use stable references: workspace file paths with line numbers when possible (e.g. `src/foo.ts:42`), command outputs (include the command), and URLs.

- [S1] `ROADMAP.md`
- [S2] `docs/IMPLEMENTATION_STATUS.md`
- [S3] `docs/nav.yaml`
- [S4] `docs/README.md`
