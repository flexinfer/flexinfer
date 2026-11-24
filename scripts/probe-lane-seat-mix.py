#!/usr/bin/env python3
"""Seat-contention probe: one long request plus a stream of short ones (stdlib).

Measures what short (interactive-sized) requests see while a long prompt is
prefilling and decoding on the same vLLM lane. It is the before/after harness
for changing a lane's maxNumSeqs. Run it inside the serving pod so it times
the engine, not the proxy:

    kubectl -n flexinfer-system exec -i deploy/<lane> -- \\
        python3 - --model <served-name> --long 60000 < scripts/probe-lane-seat-mix.py

The long request gets a fresh nonce (no prefix-cache hit) and max_tokens
--long-out. While it runs, a short request (--short tokens, --short-out
output tokens, fresh nonce) starts every --interval seconds; up to
--short-parallel of them may be in flight. When the long request finishes,
the probe stops issuing shorts, waits for the ones in flight, and prints one
JSON summary line: short TTFT p50/p95/max, the long request's TTFT and decode
tok/s, and the engine's Running/Waiting gauges sampled from /metrics.

The "during_prefill" block covers only what happened before the long request's
first token, which is the window a step-budget change (longPrefillTokenThreshold)
acts on: how many shorts started, got a first token and finished in it, the
decode rate of the ones that finished in it, and the gap between their
consecutive stream chunks. vLLM streams one chunk per engine step, so that gap
is the step time a short request sees while the long prompt prefills.

Two patterns. The defaults (a short every 5 s, four in flight) offer more than
one free slot can serve, so their TTFT is mostly queueing: use them to see the
queue. For a before/after on step time, run closed-loop with
--short-parallel 1 --interval 1, so each short's total time is its own prefill
plus its decode at the contended rate.
"""

import argparse
import json
import random
import re
import statistics
import threading
import time
import urllib.request

# Calibrated filler: with these words a prompt of N/1.3 words is ~N Qwen tokens
# (85,000 -> 85,964 measured 2026-11-21 through the proxy).
WORDS = "alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike".split()


def text_of(tokens, nonce):
    rnd = random.Random(nonce)
    # ~1.3 Qwen tokens per filler word
    body = " ".join(rnd.choice(WORDS) for _ in range(int(tokens / 1.3)))
    return (
        f"[{nonce}] Read the log below, then reply with one sentence about it.\n{body}"
    )


def stream(base, model, text, max_tokens, timeout):
    req = urllib.request.Request(
        base + "/v1/chat/completions",
        data=json.dumps(
            {
                "model": model,
                "messages": [{"role": "user", "content": text}],
                "max_tokens": max_tokens,
                "temperature": 0,
                "stream": True,
                "stream_options": {"include_usage": True},
            }
        ).encode(),
        headers={"Content-Type": "application/json"},
    )
    t0 = time.time()
    ttft = None
    usage = None
    chunks = []  # arrival time of every content chunk (one per engine step)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        for raw in r:
            line = raw.decode().strip()
            if not line.startswith("data:") or line.endswith("[DONE]"):
                continue
            d = json.loads(line[5:])
            if d.get("usage"):
                usage = d["usage"]
            for c in d.get("choices", []):
                if (c.get("delta") or {}).get("content"):
                    now = time.time()
                    chunks.append(now)
                    if ttft is None:
                        ttft = now - t0
    total = time.time() - t0
    out = (usage or {}).get("completion_tokens") or 0
    decode = (out - 1) / (total - ttft) if ttft and total > ttft and out > 1 else None
    return {
        "start": t0,
        "chunks": chunks,
        "ttft": ttft,
        "total": total,
        "prompt_tokens": (usage or {}).get("prompt_tokens"),
        "completion_tokens": out,
        "decode_tps": decode,
    }


SAMPLE_RE = re.compile(r"^(vllm:[a-z_]+)(?:\{[^}]*\})?\s+(\S+)$")


def parse_gauges(text):
    """Running and Waiting from a /metrics scrape, by exact metric name.

    A prefix match is wrong: vllm:num_requests_waiting_by_reason{reason=...}
    follows vllm:num_requests_waiting, and its last series (deferred) would
    overwrite the real queue depth.
    """
    found = {"vllm:num_requests_running": None, "vllm:num_requests_waiting": None}
    for line in text.splitlines():
        m = SAMPLE_RE.match(line.strip())
        if m and m.group(1) in found:
            found[m.group(1)] = (found[m.group(1)] or 0.0) + float(m.group(2))
    return found["vllm:num_requests_running"], found["vllm:num_requests_waiting"]


def gauges(base):
    try:
        with urllib.request.urlopen(base + "/metrics", timeout=5) as r:
            return parse_gauges(r.read().decode())
    except Exception:
        return None


def pct(v, p):
    if not v:
        return None
    v = sorted(v)
    return round(v[min(len(v) - 1, int(round(p * (len(v) - 1))))], 3)


def during_prefill(long_result, shorts):
    """What the shorts saw before the long request's first token."""
    if long_result.get("ttft") is None or long_result.get("start") is None:
        return None
    edge = long_result["start"] + long_result["ttft"]
    started = [s for s in shorts if s["start"] < edge]
    first = [s for s in started if s.get("ttft") and s["start"] + s["ttft"] < edge]
    finished = [s for s in first if s["start"] + s["total"] <= edge]
    gaps = []
    for s in first:
        inside = [t for t in s.get("chunks", []) if t < edge]
        gaps.extend(b - a for a, b in zip(inside, inside[1:]))
    decodes = [s["decode_tps"] for s in finished if s.get("decode_tps")]
    return {
        "started": len(started),
        "first_token": len(first),
        "finished": len(finished),
        "ttft_p50": pct([s["ttft"] for s in first], 0.5),
        "total_p50": pct([s["total"] for s in finished], 0.5),
        "decode_median": round(statistics.median(decodes), 2) if decodes else None,
        "step_gap_p50": pct(gaps, 0.5),
        "step_gap_p95": pct(gaps, 0.95),
        "step_gaps": len(gaps),
    }


def summarize(label, model, long_result, shorts, errors, samples):
    ttfts = [s["ttft"] for s in shorts if s.get("ttft") is not None]
    totals = [s["total"] for s in shorts if s.get("ttft") is not None]
    decodes = [s["decode_tps"] for s in shorts if s.get("decode_tps")]
    runs = [s[0] for s in samples if s[0] is not None]
    waits = [s[1] for s in samples if s[1] is not None]
    return {
        "label": label,
        "model": model,
        "long": {
            k: (round(v, 3) if isinstance(v, float) else v)
            for k, v in long_result.items()
            if k not in ("start", "chunks")
        },
        "short": {
            "n": len(shorts),
            "errors": len(errors),
            "ttft_p50": pct(ttfts, 0.5),
            "ttft_p95": pct(ttfts, 0.95),
            "ttft_max": pct(ttfts, 1.0),
            "total_p50": pct(totals, 0.5),
            "total_p95": pct(totals, 0.95),
            "decode_median": round(statistics.median(decodes), 2) if decodes else None,
        },
        "during_prefill": during_prefill(long_result, shorts),
        "engine": {
            "samples": len(samples),
            "running_max": max(runs) if runs else None,
            "waiting_max": max(waits) if waits else None,
            "waiting_nonzero_frac": (
                round(sum(1 for w in waits if w > 0) / len(waits), 3) if waits else None
            ),
        },
        "errors": errors[:5],
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base-url", default="http://127.0.0.1:8000")
    ap.add_argument("--model", required=True)
    ap.add_argument(
        "--long", type=int, default=60000, help="long prompt size in tokens (approx.)"
    )
    ap.add_argument("--long-out", type=int, default=256)
    ap.add_argument("--short", type=int, default=2000)
    ap.add_argument("--short-out", type=int, default=64)
    ap.add_argument("--interval", type=float, default=5.0)
    ap.add_argument("--short-parallel", type=int, default=4)
    ap.add_argument("--timeout", type=float, default=1800)
    ap.add_argument("--label", default="")
    a = ap.parse_args()

    shorts, errors, samples = [], [], []
    done = threading.Event()
    lock = threading.Lock()
    sem = threading.Semaphore(a.short_parallel)

    def one_short(i):
        try:
            r = stream(
                a.base_url,
                a.model,
                text_of(a.short, f"s{i}-{time.time_ns()}"),
                a.short_out,
                a.timeout,
            )
            with lock:
                shorts.append(r)
        except Exception as e:  # recorded, not fatal
            with lock:
                errors.append(repr(e)[:200])
        finally:
            sem.release()

    def sampler():
        while not done.is_set():
            g = gauges(a.base_url)
            if g:
                samples.append(g)
            time.sleep(2)

    long_result = {}

    def run_long():
        try:
            long_result.update(
                stream(
                    a.base_url,
                    a.model,
                    text_of(a.long, f"L-{time.time_ns()}"),
                    a.long_out,
                    a.timeout,
                )
            )
        except Exception as e:
            long_result["error"] = repr(e)[:200]
        finally:
            done.set()

    threading.Thread(target=sampler, daemon=True).start()
    lt = threading.Thread(target=run_long)
    lt.start()
    time.sleep(1.0)
    workers, i = [], 0
    while not done.is_set():
        if sem.acquire(timeout=0.5):
            if done.is_set():
                sem.release()
                break
            t = threading.Thread(target=one_short, args=(i,))
            t.start()
            workers.append(t)
            i += 1
            done.wait(a.interval)
    lt.join()
    for t in workers:
        t.join()

    print(json.dumps(summarize(a.label, a.model, long_result, shorts, errors, samples)))


if __name__ == "__main__":
    main()
