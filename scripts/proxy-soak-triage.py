#!/usr/bin/env python3
"""Triage proxy-soak.jsonl from deploy/debug/gfx906-llamacpp-proxy-soak.yaml.

Reads the JSONL evidence file produced by the proxy-soak Job, prints a summary
of the run, and emits a verdict that maps directly to the next RALPH slice.

Failure buckets (one per ok=false record, based on the embedded diag probe):

  diag.ok=true on every failure  -> proxy alive at failure; gap is per-Model
                                    selectorless Service or backend :8000.
                                    Next slice: EndpointSlice audit, or rerun
                                    with SOAK_DIAG_ENDPOINT pointed at a
                                    sibling-model /v1/chat/completions route.

  diag.ok=false on every failure -> proxy itself unreachable. Next slice:
                                    investigate flexinfer-proxy pod health,
                                    HPA, restarts, panics.

  mixed                          -> show ratios; both branches in play.
  no diag                        -> SOAK_DIAG_ENDPOINT was unset; rerun.

Separately, ok=true records with expected_model_match=false are tallied as
"model_mismatch" — they don't fail the run with the default soft preflight,
but they tell you the upstream advertises a different model id than the
Model CR name (e.g. llama.cpp returns the GGUF basename). Use --alias on
the upstream or accept it as informational.

Usage:
  scripts/proxy-soak-triage.py [path/to/proxy-soak.jsonl]
  kubectl -n flexinfer-system logs job/gfx906-llamacpp-proxy-soak-traffic \\
    | scripts/proxy-soak-triage.py -

Reads from stdin if path is "-" or omitted and stdin is a pipe.
"""

from __future__ import annotations

import json
import sys
from collections import Counter
from pathlib import Path


def load_records(source):
    records = []
    for lineno, raw in enumerate(source, start=1):
        line = raw.strip()
        if not line:
            continue
        try:
            records.append(json.loads(line))
        except json.JSONDecodeError as exc:
            print(f"warn: line {lineno} not valid JSON: {exc}", file=sys.stderr)
    return records


def short(value, width=80):
    if value is None:
        return ""
    text = str(value).replace("\n", " ")
    return text if len(text) <= width else text[: width - 1] + "…"


def classify(records):
    start = next((r for r in records if r.get("event") == "soak_start"), None)
    summary = next((r for r in records if r.get("event") == "soak_summary"), None)

    preflight = [r for r in records if r.get("event") == "preflight_request"]
    requests = [r for r in records if r.get("event") == "request"]

    failures = []
    for r in preflight + requests:
        if r.get("ok") is False:
            failures.append(r)

    buckets = Counter()
    for f in failures:
        diag = f.get("diag")
        if diag is None:
            buckets["no_diag"] += 1
        elif diag.get("ok") is True:
            buckets["proxy_alive"] += 1
        else:
            buckets["proxy_down"] += 1

    def mismatch_count(rs):
        return sum(
            1
            for r in rs
            if r.get("ok") is True and r.get("expected_model_match") is False
        )

    mismatches = {
        "preflight": mismatch_count(preflight),
        "measured": mismatch_count([r for r in requests if not r.get("warmup")]),
        "warmup": mismatch_count([r for r in requests if r.get("warmup")]),
    }
    sample = next(
        (
            r
            for r in preflight + requests
            if r.get("ok") is True and r.get("expected_model_match") is False
        ),
        None,
    )
    mismatches["sample_returned"] = sample.get("model_returned") if sample else None
    mismatches["expected"] = (start or {}).get("expected_model") or (sample or {}).get(
        "expected_model"
    )

    return {
        "start": start,
        "summary": summary,
        "preflight": preflight,
        "requests": requests,
        "failures": failures,
        "buckets": buckets,
        "mismatches": mismatches,
    }


def print_header(start):
    print("=== soak config ===")
    if not start:
        print("  (no soak_start record found)")
        return
    keys = (
        "started_at",
        "endpoint",
        "model",
        "expected_model",
        "duration_seconds",
        "interval_seconds",
        "preflight_attempts",
        "latency_budget_ms_per_token",
    )
    for k in keys:
        if k in start:
            print(f"  {k}: {start[k]}")


def print_counts(c):
    print("\n=== counts ===")
    preflight = c["preflight"]
    requests = c["requests"]
    warmup = [r for r in requests if r.get("warmup")]
    measured = [r for r in requests if not r.get("warmup")]

    def split(rs):
        ok = sum(1 for r in rs if r.get("ok") is True)
        fail = sum(1 for r in rs if r.get("ok") is False)
        return ok, fail

    for label, rs in (
        ("preflight", preflight),
        ("warmup", warmup),
        ("measured", measured),
    ):
        ok, fail = split(rs)
        print(f"  {label:<10} ok={ok:<5} fail={fail}")
    m = c["mismatches"]
    total_mismatch = m["preflight"] + m["warmup"] + m["measured"]
    if total_mismatch:
        print(
            f"  model_mismatch (ok=true, expected_model_match=false): "
            f"preflight={m['preflight']} warmup={m['warmup']} measured={m['measured']}"
        )
        if m["sample_returned"]:
            print(
                f"    sample: model_returned={m['sample_returned']!r} "
                f"expected={m['expected']!r}"
            )
    summary = c["summary"]
    if summary:
        print("\n=== soak_summary ===")
        for k in (
            "status",
            "attempts",
            "measured_requests",
            "measured_failures",
            "warmup_failures",
            "preflight_model_mismatches",
            "measured_model_mismatches",
            "preflight_require_model_match",
            "p95_ms_per_token",
            "latency_budget_ms_per_token",
            "completed_at",
        ):
            if k in summary:
                print(f"  {k}: {summary[k]}")


def print_failures(failures):
    if not failures:
        return
    print(f"\n=== failures ({len(failures)}) ===")
    header = f"{'time':<20} {'phase':<10} {'#':<4} {'diag.ok':<7} {'error':<40} {'diag.error'}"
    print(header)
    print("-" * len(header))
    for f in failures:
        phase = f.get("event", "?")
        if phase == "request":
            phase = "warmup" if f.get("warmup") else "measured"
        elif phase == "preflight_request":
            phase = "preflight"
        attempt = f.get("attempt", "?")
        diag = f.get("diag") or {}
        diag_ok = diag.get("ok")
        diag_ok_str = "—" if diag_ok is None else ("yes" if diag_ok else "no")
        print(
            f"{short(f.get('started_at'), 20):<20} "
            f"{phase:<10} "
            f"{str(attempt):<4} "
            f"{diag_ok_str:<7} "
            f"{short(f.get('error'), 40):<40} "
            f"{short(diag.get('error'), 60)}"
        )


def _print_mismatch_followup(c):
    m = c["mismatches"]
    total_mismatch = m["preflight"] + m["warmup"] + m["measured"]
    if not total_mismatch:
        return
    print(
        f"\n  also: model_returned mismatched expected on {total_mismatch} "
        f"ok response(s)."
    )
    if m["sample_returned"] and m["expected"]:
        print(f"    expected={m['expected']!r} returned={m['sample_returned']!r}")
    print("    if upstream is llama.cpp this is expected (advertises GGUF basename);")
    print("    set --alias on llama-server (or --served-model-name on vLLM) to fix,")
    print("    or set SOAK_PREFLIGHT_REQUIRE_MODEL_MATCH=false to accept (default).")


def print_verdict(c):
    print("\n=== verdict ===")
    failures = c["failures"]
    buckets = c["buckets"]
    m = c["mismatches"]
    total_mismatch = m["preflight"] + m["warmup"] + m["measured"]

    if not failures:
        if total_mismatch:
            print(
                "  transport clean (no ok=false records), but model_returned "
                "mismatched expected on every successful response."
            )
            print(
                "  this is normal for llama.cpp upstreams (GGUF basename in `model`)."
            )
            print(f"    expected={m['expected']!r} returned={m['sample_returned']!r}")
            print("  next options:")
            print(
                "   a) accept as informational; soft preflight already lets the gate pass"
            )
            print("   b) tighten the contract by aliasing the upstream model id")
            print(
                "      (llama.cpp --alias <model-cr-name>, vLLM --served-model-name <model-cr-name>)"
            )
            print(
                "   c) proceed to 24h gate (set SOAK_DURATION_SECONDS=86400, re-apply)"
            )
        else:
            print("  soak clean. no measured failures, no model mismatches.")
            print(
                "  next: proceed to 24h gate (set SOAK_DURATION_SECONDS=86400, re-apply)."
            )
        return

    total = sum(buckets.values())
    print(f"  failures by diag bucket (n={total}):")
    for k in ("proxy_alive", "proxy_down", "no_diag"):
        if buckets[k]:
            print(f"    {k:<12} {buckets[k]}")

    if buckets["no_diag"] and not (buckets["proxy_alive"] or buckets["proxy_down"]):
        print("\n  diag was not recorded on any failure.")
        print(
            "  next: rerun with SOAK_DIAG_ENDPOINT set (defaults to flexinfer-proxy /healthz)."
        )
        _print_mismatch_followup(c)
        return

    dominant = max(buckets, key=buckets.get)
    only_one = sum(1 for v in buckets.values() if v > 0) == 1

    if dominant == "proxy_alive" and only_one:
        print("\n  proxy was alive on every failure -> per-Model layer is the gap.")
        print("  next RALPH slice options:")
        print("   a) audit EndpointSlices for the per-Model selectorless Service")
        print(
            "      kubectl -n flexinfer-system get endpointslices -l kubernetes.io/service-name=<model-svc>"
        )
        print("   b) sibling-model A/B: rerun with")
        print(
            "      SOAK_DIAG_ENDPOINT=http://flexinfer-proxy.flexinfer-system.svc.cluster.local"
            "/model/qwen3-1p7b-tools-radeonvii/v1/chat/completions"
        )
        _print_mismatch_followup(c)
        return

    if dominant == "proxy_down" and only_one:
        print(
            "\n  proxy was unreachable on every failure -> investigate flexinfer-proxy itself."
        )
        print("  next RALPH slice:")
        print(
            "   kubectl -n flexinfer-system get pods -l app.kubernetes.io/name=flexinfer-proxy -o wide"
        )
        print(
            "   kubectl -n flexinfer-system describe pod -l app.kubernetes.io/name=flexinfer-proxy"
        )
        print(
            "   kubectl -n flexinfer-system logs -l app.kubernetes.io/name=flexinfer-proxy --tail=200 --prev"
        )
        _print_mismatch_followup(c)
        return

    print("\n  mixed signal: failures span buckets. both branches in play.")
    print(
        "  next: inspect the failure timeline; correlate proxy_down events with proxy pod restarts."
    )
    _print_mismatch_followup(c)


def main(argv):
    path_arg = argv[1] if len(argv) > 1 else "-"

    if path_arg == "-":
        if sys.stdin.isatty():
            print(
                "usage: proxy-soak-triage.py [path/to/proxy-soak.jsonl]",
                file=sys.stderr,
            )
            return 2
        records = load_records(sys.stdin)
        source_label = "<stdin>"
    else:
        p = Path(path_arg)
        if not p.exists():
            print(f"error: {p} not found", file=sys.stderr)
            return 2
        with p.open("r", encoding="utf-8") as fh:
            records = load_records(fh)
        source_label = str(p)

    if not records:
        print(f"error: no records loaded from {source_label}", file=sys.stderr)
        return 2

    print(f"# proxy-soak triage: {source_label} ({len(records)} records)\n")
    c = classify(records)
    print_header(c["start"])
    print_counts(c)
    print_failures(c["failures"])
    print_verdict(c)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
