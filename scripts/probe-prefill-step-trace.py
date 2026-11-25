#!/usr/bin/env python3
"""Step trace of a long prefill with short requests beside it (stdlib, in-pod).

One long request prefills while short requests run one at a time beside it,
and /metrics is sampled about 20 times a second on the same clock. vLLM
publishes its KV-usage gauge once per engine step, after allocating the blocks
of the step it has scheduled next, so the gauge's changes give each step's end
time and how many KV blocks the next one took. From that the summary splits a
short request's time to first token into:

* ``wait``: from the request being sent to the step that first carries it
  starting to run (the gauge first shows two running requests);
* ``own``: its own prefill steps, up to its first token.

First run 2026-11-25 on the 5930k workhorse (64K, cap 416): wait 3.8-5.0 s
behind one uncapped step of the lone long prompt, own 3.9-5.1 s over five
416-token steps. ``flexinfer_vllm_common.prefill_sched`` is the patch for both.

Usage, from a workstation::

    kubectl -n flexinfer-system exec -i <pod> -- python3 - \\
        --model <served name> --long 56000 < scripts/probe-prefill-step-trace.py

``--gap 3`` sends each short three seconds after the previous one finished,
so it arrives while the long prompt is alone, mid-step; the default sends them
back to back. Prints one JSON object: ``summary`` first, raw events after.
The probe loads the lane for the length of the long prefill. A request from
another caller shows up as ``outside`` in the summary; treat that run as
disturbed.
"""

import argparse
import json
import random
import re
import statistics
import threading
import time
import urllib.request

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
    usage = None
    chunks = []
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
                    chunks.append(round(time.time(), 4))
    return {
        "start": round(t0, 4),
        "end": round(time.time(), 4),
        "chunks": chunks,
        "prompt_tokens": (usage or {}).get("prompt_tokens"),
        "completion_tokens": (usage or {}).get("completion_tokens"),
    }


LINE = re.compile(r"^(vllm:[a-z_]+)(\{[^}]*\})?\s+(\S+)$")
WANT = {
    "vllm:kv_cache_usage_perc": "kv",
    "vllm:num_requests_running": "run",
    "vllm:num_requests_waiting": "wait",
    "vllm:request_success_total": "done",
}


def parse_metrics(text):
    """The gauges the trace needs from one /metrics scrape, by exact name."""
    out = {}
    for line in text.splitlines():
        m = LINE.match(line.strip())
        if m and m.group(1) in WANT:
            key = WANT[m.group(1)]
            out[key] = out.get(key, 0.0) + float(m.group(3))
    return out


def scrape(base):
    with urllib.request.urlopen(base + "/metrics", timeout=5) as r:
        return parse_metrics(r.read().decode())


def block_quantum(samples):
    """The KV-usage change one block makes: the smallest step the gauge took."""
    values = sorted({round(s["kv"], 7) for s in samples if "kv" in s})
    steps = [b - a for a, b in zip(values, values[1:]) if b - a > 1e-6]
    return min(steps) if steps else None


def step_events(samples, quantum):
    """(time, blocks added, running) at every sample where the engine moved."""
    events = []
    prev = None
    for s in samples:
        if "kv" not in s:
            continue
        if prev is not None:
            delta = round((s["kv"] - prev["kv"]) / quantum) if quantum else 0
            if delta or s.get("run") != prev.get("run"):
                events.append((s["t"], delta, int(s.get("run", 0))))
        prev = s
    return events


def _median(values):
    return round(statistics.median(values), 3) if values else None


def summarize(result):
    """Split each short's time to first token into wait and own prefill steps."""
    samples = result.get("samples") or []
    long_result = result.get("long") or {}
    shorts = result.get("shorts") or []
    t0 = result.get("t_long_sent") or (samples[0]["t"] if samples else 0.0)
    quantum = block_quantum(samples)
    events = step_events(samples, quantum)
    long_first = long_result["chunks"][0] if long_result.get("chunks") else None
    rows = []
    for s in shorts:
        if not s.get("chunks") or long_first is None or s["start"] >= long_first:
            continue
        first = s["chunks"][0]
        carried = [e[0] for e in events if e[0] > s["start"] + 0.05 and e[2] >= 2]
        if not carried or carried[0] > first:
            continue
        own = [e[0] for e in events if carried[0] < e[0] <= first + 0.1]
        rows.append(
            {
                "sent": round(s["start"] - t0, 2),
                "ttft": round(first - s["start"], 3),
                "wait": round(carried[0] - s["start"], 3),
                "own": round(first - carried[0], 3),
                "own_steps": len(own),
                "prompt_tokens": s.get("prompt_tokens"),
            }
        )
    # A step the long prompt ran alone: the gauge showed one running request
    # at its start and did not move again for longer than any capped step.
    alone = [
        round(b[0] - a[0], 3)
        for a, b in zip(events, events[1:])
        if a[2] == 1 and (long_first is None or b[0] <= long_first)
    ]
    own_requests = len(shorts) + (1 if long_result.get("chunks") else 0)
    finished = samples[-1].get("done", 0) - samples[0].get("done", 0) if samples else 0
    in_prefill = [s for s in samples if long_first is None or s["t"] <= long_first]
    return {
        "long_ttft": (
            round(long_first - long_result["start"], 3) if long_first else None
        ),
        "long_prompt_tokens": long_result.get("prompt_tokens"),
        "shorts_in_prefill": len(rows),
        "short_ttft_p50": _median([r["ttft"] for r in rows]),
        "short_wait_p50": _median([r["wait"] for r in rows]),
        "short_own_p50": _median([r["own"] for r in rows]),
        "short_own_steps_p50": _median([r["own_steps"] for r in rows]),
        "alone_step_p50": _median(alone),
        "alone_step_max": max(alone) if alone else None,
        "kv_blocks_total": round(1 / quantum) if quantum else None,
        "outside": {
            "finished_requests": int(round(finished - own_requests)),
            "waiting_max_in_prefill": max(
                (s.get("wait", 0.0) for s in in_prefill), default=0.0
            ),
            "running_max": max((s.get("run", 0.0) for s in samples), default=0.0),
        },
        "shorts": rows,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base-url", default="http://127.0.0.1:8000")
    ap.add_argument("--model", required=True)
    ap.add_argument("--long", type=int, default=56000)
    ap.add_argument("--long-out", type=int, default=48)
    ap.add_argument("--short", type=int, default=2000)
    ap.add_argument("--short-out", type=int, default=64)
    ap.add_argument(
        "--lead", type=float, default=6.0, help="seconds the long runs alone first"
    )
    ap.add_argument(
        "--gap", type=float, default=0.0, help="seconds between one short and the next"
    )
    ap.add_argument("--period", type=float, default=0.05)
    ap.add_argument("--timeout", type=float, default=600)
    ap.add_argument("--label", default="")
    a = ap.parse_args()

    samples, shorts, errors = [], [], []
    long_result = {}
    done = threading.Event()

    def sampler():
        while not done.is_set():
            t = time.time()
            try:
                s = scrape(a.base_url)
                s["t"] = round(t, 4)
                samples.append(s)
            except Exception as e:  # recorded, not fatal
                errors.append("scrape " + repr(e)[:120])
            rest = a.period - (time.time() - t)
            if rest > 0:
                time.sleep(rest)

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

    th_s = threading.Thread(target=sampler, daemon=True)
    th_s.start()
    time.sleep(2.0)  # idle samples before any load
    t_long = time.time()
    th_l = threading.Thread(target=run_long, daemon=True)
    th_l.start()
    time.sleep(a.lead)
    i = 0
    # One short at a time until the long request ends.
    while th_l.is_alive():
        i += 1
        try:
            shorts.append(
                stream(
                    a.base_url,
                    a.model,
                    text_of(a.short, f"s{i}-{time.time_ns()}"),
                    a.short_out,
                    a.timeout,
                )
            )
        except Exception as e:
            errors.append("short " + repr(e)[:160])
            time.sleep(0.5)
        if a.gap > 0:
            time.sleep(a.gap)
    th_l.join(timeout=a.timeout)
    time.sleep(2.0)
    done.set()
    th_s.join(timeout=5)
    result = {
        "label": a.label,
        "model": a.model,
        "args": vars(a),
        "t_long_sent": round(t_long, 4),
        "long": long_result,
        "shorts": shorts,
        "samples": samples,
        "errors": errors[:20],
    }
    print(json.dumps({"summary": summarize(result), **result}))


if __name__ == "__main__":
    main()
