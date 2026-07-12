#!/usr/bin/env python3
"""Progressive long-context COHERENCE (needle-in-haystack) benchmark.

Companion to ``scripts/bench-context-curve.sh`` (which measures *throughput* at
each context length). This tool measures *correctness*: at each target context
length it plants one or more unique "needle" facts in the prompt, pads with
filler to the target token count, then asks the model to recall them. A pass
means the model produced a COHERENT answer that retrieved every needle; a fail
means it could not (degenerate repetition, wrong answer, or error).

Primary use case: validating *forced* RoPE extrapolation. When a model is served
past its trained context window (e.g. vLLM ``VLLM_ALLOW_LONG_MAX_MODEL_LEN=1`` to
push ``max_model_len`` beyond ``text_config.max_position_embeddings``), the weights
load and short requests look fine, but coherence silently collapses somewhere past
the trained length — output turns into ``!!!!!`` or repeats. This bench finds that
cliff so you can pin ``max_model_len`` to the largest *coherent* value, not just
the largest one that loads.

Modes
-----
  # explicit target points (default: one needle at the very start)
  context-needle-bench.py --base-url http://localhost:8000 --model my-model \
      --points 32k,48k,64k,80k,96k

  # progressive sweep, stop at first FAIL
  context-needle-bench.py ... --points 32k,48k,64k,80k,96k,112k,128k --stop-on-fail

  # binary-search the coherence cliff between two bounds
  context-needle-bench.py ... --bisect 64k:128k --bisect-tol 8k

  # DEPTH GRID: place a single needle at several depths (% of context) and report
  # a length x depth recall grid — exposes "lost in the middle" style failures
  context-needle-bench.py ... --points 48k,60k,73k --depths 0,25,50,75,100

  # MULTI-NEEDLE: plant N labeled needles spread across the context in ONE prompt
  # and require the model to recall ALL of them (tracks several facts at once)
  context-needle-bench.py ... --points 48k,60k --needles 5

Output: a human table/grid on stdout plus, with ``--json FILE``, a machine-readable
report whose ``points[]`` shape mirrors bench-context-curve for easy diffing.

No third-party deps (urllib only) so it runs inside a model serving pod:
  kubectl exec -i POD -c model -- python3 - < scripts/context-needle-bench.py -- ...args
"""

from __future__ import annotations

import argparse
import json
import sys
import time
import urllib.error
import urllib.request

# A unique, unlikely-to-be-guessed needle base. Override with --needle-value.
DEFAULT_NEEDLE_VALUE = "MARMALADE-73118"
NEEDLE_TEMPLATE = "The vault override passphrase is {value}."
LABELED_NEEDLE_TEMPLATE = "The {label} vault passphrase is {value}."
QUESTION = (
    "Earlier in this message I stated a vault override passphrase exactly once, "
    "near the very beginning. Repeat that passphrase verbatim. "
    "Answer with only the passphrase."
)
# Phonetic labels for the multi-needle mode.
PHONETIC = ["ALPHA", "BRAVO", "CHARLIE", "DELTA", "ECHO", "FOXTROT", "GOLF", "HOTEL"]
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


def needle_sentence(value: str, label: str | None) -> str:
    if label:
        return LABELED_NEEDLE_TEMPLATE.format(label=label, value=value)
    return NEEDLE_TEMPLATE.format(value=value)


def build_prompt(
    target_tokens: int, needles: list[dict], tokens_per_line: float
) -> str:
    """Build a haystack prompt.

    ``needles`` is a list of ``{value, depth, label}`` dicts. Each needle sentence
    is inserted at its fractional ``depth`` (0.0 = very start, 1.0 = just before the
    question) among the filler lines. The trailing question asks the model to recall
    every needle.
    """
    n_lines = max(
        len(needles) + 1, int((target_tokens - 96) / max(1.0, tokens_per_line))
    )
    lines = [FILLER_LINE.format(i=i) for i in range(n_lines)]
    # Insert from deepest first so earlier insertions don't shift later indices.
    for nd in sorted(needles, key=lambda n: n["depth"], reverse=True):
        idx = min(len(lines), max(0, int(round(nd["depth"] * n_lines))))
        lines.insert(idx, needle_sentence(nd["value"], nd.get("label")))
    body = "".join(lines)

    if len(needles) == 1 and not needles[0].get("label"):
        question = QUESTION
    else:
        labels = ", ".join(nd["label"] for nd in needles)
        question = (
            f"Several labeled vault passphrases appear above ({labels}). For EACH "
            "label, repeat its passphrase exactly. Answer with one "
            "'LABEL: passphrase' per line, and nothing else."
        )
    return f"{body}\n\n{question}"


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
    base_url, model, target_tokens, needles, tokens_per_line, max_tokens, timeout
):
    """Run one request with the given needle set; return a result dict."""
    # Multi-needle answers need room for one line per needle.
    out_tokens = max(max_tokens, 18 * len(needles) + 16)
    # max_model_len covers prompt plus generated tokens. Reserve the requested
    # output budget so a point equal to the server limit (for example 64K) does
    # not fail request validation before the attention test starts.
    prompt_budget = max(1, target_tokens - out_tokens)
    prompt = build_prompt(prompt_budget, needles, tokens_per_line)
    body = json.dumps(
        {
            "model": model,
            "messages": [{"role": "user", "content": prompt}],
            "max_tokens": out_tokens,
            "temperature": 0.0,
        }
    ).encode()
    url = base_url.rstrip("/") + "/v1/chat/completions"
    req = urllib.request.Request(
        url, data=body, headers={"Content-Type": "application/json"}
    )
    t0 = time.time()
    base = {
        "target_tokens": target_tokens,
        "n_needles": len(needles),
        "depths": [round(nd["depth"], 3) for nd in needles],
    }
    try:
        resp = json.load(urllib.request.urlopen(req, timeout=timeout))
    except urllib.error.HTTPError as e:
        return {
            **base,
            "status": "error",
            "http_code": e.code,
            "detail": e.read().decode()[:200],
            "prompt_tokens": None,
            "n_recalled": 0,
            "coherent": False,
            "latency_s": round(time.time() - t0, 2),
        }
    except Exception as e:  # noqa: BLE001 - report any transport failure as a point
        return {
            **base,
            "status": "error",
            "detail": str(e)[:200],
            "prompt_tokens": None,
            "n_recalled": 0,
            "coherent": False,
            "latency_s": round(time.time() - t0, 2),
        }
    ans = resp["choices"][0]["message"].get("content") or ""
    pt = (resp.get("usage") or {}).get("prompt_tokens")
    per = [
        {
            "label": nd.get("label"),
            "depth": round(nd["depth"], 3),
            "recalled": nd["value"] in ans,
        }
        for nd in needles
    ]
    n_recalled = sum(1 for p in per if p["recalled"])
    degenerate = is_degenerate(ans)
    coherent = (n_recalled == len(needles)) and not degenerate
    return {
        **base,
        "prompt_tokens": pt,
        "per_needle": per,
        "n_recalled": n_recalled,
        "degenerate": degenerate,
        "coherent": coherent,
        "answer_preview": ans[:80],
        "latency_s": round(time.time() - t0, 2),
        "status": "pass" if coherent else "fail",
    }


def single_needle(value):
    return [{"value": value, "depth": 0.0, "label": None}]


def spread_needles(value, n):
    """N labeled needles evenly spread from depth 0 to ~1, each a unique value."""
    out = []
    for i in range(n):
        depth = 0.0 if n == 1 else i / (n - 1)
        out.append(
            {
                "value": f"{value}-{PHONETIC[i % len(PHONETIC)]}",
                "depth": depth,
                "label": PHONETIC[i % len(PHONETIC)],
            }
        )
    return out


def fmt_row(r):
    pt = r.get("prompt_tokens")
    pt_s = f"{pt:>7}" if isinstance(pt, int) else "      -"
    mark = {"pass": "PASS", "fail": "FAIL", "error": "ERR "}.get(r["status"], "?")
    recall = f"{r.get('n_recalled', 0)}/{r.get('n_needles', 1)}"
    extra = ""
    if r["status"] == "error":
        extra = f"  ({r.get('http_code', '')} {r.get('detail', '')})".rstrip()
    elif not r["coherent"]:
        why = "degenerate" if r.get("degenerate") else "needle-missed"
        extra = f"  ({why}: {r.get('answer_preview', '')!r})"
    return (
        f"  target={r['target_tokens']:>7}  actual={pt_s}  recall={recall}  "
        f"{mark}  {r.get('latency_s', '?')}s{extra}"
    )


def print_depth_grid(grid, depths):
    """grid: {target_tokens: {depth_pct: result}}; depths: list of int percents."""
    header = "  " + "len \\ depth".ljust(10) + "".join(f"{d:>6}%" for d in depths)
    print("\nDepth recall grid (✓ = needle recalled, ✗ = lost):")
    print(header)
    for target in sorted(grid):
        cells = ""
        for d in depths:
            r = grid[target].get(d)
            cell = "✓" if (r and r["coherent"]) else ("·" if r is None else "✗")
            cells += f"{cell:>7}"
        print("  " + f"{target:>10}".ljust(10) + cells)


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
        "(e.g. 64k:128k); overrides --points/--depths/--needles",
    )
    ap.add_argument(
        "--bisect-tol",
        default="8k",
        help="stop bisecting when the bracket is below this (default %(default)s)",
    )
    ap.add_argument(
        "--depths",
        default=None,
        help="comma-separated needle depths in %% (e.g. 0,25,50,75,100) -> run a "
        "length x depth recall grid with one needle per cell",
    )
    ap.add_argument(
        "--needles",
        type=int,
        default=1,
        help="plant N labeled needles spread across the context in one prompt and "
        "require recall of all (default 1 = single needle at the start)",
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

    def run(target, needles):
        r = probe(
            args.base_url,
            args.model,
            target,
            needles,
            args.tokens_per_line,
            args.max_tokens,
            args.timeout,
        )
        results.append(r)
        print(fmt_row(r), flush=True)
        return r

    print(
        f"context-needle-bench :: model={args.model} base={args.base_url} "
        f"needles={args.needles}"
    )

    if args.bisect:
        lo_s, hi_s = args.bisect.split(":", 1)
        lo, hi = parse_count(lo_s), parse_count(hi_s)
        tol = parse_count(args.bisect_tol)
        if run(lo, single_needle(args.needle_value))["coherent"]:
            highest_pass = lo
        if not run(hi, single_needle(args.needle_value))["coherent"]:
            lowest_fail = hi
        while (
            highest_pass is not None
            and lowest_fail is not None
            and (lowest_fail - highest_pass) > tol
        ):
            mid = (highest_pass + lowest_fail) // 2
            if run(mid, single_needle(args.needle_value))["coherent"]:
                highest_pass = mid
            else:
                lowest_fail = mid

    elif args.depths:
        depths = [int(d) for d in args.depths.split(",") if d.strip()]
        grid = {}
        for p in args.points.split(","):
            if not p.strip():
                continue
            target = parse_count(p)
            grid[target] = {}
            for d in depths:
                nd = [{"value": args.needle_value, "depth": d / 100.0, "label": None}]
                grid[target][d] = run(target, nd)
            # A length "passes" only if the needle is recalled at every depth.
            if all(grid[target][d]["coherent"] for d in depths):
                highest_pass = max(highest_pass or 0, target)
            else:
                lowest_fail = min(lowest_fail or 10**12, target)
        print_depth_grid(grid, depths)

    else:
        for p in args.points.split(","):
            if not p.strip():
                continue
            target = parse_count(p)
            needles = (
                spread_needles(args.needle_value, args.needles)
                if args.needles > 1
                else single_needle(args.needle_value)
            )
            r = run(target, needles)
            if r["coherent"]:
                highest_pass = max(highest_pass or 0, target)
            else:
                lowest_fail = min(lowest_fail or 10**12, target)
                if args.stop_on_fail:
                    break

    print("\nSummary:")
    print(f"  highest coherent context : {highest_pass if highest_pass else 'none'}")
    print(f"  lowest incoherent context: {lowest_fail if lowest_fail else 'none'}")
    if highest_pass and lowest_fail:
        print(f"  coherence cliff is between {highest_pass} and {lowest_fail}")
        print(f"  recommend pinning max_model_len at/below {highest_pass}")

    report = {
        "model": args.model,
        "base_url": args.base_url,
        "needle_value": args.needle_value,
        "needles_per_prompt": args.needles,
        "depths_pct": args.depths,
        "highest_coherent_tokens": highest_pass,
        "lowest_incoherent_tokens": lowest_fail,
        "points": results,
    }
    if args.json:
        with open(args.json, "w") as f:
            json.dump(report, f, indent=2)
        print(f"\nwrote {args.json}")

    # Exit non-zero if nothing passed (useful as a CI gate).
    sys.exit(0 if (highest_pass or any(r["coherent"] for r in results)) else 1)


if __name__ == "__main__":
    main()
