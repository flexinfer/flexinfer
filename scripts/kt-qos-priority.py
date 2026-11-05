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

KT-QOS2 (matrix row workhorse-qos-prefill-budget-2026-11-05) adds, all off by
default so a KT-QOS run is unchanged:

  --idle-warmup N         discard N interactive requests before the idle baseline;
  --warm-sweep S          run the full load pattern S seconds before the measured
                          window opens (engine counters, gaps and turns count
                          from then);
  --long-probe-fillers    after the load window, time long interactive prompts
                          (filler lines per size, e.g. 110,440,1540) idle and
                          while one background request decodes, one discarded
                          warm rep per size and condition;
  --verdict-qos2 CONTROL.json ARM.json ...   the KT-QOS2 pass rule;
  --collisions LOKI.json ...   classify proxy request_usage lines: which
                          interactive turns were forwarded while a background
                          request was before its first byte, and the reverse.
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

# KT-QOS2 pass rule (row workhorse-qos-prefill-budget-2026-11-05). Every bar
# but the first is relative to the same window's reserve-only control arm.
QOS2_TTFT_RATIO = 1.3  # interactive TTFT p95 under load <= 1.3x the arm's idle p95
QOS2_IDLE_VS_CONTROL = 1.10  # idle interactive TTFT p95 <= 1.10x the control's
QOS2_MAX_GAP_S = 60.0  # no background worker goes 60 s without a token
QOS2_DECODE_VS_CONTROL = 0.95  # aggregate decode >= 0.95x the control's
QOS2_POS0 = 0.75  # MTP position-0 acceptance under load
QOS2_POS0_DELTA = 0.02  # ... and within 0.02 of the control's
QOS2_LONG_PROMPT_RATIO = 1.25  # every long-prompt probe median TTFT <= 1.25x control

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
LONG_PROMPT = (
    "Using the reference notes above, say in two sentences what the archive records."
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


class StreamAborted(Exception):
    """Raised from an on_token callback to close a stream early.

    Leaving the urlopen context closes the connection, so the proxy and the
    engine abort the request.
    """


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
        started_epoch = time.time()
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
            "started_epoch": round(started_epoch, 3),
            "ttft_s": round(first - started, 4),
            "total_s": round(last - started, 4),
            "prompt_tokens": usage.get("prompt_tokens"),
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
    for i in range(args.idle_warmup + args.baseline_reps):
        r = client.stream(
            nonce_prompt(INTERACTIVE_PROMPT, args.interactive_filler),
            args.interactive_tokens,
            "interactive",
        )
        if i >= args.idle_warmup:  # warm reps absorb first-use compiles
            idle.append(r)
        time.sleep(2)
    result["idle_ttft_s"] = [r["ttft_s"] for r in idle]
    result["interactive_prompt_tokens"] = idle[0]["prompt_tokens"] if idle else None

    stop = threading.Event()
    worker_events = [[], []]
    worker_errors = [0, 0]
    worker_tokens = [0, 0]
    background_requests = []
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
                    background_requests.append(
                        {
                            "worker": i,
                            "started_epoch": r["started_epoch"],
                            "ttft_s": r["ttft_s"],
                            "prompt_tokens": r["prompt_tokens"],
                        }
                    )
            except (OSError, ValueError) as err:
                worker_errors[i] += 1
                print(json.dumps({"background": i, "error": str(err)}), file=sys.stderr)
                time.sleep(5)

    interactive, warm_turns, interactive_errors = [], [], 0
    threads = []

    def turn(sink):
        nonlocal interactive_errors
        try:
            r = client.stream(
                nonce_prompt(INTERACTIVE_PROMPT, args.interactive_filler),
                args.interactive_tokens,
                "interactive",
            )
            with lock:
                sink.append(r)
        except (OSError, ValueError) as err:
            with lock:
                if sink is interactive:
                    interactive_errors += 1
            print(json.dumps({"interactive_error": str(err)}), file=sys.stderr)

    def drive(until, sink):
        """Start one interactive turn every --interval seconds until `until`."""
        while time.monotonic() < until:
            t = threading.Thread(target=turn, args=(sink,), daemon=True)
            t.start()
            threads.append(t)
            time.sleep(args.interval)

    before = None
    if args.warm_sweep <= 0:
        before = scrape(metrics_url)
        window_start = time.monotonic()
    workers = [
        threading.Thread(target=background, args=(i,), daemon=True) for i in range(2)
    ]
    for w in workers:
        w.start()
    time.sleep(args.warmup)  # let both seats fill before the first interactive turn
    gap_start = None if args.warm_sweep > 0 else window_start + args.warmup
    if args.warm_sweep > 0:
        # The full load pattern runs, unmeasured, so the measured window does
        # not pay the first-use compiles of every new batch shape.
        drive(time.monotonic() + args.warm_sweep, warm_turns)
        before = scrape(metrics_url)
        window_start = gap_start = time.monotonic()

    drive(time.monotonic() + args.duration, interactive)
    for t in threads:
        t.join(timeout=900)
    window_end = time.monotonic()
    window_end_epoch = time.time()
    after = scrape(metrics_url)
    stop.set()
    for w in workers:
        w.join(timeout=900)

    window = window_end - window_start
    window_start_epoch = window_end_epoch - window
    ttfts = [r["ttft_s"] for r in interactive]
    delta = metrics_delta(before, after)
    result.update(
        {
            "window_s": round(window, 1),
            "window_start_epoch": round(window_start_epoch, 3),
            "warm_sweep_s": args.warm_sweep,
            "warm_turns_n": len(warm_turns),
            "interactive_n": len(ttfts),
            "interactive_errors": interactive_errors,
            "interactive_ttft_s": ttfts,
            "interactive_ttft_p50_s": percentile(ttfts, 50),
            "interactive_ttft_p95_s": percentile(ttfts, 95),
            "idle_ttft_p95_s": percentile(result["idle_ttft_s"], 95),
            "interactive_turns": sorted(
                (
                    {"started_epoch": r["started_epoch"], "ttft_s": r["ttft_s"]}
                    for r in interactive
                ),
                key=lambda r: r["started_epoch"],
            ),
            "background_requests": sorted(
                background_requests, key=lambda r: r["started_epoch"]
            ),
            "background_max_gap_s": [
                round(max_gap(sorted(ev), gap_start, window_end), 2)
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
    if args.long_probe_fillers:
        result["long_probes"] = long_probes(client, args)
    return result


def parse_fillers(text):
    """'110,440,1540' -> [110, 440, 1540]; empty -> []."""
    return [int(v) for v in str(text or "").split(",") if v.strip()]


def probe_once(client, args, filler, loaded):
    """Time one long interactive prompt, idle or while a background request decodes.

    Loaded: a background request (background class, ~2K-token prompt) starts
    first; the probe is sent one second after its first token, so its prefill
    is done and it is decoding. It is closed once the probe returns.
    """
    prompt = nonce_prompt(LONG_PROMPT, filler)
    if not loaded:
        return client.stream(prompt, args.long_probe_tokens, "interactive")
    decoding, done, box = threading.Event(), threading.Event(), {}

    def on_token(_t):
        decoding.set()
        if done.is_set():
            raise StreamAborted()

    def background():
        try:
            client.stream(
                nonce_prompt(BACKGROUND_PROMPT, args.background_filler),
                args.long_probe_background_tokens,
                "background",
                ignore_eos=True,
                on_token=on_token,
            )
            box["finished_early"] = True
        except StreamAborted:
            pass
        except (OSError, ValueError) as err:
            box["error"] = str(err)
        finally:
            decoding.set()

    worker = threading.Thread(target=background, daemon=True)
    worker.start()
    decoding.wait(timeout=600)
    time.sleep(1.0)
    try:
        r = client.stream(prompt, args.long_probe_tokens, "interactive")
    finally:
        done.set()
        worker.join(timeout=900)
    r["background_overlapped"] = not box
    if box.get("error"):
        r["background_error"] = box["error"]
    return r


def probe_size(client, args, filler, loaded):
    """One discarded warm rep, then --long-probe-reps timed reps."""
    reps, errors = [], []
    for i in range(1 + args.long_probe_reps):
        try:
            r = probe_once(client, args, filler, loaded)
        except (OSError, ValueError) as err:
            errors.append(str(err))
            print(
                json.dumps({"long_probe": filler, "loaded": loaded, "error": str(err)}),
                file=sys.stderr,
            )
            continue
        if i > 0:  # rep 0 absorbs the first-use compile of this shape
            reps.append(r)
    ttfts = [r["ttft_s"] for r in reps]
    median = round(statistics.median(ttfts), 4) if ttfts else None
    tokens = reps[0]["prompt_tokens"] if reps else None
    entry = {
        "filler": filler,
        "prompt_tokens": tokens,
        "ttft_s": ttfts,
        "ttft_median_s": median,
        "prefill_tps": round(tokens / median, 1) if tokens and median else None,
        "errors": errors,
    }
    if loaded:
        entry["overlapped"] = [r.get("background_overlapped") for r in reps]
    return entry


def long_probes(client, args):
    sizes = parse_fillers(args.long_probe_fillers)
    return {
        "idle": [probe_size(client, args, f, loaded=False) for f in sizes],
        "loaded": [probe_size(client, args, f, loaded=True) for f in sizes],
    }


def load_json(path):
    with open(path) as f:
        return json.load(f)


def verdict(paths):
    """Judge each arm against the §6.3 pass rule; the first file is the fcfs arm."""
    arms = [load_json(p) for p in paths]
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


def long_prompt_ratios(arm, control):
    """Median TTFT of each long-prompt probe over the control's matching probe.

    Returns {"idle/110": ratio, ...}. A probe the control or the arm lacks, has
    no median for, or (loaded) ran without a decoding background request maps
    to None, which fails the cost cap.
    """
    ratios = {}
    for cond in ("idle", "loaded"):
        ref = {p["filler"]: p for p in (control.get("long_probes") or {}).get(cond, [])}
        for probe in (arm.get("long_probes") or {}).get(cond, []):
            key = f"{cond}/{probe['filler']}"
            base = ref.get(probe["filler"])
            valid = (
                base is not None
                and probe.get("ttft_median_s")
                and base.get("ttft_median_s")
                and (
                    cond == "idle"
                    or (
                        all(probe.get("overlapped") or [False])
                        and all(base.get("overlapped") or [False])
                    )
                )
            )
            ratios[key] = (
                round(probe["ttft_median_s"] / base["ttft_median_s"], 3)
                if valid
                else None
            )
        for filler in ref:
            ratios.setdefault(f"{cond}/{filler}", None)
    return ratios


def verdict_qos2(paths):
    """Judge KT-QOS2 arms against the first file, the reserve-only control."""
    arms = [load_json(p) for p in paths]
    control = arms[0]
    rows = []
    for arm in arms:
        checks = {}
        ratio = arm["interactive_ttft_p95_s"] / arm["idle_ttft_p95_s"]
        checks["ttft_p95_vs_idle"] = (round(ratio, 3), ratio <= QOS2_TTFT_RATIO)
        idle = arm["idle_ttft_p95_s"] / control["idle_ttft_p95_s"]
        checks["idle_ttft_vs_control"] = (round(idle, 3), idle <= QOS2_IDLE_VS_CONTROL)
        gap = max(arm["background_max_gap_s"])
        checks["background_max_gap_s"] = (gap, gap <= QOS2_MAX_GAP_S)
        decode = arm["aggregate_decode_tps"] / control["aggregate_decode_tps"]
        checks["decode_vs_control"] = (
            round(decode, 4),
            decode >= QOS2_DECODE_VS_CONTROL,
        )
        checks["greedy_parity"] = (arm["parity"] == control["parity"],) * 2
        pos0 = (arm.get("engine") or {}).get("pos0_acceptance")
        ref0 = (control.get("engine") or {}).get("pos0_acceptance")
        checks["mtp_pos0"] = (
            pos0,
            pos0 is not None
            and ref0 is not None
            and pos0 >= QOS2_POS0
            and abs(pos0 - ref0) <= QOS2_POS0_DELTA,
        )
        ratios = long_prompt_ratios(arm, control)
        worst = max((v for v in ratios.values() if v is not None), default=None)
        checks["long_prompt_ttft_vs_control"] = (
            {"worst": worst, "probes": ratios},
            bool(ratios)
            and all(
                v is not None and v <= QOS2_LONG_PROMPT_RATIO for v in ratios.values()
            ),
        )
        rows.append(
            {
                "arm": arm.get("label") or arm["arm"],
                "interactive_ttft_p50_s": arm["interactive_ttft_p50_s"],
                "interactive_ttft_p95_s": arm["interactive_ttft_p95_s"],
                "interactive_ttft_max_s": max(arm["interactive_ttft_s"], default=None),
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


def parse_usage_line(ts_ns, line):
    """One proxy request_usage log line -> (class, arrival, forward, first_byte, ttft_s).

    Accepts the raw JSON line or the LogQL line_format
    "{{.qos_class}} {{.duration_ms}} {{.request_id}}". The line is written at
    the first body byte (lane breaker on), duration_ms is measured from the
    forward, and the request id's numeric prefix is the proxy arrival in ns.
    None for a line without a class or id.
    """
    line = line.strip()
    if line.startswith("{"):
        try:
            rec = json.loads(line)
        except ValueError:
            return None
        cls, dur, rid = (
            rec.get("qos_class"),
            rec.get("duration_ms"),
            rec.get("request_id"),
        )
    else:
        parts = line.split()
        if len(parts) < 3:
            return None
        cls, dur, rid = parts[0], parts[1], parts[2]
    try:
        first_byte = int(ts_ns) / 1e9
        arrival = int(str(rid).split("-")[0]) / 1e9
        ttft = int(dur) / 1000.0
    except (TypeError, ValueError):
        return None
    if cls not in PRIORITY:
        return None
    return cls, arrival, first_byte - ttft, first_byte, ttft


def collisions(rows, slow_s=0.9):
    """Classify interactive turns against background prefills.

    rows are parse_usage_line tuples. A request is "in prefill" from its
    forward to its first byte. Reports how many background requests were
    forwarded while an interactive was in prefill (what an admission gate
    could hold) and, for every interactive forwarded while a background was
    in prefill, the lag behind that forward and its TTFT.
    """
    bg = sorted((r for r in rows if r[0] != "interactive"), key=lambda r: r[2])
    it = sorted((r for r in rows if r[0] == "interactive"), key=lambda r: r[2])
    held = [
        {"lag_s": round(b[2] - i[2], 3), "interactive_ttft_s": i[4]}
        for b in bg
        for i in it
        if i[2] < b[2] < i[3]
    ]
    overlapped = []
    for i in it:
        running = [b for b in bg if b[2] <= i[2] < b[3]]
        if running:
            b = running[-1]
            overlapped.append(
                {
                    "lag_s": round(i[2] - b[2], 3),
                    "ttft_s": i[4],
                    "background_ttft_s": b[4],
                }
            )
    slow = [i for i in it if i[4] > slow_s]
    return {
        "background_n": len(bg),
        "interactive_n": len(it),
        "background_forwarded_during_interactive_prefill": len(held),
        "held_examples": held[:20],
        "interactive_forwarded_during_background_prefill": len(overlapped),
        "overlaps": overlapped,
        "slow_threshold_s": slow_s,
        "slow_interactive_n": len(slow),
        "slow_interactive_overlapping_background_prefill": sum(
            1 for i in slow if any(b[2] <= i[2] < b[3] for b in bg)
        ),
    }


def load_usage_rows(paths):
    """Parse Loki query_range JSON exports (the MCP tool's output) of request_usage lines."""
    rows = []
    for path in paths:
        data = load_json(path)
        streams = (data.get("result") or {}).get("result") or data.get("data", {}).get(
            "result", []
        )
        for stream in streams:
            for ts, line in stream.get("values", []):
                row = parse_usage_line(ts, line)
                if row:
                    rows.append(row)
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
    parser.add_argument(
        "--idle-warmup",
        type=int,
        default=0,
        help="interactive requests discarded before the idle baseline",
    )
    parser.add_argument(
        "--warm-sweep",
        type=float,
        default=0,
        help="seconds of unmeasured load before the measured window",
    )
    parser.add_argument(
        "--long-probe-fillers",
        default="",
        help="filler lines per long-prompt probe size, e.g. 110,440,1540",
    )
    parser.add_argument("--long-probe-reps", type=int, default=3)
    parser.add_argument("--long-probe-tokens", type=int, default=16)
    parser.add_argument(
        "--long-probe-background-tokens",
        type=int,
        default=4096,
        help="generation length of the background request a loaded probe runs beside",
    )
    parser.add_argument(
        "--label", help="arm label stored in the output (e.g. control, budget)"
    )
    parser.add_argument("--out")
    parser.add_argument("--verdict", nargs="+", metavar="ARM_JSON")
    parser.add_argument(
        "--verdict-qos2",
        nargs="+",
        metavar="ARM_JSON",
        help="KT-QOS2 pass rule; the first file is the reserve-only control",
    )
    parser.add_argument(
        "--collisions",
        nargs="+",
        metavar="LOKI_JSON",
        help="Loki exports of the proxy's request_usage lines for one arm",
    )
    args = parser.parse_args(argv)

    if args.verdict:
        rows = verdict(args.verdict)
        print(json.dumps(rows, indent=2))
        return 0 if all(r["pass"] for r in rows[1:]) else 1
    if args.verdict_qos2:
        rows = verdict_qos2(args.verdict_qos2)
        print(json.dumps(rows, indent=2))
        return 0 if all(r["pass"] for r in rows[1:]) else 1
    if args.collisions:
        print(json.dumps(collisions(load_usage_rows(args.collisions)), indent=2))
        return 0
    if not (args.endpoint and args.model and args.arm):
        parser.error("--endpoint, --model and --arm are required for a run")
    result = run(args)
    if args.label:
        result["label"] = args.label
    text = json.dumps({k: v for k, v in result.items()}, indent=2)
    if args.out:
        with open(args.out, "w") as f:
            f.write(text + "\n")
    print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
