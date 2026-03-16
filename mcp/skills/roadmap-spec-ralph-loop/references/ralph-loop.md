# RALPH Loop Reference

`RALPH` is a roadmap-to-delivery cycle:

- **R**eview roadmap/spec context
- **A**lign on next smallest shippable slice
- **L**and implementation
- **P**rove quality and CI
- **H**andoff and harvest context for the next cycle

## Typical Inputs

- `ROADMAP.md`
- active product spec / implementation plan docs
- recent ADRs and decision logs
- open questions and blocked tasks from agent-context

## Typical Outputs Per Slice

- merged/pushed code for one vertical slice
- passing verification and CI evidence
- updated roadmap/spec progress notes
- agent-context decisions/findings/tasks updated
- explicit handoff for next slice if needed

## Anti-Patterns to Avoid

- Taking on multiple slices in one loop
- Deferring test/lint/CI verification to the end of milestone
- Updating code without syncing roadmap/spec status
- Ending session without context summary
