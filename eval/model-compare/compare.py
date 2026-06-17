#!/usr/bin/env python3
"""Compare two OpenAI-compatible served models on QUALITY + PERFORMANCE.

Quality is scored deterministically (no LLM judge): the model's final answer
(reasoning stripped) is normalized and matched against expected substrings per
item mode (contains / all / exact). Performance (TTFT + decode tok/s) is measured
by streaming a fixed-length generation. Designed to run in-cluster against the
flexinfer proxy with only the Python standard library (works inside the vLLM pod).

Usage:
  python3 compare.py \
    --base-url http://flexinfer-proxy.flexinfer-system.svc:80 \
    --url-template '{base}/model/{model}/v1/chat/completions' \
    --models gemma4-26b-a4b-gptq,qwen35-moe-reasoning-5930k \
    --dataset prompts.json --out-prefix results

Each model name is also used verbatim as the OpenAI `model` field.
"""
import argparse, json, re, statistics, sys, time, urllib.request


def post_stream(url, model, messages, max_tokens, timeout):
    """Stream a chat completion. Returns (ttft_s, total_s, content, reasoning,
    completion_tokens, error)."""
    body = json.dumps(
        {
            "model": model,
            "messages": messages,
            "max_tokens": max_tokens,
            "temperature": 0,
            "stream": True,
            "stream_options": {"include_usage": True},
        }
    ).encode()
    req = urllib.request.Request(
        url, data=body, headers={"Content-Type": "application/json"}
    )
    t0 = time.time()
    ttft = None
    content, reasoning = [], []
    ctoks = None
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            for raw in r:
                line = raw.decode("utf-8", "ignore").strip()
                if not line.startswith("data:"):
                    continue
                d = line[5:].strip()
                if d == "[DONE]":
                    break
                try:
                    j = json.loads(d)
                except Exception:
                    continue
                ch = (j.get("choices") or [{}])[0]
                delta = ch.get("delta") or {}
                piece = delta.get("content")
                # vLLM/flexinfer-proxy stream reasoning under `reasoning` (this
                # build) or `reasoning_content` (others) — accept either.
                rpiece = delta.get("reasoning_content") or delta.get("reasoning")
                if (piece or rpiece) and ttft is None:
                    ttft = time.time() - t0
                if piece:
                    content.append(piece)
                if rpiece:
                    reasoning.append(rpiece)
                if j.get("usage"):
                    ctoks = j["usage"].get("completion_tokens")
    except Exception as e:  # noqa: BLE001
        return (
            ttft,
            time.time() - t0,
            "".join(content),
            "".join(reasoning),
            ctoks,
            str(e),
        )
    return (ttft, time.time() - t0, "".join(content), "".join(reasoning), ctoks, None)


_THINK = re.compile(r"<think>.*?</think>", re.DOTALL | re.IGNORECASE)


def final_answer(content, reasoning):
    """The answer to score: prefer `content` (vLLM separates reasoning into
    reasoning_content); strip any residual <think>..</think>. If the model put
    everything in content with inline <think>, that is removed too."""
    ans = _THINK.sub(" ", content or "")
    if not ans.strip() and reasoning:
        # Model emitted only reasoning (no separated answer) — fall back to the
        # tail of the reasoning so we don't score an empty string.
        ans = (reasoning or "").splitlines()[-1] if reasoning.strip() else ""
    return ans.strip()


def _norm(s):
    s = s.lower().replace(",", "")
    s = re.sub(r"[^a-z0-9 ]+", " ", s)
    return re.sub(r"\s+", " ", s).strip()


def _norm_exact(s):
    return re.sub(r"[^a-z0-9]+", "", s.lower())


def score(item, answer):
    mode = item.get("mode", "contains")
    expect = [str(e) for e in item["expect"]]
    if mode == "exact":
        na = _norm_exact(answer)
        return any(na == _norm_exact(e) for e in expect)
    na = _norm(answer)
    hits = [(" " + _norm(e) + " ") in (" " + na + " ") for e in expect]
    return all(hits) if mode == "all" else any(hits)


def run_quality(url, model, items, max_tokens, timeout):
    results = []
    for it in items:
        msgs = [{"role": "user", "content": it["prompt"]}]
        ttft, total, content, reasoning, ctoks, err = post_stream(
            url, model, msgs, max_tokens, timeout
        )
        ans = final_answer(content, reasoning)
        ok = (err is None) and score(it, ans)
        results.append(
            {
                "id": it["id"],
                "category": it["category"],
                "correct": bool(ok),
                "latency_s": round(total, 3),
                "ttft_s": round(ttft, 3) if ttft else None,
                "completion_tokens": ctoks,
                "reasoning_tokens_approx": len(reasoning),
                "answer": ans[:160],
                "error": err,
            }
        )
        print(
            f"  [{model}] {it['id']:<9} {'OK ' if ok else 'XX '}"
            f"({total:5.1f}s) -> {ans[:48]!r}{' ERR='+err[:40] if err else ''}"
        )
    return results


def run_perf(url, model, prompt, perf_tokens, iters, timeout):
    samples = []
    msgs = [{"role": "user", "content": prompt}]
    post_stream(url, model, msgs, 16, timeout)  # warm
    for _ in range(iters):
        ttft, total, content, reasoning, ctoks, err = post_stream(
            url, model, msgs, perf_tokens, timeout
        )
        if err or not ctoks or ttft is None:
            continue
        decode_s = max(total - ttft, 1e-6)
        samples.append(
            {
                "ttft_s": ttft,
                "tok_s": ctoks / decode_s,
                "tokens": ctoks,
                "total_s": total,
            }
        )
    if not samples:
        return {"ok": False}
    return {
        "ok": True,
        "iters": len(samples),
        "ttft_p50_s": round(statistics.median(s["ttft_s"] for s in samples), 3),
        "decode_tok_s_p50": round(statistics.median(s["tok_s"] for s in samples), 1),
        "tokens_p50": int(statistics.median(s["tokens"] for s in samples)),
    }


def aggregate(qres):
    cats = {}
    for r in qres:
        c = cats.setdefault(r["category"], [0, 0])
        c[1] += 1
        c[0] += 1 if r["correct"] else 0
    total_ok = sum(1 for r in qres if r["correct"])
    lats = [r["latency_s"] for r in qres if r["latency_s"]]
    return {
        "overall": {
            "correct": total_ok,
            "total": len(qres),
            "pct": round(100 * total_ok / len(qres), 1) if qres else 0,
        },
        "by_category": {
            k: {"correct": v[0], "total": v[1], "pct": round(100 * v[0] / v[1], 1)}
            for k, v in sorted(cats.items())
        },
        "quality_latency_p50_s": round(statistics.median(lats), 2) if lats else None,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base-url", required=True)
    ap.add_argument(
        "--url-template", default="{base}/model/{model}/v1/chat/completions"
    )
    ap.add_argument("--models", required=True, help="comma-separated model names")
    ap.add_argument("--dataset", default="prompts.json")
    ap.add_argument("--out-prefix", default="results")
    ap.add_argument(
        "--max-tokens",
        type=int,
        default=1024,
        help="budget per quality item (room for reasoning)",
    )
    ap.add_argument("--perf-tokens", type=int, default=200)
    ap.add_argument("--perf-iters", type=int, default=3)
    ap.add_argument("--timeout", type=int, default=180)
    a = ap.parse_args()

    ds = json.load(open(a.dataset))
    items = ds["items"]
    models = [m.strip() for m in a.models.split(",") if m.strip()]
    out = {"base_url": a.base_url, "dataset": a.dataset, "models": {}}

    for model in models:
        url = a.url_template.format(base=a.base_url.rstrip("/"), model=model)
        print(f"\n=== {model} === ({url})")
        q = run_quality(url, model, items, a.max_tokens, a.timeout)
        agg = aggregate(q)
        perf = run_perf(
            url, model, ds["perf_prompt"], a.perf_tokens, a.perf_iters, a.timeout
        )
        print(
            f"  -> accuracy {agg['overall']['correct']}/{agg['overall']['total']}"
            f" ({agg['overall']['pct']}%) | perf {perf}"
        )
        out["models"][model] = {"quality": q, "aggregate": agg, "perf": perf}

    json.dump(out, open(a.out_prefix + ".json", "w"), indent=2)
    write_markdown(out, a.out_prefix + ".md")
    print(f"\nWrote {a.out_prefix}.json and {a.out_prefix}.md")


def write_markdown(out, path):
    models = list(out["models"].keys())
    L = [
        "# Model comparison — quality + performance",
        "",
        f"- Base URL: `{out['base_url']}`  ",
        f"- Dataset: `{out['dataset']}` ({len(next(iter(out['models'].values()))['quality'])} objective items, deterministic scoring)  ",
        f"- Models: {', '.join('`'+m+'`' for m in models)}",
        "",
    ]
    # Overall + perf table
    L += [
        "## Summary",
        "",
        "| Metric | " + " | ".join(models) + " |",
        "|---|" + "|".join(["---"] * len(models)) + "|",
    ]

    def row(label, fn):
        return "| " + label + " | " + " | ".join(fn(m) for m in models) + " |"

    L.append(
        row(
            "Accuracy",
            lambda m: f"**{out['models'][m]['aggregate']['overall']['pct']}%** "
            f"({out['models'][m]['aggregate']['overall']['correct']}/{out['models'][m]['aggregate']['overall']['total']})",
        )
    )
    L.append(
        row(
            "Decode tok/s (p50)",
            lambda m: str(out["models"][m]["perf"].get("decode_tok_s_p50", "n/a")),
        )
    )
    L.append(
        row(
            "TTFT s (p50)",
            lambda m: str(out["models"][m]["perf"].get("ttft_p50_s", "n/a")),
        )
    )
    L.append(
        row(
            "Quality latency s (p50)",
            lambda m: str(
                out["models"][m]["aggregate"].get("quality_latency_p50_s", "n/a")
            ),
        )
    )
    # Per-category accuracy
    cats = sorted(
        {c for m in models for c in out["models"][m]["aggregate"]["by_category"]}
    )
    L += [
        "",
        "## Accuracy by category",
        "",
        "| Category | " + " | ".join(models) + " |",
        "|---|" + "|".join(["---"] * len(models)) + "|",
    ]
    for c in cats:

        def cell(m):
            bc = out["models"][m]["aggregate"]["by_category"].get(c)
            return f"{bc['pct']}% ({bc['correct']}/{bc['total']})" if bc else "—"

        L.append("| " + c + " | " + " | ".join(cell(m) for m in models) + " |")
    # Per-item correctness
    ids = [r["id"] for r in next(iter(out["models"].values()))["quality"]]
    L += [
        "",
        "## Per-item (✓/✗)",
        "",
        "| Item | " + " | ".join(models) + " |",
        "|---|" + "|".join(["---"] * len(models)) + "|",
    ]
    for iid in ids:

        def mark(m):
            r = next((x for x in out["models"][m]["quality"] if x["id"] == iid), None)
            return ("✓" if r and r["correct"] else "✗") if r else "—"

        L.append("| " + iid + " | " + " | ".join(mark(m) for m in models) + " |")
    open(path, "w").write("\n".join(L) + "\n")


if __name__ == "__main__":
    sys.exit(main())
