#!/usr/bin/env python3
"""Bounded HTTP gauntlet for a llama.cpp lane (GGUF canaries such as Muse Glimmer).

The llama.cpp images carry no Python, so run this from the workstation against
a port-forward (or from any pod that can reach the lane Service):

    kubectl -n flexinfer-system port-forward svc/muse-glimmer-30b-canary 18080:8080 &
    python3 scripts/verify-llamacpp-canary.py \
        --base http://127.0.0.1:18080 --model muse-glimmer-30b-canary

Gates (all greedy, temperature 0):
  health      GET /health is 200
  coherence   4/4: capital of France, iterative fib, apples arithmetic, exact JSON
  tools       OpenAI tools round trip -> tool_calls[0] = get_weather {"city":"Paris"}
  tools_followup  second turn: the assistant tool_call (arguments as the OpenAI
              wire STRING) plus the tool result are replayed; the template must
              render them and the answer must quote the 18 C result with no new
              tool call (the 2026-10-20 Glimmer ATEM template raises when
              arguments reach it as a string, so this gates llama.cpp's
              requires_object_arguments handling too)
  restraint   same tools offered, "2+2" answered as 4 with no tool call
  decode      --decode-runs novel 256-token generations; tok/s from llama-server's
              `timings.predicted_per_second` (wall-clock fallback); when the
              server drafts (--spec-type), `timings.draft_n{,_accepted}` are
              reported as the acceptance rate

Exit 0 only when every gate passes. Image identity, startup logs, VRAM, restarts
and host health are separate operator gates; HTTP success alone cannot promote.
"""

import argparse
import json
import statistics
import sys
import time
import urllib.error
import urllib.request

TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "get_weather",
            "description": "Get the current weather for a city.",
            "parameters": {
                "type": "object",
                "properties": {"city": {"type": "string", "description": "City name"}},
                "required": ["city"],
            },
        },
    }
]


def post(base, path, payload, timeout):
    body = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        base + path,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    started = time.perf_counter()
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        data = json.loads(resp.read().decode("utf-8"))
    return data, time.perf_counter() - started


def chat(base, model, messages, timeout, max_tokens=768, tools=None):
    payload = {
        "model": model,
        "messages": messages,
        "temperature": 0,
        "max_tokens": max_tokens,
    }
    if tools:
        payload["tools"] = tools
        payload["tool_choice"] = "auto"
    data, elapsed = post(base, "/v1/chat/completions", payload, timeout)
    choice = data["choices"][0]
    message = choice.get("message") or {}
    return {
        "content": (message.get("content") or "").strip(),
        "reasoning": (message.get("reasoning_content") or "").strip(),
        "tool_calls": message.get("tool_calls") or [],
        "finish_reason": choice.get("finish_reason"),
        "usage": data.get("usage") or {},
        "timings": data.get("timings") or {},
        "elapsed_s": elapsed,
    }


def extract_json(text):
    start, end = text.find("{"), text.rfind("}")
    if start < 0 or end <= start:
        raise ValueError("no JSON object in content")
    return json.loads(text[start : end + 1])


def gate_health(base, timeout):
    with urllib.request.urlopen(base + "/health", timeout=timeout) as resp:
        return resp.status == 200, f"HTTP {resp.status}"


def gate_coherence(base, model, timeout):
    probes = [
        (
            "capital",
            "What is the capital of France? Answer with the city name only.",
            lambda c: "paris" in c.lower(),
        ),
        (
            "fib",
            "Write an iterative Python function fib(n) that returns the nth Fibonacci "
            "number. Output only the code.",
            lambda c: "def fib" in c and ("for " in c or "while " in c),
        ),
        (
            "apples",
            "I had 20 apples and gave 6 to a friend. How many apples do I have left? "
            "Reply with just the number.",
            lambda c: "14" in c,
        ),
        (
            "json",
            'Return a JSON object with the key "name" set to "Birch" and the key '
            '"count" set to 7. Output only the JSON.',
            lambda c: extract_json(c) == {"name": "Birch", "count": 7},
        ),
    ]
    results, passed = [], 0
    for name, prompt, check in probes:
        out = chat(base, model, [{"role": "user", "content": prompt}], timeout)
        try:
            ok = bool(check(out["content"]))
        except (ValueError, json.JSONDecodeError):
            ok = False
        passed += ok
        results.append(
            {
                "probe": name,
                "ok": ok,
                "content": out["content"][:200],
                "reasoning_chars": len(out["reasoning"]),
                "finish_reason": out["finish_reason"],
                "elapsed_s": round(out["elapsed_s"], 2),
            }
        )
    return passed == len(probes), {
        "passed": f"{passed}/{len(probes)}",
        "probes": results,
    }


def gate_tools(base, model, timeout):
    out = chat(
        base,
        model,
        [
            {
                "role": "user",
                "content": "What is the weather in Paris right now? Use the tool.",
            }
        ],
        timeout,
        tools=TOOLS,
    )
    calls = out["tool_calls"]
    ok, args = False, None
    if calls:
        fn = calls[0].get("function") or {}
        try:
            args = json.loads(fn.get("arguments") or "{}")
        except json.JSONDecodeError:
            args = fn.get("arguments")
        ok = (
            fn.get("name") == "get_weather"
            and isinstance(args, dict)
            and str(args.get("city", "")).lower().startswith("paris")
        )
    return ok, {
        "tool_calls": len(calls),
        "name": (calls[0].get("function") or {}).get("name") if calls else None,
        "arguments": args,
        "finish_reason": out["finish_reason"],
        "content": out["content"][:200],
        "elapsed_s": round(out["elapsed_s"], 2),
    }


def gate_tools_followup(base, model, timeout):
    """Replay a completed tool round and expect the model to use the result."""
    messages = [
        {
            "role": "user",
            "content": "What is the weather in Paris right now? Use the tool.",
        },
        {
            "role": "assistant",
            "content": None,
            "tool_calls": [
                {
                    "id": "call_paris_1",
                    "type": "function",
                    "function": {
                        "name": "get_weather",
                        # OpenAI wire format: arguments is a JSON *string*.
                        "arguments": json.dumps({"city": "Paris"}),
                    },
                }
            ],
        },
        {
            "role": "tool",
            "tool_call_id": "call_paris_1",
            "content": json.dumps(
                {"city": "Paris", "temperature_c": 18, "condition": "cloudy"}
            ),
        },
    ]
    out = chat(base, model, messages, timeout, tools=TOOLS)
    content = out["content"]
    ok = not out["tool_calls"] and "18" in content
    return ok, {
        "tool_calls": len(out["tool_calls"]),
        "mentions_18": "18" in content,
        "mentions_cloudy": "cloud" in content.lower(),
        "content": content[:200],
        "reasoning_chars": len(out["reasoning"]),
        "finish_reason": out["finish_reason"],
        "elapsed_s": round(out["elapsed_s"], 2),
    }


def gate_restraint(base, model, timeout):
    out = chat(
        base,
        model,
        [{"role": "user", "content": "What is 2+2? Reply with just the number."}],
        timeout,
        tools=TOOLS,
    )
    ok = not out["tool_calls"] and "4" in out["content"]
    return ok, {
        "tool_calls": len(out["tool_calls"]),
        "content": out["content"][:200],
        "finish_reason": out["finish_reason"],
        "elapsed_s": round(out["elapsed_s"], 2),
    }


def gate_decode(base, model, timeout, runs, max_tokens):
    prompt = (
        "Write a vivid story of about 400 words about a lighthouse keeper who finds a "
        "message in a bottle. Prose only, no title."
    )
    rates, drafted, accepted, details = [], 0, 0, []
    for _ in range(runs):
        out = chat(
            base,
            model,
            [{"role": "user", "content": prompt}],
            timeout,
            max_tokens=max_tokens,
        )
        t = out["timings"]
        n = t.get("predicted_n") or out["usage"].get("completion_tokens") or 0
        if t.get("predicted_per_second"):
            tps = float(t["predicted_per_second"])
        else:
            tps = n / out["elapsed_s"] if out["elapsed_s"] else 0.0
        rates.append(tps)
        drafted += int(t.get("draft_n") or 0)
        accepted += int(t.get("draft_n_accepted") or 0)
        details.append(
            {
                "predicted_n": n,
                "predicted_per_second": round(tps, 2),
                "prompt_n": t.get("prompt_n"),
                "prompt_per_second": round(float(t.get("prompt_per_second") or 0), 1),
                "finish_reason": out["finish_reason"],
            }
        )
    summary = {
        "runs": runs,
        "decode_tps_median": round(statistics.median(rates), 2) if rates else 0.0,
        "decode_tps_all": [round(r, 2) for r in rates],
        "details": details,
    }
    if drafted:
        summary["draft_n"] = drafted
        summary["draft_n_accepted"] = accepted
        summary["draft_acceptance"] = round(accepted / drafted, 3)
    return bool(rates) and min(rates) > 0, summary


def main():
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument(
        "--base", default="http://127.0.0.1:18080", help="llama-server base URL"
    )
    ap.add_argument("--model", required=True, help="model name sent in requests")
    ap.add_argument(
        "--timeout", type=float, default=600.0, help="per-request timeout (s)"
    )
    ap.add_argument("--decode-runs", type=int, default=3)
    ap.add_argument("--decode-tokens", type=int, default=256)
    ap.add_argument(
        "--skip-tools", action="store_true", help="skip the tools and restraint gates"
    )
    ap.add_argument("--json", dest="json_out", help="write the full report here")
    args = ap.parse_args()

    report, all_ok = {"base": args.base, "model": args.model, "gates": {}}, True
    gates = [("health", lambda: gate_health(args.base, args.timeout))]
    gates.append(
        ("coherence", lambda: gate_coherence(args.base, args.model, args.timeout))
    )
    if not args.skip_tools:
        gates.append(("tools", lambda: gate_tools(args.base, args.model, args.timeout)))
        gates.append(
            (
                "tools_followup",
                lambda: gate_tools_followup(args.base, args.model, args.timeout),
            )
        )
        gates.append(
            ("restraint", lambda: gate_restraint(args.base, args.model, args.timeout))
        )
    gates.append(
        (
            "decode",
            lambda: gate_decode(
                args.base,
                args.model,
                args.timeout,
                args.decode_runs,
                args.decode_tokens,
            ),
        )
    )
    for name, fn in gates:
        try:
            ok, detail = fn()
        except (urllib.error.URLError, TimeoutError, KeyError, ValueError) as exc:
            ok, detail = False, {"error": f"{type(exc).__name__}: {exc}"}
        report["gates"][name] = {
            "ok": ok,
            **(detail if isinstance(detail, dict) else {"detail": detail}),
        }
        all_ok &= ok
        print(
            f"[{'PASS' if ok else 'FAIL'}] {name}: {json.dumps(detail)[:400]}",
            flush=True,
        )

    report["ok"] = all_ok
    if args.json_out:
        with open(args.json_out, "w", encoding="utf-8") as fh:
            json.dump(report, fh, indent=2)
    print("RESULT:", "PASS" if all_ok else "FAIL")
    return 0 if all_ok else 1


if __name__ == "__main__":
    sys.exit(main())
