#!/usr/bin/env python3
"""Heuristic task-router for the flexinfer two-lane (fast / reasoning) setup.

Background: the model-compare runs (eval/model-compare/results/, 2026-06-17)
showed the fast lane (gemma4-26b-a4b-gptq) and the reasoning lane
(qwen35-moe-reasoning-5930k) fail *different* items along a clean fault line:

  - The reasoning lane EARNS its ~38x latency on concrete trick/multi-step word
    problems (two-coin trap, hens-and-eggs rate, overtime pay) — gemma4 slips,
    qwen35 catches them.
  - The reasoning lane LOSES on counting/enumeration ("how many 7s from 1-100":
    it tried to enumerate, blew the token budget), unit conversion (misread
    "seconds" as "minutes"), and *abstract symbolic syllogisms* (it over-thought
    two clean syllogisms into the wrong answer) — gemma4 answers all of these
    instantly and correctly.

So the right policy is "route by task, don't pick a winner" (see
results/FINDINGS-hard-2026-06-17.md). This module encodes that policy as a
zero-dependency, content-only classifier: given a prompt, decide which lane.

It is deliberately cheap (regex, no model call) so it adds ~no latency. The
rules below are derived from the observed failure modes; see
route_analysis.py for an offline measurement of how well they recover the
oracle on the 2026-06-17 item set (and the in-sample caveat).
"""
from __future__ import annotations

import re
import sys
from dataclasses import dataclass

FAST = "fast"
REASONING = "reasoning"


@dataclass
class Decision:
    lane: str  # FAST or REASONING
    rule: str  # which rule fired (for debuggability)
    reason: str  # human-readable justification


# Counting / enumeration / unit-conversion cues. The reasoning lane over-enumerates
# or misreads units on these; the fast lane is strictly better. -> FAST
_COUNTING = re.compile(
    r"how many (times|integers?|digits?|numbers?|multiples?|divisors?)"
    r"|\bdigit\b.*\bappear"
    r"|how many (seconds?|minutes?|hours?|days?|weeks?|cups?|grams?|"
    r"kilograms?|milliliters?|millilitres?|liters?|litres?|ounces?|pounds?|"
    r"meters?|metres?|feet|inches?|millimeters?|centimeters?)"
    r"|\bdivisible by\b",
    re.I,
)

# Abstract symbolic syllogism: "all X are Y ... can we conclude / are all ...
# definitely". The reasoning lane over-thinks these and slips; the fast lane
# nails them. -> FAST. Guarded so a *concrete* money/quantity word problem that
# happens to contain "all ... are" is not misclassified as abstract.
_SYLLOGISM = re.compile(
    r"can we conclude|are all .+\b(definitely|necessarily)\b|\ball [a-z]+ are [a-z]+",
    re.I,
)
_CONCRETE = re.compile(
    r"\$|\bcents?\b|\bdollars?\b|\bmiles?\b|\bhours?\b|\bapples?\b|\beggs?\b|"
    r"\bcoins?\b|\bnickel\b|\bmarbles?\b|\bdice\b|\d+\s*%",
    re.I,
)

# Concrete multi-step / trap word-problem scenario cues. Combined with a digit
# and a question, this is where the reasoning lane earns its latency. -> REASONING
_SCENARIO = re.compile(
    r"\bcosts?\b|\btravels?\b|\bearns?\b|\bpays?\b|\bpay\b|per hour|\bper \w+\b"
    r"|\blays?\b|\bpaint|\bshare|\btotal\b|\bage\b|\bolder\b|\byears?\b|\brate\b"
    r"|\bmarbles?\b|\bdice\b|probability|\bhens?\b|\beggs?\b|\bcoins?\b|\bnickel\b"
    r"|\bdiscount|\bbill\b|\btip\b|\bgallons?\b",
    re.I,
)
_DIGIT = re.compile(r"\d")


def classify(prompt: str) -> Decision:
    """Return the lane this prompt should be routed to, with the rule that fired."""
    p = prompt or ""

    # 1. Counting / enumeration / unit conversion -> fast lane wins.
    if _COUNTING.search(p):
        return Decision(
            FAST,
            "counting/units",
            "counting/enumeration/unit conversion — reasoning lane "
            "over-enumerates or misreads units here",
        )

    # 2. Abstract symbolic syllogism -> fast lane wins (reasoning over-thinks).
    if _SYLLOGISM.search(p) and not _CONCRETE.search(p):
        return Decision(
            FAST,
            "abstract-syllogism",
            "abstract symbolic syllogism — reasoning lane over-thinks "
            "these into the wrong answer",
        )

    # 3. Concrete multi-step / trap word problem -> reasoning lane earns its keep.
    if _DIGIT.search(p) and _SCENARIO.search(p) and "?" in p:
        return Decision(
            REASONING,
            "word-problem",
            "concrete multi-step / trap word problem — a first-glance "
            "answer is often wrong, so the reasoning lane pays off",
        )

    # 4. Default: direct recall, simple arithmetic, code, sequences, short Q&A.
    return Decision(
        FAST, "default", "direct/short — the fast lane is strictly better here"
    )


# Sanity checks: representative prompts whose correct lane we know from the
# 2026-06-17 runs (the lane that actually answered them correctly). These pin the
# router's intended behavior; route_analysis.py does the full measurement.
_SELFTEST = [
    # reasoning lane is the one that gets these right (fast slipped)
    (
        "A man has two coins that total 30 cents. One of them is not a nickel. "
        "What is the OTHER coin?",
        REASONING,
    ),
    (
        "If 4 hens lay 4 eggs in 4 days, how many eggs do 8 hens lay in 8 days?",
        REASONING,
    ),
    (
        "A worker earns $15/hour for 40 hours and 1.5x for overtime. Total pay for "
        "a 46-hour week?",
        REASONING,
    ),
    # fast lane is the one that gets these right (reasoning slipped)
    ("How many times does the digit 7 appear from 1 to 100 inclusive?", FAST),
    ("How many seconds are there in 2.5 hours?", FAST),
    (
        "All Gloops are Frinks, and some Frinks are Bloops. Can we conclude that "
        "some Gloops are definitely Bloops? Answer yes or no.",
        FAST,
    ),
    (
        "If all bloops are razzies and all razzies are lazzies, are all bloops "
        "definitely lazzies? Answer yes or no.",
        FAST,
    ),
    # both correct, but fast is faster -> keep on fast
    ("What is the capital of Japan?", FAST),
    ("What is 17 multiplied by 23?", FAST),
    ("If 3x + 7 = 2x + 18, what is x?", FAST),
]


def _selftest() -> int:
    bad = 0
    for prompt, want in _SELFTEST:
        got = classify(prompt)
        ok = got.lane == want
        bad += not ok
        print(
            f"  [{'ok' if ok else 'XX'}] want={want:9s} got={got.lane:9s} "
            f"({got.rule}) :: {prompt[:64]}"
        )
    print(
        f"\n{'PASS' if not bad else 'FAIL'}: {len(_SELFTEST) - bad}/{len(_SELFTEST)} expected routings"
    )
    return 1 if bad else 0


def _route_dataset(path: str) -> None:
    import json

    with open(path) as f:
        data = json.load(f)
    n_reason = 0
    for it in data["items"]:
        d = classify(it["prompt"])
        n_reason += d.lane == REASONING
        print(f"  {it['id']:10s} -> {d.lane:9s} [{d.rule}]")
    n = len(data["items"])
    print(
        f"\n{n_reason}/{n} -> reasoning lane ({100*n_reason/n:.0f}%), "
        f"{n - n_reason}/{n} -> fast lane"
    )


def main(argv: list[str]) -> int:
    if not argv or argv[0] in ("-h", "--help"):
        print('usage: router.py --selftest | --dataset FILE.json | "<prompt>"')
        return 0
    if argv[0] == "--selftest":
        return _selftest()
    if argv[0] == "--dataset":
        _route_dataset(argv[1])
        return 0
    d = classify(" ".join(argv))
    print(f"{d.lane}\t[{d.rule}] {d.reason}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
