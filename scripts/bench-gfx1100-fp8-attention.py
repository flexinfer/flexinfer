#!/usr/bin/env python3
"""fp8 vs fp16 KV attention microbench for the Qwen3.8-27B decode shapes on gfx1100.

The kill test for the fp8 conversion variants in ``fp8_convert_variants.py``:
does a cheaper fp8 -> fp16 conversion bring the fp8 KV cache's attention cost
near the fp16 cache's, at the context lengths the 128K lane serves?

Run it inside a vLLM 0.33 serving image while no engine holds the card, with
both files copied in (Triton needs a real file, not stdin):

    kubectl -n flexinfer-system exec -i <pod> -- \\
        sh -c 'cat > /tmp/fp8_convert_variants.py' < scripts/fp8_convert_variants.py
    kubectl -n flexinfer-system exec -i <pod> -- \\
        sh -c 'cat > /tmp/fp8-bench.py && python3 /tmp/fp8-bench.py --out /tmp/fp8-bench.json' \\
        < scripts/bench-gfx1100-fp8-attention.py

It needs about 1 GiB of free VRAM at the default contexts.

What it measures. One attention layer's call into vLLM's own
``run_segmented_attention`` (stage plus reduce kernels), on one sequence, with
the model's head layout and the 128K lane's 832-token KV pages, at query
lengths 1 (an MTP draft step) and 3 (the target verifying K=2 drafts). For
each context it times the fp16 cache, the stock fp8 cache and each variant
with HIP events (median of --iters after --warmup), and checks every variant's
output against stock fp8.

The fp16 cache holds exactly the values the fp8 cache decodes to, so all rows
compute the same attention. A share of the elements (--small-frac) is zero or
below the smallest normal, so the codes the variants treat differently are
present.

The JSON summary goes to stdout between BEGIN/END markers and to --out.
"""

import argparse
import importlib.util
import json
import math
import os
import statistics
import sys
import time

# Qwen3.8-27B: 16 full-attention layers plus one MTP draft layer, 24 query
# heads over 4 KV heads, head_dim 256.
HQ, HK, DIM = 24, 4, 256
ATTENTION_LAYERS = 16
MTP_DRAFTS = 2
# A decode step: the target runs every attention layer over the last accepted
# token plus the drafts, and the MTP layer runs once per draft.
TARGET_QUERY_LEN = 1 + MTP_DRAFTS
DRAFT_QUERY_LEN = 1

BEGIN, END = "=== BEGIN fp8-attention-bench ===", "=== END fp8-attention-bench ==="


# --- pure helpers (tested without a GPU) -------------------------------------


def parse_int_list(text):
    values = [int(part) for part in text.split(",") if part.strip()]
    if not values or any(value < 1 for value in values):
        raise argparse.ArgumentTypeError(f"expected positive integers, got {text!r}")
    return values


def kv_bytes(context, element_size):
    """Bytes of K plus V one layer reads for one sequence of ``context`` tokens."""
    return context * HK * DIM * 2 * element_size


def gb_per_s(nbytes, ms):
    return round(nbytes / 1e9 / (ms / 1e3), 1) if ms and ms > 0 else None


def step_attention_ms(by_query_len):
    """Attention time in one decode step, from per-call times keyed by query length."""
    if TARGET_QUERY_LEN not in by_query_len or DRAFT_QUERY_LEN not in by_query_len:
        return None
    return round(
        ATTENTION_LAYERS * by_query_len[TARGET_QUERY_LEN]
        + MTP_DRAFTS * by_query_len[DRAFT_QUERY_LEN],
        3,
    )


def verdict(step_ms, exact_variants=("exact", "lut"), bar=2.0, min_context=65536):
    """Apply the kill-test rule to ``{context: {row: step attention ms}}``.

    Pass: at every context of ``min_context`` or more, some bit-identical
    variant's step attention time is within ``bar`` times fp16's. The verdict
    names the best such variant per context.
    """
    judged, passed = {}, True
    for context, rows in sorted(step_ms.items()):
        if context < min_context:
            continue
        fp16 = rows.get("fp16")
        candidates = {
            name: rows[name] for name in exact_variants if rows.get(name) is not None
        }
        if not fp16 or not candidates:
            judged[context] = {"pass": False, "reason": "missing fp16 or exact variant"}
            passed = False
            continue
        best = min(candidates, key=candidates.get)
        ratio = round(candidates[best] / fp16, 2)
        stock = rows.get("stock")
        judged[context] = {
            "best_exact": best,
            "ratio_to_fp16": ratio,
            "stock_ratio_to_fp16": round(stock / fp16, 2) if stock else None,
            "pass": ratio <= bar,
        }
        passed = passed and ratio <= bar
    if not judged:
        return {
            "pass": False,
            "reason": f"no context >= {min_context}",
            "by_context": {},
        }
    return {"pass": passed, "bar": bar, "by_context": judged}


def load_convert_module(path):
    spec = importlib.util.spec_from_file_location("fp8_convert_variants", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


# --- the GPU side ------------------------------------------------------------


def timed(torch, fn, iters, warmup):
    for _ in range(warmup):
        fn()
    torch.cuda.synchronize()
    samples = []
    for _ in range(iters):
        start = torch.cuda.Event(enable_timing=True)
        end = torch.cuda.Event(enable_timing=True)
        start.record()
        fn()
        end.record()
        end.synchronize()
        samples.append(start.elapsed_time(end))
    return {
        "median_ms": round(statistics.median(samples), 4),
        "min_ms": round(min(samples), 4),
        "p90_ms": round(sorted(samples)[int(0.9 * (len(samples) - 1))], 4),
    }


def to_fp8(torch, values):
    """fp16 -> float8_e4m3fn and back, on the device when it can cast there."""
    try:
        fp8 = values.to(torch.float16).to(torch.float8_e4m3fn)
        return fp8, fp8.to(torch.float16)
    except (RuntimeError, NotImplementedError):
        fp8 = values.to(torch.float16).cpu().to(torch.float8_e4m3fn)
        return fp8.to(values.device), fp8.to(torch.float16).to(values.device)


def build_inputs(torch, context, query_len, page, small_frac, seed, device="cuda"):
    """One sequence of ``context`` tokens whose last ``query_len`` are the query."""
    generator = torch.Generator(device=device).manual_seed(seed)
    blocks = math.ceil(context / page)
    shape = (blocks, page, HK, DIM)

    def cache():
        values = torch.randn(shape, generator=generator, device=device)
        pick = torch.rand(shape, generator=generator, device=device)
        # Half of the small share is exact zero, half is below the smallest
        # normal (2**-6), so every exponent-0 code turns up.
        values = torch.where(pick < small_frac / 2, torch.zeros_like(values), values)
        tiny = (torch.rand(shape, generator=generator, device=device) - 0.5) * 2**-5
        values = torch.where(
            (pick >= small_frac / 2) & (pick < small_frac), tiny, values
        )
        return to_fp8(torch, values)

    k8, k16 = cache()
    v8, v16 = cache()
    q = torch.randn(
        (query_len, HQ, DIM), generator=generator, device=device, dtype=torch.float32
    ).to(torch.float16)
    table = torch.arange(blocks, device=device, dtype=torch.int32).view(1, blocks)
    starts = torch.tensor([0, query_len], device=device, dtype=torch.int32)
    lengths = torch.tensor([context], device=device, dtype=torch.int32)
    one = torch.ones(1, device=device, dtype=torch.float32)
    return {
        "q": q,
        "fp8": (k8, v8),
        "fp16": (k16, v16),
        "table": table,
        "starts": starts,
        "lengths": lengths,
        "scale": one,
        "codes_exp0": int(((k8.view(torch.uint8) & 0x78) == 0).sum().item()),
        "codes_total": k8.numel(),
    }


def make_call(torch, module, inputs, caches, context, query_len, config):
    kc, vc = caches
    out = torch.zeros_like(inputs["q"])
    qcap = module.segmented_query_capacity(query_len)
    shapes = module.segmented_workspace_shapes(1, qcap, HQ, HK, DIM, config["splits"])
    workspace = None
    if shapes is not None:
        workspace = tuple(
            torch.empty(math.prod(shape), device="cuda", dtype=torch.float32)
            for shape in shapes
        )

    def call():
        module.run_segmented_attention(
            inputs["q"],
            out,
            kc,
            vc,
            inputs["table"],
            inputs["starts"],
            inputs["lengths"],
            query_len,
            context,
            inputs["scale"],
            inputs["scale"],
            DIM**-0.5,
            config=config,
            workspace=workspace,
        )

    return call, out


def bench(args):
    import torch
    import triton
    import vllm
    from vllm.v1.attention.ops import segmented_attention as stock

    convert = load_convert_module(args.convert_module)
    with open(stock.__file__, encoding="utf-8") as handle:
        source = handle.read()
    digest = convert.source_sha256(source)

    modules, errors = {"stock": stock}, {}
    for variant in args.variants:
        if variant == "stock":
            continue
        try:
            modules[variant] = convert.load_variant_module(
                source, variant, args.work_dir
            )
        except Exception as exc:  # a variant that does not load is a result
            errors[variant] = f"load: {exc!r}"[:400]

    result = {
        "device": torch.cuda.get_device_name(0),
        "versions": {
            "vllm": vllm.__version__,
            "triton": triton.__version__,
            "torch": torch.__version__,
        },
        "source_sha256": digest,
        "source_known": convert.KNOWN_SOURCES.get(digest),
        "shape": {"hq": HQ, "hk": HK, "dim": DIM, "page": args.page},
        "iters": args.iters,
        "small_frac": args.small_frac,
        "rows": [],
        "errors": errors,
    }
    step_ms = {}
    for context in args.contexts:
        per_row = {}
        for query_len in args.query_lens:
            inputs = build_inputs(
                torch, context, query_len, args.page, args.small_frac, args.seed
            )
            fp8_config = dict(
                stock.select_segmented_config(1, query_len, context, HQ, HK, DIM, True)
            )
            fp16_config = dict(
                stock.select_segmented_config(1, query_len, context, HQ, HK, DIM, False)
            )
            plans = [("fp16", stock, "fp16", fp16_config)] + [
                (name, module, "fp8", fp8_config) for name, module in modules.items()
            ]
            reference = None
            for name, module, kind, config in plans:
                row = {
                    "context": context,
                    "query_len": query_len,
                    "row": name,
                    "kv": kind,
                    "config": config,
                }
                try:
                    call, out = make_call(
                        torch, module, inputs, inputs[kind], context, query_len, config
                    )
                    started = time.time()
                    call()
                    torch.cuda.synchronize()
                    row["first_call_s"] = round(time.time() - started, 2)
                    row.update(timed(torch, call, args.iters, args.warmup))
                    size = 1 if kind == "fp8" else 2
                    row["kv_gb_per_s"] = gb_per_s(
                        kv_bytes(context, size), row["median_ms"]
                    )
                    row["ms_per_1k_ctx"] = round(row["median_ms"] / (context / 1000), 5)
                    row["finite"] = bool(torch.isfinite(out).all().item())
                    if name == "stock":
                        reference = out.clone()
                    elif kind == "fp8" and reference is not None:
                        row["equal_to_stock"] = bool(torch.equal(out, reference))
                        row["max_abs_diff"] = float(
                            (out.float() - reference.float()).abs().max().item()
                        )
                    per_row.setdefault(name, {})[query_len] = row["median_ms"]
                except Exception as exc:
                    row["error"] = repr(exc)[:400]
                result["rows"].append(row)
            result.setdefault("inputs", {})[f"{context}/{query_len}"] = {
                "exp0_code_share": round(
                    inputs["codes_exp0"] / inputs["codes_total"], 4
                )
            }
            del inputs
            torch.cuda.empty_cache()
        step_ms[context] = {
            name: step_attention_ms(times) for name, times in per_row.items()
        }
    result["step_attention_ms"] = step_ms
    result["verdict"] = verdict(step_ms, bar=args.bar)
    return result


def main(argv=None):
    here = os.path.dirname(os.path.abspath(__file__))
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "--contexts", type=parse_int_list, default=[16384, 65536, 122880]
    )
    parser.add_argument("--query-lens", type=parse_int_list, default=[1, 3])
    parser.add_argument(
        "--variants",
        type=lambda text: [part for part in text.split(",") if part],
        default=["stock", "exact", "exact_nonan", "approx", "lut"],
    )
    parser.add_argument("--page", type=int, default=832, help="KV tokens per block")
    parser.add_argument("--small-frac", type=float, default=0.02)
    parser.add_argument("--iters", type=int, default=40)
    parser.add_argument("--warmup", type=int, default=5)
    parser.add_argument("--seed", type=int, default=20261124)
    parser.add_argument("--bar", type=float, default=2.0)
    parser.add_argument(
        "--convert-module", default=os.path.join(here, "fp8_convert_variants.py")
    )
    parser.add_argument("--work-dir", default="/tmp/fp8-attention-variants")
    parser.add_argument("--out", default="/tmp/fp8-attention-bench.json")
    args = parser.parse_args(argv)
    if "stock" not in args.variants:
        args.variants.insert(0, "stock")

    result = bench(args)
    text = json.dumps(result, indent=1, sort_keys=True)
    with open(args.out, "w", encoding="utf-8") as handle:
        handle.write(text + "\n")
    print(BEGIN)
    print(text)
    print(END)
    return 0


if __name__ == "__main__":
    sys.exit(main())
