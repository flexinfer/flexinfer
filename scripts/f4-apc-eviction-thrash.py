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
per-turn TTFT and the runtime's reported `usage.prompt_tokens`. Two
independent signals derive an aggregate hit rate:

1. Proxy response headers (preferred — added in MR !524):
       X-Flexinfer-Upstream-Ms     proxy-measured runtime time
       X-Flexinfer-Cached-Tokens   vLLM-reported (absent on llama.cpp)
       X-Flexinfer-Prompt-Tokens   cross-check vs usage.prompt_tokens
       X-Flexinfer-Finish-Reason   cross-check vs choices[0].finish_reason
   Sum(cached_tokens) / Sum(prompt_tokens) over header-bearing turns
   gives a header-derived aggregate that needs only the proxy
   port-forward, not a second port-forward to the canary pod's
   /metrics endpoint. Per-turn cached_ratio also exposes asymmetric
   eviction (e.g. A always hits, B always misses) that an aggregate
   counter rate hides.

2. /metrics scrape (legacy — still supported as cross-check):
       vllm:prefix_cache_hits_total / vllm:prefix_cache_queries_total
   delta before/after run, and after the third alternation, so the
   aggregate hit rate is comparable across runs.

A third complementary signal — informational only, does NOT drive the
verdict — is per-label upstream_ms decay (last-round p50 vs first-round
p50, computed separately for prefix A and prefix B). Mirrors the
brainstorm bet from `F4-instant-followup`: "10-turn turn-10-TTFT ≤
turn-2-TTFT". When APC works, the last B turn's upstream_ms should be
much smaller than the first B turn (same for A). Asymmetric decay
(A decays cleanly, B does not, or vice versa) is a fingerprint of
asymmetric eviction that the aggregate hit rate hides. Emitted under
summary.ttft_decay; assertion_passed is logged but does NOT change the
exit code (verdict cascade is unchanged from v2).

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

HEADER_UPSTREAM_MS = "X-Flexinfer-Upstream-Ms"
HEADER_CACHED_TOKENS = "X-Flexinfer-Cached-Tokens"
HEADER_PROMPT_TOKENS = "X-Flexinfer-Prompt-Tokens"
HEADER_FINISH_REASON = "X-Flexinfer-Finish-Reason"

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


def _header_int(headers, name: str) -> int | None:
    """Parse a non-negative integer response header. Returns None if absent,
    empty, or non-parseable so the caller can distinguish "engine did not
    report" from a legitimate zero."""
    if headers is None:
        return None
    raw = headers.get(name)
    if raw is None or raw == "":
        return None
    try:
        v = int(raw)
    except (TypeError, ValueError):
        return None
    if v < 0:
        return None
    return v


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
            resp_headers = resp.headers
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

    upstream_ms = _header_int(resp_headers, HEADER_UPSTREAM_MS)
    cached_tokens = _header_int(resp_headers, HEADER_CACHED_TOKENS)
    header_prompt_tokens = _header_int(resp_headers, HEADER_PROMPT_TOKENS)
    header_finish_reason = (
        resp_headers.get(HEADER_FINISH_REASON) if resp_headers else None
    )

    prompt_tokens = usage.get("prompt_tokens", 0) or 0
    cached_ratio: float | None
    if cached_tokens is not None and prompt_tokens > 0:
        cached_ratio = cached_tokens / prompt_tokens
    else:
        cached_ratio = None

    return {
        "ok": True,
        "duration_ms": duration_ms,
        "upstream_ms": upstream_ms,
        "prompt_tokens": prompt_tokens,
        "completion_tokens": usage.get("completion_tokens", 0),
        "total_tokens": usage.get("total_tokens", 0),
        "finish_reason": finish,
        "content_head": content[:120],
        # Proxy header signals (None when engine doesn't report — e.g. llama.cpp
        # omits cached_tokens, so we distinguish "absent" from "zero").
        "cached_tokens": cached_tokens,
        "cached_ratio": cached_ratio,
        "header_prompt_tokens": header_prompt_tokens,
        "header_finish_reason": header_finish_reason,
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


def header_cached_rate(
    turns: list[dict], rounds_max: int | None = None
) -> float | None:
    """Compute aggregate Sum(cached_tokens) / Sum(prompt_tokens) over successful
    turns whose proxy header reported cached_tokens. Skips turns with no header
    (e.g. llama.cpp non-vLLM backends). Returns None if no eligible turns.

    rounds_max: when set, only consider turns with round <= rounds_max. The
    spec's pass criterion anchors on "after the third alternation" (round
    index 2), so callers pass rounds_max=2 for the post-third snapshot."""
    total_prompt = 0
    total_cached = 0
    eligible = 0
    for t in turns:
        if not t.get("ok"):
            continue
        if rounds_max is not None and t.get("round", -1) > rounds_max:
            continue
        cached = t.get("cached_tokens")
        prompt = t.get("prompt_tokens") or 0
        if cached is None or prompt <= 0:
            continue
        total_prompt += prompt
        total_cached += cached
        eligible += 1
    if eligible == 0 or total_prompt <= 0:
        return None
    return total_cached / total_prompt


def upstream_ms_decay(turns: list[dict], rounds_total: int) -> dict:
    """Per-label first-round vs last-round upstream_ms decay. Informational
    signal — caller MUST NOT use the assertion to drive the verdict (the
    primary hit-rate cascade in main() owns that).

    Mirrors the `F4-instant-followup` brainstorm bet ("turn-10-TTFT ≤
    turn-2-TTFT") translated to ABABAB rounds: for each label L in (A, B),
    compare median upstream_ms of L turns in round=0 vs round=rounds_total-1.

    Asymmetric decay (one label decays cleanly, the other does not) is a
    fingerprint of asymmetric eviction that the aggregate hit rate hides.

    Returns a dict with per-label first/last p50s, decay ratios
    (last/first; lower = better), an overall `assertion_passed` (both
    labels' last p50 <= first p50, with both populated), and an
    `assertion_note` explaining edge cases. All fields may be None when
    the data is insufficient (single round, header absent, etc.)."""
    if rounds_total < 2:
        return {
            "a_first_upstream_ms_p50": None,
            "a_last_upstream_ms_p50": None,
            "a_decay_ratio": None,
            "b_first_upstream_ms_p50": None,
            "b_last_upstream_ms_p50": None,
            "b_decay_ratio": None,
            "assertion_passed": None,
            "assertion_note": (
                f"rounds_total={rounds_total} < 2; need >= 2 rounds for decay"
            ),
        }

    last_round = rounds_total - 1

    def _median_upstream(label: str, round_idx: int) -> int | None:
        vals = [
            t["upstream_ms"]
            for t in turns
            if t.get("ok")
            and t.get("label") == label
            and t.get("round") == round_idx
            and t.get("upstream_ms") is not None
        ]
        if not vals:
            return None
        return int(statistics.median(vals))

    def _ratio(first: int | None, last: int | None) -> float | None:
        if first is None or last is None or first <= 0:
            return None
        return last / first

    a_first = _median_upstream("A", 0)
    a_last = _median_upstream("A", last_round)
    b_first = _median_upstream("B", 0)
    b_last = _median_upstream("B", last_round)

    a_ratio = _ratio(a_first, a_last)
    b_ratio = _ratio(b_first, b_last)

    passed: bool | None
    note: str
    if a_ratio is not None and b_ratio is not None:
        passed = a_ratio <= 1.0 and b_ratio <= 1.0
        if passed:
            note = "both labels: last_p50 <= first_p50 (APC warming works)"
        elif a_ratio > 1.0 and b_ratio > 1.0:
            note = "both labels regress: last_p50 > first_p50 (no APC benefit)"
        elif a_ratio > 1.0:
            note = "asymmetric: A regresses, B decays (B prefix held; A evicted)"
        else:
            note = "asymmetric: B regresses, A decays (A prefix held; B evicted)"
    elif a_ratio is None and b_ratio is None:
        passed = None
        note = "no upstream_ms decay data (header absent on both labels)"
    else:
        passed = None
        note = (
            f"partial decay data (A_ratio={a_ratio}, B_ratio={b_ratio}); "
            "engine omitted upstream_ms on one label"
        )

    return {
        "a_first_upstream_ms_p50": a_first,
        "a_last_upstream_ms_p50": a_last,
        "a_decay_ratio": a_ratio,
        "b_first_upstream_ms_p50": b_first,
        "b_last_upstream_ms_p50": b_last,
        "b_decay_ratio": b_ratio,
        "assertion_passed": passed,
        "assertion_note": note,
    }


def verdict(rate: float | None) -> tuple[str, str, int]:
    if rate is None:
        return (
            "unknown",
            "no hit-rate signal available — engine omitted cached_tokens header AND /metrics unreachable",
            3,
        )
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


def _run_self_check() -> int:
    """In-process smoke-check of header parsing and the header_cached_rate
    aggregator. Spins up a tiny HTTP server emitting !524's response headers,
    runs post_chat against it, and verifies parse + aggregate behavior. No
    live canary needed."""
    import http.server
    import threading

    canned_headers = {
        HEADER_UPSTREAM_MS: "567",
        HEADER_CACHED_TOKENS: "1234",
        HEADER_PROMPT_TOKENS: "30000",
        HEADER_FINISH_REASON: "stop",
    }
    canned_body = json.dumps(
        {
            "id": "self-check",
            "choices": [
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": "ok"},
                    "finish_reason": "stop",
                }
            ],
            "usage": {
                "prompt_tokens": 30000,
                "completion_tokens": 1,
                "total_tokens": 30001,
            },
        }
    ).encode()

    class _Handler(http.server.BaseHTTPRequestHandler):
        def do_POST(self):  # noqa: N802 (urllib method name)
            length = int(self.headers.get("Content-Length", "0"))
            self.rfile.read(length)
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            for k, v in canned_headers.items():
                self.send_header(k, v)
            self.send_header("Content-Length", str(len(canned_body)))
            self.end_headers()
            self.wfile.write(canned_body)

        def log_message(self, *_args, **_kw):  # silence stderr noise
            return

    httpd = http.server.HTTPServer(("127.0.0.1", 0), _Handler)
    port = httpd.server_address[1]
    th = threading.Thread(target=httpd.serve_forever, daemon=True)
    th.start()
    try:
        turn = post_chat(
            endpoint=f"http://127.0.0.1:{port}",
            model="self-check",
            system_prompt="sys",
            user_suffix="hi",
            max_tokens=1,
            timeout_s=5,
        )
    finally:
        httpd.shutdown()
        httpd.server_close()

    failures: list[str] = []

    def check(cond: bool, msg: str) -> None:
        if not cond:
            failures.append(msg)

    check(turn.get("ok") is True, f"turn.ok={turn.get('ok')!r}")
    check(turn.get("upstream_ms") == 567, f"upstream_ms={turn.get('upstream_ms')!r}")
    check(
        turn.get("cached_tokens") == 1234,
        f"cached_tokens={turn.get('cached_tokens')!r}",
    )
    check(
        turn.get("prompt_tokens") == 30000,
        f"prompt_tokens={turn.get('prompt_tokens')!r}",
    )
    check(
        turn.get("header_prompt_tokens") == 30000,
        f"header_prompt_tokens={turn.get('header_prompt_tokens')!r}",
    )
    check(
        turn.get("header_finish_reason") == "stop",
        f"header_finish_reason={turn.get('header_finish_reason')!r}",
    )
    ratio = turn.get("cached_ratio")
    check(
        ratio is not None and abs(ratio - (1234 / 30000)) < 1e-9,
        f"cached_ratio={ratio!r}",
    )

    # Aggregator: hits + misses + a header-absent row should yield
    # Sum(cached) / Sum(prompt) across eligible turns only.
    synth = [
        {"ok": True, "round": 0, "cached_tokens": 0, "prompt_tokens": 30000},
        {"ok": True, "round": 1, "cached_tokens": 27000, "prompt_tokens": 30000},
        {"ok": True, "round": 2, "cached_tokens": 28500, "prompt_tokens": 30000},
        {"ok": True, "round": 3, "cached_tokens": 29000, "prompt_tokens": 30000},
        # llama.cpp-style row: header absent
        {"ok": True, "round": 4, "cached_tokens": None, "prompt_tokens": 30000},
        # failed turn — ignored
        {"ok": False, "round": 5, "cached_tokens": 99999, "prompt_tokens": 30000},
    ]
    full = header_cached_rate(synth)
    expected_full = (0 + 27000 + 28500 + 29000) / (30000 * 4)
    check(
        full is not None and abs(full - expected_full) < 1e-9,
        f"header_cached_rate(full)={full!r} expected={expected_full!r}",
    )
    post3 = header_cached_rate(synth, rounds_max=2)
    expected_post3 = (0 + 27000 + 28500) / (30000 * 3)
    check(
        post3 is not None and abs(post3 - expected_post3) < 1e-9,
        f"header_cached_rate(post_third)={post3!r} expected={expected_post3!r}",
    )
    empty = header_cached_rate(
        [{"ok": True, "round": 0, "cached_tokens": None, "prompt_tokens": 30000}]
    )
    check(empty is None, f"header_cached_rate(no-eligible)={empty!r}")

    # ttft_decay aggregator: symmetric improvement (APC working).
    symmetric_turns = [
        {"ok": True, "round": 0, "label": "A", "upstream_ms": 28000},
        {"ok": True, "round": 0, "label": "B", "upstream_ms": 27500},
        {"ok": True, "round": 4, "label": "A", "upstream_ms": 3500},
        {"ok": True, "round": 4, "label": "B", "upstream_ms": 3200},
    ]
    sym = upstream_ms_decay(symmetric_turns, rounds_total=5)
    check(sym["assertion_passed"] is True, f"sym.passed={sym['assertion_passed']!r}")
    check(
        sym["a_first_upstream_ms_p50"] == 28000,
        f"sym.a_first={sym['a_first_upstream_ms_p50']!r}",
    )
    check(
        sym["a_last_upstream_ms_p50"] == 3500,
        f"sym.a_last={sym['a_last_upstream_ms_p50']!r}",
    )
    check(
        sym["a_decay_ratio"] is not None
        and abs(sym["a_decay_ratio"] - (3500 / 28000)) < 1e-9,
        f"sym.a_ratio={sym['a_decay_ratio']!r}",
    )

    # Asymmetric: A evicted (regresses), B held (decays).
    asym_turns = [
        {"ok": True, "round": 0, "label": "A", "upstream_ms": 28000},
        {"ok": True, "round": 0, "label": "B", "upstream_ms": 27500},
        {"ok": True, "round": 4, "label": "A", "upstream_ms": 29000},
        {"ok": True, "round": 4, "label": "B", "upstream_ms": 3200},
    ]
    asym = upstream_ms_decay(asym_turns, rounds_total=5)
    check(
        asym["assertion_passed"] is False, f"asym.passed={asym['assertion_passed']!r}"
    )
    check(
        "A regresses" in asym["assertion_note"],
        f"asym.note={asym['assertion_note']!r}",
    )

    # Single round (degenerate): no decay possible.
    one_round = upstream_ms_decay(symmetric_turns, rounds_total=1)
    check(
        one_round["assertion_passed"] is None
        and one_round["a_first_upstream_ms_p50"] is None,
        f"one_round={one_round!r}",
    )

    # All headers absent (llama.cpp): no decay data.
    no_header = upstream_ms_decay(
        [
            {"ok": True, "round": 0, "label": "A", "upstream_ms": None},
            {"ok": True, "round": 4, "label": "B", "upstream_ms": None},
        ],
        rounds_total=5,
    )
    check(
        no_header["assertion_passed"] is None
        and "header absent" in no_header["assertion_note"],
        f"no_header={no_header!r}",
    )

    if failures:
        for f in failures:
            print(f"[self-check] FAIL: {f}", file=sys.stderr)
        return 1
    print(
        "[self-check] OK: header parse + aggregator + ttft_decay behavior verified",
        file=sys.stderr,
    )
    return 0


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
    p.add_argument(
        "--report",
        default=None,
        help="output JSON report path (required unless --self-check)",
    )
    p.add_argument(
        "--seed", type=int, default=20260526, help="random seed for user-suffix wording"
    )
    p.add_argument(
        "--self-check",
        action="store_true",
        help="run in-process header-parse + aggregator smoke check, then exit",
    )
    args = p.parse_args(argv)

    if args.self_check:
        return _run_self_check()
    if not args.report:
        p.error("--report is required unless --self-check is set")

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
        "schema_version": "flexinfer.f4_apc_eviction_thrash.v3",
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
                cached = t.get("cached_tokens")
                cached_str = "absent" if cached is None else str(cached)
                ratio = t.get("cached_ratio")
                ratio_str = "n/a" if ratio is None else f"{ratio:.3f}"
                upstream = t.get("upstream_ms")
                upstream_str = "n/a" if upstream is None else f"{upstream}ms"
                print(
                    f"[f4-apc] r{r}-{label}: prompt={t['prompt_tokens']} "
                    f"completion={t['completion_tokens']} duration={t['duration_ms']}ms "
                    f"upstream={upstream_str} cached={cached_str} ratio={ratio_str} "
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

    # Header-derived rates (independent of /metrics; needs only the proxy PF).
    # post_third anchors on round_idx <= 2 to mirror the /metrics post-third snapshot.
    header_post_third_rate = header_cached_rate(report["turns"], rounds_max=2)
    header_aggregate_rate = header_cached_rate(report["turns"])

    # Verdict cascade: /metrics post-third wins when present (matches spec
    # wording); otherwise fall back to /metrics aggregate, then header post-third,
    # then header aggregate, then unknown. Header rates are kept in the summary
    # in all cases for cross-check.
    primary_rate: float | None
    primary_source: str
    if post_third_rate is not None:
        primary_rate = post_third_rate
        primary_source = "metrics_post_third"
    elif aggregate_rate is not None:
        primary_rate = aggregate_rate
        primary_source = "metrics_aggregate"
    elif header_post_third_rate is not None:
        primary_rate = header_post_third_rate
        primary_source = "header_post_third"
    elif header_aggregate_rate is not None:
        primary_rate = header_aggregate_rate
        primary_source = "header_aggregate"
    else:
        primary_rate = None
        primary_source = "none"
    v_label, v_reason, exit_code = verdict(primary_rate)

    durations = [t["duration_ms"] for t in report["turns"] if t["ok"]]
    upstream_ms_values = [
        t["upstream_ms"]
        for t in report["turns"]
        if t.get("ok") and t.get("upstream_ms") is not None
    ]
    completions = [t["completion_tokens"] for t in report["turns"] if t["ok"]]
    ttft_decay = upstream_ms_decay(report["turns"], rounds_total=args.rounds)
    report["summary"] = {
        "verdict": v_label,
        "reason": v_reason,
        "primary_hit_rate": primary_rate,
        "primary_hit_rate_source": primary_source,
        "aggregate_hit_rate": aggregate_rate,
        "post_third_alternation_hit_rate": post_third_rate,
        "header_aggregate_hit_rate": header_aggregate_rate,
        "header_post_third_alternation_hit_rate": header_post_third_rate,
        "turn_count": len(report["turns"]),
        "turn_success_count": len(durations),
        "duration_ms_p50": statistics.median(durations) if durations else None,
        "duration_ms_p95": (
            statistics.quantiles(durations, n=20)[-1]
            if len(durations) >= 20
            else max(durations) if durations else None
        ),
        "upstream_ms_p50": (
            statistics.median(upstream_ms_values) if upstream_ms_values else None
        ),
        "upstream_ms_p95": (
            statistics.quantiles(upstream_ms_values, n=20)[-1]
            if len(upstream_ms_values) >= 20
            else max(upstream_ms_values) if upstream_ms_values else None
        ),
        "mean_completion_tokens": statistics.mean(completions) if completions else None,
        "ttft_decay": ttft_decay,
    }

    out_path = Path(args.report)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2) + "\n")

    summary = report["summary"]
    print(
        f"[f4-apc] verdict={summary['verdict']} "
        f"primary_hit_rate={summary['primary_hit_rate']} "
        f"primary_source={summary['primary_hit_rate_source']} "
        f"metrics_aggregate={summary['aggregate_hit_rate']} "
        f"header_aggregate={summary['header_aggregate_hit_rate']} "
        f"turns={summary['turn_success_count']}/{summary['turn_count']} "
        f"duration_p50={summary['duration_ms_p50']}ms "
        f"upstream_p50={summary['upstream_ms_p50']}ms",
        file=sys.stderr,
    )
    td = summary["ttft_decay"]
    print(
        f"[f4-apc] ttft_decay: assertion_passed={td['assertion_passed']} "
        f"A first/last/ratio={td['a_first_upstream_ms_p50']}/"
        f"{td['a_last_upstream_ms_p50']}/{td['a_decay_ratio']} "
        f"B first/last/ratio={td['b_first_upstream_ms_p50']}/"
        f"{td['b_last_upstream_ms_p50']}/{td['b_decay_ratio']} "
        f"note={td['assertion_note']!r}",
        file=sys.stderr,
    )
    print(f"[f4-apc] report written to {out_path}", file=sys.stderr)
    return exit_code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
