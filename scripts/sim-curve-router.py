#!/usr/bin/env python3
"""sim-curve-router.py — CC-6 backtest for context-curve scheduler.

Reads context-curve reports from the live ConfigMap
`flexinfer-context-curve-results`, simulates a synthetic workload, and
compares two routing policies:

  baseline      uniform-random across known lanes (approximates
                round-robin under a shared service label)
  curve-aware   for each request, pick the lane whose interpolated
                decode throughput at the request's prompt length is
                highest

Latency model per request:

    elapsed = prompt_tokens / prefill_tps(lane, prompt_len)
            + completion_tokens / decode_tps(lane, prompt_len)

Throughput at a prompt length is interpolated from the lane's curve
points. Two interpolation modes are evaluated:

  linear        piecewise-linear between adjacent points; clamp to the
                endpoints outside the measured range
  nearest       nearest-point selection; same clamping rule

Pass criteria (all must hold for at least one interpolation mode):

  - curve-aware p95 elapsed on the long subset ≥ 20% lower than baseline
  - curve-aware p95 elapsed on the short subset ≤ 5% higher than baseline
  - neither lane receives 0 or 100% of requests under curve-aware routing
    for either subset

Outputs a JSON report and prints a one-line summary verdict.

This is a planning/evidence tool. It does not modify cluster state.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import math
import os
import random
import statistics
import subprocess
import sys
from pathlib import Path
from typing import Iterable

DEFAULT_NAMESPACE = "flexinfer-system"
DEFAULT_CONFIGMAP = "flexinfer-context-curve-results"
DEFAULT_COMPLETION_TOKENS = 64


def now_iso() -> str:
    return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def load_configmap(name: str, namespace: str, kubectl: str) -> dict:
    """Fetch the ConfigMap with `kubectl get -o json` and return its parsed body."""
    cmd = [kubectl, "-n", namespace, "get", "configmap", name, "-o", "json"]
    proc = subprocess.run(cmd, capture_output=True, text=True, check=True)
    return json.loads(proc.stdout)


def load_curves_from_configmap(cm: dict) -> list[dict]:
    """Pull `flexinfer.context_curve.v1` reports out of a ConfigMap body.

    Returns a list of {"model", "report"} dicts, one per stored curve.
    The most recent report per model wins (by `completed_at`).
    """
    data = cm.get("data") or {}
    by_model: dict[str, dict] = {}
    for key, value in data.items():
        if not key.endswith(".json"):
            continue
        try:
            report = json.loads(value)
        except json.JSONDecodeError:
            continue
        if report.get("schema_version") != "flexinfer.context_curve.v1":
            continue
        model = report.get("model")
        if not model:
            continue
        existing = by_model.get(model)
        if existing is None or report.get("completed_at", "") > existing.get(
            "completed_at", ""
        ):
            by_model[model] = report
    return [{"model": m, "report": r} for m, r in sorted(by_model.items())]


def extract_points(report: dict) -> list[dict]:
    """Return only the `pass` points from a curve report, sorted by target context."""
    raw = report.get("context_curve", {}).get("points", []) or []
    points = []
    for point in raw:
        if point.get("status") != "pass":
            continue
        target = point.get("context_tokens_target")
        prefill = point.get("prefill_tokens_per_second_avg")
        decode = point.get("decode_tokens_per_second_avg")
        if target is None or prefill is None or decode is None:
            continue
        points.append(
            {
                "context_tokens_target": float(target),
                "prefill_tps": float(prefill),
                "decode_tps": float(decode),
            }
        )
    points.sort(key=lambda p: p["context_tokens_target"])
    return points


def interp(points: list[dict], field: str, x: float, mode: str) -> float:
    """Look up `field` at context length `x` using the given mode.

    Clamps outside the measured range to the nearest endpoint regardless of mode.
    Caller guarantees len(points) >= 1.
    """
    if x <= points[0]["context_tokens_target"]:
        return points[0][field]
    if x >= points[-1]["context_tokens_target"]:
        return points[-1][field]
    if mode == "nearest":
        # Pick the closer of the two surrounding points.
        for i in range(len(points) - 1):
            lo = points[i]
            hi = points[i + 1]
            if lo["context_tokens_target"] <= x <= hi["context_tokens_target"]:
                if (x - lo["context_tokens_target"]) <= (
                    hi["context_tokens_target"] - x
                ):
                    return lo[field]
                return hi[field]
        return points[-1][field]  # unreachable
    # linear (default)
    for i in range(len(points) - 1):
        lo = points[i]
        hi = points[i + 1]
        x0 = lo["context_tokens_target"]
        x1 = hi["context_tokens_target"]
        if x0 <= x <= x1:
            if x1 == x0:
                return lo[field]
            t = (x - x0) / (x1 - x0)
            return lo[field] + t * (hi[field] - lo[field])
    return points[-1][field]  # unreachable


def simulate_elapsed(
    prompt_tokens: int,
    completion_tokens: int,
    lane: dict,
    mode: str,
) -> float:
    """Predict elapsed seconds for `prompt_tokens`/`completion_tokens` on `lane`.

    Uses the lane's curve points and the chosen interpolation mode.
    """
    pts = lane["points"]
    prefill_tps = max(1e-6, interp(pts, "prefill_tps", prompt_tokens, mode))
    decode_tps = max(1e-6, interp(pts, "decode_tps", prompt_tokens, mode))
    return prompt_tokens / prefill_tps + completion_tokens / decode_tps


def pick_curve_aware(
    prompt_tokens: int,
    completion_tokens: int,
    lanes: list[dict],
    mode: str,
) -> str:
    """Return the model name whose simulated elapsed is lowest."""
    best_model = None
    best_elapsed = math.inf
    for lane in lanes:
        elapsed = simulate_elapsed(prompt_tokens, completion_tokens, lane, mode)
        # Deterministic tiebreak by model name when elapsed is identical.
        key = (elapsed, lane["model"])
        if best_model is None or key < (best_elapsed, best_model):
            best_model = lane["model"]
            best_elapsed = elapsed
    assert best_model is not None
    return best_model


def percentile(values: list[float], pct: float) -> float:
    """Return the `pct` percentile (0..100) using nearest-rank.

    For small N (<= 100) this is more honest than statistics.quantiles which
    interpolates and can hide tail behavior.
    """
    if not values:
        return 0.0
    s = sorted(values)
    if len(s) == 1:
        return s[0]
    k = max(0, min(len(s) - 1, math.ceil(pct / 100.0 * len(s)) - 1))
    return s[k]


def build_workload(
    rng: random.Random,
    short_count: int,
    long_count: int,
    short_range: tuple[int, int],
    long_range: tuple[int, int],
    completion_tokens: int,
) -> list[dict]:
    """Return a list of {prompt_tokens, completion_tokens, subset} dicts."""
    out = []
    for _ in range(short_count):
        out.append(
            {
                "prompt_tokens": rng.randint(short_range[0], short_range[1]),
                "completion_tokens": completion_tokens,
                "subset": "short",
            }
        )
    for _ in range(long_count):
        out.append(
            {
                "prompt_tokens": rng.randint(long_range[0], long_range[1]),
                "completion_tokens": completion_tokens,
                "subset": "long",
            }
        )
    rng.shuffle(out)
    return out


def evaluate_policy(
    workload: list[dict],
    lanes: list[dict],
    policy_pick: callable,
    mode: str,
) -> dict:
    """Apply `policy_pick(req, lanes, mode)` to every request, collect elapsed by subset.

    Returns:
        {
            "lane_distribution": {model: {"short": N, "long": N}},
            "elapsed": {"short": [..], "long": [..]},
        }
    """
    lane_index = {lane["model"]: lane for lane in lanes}
    elapsed = {"short": [], "long": []}
    lane_distribution: dict[str, dict[str, int]] = {
        lane["model"]: {"short": 0, "long": 0} for lane in lanes
    }
    for req in workload:
        chosen = policy_pick(
            req["prompt_tokens"], req["completion_tokens"], lanes, mode
        )
        lane = lane_index[chosen]
        t = simulate_elapsed(req["prompt_tokens"], req["completion_tokens"], lane, mode)
        elapsed[req["subset"]].append(t)
        lane_distribution[chosen][req["subset"]] += 1
    return {"lane_distribution": lane_distribution, "elapsed": elapsed}


def baseline_pick(rng: random.Random):
    """Return a policy-pick callable that ignores the curve and picks uniformly."""

    def _pick(prompt_tokens, completion_tokens, lanes, mode):
        return rng.choice(lanes)["model"]

    return _pick


def summarize_run(
    workload: list[dict],
    lanes: list[dict],
    mode: str,
    baseline_rng_seed: int,
) -> dict:
    """Run baseline + curve-aware over the same workload, return summary dict."""
    baseline_rng = random.Random(baseline_rng_seed)
    baseline = evaluate_policy(workload, lanes, baseline_pick(baseline_rng), mode)
    curve = evaluate_policy(
        workload,
        lanes,
        lambda pt, ct, l, m: pick_curve_aware(pt, ct, l, m),
        mode,
    )
    summary = {"mode": mode, "policies": {}}
    for label, result in [("baseline", baseline), ("curve_aware", curve)]:
        per_subset = {}
        for subset in ("short", "long"):
            elapsed = result["elapsed"][subset]
            per_subset[subset] = {
                "count": len(elapsed),
                "p50_elapsed_seconds": (statistics.median(elapsed) if elapsed else 0.0),
                "p95_elapsed_seconds": percentile(elapsed, 95),
                "max_elapsed_seconds": max(elapsed) if elapsed else 0.0,
                "mean_elapsed_seconds": (statistics.fmean(elapsed) if elapsed else 0.0),
            }
        summary["policies"][label] = {
            "per_subset": per_subset,
            "lane_distribution": result["lane_distribution"],
        }
    summary["delta_pct"] = {
        subset: pct_delta(
            summary["policies"]["baseline"]["per_subset"][subset][
                "p95_elapsed_seconds"
            ],
            summary["policies"]["curve_aware"]["per_subset"][subset][
                "p95_elapsed_seconds"
            ],
        )
        for subset in ("short", "long")
    }
    summary["verdict"] = check_pass_criteria(summary)
    return summary


def pct_delta(baseline: float, candidate: float) -> float:
    """Return percentage change: positive means candidate is slower."""
    if baseline <= 0:
        return 0.0
    return (candidate - baseline) / baseline * 100.0


def check_pass_criteria(summary: dict) -> dict:
    """Apply the three CC-5 pass criteria to a per-mode summary."""
    long_delta = summary["delta_pct"]["long"]  # curve_aware vs baseline, percent
    short_delta = summary["delta_pct"]["short"]
    long_pass = long_delta <= -20.0  # curve-aware is ≥ 20% lower on long
    short_pass = short_delta <= 5.0  # curve-aware is ≤ 5% higher on short
    distribution = summary["policies"]["curve_aware"]["lane_distribution"]
    lanes = list(distribution.keys())
    degenerate = False
    degenerate_reason = ""
    if len(lanes) < 2:
        degenerate = True
        degenerate_reason = "fewer than 2 lanes available"
    else:
        for subset in ("short", "long"):
            totals = [distribution[m][subset] for m in lanes]
            total = sum(totals)
            if total == 0:
                continue
            if min(totals) == 0:
                degenerate = True
                degenerate_reason = f"one lane received 0 requests in subset={subset}"
                break
            if max(totals) == total:
                degenerate = True
                degenerate_reason = (
                    f"one lane received 100% of subset={subset} requests"
                )
                break
    overall_pass = long_pass and short_pass and not degenerate
    return {
        "long_subset_p95_delta_pct": long_delta,
        "short_subset_p95_delta_pct": short_delta,
        "long_subset_pass": long_pass,
        "short_subset_pass": short_pass,
        "degenerate_split": degenerate,
        "degenerate_reason": degenerate_reason,
        "overall_pass": overall_pass,
    }


def lanes_from_curves(curves: list[dict]) -> list[dict]:
    lanes = []
    for entry in curves:
        report = entry["report"]
        pts = extract_points(report)
        if len(pts) < 1:
            continue
        lanes.append(
            {
                "model": entry["model"],
                "points": pts,
                "completed_at": report.get("completed_at", ""),
                "run_id": report.get("run_id", ""),
                "git_sha": report.get("git_sha", ""),
            }
        )
    return lanes


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--configmap", default=DEFAULT_CONFIGMAP)
    parser.add_argument("--namespace", default=DEFAULT_NAMESPACE)
    parser.add_argument("--kubectl", default=os.environ.get("KUBECTL", "kubectl"))
    parser.add_argument(
        "--curves-from",
        help=("Optional path to a JSON file holding ConfigMap body (skip kubectl)."),
    )
    parser.add_argument("--short-prompts", type=int, default=80)
    parser.add_argument("--long-prompts", type=int, default=20)
    parser.add_argument(
        "--short-range",
        default="256,2048",
        help="Min,max prompt tokens for the short subset (inclusive)",
    )
    parser.add_argument(
        "--long-range",
        default="4096,14000",
        help="Min,max prompt tokens for the long subset (inclusive)",
    )
    parser.add_argument(
        "--completion-tokens", type=int, default=DEFAULT_COMPLETION_TOKENS
    )
    parser.add_argument("--seed", type=int, default=20260525)
    parser.add_argument(
        "--models",
        help=(
            "Comma-separated list of model names to restrict the simulation "
            "to. Use this to test substitutable lanes (lanes sharing a "
            "service label), as required by the CC-5 spec. If omitted, every "
            "lane with a curve in the ConfigMap is used."
        ),
    )
    parser.add_argument(
        "--report",
        help="Optional path to write the JSON report to.",
    )
    parser.add_argument(
        "--print-summary",
        action="store_true",
        default=True,
        help="Print a one-line verdict summary to stderr (default on).",
    )
    args = parser.parse_args()

    short_lo, short_hi = (int(x) for x in args.short_range.split(","))
    long_lo, long_hi = (int(x) for x in args.long_range.split(","))

    if args.curves_from:
        cm = json.loads(Path(args.curves_from).read_text(encoding="utf-8"))
    else:
        cm = load_configmap(args.configmap, args.namespace, args.kubectl)

    curves = load_curves_from_configmap(cm)
    lanes = lanes_from_curves(curves)
    if args.models:
        wanted = {m.strip() for m in args.models.split(",") if m.strip()}
        lanes = [lane for lane in lanes if lane["model"] in wanted]
        missing = wanted - {lane["model"] for lane in lanes}
        if missing:
            print(
                json.dumps(
                    {
                        "event": "sim_curve_router_abort",
                        "reason": "requested models not found in ConfigMap",
                        "missing_models": sorted(missing),
                    }
                ),
                file=sys.stderr,
            )
            return 2
    if len(lanes) < 2:
        print(
            json.dumps(
                {
                    "event": "sim_curve_router_abort",
                    "reason": "need at least 2 lanes with passing curves",
                    "found_models": [c["model"] for c in curves],
                }
            ),
            file=sys.stderr,
        )
        return 2

    rng = random.Random(args.seed)
    workload = build_workload(
        rng,
        args.short_prompts,
        args.long_prompts,
        (short_lo, short_hi),
        (long_lo, long_hi),
        args.completion_tokens,
    )

    runs = {}
    for mode in ("linear", "nearest"):
        runs[mode] = summarize_run(workload, lanes, mode, baseline_rng_seed=args.seed)

    any_pass = any(runs[m]["verdict"]["overall_pass"] for m in runs)
    report = {
        "schema_version": "flexinfer.context_curve_sim.v1",
        "created_at": now_iso(),
        "lanes": [
            {
                "model": lane["model"],
                "completed_at": lane["completed_at"],
                "run_id": lane["run_id"],
                "git_sha": lane["git_sha"],
                "points": lane["points"],
            }
            for lane in lanes
        ],
        "workload": {
            "short_count": args.short_prompts,
            "long_count": args.long_prompts,
            "short_range": [short_lo, short_hi],
            "long_range": [long_lo, long_hi],
            "completion_tokens": args.completion_tokens,
            "seed": args.seed,
        },
        "runs": runs,
        "verdict": {
            "any_mode_passes": any_pass,
            "best_mode": pick_best_mode(runs),
            "criteria": "see CC-5 spec docs/planning/context-curve-scheduler-spec.md",
        },
    }

    output = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if args.report:
        Path(args.report).parent.mkdir(parents=True, exist_ok=True)
        Path(args.report).write_text(output, encoding="utf-8")
    sys.stdout.write(output)

    if args.print_summary:
        for mode, run in runs.items():
            v = run["verdict"]
            print(
                f"[{mode:>7}] long_p95Δ={v['long_subset_p95_delta_pct']:+.1f}%  "
                f"short_p95Δ={v['short_subset_p95_delta_pct']:+.1f}%  "
                f"degenerate={v['degenerate_split']}  pass={v['overall_pass']}",
                file=sys.stderr,
            )
        print(
            f"VERDICT: any_mode_passes={any_pass} best_mode={pick_best_mode(runs)}",
            file=sys.stderr,
        )

    return 0 if any_pass else 1


def pick_best_mode(runs: dict) -> str | None:
    """Return the mode that passes; tiebreak by larger negative long delta."""
    passing = [(m, r) for m, r in runs.items() if r["verdict"]["overall_pass"]]
    if not passing:
        return None
    passing.sort(key=lambda mr: mr[1]["verdict"]["long_subset_p95_delta_pct"])
    return passing[0][0]


if __name__ == "__main__":
    sys.exit(main())
