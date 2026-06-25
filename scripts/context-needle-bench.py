#!/usr/bin/env python3
"""Progressive long-context COHERENCE (needle-in-haystack) benchmark.

Companion to ``scripts/bench-context-curve.sh`` (which measures *throughput* at
each context length). This tool measures *correctness*: at each target context
length it plants a unique "needle" fact near the very start of the prompt, pads
with filler to the target token count, then asks the model to recall the needle
at the end. A pass means the model produced a COHERENT answer that retrieved the
needle; a fail means it could not (degenerate repetition, wrong answer, or error).

Primary use case: validating *forced* RoPE extrapolation. When a model is served
past its trained context window (e.g. vLLM ``VLLM_ALLOW_LONG_MAX_MODEL_LEN=1`` to
push ``max_model_len`` beyond ``text_config.max_position_embeddings``), the weights
load and short requests look fine, but coherence silently collapses somewhere past
the trained length — output turns into ``!!!!!`` or repeats. This bench finds that
cliff so you can pin ``max_model_len`` to the largest *coherent* value, not just
the largest one that loads.

Modes
-----
  # explicit target points (default)
  context-needle-bench.py --base-url http://localhost:8000 --model my-model \
      --points 32k,48k,64k,80k,96k

  # progressive sweep, stop at first FAIL
  context-needle-bench.py ... --points 32k,48k,64k,80k,96k,112k,128k --stop-on-fail

  # binary-search the coherence cliff between two bounds
  context-needle-bench.py ... --bisect 64k:128k --bisect-tol 8k

Output: a human table on stdout plus, with ``--json FILE``, a machine-readable
report whose ``points[]`` shape mirrors bench-context-curve for easy diffing.

No third-party deps (urllib only) so it runs inside a model serving pod:
  kubectl exec -i POD -c model -- python3 - < scripts/context-needle-bench.py -- ...args
"""

from __future__ import annotations

import argparse
import json
import re
import sys
import time
import urllib.error
import urllib.request

# A unique, unlikely-to-be-guessed needle. Override with --needle-value.
DEFAULT_NEEDLE_VALUE = "MARMALADE-73118"
NEEDLE_TEMPLATE = "The vault override passphrase is {value}."
QUESTION = (
    "Earlier in this message I stated a vault override passphrase exactly once, "
    "near the very beginning. Repeat that passphrase verbatim. "
    "Answer with only the passphrase."
)
FILLER_LINE = (
    "Log entry {i}: routine telemetry sample, subsystem nominal, no anomalies "
    "recorded during this interval window. "
)
# Rough tokens/filler-line for the default line (calibrate with --tokens-per-line
# if your tokenizer differs). The actual prompt_tokens is read back from usage,
# so this only affects how close we land to each target.
DEFAULT_TOKENS_PER_LINE = 22.0


def parse_count(raw: str) -> int:
    """Parse '64k' / '64000' / '131072' into an int token count."""
    s = raw.strip().lower()
    mult = 1
    if s.endswith("k"):
        mult = 1024
        s = s[:-1]
    return int(round(float(s) * mult))


def build_prompt(target_tokens: int, needle_value: str, tokens_per_line: float) -> str:
    needle = NEEDLE_TEMPLATE.format(value=needle_value)
    n_lines = max(1, int((target_tokens - 64) / max(1.0, tokens_per_line)))
    filler = "".join(FILLER_LINE.format(i=i) for i in range(n_lines))
    return f"{needle} {filler}\n\n{QUESTION}"


def is_degenerate(text: str) -> bool:
    """Detect the classic extrapolation-collapse output (repeated single token)."""
    t = (text or "").strip()
    if not t:
        return True
    # >60% of the answer is one repeated non-alphanumeric char (e.g. '!!!!!').
    most = max((t.count(c) for c in set(t)), default=0)
    if most >= 0.6 * len(t) and not t[0].isalnum():
        return True
    # A single token repeated many times.
    toks = t.split()
    if len(toks) >= 6 and len(set(toks)) == 1:
        return True
    return False


def probe(
    base_url, model, target_tokens, needle_value, tokens_per_line, max_tokens, timeout
):
    prompt = build_prompt(target_tokens, needle_value, tokens_per_line)
    body = json.dumps(
        {
            "model": model,
            "messages": [{"role": "user", "content": prompt}],
            "max_tokens": max_tokens,
            "temperature": 0.0,
        }
    ).encode()
    url = base_url.rstrip("/") + "/v1/chat/completions"
    req = urllib.request.Request(
        url, data=body, headers={"Content-Type": "application/json"}
    )
    t0 = time.time()
    try:
        resp = json.load(urllib.request.urlopen(req, timeout=timeout))
    except urllib.error.HTTPError as e:
        return {
            "target_tokens": target_tokens,
            "status": "error",
            "http_code": e.code,
            "detail": e.read().decode()[:200],
            "prompt_tokens": None,
            "needle_recalled": False,
            "coherent": False,
            "latency_s": round(time.time() - t0, 2),
        }
    except Exception as e:  # noqa: BLE001 - report any transport failure as a point
        return {
            "target_tokens": target_tokens,
            "status": "error",
            "detail": str(e)[:200],
            "prompt_tokens": None,
            "needle_recalled": False,
            "coherent": False,
            "latency_s": round(time.time() - t0, 2),
        }
    ans = resp["choices"][0]["message"].get("content") or ""
    pt = (resp.get("usage") or {}).get("prompt_tokens")
    recalled = needle_value in ans
    degenerate = is_degenerate(ans)
    coherent = recalled and not degenerate
    return {
        "target_tokens": target_tokens,
        "prompt_tokens": pt,
        "needle_recalled": recalled,
        "degenerate": degenerate,
        "coherent": coherent,
        "answer_preview": ans[:60],
        "latency_s": round(time.time() - t0, 2),
        "status": "pass" if coherent else "fail",
    }


def fmt_row(r):
    pt = r.get("prompt_tokens")
    pt_s = f"{pt:>7}" if isinstance(pt, int) else "      -"
    mark = {"pass": "PASS", "fail": "FAIL", "error": "ERR "}.get(r["status"], "?")
    extra = ""
    if r["status"] == "error":
        extra = f"  ({r.get('http_code', '')} {r.get('detail', '')})".rstrip()
    elif not r["coherent"]:
        why = "degenerate" if r.get("degenerate") else "needle-missed"
        extra = f"  ({why}: {r.get('answer_preview', '')!r})"
    return (
        f"  target={r['target_tokens']:>7}  actual={pt_s}  "
        f"{mark}  {r.get('latency_s', '?')}s{extra}"
    )


def main():
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument(
        "--base-url",
        default="http://localhost:8000",
        help="OpenAI-compatible base URL (default: %(default)s)",
    )
    ap.add_argument("--model", required=True, help="served model name")
    ap.add_argument(
        "--points",
        default="32k,48k,64k,80k,96k",
        help="comma-separated target token counts (e.g. 32k,64k,96k)",
    )
    ap.add_argument(
        "--bisect",
        default=None,
        metavar="LO:HI",
        help="binary-search the coherence cliff between LO and HI "
        "(e.g. 64k:128k); overrides --points",
    )
    ap.add_argument(
        "--bisect-tol",
        default="8k",
        help="stop bisecting when the bracket is below this (default %(default)s)",
    )
    ap.add_argument(
        "--stop-on-fail",
        action="store_true",
        help="in --points mode, stop after the first FAIL",
    )
    ap.add_argument("--needle-value", default=DEFAULT_NEEDLE_VALUE)
    ap.add_argument("--tokens-per-line", type=float, default=DEFAULT_TOKENS_PER_LINE)
    ap.add_argument(
        "--max-tokens", type=int, default=40, help="output tokens to request"
    )
    ap.add_argument("--timeout", type=float, default=240.0)
    ap.add_argument("--json", default=None, help="write machine-readable report here")
    args = ap.parse_args()

    results = []
    highest_pass = None
    lowest_fail = None

    def run(target):
        r = probe(
            args.base_url,
            args.model,
            target,
            args.needle_value,
            args.tokens_per_line,
            args.max_tokens,
            args.timeout,
        )
        results.append(r)
        print(fmt_row(r), flush=True)
        return r

    print(f"context-needle-bench :: model={args.model} base={args.base_url}")
    if args.bisect:
        lo_s, hi_s = args.bisect.split(":", 1)
        lo, hi = parse_count(lo_s), parse_count(hi_s)
        tol = parse_count(args.bisect_tol)
        # Establish that lo passes and hi fails before bisecting.
        if run(lo)["coherent"]:
            highest_pass = lo
        if not run(hi)["coherent"]:
            lowest_fail = hi
        while (
            highest_pass is not None
            and lowest_fail is not None
            and (lowest_fail - highest_pass) > tol
        ):
            mid = (highest_pass + lowest_fail) // 2
            if run(mid)["coherent"]:
                highest_pass = mid
            else:
                lowest_fail = mid
    else:
        for p in args.points.split(","):
            if not p.strip():
                continue
            r = run(parse_count(p))
            if r["coherent"]:
                highest_pass = max(highest_pass or 0, r["target_tokens"])
            else:
                lowest_fail = min(lowest_fail or 10**12, r["target_tokens"])
                if args.stop_on_fail:
                    break

    print("\nSummary:")
    print(
        f"  highest coherent context : "
        f"{highest_pass if highest_pass is not None else 'none'}"
    )
    print(
        f"  lowest incoherent context: "
        f"{lowest_fail if lowest_fail is not None else 'none'}"
    )
    if highest_pass and lowest_fail:
        print(f"  coherence cliff is between {highest_pass} and {lowest_fail}")
        print(f"  recommend pinning max_model_len at/below {highest_pass}")

    report = {
        "model": args.model,
        "base_url": args.base_url,
        "needle_value": args.needle_value,
        "highest_coherent_tokens": highest_pass,
        "lowest_incoherent_tokens": lowest_fail,
        "points": results,
    }
    if args.json:
        with open(args.json, "w") as f:
            json.dump(report, f, indent=2)
        print(f"\nwrote {args.json}")

    # Exit non-zero if nothing passed (useful as a CI gate).
    sys.exit(0 if highest_pass else 1)


if __name__ == "__main__":
    main()
