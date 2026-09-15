#!/usr/bin/env python3
"""Bounded HTTP probes for the #74 S2 canary; run inside the serving pod.

kubectl -n flexinfer-system exec -i deploy/qwen38-27b-v026-canary -- \
    python3 - --model qwen38-27b-v026-canary < scripts/verify-vllm-canary.py

This checks coherence, the default non-thinking behavior, and warm latency.
Image identity, startup logs, KV capacity, restarts, and neighbouring cache
integrity are separate operator gates. HTTP success alone cannot promote a pin.
"""

import argparse
import json
import signal
import statistics
import sys
import time
import urllib.request


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
    return {
        "answer": "".join(content).strip(),
        "prompt_tokens": usage["prompt_tokens"],
        "completion_tokens": tokens,
        "finish_reason": finish,
        "ttft_s": round(first - started, 4),
        "decode_tps": round((tokens - 1) / (last - first), 4) if last > first else None,
    }


def request(base, path, payload):
    return urllib.request.urlopen(urllib.request.Request(
        base + path, data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
    ), timeout=90)


def chat(base, model, prompt, max_tokens):
    started = time.perf_counter()
    # Deliberately omit chat_template_kwargs to exercise the service default.
    with request(base, "/v1/chat/completions", {
        "model": model, "messages": [{"role": "user", "content": prompt}],
        "temperature": 0, "max_tokens": max_tokens, "stream": True,
        "stream_options": {"include_usage": True},
    }) as response:
        return read_stream(response, started)


def recall_prompt(base, model):
    repeats = 160
    for _ in range(6):
        prompt = (
            "Remember the access key: MAPLE-4827.\n"
            + "The depot keeps blue crates on shelf seven.\n" * repeats
            + "Reply with only the access key stated at the beginning."
        )
        with request(base, "/tokenize", {
            "model": model, "messages": [{"role": "user", "content": prompt}],
            "add_generation_prompt": True,
        }) as response:
            count = json.load(response)["count"]
        if 2200 <= count <= 2500:
            return prompt
        repeats = max(1, round(repeats * 2350 / count))
    raise ValueError("could not construct a 2.3K-token recall prompt")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8000")
    parser.add_argument("--model", required=True)
    args = parser.parse_args()
    base = args.base_url.rstrip("/")
    probes, failures = [], []
    cases = [
        ("arithmetic", "What is 17 + 25? Reply with only the integer.", "42"),
        ("extraction", 'Return only a JSON object with name and count from: name=Birch, count=7.', {"name": "Birch", "count": 7}),
        ("recall_2k", recall_prompt(base, args.model), "MAPLE-4827"),
    ]
    for name, prompt, expected in cases:
        result = chat(base, args.model, prompt, 96)
        answer = result["answer"]
        try:
            passed = (json.loads(answer) == expected) if isinstance(expected, dict) else answer == expected
        except ValueError:
            passed = False
        if not passed:
            failures.append(f"{name}: unexpected answer")
        if name == "recall_2k":
            if not 2200 <= result["prompt_tokens"] <= 2500:
                failures.append("recall prompt usage outside 2.3K envelope")
            if result["ttft_s"] >= 3:
                failures.append("recall TTFT >= 3 seconds")
        result.update(name=name, coherent=passed)
        probes.append(result)
        print(json.dumps(result), flush=True)

    # Novel generation avoids prompt-copy throughput inflation. Three repeats
    # use the same prompt; this canary's prefix cache is disabled.
    for i in range(3):
        result = chat(base, args.model,
                      "Write a detailed 400-word explanation of how rain forms, from evaporation to rainfall.", 256)
        if result["completion_tokens"] < 128 or result["decode_tps"] is None:
            failures.append(f"decode_{i}: too few tokens for a throughput sample")
        result["name"] = f"decode_{i}"
        probes.append(result)
        print(json.dumps(result), flush=True)
    speeds = [p["decode_tps"] for p in probes[-3:] if p["decode_tps"] is not None]
    median = statistics.median(speeds) if len(speeds) == 3 else None
    if median is None or median < 30:
        failures.append("median novel decode < 30 tokens/second")
    print(json.dumps({"model": args.model, "http_probe_verdict": "fail" if failures else "pass",
                      "median_decode_tps": median, "failures": failures}), flush=True)
    return 1 if failures else 0


if __name__ == "__main__":
    # Bound the entire in-pod run, including a peer that trickles SSE data
    # forever without hitting urllib's per-read timeout.
    signal.alarm(600)
    try:
        sys.exit(main())
    except Exception as exc:
        print(json.dumps({"http_probe_verdict": "fail", "error": str(exc)}), flush=True)
        sys.exit(1)
