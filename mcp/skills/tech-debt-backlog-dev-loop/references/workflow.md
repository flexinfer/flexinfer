# Tech Debt Backlog Dev Loop Reference

## Intent

Ship debt reductions in small, verified increments while minimizing regression risk.

## Debt Slice Definition

A valid debt slice should:
- target one primary pain point
- have explicit acceptance criteria
- include a verification plan
- be independently revertible

## Implementation Patterns

- Prefer characterization tests before refactoring unfamiliar/high-risk code.
- Favor extraction + adapter patterns over broad signature churn.
- If schema/state migration is needed, split migration from refactor when possible.

## Closure Criteria

- Local checks pass (`verify_local_loop.sh`)
- CI checks pass (`verify_ci_loop.sh`)
- Debt item record updated with outcome and residual risk
- Session summary stored for future recall
