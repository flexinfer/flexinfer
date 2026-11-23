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
"""
import argparse
import json
import random
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
    with urllib.request.urlopen(req, timeout=timeout) as r:
        for raw in r:
            line = raw.decode().strip()
            if not line.startswith("data:") or line.endswith("[DONE]"):
                continue
            d = json.loads(line[5:])
            if d.get("usage"):
                usage = d["usage"]
            for c in d.get("choices", []):
                if (c.get("delta") or {}).get("content") and ttft is None:
                    ttft = time.time() - t0
    total = time.time() - t0
    out = (usage or {}).get("completion_tokens") or 0
    decode = (out - 1) / (total - ttft) if ttft and total > ttft and out > 1 else None
    return {
        "ttft": ttft,
        "total": total,
        "prompt_tokens": (usage or {}).get("prompt_tokens"),
        "completion_tokens": out,
        "decode_tps": decode,
    }


def gauges(base):
    try:
        with urllib.request.urlopen(base + "/metrics", timeout=5) as r:
            text = r.read().decode()
    except Exception:
        return None
    run = wait = None
    for line in text.splitlines():
        if line.startswith("vllm:num_requests_running"):
            run = float(line.rsplit(" ", 1)[1])
        elif line.startswith("vllm:num_requests_waiting"):
            wait = float(line.rsplit(" ", 1)[1])
    return run, wait


def pct(v, p):
    if not v:
        return None
    v = sorted(v)
    return round(v[min(len(v) - 1, int(round(p * (len(v) - 1))))], 3)


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

    ttfts = [s["ttft"] for s in shorts if s.get("ttft") is not None]
    runs = [s[0] for s in samples if s[0] is not None]
    waits = [s[1] for s in samples if s[1] is not None]
    print(
        json.dumps(
            {
                "label": a.label,
                "model": a.model,
                "long": {
                    k: (round(v, 3) if isinstance(v, float) else v)
                    for k, v in long_result.items()
                },
                "short": {
                    "n": len(shorts),
                    "errors": len(errors),
                    "ttft_p50": pct(ttfts, 0.5),
                    "ttft_p95": pct(ttfts, 0.95),
                    "ttft_max": pct(ttfts, 1.0),
                    "decode_median": (
                        round(
                            statistics.median(
                                [s["decode_tps"] for s in shorts if s.get("decode_tps")]
                            ),
                            2,
                        )
                        if any(s.get("decode_tps") for s in shorts)
                        else None
                    ),
                },
                "engine": {
                    "samples": len(samples),
                    "running_max": max(runs) if runs else None,
                    "waiting_max": max(waits) if waits else None,
                    "waiting_nonzero_frac": (
                        round(sum(1 for w in waits if w > 0) / len(waits), 3)
                        if waits
                        else None
                    ),
                },
                "errors": errors[:5],
            }
        )
    )


if __name__ == "__main__":
    main()
