#!/usr/bin/env python3
"""Offline projection: how well does the heuristic router (router.py) recover the
two-lane oracle on the 2026-06-17 model-compare runs?

This does NOT call any model. It joins:
  - the prompt datasets (prompts.json, prompts-hard.json) — for the prompt text,
  - the real per-item correctness (results/per-item-2026-06-17.json) — which item
    each lane got right/wrong in the live gfx1100 runs,
  - router.classify(prompt)                                  — the routing policy,

and computes, per tier and overall:
  - fast-only / reasoning-only / ROUTED / oracle accuracy,
  - what fraction of prompts the router sends to the (slow) reasoning lane,
  - the estimated mean answer-latency of each strategy (per-lane p50 as the
    per-item estimate).

The point: quantify whether a cheap content-only router captures the
best-of-both the per-lane data promised, and at what latency cost. See the
emitted report's caveats — this is an in-sample upper bound (the router's rules
encode failure modes observed on this very set), not a generalization claim.
"""
from __future__ import annotations

import json
import os
import sys

from router import FAST, REASONING, classify

HERE = os.path.dirname(os.path.abspath(__file__))


def _load(path: str) -> dict:
    with open(path if os.path.isabs(path) else os.path.join(HERE, path)) as f:
        return json.load(f)


def analyze() -> tuple[str, int]:
    peritem = _load("results/per-item-2026-06-17.json")
    fast_lane = peritem["fast_lane"]
    reasoning_lane = peritem["reasoning_lane"]

    lines: list[str] = []
    P = lines.append

    P("# Offline routing analysis — does a heuristic router beat either lane alone?")
    P("")
    P(f"Fast lane: `{fast_lane}` · Reasoning lane: `{reasoning_lane}`")
    P(
        "Source: real per-item correctness from the 2026-06-17 gfx1100 runs "
        "(`results/per-item-2026-06-17.json`); routing by `router.py`; no model "
        "calls. Latency is the per-tier per-lane p50 used as a per-item estimate."
    )
    P("")

    # accumulators across all tiers
    tot = dict(
        n=0,
        fast=0,
        reason=0,
        routed=0,
        oracle=0,
        to_reason=0,
        lat_fast=0.0,
        lat_reason=0.0,
        lat_routed=0.0,
    )
    misroutes: list[str] = []

    for tier, tinfo in peritem["tiers"].items():
        data = _load(tinfo["dataset"])
        wrong_fast = set(tinfo["wrong"][fast_lane])
        wrong_reason = set(tinfo["wrong"][reasoning_lane])
        lat_f = tinfo["latency_p50_s"][fast_lane]
        lat_r = tinfo["latency_p50_s"][reasoning_lane]

        n = fast_ok = reason_ok = routed_ok = oracle_ok = to_reason = 0
        lat_routed = 0.0
        for it in data["items"]:
            iid = it["id"]
            f_ok = iid not in wrong_fast
            r_ok = iid not in wrong_reason
            d = classify(it["prompt"])
            chosen_ok = r_ok if d.lane == REASONING else f_ok

            n += 1
            fast_ok += f_ok
            reason_ok += r_ok
            routed_ok += chosen_ok
            oracle_ok += f_ok or r_ok
            to_reason += d.lane == REASONING
            lat_routed += lat_r if d.lane == REASONING else lat_f

            if not chosen_ok:
                misroutes.append(
                    f"{tier}/{iid}: routed->{d.lane} [{d.rule}] but that lane was "
                    f"wrong (fast_ok={f_ok}, reason_ok={r_ok})"
                )

        P(f"## {tier} tier ({n} items, dataset `{tinfo['dataset']}`)")
        P("")
        P("| strategy | accuracy | mean answer latency |")
        P("|---|---|---|")
        P(
            f"| fast-only (`{fast_lane}`) | {fast_ok}/{n} ({100*fast_ok/n:.1f}%) | {lat_f:.2f}s |"
        )
        P(
            f"| reasoning-only (`{reasoning_lane}`) | {reason_ok}/{n} ({100*reason_ok/n:.1f}%) | {lat_r:.2f}s |"
        )
        P(
            f"| **routed (router.py)** | **{routed_ok}/{n} ({100*routed_ok/n:.1f}%)** | {lat_routed/n:.2f}s |"
        )
        P(f"| oracle (best per item) | {oracle_ok}/{n} ({100*oracle_ok/n:.1f}%) | — |")
        P("")
        P(
            f"Router sent **{to_reason}/{n} ({100*to_reason/n:.0f}%)** to the reasoning lane."
        )
        P("")

        tot["n"] += n
        tot["fast"] += fast_ok
        tot["reason"] += reason_ok
        tot["routed"] += routed_ok
        tot["oracle"] += oracle_ok
        tot["to_reason"] += to_reason
        tot["lat_fast"] += lat_f * n
        tot["lat_reason"] += lat_r * n
        tot["lat_routed"] += lat_routed

    n = tot["n"]
    P("## Combined (both tiers)")
    P("")
    P("| strategy | accuracy | mean answer latency |")
    P("|---|---|---|")
    P(
        f"| fast-only | {tot['fast']}/{n} ({100*tot['fast']/n:.1f}%) | {tot['lat_fast']/n:.2f}s |"
    )
    P(
        f"| reasoning-only | {tot['reason']}/{n} ({100*tot['reason']/n:.1f}%) | {tot['lat_reason']/n:.2f}s |"
    )
    P(
        f"| **routed** | **{tot['routed']}/{n} ({100*tot['routed']/n:.1f}%)** | **{tot['lat_routed']/n:.2f}s** |"
    )
    P(f"| oracle | {tot['oracle']}/{n} ({100*tot['oracle']/n:.1f}%) | — |")
    P("")
    P(
        f"Router sent {tot['to_reason']}/{n} ({100*tot['to_reason']/n:.0f}%) to the "
        f"reasoning lane overall."
    )
    P("")
    if misroutes:
        P("### Residual misroutes (router picked a lane that was wrong)")
        P("")
        for m in misroutes:
            P(f"- {m}")
        P("")
    else:
        P(
            "No residual misroutes: on this set the router picks a correct lane for "
            "every item (it recovers the full oracle)."
        )
        P("")

    P("## Caveats")
    P("")
    P(
        "- **In-sample upper bound.** `router.py`'s rules encode failure modes "
        "observed on *this* item set (reasoning over-enumerates counting, "
        "over-thinks abstract syllogisms). A clean recovery here proves the two "
        "lanes' errors are *content-separable*, not that the rules generalize. The "
        "real test is a held-out tier — build it before trusting these numbers in "
        "production."
    )
    P(
        "- **Latency is an estimate.** Per-item latency was not persisted; this uses "
        "the per-tier per-lane p50. The item set is also reasoning-heavy by "
        "construction, so the 'fraction to reasoning lane' overstates the cost on "
        "real traffic (mostly direct recall, which routes to the fast lane)."
    )
    P("- 28 items/tier — directional. Grow each tier for tighter confidence.")
    P("")

    report = "\n".join(lines) + "\n"
    n_bad = len(misroutes)
    return report, n_bad


def main(argv: list[str]) -> int:
    report, _ = analyze()
    out = None
    if "--out" in argv:
        out = argv[argv.index("--out") + 1]
    sys.stdout.write(report)
    if out:
        with open(out if os.path.isabs(out) else os.path.join(HERE, out), "w") as f:
            f.write(report)
        sys.stderr.write(f"\n[wrote {out}]\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
