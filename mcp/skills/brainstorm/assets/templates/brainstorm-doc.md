# Brainstorm: <topic>

**Date**: <YYYY-MM-DD>
**Triggered by**: <one-line context — what was the user wrestling with?>
**Constraints noted**: <if any were stated upfront — budget, deadline, must-use-X, etc.>

## Phase 1 — Framings

### F1 — <short name>

<one paragraph describing the framing>

- **Bet**: <one line — what would have to be true for this to win>
- **Risk**: <one line — what kills it>

### F2 — <short name>

<one paragraph>

- **Bet**: <...>
- **Risk**: <...>

<...repeat for 5–8 framings...>

## Phase 2 — Cross-Pollinations & Tensions

### Combinations

- **F1 + F3**: <what new thing emerges>
- **F2 + F4**: <what new thing emerges>

### Tensions

- **F1 vs. F5**: <the axis the real decision lives on>

## Phase 3 — Convergence

### Recommended: <F# or combination>

<one paragraph on why this wins given current context>

### Runner-up: <F#>

<one paragraph on what would tip the choice the other way>

### Open question

<one question the user has to answer before the recommended path is actually chosen>

## Riskiest assumption + kill-test

> Every brainstorm-derived plan must surface its riskiest load-bearing
> assumption explicitly. See the `spec-riskiest-assumption` skill.

**Load-bearing assumption**: <one specific sentence — name the product,
version, host, feature. "Vendor X supports Y" is too vague; "Claude
Code Desktop 2.1+ renders MCP Apps widgets via `_meta.ui.resourceUri`"
is specific.>

**Kill test**: <a procedure another agent or human can run in ≤30
minutes that produces an observable, unambiguous outcome. Unit tests
that don't exercise the assumption end-to-end don't count.>

**Failure mode if wrong**: <what we'd be building toward that wouldn't
work — helps judge how much verification energy this assumption
deserves.>

**Status**: not run

> The downstream slice plan is BLOCKED until this kill-test passes.
> Pair it with at least one disconfirming-search query (look for
> "feature NOT supported in product" / "product limitations feature")
> before declaring the assumption verified.

## Handoff

- If chosen → next step is: `<plan-loom-core | feature-dev | research | other>`
- Linked spec/plan doc (fill in once it exists): `<.loom/NNN-...md>`
