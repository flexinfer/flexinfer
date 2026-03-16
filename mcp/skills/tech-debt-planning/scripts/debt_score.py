#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any


WEIGHTS = {
    "impact": 0.35,
    "risk_reduction": 0.30,
    "drag_reduction": 0.20,
    "effort_inverse": 0.15,
}


@dataclass
class DebtItem:
    item_id: str
    title: str
    component: str
    impact: float
    risk_reduction: float
    drag_reduction: float
    effort: float

    @property
    def effort_inverse(self) -> float:
        # effort scale is 1..5 where lower is better
        effort = max(1.0, min(5.0, self.effort))
        return (6.0 - effort) / 5.0

    @property
    def score(self) -> float:
        raw = (
            self.impact * WEIGHTS["impact"]
            + self.risk_reduction * WEIGHTS["risk_reduction"]
            + self.drag_reduction * WEIGHTS["drag_reduction"]
            + self.effort_inverse * WEIGHTS["effort_inverse"]
        )
        return round(raw * 100.0, 2)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Score technical debt items from JSON inventory and emit markdown ranking."
    )
    parser.add_argument("--input", required=True, help="Path to inventory JSON")
    parser.add_argument("--output", required=True, help="Path to output markdown")
    return parser.parse_args()


def _to_ratio(value: Any, field: str, item_id: str) -> float:
    try:
        v = float(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"item {item_id}: field '{field}' must be numeric") from exc

    if v < 0:
        raise ValueError(f"item {item_id}: field '{field}' must be non-negative")

    # Accept either 0..1 ratios or 1..5 scale.
    if v <= 1.0:
        return v
    if v <= 5.0:
        return v / 5.0

    raise ValueError(f"item {item_id}: field '{field}' must be in 0..1 or 1..5 range")


def _to_effort(value: Any, item_id: str) -> float:
    try:
        effort = float(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"item {item_id}: field 'effort' must be numeric") from exc

    if 1.0 <= effort <= 5.0:
        return effort
    raise ValueError(f"item {item_id}: field 'effort' must be in 1..5 range")


def load_items(path: Path) -> list[DebtItem]:
    raw = json.loads(path.read_text(encoding="utf-8"))
    data = raw.get("items") if isinstance(raw, dict) else raw

    if not isinstance(data, list):
        raise ValueError("input must be a JSON array or an object with an 'items' array")

    items: list[DebtItem] = []
    for idx, row in enumerate(data, start=1):
        if not isinstance(row, dict):
            raise ValueError(f"item {idx}: expected object")

        item_id = str(row.get("id") or row.get("item_id") or f"DEBT-{idx}")
        title = str(row.get("title") or "Untitled debt item")
        component = str(row.get("component") or row.get("area") or "unknown")

        items.append(
            DebtItem(
                item_id=item_id,
                title=title,
                component=component,
                impact=_to_ratio(row.get("impact"), "impact", item_id),
                risk_reduction=_to_ratio(
                    row.get("risk_reduction", row.get("risk")), "risk_reduction", item_id
                ),
                drag_reduction=_to_ratio(
                    row.get("drag_reduction", row.get("drag")), "drag_reduction", item_id
                ),
                effort=_to_effort(row.get("effort"), item_id),
            )
        )

    return sorted(items, key=lambda item: item.score, reverse=True)


def render_markdown(items: list[DebtItem]) -> str:
    lines = [
        "# Technical Debt Priority Ranking",
        "",
        "Scored using weighted model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%.",
        "",
        "| Rank | ID | Title | Component | Impact | Risk | Drag | Effort | Score |",
        "|---:|---|---|---|---:|---:|---:|---:|---:|",
    ]

    for rank, item in enumerate(items, start=1):
        lines.append(
            "| {rank} | {item_id} | {title} | {component} | {impact:.2f} | {risk:.2f} | {drag:.2f} | {effort:.1f} | {score:.2f} |".format(
                rank=rank,
                item_id=item.item_id,
                title=item.title.replace("|", "\\|"),
                component=item.component.replace("|", "\\|"),
                impact=item.impact,
                risk=item.risk_reduction,
                drag=item.drag_reduction,
                effort=item.effort,
                score=item.score,
            )
        )

    lines += [
        "",
        "## Suggested Cut Lines",
        "",
        "- Wave 1: top 20-30% by score, low dependency risk",
        "- Wave 2: next 30-40%, medium effort and moderate coupling",
        "- Wave 3: remaining strategic refactors with cross-team dependencies",
    ]
    return "\n".join(lines) + "\n"


def main() -> int:
    args = parse_args()
    input_path = Path(args.input).expanduser().resolve()
    output_path = Path(args.output).expanduser().resolve()

    items = load_items(input_path)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(render_markdown(items), encoding="utf-8")
    print(f"wrote ranking: {output_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
