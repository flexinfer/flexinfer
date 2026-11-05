#!/usr/bin/env python3
"""Decode-kernel microbench for the Qwen3.8-27B workhorse shapes on gfx1100.

Slice S4 step 0 of .loom/10-research-backend-currency-gfx1100-gfx906-2026-11-04.md
(§5.3). Run it inside a vLLM 0.32 serving image while no engine holds the card
(the canary's `S4_MODE=hold` posture), so every allocation fits:

    kubectl -n flexinfer-system exec -i <canary-pod> -- \
        python3 - --out /tmp/s4-bench.json < scripts/bench-gfx1100-decode-kernels.py

It measures, with HIP events (median of --iters after --warmup):

  - read bandwidth: fp32-accumulated sum and amax over a 2 GiB fp16 buffer, a
    Triton streaming-read kernel, and a device copy (read + write); the read
    ceiling is the best read-only rate;
  - the fp16 `lm_head` [248320, 5120] at n = 1..6 activation rows, through
    `_rocm_C.wvSplitK` (vLLM's decode path for n <= 5) and `F.linear`
    (hipBLASLt; vLLM's path at n = 6);
  - `_rocm_C.gptq_gemm_rdna3` (the RDNA3 W4A16 scalar kernel, M < 12) on the
    model's GPTQ GEMMs as vLLM 0.32 merges them, at M = 1, 2, 3, 4, 6, 8, with
    enough weight copies in rotation that no call reads a weight still in the
    96 MB Infinity Cache, and the per-forward sum weighted by layer count.

Inputs are random: timing depends on shapes, not values. The JSON summary goes
to stdout between BEGIN/END markers and to --out.
"""

import argparse
import json
import math
import os
import statistics
import subprocess
import sys
import time

# Qwen3.8-27B (biMEMO int4 AutoRound @ 2d7425f7, config.json): hidden 5120,
# intermediate 17408, 64 layers = 48 GDN + 16 full attention (24 q heads with
# an output gate, 4 kv heads, head_dim 256), GDN 16 key / 48 value heads x 128,
# vocab 248320, one MTP layer (full attention), group size 128.
HIDDEN = 5120
VOCAB = 248320
GROUP = 128

# (role, K, N, calls per target forward). vLLM 0.32 merges q/k/v (+ the output
# gate) into qkv_proj, gate/up into gate_up_proj and the GDN in_proj_qkv and
# in_proj_z into in_proj_qkvz (models/qwen3_5.py packed_modules_mapping).
GPTQ_LAYERS = (
    ("attn.qkv_proj", 5120, 24 * 256 * 2 + 2 * 4 * 256, 16),
    ("attn.o_proj", 24 * 256, 5120, 16),
    ("gdn.in_proj_qkvz", 5120, 2 * 16 * 128 + 2 * 48 * 128, 48),
    ("gdn.out_proj", 48 * 128, 5120, 48),
    ("mlp.gate_up_proj", 5120, 2 * 17408, 64),
    ("mlp.down_proj", 17408, 5120, 64),
)
# The MTP draft layer (full attention + dense MLP) reuses the attention and MLP
# shapes once per draft step.
MTP_LAYER_ROLES = ("attn.qkv_proj", "attn.o_proj", "mlp.gate_up_proj", "mlp.down_proj")
GPTQ_ROWS = (1, 2, 3, 4, 6, 8)
LM_HEAD_ROWS = (1, 2, 3, 4, 5, 6)
INFINITY_CACHE_BYTES = 96 * 2**20
ROTATION_BYTES = 6 * INFINITY_CACHE_BYTES


def device_facts(torch):
    props = torch.cuda.get_device_properties(0)
    facts = {
        "device": torch.cuda.get_device_name(0),
        "gcn_arch": getattr(props, "gcnArchName", ""),
        "compute_units": props.multi_processor_count,
        "total_memory_gib": round(props.total_memory / 2**30, 2),
        "torch": torch.__version__,
        "hip": getattr(torch.version, "hip", None),
    }
    try:
        import vllm

        facts["vllm"] = vllm.__version__
    except Exception as exc:  # noqa: BLE001 - informational only
        facts["vllm"] = f"import failed: {exc}"
    for cmd in (
        [
            "rocm-smi",
            "--showclocks",
            "--showpower",
            "--showperflevel",
            "--showmaxpower",
        ],
        ["amd-smi", "metric", "--clock", "--power"],
    ):
        try:
            out = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
            if out.returncode == 0 and out.stdout.strip():
                facts[cmd[0]] = out.stdout.strip().splitlines()[-40:]
                break
        except (OSError, subprocess.TimeoutExpired):
            continue
    return facts


def timed(torch, fn, iters, warmup):
    """Median, min and p90 milliseconds of fn over iters HIP-event samples."""
    for _ in range(warmup):
        fn()
    torch.cuda.synchronize()
    starts = [torch.cuda.Event(enable_timing=True) for _ in range(iters)]
    ends = [torch.cuda.Event(enable_timing=True) for _ in range(iters)]
    for i in range(iters):
        starts[i].record()
        fn()
        ends[i].record()
    torch.cuda.synchronize()
    samples = sorted(s.elapsed_time(e) for s, e in zip(starts, ends))
    return {
        "median_ms": round(statistics.median(samples), 5),
        "min_ms": round(samples[0], 5),
        "p90_ms": round(samples[min(len(samples) - 1, int(0.9 * len(samples)))], 5),
        "n": len(samples),
    }


def gbps(nbytes, ms):
    return round(nbytes / (ms * 1e-3) / 1e9, 1) if ms > 0 else None


def rotating(items):
    state = {"i": 0}

    def nxt():
        item = items[state["i"]]
        state["i"] = (state["i"] + 1) % len(items)
        return item

    return nxt


def triton_read(torch, buf, iters, warmup):
    """A plain streaming-read kernel: each program sums BLOCK x UNROLL halves."""
    import triton
    import triton.language as tl

    @triton.jit
    def _read(x_ptr, out_ptr, n, BLOCK: tl.constexpr, UNROLL: tl.constexpr):
        pid = tl.program_id(0)
        base = pid * BLOCK * UNROLL
        acc = tl.zeros([BLOCK], dtype=tl.float32)
        for u in tl.static_range(UNROLL):
            offs = base + u * BLOCK + tl.arange(0, BLOCK)
            acc += tl.load(x_ptr + offs, mask=offs < n, other=0.0).to(tl.float32)
        tl.store(out_ptr + pid, tl.sum(acc, axis=0))

    n = buf.numel()
    best = None
    for block, unroll, warps in ((1024, 8, 4), (2048, 8, 8), (4096, 4, 8)):
        programs = triton.cdiv(n, block * unroll)
        out = torch.empty(programs, dtype=torch.float32, device=buf.device)

        def run(block=block, unroll=unroll, warps=warps, programs=programs, out=out):
            _read[(programs,)](buf, out, n, BLOCK=block, UNROLL=unroll, num_warps=warps)

        t = timed(torch, run, iters, warmup)
        rate = gbps(buf.numel() * buf.element_size(), t["median_ms"])
        row = {"block": block, "unroll": unroll, "num_warps": warps, **t, "gbps": rate}
        if best is None or (rate or 0) > (best["gbps"] or 0):
            best = row
    return best


def bench_bandwidth(torch, args):
    nbytes = args.bandwidth_gib * 2**30
    buf = torch.empty(nbytes // 2, dtype=torch.float16, device="cuda").fill_(1.0)
    dst = torch.empty_like(buf)
    out = {"buffer_gib": args.bandwidth_gib}
    t = timed(
        torch, lambda: torch.sum(buf, dtype=torch.float32), args.iters, args.warmup
    )
    out["sum_fp32acc"] = {**t, "gbps": gbps(nbytes, t["median_ms"])}
    t = timed(torch, lambda: torch.amax(buf), args.iters, args.warmup)
    out["amax"] = {**t, "gbps": gbps(nbytes, t["median_ms"])}
    t = timed(torch, lambda: dst.copy_(buf), args.iters, args.warmup)
    out["copy_read_plus_write"] = {**t, "gbps": gbps(2 * nbytes, t["median_ms"])}
    try:
        out["triton_read"] = triton_read(torch, buf, args.iters, args.warmup)
    except Exception as exc:  # noqa: BLE001 - keep the other methods
        out["triton_read"] = {"error": f"{type(exc).__name__}: {exc}"}
    reads = [
        out[key].get("gbps")
        for key in ("sum_fp32acc", "amax", "triton_read")
        if isinstance(out.get(key), dict) and out[key].get("gbps")
    ]
    out["read_ceiling_gbps"] = max(reads) if reads else None
    del buf, dst
    torch.cuda.empty_cache()
    return out


def bench_lm_head(torch, ops, args, read_ceiling):
    import torch.nn.functional as F

    weight = (
        torch.randn(VOCAB, HIDDEN, dtype=torch.float16, device="cuda") * 0.02
    ).contiguous()
    cu = torch.cuda.get_device_properties(0).multi_processor_count
    nbytes = weight.numel() * weight.element_size()
    rows = []
    for n in LM_HEAD_ROWS:
        x = torch.randn(n, HIDDEN, dtype=torch.float16, device="cuda").contiguous()
        row = {"n": n, "weight_bytes": nbytes}
        if n <= 5:
            t = timed(
                torch, lambda: ops.wvSplitK(weight, x, cu), args.lm_iters, args.warmup
            )
            row["wvSplitK"] = {**t, "gbps": gbps(nbytes, t["median_ms"])}
            if read_ceiling:
                row["wvSplitK"]["fraction_of_read_ceiling"] = round(
                    row["wvSplitK"]["gbps"] / read_ceiling, 3
                )
        t = timed(torch, lambda: F.linear(x, weight), args.lm_iters, args.warmup)
        row["F.linear"] = {**t, "gbps": gbps(nbytes, t["median_ms"])}
        row["vllm_path"] = "wvSplitK" if n <= 5 else "F.linear"
        rows.append(row)
    if args.check:
        x = torch.randn(3, HIDDEN, dtype=torch.float16, device="cuda")
        ref = F.linear(x.float(), weight.float())
        got = ops.wvSplitK(weight, x, cu).float()
        rows.append({"check_n3_max_abs_err": round((got - ref).abs().max().item(), 4)})
    del weight
    torch.cuda.empty_cache()
    return {"compute_units": cu, "rows": rows}


def bench_gptq(torch, ops, args):
    shapes = {}
    for role, k, n, count in GPTQ_LAYERS:
        shapes.setdefault((k, n), []).append((role, count))
    results = []
    per_role_ms = {}
    for (k, n), roles in shapes.items():
        weight_bytes = k * n // 2 + (k // GROUP) * n * 2 + (k // GROUP) * (n // 8) * 4
        copies = max(2, math.ceil(ROTATION_BYTES / weight_bytes))
        ring = []
        for _ in range(copies):
            qweight = torch.randint(
                -(2**31), 2**31 - 1, (k // 8, n), dtype=torch.int32, device="cuda"
            )
            qzeros = torch.randint(
                -(2**31),
                2**31 - 1,
                (k // GROUP, n // 8),
                dtype=torch.int32,
                device="cuda",
            )
            scales = (
                torch.rand(k // GROUP, n, dtype=torch.float16, device="cuda") * 0.01
            )
            ring.append((qweight, qzeros, scales))
        entry = {
            "K": k,
            "N": n,
            "roles": [r for r, _ in roles],
            "weight_bytes": weight_bytes,
            "rotation_copies": copies,
            "by_m": {},
        }
        for m in GPTQ_ROWS:
            a = torch.randn(m, k, dtype=torch.float16, device="cuda")
            nxt = rotating(ring)

            def call(a=a, nxt=nxt):
                qweight, qzeros, scales = nxt()
                ops.gptq_gemm_rdna3(a, qweight, qzeros, scales, False)

            t = timed(torch, call, args.iters, args.warmup)
            entry["by_m"][str(m)] = {
                **t,
                "us": round(t["median_ms"] * 1000, 2),
                "gbps": gbps(weight_bytes, t["median_ms"]),
            }
            for role, _ in roles:
                per_role_ms.setdefault(role, {})[m] = t["median_ms"]
        results.append(entry)
        del ring
        torch.cuda.empty_cache()

    def forward_sum(m, roles=None):
        total = 0.0
        for role, _k, _n, count in GPTQ_LAYERS:
            if roles is not None and role not in roles:
                continue
            total += (count if roles is None else 1) * per_role_ms[role][m]
        return total

    target = {str(m): round(forward_sum(m), 4) for m in GPTQ_ROWS}
    mtp = {str(m): round(forward_sum(m, MTP_LAYER_ROLES), 4) for m in GPTQ_ROWS}
    return {
        "shapes": results,
        "target_forward_sum_ms": target,
        "mtp_layer_sum_ms": mtp,
        "ratio_m4_over_m3": round(target["4"] / target["3"], 4),
        "ratio_m8_over_m6": round(target["8"] / target["6"], 4),
    }


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("--iters", type=int, default=200)
    parser.add_argument("--lm-iters", type=int, default=60)
    parser.add_argument("--warmup", type=int, default=10)
    parser.add_argument("--bandwidth-gib", type=int, default=2)
    parser.add_argument(
        "--check", action="store_true", help="also compare wvSplitK with F.linear"
    )
    parser.add_argument("--out", default="/tmp/s4-bench.json")
    args = parser.parse_args(argv)

    import torch

    if not torch.cuda.is_available():
        print("no GPU visible", file=sys.stderr)
        return 2
    free, total = torch.cuda.mem_get_info()
    if free < 8 * 2**30:
        print(
            f"only {free / 2**30:.1f} GiB free of {total / 2**30:.1f}; is an engine running?",
            file=sys.stderr,
        )
        return 3
    from vllm import _custom_ops as ops

    started = time.time()
    summary = {
        "facts": device_facts(torch),
        "free_gib_at_start": round(free / 2**30, 2),
    }
    # Bring the clocks up before the first timed sample.
    warm = torch.randn(4096, 4096, dtype=torch.float16, device="cuda")
    for _ in range(50):
        warm @ warm
    torch.cuda.synchronize()
    del warm
    summary["bandwidth"] = bench_bandwidth(torch, args)
    summary["lm_head"] = bench_lm_head(
        torch, ops, args, summary["bandwidth"]["read_ceiling_gbps"]
    )
    summary["gptq_gemm_rdna3"] = bench_gptq(torch, ops, args)
    summary["elapsed_s"] = round(time.time() - started, 1)
    text = json.dumps(summary, indent=1)
    try:
        with open(args.out, "w") as handle:
            handle.write(text)
    except OSError:
        pass
    print("=== BEGIN S4 BENCH JSON ===")
    print(text)
    print("=== END S4 BENCH JSON ===")
    return 0


if __name__ == "__main__":
    sys.exit(main())
