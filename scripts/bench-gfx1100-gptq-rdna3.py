#!/usr/bin/env python3
"""A/B microbench of the RDNA3 W4A16 GEMM: vLLM's kernel, persistent tickets, dot2.

Work item GPTQ-RDNA3 (.loom/70-result-gptq-rdna3-*.md). Run it inside a
serving image that carries build/vllm-rdna3-w4a16-plugin while no engine
holds the card (a canary's hold posture), so the whole model's GPTQ weights
fit next to each other:

    kubectl -n flexinfer-system exec -i <canary-pod> -- \
        python3 - --out /tmp/gptq-rdna3.json < scripts/bench-gfx1100-gptq-rdna3.py

Arms, all in one process (the plugin's install() swaps the CUDA kernel of
_rocm_C::gptq_gemm_rdna3; the direct ops need no swap):

  stock     torch.ops._rocm_C.gptq_gemm_rdna3 with the override removed
            (vLLM's own kernel: a fresh at::zeros ticket array per call);
  tickets   torch.ops.flexinfer_rdna3.gptq_gemm (same kernel code, tickets
            from a pool zeroed once);
  dot2-wN   torch.ops.flexinfer_rdna3.gptq_gemm_dot2 with N waves per block
            (N = 0 is the automatic choice), where the shape fits.

Measurements:

 1. correctness, per shape and M: tickets must equal stock bit for bit;
    every arm's max abs error against an fp64 reference of the dequantized
    weights (real GPTQ packing, then gptq_shuffle as vLLM does);
 2. per-call time (HIP events, median), arms interleaved round-robin so
    clock drift hits all arms alike, with weight copies rotated past the
    96 MB Infinity Cache; GB/s of weight bytes per call;
 3. forward: one CUDA graph per arm and M holding the 256 GPTQ GEMMs of one
    Qwen3.8-27B target forward in layer order, each with its own weights
    (the model's 12.6 GB), replayed and timed; this is the decode step's GPTQ
    time including the dispatch gaps and, for stock, the 256 fill kernels;
 4. graph safety of the ticket pool: a graph captured on a side stream
    replayed many times, two graphs captured on one stream replayed
    alternately, eager calls racing on two streams, and a graph captured
    before any eager call (the per-call fallback) all equal eager stock;
 5. kernel census: torch-profiler kernel counts for 10 calls per arm
    (stock shows one fill per GEMM, tickets none).

The JSON summary goes to stdout between BEGIN/END markers and to --out.
"""

import argparse
import json
import math
import statistics
import sys
import time

HIDDEN = 5120
GROUP = 128
LAYERS = 64
# Qwen3.8-27B: 48 GDN + 16 full-attention layers, full attention every 4th.
FULL_ATTN_EVERY = 4
# (role, K, N); see scripts/bench-gfx1100-decode-kernels.py for the sources.
SHAPES = {
    "attn.qkv_proj": (5120, 14336),
    "attn.o_proj": (6144, 5120),
    "gdn.in_proj_qkvz": (5120, 16384),
    "gdn.out_proj": (6144, 5120),
    "mlp.gate_up_proj": (5120, 34816),
    "mlp.down_proj": (17408, 5120),
}
ROWS = (1, 2, 3, 4, 6, 8)
INFINITY_CACHE_BYTES = 96 * 2**20
ROTATION_BYTES = 6 * INFINITY_CACHE_BYTES


def layer_roles():
    """The 256 GPTQ GEMMs of one target forward, in execution order."""
    roles = []
    for layer in range(LAYERS):
        if (layer + 1) % FULL_ATTN_EVERY == 0:
            roles += ["attn.qkv_proj", "attn.o_proj"]
        else:
            roles += ["gdn.in_proj_qkvz", "gdn.out_proj"]
        roles += ["mlp.gate_up_proj", "mlp.down_proj"]
    return roles


def weight_bytes(k, n):
    return k * n // 2 + (k // GROUP) * n * 2 + (k // GROUP) * (n // 8) * 4


def make_weight(torch, ops, k, n, gen=None):
    """Random GPTQ weight in vLLM's kernel layout plus its fp64 dequantization.

    Returns (qweight_shuffled, qzeros, scales, w_ref or None). Packing: nibble
    i of qweight[r, c] is row 8r + i; nibble j of qzeros[g, c] is column
    8c + j; GPTQv1 stores zero - 1 (use_v2_format=False adds the 1 back).
    """
    q = torch.randint(0, 16, (k, n), dtype=torch.int32, device="cuda", generator=gen)
    z = torch.randint(
        0, 15, (k // GROUP, n), dtype=torch.int32, device="cuda", generator=gen
    )
    s = (
        torch.rand(k // GROUP, n, dtype=torch.float16, device="cuda", generator=gen)
        * 0.02
        + 0.001
    )
    # Pack in int64 (no sign wrap), then reinterpret the low 32 bits as int32.
    shifts = torch.arange(8, device="cuda", dtype=torch.int64) * 4
    qw = (q.view(k // 8, 8, n).to(torch.int64) << shifts.view(1, 8, 1)).sum(dim=1)
    qw = torch.where(qw >= 2**31, qw - 2**32, qw).to(torch.int32).contiguous()
    qz = (z.view(k // GROUP, n // 8, 8).to(torch.int64) << shifts.view(1, 1, 8)).sum(
        dim=2
    )
    qz = torch.where(qz >= 2**31, qz - 2**32, qz).to(torch.int32).contiguous()
    ops.gptq_shuffle(qw, 4)
    return qw, qz, s.contiguous(), (q, z, s)


def dequant_ref(torch, packed):
    q, z, s = packed
    g = torch.arange(q.shape[0], device="cuda") // GROUP
    return (q.double() - (z.double()[g] + 1.0)) * s.double()[g]


def timed_rounds(torch, fns, iters, warmup):
    """Median ms per arm; arms interleaved round-robin, HIP events per call."""
    for fn in fns.values():
        for _ in range(warmup):
            fn()
    torch.cuda.synchronize()
    names = list(fns)
    events = {name: [] for name in names}
    for i in range(iters):
        order = names[i % len(names) :] + names[: i % len(names)]
        for name in order:
            start = torch.cuda.Event(enable_timing=True)
            end = torch.cuda.Event(enable_timing=True)
            start.record()
            fns[name]()
            end.record()
            events[name].append((start, end))
    torch.cuda.synchronize()
    out = {}
    for name in names:
        samples = sorted(s.elapsed_time(e) for s, e in events[name])
        out[name] = {
            "median_ms": round(statistics.median(samples), 5),
            "min_ms": round(samples[0], 5),
            "p90_ms": round(samples[min(len(samples) - 1, int(0.9 * len(samples)))], 5),
        }
    return out


def gbps(nbytes, ms):
    return round(nbytes / (ms * 1e-3) / 1e9, 1) if ms > 0 else None


def arms_for(torch, a, qw, qz, s, waves_list):
    fl = torch.ops.flexinfer_rdna3
    arms = {
        "stock": lambda: torch.ops._rocm_C.gptq_gemm_rdna3(a, qw, qz, s, False),
        "tickets": lambda: fl.gptq_gemm(a, qw, qz, s, False),
    }
    for w in waves_list:
        if fl.dot2_fits(a, qw, qz, s, w):
            arms[f"dot2-w{w}"] = lambda w=w: fl.gptq_gemm_dot2(a, qw, qz, s, False, w)
    return arms


def bench_correctness(torch, ops, args):
    gen = torch.Generator(device="cuda")
    gen.manual_seed(1234)
    rows = []
    for role, (k, n) in SHAPES.items():
        qw, qz, s, packed = make_weight(torch, ops, k, n, gen)
        w_ref = dequant_ref(torch, packed)
        for m in ROWS:
            a = (
                torch.randn(m, k, dtype=torch.float16, device="cuda", generator=gen)
                * 0.5
            ).contiguous()
            ref = a.double() @ w_ref
            scale = ref.abs().max().item()
            row = {"role": role, "K": k, "N": n, "M": m, "ref_absmax": round(scale, 4)}
            outs = {
                name: fn()
                for name, fn in arms_for(torch, a, qw, qz, s, args.waves).items()
            }
            for name, out in outs.items():
                row[f"{name}_max_abs_err"] = round(
                    (out.double() - ref).abs().max().item(), 6
                )
            row["tickets_bit_equal_stock"] = bool(
                torch.equal(outs["tickets"], outs["stock"])
            )
            for name, out in outs.items():
                if name.startswith("dot2"):
                    row[f"{name}_max_abs_diff_vs_stock"] = round(
                        (out.float() - outs["stock"].float()).abs().max().item(), 6
                    )
            rows.append(row)
        del qw, qz, s, packed, w_ref
        torch.cuda.empty_cache()
    return {
        "rows": rows,
        "tickets_bit_equal_all": all(r["tickets_bit_equal_stock"] for r in rows),
    }


def bench_per_call(torch, ops, args):
    results = []
    for role, (k, n) in SHAPES.items():
        if role == "gdn.out_proj":
            continue  # same shape as attn.o_proj
        nbytes = weight_bytes(k, n)
        copies = max(2, math.ceil(ROTATION_BYTES / nbytes))
        ring = [make_weight(torch, ops, k, n)[:3] for _ in range(copies)]
        entry = {
            "role": role,
            "K": k,
            "N": n,
            "weight_bytes": nbytes,
            "rotation_copies": copies,
            "by_m": {},
        }
        for m in ROWS:
            a = torch.randn(m, k, dtype=torch.float16, device="cuda").contiguous()
            state = {"i": 0}

            def nxt():
                item = ring[state["i"]]
                state["i"] = (state["i"] + 1) % len(ring)
                return item

            probe = ring[0]
            names = list(arms_for(torch, a, *probe, args.waves))
            fl = torch.ops.flexinfer_rdna3

            def make(name):
                if name == "stock":
                    return lambda: torch.ops._rocm_C.gptq_gemm_rdna3(a, *nxt(), False)
                if name == "tickets":
                    return lambda: fl.gptq_gemm(a, *nxt(), False)
                w = int(name.split("-w")[1])
                return lambda: fl.gptq_gemm_dot2(a, *nxt(), False, w)

            t = timed_rounds(
                torch, {nm: make(nm) for nm in names}, args.iters, args.warmup
            )
            entry["by_m"][str(m)] = {
                nm: {
                    **v,
                    "us": round(v["median_ms"] * 1000, 2),
                    "gbps": gbps(nbytes, v["median_ms"]),
                }
                for nm, v in t.items()
            }
        results.append(entry)
        del ring
        torch.cuda.empty_cache()
    return results


def bench_forward_graph(torch, ops, args, plugin):
    """Graph of one target forward's 256 GPTQ GEMMs, per arm and M."""
    roles = layer_roles()
    free, _ = torch.cuda.mem_get_info()
    need = sum(weight_bytes(*SHAPES[r]) for r in roles)
    if free < need + 2 * 2**30:
        return {"skipped": f"needs {need / 2**30:.1f} GiB + 2, {free / 2**30:.1f} free"}
    weights = [make_weight(torch, ops, *SHAPES[r])[:3] for r in roles]
    out = {"gemms": len(roles), "weight_gib": round(need / 2**30, 2), "by_m": {}}
    fl = torch.ops.flexinfer_rdna3
    stream = torch.cuda.Stream()
    for m in args.graph_rows:
        acts = {
            k: torch.randn(m, k, dtype=torch.float16, device="cuda")
            for k in {SHAPES[r][0] for r in roles}
        }
        arm_fns = {
            "stock": lambda a, w: torch.ops._rocm_C.gptq_gemm_rdna3(a, *w, False),
            "tickets": lambda a, w: fl.gptq_gemm(a, *w, False),
            "override-tickets": "install1",
            "dot2-auto": lambda a, w: (
                fl.gptq_gemm_dot2(a, *w, False, 0)
                if fl.dot2_fits(a, *w, 0)
                else fl.gptq_gemm(a, *w, False)
            ),
        }
        graphs = {}
        for name, fn in arm_fns.items():
            if fn == "install1":
                # The production path: vLLM's op name, our kernel behind it.
                plugin.install(1, torch)
                fn = arm_fns["stock"]
            else:
                plugin.install(0, torch)
            # Eager warm-up on the capture stream, as vLLM's warm-up runs do.
            with torch.cuda.stream(stream):
                for r, w in zip(roles, weights):
                    fn(acts[SHAPES[r][0]], w)
            torch.cuda.synchronize()
            g = torch.cuda.CUDAGraph()
            with torch.cuda.graph(g, stream=stream):
                for r, w in zip(roles, weights):
                    fn(acts[SHAPES[r][0]], w)
            graphs[name] = g
        plugin.install(0, torch)
        t = timed_rounds(
            torch, {nm: g.replay for nm, g in graphs.items()}, args.graph_iters, 3
        )
        out["by_m"][str(m)] = {
            nm: {**v, "gbps": gbps(need, v["median_ms"])} for nm, v in t.items()
        }
        del graphs
    del weights
    torch.cuda.empty_cache()
    return out


def check_graph_safety(torch, ops, plugin):
    """Ticket-pool cases that must equal eager stock output exactly."""
    plugin.install(0, torch)
    k, n = SHAPES["mlp.down_proj"]
    qw, qz, s, _ = make_weight(torch, ops, k, n)
    fl = torch.ops.flexinfer_rdna3
    res = {}
    # 0. A graph captured before the ticket pool exists takes the per-call
    #    fallback (the zero fill is captured into the graph). Load the kernel
    #    module first with a K = 256 call: one split, so no tickets, no pool.
    k0, n0 = 256, 5120
    qw0, qz0, s0, _ = make_weight(torch, ops, k0, n0)
    a0 = torch.randn(3, k0, dtype=torch.float16, device="cuda")
    fl.gptq_gemm(a0, qw0, qz0, s0, False)
    a_early = torch.randn(3, k, dtype=torch.float16, device="cuda")
    ref_early = torch.ops._rocm_C.gptq_gemm_rdna3(a_early, qw, qz, s, False)
    early = torch.cuda.Stream()
    torch.cuda.synchronize()
    g0 = torch.cuda.CUDAGraph()
    with torch.cuda.graph(g0, stream=early):
        o0 = fl.gptq_gemm(a_early, qw, qz, s, False)
    ok0 = True
    for _ in range(20):
        g0.replay()
        torch.cuda.synchronize()
        ok0 &= bool(torch.equal(o0, ref_early))
    res["capture_before_pool"] = ok0
    for m in (1, 3, 6):
        a = torch.randn(m, k, dtype=torch.float16, device="cuda")
        ref = torch.ops._rocm_C.gptq_gemm_rdna3(a, qw, qz, s, False)
        side = torch.cuda.Stream()
        # 1. graph on a side stream, replayed 50 times
        with torch.cuda.stream(side):
            fl.gptq_gemm(a, qw, qz, s, False)
        torch.cuda.synchronize()
        g1 = torch.cuda.CUDAGraph()
        with torch.cuda.graph(g1, stream=side):
            o1 = fl.gptq_gemm(a, qw, qz, s, False)
            o1b = fl.gptq_gemm(a, qw, qz, s, False)
        ok1 = True
        for _ in range(50):
            g1.replay()
            torch.cuda.synchronize()
            ok1 &= bool(torch.equal(o1, ref) and torch.equal(o1b, ref))
        # 2. a second graph on the same capture stream, replayed alternately
        a2 = torch.randn(m, k, dtype=torch.float16, device="cuda")
        ref2 = torch.ops._rocm_C.gptq_gemm_rdna3(a2, qw, qz, s, False)
        g2 = torch.cuda.CUDAGraph()
        with torch.cuda.graph(g2, stream=side):
            o2 = fl.gptq_gemm(a2, qw, qz, s, False)
        ok2 = True
        for _ in range(25):
            g2.replay()
            g1.replay()
            torch.cuda.synchronize()
            ok2 &= bool(torch.equal(o2, ref2) and torch.equal(o1, ref))
        # 3. eager calls racing on two streams
        s_a, s_b = torch.cuda.Stream(), torch.cuda.Stream()
        ok3 = True
        for _ in range(25):
            with torch.cuda.stream(s_a):
                xa = fl.gptq_gemm(a, qw, qz, s, False)
            with torch.cuda.stream(s_b):
                xb = fl.gptq_gemm(a2, qw, qz, s, False)
            torch.cuda.synchronize()
            ok3 &= bool(torch.equal(xa, ref) and torch.equal(xb, ref2))
        res[str(m)] = {
            "side_stream_graph": ok1,
            "two_graphs_one_stream": ok2,
            "two_eager_streams": ok3,
        }
    return res


def kernel_census(torch, ops, plugin):
    from torch.profiler import ProfilerActivity, profile

    k, n = SHAPES["mlp.gate_up_proj"]
    qw, qz, s, _ = make_weight(torch, ops, k, n)
    a = torch.randn(3, k, dtype=torch.float16, device="cuda")
    fl = torch.ops.flexinfer_rdna3
    out = {}
    for name, fn in (
        ("stock", lambda: torch.ops._rocm_C.gptq_gemm_rdna3(a, qw, qz, s, False)),
        ("tickets", lambda: fl.gptq_gemm(a, qw, qz, s, False)),
    ):
        plugin.install(0, torch)
        fn()
        torch.cuda.synchronize()
        with profile(activities=[ProfilerActivity.CUDA]) as prof:
            for _ in range(10):
                fn()
            torch.cuda.synchronize()
        counts = {}
        for ev in prof.events():
            if ev.device_type is not None and "CUDA" in str(ev.device_type):
                counts[ev.name[:80]] = counts.get(ev.name[:80], 0) + 1
        out[name] = counts
    return out


def main(argv=None):
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument("--iters", type=int, default=200)
    parser.add_argument("--warmup", type=int, default=10)
    parser.add_argument("--graph-iters", type=int, default=50)
    parser.add_argument("--graph-rows", type=int, nargs="+", default=[1, 3, 6])
    parser.add_argument("--waves", type=int, nargs="+", default=[0, 8, 16, 32])
    parser.add_argument(
        "--skip",
        nargs="*",
        default=[],
        choices=["correctness", "per_call", "forward", "safety", "census"],
    )
    parser.add_argument("--out", default="/tmp/gptq-rdna3.json")
    args = parser.parse_args(argv)

    import torch

    if not torch.cuda.is_available():
        print("no GPU visible", file=sys.stderr)
        return 2
    free, total = torch.cuda.mem_get_info()
    if free < 16 * 2**30:
        print(
            f"only {free / 2**30:.1f} GiB free of {total / 2**30:.1f}; is an engine running?",
            file=sys.stderr,
        )
        return 3
    import vllm
    from vllm import _custom_ops as ops
    import flexinfer_vllm_rdna3_w4a16 as plugin

    plugin.install(0, torch)  # loads the library, keeps vLLM's kernel active
    started = time.time()
    props = torch.cuda.get_device_properties(0)
    summary = {
        "facts": {
            "device": torch.cuda.get_device_name(0),
            "gcn_arch": getattr(props, "gcnArchName", ""),
            "vllm": vllm.__version__,
            "torch": torch.__version__,
            "plugin_library": plugin.library_path(),
            "free_gib_at_start": round(free / 2**30, 2),
        }
    }
    warm = torch.randn(4096, 4096, dtype=torch.float16, device="cuda")
    for _ in range(50):
        warm @ warm
    torch.cuda.synchronize()
    del warm
    steps = (
        # safety first: its case 0 needs a process with no ticket pool yet
        ("safety", lambda: check_graph_safety(torch, ops, plugin)),
        ("correctness", lambda: bench_correctness(torch, ops, args)),
        ("census", lambda: kernel_census(torch, ops, plugin)),
        ("per_call", lambda: bench_per_call(torch, ops, args)),
        ("forward", lambda: bench_forward_graph(torch, ops, args, plugin)),
    )
    for name, fn in steps:
        if name in args.skip:
            continue
        t0 = time.time()
        try:
            summary[name] = fn()
        except Exception as exc:  # noqa: BLE001 - keep the other steps
            summary[name] = {"error": f"{type(exc).__name__}: {exc}"}
        summary.setdefault("step_seconds", {})[name] = round(time.time() - t0, 1)
        torch.cuda.empty_cache()
    plugin.install(0, torch)
    summary["elapsed_s"] = round(time.time() - started, 1)
    text = json.dumps(summary, indent=1)
    try:
        with open(args.out, "w") as handle:
            handle.write(text)
    except OSError:
        pass
    print("=== BEGIN GPTQ-RDNA3 BENCH JSON ===")
    print(text)
    print("=== END GPTQ-RDNA3 BENCH JSON ===")
    return 0


if __name__ == "__main__":
    sys.exit(main())
