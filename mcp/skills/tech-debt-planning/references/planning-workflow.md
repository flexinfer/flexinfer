# Tech Debt Planning Workflow Reference

## Goal

Create a debt plan that is evidence-based, rank-ordered, and execution-ready.

## Inventory JSON Shape

```json
{
  "items": [
    {
      "id": "DEBT-001",
      "title": "Stabilize flaky integration test harness",
      "component": "pkg/integration",
      "impact": 4,
      "risk_reduction": 5,
      "drag_reduction": 4,
      "effort": 2
    }
  ]
}
```

Notes:
- `impact`, `risk_reduction`, `drag_reduction` accept either `1..5` or `0..1`.
- `effort` is `1..5` (1 = easiest, 5 = hardest).

## Ranking Command

```bash
python ${SKILL_PATH}/scripts/debt_score.py --input .loom/tech-debt-inventory.json --output .loom/tech-debt-priority.md
```

## Planning Heuristics

- Prioritize debt with proven operational or delivery pain.
- Bundle highly-coupled debt into a single wave to avoid repeated churn.
- Keep each wave independently shippable.
- Avoid opening too many concurrent debt threads without ownership.
