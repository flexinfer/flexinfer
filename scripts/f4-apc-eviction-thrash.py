#!/usr/bin/env python3
"""f4-apc-eviction-thrash.py — F4-skeptic-cache-eviction-thrash kill-test.

Lives downstream of the F4-prefix-cache-flip canary landed in MR !519. Spec
in `.loom/ralph-f4-prefix-cache-flip-canary-2026-05-26.md`; brainstorm root
in `.loom/brainstorm-f4-long-context-agent-2026-05-25.md` (sections
`F4-skeptic-cache-eviction-thrash` and `F4-prefix-cache-flip`).

The riskiest assumption the canary is meant to falsify is whether vLLM's
native automatic prefix caching (APC) at 32k FP8 KV + GPTQ on gfx1100 with
`gpuMemoryUtilization: "0.94"` retains at least two distinct ~30k-token
prefixes simultaneously. If only one fits, every user-switch is a cold
miss and the F4 multi-user product framings ("instant follow-up",
"the model remembers me", "shared prefix multi-tenant") all silently
break.

The test alternates two distinct ~30k-token system prompts A and B with a
short user suffix per turn, pattern ABABAB × N (default 5). It captures
per-turn TTFT and the runtime's reported `usage.prompt_tokens`, then
scrapes `vllm:prefix_cache_hit_rate` from the pod's /metrics endpoint
before and after to derive an aggregate hit rate over the run.

Pass criterion (default):
    Aggregate hit rate >= 0.50 AFTER the third alternation. Cache holds
    both prefixes; user-switch is mostly free.

Fail criterion:
    Aggregate hit rate < 0.20 across the whole run. Eviction thrashes;
    APC at 32k FP8 KV + 0.94 utilization holds <2 distinct prefixes.
    F4 prefix-cache product framings are structurally bounded.

Ambiguous middle (0.20 <= rate < 0.50):
    Document and re-run with longer prompts or lower utilization. Treat
    as conditional rather than promote.

Usage:
    # Pre-conditions (see plan doc operator runbook):
    #   1. canary Model is Ready
    #   2. proxy port-forward in another shell:
    #        kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 18080:80
    #   3. (optional) canary metrics port-forward for /metrics scrape:
    #        kubectl -n flexinfer-system port-forward \\
    #            svc/gemma4-26b-a4b-gptq-apc-canary 18000:8000

    python3 scripts/f4-apc-eviction-thrash.py \\
        --endpoint http://localhost:18080 \\
        --model gemma4-26b-a4b-gptq-apc-canary \\
        --metrics http://localhost:18000/metrics \\
        --report .loom/local/validation/f4-apc/$(date -u +%F)-eviction-thrash/report.json

Exit codes:
    0  hit_rate >= 0.50 after third alternation (pass)
    1  hit_rate <  0.20 across run (fail)
    2  ambiguous (0.20-0.50) — recorded as conditional
    3  any infrastructure failure (timeouts, parse errors, no response)
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import random
import re
import statistics
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

DEFAULT_ENDPOINT = "http://localhost:18080"
DEFAULT_METRICS = "http://localhost:18000/metrics"
DEFAULT_MODEL = "gemma4-26b-a4b-gptq-apc-canary"
DEFAULT_ROUNDS = 5  # ABABAB × 5 = 10 turns
DEFAULT_PROMPT_TOKENS = 30000
DEFAULT_MAX_TOKENS = 256
DEFAULT_TIMEOUT_S = 600

PASS_HIT_RATE = 0.50
FAIL_HIT_RATE = 0.20

# Two seed words used to build the two distinct ~30k-token prefixes. They are
# topic-disjoint so vLLM cannot deduplicate across them. The body is filler
# repeated to reach the target token count; the suffix is what the model
# actually has to answer, so each turn ends in a distinguishable instruction.
SEED_A_INTRO = (
    "You are a senior Kubernetes operator reviewing a long incident "
    "postmortem about a Longhorn volume crash. Below is the full "
    "incident timeline. Read it carefully. "
)
SEED_A_BODY = (
    "At 03:14:00Z the volume entered Faulted state because a replica on "
    "node cblevins-radeonvii failed health-check after the SCSI target "
    "lost its TCP keepalive. The reconciler retried the replica three "
    "times with exponential backoff, each retry adding ~12 seconds to "
    "the outage window before the longhorn-manager finally marked the "
    "instance Detached and triggered the auto-rebuild path. "
)

SEED_B_INTRO = (
    "You are a literary editor preparing a review of an 1860s travel "
    "memoir. Below is the manuscript in its entirety. Read it carefully. "
)
SEED_B_BODY = (
    "We disembarked at Marseille on the morning of the seventeenth, "
    "and after a brief delay with the customs officials, made our way "
    "by carriage to the inn at which we had reserved rooms by post the "
    "previous month. The proprietor met us at the door, expressed his "
    "gratitude for our patronage, and showed us up a narrow staircase "
    "to a parlor overlooking a small interior courtyard. "
)


def build_prefix(intro: str, body: str, target_chars: int) -> str:
    """Build a ~30k-token prefix. Tokenization varies by model — we target a
    character count that produces roughly the requested token count on the
    Gemma SentencePiece tokenizer (~4 chars/token average for English).
    The exact token count is reported back by the runtime in usage.prompt_tokens;
    we record both target and actual."""
    out = [intro]
    cur = len(intro)
    while cur < target_chars:
        out.append(body)
        cur += len(body)
    return "".join(out)


def now_ms() -> int:
    return int(time.time() * 1000)


def post_chat(
    endpoint: str,
    model: str,
    system_prompt: str,
    user_suffix: str,
    max_tokens: int,
    timeout_s: int,
) -> dict:
    """Send one chat completion, return (turn_record_dict)."""
    body = {
        "model": model,
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": user_suffix},
        ],
        "max_tokens": max_tokens,
        "temperature": 0,
        "stream": False,
    }
    raw = json.dumps(body).encode()
    req = urllib.request.Request(
        f"{endpoint.rstrip('/')}/v1/chat/completions",
        data=raw,
        method="POST",
        headers={"Content-Type": "application/json"},
    )
    t0 = now_ms()
    try:
        with urllib.request.urlopen(req, timeout=timeout_s) as resp:
            duration_ms = now_ms() - t0
            payload = json.loads(resp.read().decode())
    except urllib.error.HTTPError as e:
        return {
            "ok": False,
            "duration_ms": now_ms() - t0,
            "error": f"http_{e.code}",
            "detail": e.read().decode(errors="replace")[:500],
        }
    except (urllib.error.URLError, TimeoutError, OSError) as e:
        return {
            "ok": False,
            "duration_ms": now_ms() - t0,
            "error": "transport",
            "detail": str(e)[:500],
        }
    usage = payload.get("usage", {}) or {}
    choices = payload.get("choices") or [{}]
    finish = choices[0].get("finish_reason", "")
    content = choices[0].get("message", {}).get("content", "") or ""
    return {
        "ok": True,
        "duration_ms": duration_ms,
        "prompt_tokens": usage.get("prompt_tokens", 0),
        "completion_tokens": usage.get("completion_tokens", 0),
        "total_tokens": usage.get("total_tokens", 0),
        "finish_reason": finish,
        "content_head": content[:120],
    }


_PREFIX_HIT_RE = re.compile(
    r"^vllm:prefix_cache_(hits|queries)_total(?:\{[^}]*\})?\s+([0-9.eE+-]+)\s*$",
    re.MULTILINE,
)


def scrape_metrics(metrics_url: str | None, timeout_s: int = 10) -> dict:
    """Best-effort scrape of vLLM's prefix-cache hit/query counters. The two
    counters give an aggregate hit rate without needing the histogram. If
    /metrics is unreachable, return zeros and let the caller decide."""
    if not metrics_url:
        return {"hits": 0.0, "queries": 0.0, "ok": False, "skipped": "no metrics url"}
    try:
        with urllib.request.urlopen(metrics_url, timeout=timeout_s) as resp:
            text = resp.read().decode()
    except (urllib.error.URLError, TimeoutError, OSError) as e:
        return {"hits": 0.0, "queries": 0.0, "ok": False, "skipped": str(e)[:200]}
    hits = 0.0
    queries = 0.0
    for m in _PREFIX_HIT_RE.finditer(text):
        kind, val = m.group(1), float(m.group(2))
        if kind == "hits":
            hits += val
        else:
            queries += val
    return {"hits": hits, "queries": queries, "ok": True}


def hit_rate(snap_before: dict, snap_after: dict) -> float | None:
    if not snap_before.get("ok") or not snap_after.get("ok"):
        return None
    d_hits = snap_after["hits"] - snap_before["hits"]
    d_queries = snap_after["queries"] - snap_before["queries"]
    if d_queries <= 0:
        return None
    return d_hits / d_queries


def verdict(rate: float | None) -> tuple[str, str, int]:
    if rate is None:
        return ("unknown", "no /metrics delta available — re-run with --metrics", 3)
    if rate >= PASS_HIT_RATE:
        return ("pass", f"aggregate hit_rate={rate:.3f} >= {PASS_HIT_RATE} gate", 0)
    if rate < FAIL_HIT_RATE:
        return (
            "fail",
            f"aggregate hit_rate={rate:.3f} < {FAIL_HIT_RATE} gate — eviction thrash",
            1,
        )
    return (
        "conditional",
        f"aggregate hit_rate={rate:.3f} in [{FAIL_HIT_RATE}, {PASS_HIT_RATE}) — re-run with adjusted utilization or longer prompts",
        2,
    )


def main(argv: list[str]) -> int:
    p = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    p.add_argument(
        "--endpoint",
        default=DEFAULT_ENDPOINT,
        help=f"proxy chat endpoint base URL (default: {DEFAULT_ENDPOINT})",
    )
    p.add_argument(
        "--model",
        default=DEFAULT_MODEL,
        help=f"served model name (default: {DEFAULT_MODEL})",
    )
    p.add_argument(
        "--metrics",
        default=DEFAULT_METRICS,
        help=f"canary pod /metrics URL (default: {DEFAULT_METRICS}); "
        "set to '' to skip and record turn-level data only",
    )
    p.add_argument(
        "--rounds",
        type=int,
        default=DEFAULT_ROUNDS,
        help="number of AB alternation rounds (default: 5 = 10 turns)",
    )
    p.add_argument(
        "--prompt-tokens",
        type=int,
        default=DEFAULT_PROMPT_TOKENS,
        help="target token count per prefix (default: 30000)",
    )
    p.add_argument(
        "--max-tokens",
        type=int,
        default=DEFAULT_MAX_TOKENS,
        help="max_tokens per turn (default: 256)",
    )
    p.add_argument(
        "--timeout",
        type=int,
        default=DEFAULT_TIMEOUT_S,
        help="per-turn HTTP timeout in seconds (default: 600)",
    )
    p.add_argument("--report", required=True, help="output JSON report path")
    p.add_argument(
        "--seed", type=int, default=20260526, help="random seed for user-suffix wording"
    )
    args = p.parse_args(argv)

    rng = random.Random(args.seed)
    target_chars = args.prompt_tokens * 4  # ~4 chars/token for Gemma SP English
    prefix_a = build_prefix(SEED_A_INTRO, SEED_A_BODY, target_chars)
    prefix_b = build_prefix(SEED_B_INTRO, SEED_B_BODY, target_chars)

    # User suffixes differ per turn so the runtime never returns a degenerate
    # cached completion. They are intentionally tiny so the prompt is
    # dominated by the prefix (the thing APC is meant to cache).
    suffixes_a = [
        "In one sentence, what was the root cause?",
        "Name the node that hosted the failed replica.",
        "List the three retry counts mentioned.",
        "What service marked the instance Detached?",
        "Quote the timestamp of the initial fault.",
    ]
    suffixes_b = [
        "Name the city of disembarkation.",
        "What day of the month did they arrive?",
        "Describe the parlor in one sentence.",
        "Where did the carriage take them?",
        "Quote one sentence about the courtyard.",
    ]

    report = {
        "schema_version": "flexinfer.f4_apc_eviction_thrash.v1",
        "created_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "model": args.model,
        "endpoint": args.endpoint,
        "rounds": args.rounds,
        "prompt_tokens_target": args.prompt_tokens,
        "max_tokens": args.max_tokens,
        "turns": [],
        "metrics": {},
        "summary": {},
    }

    print(
        f"[f4-apc] model={args.model} rounds={args.rounds} "
        f"target_prompt_tokens={args.prompt_tokens} max_tokens={args.max_tokens}",
        file=sys.stderr,
    )

    snap_before = scrape_metrics(args.metrics)
    report["metrics"]["before"] = snap_before
    print(
        f"[f4-apc] metrics before: hits={snap_before['hits']:.0f} "
        f"queries={snap_before['queries']:.0f}",
        file=sys.stderr,
    )

    snap_after_third = None
    for r in range(args.rounds):
        for label, prefix, suffix_pool in (
            ("A", prefix_a, suffixes_a),
            ("B", prefix_b, suffixes_b),
        ):
            suffix = suffix_pool[r % len(suffix_pool)]
            t = post_chat(
                endpoint=args.endpoint,
                model=args.model,
                system_prompt=prefix,
                user_suffix=suffix,
                max_tokens=args.max_tokens,
                timeout_s=args.timeout,
            )
            t.update({"round": r, "label": label, "suffix": suffix})
            report["turns"].append(t)
            if t["ok"]:
                print(
                    f"[f4-apc] r{r}-{label}: prompt={t['prompt_tokens']} "
                    f"completion={t['completion_tokens']} duration={t['duration_ms']}ms "
                    f"head={t['content_head']!r}",
                    file=sys.stderr,
                )
            else:
                print(
                    f"[f4-apc] r{r}-{label}: FAILED error={t['error']} "
                    f"duration={t['duration_ms']}ms detail={t.get('detail', '')!r}",
                    file=sys.stderr,
                )
        # Capture metrics after each round; the post-third-alternation
        # snapshot anchors the pass-criterion ("≥ 50% after the third
        # alternation"). With AB per round, the "third alternation" is the
        # end of round index 2 (zero-indexed).
        if r == 2:
            snap_after_third = scrape_metrics(args.metrics)
            report["metrics"]["after_third_alternation"] = snap_after_third
            print(
                f"[f4-apc] metrics after-3rd-alt: hits={snap_after_third['hits']:.0f} "
                f"queries={snap_after_third['queries']:.0f}",
                file=sys.stderr,
            )

    snap_after = scrape_metrics(args.metrics)
    report["metrics"]["after"] = snap_after
    print(
        f"[f4-apc] metrics after: hits={snap_after['hits']:.0f} "
        f"queries={snap_after['queries']:.0f}",
        file=sys.stderr,
    )

    aggregate_rate = hit_rate(snap_before, snap_after)
    post_third_rate = (
        hit_rate(snap_before, snap_after_third) if snap_after_third else None
    )

    # The spec's pass criterion is "≥ 50% after the third alternation",
    # so the verdict is driven by post_third_rate when available;
    # aggregate_rate is reported for context.
    primary_rate = post_third_rate if post_third_rate is not None else aggregate_rate
    v_label, v_reason, exit_code = verdict(primary_rate)

    durations = [t["duration_ms"] for t in report["turns"] if t["ok"]]
    completions = [t["completion_tokens"] for t in report["turns"] if t["ok"]]
    report["summary"] = {
        "verdict": v_label,
        "reason": v_reason,
        "primary_hit_rate": primary_rate,
        "aggregate_hit_rate": aggregate_rate,
        "post_third_alternation_hit_rate": post_third_rate,
        "turn_count": len(report["turns"]),
        "turn_success_count": len(durations),
        "duration_ms_p50": statistics.median(durations) if durations else None,
        "duration_ms_p95": (
            statistics.quantiles(durations, n=20)[-1]
            if len(durations) >= 20
            else max(durations) if durations else None
        ),
        "mean_completion_tokens": statistics.mean(completions) if completions else None,
    }

    out_path = Path(args.report)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2) + "\n")

    summary = report["summary"]
    print(
        f"[f4-apc] verdict={summary['verdict']} "
        f"primary_hit_rate={summary['primary_hit_rate']} "
        f"aggregate_hit_rate={summary['aggregate_hit_rate']} "
        f"turns={summary['turn_success_count']}/{summary['turn_count']} "
        f"duration_p50={summary['duration_ms_p50']}ms",
        file=sys.stderr,
    )
    print(f"[f4-apc] report written to {out_path}", file=sys.stderr)
    return exit_code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
