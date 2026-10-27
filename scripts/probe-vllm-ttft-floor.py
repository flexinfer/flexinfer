#!/usr/bin/env python3
"""Short-prompt TTFT floor probe (S3b); run inside the vLLM serving pod.

    kubectl -n flexinfer-system exec -i deploy/qwen38-27b-v032-s3-attn-canary -- \
        python3 - --model qwen38-27b-v032-s3-attn-canary --out /tmp/s3b.json \
        < scripts/probe-vllm-ttft-floor.py

Self-contained on purpose (the pod receives one file over exec, stdlib only).
For each prompt size it sends single-stream requests that generate one token,
each behind a unique leading nonce so every request is a cold prefill, and
splits the time to first token with the server's own counters, scraped
before and after each request:

  - client TTFT (first streamed chunk) against the server's TTFT, queue and
    prefill histograms; the remainder is HTTP, tokenization and IPC time;
  - output batches (vllm:iteration_tokens_total observations) and prefix-cache
    hit tokens, which must be zero for a cold prefill.

A request that overlaps another one (a finished-request or prompt-token delta
that is not its own, or a busy engine before it starts) is marked shared and
left out of the medians, so the probe also runs against a serving lane.

Optional evidence, one request or a short burst per size:

  --profile-sizes  wraps one request in /start_profile and /stop_profile (the
                   server must run with --profiler-config, torch profiler,
                   torch_profiler_dir = --profile-dir) and summarizes the
                   newest worker trace: GPU busy and idle time, the top
                   kernels, and the longest GPU idle gaps with the host ops
                   that ran during them;
  --pyspy-sizes    samples the EngineCore and API server processes with py-spy
                   (--install-pyspy fetches it into /tmp/pyspy) while requests
                   of that size run back to back, and reports the hottest
                   frames and active thread-samples per sampling tick.
"""

import argparse
import glob
import gzip
import json
import os
import re
import shutil
import statistics
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

METRIC_LINE = re.compile(r"^([A-Za-z_:][A-Za-z0-9_:]*)(\{[^}]*\})?\s+(\S+)")
LABEL = re.compile(r'(\w+)="([^"]*)"')
HISTOGRAMS = {
    "ttft": "vllm:time_to_first_token_seconds",
    "queue": "vllm:request_queue_time_seconds",
    "prefill": "vllm:request_prefill_time_seconds",
    "inference": "vllm:request_inference_time_seconds",
    "e2e": "vllm:e2e_request_latency_seconds",
    "batches": "vllm:iteration_tokens_total",
}
FINISHED = "vllm:request_success_total"
PROMPT_TOKENS = "vllm:prompt_tokens_total"
PREFIX_HITS = "vllm:prefix_cache_hits_total"
CACHED_TOKENS = "vllm:prompt_tokens_cached_total"
BUSY_GAUGES = ("vllm:num_requests_running", "vllm:num_requests_waiting")
FILLER = "Note {i}: sensor {i} reported nominal temperature and pressure today.\n"
PYSPY_DIR = "/tmp/pyspy"
PYSPY_VERSION = "0.4.2"
KERNEL_CATS = ("kernel", "gpu_memcpy", "gpu_memset")
HOST_CATS = ("cpu_op", "user_annotation", "python_function", "cuda_runtime")


def parse_metrics(text):
    """Sum every sample over its label sets; histogram buckets are skipped."""
    totals = {}
    for line in text.splitlines():
        match = METRIC_LINE.match(line)
        if not match or line.startswith("#"):
            continue
        name, _, raw = match.groups()
        if name.endswith("_bucket") or name.endswith("_created"):
            continue
        try:
            value = float(raw)
        except ValueError:
            continue
        totals[name] = totals.get(name, 0.0) + value
    return totals


def info_labels(text, name):
    """Labels of the first sample of an info gauge such as cache_config_info."""
    for line in text.splitlines():
        match = METRIC_LINE.match(line)
        if match and match.group(1) == name and match.group(2):
            return dict(LABEL.findall(match.group(2)))
    return {}


def request_split(before, after, prompt_tokens):
    """One request's server-side split from two scrapes around it."""

    def delta(name):
        return after.get(name, 0.0) - before.get(name, 0.0)

    finished = delta(FINISHED)
    busy = any(before.get(name, 0.0) for name in BUSY_GAUGES)
    shared = (
        busy
        or round(finished) != 1
        or round(delta(PROMPT_TOKENS)) != prompt_tokens
        or round(delta(HISTOGRAMS["ttft"] + "_count")) != 1
    )
    split = {"shared": shared}
    for key, name in HISTOGRAMS.items():
        if key == "batches":
            continue
        split[f"server_{key}_s"] = round(delta(name + "_sum"), 4)
    split["output_batches"] = round(delta(HISTOGRAMS["batches"] + "_count"))
    split["batch_tokens"] = round(delta(HISTOGRAMS["batches"] + "_sum"))
    split["prefix_hit_tokens"] = round(delta(PREFIX_HITS))
    split["cached_prompt_tokens"] = round(delta(CACHED_TOKENS))
    return split


def http(base, path, payload=None, timeout=300):
    body = None if payload is None else json.dumps(payload).encode()
    return urllib.request.urlopen(
        urllib.request.Request(
            base + path,
            data=body,
            headers={"Content-Type": "application/json"},
        ),
        timeout=timeout,
    )


def scrape(base):
    with http(base, "/metrics", timeout=30) as response:
        return response.read().decode("utf-8")


def tokenize(base, model, text):
    payload = {"model": model, "prompt": text, "add_special_tokens": False}
    with http(base, "/tokenize", payload) as response:
        return json.load(response)["tokens"]


def read_first_chunk(response, started, clock=time.perf_counter):
    """Time to the first streamed choice, and the usage block at the end."""
    first, usage, done = None, None, False
    for raw in response:
        line = raw.decode("utf-8").strip()
        if not line.startswith("data:"):
            continue
        data = line[5:].strip()
        if data == "[DONE]":
            done = True
            break
        event = json.loads(data)
        if event.get("error"):
            raise ValueError(f"stream error: {event['error']}")
        if event.get("choices") and first is None:
            first = clock()
        if event.get("usage"):
            usage = event["usage"]
    finished = clock()
    if not done or first is None or not usage:
        raise ValueError("incomplete SSE response")
    return {
        "client_ttft_s": round(first - started, 4),
        "client_total_s": round(finished - started, 4),
        "prompt_tokens": usage["prompt_tokens"],
    }


def complete(base, model, token_ids):
    """One cold prefill of exactly len(token_ids) tokens, one output token."""
    payload = {
        "model": model,
        "prompt": token_ids,
        "max_tokens": 1,
        "temperature": 0,
        "stream": True,
        "stream_options": {"include_usage": True},
    }
    started = time.perf_counter()
    with http(base, "/v1/completions", payload) as response:
        return read_first_chunk(response, started)


def chat(base, model, prompt):
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": 1,
        "temperature": 0,
        "stream": True,
        "stream_options": {"include_usage": True},
    }
    started = time.perf_counter()
    with http(base, "/v1/chat/completions", payload) as response:
        return read_first_chunk(response, started)


class Prompts:
    """Exact-length token prompts behind a fresh nonce, and sized chat text."""

    def __init__(self, base, model, max_size):
        self.base, self.model = base, model
        pool = []
        repeats = max(64, max_size // 12)
        while len(pool) < max_size:
            text = "".join(FILLER.format(i=i) for i in range(repeats))
            pool = tokenize(base, model, text)
            repeats *= 2
        self.pool = pool

    def tokens(self, size):
        nonce = tokenize(self.base, self.model, f"Session {uuid.uuid4().hex[:12]}.\n")
        if len(nonce) >= size:
            raise ValueError(f"size {size} is shorter than the nonce")
        return nonce + self.pool[: size - len(nonce)]

    def chat_text(self, size, tolerance=0.05):
        low, high = size * (1 - tolerance), size * (1 + tolerance)
        repeats = max(1, size // 16)
        for _ in range(8):
            prompt = (
                f"Session {uuid.uuid4().hex}.\nContext log:\n"
                + "".join(FILLER.format(i=i) for i in range(repeats))
                + "\nReply with one word."
            )
            payload = {
                "model": self.model,
                "add_generation_prompt": True,
                "messages": [{"role": "user", "content": prompt}],
            }
            with http(self.base, "/tokenize", payload) as response:
                count = json.load(response)["count"]
            if low <= count <= high:
                return prompt
            repeats = max(1, round(repeats * size / count))
        raise ValueError(f"could not size a ~{size}-token chat prompt")


def wait_idle(base, timeout):
    """Wait until the engine has nothing running or waiting; return a scrape."""
    deadline = time.monotonic() + timeout
    while True:
        text = scrape(base)
        metrics = parse_metrics(text)
        if not any(metrics.get(name, 0.0) for name in BUSY_GAUGES):
            return metrics
        if time.monotonic() > deadline:
            return metrics
        time.sleep(0.5)


def measure(base, send, prompt_tokens_hint, idle_timeout, settle=0.3):
    before = wait_idle(base, idle_timeout)
    client = send()
    time.sleep(settle)
    after = parse_metrics(scrape(base))
    split = request_split(before, after, client["prompt_tokens"])
    split.update(client)
    split["host_overhead_s"] = round(
        client["client_ttft_s"] - split["server_ttft_s"], 4
    )
    if prompt_tokens_hint and client["prompt_tokens"] != prompt_tokens_hint:
        split["shared"] = True
    return split


MEDIAN_KEYS = (
    "client_ttft_s",
    "server_ttft_s",
    "server_queue_s",
    "server_prefill_s",
    "host_overhead_s",
    "output_batches",
    "prompt_tokens",
)


def medians(rows):
    """Median of each reported field over the rows not marked shared."""
    clean = [row for row in rows if not row["shared"]]
    out = {"n": len(clean), "shared": len(rows) - len(clean)}
    for key in MEDIAN_KEYS:
        values = [row[key] for row in clean if key in row]
        if values:
            out[key] = round(statistics.median(values), 4)
    return out


def sweep(base, model, prompts, sizes, reps, idle_timeout, kind="completions"):
    results = {}
    for size in sizes:
        rows = []
        for rep in range(reps):
            if kind == "completions":
                ids = prompts.tokens(size)
                send, hint = (lambda ids=ids: complete(base, model, ids)), size
            else:
                text = prompts.chat_text(size)
                send, hint = (lambda text=text: chat(base, model, text)), 0
            row = measure(base, send, hint, idle_timeout)
            rows.append(row)
            print(
                f"{kind} size={size} rep={rep} prompt={row['prompt_tokens']} "
                f"client_ttft={row['client_ttft_s']:.3f} "
                f"server_ttft={row['server_ttft_s']:.3f} "
                f"queue={row['server_queue_s']:.3f} "
                f"prefill={row['server_prefill_s']:.3f} "
                f"batches={row['output_batches']} "
                f"hit={row['prefix_hit_tokens']} shared={row['shared']}",
                flush=True,
            )
        results[str(size)] = {"median": medians(rows), "rows": rows}
    return results


def merged_intervals(events):
    spans = sorted((e["ts"], e["ts"] + e.get("dur", 0)) for e in events)
    merged = []
    for start, end in spans:
        if merged and start <= merged[-1][1]:
            merged[-1][1] = max(merged[-1][1], end)
        else:
            merged.append([start, end])
    return merged


def summarize_trace(events, gap_ms=20.0, top=15):
    """GPU busy/idle time, top kernels and host ops inside long GPU gaps."""
    complete_events = [e for e in events if e.get("ph") == "X" and "ts" in e]
    kernels = [e for e in complete_events if e.get("cat") in KERNEL_CATS]
    host = [e for e in complete_events if e.get("cat") in HOST_CATS]
    if not kernels:
        return {"kernel_count": 0, "host_events": len(host)}
    busy = merged_intervals(kernels)
    start, end = busy[0][0], busy[-1][1]
    busy_us = sum(b - a for a, b in busy)
    by_kernel = {}
    for event in kernels:
        name = event.get("name", "?")[:120]
        total, count = by_kernel.get(name, (0.0, 0))
        by_kernel[name] = (total + event.get("dur", 0), count + 1)
    top_kernels = sorted(by_kernel.items(), key=lambda item: -item[1][0])[:top]
    gaps = []
    for (_, gap_start), (gap_end, _) in zip(busy, busy[1:]):
        if gap_end - gap_start < gap_ms * 1000:
            continue
        overlap = {}
        for event in host:
            lo = max(gap_start, event["ts"])
            hi = min(gap_end, event["ts"] + event.get("dur", 0))
            if hi > lo:
                name = event.get("name", "?")[:100]
                overlap[name] = overlap.get(name, 0.0) + (hi - lo)
        gaps.append(
            {
                "at_ms": round((gap_start - start) / 1000, 1),
                "dur_ms": round((gap_end - gap_start) / 1000, 1),
                "host": [
                    [name, round(us / 1000, 1)]
                    for name, us in sorted(overlap.items(), key=lambda kv: -kv[1])[:4]
                ],
            }
        )
    gaps.sort(key=lambda gap: -gap["dur_ms"])
    annotations = {}
    for event in host:
        if event.get("cat") != "user_annotation":
            continue
        name = event.get("name", "?")[:100]
        total, count = annotations.get(name, (0.0, 0))
        annotations[name] = (total + event.get("dur", 0), count + 1)
    return {
        "kernel_count": len(kernels),
        "gpu_span_ms": round((end - start) / 1000, 1),
        "gpu_busy_ms": round(busy_us / 1000, 1),
        "gpu_idle_ms": round((end - start - busy_us) / 1000, 1),
        "top_kernels": [
            [name, round(total / 1000, 2), count]
            for name, (total, count) in top_kernels
        ],
        "idle_gaps": gaps[:10],
        "annotations": [
            [name, round(total / 1000, 1), count]
            for name, (total, count) in sorted(
                annotations.items(), key=lambda kv: -kv[1][0]
            )[:top]
        ],
    }


def load_trace(path):
    opener = gzip.open if path.endswith(".gz") else open
    with opener(path, "rt") as handle:
        data = json.load(handle)
    return data.get("traceEvents", data) if isinstance(data, dict) else data


def newest_traces(directory, since, settle=5.0, timeout=120.0):
    """Trace files written after ``since`` whose sizes have stopped changing."""
    deadline = time.monotonic() + timeout
    sizes = {}
    while time.monotonic() < deadline:
        paths = [
            path
            for path in glob.glob(
                os.path.join(directory, "**", "*.json*"), recursive=True
            )
            if os.path.getmtime(path) >= since
        ]
        current = {path: os.path.getsize(path) for path in paths}
        if current and current == sizes:
            return sorted(current)
        sizes = current
        time.sleep(settle)
    return sorted(sizes)


def profile_one(base, model, prompts, size, directory):
    since = time.time() - 1
    prefix = f"s3b-{size}-{uuid.uuid4().hex[:6]}"
    # Tokenize before the window so the trace holds only the request itself.
    ids = prompts.tokens(size)
    try:
        http(base, "/start_profile", {"profile_prefix": prefix}, timeout=60).close()
    except urllib.error.HTTPError as exc:
        return {
            "error": f"/start_profile returned {exc.code}; is --profiler-config set?"
        }
    try:
        client = complete(base, model, ids)
    finally:
        http(base, "/stop_profile", {}, timeout=600).close()
    out = {"prefix": prefix, "client": client, "traces": {}}
    for path in newest_traces(directory, since):
        try:
            out["traces"][path] = summarize_trace(load_trace(path))
        except (OSError, ValueError) as exc:
            out["traces"][path] = {"error": str(exc)}
    return out


def find_pids(proc="/proc"):
    """The EngineCore and API server pids, from their command lines."""
    found = {}
    for entry in sorted(os.listdir(proc), key=lambda name: (len(name), name)):
        if not entry.isdigit():
            continue
        try:
            with open(os.path.join(proc, entry, "cmdline"), "rb") as handle:
                cmdline = handle.read().replace(b"\0", b" ").decode(errors="replace")
        except OSError:
            continue
        if "EngineCore" in cmdline:
            found.setdefault("engine_core", int(entry))
        elif "vllm serve" in cmdline or "vllm.entrypoints" in cmdline:
            found.setdefault("api_server", int(entry))
    return found


def pyspy_binary(install):
    candidate = shutil.which("py-spy") or os.path.join(PYSPY_DIR, "bin", "py-spy")
    if os.path.exists(candidate):
        return candidate
    if not install:
        return None
    subprocess.run(
        [
            sys.executable,
            "-m",
            "pip",
            "install",
            "--quiet",
            "--no-cache-dir",
            "--target",
            PYSPY_DIR,
            f"py-spy=={PYSPY_VERSION}",
        ],
        check=True,
    )
    return os.path.join(PYSPY_DIR, "bin", "py-spy")


def summarize_collapsed(text, top=12):
    """Hottest leaf and inclusive frames of py-spy's raw (collapsed) output."""
    total, leaves, inclusive = 0, {}, {}
    for line in text.splitlines():
        stack, _, count = line.rpartition(" ")
        if not stack or not count.isdigit():
            continue
        samples = int(count)
        frames = stack.split(";")
        total += samples
        leaves[frames[-1]] = leaves.get(frames[-1], 0) + samples
        for frame in set(frames):
            inclusive[frame] = inclusive.get(frame, 0) + samples

    def ranked(table):
        return [
            [frame[:140], samples, round(samples / total, 3)]
            for frame, samples in sorted(table.items(), key=lambda kv: -kv[1])[:top]
        ]

    return {
        "samples": total,
        "leaf": ranked(leaves) if total else [],
        "inclusive": ranked(inclusive) if total else [],
    }


def pyspy_burst(base, model, prompts, size, seconds, binary, rate=100):
    pids = find_pids()
    if not pids:
        return {"error": "no EngineCore or API server process found"}
    procs, outputs = {}, {}
    for role, pid in pids.items():
        outputs[role] = f"/tmp/s3b-pyspy-{role}-{size}.txt"
        procs[role] = subprocess.Popen(
            [
                binary,
                "record",
                "--pid",
                str(pid),
                "--duration",
                str(seconds),
                "--rate",
                str(rate),
                "--nonblocking",
                "--format",
                "raw",
                "--output",
                outputs[role],
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
        )
    time.sleep(0.5)
    requests = []
    deadline = time.monotonic() + seconds - 1
    while time.monotonic() < deadline:
        ids = prompts.tokens(size)
        requests.append(complete(base, model, ids)["client_ttft_s"])
    out = {"pids": pids, "requests": len(requests), "ttft_s": requests, "processes": {}}
    for role, proc in procs.items():
        _, err = proc.communicate(timeout=seconds + 60)
        result = {"exit": proc.returncode}
        if proc.returncode != 0:
            result["stderr"] = err.decode(errors="replace")[-400:]
        if os.path.exists(outputs[role]):
            with open(outputs[role]) as handle:
                result.update(summarize_collapsed(handle.read()))
            # py-spy drops idle threads and counts each active thread per tick,
            # so this exceeds 1 when several threads are busy at once.
            result["active_samples_per_tick"] = round(
                result.get("samples", 0) / float(rate * seconds), 3
            )
        out["processes"][role] = result
    return out


def parse_sizes(raw):
    sizes = [int(part) for part in raw.split(",") if part.strip()]
    if any(size < 16 for size in sizes):
        raise ValueError("sizes must be at least 16 tokens")
    return sizes


def run(args):
    metrics_text = scrape(args.base_url)
    summary = {
        "name": "ttft_floor_probe",
        "model": args.model,
        "started": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "cache_config": info_labels(metrics_text, "vllm:cache_config_info"),
    }
    largest = max(args.sizes + args.profile_sizes + args.pyspy_sizes + [64])
    prompts = Prompts(args.base_url, args.model, largest)
    # One throwaway request so the first measured point is not a cold path.
    complete(args.base_url, args.model, prompts.tokens(64))
    summary["completions"] = sweep(
        args.base_url, args.model, prompts, args.sizes, args.reps, args.idle_timeout
    )
    if args.chat_sizes:
        summary["chat"] = sweep(
            args.base_url,
            args.model,
            prompts,
            args.chat_sizes,
            args.reps,
            args.idle_timeout,
            kind="chat",
        )
    if args.profile_sizes:
        summary["profiles"] = {
            str(size): profile_one(
                args.base_url, args.model, prompts, size, args.profile_dir
            )
            for size in args.profile_sizes
        }
    if args.pyspy_sizes:
        binary = pyspy_binary(args.install_pyspy)
        if binary is None:
            summary["pyspy"] = {"error": "py-spy not found; pass --install-pyspy"}
        else:
            summary["pyspy"] = {
                str(size): pyspy_burst(
                    args.base_url,
                    args.model,
                    prompts,
                    size,
                    args.pyspy_seconds,
                    binary,
                )
                for size in args.pyspy_sizes
            }
    summary["finished"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    print("size  n  client_ttft  server_ttft  queue  prefill  host  batches")
    for kind in ("completions", "chat"):
        for size, point in summary.get(kind, {}).items():
            m = point["median"]
            if not m.get("n"):
                print(f"{kind} {size}: every request shared the engine")
                continue
            print(
                f"{kind[:4]} {size:>5} {m['n']} {m['client_ttft_s']:>8.3f} "
                f"{m['server_ttft_s']:>11.3f} {m['server_queue_s']:>6.3f} "
                f"{m['server_prefill_s']:>7.3f} {m['host_overhead_s']:>5.3f} "
                f"{m['output_batches']:>4}"
            )
    for size, profile in summary.get("profiles", {}).items():
        for path, trace in profile.get("traces", {}).items():
            print(
                f"profile {size} {os.path.basename(path)}: "
                + json.dumps(
                    {
                        k: trace.get(k)
                        for k in ("gpu_span_ms", "gpu_busy_ms", "gpu_idle_ms")
                    }
                )
            )
        if profile.get("error"):
            print(f"profile {size}: {profile['error']}")
    pyspy = summary.get("pyspy", {})
    if "error" in pyspy:
        print(f"pyspy: {pyspy['error']}")
    else:
        for size, burst in pyspy.items():
            for role, result in burst.get("processes", {}).items():
                leaf = result.get("leaf", [])[:3]
                print(
                    f"pyspy {size} {role}: active={result.get('active_samples_per_tick')} {leaf}"
                )
    if args.out:
        with open(args.out, "w") as handle:
            json.dump(summary, handle, indent=1)
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("--base-url", default="http://127.0.0.1:8000")
    parser.add_argument("--model", required=True)
    parser.add_argument(
        "--sizes",
        default="32,64,128,256,384,399,401,512,768,799,801,1024,1124,1536,2048,4096",
        help="comma-separated exact prompt sizes in tokens (completions path)",
    )
    parser.add_argument("--reps", type=int, default=3)
    parser.add_argument(
        "--chat-sizes", default="", help="sizes to repeat on the chat path"
    )
    parser.add_argument(
        "--profile-sizes",
        default="",
        help="sizes to capture one torch-profiler trace each",
    )
    parser.add_argument("--profile-dir", default="/tmp/vllm-profile")
    parser.add_argument("--pyspy-sizes", default="", help="sizes to sample with py-spy")
    parser.add_argument("--pyspy-seconds", type=int, default=6)
    parser.add_argument("--install-pyspy", action="store_true")
    parser.add_argument(
        "--idle-timeout",
        type=float,
        default=60.0,
        help="seconds to wait for an idle engine before each request",
    )
    parser.add_argument("--out", help="write the full summary JSON here")
    args = parser.parse_args(argv)
    try:
        args.sizes = parse_sizes(args.sizes)
        args.chat_sizes = parse_sizes(args.chat_sizes)
        args.profile_sizes = parse_sizes(args.profile_sizes)
        args.pyspy_sizes = parse_sizes(args.pyspy_sizes)
    except ValueError as exc:
        parser.error(str(exc))
    if not 1 <= args.reps <= 20:
        parser.error("--reps must be between 1 and 20")
    return run(args)


if __name__ == "__main__":
    sys.exit(main())
