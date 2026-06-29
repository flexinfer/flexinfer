#!/usr/bin/env python3
"""Retrieval-quality gate kernel (F3 plan Slice 5 — the repurposed F1).

Turns the F3 kill-test's per-question results into a reusable **promotion gate**:
an aggregate quality score, an absolute-threshold PASS/FAIL verdict, and a flat
score row for storage/emission. The kill-test's own verdict is
*retrieval-vs-naive* (a comparison); a gate needs an *absolute* bar so an index /
chunker / answer-model change is caught when quality drops, not just when it loses
to stuffing.

Pure: no I/O and no environment reads at import, so it is unit-tested directly
(mirrors ``test_readpath.py``) and imported by ``f3eval.py`` to emit the gate row
from the rows it already builds — single source for the gate logic, zero new I/O.

Row shape (exactly what ``f3eval.py`` writes per question)::

    {"retrieval": {"kw": bool, "judge": "CORRECT|PARTIAL|INCORRECT"},
     "ev_retrieved": bool}

The gate is **two-dimensional**, mirroring the Slice-3 finding that retrieval
*recall* and answer *synthesis* are distinct failure modes:

  * ``ev_ratio``    — fraction of questions whose evidence file was retrieved into
                      the top-K context (recall: did we even fetch the right file);
  * ``judge_ratio`` — fraction judged CORRECT (with partial credit) by the
                      independent judge (synthesis: did the model assemble it).

A regression in either axis fails the gate, so "we still find the file but stopped
answering" and "we stopped finding the file" are both caught.
"""
from __future__ import annotations

# Provisional defaults, set conservatively from the Slice-3 hard-set bake-off
# (ev_retrieved ~15-16/18 ≈ 0.83, judge ~8-9/18 ≈ 0.47). Callers override via env;
# the runner treats a FAIL as advisory unless RQ_FAIL_ON_GATE is set.
DEFAULT_MIN_JUDGE_RATIO = 0.6
DEFAULT_MIN_EV_RATIO = 0.8
DEFAULT_PARTIAL_WEIGHT = 0.5

_VALID_JUDGE = ("CORRECT", "PARTIAL", "INCORRECT")


def _judge_of(row):
    """Normalise a row's retrieval judge verdict to one of _VALID_JUDGE.

    Anything unexpected (errors, missing key, judge-call failure marker) counts as
    INCORRECT — a gate must never give credit for an answer it could not grade.
    """
    j = str(((row or {}).get("retrieval") or {}).get("judge", "")).upper()
    return j if j in _VALID_JUDGE else "INCORRECT"


def _kw_of(row):
    return bool(((row or {}).get("retrieval") or {}).get("kw", False))


def _ev_of(row):
    return bool((row or {}).get("ev_retrieved", False))


def aggregate(rows, partial_weight=DEFAULT_PARTIAL_WEIGHT):
    """Aggregate f3eval-shaped retrieval rows into a quality score dict.

    ``partial_weight`` is the credit a PARTIAL judge verdict contributes to
    ``judge_score`` (0..1). Ratios are over ``n`` and are 0.0 when ``n == 0``.
    """
    rows = list(rows or [])
    n = len(rows)
    judge_correct = sum(1 for r in rows if _judge_of(r) == "CORRECT")
    judge_partial = sum(1 for r in rows if _judge_of(r) == "PARTIAL")
    kw_correct = sum(1 for r in rows if _kw_of(r))
    ev_retrieved = sum(1 for r in rows if _ev_of(r))
    judge_score = judge_correct + partial_weight * judge_partial

    def ratio(x):
        return (x / n) if n else 0.0

    return {
        "n": n,
        "judge_correct": judge_correct,
        "judge_partial": judge_partial,
        "kw_correct": kw_correct,
        "ev_retrieved": ev_retrieved,
        "judge_score": judge_score,
        "judge_ratio": ratio(judge_score),
        "ev_ratio": ratio(ev_retrieved),
        "kw_ratio": ratio(kw_correct),
    }


def gate(
    score,
    min_judge_ratio=DEFAULT_MIN_JUDGE_RATIO,
    min_ev_ratio=DEFAULT_MIN_EV_RATIO,
):
    """Apply the two-dimension threshold gate to an ``aggregate`` score.

    Returns ``{"verdict": "PASS"|"FAIL", "reasons": [...], "thresholds": {...}}``.
    ``n == 0`` is always a FAIL (an empty run is not a vacuous pass).
    """
    reasons = []
    n = score.get("n", 0)
    if n <= 0:
        reasons.append("no questions graded")
    else:
        if score["judge_ratio"] < min_judge_ratio:
            reasons.append(
                f"judge_ratio {score['judge_ratio']:.3f} < {min_judge_ratio:.3f}"
            )
        if score["ev_ratio"] < min_ev_ratio:
            reasons.append(f"ev_ratio {score['ev_ratio']:.3f} < {min_ev_ratio:.3f}")
    return {
        "verdict": "PASS" if not reasons else "FAIL",
        "reasons": reasons,
        "thresholds": {
            "min_judge_ratio": min_judge_ratio,
            "min_ev_ratio": min_ev_ratio,
        },
    }


def result_row(model, collection, score, gate_result, extra=None):
    """Flat, JSON-serialisable score row — the gauntlet's retrieval-quality output,
    the sibling of the throughput row the bench binary stores per model.
    """
    row = {
        "kind": "retrieval_quality",
        "model": model,
        "collection": collection,
        "n": score["n"],
        "judge_correct": score["judge_correct"],
        "judge_partial": score["judge_partial"],
        "kw_correct": score["kw_correct"],
        "ev_retrieved": score["ev_retrieved"],
        "judge_ratio": round(score["judge_ratio"], 3),
        "ev_ratio": round(score["ev_ratio"], 3),
        "kw_ratio": round(score["kw_ratio"], 3),
        "verdict": gate_result["verdict"],
        "reasons": gate_result["reasons"],
    }
    if extra:
        row.update(extra)
    return row


def evaluate(rows, model, collection, **kw):
    """Convenience one-shot: aggregate → gate → result_row.

    Keyword args: ``partial_weight``, ``min_judge_ratio``, ``min_ev_ratio``,
    ``extra``. Returned by ``f3eval.py`` as the ``RQ_RESULT_JSON`` payload.
    """
    score = aggregate(
        rows, partial_weight=kw.get("partial_weight", DEFAULT_PARTIAL_WEIGHT)
    )
    g = gate(
        score,
        min_judge_ratio=kw.get("min_judge_ratio", DEFAULT_MIN_JUDGE_RATIO),
        min_ev_ratio=kw.get("min_ev_ratio", DEFAULT_MIN_EV_RATIO),
    )
    return result_row(model, collection, score, g, extra=kw.get("extra"))


def _self_check():
    """Offline wiring gate: exercise aggregate→gate→result_row on a synthetic
    fixture and assert the invariants. No network. Mirrors the --self-check pattern
    used by cmd/agent-loop and the reembed task.
    """
    rows = [
        {"retrieval": {"kw": True, "judge": "CORRECT"}, "ev_retrieved": True},
        {"retrieval": {"kw": False, "judge": "PARTIAL"}, "ev_retrieved": True},
        {"retrieval": {"kw": False, "judge": "INCORRECT"}, "ev_retrieved": True},
        {"retrieval": {"kw": False, "judge": "wat"}, "ev_retrieved": False},
    ]
    checks = []

    score = aggregate(rows)
    checks.append(("n", score["n"] == 4))
    checks.append(("judge_correct", score["judge_correct"] == 1))
    checks.append(("judge_partial", score["judge_partial"] == 1))
    checks.append(("ev_retrieved", score["ev_retrieved"] == 3))
    # judge_score = 1 + 0.5*1 = 1.5 ; /4 = 0.375
    checks.append(("judge_ratio", abs(score["judge_ratio"] - 0.375) < 1e-9))
    checks.append(("ev_ratio", abs(score["ev_ratio"] - 0.75) < 1e-9))

    # Below both thresholds -> FAIL on both axes.
    g = gate(score)
    checks.append(("fail_verdict", g["verdict"] == "FAIL"))
    checks.append(("two_reasons", len(g["reasons"]) == 2))

    # Strong rows -> PASS.
    good = [{"retrieval": {"kw": True, "judge": "CORRECT"}, "ev_retrieved": True}] * 5
    gg = gate(aggregate(good))
    checks.append(("pass_verdict", gg["verdict"] == "PASS"))

    # Empty -> FAIL, never a vacuous pass.
    ge = gate(aggregate([]))
    checks.append(("empty_fail", ge["verdict"] == "FAIL"))

    # result_row is flat + serialisable.
    import json as _json

    row = result_row("m", "c", score, g)
    checks.append(("row_kind", row["kind"] == "retrieval_quality"))
    checks.append(("row_serialisable", isinstance(_json.dumps(row), str)))

    ok = all(passed for _, passed in checks)
    for name, passed in checks:
        print(f"  [{'PASS' if passed else 'FAIL'}] {name}")
    print(
        f"rqgate self-check: {'PASS' if ok else 'FAIL'} ({sum(p for _, p in checks)}/{len(checks)})"
    )
    return ok


if __name__ == "__main__":
    import sys

    if "--self-check" in sys.argv:
        sys.exit(0 if _self_check() else 1)
    print(
        "usage: rqgate.py --self-check  (library module; import for aggregate/gate/result_row)"
    )
    sys.exit(2)
