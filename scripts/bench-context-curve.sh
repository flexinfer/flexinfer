#!/usr/bin/env bash
#
# bench-context-curve.sh - Reporting-only long-context benchmark curve runner.
#
# The runner measures each requested context point independently through the
# FlexInfer proxy's OpenAI-compatible /v1/chat/completions endpoint. It emits one
# JSON report with a stable context_curve.points[] shape and keeps earlier
# successful points when later, larger points fail.
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLEXINFER_ROOT="$ROOT_DIR" exec python3 - "$@" <<'PY'
import argparse
import json
import os
import random
import subprocess
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path


DEFAULT_POINTS = "2048,8192,32768,131072"
DEFAULT_MODEL = "qwen3-14b-claude-distill"
DEFAULT_ENDPOINT = "http://localhost:8080"


def parse_points(raw):
    points = []
    for part in raw.split(","):
        part = part.strip().lower()
        if not part:
            continue
        multiplier = 1
        if part.endswith("k"):
            multiplier = 1024
            part = part[:-1]
        try:
            value = int(float(part) * multiplier)
        except ValueError as exc:
            raise argparse.ArgumentTypeError(f"invalid context point: {part!r}") from exc
        if value <= 0:
            raise argparse.ArgumentTypeError("context points must be positive")
        points.append(value)
    if not points:
        raise argparse.ArgumentTypeError("at least one context point is required")
    return points


def git_sha():
    root = Path(os.environ.get("FLEXINFER_ROOT", ".")).resolve()
    try:
        return subprocess.check_output(
            ["git", "-C", str(root), "rev-parse", "--short", "HEAD"],
            stderr=subprocess.DEVNULL,
            text=True,
        ).strip()
    except Exception:
        return "unknown"


def now_iso():
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def build_api_url(endpoint, model, direct):
    base = endpoint.rstrip("/")
    if direct:
        return f"{base}/v1/chat/completions"
    return f"{base}/model/{model}/v1/chat/completions"


def slugify(value):
    safe = []
    for char in value:
        if char.isalnum() or char in "._-":
            safe.append(char)
        else:
            safe.append("-")
    slug = "".join(safe).strip("-")
    return slug or "model"


def prompt_for_context(target_tokens):
    # This is intentionally approximate. The MVP needs stable pressure points,
    # not tokenizer-perfect accounting; actual prompt token counts come from the
    # API response when the backend provides them.
    header = (
        "You are running a FlexInfer context-curve benchmark. "
        "Read the repeated context and answer the final instruction briefly.\n\n"
    )
    unit = (
        "Context marker alpha beta gamma delta. "
        "The benchmark color is blue and the checksum word is horizon. "
    )
    target_words = max(32, int(target_tokens * 0.75))
    unit_words = unit.split()
    repeats = max(1, (target_words + len(unit_words) - 1) // len(unit_words))
    body = " ".join([unit] * repeats)
    tail = (
        "\n\nFinal instruction: reply with one concise sentence containing the "
        "words blue and horizon."
    )
    return header + body + tail


def run_vram_command(command):
    if not command:
        return {"available": False, "value_mb": None, "raw": "", "error": ""}
    try:
        completed = subprocess.run(
            command,
            shell=True,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=30,
            check=False,
        )
    except Exception as exc:
        return {"available": False, "value_mb": None, "raw": "", "error": repr(exc)}

    raw = completed.stdout.strip()
    if completed.returncode != 0:
        return {
            "available": False,
            "value_mb": None,
            "raw": raw,
            "error": completed.stderr.strip() or f"exit {completed.returncode}",
        }
    try:
        value = float(raw.split()[0])
    except Exception:
        return {
            "available": False,
            "value_mb": None,
            "raw": raw,
            "error": "stdout did not start with a numeric MB value",
        }
    return {"available": True, "value_mb": value, "raw": raw, "error": ""}


def percentile(values, pct):
    if not values:
        return None
    ordered = sorted(values)
    index = int(round((len(ordered) - 1) * pct))
    return ordered[index]


def request_once(api_url, model, prompt, max_tokens, timeout):
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": max_tokens,
        "temperature": 0,
    }
    body = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        api_url,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    started = time.monotonic()
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        raw = resp.read()
        elapsed = time.monotonic() - started
        status = getattr(resp, "status", 200)
    data = json.loads(raw)
    usage = data.get("usage") or {}
    choices = data.get("choices") or []
    content = ""
    if choices:
        content = (choices[0].get("message") or {}).get("content") or choices[0].get("text") or ""
    prompt_tokens = int(usage.get("prompt_tokens") or 0)
    completion_tokens = int(usage.get("completion_tokens") or 0)
    if prompt_tokens <= 0:
        prompt_tokens = max(1, len(prompt.split()))
    if completion_tokens <= 0:
        completion_tokens = max(1, len(content.split()))
    return {
        "status_code": status,
        "elapsed_seconds": elapsed,
        "prompt_tokens": prompt_tokens,
        "completion_tokens": completion_tokens,
        "prefill_tokens_per_second": prompt_tokens / elapsed if elapsed > 0 else None,
        "decode_tokens_per_second": completion_tokens / elapsed if elapsed > 0 else None,
        "model_returned": data.get("model", ""),
        "content_preview": content[:160],
    }


def benchmark_point(args, api_url, point):
    record = {
        "context_tokens_target": point,
        "status": "pending",
        "attempted": False,
        "reason": "",
        "iterations": args.iterations,
        "warmup": args.warmup,
        "max_tokens": args.max_tokens,
        "timeout_seconds": args.timeout,
        "prompt_tokens_observed": None,
        "completion_tokens_observed": None,
        "elapsed_seconds_avg": None,
        "elapsed_seconds_p95": None,
        "prefill_tokens_per_second_avg": None,
        "decode_tokens_per_second_avg": None,
        "free_vram_before_mb": None,
        "free_vram_after_mb": None,
        "vram_sample_error": "",
        "samples": [],
    }

    if args.dry_run:
        record.update({"status": "skip", "reason": "dry_run"})
        return record

    prompt = prompt_for_context(point)
    before = run_vram_command(args.vram_command)
    record["free_vram_before_mb"] = before["value_mb"]
    if before["error"]:
        record["vram_sample_error"] = before["error"]

    failures = []
    samples = []
    total_runs = args.warmup + args.iterations
    for index in range(total_runs):
        sample = {
            "sample_index": index + 1,
            "warmup": index < args.warmup,
            "ok": False,
            "error": "",
        }
        record["attempted"] = True
        try:
            sample.update(request_once(api_url, args.model, prompt, args.max_tokens, args.timeout))
            sample["ok"] = True
        except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError, json.JSONDecodeError, OSError) as exc:
            sample["error"] = repr(exc)
            failures.append(sample["error"])
        print(json.dumps({
            "event": "context_curve_sample",
            "context_tokens_target": point,
            **sample,
        }), flush=True)
        if sample["ok"] and not sample["warmup"]:
            samples.append(sample)

    after = run_vram_command(args.vram_command)
    record["free_vram_after_mb"] = after["value_mb"]
    if after["error"] and not record["vram_sample_error"]:
        record["vram_sample_error"] = after["error"]

    record["samples"] = samples
    if not samples:
        record["status"] = "fail"
        record["reason"] = failures[-1] if failures else "no measured samples"
        return record

    elapsed = [float(sample["elapsed_seconds"]) for sample in samples]
    prefill = [float(sample["prefill_tokens_per_second"]) for sample in samples if sample["prefill_tokens_per_second"] is not None]
    decode = [float(sample["decode_tokens_per_second"]) for sample in samples if sample["decode_tokens_per_second"] is not None]
    record.update({
        "status": "pass",
        "reason": "",
        "prompt_tokens_observed": samples[-1]["prompt_tokens"],
        "completion_tokens_observed": samples[-1]["completion_tokens"],
        "elapsed_seconds_avg": sum(elapsed) / len(elapsed),
        "elapsed_seconds_p95": percentile(elapsed, 0.95),
        "prefill_tokens_per_second_avg": sum(prefill) / len(prefill) if prefill else None,
        "decode_tokens_per_second_avg": sum(decode) / len(decode) if decode else None,
    })
    if failures:
        record["reason"] = f"{len(failures)} warmup or attempted sample failure(s) before pass"
    return record


def rounded(value):
    if isinstance(value, float):
        return round(value, 6)
    if isinstance(value, list):
        return [rounded(item) for item in value]
    if isinstance(value, dict):
        return {key: rounded(item) for key, item in value.items()}
    return value


def main():
    parser = argparse.ArgumentParser(
        description="Run a reporting-only FlexInfer long-context benchmark curve.",
    )
    parser.add_argument("--model", default=os.environ.get("MODEL", DEFAULT_MODEL))
    parser.add_argument("--endpoint", default=os.environ.get("ENDPOINT", DEFAULT_ENDPOINT))
    parser.add_argument("--points", type=parse_points, default=parse_points(os.environ.get("POINTS", DEFAULT_POINTS)))
    parser.add_argument("--max-tokens", type=int, default=int(os.environ.get("MAX_TOKENS", "64")))
    parser.add_argument("--iterations", type=int, default=int(os.environ.get("ITERATIONS", "1")))
    parser.add_argument("--warmup", type=int, default=int(os.environ.get("WARMUP", "0")))
    parser.add_argument("--timeout", type=int, default=int(os.environ.get("TIMEOUT", "300")))
    parser.add_argument("--report-dir", default=os.environ.get("REPORT_DIR", "/tmp"))
    parser.add_argument("--vram-command", default=os.environ.get("VRAM_COMMAND", ""))
    parser.add_argument("--direct", action="store_true", default=os.environ.get("DIRECT", "0") == "1")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    if args.max_tokens <= 0:
        parser.error("--max-tokens must be positive")
    if args.iterations <= 0:
        parser.error("--iterations must be positive")
    if args.warmup < 0:
        parser.error("--warmup cannot be negative")
    if args.timeout <= 0:
        parser.error("--timeout must be positive")

    report_root = Path(args.report_dir)
    report_root.mkdir(parents=True, exist_ok=True)
    run_id = f"context-curve-{time.strftime('%Y%m%dT%H%M%S')}-{random.randrange(16**6):06x}"
    report_path = report_root / f"bench-context-curve-{slugify(args.model)}-{run_id}.json"
    api_url = build_api_url(args.endpoint, args.model, args.direct)

    points = []
    started = now_iso()
    for point in args.points:
        points.append(benchmark_point(args, api_url, point))

    report = {
        "schema_version": "flexinfer.context_curve.v1",
        "run_id": run_id,
        "git_sha": git_sha(),
        "created_at": started,
        "completed_at": now_iso(),
        "model": args.model,
        "endpoint": args.endpoint,
        "api_url": api_url,
        "direct": args.direct,
        "dry_run": args.dry_run,
        "context_curve": {
            "points": points,
            "summary": {
                "total_points": len(points),
                "passed": sum(1 for point in points if point["status"] == "pass"),
                "failed": sum(1 for point in points if point["status"] == "fail"),
                "skipped": sum(1 for point in points if point["status"] == "skip"),
                "first_failure_point": next((point["context_tokens_target"] for point in points if point["status"] == "fail"), None),
            },
        },
    }
    report = rounded(report)
    report_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(json.dumps({
        "event": "context_curve_report",
        "path": str(report_path),
        "summary": report["context_curve"]["summary"],
    }), flush=True)

    return 1 if any(point["status"] == "fail" for point in points) else 0


if __name__ == "__main__":
    sys.exit(main())
PY
