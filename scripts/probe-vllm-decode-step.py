#!/usr/bin/env python3
"""Decode-step anatomy and plugin-flag probes (gfx1100 slice S4 step 0).

Plan: .loom/10-research-backend-currency-gfx1100-gfx906-2026-11-04.md §5.3. Run
the probe modes inside a vLLM serving pod started with --profiler-config
(torch profiler, torch_profiler_dir = --profile-dir); `analyze` runs anywhere:

    kubectl -n flexinfer-system exec -i <pod> -- python3 - jit-sweep \
        --model qwen38-27b-s4-step0-canary < scripts/probe-vllm-decode-step.py
    ... - ttft --sizes 16384 --reps 5 ...
    ... - profile --context 2048 --concurrency 1,2 --steps 20 ...
    python3 scripts/probe-vllm-decode-step.py analyze trace.json.gz

Modes:

  jit-sweep  a fixed sequence of 100 requests (14 prompt sizes from 64 to 16K,
             every fourth slot a concurrent pair, 16 output tokens each, fresh
             nonce per prompt) that touches every attention shape a fresh
             process meets in normal traffic. The arms of the Q2 bar (zero
             `jit_monitor` lines in the first 100 requests after a cold boot)
             all run this same sequence first; the UTC window it prints
             brackets the log lines to count.
  ttft       cold-prefill time to first token at each size, --reps times, each
             behind a fresh nonce (no prefix-cache hit), from the server's own
             TTFT histogram as well as the client clock.
  profile    for each concurrency c: start c streaming requests at --context
             prompt tokens (greedy, ignore_eos), let them decode, then POST
             /start_profile with delay_iterations and max_iterations = --steps,
             so the trace holds exactly that many engine steps of pure decode.
             The newest worker trace is summarized as `analyze` does and its
             path printed for copying out.
  analyze    the step anatomy of one trace: steps are cut at the end of each
             step's last draft lm_head (the full-vocabulary fp16 GEMM, the only
             kernel above --lm-head-ms); per step, GPU busy time by kernel
             class (target lm_head, draft lm_heads, GPTQ GEMMs, attention, GDN,
             fused elementwise, sampling, copies, other) and GPU idle time
             inside and between steps.
"""

import argparse
import glob
import gzip
import json
import os
import re
import statistics
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid

METRIC_LINE = re.compile(r"^([A-Za-z_:][A-Za-z0-9_:]*)(\{[^}]*\})?\s+(\S+)")
FILLER = "Note {i}: sensor {i} reported nominal temperature and pressure today.\n"
KERNEL_CATS = ("kernel", "gpu_memcpy", "gpu_memset")
JIT_SIZES = (
    64,
    256,
    512,
    1024,
    2048,
    4096,
    8192,
    16384,
    128,
    384,
    768,
    1536,
    3072,
    6144,
)
JIT_REQUESTS = 100
TTFT = "vllm:time_to_first_token_seconds"

# Kernel classes, first match wins. The lm_head classes are assigned by
# position inside a step, not by name.
CLASSES = (
    ("gptq_gemm", re.compile(r"gemm_q4|q4_kernel|gptq|rdna3_wmma|w4a16", re.I)),
    ("attention", re.compile(r"segmented|attention|attn|paged", re.I)),
    (
        "gdn",
        re.compile(
            r"delta_rule|recurrent|gated_delta|gdn|conv1d|chunk_|fla_|l2norm", re.I
        ),
    ),
    ("skinny_gemm", re.compile(r"wvSplitK|LLMM1", re.I)),
    ("blas_gemm", re.compile(r"Cijk_|gemm|matmul|hipblaslt", re.I)),
    (
        "sampling",
        re.compile(
            r"argmax|rejection|sample|topk|top_k|softmax|gumbel|argsort|sort", re.I
        ),
    ),
    (
        "elementwise",
        re.compile(
            r"triton_(poi|red|per)|elementwise|vectorized|reduce|norm|rotary|rope|silu|act_and_mul|copy",
            re.I,
        ),
    ),
)


# ---------------------------------------------------------------- HTTP helpers


def http(base, path, payload=None, timeout=600):
    body = None if payload is None else json.dumps(payload).encode()
    return urllib.request.urlopen(
        urllib.request.Request(
            base + path, data=body, headers={"Content-Type": "application/json"}
        ),
        timeout=timeout,
    )


def scrape(base):
    with http(base, "/metrics", timeout=30) as response:
        totals = {}
        for line in response.read().decode("utf-8").splitlines():
            match = METRIC_LINE.match(line)
            if not match or line.startswith("#"):
                continue
            name, _, raw = match.groups()
            if name.endswith("_bucket") or name.endswith("_created"):
                continue
            try:
                totals[name] = totals.get(name, 0.0) + float(raw)
            except ValueError:
                continue
        return totals


def tokenize(base, model, text):
    payload = {"model": model, "prompt": text, "add_special_tokens": False}
    with http(base, "/tokenize", payload) as response:
        return json.load(response)["tokens"]


class Prompts:
    """Exact-length token prompts behind a fresh nonce."""

    def __init__(self, base, model, max_size):
        self.base, self.model = base, model
        pool, repeats = [], max(64, max_size // 12)
        while len(pool) < max_size:
            pool = tokenize(
                base, model, "".join(FILLER.format(i=i) for i in range(repeats))
            )
            repeats *= 2
        self.pool = pool

    def tokens(self, size):
        nonce = tokenize(self.base, self.model, f"Session {uuid.uuid4().hex[:12]}.\n")
        return nonce + self.pool[: size - len(nonce)]


def stream(base, model, token_ids, max_tokens, on_chunk=None, ignore_eos=True):
    """One streamed completion; returns client TTFT, total and chunk count."""
    payload = {
        "model": model,
        "prompt": token_ids,
        "max_tokens": max_tokens,
        "temperature": 0,
        "ignore_eos": ignore_eos,
        "stream": True,
        "stream_options": {"include_usage": True},
    }
    started = time.perf_counter()
    first, chunks, usage = None, 0, None
    with http(base, "/v1/completions", payload) as response:
        for raw in response:
            line = raw.decode("utf-8").strip()
            if not line.startswith("data:"):
                continue
            data = line[5:].strip()
            if data == "[DONE]":
                break
            event = json.loads(data)
            if event.get("error"):
                raise ValueError(f"stream error: {event['error']}")
            if event.get("choices"):
                chunks += 1
                if first is None:
                    first = time.perf_counter()
                if on_chunk is not None and on_chunk(chunks) is False:
                    break
            if event.get("usage"):
                usage = event["usage"]
    end = time.perf_counter()
    return {
        "client_ttft_s": round((first or end) - started, 4),
        "client_total_s": round(end - started, 4),
        "chunks": chunks,
        "usage": usage,
    }


def utc():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


# ---------------------------------------------------------------- modes


def jit_sweep_plan(requests=JIT_REQUESTS, sizes=JIT_SIZES):
    """The fixed request plan: a list of slots, each one or two prompt sizes."""
    plan, sent, i = [], 0, 0
    while sent < requests:
        slot_sizes = [sizes[i % len(sizes)]]
        i += 1
        if len(plan) % 4 == 3 and sent + 2 <= requests:
            slot_sizes.append(sizes[i % len(sizes)])
            i += 1
        plan.append(slot_sizes)
        sent += len(slot_sizes)
    return plan


def run_jit_sweep(args):
    prompts = Prompts(args.base_url, args.model, max(JIT_SIZES))
    plan = jit_sweep_plan()
    out = {
        "mode": "jit-sweep",
        "start_utc": utc(),
        "requests": 0,
        "errors": 0,
        "slots": [],
    }
    started = time.perf_counter()
    for slot in plan:
        ids = [prompts.tokens(size) for size in slot]
        results = [None] * len(ids)

        def worker(j, token_ids):
            try:
                results[j] = stream(args.base_url, args.model, token_ids, 16)
            except Exception as exc:  # noqa: BLE001 - counted, not fatal
                results[j] = {"error": str(exc)}

        threads = [
            threading.Thread(target=worker, args=(j, t)) for j, t in enumerate(ids)
        ]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        out["requests"] += len(slot)
        out["errors"] += sum(1 for r in results if "error" in r)
        out["slots"].append(
            {"sizes": slot, "ttft_s": [r.get("client_ttft_s") for r in results]}
        )
    out["end_utc"] = utc()
    out["elapsed_s"] = round(time.perf_counter() - started, 1)
    return out


def run_ttft(args):
    sizes = [int(s) for s in args.sizes.split(",") if s]
    prompts = Prompts(args.base_url, args.model, max(sizes))
    out = {"mode": "ttft", "start_utc": utc(), "sizes": {}}
    for size in sizes:
        rows = []
        for _ in range(args.reps):
            ids = prompts.tokens(size)
            before = scrape(args.base_url)
            client = stream(args.base_url, args.model, ids, 1, ignore_eos=False)
            after = scrape(args.base_url)
            count = after.get(TTFT + "_count", 0) - before.get(TTFT + "_count", 0)
            server = after.get(TTFT + "_sum", 0) - before.get(TTFT + "_sum", 0)
            rows.append(
                {
                    **client,
                    "server_ttft_s": round(server, 4) if round(count) == 1 else None,
                    "prompt_tokens": (client.get("usage") or {}).get("prompt_tokens"),
                }
            )
            time.sleep(args.settle)
        server = [r["server_ttft_s"] for r in rows if r["server_ttft_s"] is not None]
        out["sizes"][str(size)] = {
            "reps": rows,
            "server_ttft_median_s": (
                round(statistics.median(server), 4) if server else None
            ),
            "client_ttft_median_s": round(
                statistics.median(r["client_ttft_s"] for r in rows), 4
            ),
        }
    out["end_utc"] = utc()
    return out


def newest_traces(directory, since, settle=5.0, timeout=180.0):
    deadline, sizes = time.monotonic() + timeout, {}
    while time.monotonic() < deadline:
        paths = [
            p
            for p in glob.glob(os.path.join(directory, "**", "*.json*"), recursive=True)
            if os.path.getmtime(p) >= since
        ]
        current = {p: os.path.getsize(p) for p in paths}
        if current and current == sizes:
            return sorted(current)
        sizes = current
        time.sleep(settle)
    return sorted(sizes)


def profile_once(args, prompts, concurrency):
    stop = threading.Event()
    progress = [0] * concurrency
    results = [None] * concurrency

    def on_chunk(j):
        def callback(chunks):
            progress[j] = chunks
            return not stop.is_set()

        return callback

    def worker(j, ids):
        try:
            results[j] = stream(
                args.base_url, args.model, ids, args.max_tokens, on_chunk(j)
            )
        except Exception as exc:  # noqa: BLE001
            results[j] = {"error": str(exc)}

    ids = [prompts.tokens(args.context) for _ in range(concurrency)]
    threads = [
        threading.Thread(target=worker, args=(j, ids[j])) for j in range(concurrency)
    ]
    for thread in threads:
        thread.start()
    deadline = time.monotonic() + 300
    while min(progress) < args.lead_chunks and time.monotonic() < deadline:
        time.sleep(0.05)
    since = time.time() - 1
    prefix = f"s4-c{concurrency}-{uuid.uuid4().hex[:6]}"
    request = {
        "profile_prefix": prefix,
        "delay_iterations": args.delay,
        "max_iterations": args.steps,
    }
    try:
        http(args.base_url, "/start_profile", request, timeout=60).close()
    except urllib.error.HTTPError as exc:
        stop.set()
        return {
            "error": f"/start_profile returned {exc.code}; is --profiler-config set?"
        }
    # The profiler stops itself after max_iterations; give it the decode time,
    # then stop it explicitly in case the requests ended first.
    time.sleep(args.window_s)
    try:
        http(args.base_url, "/stop_profile", {}, timeout=600).close()
    except urllib.error.HTTPError:
        pass
    stop.set()
    for thread in threads:
        thread.join(timeout=120)
    out = {
        "prefix": prefix,
        "concurrency": concurrency,
        "chunks_before_start": list(progress),
        "traces": {},
    }
    for path in newest_traces(args.profile_dir, since):
        try:
            out["traces"][path] = analyze_events(load_trace(path), args.lm_head_ms)
        except (OSError, ValueError) as exc:
            out["traces"][path] = {"error": str(exc)}
    return out


def run_profile(args):
    prompts = Prompts(args.base_url, args.model, args.context)
    out = {"mode": "profile", "start_utc": utc(), "runs": []}
    for concurrency in [int(c) for c in args.concurrency.split(",") if c]:
        for _ in range(args.reps):
            out["runs"].append(profile_once(args, prompts, concurrency))
            time.sleep(2)
    out["end_utc"] = utc()
    return out


# ---------------------------------------------------------------- analysis


def load_trace(path):
    opener = gzip.open if path.endswith(".gz") else open
    with opener(path, "rt") as handle:
        data = json.load(handle)
    return data.get("traceEvents", data) if isinstance(data, dict) else data


def classify(name):
    for label, pattern in CLASSES:
        if pattern.search(name):
            return label
    return "other"


def merged_busy(spans):
    """Total length of the union of (start, end) spans."""
    total, cur_start, cur_end = 0.0, None, None
    for start, end in sorted(spans):
        if cur_end is None or start > cur_end:
            if cur_end is not None:
                total += cur_end - cur_start
            cur_start, cur_end = start, end
        else:
            cur_end = max(cur_end, end)
    if cur_end is not None:
        total += cur_end - cur_start
    return total


def template_n(name):
    """Last integer template argument of a wvSplitK kernel name (its N)."""
    match = re.search(r"wvSplitK\w*<([^>]*)>", name)
    if not match:
        return None
    ints = [p.strip() for p in match.group(1).split(",")]
    try:
        return int(ints[-1])
    except ValueError:
        return None


def pct(part, whole):
    return round(100.0 * part / whole, 2) if whole else None


def analyze_events(events, lm_head_ms=1.0, drafts_per_step=2):
    """Per-step decode anatomy from one torch-profiler trace (times in us)."""
    kernels = sorted(
        (
            e
            for e in events
            if e.get("ph") == "X"
            and e.get("cat") in KERNEL_CATS
            and "ts" in e
            and e.get("dur", 0) > 0
        ),
        key=lambda e: e["ts"],
    )
    if not kernels:
        return {
            "kernel_count": 0,
            "note": "no GPU kernel events (graph replay not traced?)",
        }
    # The fp16 lm_head is the only unquantized GEMM this long in a decode step:
    # wvSplitK at <= 5 rows, hipBLASLt (Tensile Cijk_*) at 6.
    heads = [
        i
        for i, e in enumerate(kernels)
        if e["dur"] >= lm_head_ms * 1000.0
        and classify(e.get("name", "")) in ("skinny_gemm", "blas_gemm", "other")
    ]
    per_step = drafts_per_step + 1
    if len(heads) < 2 * per_step:
        return {
            "kernel_count": len(kernels),
            "lm_head_kernels": len(heads),
            "note": f"fewer than two steps of lm_head kernels at >= {lm_head_ms} ms",
        }
    # Align on the target head: it is the first of each triple. A draft head
    # follows another head with only the MTP layer between them, so the target
    # head is the one after the longest head-to-head distance in the cycle.
    gaps = [
        kernels[heads[i + 1]]["ts"] - kernels[heads[i]]["ts"]
        for i in range(len(heads) - 1)
    ]
    phase_score = [0.0] * per_step
    for i, gap in enumerate(gaps):
        phase_score[(i + 1) % per_step] += gap
    target_phase = max(range(per_step), key=lambda p: phase_score[p])
    roles = {}
    for i, idx in enumerate(heads):
        roles[idx] = (
            "lm_head_target" if (i - target_phase) % per_step == 0 else "lm_head_draft"
        )
    # Step boundary = end of the last draft head of each step.
    boundaries = []
    for i, idx in enumerate(heads):
        if (i - target_phase) % per_step == drafts_per_step:
            boundaries.append(kernels[idx]["ts"] + kernels[idx]["dur"])
    steps = []
    k = 0
    for left, right in zip(boundaries, boundaries[1:]):
        while k < len(kernels) and kernels[k]["ts"] < left:
            k += 1
        j = k
        by_class, spans, names = {}, [], {}
        first_ts, last_end = None, None
        heads_in_step = {"lm_head_target": [], "lm_head_draft": []}
        while j < len(kernels) and kernels[j]["ts"] < right:
            e = kernels[j]
            label = roles.get(j) or classify(e.get("name", ""))
            if label in heads_in_step:
                heads_in_step[label].append(
                    {
                        "us": round(e["dur"], 1),
                        "n": template_n(e.get("name", "")),
                        "name": e.get("name", "")[:90],
                    }
                )
            by_class[label] = by_class.get(label, 0.0) + e["dur"]
            short = e.get("name", "?")[:110]
            total, count = names.get(short, (0.0, 0))
            names[short] = (total + e["dur"], count + 1)
            end = min(e["ts"] + e["dur"], right)
            spans.append((e["ts"], end))
            first_ts = e["ts"] if first_ts is None else first_ts
            last_end = end if last_end is None else max(last_end, end)
            j += 1
        period = right - left
        busy = merged_busy(spans)
        kernel_sum = sum(by_class.values())
        steps.append(
            {
                "period_us": period,
                "busy_us": busy,
                "kernel_sum_us": kernel_sum,
                "kernels": j - k,
                "idle_us": period - busy,
                "lead_idle_us": (first_ts - left) if first_ts is not None else period,
                "by_class_us": by_class,
                "heads": heads_in_step,
                "names": names,
            }
        )
    if not steps:
        return {"kernel_count": len(kernels), "note": "no complete step"}

    def med(values):
        return round(statistics.median(values), 1)

    classes = sorted({c for s in steps for c in s["by_class_us"]})
    period = med([s["period_us"] for s in steps])
    busy = med([s["busy_us"] for s in steps])
    class_med = {c: med([s["by_class_us"].get(c, 0.0) for s in steps]) for c in classes}
    draft_heads = med([s["by_class_us"].get("lm_head_draft", 0.0) for s in steps])
    target_head = med([s["by_class_us"].get("lm_head_target", 0.0) for s in steps])
    names = {}
    for s in steps:
        for name, (total, count) in s["names"].items():
            t, c = names.get(name, (0.0, 0))
            names[name] = (t + total, c + count)
    top = sorted(names.items(), key=lambda kv: -kv[1][0])[:25]
    return {
        "kernel_count": len(kernels),
        "steps": len(steps),
        "median_period_ms": round(period / 1000, 3),
        "median_gpu_busy_ms": round(busy / 1000, 3),
        "median_idle_ms": round((period - busy) / 1000, 3),
        "idle_pct_of_period": pct(period - busy, period),
        "median_lead_idle_ms": round(med([s["lead_idle_us"] for s in steps]) / 1000, 3),
        "kernels_per_step": med([s["kernels"] for s in steps]),
        "by_class_ms": {c: round(v / 1000, 3) for c, v in class_med.items()},
        "by_class_pct_of_busy": {c: pct(v, busy) for c, v in class_med.items()},
        "draft_lm_heads_ms": round(draft_heads / 1000, 3),
        "draft_lm_heads_pct_of_busy": pct(draft_heads, busy),
        "draft_lm_heads_pct_of_period": pct(draft_heads, period),
        "target_lm_head_ms": round(target_head / 1000, 3),
        "head_samples": steps[0]["heads"],
        "periods_ms": [round(s["period_us"] / 1000, 3) for s in steps],
        "top_kernels_per_step": [
            [name, round(total / len(steps) / 1000, 4), round(count / len(steps), 2)]
            for name, (total, count) in top
        ],
    }


def run_analyze(args):
    return {
        path: analyze_events(load_trace(path), args.lm_head_ms, args.drafts)
        for path in args.paths
    }


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    sub = parser.add_subparsers(dest="mode", required=True)

    def common(p):
        p.add_argument("--base-url", default="http://127.0.0.1:8000")
        p.add_argument("--model", required=True)
        p.add_argument("--out")

    p = sub.add_parser("jit-sweep")
    common(p)
    p = sub.add_parser("ttft")
    common(p)
    p.add_argument("--sizes", default="16384")
    p.add_argument("--reps", type=int, default=5)
    p.add_argument("--settle", type=float, default=1.0)
    p = sub.add_parser("profile")
    common(p)
    p.add_argument("--context", type=int, default=2048)
    p.add_argument("--concurrency", default="1,2")
    p.add_argument("--steps", type=int, default=20)
    p.add_argument("--delay", type=int, default=3)
    p.add_argument("--reps", type=int, default=2)
    p.add_argument("--max-tokens", type=int, default=600)
    p.add_argument("--lead-chunks", type=int, default=8)
    p.add_argument("--window-s", type=float, default=6.0)
    p.add_argument("--profile-dir", default="/tmp/vllm-profile")
    p.add_argument("--lm-head-ms", type=float, default=1.0)
    p = sub.add_parser("analyze")
    p.add_argument("paths", nargs="+")
    p.add_argument("--lm-head-ms", type=float, default=1.0)
    p.add_argument("--drafts", type=int, default=2)
    p.add_argument("--out")
    args = parser.parse_args(argv)

    runner = {
        "jit-sweep": run_jit_sweep,
        "ttft": run_ttft,
        "profile": run_profile,
        "analyze": run_analyze,
    }[args.mode]
    result = runner(args)
    text = json.dumps(result, indent=1)
    if args.out:
        with open(args.out, "w") as handle:
            handle.write(text)
    print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
