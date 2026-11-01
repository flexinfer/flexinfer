#!/usr/bin/env python3
"""KT-QOS: does vLLM priority scheduling protect interactive turns on a two-seat lane?

Kill-test for D3 (.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md
§6.3). One run measures one arm on one lane:

  1. greedy parity: three fixed 256-token generations at temperature 0;
  2. idle baseline: --baseline-reps interactive requests alone, spaced apart;
  3. load: two background workers stream long generations back to back
     (saturating both seats) while one interactive request starts every
     --interval seconds for --duration seconds;
  4. engine counters over the load window: spec-decode acceptance (MTP pos-0),
     generation tokens, preemptions.

Arms (the class reaches the engine differently in each):

  --arm fcfs      direct to the lane's Service, no priority field (baseline);
  --arm priority  direct to the Service, body "priority": 0 / 10 (the lane runs
                  schedulingPolicy: priority);
  --arm proxy     through flexinfer-proxy with X-FlexInfer-QoS headers; the
                  proxy applies its seat gate and, on a priority lane, the
                  priority field (D3 MR 2 and MR 3).

    python3 scripts/kt-qos-priority.py --endpoint http://<lane-svc>:8000 \
        --model <served-model-name> --arm fcfs --out fcfs.json
    python3 scripts/kt-qos-priority.py --verdict fcfs.json priority.json [proxy.json]

The engine /metrics come from --endpoint in the direct arms; pass --metrics
http://<lane-svc>:8000/metrics in the proxy arm. Stdlib only, so the file can be
piped into the serving pod (kubectl exec -i <pod> -- python3 - ... < this file).
"""

import argparse
import hashlib
import json
import math
import re
import statistics
import sys
import threading
import time
import urllib.request
import uuid

PASS_TTFT_RATIO = 1.3  # interactive TTFT p95 <= 1.3x the idle-lane p95
PASS_MAX_GAP_S = 60.0  # no background worker goes 60 s without a token
PASS_DECODE_DELTA = 0.05  # aggregate decode within 5 % of the fcfs arm
PASS_POS0 = 0.75  # MTP position-0 acceptance under load

PRIORITY = {"interactive": 0, "background": 10, "batch": 20}

PARITY_PROMPTS = [
    (
        "parity_code",
        "Write a Python function `merge_intervals(intervals)` that merges "
        "overlapping closed intervals and returns them sorted. Include a "
        "docstring and two usage examples with their printed output.",
    ),
    (
        "parity_explain",
        "Explain, in about 250 words, why TCP uses a three-way handshake "
        "and what would go wrong with a two-way handshake.",
    ),
    (
        "parity_list",
        "List eight distinct causes of high p99 latency in a GPU inference "
        "server, one sentence each, numbered.",
    ),
]
INTERACTIVE_PROMPT = (
    "You are helping a person in a live chat. In three sentences, explain what "
    "a hash map is and when you would use one."
)
BACKGROUND_PROMPT = (
    "Write a long, detailed technical report on the history of distributed "
    "consensus algorithms, covering Paxos, Viewstamped Replication, Raft and "
    "their production use, with examples."
)
FILLER = (
    "Reference note {i}: the archive lists shipments, dates and carriers in order.\n"
)

METRIC_LINE = re.compile(r"^([A-Za-z_:][A-Za-z0-9_:]*)(\{[^}]*\})?\s+(\S+)")
LABEL = re.compile(r'(\w+)="([^"]*)"')
COUNTERS = {
    "drafts": "vllm:spec_decode_num_drafts",
    "draft_tokens": "vllm:spec_decode_num_draft_tokens",
    "accepted": "vllm:spec_decode_num_accepted_tokens",
    "generation_tokens": "vllm:generation_tokens",
    "preemptions": "vllm:num_preemptions",
}
PER_POS = "vllm:spec_decode_num_accepted_tokens_per_pos"


def percentile(values, q):
    """Nearest-rank percentile (q in [0, 100]); None for an empty list."""
    if not values:
        return None
    ordered = sorted(values)
    rank = max(1, math.ceil(q / 100.0 * len(ordered)))
    return ordered[rank - 1]


def max_gap(events, start, end):
    """Longest interval without a token on one worker.

    events is the sorted list of token-arrival times for the worker; the window
    edges count, so a worker that never streamed has a gap of end - start.
    """
    points = [start] + [t for t in events if start <= t <= end] + [end]
    return max(b - a for a, b in zip(points, points[1:]))


def parse_metrics(text):
    """Sum the counters of interest over all label sets."""
    totals = {key: 0.0 for key in COUNTERS}
    per_pos = {}
    for line in text.splitlines():
        if not line or line.startswith("#"):
            continue
        match = METRIC_LINE.match(line)
        if not match:
            continue
        name, labels, value = (
            match.group(1),
            match.group(2) or "",
            float(match.group(3)),
        )
        if name.endswith("_total"):
            name = name[: -len("_total")]
        if name == PER_POS:
            pos = int(dict(LABEL.findall(labels)).get("position", -1))
            per_pos[pos] = per_pos.get(pos, 0.0) + value
            continue
        for key, metric in COUNTERS.items():
            if name == metric:
                totals[key] += value
    totals["per_pos"] = per_pos
    return totals


def metrics_delta(before, after):
    if before is None or after is None:
        return None
    delta = {k: after[k] - before[k] for k in COUNTERS}
    drafts = delta["drafts"]
    pos0 = after["per_pos"].get(0, 0.0) - before["per_pos"].get(0, 0.0)
    delta["pos0_acceptance"] = round(pos0 / drafts, 4) if drafts > 0 else None
    delta["acceptance_rate"] = (
        round(delta["accepted"] / delta["draft_tokens"], 4)
        if delta["draft_tokens"] > 0
        else None
    )
    return delta


def scrape(url):
    if not url:
        return None
    try:
        with urllib.request.urlopen(url, timeout=10) as response:
            return parse_metrics(response.read().decode())
    except OSError as err:
        print(json.dumps({"warning": f"metrics scrape failed: {err}"}), file=sys.stderr)
        return None


class Client:
    def __init__(self, args):
        self.base = args.endpoint.rstrip("/")
        self.model = args.model
        self.arm = args.arm

    def payload(self, prompt, max_tokens, qos, temperature=0.7, ignore_eos=False):
        body = {
            "model": self.model,
            "messages": [{"role": "user", "content": prompt}],
            "max_tokens": max_tokens,
            "temperature": temperature,
            "stream": True,
            "stream_options": {"include_usage": True},
        }
        if ignore_eos:
            body["ignore_eos"] = True
        if self.arm == "priority":
            body["priority"] = PRIORITY[qos]
        return body

    def stream(self, prompt, max_tokens, qos, on_token=None, **kw):
        """Run one streaming chat request; returns timing and usage."""
        headers = {"Content-Type": "application/json"}
        if self.arm == "proxy":
            headers["X-FlexInfer-QoS"] = qos
        req = urllib.request.Request(
            self.base + "/v1/chat/completions",
            data=json.dumps(self.payload(prompt, max_tokens, qos, **kw)).encode(),
            headers=headers,
        )
        started = time.monotonic()
        first = last = None
        parts, usage, finish = [], None, None
        with urllib.request.urlopen(req, timeout=900) as response:
            for raw in response:
                line = raw.decode("utf-8").strip()
                if not line.startswith("data:"):
                    continue
                data = line[5:].strip()
                if data == "[DONE]":
                    break
                event = json.loads(data)
                if event.get("usage"):
                    usage = event["usage"]
                for choice in event.get("choices", []):
                    text = (choice.get("delta") or {}).get("content") or ""
                    if text:
                        last = time.monotonic()
                        first = first or last
                        parts.append(text)
                        if on_token:
                            on_token(last)
                    finish = choice.get("finish_reason") or finish
        if first is None or not usage:
            raise ValueError("empty stream or no usage chunk")
        answer = "".join(parts).strip()
        tokens = usage.get("completion_tokens", 0)
        return {
            "ttft_s": round(first - started, 4),
            "total_s": round(last - started, 4),
            "completion_tokens": tokens,
            "decode_tps": (
                round((tokens - 1) / (last - first), 3) if last > first else None
            ),
            "finish_reason": finish,
            "sha256": hashlib.sha256(answer.encode()).hexdigest()[:16],
            "answer": answer,
        }


def nonce_prompt(base, filler_lines=0):
    """Prefix a nonce so the prefix cache cannot serve the prompt."""
    filler = "".join(FILLER.format(i=i) for i in range(filler_lines))
    return f"[run {uuid.uuid4().hex}]\n{filler}{base}"


def run(args):
    client = Client(args)
    metrics_url = args.metrics or (
        client.base + "/metrics" if args.arm != "proxy" else None
    )
    result = {
        "arm": args.arm,
        "model": args.model,
        "endpoint": client.base,
        "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    }

    result["parity"] = {}
    for name, prompt in PARITY_PROMPTS:
        r = client.stream(prompt, 256, "interactive", temperature=0)
        result["parity"][name] = r["sha256"]

    idle = []
    for _ in range(args.baseline_reps):
        idle.append(
            client.stream(
                nonce_prompt(INTERACTIVE_PROMPT, args.interactive_filler),
                args.interactive_tokens,
                "interactive",
            )
        )
        time.sleep(2)
    result["idle_ttft_s"] = [r["ttft_s"] for r in idle]

    stop = threading.Event()
    worker_events = [[], []]
    worker_errors = [0, 0]
    worker_tokens = [0, 0]
    lock = threading.Lock()

    def background(i):
        while not stop.is_set():
            try:
                r = client.stream(
                    nonce_prompt(BACKGROUND_PROMPT, args.background_filler),
                    args.background_tokens,
                    "background",
                    ignore_eos=True,
                    on_token=lambda t: worker_events[i].append(t),
                )
                with lock:
                    worker_tokens[i] += r["completion_tokens"]
            except (OSError, ValueError) as err:
                worker_errors[i] += 1
                print(json.dumps({"background": i, "error": str(err)}), file=sys.stderr)
                time.sleep(5)

    before = scrape(metrics_url)
    window_start = time.monotonic()
    workers = [
        threading.Thread(target=background, args=(i,), daemon=True) for i in range(2)
    ]
    for w in workers:
        w.start()
    time.sleep(args.warmup)  # let both seats fill before the first interactive turn

    interactive, interactive_errors = [], 0
    deadline = time.monotonic() + args.duration
    threads = []

    def turn():
        nonlocal interactive_errors
        try:
            r = client.stream(
                nonce_prompt(INTERACTIVE_PROMPT, args.interactive_filler),
                args.interactive_tokens,
                "interactive",
            )
            with lock:
                interactive.append(r)
        except (OSError, ValueError) as err:
            with lock:
                interactive_errors += 1
            print(json.dumps({"interactive_error": str(err)}), file=sys.stderr)

    while time.monotonic() < deadline:
        t = threading.Thread(target=turn, daemon=True)
        t.start()
        threads.append(t)
        time.sleep(args.interval)
    for t in threads:
        t.join(timeout=900)
    window_end = time.monotonic()
    after = scrape(metrics_url)
    stop.set()
    for w in workers:
        w.join(timeout=900)

    window = window_end - window_start
    ttfts = [r["ttft_s"] for r in interactive]
    delta = metrics_delta(before, after)
    result.update(
        {
            "window_s": round(window, 1),
            "interactive_n": len(ttfts),
            "interactive_errors": interactive_errors,
            "interactive_ttft_s": ttfts,
            "interactive_ttft_p50_s": percentile(ttfts, 50),
            "interactive_ttft_p95_s": percentile(ttfts, 95),
            "idle_ttft_p95_s": percentile(result["idle_ttft_s"], 95),
            "background_max_gap_s": [
                round(max_gap(sorted(ev), window_start + args.warmup, window_end), 2)
                for ev in worker_events
            ],
            "background_errors": worker_errors,
            "background_tokens": worker_tokens,
            "engine": delta,
        }
    )
    gen = (
        delta["generation_tokens"]
        if delta
        else sum(worker_tokens) + sum(r["completion_tokens"] for r in interactive)
    )
    result["aggregate_decode_tps"] = round(gen / window, 2)
    return result


def verdict(paths):
    """Judge each arm against the §6.3 pass rule; the first file is the fcfs arm."""
    arms = [json.load(open(p)) for p in paths]
    fcfs = arms[0]
    if fcfs["arm"] != "fcfs":
        raise SystemExit("the first --verdict file must be the fcfs arm")
    rows = []
    for arm in arms:
        checks = {}
        ratio = arm["interactive_ttft_p95_s"] / arm["idle_ttft_p95_s"]
        checks["ttft_p95_vs_idle"] = (round(ratio, 3), ratio <= PASS_TTFT_RATIO)
        gap = max(arm["background_max_gap_s"])
        checks["background_max_gap_s"] = (gap, gap <= PASS_MAX_GAP_S)
        decode = arm["aggregate_decode_tps"] / fcfs["aggregate_decode_tps"] - 1
        checks["decode_vs_fcfs"] = (round(decode, 4), abs(decode) <= PASS_DECODE_DELTA)
        checks["greedy_parity"] = (arm["parity"] == fcfs["parity"],) * 2
        pos0 = (arm.get("engine") or {}).get("pos0_acceptance")
        checks["mtp_pos0"] = (pos0, pos0 is not None and pos0 >= PASS_POS0)
        rows.append(
            {
                "arm": arm["arm"],
                "interactive_ttft_p50_s": arm["interactive_ttft_p50_s"],
                "interactive_ttft_p95_s": arm["interactive_ttft_p95_s"],
                "idle_ttft_p95_s": arm["idle_ttft_p95_s"],
                "aggregate_decode_tps": arm["aggregate_decode_tps"],
                "preemptions": (arm.get("engine") or {}).get("preemptions"),
                "checks": {
                    k: {"value": v, "pass": ok} for k, (v, ok) in checks.items()
                },
                "pass": all(ok for _, ok in checks.values()),
            }
        )
    return rows


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("--endpoint")
    parser.add_argument("--model")
    parser.add_argument("--arm", choices=["fcfs", "priority", "proxy"])
    parser.add_argument(
        "--metrics", help="engine /metrics URL (required for --arm proxy)"
    )
    parser.add_argument("--duration", type=float, default=1200)
    parser.add_argument("--interval", type=float, default=10)
    parser.add_argument("--warmup", type=float, default=20)
    parser.add_argument("--baseline-reps", type=int, default=10)
    parser.add_argument("--interactive-tokens", type=int, default=64)
    parser.add_argument(
        "--interactive-filler",
        type=int,
        default=20,
        help="filler lines in the interactive prompt (~18 tokens each)",
    )
    parser.add_argument("--background-tokens", type=int, default=1024)
    parser.add_argument("--background-filler", type=int, default=100)
    parser.add_argument("--out")
    parser.add_argument("--verdict", nargs="+", metavar="ARM_JSON")
    args = parser.parse_args(argv)

    if args.verdict:
        rows = verdict(args.verdict)
        print(json.dumps(rows, indent=2))
        return 0 if all(r["pass"] for r in rows[1:]) else 1
    if not (args.endpoint and args.model and args.arm):
        parser.error("--endpoint, --model and --arm are required for a run")
    result = run(args)
    text = json.dumps({k: v for k, v in result.items()}, indent=2)
    if args.out:
        with open(args.out, "w") as f:
            f.write(text + "\n")
    print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
