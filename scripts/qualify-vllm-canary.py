#!/usr/bin/env python3
"""Bounded extended HTTP qualification for an isolated two-sequence vLLM pod.

Copy this script and verify-vllm-canary.py into the same directory in the pod.
Run with --model NAME; --phase selects context, tools, load, or all (default).
This is synthetic 64K/two-request evidence, not a 128K or LoRA certificate.
"""

import argparse
import concurrent.futures
import importlib.util
import json
import math
from pathlib import Path
import signal
import statistics
import threading
import time
import urllib.request
import uuid

spec = importlib.util.spec_from_file_location(
    "smoke", Path(__file__).with_name("verify-vllm-canary.py"))
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


def emit(name, **result):
    print(json.dumps({"name": name, **result}), flush=True)


def require(condition, message):
    if not condition:
        raise ValueError(message)


class Client:
    def __init__(self, base, model):
        self.base = base.rstrip("/")
        self.model = model

    def request(self, path, payload=None, timeout=90):
        body = None if payload is None else json.dumps(payload).encode()
        return urllib.request.urlopen(urllib.request.Request(
            self.base + path, data=body,
            headers={"Content-Type": "application/json"}), timeout=timeout)

    def payload(self, messages, max_tokens=128, **extra):
        # Exercise the server's non-thinking default without request overrides.
        return {"model": self.model, "messages": messages, "temperature": 0,
                "max_tokens": max_tokens, **extra}

    def chat(self, messages, max_tokens=128, timeout=90, **extra):
        started = time.perf_counter()
        with self.request("/v1/chat/completions", self.payload(
                messages, max_tokens, stream=True,
                stream_options={"include_usage": True}, **extra), timeout=timeout) as response:
            return smoke.read_stream(response, started)

    def tokenize(self, messages):
        with self.request("/tokenize", {"model": self.model, "messages": messages,
                                       "add_generation_prompt": True}) as response:
            return json.load(response)["count"]

    def metrics(self):
        with self.request("/metrics") as response:
            return parse_metrics(response.read().decode())


def parse_metrics(text):
    names = {"running": "vllm:num_requests_running",
             "waiting": "vllm:num_requests_waiting",
             "kv": "vllm:kv_cache_usage_perc"}
    result = {}
    for line in text.splitlines():
        if not line or line.startswith("#"):
            continue
        name = line.split("{", 1)[0].split()[0]
        for key, expected in names.items():
            if name == expected:
                value = float(line.split()[-1])
                require(math.isfinite(value) and value >= 0, "invalid metric value")
                result[key] = result.get(key, 0) + value
    require(result.keys() == names.keys(), "missing request/KV metrics")
    return result


def idle(client, timeout=20):
    deadline = time.monotonic() + timeout
    while True:
        values = client.metrics()
        if values["running"] == values["waiting"] == 0 and values["kv"] <= 0.005:
            return values
        require(time.monotonic() < deadline, f"requests/KV did not drain: {values}")
        time.sleep(0.5)


def user(text):
    return [{"role": "user", "content": text}]


def build_context(lines, values):
    filler = [f"Record {i}: depot telemetry is nominal; blue crates remain on shelf seven.\n"
              for i in range(lines)]
    for depth, (label, value) in reversed(list(zip((0, 0.5, 1), values.items()))):
        filler.insert(round(lines * depth), f"The {label} vault key is {value}.\n")
    return user("".join(filler) + "\nReturn only a JSON object mapping ALPHA, BRAVO, "
                "and CHARLIE to the three vault keys stated above.")


def calibrated_context(client, context_limit, values):
    # Reserve 128 output tokens and 64 tokens of tokenizer boundary slack.
    target = context_limit - 192
    lines = round(target / 20)
    for _ in range(8):
        messages = build_context(lines, values)
        count = client.tokenize(messages)
        if target - 128 <= count <= target + 64:
            return messages, count
        require(count > 0, "invalid tokenizer count")
        lines = max(1, round(lines * target / count))
    raise ValueError("could not calibrate a near-limit prompt")


def check_context(result, expected, count, limit):
    require(result["prompt_tokens"] == count, "tokenize/chat usage mismatch")
    require(limit - 320 <= count <= limit - 128, "prompt outside context envelope")
    require(result["finish_reason"] == "stop", "context answer was truncated")
    require(json.loads(result["answer"]) == expected, "long-context key mismatch")


def context_gate(client, limit):
    # Independent random keys cannot be inferred from labels or the question.
    values = {label: uuid.uuid4().hex[:12] for label in ("ALPHA", "BRAVO", "CHARLIE")}
    messages, count = calibrated_context(client, limit, values)
    emit("context_start", context_limit=limit, prompt_tokens=count, depths=[0, 50, 100])
    # The established dense-27B baseline needs ~300 s for 60K prefill.
    # Use its documented 900 s context budget; short requests stay bounded at 90 s.
    # Require the API's JSON contract; a prompt alone may produce Markdown fences.
    result = client.chat(messages, timeout=900, response_format={"type": "json_object"})
    emit("context_result", **result, expected=values)
    check_context(result, values, count, limit)
    idle(client)
    emit("context_gate", verdict="pass", recalled=3, prompt_tokens=count)


TOOL = {"type": "function", "function": {
    "name": "lookup_depot", "description": "Look up a depot's current crate count.",
    "parameters": {"type": "object", "properties": {"depot": {"type": "string"}},
                   "required": ["depot"], "additionalProperties": False}}}


def check_tool(response):
    choice = response["choices"][0]
    message = choice["message"]
    require(choice["finish_reason"] == "tool_calls", "missing tool_calls finish reason")
    require(not (message.get("reasoning") or message.get("reasoning_content"))
            and "<think>" not in (message.get("content") or ""),
            "tool request emitted reasoning")
    calls = message.get("tool_calls", [])
    require(len(calls) == 1, "expected exactly one tool call")
    call = calls[0]
    require(call.get("id") and call.get("type") == "function", "invalid tool call ID/type")
    require(call["function"]["name"] == "lookup_depot", "wrong tool name")
    require(json.loads(call["function"]["arguments"]) == {"depot": "Birch"},
            "wrong tool arguments")
    return message, call["id"]


def tools_gate(client):
    for mode in ("auto", "required"):
        messages = user("Use lookup_depot to find the current crate count at Birch. "
                        "Do not invent the count; call the tool first.")
        with client.request("/v1/chat/completions", client.payload(
                messages, tools=[TOOL], tool_choice=mode)) as response:
            result = json.load(response)
        emit("tool_call", mode=mode, response=result)
        message, call_id = check_tool(result)
        count = int(uuid.uuid4().hex[:4], 16)
        messages.extend([message, {"role": "tool", "tool_call_id": call_id,
                                  "content": json.dumps({"depot": "Birch", "count": count})},
                         {"role": "user", "content": "Reply with only the integer count "
                          "returned by the tool."}])
        answer = client.chat(messages, tools=[TOOL], tool_choice="none")
        emit("tool_roundtrip", mode=mode, expected=count, **answer)
        require(answer["answer"] == str(count), "tool result was not used")
    idle(client)
    emit("tools_gate", verdict="pass", roundtrips=2)


def cancel_one(client):
    idle(client)
    started, release = threading.Event(), threading.Event()

    def generate():
        with client.request("/v1/chat/completions", client.payload(
                user("Write a detailed essay about weather and keep writing."),
                max_tokens=4096, stream=True, ignore_eos=True)) as response:
            for raw in response:
                if not raw.startswith(b"data:"):
                    continue
                data = raw[5:].strip()
                require(data != b"[DONE]", "generation finished before cancellation")
                event = json.loads(data)
                require(not event.get("error"), f"cancellation stream error: {event}")
                if any(c.get("delta", {}).get("content") for c in event.get("choices", [])):
                    started.set()
                    require(release.wait(20), "cancellation release timed out")
                    return  # Close the socket while the server is still generating.
        raise ValueError("cancellation stream had no content")

    with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
        future = pool.submit(generate)
        try:
            require(started.wait(30), "cancellation request produced no content")
            deadline = time.monotonic() + 15
            while True:
                active = client.metrics()
                if active["running"] >= 1 and active["kv"] > 0:
                    break
                require(time.monotonic() < deadline, "no active request/KV before cancel")
                time.sleep(0.5)
        finally:
            release.set()
        future.result(timeout=5)
    closed = time.monotonic()
    drained = idle(client)
    drain_s = time.monotonic() - closed
    recovery = client.chat(user("What is 17 + 25? Reply with only the integer."), 32)
    require(recovery["answer"] == "42", "engine did not recover after cancellation")
    emit("cancel_result", active=active, drained=drained,
         drain_s=round(drain_s, 3), recovery=recovery)


def load_gate(client, seconds):
    idle(client)
    for _ in range(3):
        cancel_one(client)
    begun = time.monotonic()
    pairs, peak, samples = 0, 0, []
    prompts = ["Write a detailed 400-word explanation of how rain forms.",
               "Write a detailed 400-word explanation of how snow forms."]
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        while time.monotonic() - begun < seconds:
            barrier = threading.Barrier(2, timeout=10)

            def generate(prompt):
                barrier.wait()
                return client.chat(user(prompt), 256)

            futures = [pool.submit(generate, prompt) for prompt in prompts]
            while not all(f.done() for f in futures):
                current = client.metrics()
                peak = max(peak, current["running"])
                time.sleep(0.5)
            replies = [f.result() for f in futures]
            for reply in replies:
                require(reply["completion_tokens"] >= 128 and reply["decode_tps"] is not None,
                        "short/unmeasurable concurrent generation")
                samples.append(reply["decode_tps"])
            pairs += 1
            # Keep logs compact: all timings/tokens, with only an answer preview.
            emit("load_pair", pair=pairs, elapsed_s=round(time.monotonic() - begun, 2),
                 peak_running=peak, replies=[{**r, "answer": r["answer"][:80]} for r in replies])
    drained = idle(client)
    require(peak >= 2, "two active requests were never observed")
    require(pairs >= 2, "insufficient concurrent samples")
    emit("load_gate", verdict="pass", pairs=pairs, requests=pairs * 2,
         duration_s=round(time.monotonic() - begun, 2), peak_running=peak,
         median_per_request_decode_tps=statistics.median(samples), drained=drained,
         cancellations=3)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8000")
    parser.add_argument("--model", required=True)
    parser.add_argument("--phase", choices=("all", "context", "tools", "load"), default="all")
    parser.add_argument("--context-limit", type=int, default=65536)
    parser.add_argument("--soak-seconds", type=int, default=300)
    args = parser.parse_args()
    require(600 <= args.context_limit <= 65536, "context limit must be between 600 and 65536")
    require(30 <= args.soak_seconds <= 600, "soak must be between 30 and 600 seconds")
    client = Client(args.base_url, args.model)
    idle(client)
    phases = ("context", "tools", "load") if args.phase == "all" else (args.phase,)
    failures = []
    for phase in phases:
        try:
            if phase == "context":
                context_gate(client, args.context_limit)
            elif phase == "tools":
                tools_gate(client)
            else:
                load_gate(client, args.soak_seconds)
        except Exception as exc:
            failures.append({"phase": phase, "error": str(exc)})
            emit(phase + "_gate", verdict="fail", error=str(exc))
            # Continue only if the engine recovered and released request state.
            idle(client)
    emit("qualification", verdict="fail" if failures else "pass", phases=phases,
         context_limit=args.context_limit, soak_seconds=args.soak_seconds, failures=failures)
    return 1 if failures else 0


if __name__ == "__main__":
    # Includes all worker threads and a peer that trickles SSE data indefinitely.
    signal.alarm(1800)
    try:
        raise SystemExit(main())
    except Exception as exc:
        emit("qualification", verdict="fail", error=str(exc))
        raise SystemExit(1)
