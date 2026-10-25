#!/usr/bin/env python3
"""MTP kill-test probes for the S2 canary; run inside the serving pod.

    kubectl -n flexinfer-system exec -i deploy/qwen38-27b-v032-mtp-canary -- \
        python3 - --model qwen38-27b-v032-mtp-canary < scripts/verify-vllm-mtp.py

Self-contained on purpose (the pod receives one file over exec). It reads the
vLLM spec-decode counters before and after a fixed workload and reports:

  - position-0 draft acceptance (accepted@pos0 / drafts) and the per-position
    vector, the overall acceptance rate (accepted / draft tokens) and the mean
    acceptance length in vLLM's convention (1 + accepted / drafts);
  - the standard coherence probes (`42`, Birch JSON, `MAPLE-4827`) and three
    greedy 256-token parity generations whose text hashes are compared across
    phases with --compare (MTP on vs the same digest with MTP off);
  - single-stream decode throughput at a short and a ~2K-token context, and a
    concurrency-2 soak of --soak-pairs request pairs;
  - with --contexts, a single-stream sweep at each prompt size (cold prefill
    TTFT, decode tok/s and that request's own draft acceptance), which is how
    S3 compares attention backends with MTP on and off. --phase measure turns
    the throughput and acceptance gates off for such A/B arms.

The thresholds are the S2 pass bar from
.loom/10-research-gfx1100-next-feature-2026-10-23.md §7. Image identity,
boot lines, KV pool, restarts and Loki fault markers are operator gates.
"""

import argparse
import concurrent.futures
import hashlib
import json
import re
import signal
import statistics
import sys
import threading
import time
import urllib.request
import uuid

SPEC_COUNTERS = (
    "vllm:spec_decode_num_drafts",
    "vllm:spec_decode_num_draft_tokens",
    "vllm:spec_decode_num_accepted_tokens",
    "vllm:spec_decode_num_accepted_tokens_per_pos",
)
METRIC_LINE = re.compile(r"^([A-Za-z_:][A-Za-z0-9_:]*)(\{[^}]*\})?\s+(\S+)")
LABEL = re.compile(r'(\w+)="([^"]*)"')

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
DECODE_PROMPT = (
    "Write a detailed 400-word explanation of how rain forms, from "
    "evaporation to rainfall."
)
SOAK_PROMPTS = [
    "Write a detailed 400-word explanation of how rain forms.",
    "Write a detailed 400-word explanation of how snow forms.",
]


def read_stream(response, started, clock=time.perf_counter):
    content, reasoning = [], []
    first, last, usage, finish = None, None, None, None
    done = False
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
        if event.get("usage"):
            usage = event["usage"]
        for choice in event.get("choices", []):
            delta = choice.get("delta") or {}
            text = delta.get("content") or ""
            thought = delta.get("reasoning_content") or delta.get("reasoning") or ""
            if text or thought:
                last = clock()
                if first is None:
                    first = last
                content.append(text)
                reasoning.append(thought)
            if choice.get("finish_reason"):
                finish = choice["finish_reason"]
    if not done or not finish or first is None:
        raise ValueError("incomplete or empty SSE response")
    if finish not in ("stop", "length"):
        raise ValueError(f"unexpected finish reason: {finish}")
    if not usage or usage.get("completion_tokens", 0) <= 0:
        raise ValueError("missing completion-token usage; throughput is unmeasurable")
    if "".join(reasoning).strip() or "<think>" in "".join(content):
        raise ValueError("non-thinking default emitted reasoning")
    tokens = usage["completion_tokens"]
    answer = "".join(content).strip()
    return {
        "answer": answer,
        "sha256": hashlib.sha256(answer.encode("utf-8")).hexdigest()[:16],
        "prompt_tokens": usage["prompt_tokens"],
        "completion_tokens": tokens,
        "finish_reason": finish,
        "ttft_s": round(first - started, 4),
        "decode_tps": round((tokens - 1) / (last - first), 4) if last > first else None,
    }


def request(base, path, payload=None, timeout=180):
    body = None if payload is None else json.dumps(payload).encode()
    return urllib.request.urlopen(
        urllib.request.Request(
            base + path,
            data=body,
            headers={"Content-Type": "application/json"},
        ),
        timeout=timeout,
    )


def chat(base, model, prompt, max_tokens):
    started = time.perf_counter()
    # Deliberately omit chat_template_kwargs to exercise the service default.
    with request(
        base,
        "/v1/chat/completions",
        {
            "model": model,
            "messages": [{"role": "user", "content": prompt}],
            "temperature": 0,
            "max_tokens": max_tokens,
            "stream": True,
            "stream_options": {"include_usage": True},
        },
    ) as response:
        return read_stream(response, started)


def parse_spec_metrics(text):
    """Sum the spec-decode counters over engine/model labels, keyed by position."""
    totals = {name: 0.0 for name in SPEC_COUNTERS[:3]}
    per_pos = {}
    seen = False
    for line in text.splitlines():
        if not line or line.startswith("#"):
            continue
        match = METRIC_LINE.match(line)
        if not match:
            continue
        name, labels, value = match.group(1), match.group(2) or "", match.group(3)
        if name.endswith("_total"):
            name = name[: -len("_total")]
        if name not in SPEC_COUNTERS:
            continue
        value = float(value)
        if value != value or value < 0:
            raise ValueError(f"invalid counter value on {line!r}")
        seen = True
        if name == SPEC_COUNTERS[3]:
            position = dict(LABEL.findall(labels)).get("position")
            if position is None:
                raise ValueError("per-position counter without a position label")
            per_pos[int(position)] = per_pos.get(int(position), 0.0) + value
        else:
            totals[name] += value
    if not seen:
        return None
    return {
        "drafts": totals[SPEC_COUNTERS[0]],
        "draft_tokens": totals[SPEC_COUNTERS[1]],
        "accepted": totals[SPEC_COUNTERS[2]],
        "per_pos": dict(sorted(per_pos.items())),
    }


def spec_delta(before, after):
    drafts = after["drafts"] - before["drafts"]
    draft_tokens = after["draft_tokens"] - before["draft_tokens"]
    accepted = after["accepted"] - before["accepted"]
    positions = sorted(set(before["per_pos"]) | set(after["per_pos"]))
    per_pos = {
        p: after["per_pos"].get(p, 0.0) - before["per_pos"].get(p, 0.0)
        for p in positions
    }
    if drafts <= 0 or draft_tokens <= 0:
        raise ValueError("no drafts were recorded during the workload")
    if accepted < 0 or any(v < 0 for v in per_pos.values()):
        raise ValueError("spec-decode counters went backwards (engine restart?)")
    return {
        "drafts": drafts,
        "draft_tokens": draft_tokens,
        "accepted_tokens": accepted,
        "acceptance_rate": round(accepted / draft_tokens, 4),
        "mean_acceptance_length": round(1 + accepted / drafts, 4),
        "per_pos_acceptance": {
            str(p): round(v / drafts, 4) for p, v in per_pos.items()
        },
        "pos0_acceptance": round(per_pos.get(0, 0.0) / drafts, 4),
    }


def spec_metrics(base):
    with request(base, "/metrics") as response:
        return parse_spec_metrics(response.read().decode())


def recall_prompt(base, model):
    repeats = 70
    for _ in range(6):
        filler = "Line {i}: the quick brown fox jumps over the lazy dog near the river bank.\n"
        prompt = (
            "Remember the access key: MAPLE-4827.\n"
            + "".join(filler.format(i=i) for i in range(repeats))
            + "\nWhat is the access key? Reply with only the key."
        )
        with request(
            base,
            "/tokenize",
            {
                "model": model,
                "add_generation_prompt": True,
                "messages": [{"role": "user", "content": prompt}],
            },
        ) as response:
            count = json.load(response)["count"]
        if 2200 <= count <= 2500:
            return prompt
        repeats = max(1, round(repeats * 2350 / count))
    raise ValueError("could not construct a 2.3K-token recall prompt")


def context_prompt(base, model):
    """A ~2K-token prompt that still needs a long novel answer (decode at 2K)."""
    repeats = 60
    for _ in range(6):
        filler = (
            "Note {i}: sensor {i} reported nominal temperature and pressure today.\n"
        )
        prompt = (
            "Context log:\n"
            + "".join(filler.format(i=i) for i in range(repeats))
            + "\nIgnore the log above. "
            + DECODE_PROMPT
        )
        with request(
            base,
            "/tokenize",
            {
                "model": model,
                "add_generation_prompt": True,
                "messages": [{"role": "user", "content": prompt}],
            },
        ) as response:
            count = json.load(response)["count"]
        if 1800 <= count <= 2300:
            return prompt
        repeats = max(1, round(repeats * 2050 / count))
    raise ValueError("could not construct a ~2K-token context prompt")


def sized_prompt(base, model, target, nonce=None, tolerance=0.10):
    """A prompt of ``target`` +/- tolerance tokens that ends in a long novel task.

    ``nonce`` opens the prompt with a unique line so consecutive sweep points
    (and arms) never share a cached prefix: the TTFT it yields is a cold
    prefill of the whole context.
    """
    low, high = target * (1 - tolerance), target * (1 + tolerance)
    head = f"Session {nonce}.\n" if nonce else ""
    repeats = max(1, target // 16)
    for _ in range(8):
        filler = (
            "Note {i}: sensor {i} reported nominal temperature and pressure today.\n"
        )
        prompt = (
            head
            + "Context log:\n"
            + "".join(filler.format(i=i) for i in range(repeats))
            + "\nIgnore the log above. "
            + DECODE_PROMPT
        )
        with request(
            base,
            "/tokenize",
            {
                "model": model,
                "add_generation_prompt": True,
                "messages": [{"role": "user", "content": prompt}],
            },
        ) as response:
            count = json.load(response)["count"]
        if low <= count <= high:
            return prompt
        repeats = max(1, round(repeats * target / count))
    raise ValueError(f"could not construct a ~{target}-token context prompt")


def context_sweep(base, model, targets, max_tokens=256, metrics=None):
    """Single-stream decode, cold prefill and draft acceptance at each context.

    Each point is one request against a unique prefix. When the server
    speculates, the spec-decode counter delta around that one request gives the
    point's own acceptance, which is what shows how MTP's verify cost and
    acceptance move with context.
    """
    metrics = metrics or spec_metrics
    rows = []
    for target in targets:
        prompt = sized_prompt(base, model, target, nonce=uuid.uuid4().hex[:12])
        before = metrics(base)
        result = chat(base, model, prompt, max_tokens)
        after = metrics(base)
        row = {
            "name": "context",
            "target_tokens": target,
            "prompt_tokens": result["prompt_tokens"],
            "completion_tokens": result["completion_tokens"],
            "ttft_s": result["ttft_s"],
            "prefill_tps": (
                round(result["prompt_tokens"] / result["ttft_s"], 1)
                if result["ttft_s"] > 0
                else None
            ),
            "decode_tps": result["decode_tps"],
            "answer": result["answer"][:80],
        }
        if before is not None and after is not None:
            try:
                row["spec"] = spec_delta(before, after)
            except ValueError as exc:
                row["spec_error"] = str(exc)
        rows.append(emit(row))
    return rows


def parse_contexts(raw):
    """Parse ``--contexts 2048,8192`` into sorted unique ints in [256, 262144]."""
    if not raw:
        return []
    values = []
    for part in raw.split(","):
        part = part.strip()
        if not part.isdigit():
            raise ValueError(f"--contexts entries must be integers, got {part!r}")
        value = int(part)
        if not 256 <= value <= 262144:
            raise ValueError(f"--contexts entries must be in [256, 262144], got {value}")
        values.append(value)
    return sorted(set(values))


def emit(record):
    print(json.dumps(record), flush=True)
    return record


def soak(base, model, pairs, max_tokens=256):
    samples, completed = [], 0
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        for pair in range(pairs):
            barrier = threading.Barrier(2, timeout=30)

            def generate(prompt):
                barrier.wait()
                return chat(base, model, prompt, max_tokens)

            replies = [
                f.result() for f in [pool.submit(generate, p) for p in SOAK_PROMPTS]
            ]
            for reply in replies:
                if reply["completion_tokens"] < 128 or reply["decode_tps"] is None:
                    raise ValueError(
                        f"soak pair {pair}: short or unmeasurable generation"
                    )
                samples.append(reply["decode_tps"])
            completed += 2
            if pair % 10 == 0 or pair == pairs - 1:
                emit(
                    {
                        "name": "soak_progress",
                        "pair": pair + 1,
                        "requests": completed,
                        "median_per_request_decode_tps": round(
                            statistics.median(samples), 2
                        ),
                    }
                )
    return {
        "requests": completed,
        "median_per_request_decode_tps": round(statistics.median(samples), 2),
        "min_per_request_decode_tps": round(min(samples), 2),
    }


def verdict(args, results, spec):
    failures = []
    for name in ("arithmetic", "extraction", "recall_2k"):
        if not results[name]["coherent"]:
            failures.append(f"{name}: unexpected answer")
    if args.phase == "measure":
        # A/B arms (S3): only coherence gates; throughput, TTFT and acceptance
        # are the measurements being compared, not pass/fail bars.
        return failures
    if results["recall_2k"]["ttft_s"] >= 3:
        failures.append("recall TTFT >= 3 seconds")
    if results["decode_short_median_tps"] < args.min_decode_tps:
        failures.append(f"short-context decode < {args.min_decode_tps} tok/s")
    if (
        results["decode_2k"]["decode_tps"] is None
        or results["decode_2k"]["decode_tps"] < args.min_decode_tps
    ):
        failures.append(f"2K-context decode < {args.min_decode_tps} tok/s")
    if args.phase == "mtp":
        if spec is None:
            failures.append(
                "spec-decode metrics missing (speculative decoding not active)"
            )
        else:
            if spec["pos0_acceptance"] < args.min_pos0:
                failures.append(
                    f"pos-0 acceptance {spec['pos0_acceptance']} < {args.min_pos0}"
                )
            if spec["mean_acceptance_length"] < args.min_acceptance_length:
                failures.append(
                    f"mean acceptance length {spec['mean_acceptance_length']} "
                    f"< {args.min_acceptance_length}"
                )
    return failures


def run(args):
    base = args.base_url.rstrip("/")
    results = {}
    before = spec_metrics(base)
    if args.phase == "mtp" and before is None:
        emit({"name": "spec_metrics", "present": False})
    cases = [
        ("arithmetic", "What is 17 + 25? Reply with only the integer.", "42"),
        (
            "extraction",
            "Return only a JSON object with name and count from: name=Birch, count=7.",
            {"name": "Birch", "count": 7},
        ),
        ("recall_2k", recall_prompt(base, args.model), "MAPLE-4827"),
    ]
    for name, prompt, expected in cases:
        result = chat(base, args.model, prompt, 96)
        answer = result["answer"]
        try:
            passed = (
                (json.loads(answer) == expected)
                if isinstance(expected, dict)
                else answer == expected
            )
        except ValueError:
            passed = False
        result.update(name=name, coherent=passed)
        results[name] = emit(result)

    for name, prompt in PARITY_PROMPTS:
        result = chat(base, args.model, prompt, args.parity_tokens)
        result["name"] = name
        results[name] = emit({**result, "answer": result["answer"][:120]})
        results[name]["answer"] = result["answer"]

    speeds = []
    for i in range(3):
        result = chat(base, args.model, DECODE_PROMPT, 256)
        if result["completion_tokens"] < 128 or result["decode_tps"] is None:
            raise ValueError(f"decode_{i}: too few tokens for a throughput sample")
        result["name"] = f"decode_short_{i}"
        emit({**result, "answer": result["answer"][:80]})
        speeds.append(result["decode_tps"])
    results["decode_short_median_tps"] = round(statistics.median(speeds), 2)

    result = chat(
        args.base_url.rstrip("/"), args.model, context_prompt(base, args.model), 256
    )
    result["name"] = "decode_2k"
    results["decode_2k"] = emit({**result, "answer": result["answer"][:80]})

    results["contexts"] = (
        context_sweep(base, args.model, args.contexts) if args.contexts else []
    )

    if args.soak_pairs > 0:
        results["soak"] = emit(
            {"name": "soak", **soak(base, args.model, args.soak_pairs)}
        )

    after = spec_metrics(base)
    spec = (
        spec_delta(before, after)
        if (before is not None and after is not None)
        else None
    )
    if spec is not None:
        emit({"name": "spec_decode", **spec})
    failures = verdict(args, results, spec)
    summary = {
        "name": "mtp_probe",
        "phase": args.phase,
        "model": args.model,
        "verdict": "fail" if failures else "pass",
        "failures": failures,
        "decode_short_median_tps": results["decode_short_median_tps"],
        "decode_2k_tps": results["decode_2k"]["decode_tps"],
        "contexts": [
            {k: v for k, v in row.items() if k not in ("name", "answer")}
            for row in results["contexts"]
        ],
        "spec_decode": spec,
        "parity": {name: results[name]["sha256"] for name, _ in PARITY_PROMPTS},
        "parity_text": {name: results[name]["answer"] for name, _ in PARITY_PROMPTS},
        "probes": {
            name: results[name]["answer"]
            for name in ("arithmetic", "extraction", "recall_2k")
        },
    }
    if args.out:
        with open(args.out, "w", encoding="utf-8") as handle:
            json.dump(summary, handle, indent=1)
    emit({k: v for k, v in summary.items() if k != "parity_text"})
    return 1 if failures else 0


def compare(paths):
    runs = []
    for path in paths:
        with open(path, encoding="utf-8") as handle:
            runs.append(json.load(handle))
    first, second = runs
    diffs = []
    for name in sorted(set(first["parity"]) | set(second["parity"])):
        if first["parity"].get(name) != second["parity"].get(name):
            a, b = first["parity_text"].get(name, ""), second["parity_text"].get(
                name, ""
            )
            prefix = 0
            while prefix < min(len(a), len(b)) and a[prefix] == b[prefix]:
                prefix += 1
            diffs.append(
                {
                    "name": name,
                    "common_prefix_chars": prefix,
                    "len_a": len(a),
                    "len_b": len(b),
                }
            )
    for name in ("arithmetic", "extraction", "recall_2k"):
        if first["probes"].get(name) != second["probes"].get(name):
            diffs.append(
                {
                    "name": name,
                    "a": first["probes"].get(name),
                    "b": second["probes"].get(name),
                }
            )
    emit(
        {
            "name": "parity_compare",
            "phases": [first["phase"], second["phase"]],
            "verdict": "fail" if diffs else "pass",
            "diffs": diffs,
        }
    )
    return 1 if diffs else 0


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("--base-url", default="http://127.0.0.1:8000")
    parser.add_argument("--model")
    parser.add_argument(
        "--phase",
        choices=("mtp", "baseline", "measure"),
        default="mtp",
        help=(
            "mtp enforces the acceptance and throughput gates; baseline enforces "
            "throughput only; measure (A/B arms) enforces coherence only"
        ),
    )
    parser.add_argument(
        "--contexts",
        default="",
        help=(
            "comma-separated prompt sizes in tokens for a single-stream context "
            "sweep (decode, cold prefill and per-point acceptance), e.g. "
            "2048,8192,16384,28672"
        ),
    )
    parser.add_argument("--min-pos0", type=float, default=0.70)
    parser.add_argument("--min-acceptance-length", type=float, default=1.6)
    parser.add_argument("--min-decode-tps", type=float, default=60.0)
    parser.add_argument("--parity-tokens", type=int, default=256)
    parser.add_argument(
        "--soak-pairs",
        type=int,
        default=50,
        help="concurrency-2 pairs of 256-token generations (0 skips)",
    )
    parser.add_argument("--out", help="write the full summary JSON here for --compare")
    parser.add_argument(
        "--compare",
        nargs=2,
        metavar="SUMMARY_JSON",
        help="compare two --out summaries for greedy parity and exit",
    )
    args = parser.parse_args(argv)
    if args.compare:
        return compare(args.compare)
    if not args.model:
        parser.error("--model is required unless --compare is used")
    if not 0 <= args.soak_pairs <= 200:
        parser.error("--soak-pairs must be between 0 and 200")
    try:
        args.contexts = parse_contexts(args.contexts)
    except ValueError as exc:
        parser.error(str(exc))
    return run(args)


if __name__ == "__main__":
    # Bound the entire in-pod run; 50 soak pairs at ~40 tok/s take ~6 minutes,
    # and a 2K-28K context sweep adds about 4 more.
    signal.alarm(2100)
    try:
        sys.exit(main())
    except Exception as exc:
        print(
            json.dumps({"name": "mtp_probe", "verdict": "fail", "error": str(exc)}),
            flush=True,
        )
        sys.exit(1)
