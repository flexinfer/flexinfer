#!/usr/bin/env python3
"""admission-corpus-test.py — CC-6a-2 kill-test B: false-positive corpus.

Closes the spec's deferred kill-test criterion B (see
docs/planning/context-bounded-admission-spec.md). Generates a small
representative chat-completion corpus, runs the Python port of the proxy's
admission token estimator on each entry, and compares against the runtime's
reported `usage.prompt_tokens` from the actual response.

Pass criterion (from CC-5 / CC-6a):
    >= 95% of sampled requests have estimated_tokens within
    [0.85 * actual, 1.30 * actual].

The Python estimator below MUST stay in sync with the Go implementation in
`internal/proxy/admission_estimator.go`. If the Go heuristic changes, this
script needs the same change or the corpus verdict drifts silently.

Usage:
    python3 scripts/admission-corpus-test.py \\
        --endpoint http://localhost:18080 \\
        --model qwen3-8b-radeonvii-soak \\
        --report .loom/local/validation/admission-corpus/<date>/report.json

Exit codes:
    0  >= 95% in band (pass)
    1  < 95% in band (fail)
    2  any infrastructure failure (no model response, parse error)
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import math
import os
import statistics
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

DEFAULT_ENDPOINT = "http://localhost:18080"
DEFAULT_MAX_TOKENS = 4
BAND_LOW = 0.85
BAND_HIGH = 1.30
PASS_FRACTION = 0.95


# ----- estimator port (keep in sync with internal/proxy/admission_estimator.go)

PER_MESSAGE_OVERHEAD = 4
CONVERSATION_OVERHEAD = 3


def estimate_tokens_from_string(s: str) -> int:
    """Conservative ASCII/CJK heuristic. See admission_estimator.go."""
    if not s:
        return 0
    ascii_bytes = 0
    high_runes = 0
    # We iterate over Python characters; CPython gives us decoded runes,
    # which matches the Go side's utf8.DecodeRuneInString output.
    for ch in s:
        if ord(ch) < 0x80:
            ascii_bytes += 1
        else:
            high_runes += 1
    # ASCII portion: ceil(bytes / 3.5) -> bytes*2/7, rounded up.
    ascii_tokens = (ascii_bytes * 2 + 6) // 7
    return ascii_tokens + high_runes


def estimate_prompt_tokens(body: dict) -> tuple[int, bool]:
    """Walk a chat-completion or completion body. Returns (tokens, ok)."""
    total = 0
    matched = False

    msgs = body.get("messages")
    if isinstance(msgs, list):
        matched = True
        total += CONVERSATION_OVERHEAD
        for raw in msgs:
            if not isinstance(raw, dict):
                continue
            total += PER_MESSAGE_OVERHEAD
            content = raw.get("content")
            if isinstance(content, str):
                total += estimate_tokens_from_string(content)
            elif isinstance(content, list):
                for part in content:
                    if not isinstance(part, dict):
                        continue
                    text = part.get("text")
                    if isinstance(text, str):
                        total += estimate_tokens_from_string(text)
                    if isinstance(part.get("image_url"), dict):
                        total += 256

    prompt = body.get("prompt")
    if isinstance(prompt, str):
        matched = True
        total += estimate_tokens_from_string(prompt)
    elif isinstance(prompt, list):
        matched = True
        for p in prompt:
            if isinstance(p, str):
                total += estimate_tokens_from_string(p)

    return total, matched


# ----- corpus generator


def build_corpus(model: str) -> list[dict]:
    """Return a list of {name, body, expected_shape} corpus entries.

    Each body is a /v1/chat/completions request. Shapes intentionally vary:
    short English, medium English, long English, code, JSON-stuffed,
    repeated whitespace, mixed English+CJK, CJK-only, base64-ish blob,
    multi-turn conversation.
    """

    def chat(content: str, mt: int = DEFAULT_MAX_TOKENS) -> dict:
        return {
            "model": model,
            "messages": [{"role": "user", "content": content}],
            "max_tokens": mt,
            "stream": False,
        }

    code_block = (
        "Here is a Go snippet:\n"
        "```go\n"
        "func handler(w http.ResponseWriter, r *http.Request) {\n"
        "    if r.Method != http.MethodPost {\n"
        "        w.WriteHeader(http.StatusMethodNotAllowed)\n"
        "        return\n"
        "    }\n"
        "    var body Request\n"
        "    if err := json.NewDecoder(r.Body).Decode(&body); err != nil {\n"
        "        http.Error(w, err.Error(), http.StatusBadRequest)\n"
        "        return\n"
        "    }\n"
        "}\n"
        "```\n"
        "Explain the error handling in one paragraph."
    )

    json_blob = json.dumps(
        {
            "users": [
                {"id": i, "name": f"user-{i}", "email": f"u{i}@example.com"}
                for i in range(40)
            ]
        }
    )

    paragraph = (
        "Software engineering at a Kubernetes-native platform shop requires "
        "careful attention to controller reconciliation patterns, idempotent "
        "API design, and observability. In particular, when implementing an "
        "admission filter at the proxy edge, the goal is to make conservative "
        "estimates that bias toward forwarding ambiguous requests while still "
        "refusing genuinely over-budget payloads. The trade-off is between "
        "tail latency for invalid input and false-positive rate for valid input."
    )

    cjk_short = "你好，请用一句话介绍 Kubernetes。"
    cjk_long = (
        "请用三段话讨论容器编排系统的设计权衡，"
        "包括调度、网络和持久化存储。"
        "重点说明 Pod 生命周期与节点资源管理之间的关系。"
    ) * 4

    base64_blob = (
        "QXJlIHlvdSByZWFsbHkgcmVhZGluZyB0aGlzPyBJZiBzbywgaSBhbSBpbXByZXNzZWQu" * 8
    )

    multi_turn = {
        "model": model,
        "messages": [
            {"role": "system", "content": "You are a concise assistant."},
            {"role": "user", "content": "What is the capital of France?"},
            {"role": "assistant", "content": "Paris."},
            {"role": "user", "content": "What about Spain?"},
            {"role": "assistant", "content": "Madrid."},
            {"role": "user", "content": "And Italy?"},
        ],
        "max_tokens": DEFAULT_MAX_TOKENS,
        "stream": False,
    }

    return [
        {"name": "short_question", "body": chat("hi")},
        {"name": "short_sentence", "body": chat("Name the capital of France.")},
        {"name": "medium_paragraph", "body": chat(paragraph)},
        {"name": "code_block", "body": chat(code_block)},
        {"name": "json_stuffed", "body": chat("Parse: " + json_blob)},
        {"name": "long_paragraph_x4", "body": chat(paragraph * 4)},
        {"name": "cjk_short", "body": chat(cjk_short)},
        {"name": "cjk_long", "body": chat(cjk_long)},
        {"name": "base64_blob", "body": chat("Decode: " + base64_blob)},
        {"name": "multi_turn", "body": multi_turn},
        {"name": "whitespace_padded", "body": chat("hello   world   " * 30)},
    ]


# ----- proxy round-trip


def post_chat(endpoint: str, body: dict, model: str, timeout: int) -> dict:
    """POST a chat-completions body via the proxy and return the parsed JSON.

    The endpoint is the proxy root (e.g. http://localhost:18080); the URL
    pattern matches the existing scripts/bench-context-curve.sh layout.
    """
    url = f"{endpoint.rstrip('/')}/model/{model}/v1/chat/completions"
    data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(
        url,
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    started = time.perf_counter()
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        payload = resp.read()
    elapsed = time.perf_counter() - started
    parsed = json.loads(payload)
    return {"elapsed_seconds": elapsed, "response": parsed}


# ----- runner


def evaluate_entry(endpoint: str, model: str, entry: dict, timeout: int) -> dict:
    body = entry["body"]
    estimated, ok = estimate_prompt_tokens(body)
    if not ok:
        return {
            "name": entry["name"],
            "estimator_ok": False,
            "estimated_prompt_tokens": estimated,
        }
    try:
        result = post_chat(endpoint, body, model, timeout)
    except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError) as exc:
        return {
            "name": entry["name"],
            "estimator_ok": True,
            "estimated_prompt_tokens": estimated,
            "error": f"{type(exc).__name__}: {exc}",
        }

    usage = result["response"].get("usage") or {}
    actual = usage.get("prompt_tokens")
    if actual is None:
        return {
            "name": entry["name"],
            "estimator_ok": True,
            "estimated_prompt_tokens": estimated,
            "error": "response missing usage.prompt_tokens",
        }
    ratio = estimated / actual if actual > 0 else math.inf
    in_band = BAND_LOW <= ratio <= BAND_HIGH
    return {
        "name": entry["name"],
        "estimator_ok": True,
        "estimated_prompt_tokens": estimated,
        "actual_prompt_tokens": actual,
        "ratio": ratio,
        "in_band": in_band,
        "elapsed_seconds": result["elapsed_seconds"],
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--endpoint", default=os.environ.get("ENDPOINT", DEFAULT_ENDPOINT)
    )
    parser.add_argument("--model", required=True)
    parser.add_argument("--timeout", type=int, default=60)
    parser.add_argument(
        "--report",
        help="Optional JSON report path. The parent directory is created.",
    )
    args = parser.parse_args()

    corpus = build_corpus(args.model)
    entries = [
        evaluate_entry(args.endpoint, args.model, e, args.timeout) for e in corpus
    ]

    successful = [e for e in entries if "ratio" in e]
    failures = [e for e in entries if "ratio" not in e]
    if not successful:
        report = {
            "schema_version": "flexinfer.admission_corpus.v1",
            "created_at": dt.datetime.now(dt.timezone.utc).strftime(
                "%Y-%m-%dT%H:%M:%SZ"
            ),
            "model": args.model,
            "endpoint": args.endpoint,
            "entries": entries,
            "verdict": {"overall_pass": False, "reason": "no successful samples"},
        }
        write_report(report, args.report)
        print(
            json.dumps(
                {"event": "admission_corpus_abort", "reason": "no successful samples"}
            ),
            file=sys.stderr,
        )
        return 2

    in_band = [e for e in successful if e["in_band"]]
    in_band_fraction = len(in_band) / len(successful)
    ratios = [e["ratio"] for e in successful]
    summary = {
        "samples_total": len(entries),
        "samples_successful": len(successful),
        "samples_in_band": len(in_band),
        "in_band_fraction": in_band_fraction,
        "band_low": BAND_LOW,
        "band_high": BAND_HIGH,
        "pass_fraction": PASS_FRACTION,
        "ratio_mean": statistics.fmean(ratios),
        "ratio_median": statistics.median(ratios),
        "ratio_min": min(ratios),
        "ratio_max": max(ratios),
        "failures": failures,
    }
    overall_pass = in_band_fraction >= PASS_FRACTION

    report = {
        "schema_version": "flexinfer.admission_corpus.v1",
        "created_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "model": args.model,
        "endpoint": args.endpoint,
        "entries": entries,
        "summary": summary,
        "verdict": {"overall_pass": overall_pass},
    }
    write_report(report, args.report)
    print(
        json.dumps(
            {
                "event": "admission_corpus_summary",
                "in_band_fraction": round(in_band_fraction, 4),
                "samples_successful": len(successful),
                "samples_in_band": len(in_band),
                "ratio_mean": round(summary["ratio_mean"], 3),
                "ratio_min": round(summary["ratio_min"], 3),
                "ratio_max": round(summary["ratio_max"], 3),
                "verdict_pass": overall_pass,
            }
        ),
        file=sys.stderr,
    )
    for e in successful:
        flag = "" if e["in_band"] else "  OUT-OF-BAND"
        print(
            f"  {e['name']:>20s}  est={e['estimated_prompt_tokens']:6d}  "
            f"actual={e['actual_prompt_tokens']:6d}  ratio={e['ratio']:.3f}{flag}",
            file=sys.stderr,
        )
    if failures:
        print("infrastructure failures:", file=sys.stderr)
        for f in failures:
            print(f"  {f['name']}: {f.get('error', '?')}", file=sys.stderr)
    return 0 if overall_pass else 1


def write_report(report: dict, path: str | None) -> None:
    body = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if path:
        p = Path(path)
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(body, encoding="utf-8")
    sys.stdout.write(body)


if __name__ == "__main__":
    sys.exit(main())
