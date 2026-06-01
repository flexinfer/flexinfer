#!/usr/bin/env python3
"""f4-tool-loop-as-prefix.py — F4-tool-loop-as-prefix kill-test.

Second leg of the F4 compound. The predecessor canary
(`scripts/f4-apc-eviction-thrash.py`, verdict *conditional* 2026-05-28)
proved the *alternating two-prefix* (multi-tenant) pattern survives eviction
at `maxModelLen ≤ 20480`. This runner tests the **distinct, still-unproven**
*append-only growing* pattern from the F4 brainstorm
(`.loom/brainstorm-f4-long-context-agent-2026-05-25.md`,
`F4-tool-loop-as-prefix` lines 140-146, `F4-instant-followup` lines 90-96).

The shape is a Claude-Code-style ReAct loop: an immutable `system + tool-schema`
prefix, then an append-only history of `(user → assistant → tool-result)`
rounds. The whole growing context is re-sent each round. If vLLM's chat-template
re-render keeps each turn a *block-aligned prefix extension* of the previous
turn, then per-turn prefill cost tracks only the new tail tokens, not the total
prompt length — the brainstorm bet "hit% stays >90% across 20 rounds".

Two independent signals, mirroring the sibling harness:

1. Prefix-hit ratio (preferred when present): per-turn
   `X-Flexinfer-Cached-Tokens / usage.prompt_tokens`. Median over the warm
   rounds (round index >= 1; round 0 is the cold first prefill) is the primary
   verdict. The brainstorm bet anchors on >= 0.90.

2. TTFT-flatness fallback (engine-agnostic): the gemma4 engine omitted
   `cached_tokens` on every turn during the 2026-05-28 live run, so the verdict
   must survive without it. With APC working, `X-Flexinfer-Upstream-Ms` stays
   roughly flat across rounds even as `prompt_tokens` grows several-fold (only
   the new tail is prefilled). Without APC, upstream_ms grows in proportion to
   prompt_tokens (the whole context is reprocessed every turn). This is the
   `F4-instant-followup` bet "turn-N TTFT <= turn-2 TTFT" applied to a growing
   context.

Verdict cascade (first available wins; both signals always recorded):
    cached-ratio present  -> median warm prefix-hit ratio
    else                  -> TTFT-flatness assertion
    else                  -> unknown (infra failure)

Pass criterion (default):
    median warm prefix-hit ratio >= 0.90  (cache reuse is the dominant cost
    variable; the growing tool history is effectively a sunk cost), OR — in the
    cached-token-absent fallback — last-round upstream_ms <= warm-round
    upstream_ms * 1.5 while prompt_tokens grew >= 2x.

Fail criterion:
    median warm prefix-hit ratio < 0.50 (template re-render busts block
    alignment; cache reuse collapses), OR — fallback — upstream_ms scales with
    prompt_tokens (prefill reprocesses the whole context every round).

Ambiguous middle:
    Documented and recorded as conditional.

Usage:
    # Pre-conditions (see plan doc operator runbook):
    #   1. APC canary Ready at maxModelLen 20480 (32k is structurally
    #      infeasible per the canary verdict).
    #   2. proxy port-forward in another shell:
    #        kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 18080:80
    #   3. (optional) canary metrics port-forward for /metrics cross-check.

    python3 scripts/f4-tool-loop-as-prefix.py \\
        --endpoint http://localhost:18080 \\
        --model gemma4-26b-a4b-gptq-apc-canary \\
        --metrics http://localhost:18000/metrics \\
        --rounds 20 \\
        --report .loom/local/validation/f4-tool-loop/$(date -u +%F)/report.json

Exit codes:
    0  pass (prefix-hit >= 0.90, or flatness assertion holds)
    1  fail (prefix-hit < 0.50, or upstream_ms scales with prompt)
    2  conditional (ambiguous middle)
    3  infrastructure failure (timeouts, parse errors, no usable signal)
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
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
DEFAULT_ROUNDS = 20
DEFAULT_SYSTEM_TOKENS = 6000
DEFAULT_TOOL_RESULT_TOKENS = 400
DEFAULT_MAX_TOKENS = 256
DEFAULT_TIMEOUT_S = 600

# Prefix-hit gates (append-only single-session should reuse trivially, so the
# pass bar is higher than the multi-tenant eviction test's 0.50).
PASS_HIT_RATE = 0.90
FAIL_HIT_RATE = 0.50

# TTFT-flatness fallback gates (engine omits cached_tokens).
FLATNESS_PASS_TOLERANCE = 1.5  # last upstream_ms <= warm * this == APC working
FLATNESS_MIN_GROWTH = 2.0  # prompt must grow >= this for the assertion to mean anything
# fail when prefill scales with prompt: flatness_ratio >= prompt_growth * this
FLATNESS_FAIL_SCALE = 0.70

HEADER_UPSTREAM_MS = "X-Flexinfer-Upstream-Ms"
HEADER_CACHED_TOKENS = "X-Flexinfer-Cached-Tokens"
HEADER_PROMPT_TOKENS = "X-Flexinfer-Prompt-Tokens"
HEADER_FINISH_REASON = "X-Flexinfer-Finish-Reason"

# Immutable system prefix: a senior-agent persona plus a block of "tool schemas".
# The body is filler repeated to the target token count. This is the slowest-
# mutating part of the context (it never changes), so per the mutability-ordering
# bet it sits first and is cached across every round.
SYSTEM_INTRO = (
    "You are a senior infrastructure agent operating a Kubernetes GPU fleet. "
    "You answer using ONLY the tool results provided below in the conversation. "
    "Available tools (JSON schema):\n"
)
SYSTEM_TOOL_SCHEMA = (
    '{"name":"kubectl_get","description":"read a resource",'
    '"parameters":{"resource":"string","namespace":"string","name":"string"}} '
    '{"name":"loki_query","description":"query logs",'
    '"parameters":{"selector":"string","since":"string","limit":"int"}} '
    '{"name":"prom_query","description":"instant PromQL",'
    '"parameters":{"expr":"string"}} '
    '{"name":"flux_get","description":"reconciler state",'
    '"parameters":{"kind":"string","namespace":"string"}} '
    '{"name":"node_describe","description":"node detail",'
    '"parameters":{"node":"string"}} '
)

# Synthetic tool-result filler. Each round appends one of these as the prior
# tool's output, growing the append-only history. The runtime only ever sees
# text; tool semantics are irrelevant — what matters is the token layout.
TOOL_RESULT_BODY = (
    "RESULT: node cblevins-7900xtx allocatable gpu=2 memory=62Gi; pod "
    "gemma4-26b-a4b-gptq-apc-canary phase=Running restarts=0; vllm "
    "prefix_cache_queries_total=14821 hits_total=13190; kv_cache_usage=0.41; "
    "num_requests_running=1 waiting=0; gpu_cache_usage_perc=0.41; "
    "last_reconcile=Ready age=12m; "
)

# Per-round user questions referencing the accumulating tool history.
USER_QUESTIONS = [
    "Call kubectl_get for the canary pod and report its phase.",
    "From the last result, how many restarts does the pod have?",
    "Query prefix cache hit rate; is it above 0.85?",
    "What is the current kv_cache_usage from the latest result?",
    "Are there any requests waiting in the queue right now?",
    "Summarize the node's allocatable GPU count.",
    "Has the reconciler reported Ready, and for how long?",
    "Given all results so far, is the canary healthy? One word.",
    "What was the hits_total in the most recent prom result?",
    "Cross-check: did gpu_cache_usage_perc match kv_cache_usage?",
]


def build_token_filler(intro: str, body: str, target_tokens: int) -> str:
    """Build text targeting ~target_tokens on the Gemma SP tokenizer
    (~4 chars/token for English). Exact counts come back in usage.prompt_tokens;
    we record both target and actual."""
    target_chars = target_tokens * 4
    out = [intro]
    cur = len(intro)
    while cur < target_chars:
        out.append(body)
        cur += len(body)
    return "".join(out)


def now_ms() -> int:
    return int(time.time() * 1000)


def _header_int(headers, name: str) -> int | None:
    """Parse a non-negative integer response header. None when absent, empty, or
    unparseable so the caller distinguishes 'engine did not report' from zero."""
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
    messages: list[dict],
    max_tokens: int,
    timeout_s: int,
) -> dict:
    """Send one chat completion with the full append-only message list, return a
    turn record. Distinct from the sibling harness's two-message form: here the
    growing `messages` list IS the test surface."""
    body = {
        "model": model,
        "messages": messages,
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
        "content": content,
        "content_head": content[:120],
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
    """Best-effort scrape of vLLM's prefix-cache hit/query counters as a
    cross-check. None of the verdict depends on this when headers are present."""
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


def prefix_hit_median(turns: list[dict], warm_from_round: int = 1) -> float | None:
    """Median per-turn cached_ratio over warm rounds (round >= warm_from_round).
    Round 0 is the cold first prefill and is excluded. Returns None if no warm
    turn reported cached_tokens (engine omission)."""
    ratios = [
        t["cached_ratio"]
        for t in turns
        if t.get("ok")
        and t.get("round", -1) >= warm_from_round
        and t.get("cached_ratio") is not None
    ]
    if not ratios:
        return None
    return statistics.median(ratios)


def ttft_flatness(turns: list[dict], warm_round: int = 1) -> dict:
    """Engine-agnostic fallback: does upstream_ms stay flat while prompt_tokens
    grows? With APC, only the new tail is prefilled, so the last warm round's
    upstream_ms should be close to the first warm round's despite a much larger
    prompt. Without APC, prefill scales with prompt_tokens.

    flatness_ratio = upstream_ms[last] / upstream_ms[warm]   (lower = better)
    prompt_growth  = prompt_tokens[last] / prompt_tokens[warm]

    assertion_passed True  when flatness_ratio <= FLATNESS_PASS_TOLERANCE AND
                           prompt_growth >= FLATNESS_MIN_GROWTH
                     False when flatness_ratio >= prompt_growth * FLATNESS_FAIL_SCALE
                           (prefill clearly scales with prompt)
                     None  when data is insufficient (no headers, < 2 warm rounds,
                           or prompt did not grow enough to discriminate)."""
    warm_turns = [
        t
        for t in turns
        if t.get("ok")
        and t.get("round", -1) >= warm_round
        and t.get("upstream_ms") is not None
        and (t.get("prompt_tokens") or 0) > 0
    ]
    base = {
        "warm_round": warm_round,
        "warm_upstream_ms": None,
        "last_upstream_ms": None,
        "warm_prompt_tokens": None,
        "last_prompt_tokens": None,
        "flatness_ratio": None,
        "prompt_growth": None,
        "assertion_passed": None,
        "assertion_note": "",
    }
    if len(warm_turns) < 2:
        base["assertion_note"] = (
            f"only {len(warm_turns)} warm turn(s) with upstream_ms; "
            "need >= 2 to assess flatness"
        )
        return base
    first = warm_turns[0]
    last = warm_turns[-1]
    warm_ms = first["upstream_ms"]
    last_ms = last["upstream_ms"]
    warm_pt = first["prompt_tokens"]
    last_pt = last["prompt_tokens"]
    flatness_ratio = (last_ms / warm_ms) if warm_ms > 0 else None
    prompt_growth = (last_pt / warm_pt) if warm_pt > 0 else None

    base.update(
        {
            "warm_upstream_ms": warm_ms,
            "last_upstream_ms": last_ms,
            "warm_prompt_tokens": warm_pt,
            "last_prompt_tokens": last_pt,
            "flatness_ratio": flatness_ratio,
            "prompt_growth": prompt_growth,
        }
    )

    if flatness_ratio is None or prompt_growth is None:
        base["assertion_note"] = "degenerate zero baseline (upstream_ms or prompt 0)"
        return base
    if prompt_growth < FLATNESS_MIN_GROWTH:
        base["assertion_passed"] = None
        base["assertion_note"] = (
            f"prompt_growth={prompt_growth:.2f} < {FLATNESS_MIN_GROWTH}; "
            "context did not grow enough to discriminate caching from cold"
        )
        return base
    if flatness_ratio <= FLATNESS_PASS_TOLERANCE:
        base["assertion_passed"] = True
        base["assertion_note"] = (
            f"upstream_ms flat (ratio={flatness_ratio:.2f} <= "
            f"{FLATNESS_PASS_TOLERANCE}) despite {prompt_growth:.2f}x prompt "
            "growth — APC reuses the growing prefix"
        )
    elif flatness_ratio >= prompt_growth * FLATNESS_FAIL_SCALE:
        base["assertion_passed"] = False
        base["assertion_note"] = (
            f"upstream_ms scales with prompt (ratio={flatness_ratio:.2f} >= "
            f"{prompt_growth:.2f}*{FLATNESS_FAIL_SCALE}) — prefill reprocesses "
            "the whole context; no APC benefit"
        )
    else:
        base["assertion_passed"] = None
        base["assertion_note"] = (
            f"ambiguous: ratio={flatness_ratio:.2f}, growth={prompt_growth:.2f} "
            "(some reuse but not flat)"
        )
    return base


def verdict(
    hit_median: float | None, flatness: dict
) -> tuple[str, str, int, float | None, str]:
    """Cascade: prefix-hit median wins when present; else TTFT flatness; else
    unknown. Returns (label, reason, exit_code, primary_value, primary_source)."""
    if hit_median is not None:
        if hit_median >= PASS_HIT_RATE:
            return (
                "pass",
                f"median warm prefix-hit ratio={hit_median:.3f} >= {PASS_HIT_RATE}",
                0,
                hit_median,
                "prefix_hit_ratio",
            )
        if hit_median < FAIL_HIT_RATE:
            return (
                "fail",
                f"median warm prefix-hit ratio={hit_median:.3f} < {FAIL_HIT_RATE} "
                "— append-only prefix reuse collapsed",
                1,
                hit_median,
                "prefix_hit_ratio",
            )
        return (
            "conditional",
            f"median warm prefix-hit ratio={hit_median:.3f} in "
            f"[{FAIL_HIT_RATE}, {PASS_HIT_RATE})",
            2,
            hit_median,
            "prefix_hit_ratio",
        )
    # Fallback: TTFT flatness (engine omitted cached_tokens).
    passed = flatness.get("assertion_passed")
    ratio = flatness.get("flatness_ratio")
    if passed is True:
        return (
            "pass",
            "cached_tokens absent; TTFT-flatness fallback: "
            + flatness["assertion_note"],
            0,
            ratio,
            "ttft_flatness",
        )
    if passed is False:
        return (
            "fail",
            "cached_tokens absent; TTFT-flatness fallback: "
            + flatness["assertion_note"],
            1,
            ratio,
            "ttft_flatness",
        )
    if ratio is not None:
        return (
            "conditional",
            "cached_tokens absent; TTFT-flatness inconclusive: "
            + flatness["assertion_note"],
            2,
            ratio,
            "ttft_flatness",
        )
    return (
        "unknown",
        "no usable signal — engine omitted cached_tokens AND no upstream_ms "
        "decay data (proxy headers absent on every turn?)",
        3,
        None,
        "none",
    )


def _run_self_check() -> int:
    """Offline smoke-check (no cluster). Verifies header parsing against !524's
    response headers, the prefix-hit median aggregator, the TTFT-flatness
    assertion across pass/fail/insufficient-growth cases, and that post_chat
    accepts the append-only message list shape."""
    import http.server
    import threading

    canned_headers = {
        HEADER_UPSTREAM_MS: "489",
        HEADER_CACHED_TOKENS: "5800",
        HEADER_PROMPT_TOKENS: "6400",
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
                "prompt_tokens": 6400,
                "completion_tokens": 1,
                "total_tokens": 6401,
            },
        }
    ).encode()

    captured = {}

    class _Handler(http.server.BaseHTTPRequestHandler):
        def do_POST(self):  # noqa: N802 (urllib method name)
            length = int(self.headers.get("Content-Length", "0"))
            captured["body"] = json.loads(self.rfile.read(length).decode())
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            for k, v in canned_headers.items():
                self.send_header(k, v)
            self.send_header("Content-Length", str(len(canned_body)))
            self.end_headers()
            self.wfile.write(canned_body)

        def log_message(self, *_args, **_kw):
            return

    httpd = http.server.HTTPServer(("127.0.0.1", 0), _Handler)
    port = httpd.server_address[1]
    th = threading.Thread(target=httpd.serve_forever, daemon=True)
    th.start()
    try:
        turn = post_chat(
            endpoint=f"http://127.0.0.1:{port}",
            model="self-check",
            messages=[
                {"role": "system", "content": "sys"},
                {"role": "user", "content": "u1"},
                {"role": "assistant", "content": "a1"},
                {"role": "user", "content": "TOOL_RESULT[0]: ..."},
                {"role": "user", "content": "u2"},
            ],
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

    # post_chat accepted the append-only message list and parsed headers.
    check(turn.get("ok") is True, f"turn.ok={turn.get('ok')!r}")
    check(turn.get("upstream_ms") == 489, f"upstream_ms={turn.get('upstream_ms')!r}")
    check(
        turn.get("cached_tokens") == 5800,
        f"cached_tokens={turn.get('cached_tokens')!r}",
    )
    check(
        turn.get("prompt_tokens") == 6400,
        f"prompt_tokens={turn.get('prompt_tokens')!r}",
    )
    ratio = turn.get("cached_ratio")
    check(
        ratio is not None and abs(ratio - (5800 / 6400)) < 1e-9,
        f"cached_ratio={ratio!r}",
    )
    check(
        isinstance(captured.get("body"), dict)
        and len(captured["body"].get("messages", [])) == 5,
        f"server saw messages={captured.get('body', {}).get('messages')!r}",
    )

    # prefix_hit_median: round 0 (cold) excluded; warm rounds averaged.
    synth = [
        {"ok": True, "round": 0, "cached_ratio": 0.0},  # cold — excluded
        {"ok": True, "round": 1, "cached_ratio": 0.94},
        {"ok": True, "round": 2, "cached_ratio": 0.96},
        {"ok": True, "round": 3, "cached_ratio": 0.97},
    ]
    med = prefix_hit_median(synth)
    check(med is not None and abs(med - 0.96) < 1e-9, f"prefix_hit_median={med!r}")
    # All warm rounds lack cached_ratio (engine omission) -> None.
    none_med = prefix_hit_median([{"ok": True, "round": 1, "cached_ratio": None}])
    check(none_med is None, f"prefix_hit_median(no-cached)={none_med!r}")

    # TTFT flatness — PASS: prompt grew 4x but upstream_ms barely moved.
    flat_pass = ttft_flatness(
        [
            {"ok": True, "round": 0, "upstream_ms": 9000, "prompt_tokens": 6000},
            {"ok": True, "round": 1, "upstream_ms": 520, "prompt_tokens": 6800},
            {"ok": True, "round": 5, "upstream_ms": 610, "prompt_tokens": 27000},
        ]
    )
    check(
        flat_pass["assertion_passed"] is True,
        f"flat_pass.passed={flat_pass['assertion_passed']!r} note={flat_pass['assertion_note']!r}",
    )
    check(
        abs(flat_pass["prompt_growth"] - (27000 / 6800)) < 1e-9,
        f"flat_pass.growth={flat_pass['prompt_growth']!r}",
    )

    # TTFT flatness — FAIL: upstream_ms scales with prompt (no caching).
    flat_fail = ttft_flatness(
        [
            {"ok": True, "round": 1, "upstream_ms": 700, "prompt_tokens": 6800},
            {"ok": True, "round": 5, "upstream_ms": 2800, "prompt_tokens": 27000},
        ]
    )
    check(
        flat_fail["assertion_passed"] is False,
        f"flat_fail.passed={flat_fail['assertion_passed']!r} note={flat_fail['assertion_note']!r}",
    )

    # TTFT flatness — insufficient growth -> None (cannot discriminate).
    flat_nogrow = ttft_flatness(
        [
            {"ok": True, "round": 1, "upstream_ms": 600, "prompt_tokens": 6800},
            {"ok": True, "round": 2, "upstream_ms": 650, "prompt_tokens": 7000},
        ]
    )
    check(
        flat_nogrow["assertion_passed"] is None
        and "did not grow" in flat_nogrow["assertion_note"],
        f"flat_nogrow={flat_nogrow!r}",
    )

    # TTFT flatness — too few warm turns -> None.
    flat_few = ttft_flatness(
        [{"ok": True, "round": 1, "upstream_ms": 600, "prompt_tokens": 6800}]
    )
    check(
        flat_few["assertion_passed"] is None
        and "need >= 2" in flat_few["assertion_note"],
        f"flat_few={flat_few!r}",
    )

    # verdict cascade — prefix-hit wins when present.
    lbl, _, ec, val, src = verdict(0.95, flat_fail)
    check(
        lbl == "pass" and ec == 0 and src == "prefix_hit_ratio",
        f"verdict(0.95)={lbl},{ec},{src}",
    )
    lbl, _, ec, _, _ = verdict(0.30, flat_pass)
    check(lbl == "fail" and ec == 1, f"verdict(0.30)={lbl},{ec}")
    lbl, _, ec, _, _ = verdict(0.70, flat_pass)
    check(lbl == "conditional" and ec == 2, f"verdict(0.70)={lbl},{ec}")
    # verdict cascade — falls back to flatness when cached absent.
    lbl, _, ec, _, src = verdict(None, flat_pass)
    check(
        lbl == "pass" and ec == 0 and src == "ttft_flatness",
        f"verdict(None,pass)={lbl},{ec},{src}",
    )
    lbl, _, ec, _, _ = verdict(None, flat_fail)
    check(lbl == "fail" and ec == 1, f"verdict(None,fail)={lbl},{ec}")
    lbl, _, ec, _, src = verdict(
        None, {"assertion_passed": None, "flatness_ratio": None, "assertion_note": "x"}
    )
    check(
        lbl == "unknown" and ec == 3 and src == "none",
        f"verdict(None,none)={lbl},{ec},{src}",
    )

    # build_token_filler reaches the target token band.
    sysprompt = build_token_filler(
        SYSTEM_INTRO + SYSTEM_TOOL_SCHEMA, TOOL_RESULT_BODY, 6000
    )
    approx_tokens = len(sysprompt) / 4
    check(
        5500 <= approx_tokens <= 7000,
        f"system filler approx_tokens={approx_tokens:.0f} (target ~6000)",
    )

    if failures:
        for f in failures:
            print(f"[self-check] FAIL: {f}", file=sys.stderr)
        return 1
    print(
        "[self-check] OK: header parse + append-only post + prefix-hit median + "
        "ttft-flatness + verdict cascade + filler builder verified",
        file=sys.stderr,
    )
    return 0


def main(argv: list[str]) -> int:
    p = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    p.add_argument("--endpoint", default=DEFAULT_ENDPOINT)
    p.add_argument("--model", default=DEFAULT_MODEL)
    p.add_argument(
        "--metrics",
        default=DEFAULT_METRICS,
        help="canary /metrics URL for cross-check; '' to skip",
    )
    p.add_argument("--rounds", type=int, default=DEFAULT_ROUNDS)
    p.add_argument(
        "--system-tokens",
        type=int,
        default=DEFAULT_SYSTEM_TOKENS,
        help="target token count for the immutable system+tool-schema prefix",
    )
    p.add_argument(
        "--tool-result-tokens",
        type=int,
        default=DEFAULT_TOOL_RESULT_TOKENS,
        help="target token count for each appended synthetic tool result",
    )
    p.add_argument("--max-tokens", type=int, default=DEFAULT_MAX_TOKENS)
    p.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT_S)
    p.add_argument("--report", default=None, help="output JSON report path")
    p.add_argument("--seed", type=int, default=20260601)
    p.add_argument(
        "--self-check",
        action="store_true",
        help="run offline header-parse + aggregator + verdict smoke check, then exit",
    )
    args = p.parse_args(argv)

    if args.self_check:
        return _run_self_check()
    if not args.report:
        p.error("--report is required unless --self-check is set")

    rng = random.Random(args.seed)
    system_prompt = build_token_filler(
        SYSTEM_INTRO + SYSTEM_TOOL_SCHEMA, TOOL_RESULT_BODY, args.system_tokens
    )
    tool_result_filler = build_token_filler(
        "", TOOL_RESULT_BODY, args.tool_result_tokens
    )

    report = {
        "schema_version": "flexinfer.f4_tool_loop_as_prefix.v1",
        "created_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "model": args.model,
        "endpoint": args.endpoint,
        "rounds": args.rounds,
        "system_tokens_target": args.system_tokens,
        "tool_result_tokens_target": args.tool_result_tokens,
        "max_tokens": args.max_tokens,
        "turns": [],
        "metrics": {},
        "summary": {},
    }

    print(
        f"[f4-tool-loop] model={args.model} rounds={args.rounds} "
        f"system_tokens={args.system_tokens} tool_result_tokens={args.tool_result_tokens} "
        f"max_tokens={args.max_tokens}",
        file=sys.stderr,
    )

    snap_before = scrape_metrics(args.metrics)
    report["metrics"]["before"] = snap_before

    # Append-only conversation: immutable system prefix, then growing
    # (user -> assistant -> tool-result) rounds. The full list is re-sent each
    # round so each turn is a prefix extension of the previous one.
    messages: list[dict] = [{"role": "system", "content": system_prompt}]

    for r in range(args.rounds):
        question = USER_QUESTIONS[r % len(USER_QUESTIONS)]
        # nonce keeps the question distinct enough that the runtime cannot return
        # a degenerate cached completion, while staying tiny vs the prefix.
        nonce = rng.randint(1000, 9999)
        user_turn = f"[step {r} #{nonce}] {question}"
        messages.append({"role": "user", "content": user_turn})

        t = post_chat(
            endpoint=args.endpoint,
            model=args.model,
            messages=messages,
            max_tokens=args.max_tokens,
            timeout_s=args.timeout,
        )
        t.update({"round": r, "user_turn": user_turn})
        report["turns"].append(t)

        if t["ok"]:
            # Append the model's reply + a synthetic tool result, growing the
            # append-only history for the next round.
            messages.append({"role": "assistant", "content": t["content"]})
            messages.append(
                {
                    "role": "user",
                    "content": f"TOOL_RESULT[{r}] {tool_result_filler}",
                }
            )
            cached = t.get("cached_tokens")
            cached_str = "absent" if cached is None else str(cached)
            ratio = t.get("cached_ratio")
            ratio_str = "n/a" if ratio is None else f"{ratio:.3f}"
            upstream = t.get("upstream_ms")
            upstream_str = "n/a" if upstream is None else f"{upstream}ms"
            print(
                f"[f4-tool-loop] r{r}: prompt={t['prompt_tokens']} "
                f"completion={t['completion_tokens']} duration={t['duration_ms']}ms "
                f"upstream={upstream_str} cached={cached_str} hit_ratio={ratio_str}",
                file=sys.stderr,
            )
        else:
            print(
                f"[f4-tool-loop] r{r}: FAILED error={t['error']} "
                f"duration={t['duration_ms']}ms detail={t.get('detail', '')!r}",
                file=sys.stderr,
            )
            # Drop the unanswered user turn so the next round stays append-only
            # consistent (no dangling user without assistant reply).
            messages.pop()

    snap_after = scrape_metrics(args.metrics)
    report["metrics"]["after"] = snap_after

    hit_median = prefix_hit_median(report["turns"])
    flatness = ttft_flatness(report["turns"])
    v_label, v_reason, exit_code, primary_value, primary_source = verdict(
        hit_median, flatness
    )

    durations = [t["duration_ms"] for t in report["turns"] if t["ok"]]
    upstream_values = [
        t["upstream_ms"]
        for t in report["turns"]
        if t.get("ok") and t.get("upstream_ms") is not None
    ]
    prompt_tokens = [t["prompt_tokens"] for t in report["turns"] if t.get("ok")]
    completions = [t["completion_tokens"] for t in report["turns"] if t["ok"]]

    report["summary"] = {
        "verdict": v_label,
        "reason": v_reason,
        "primary_value": primary_value,
        "primary_source": primary_source,
        "prefix_hit_ratio_median": hit_median,
        "ttft_flatness": flatness,
        "turn_count": len(report["turns"]),
        "turn_success_count": len(durations),
        "prompt_tokens_first": prompt_tokens[0] if prompt_tokens else None,
        "prompt_tokens_last": prompt_tokens[-1] if prompt_tokens else None,
        "duration_ms_p50": statistics.median(durations) if durations else None,
        "upstream_ms_p50": (
            statistics.median(upstream_values) if upstream_values else None
        ),
        "mean_completion_tokens": statistics.mean(completions) if completions else None,
    }

    out_path = Path(args.report)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(report, indent=2) + "\n")

    s = report["summary"]
    print(
        f"[f4-tool-loop] verdict={s['verdict']} primary={s['primary_value']} "
        f"source={s['primary_source']} prefix_hit_median={s['prefix_hit_ratio_median']} "
        f"prompt {s['prompt_tokens_first']}->{s['prompt_tokens_last']} "
        f"turns={s['turn_success_count']}/{s['turn_count']} "
        f"upstream_p50={s['upstream_ms_p50']}ms",
        file=sys.stderr,
    )
    f = s["ttft_flatness"]
    print(
        f"[f4-tool-loop] ttft_flatness: passed={f['assertion_passed']} "
        f"warm/last={f['warm_upstream_ms']}/{f['last_upstream_ms']}ms "
        f"ratio={f['flatness_ratio']} prompt_growth={f['prompt_growth']} "
        f"note={f['assertion_note']!r}",
        file=sys.stderr,
    )
    print(f"[f4-tool-loop] report written to {out_path}", file=sys.stderr)
    return exit_code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
