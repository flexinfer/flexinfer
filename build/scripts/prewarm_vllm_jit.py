#!/usr/bin/env python3
"""Pre-warm vLLM JIT caches at Docker build time.

Motivation: vLLM 0.19+ on ROCm gfx1100 compiles several pieces of native code
on first import / first forward pass:

  - AITER's `module_aiter_enum` HIP module (via clang++ with --offload-arch=gfx1100)
  - vLLM's `_custom_ops` and custom-fusion compilation cache (norm_quant, act_quant)
  - Triton MoE kernel JIT compile

These take 10-30+ seconds total on cold start and can push pod-startup past the
kubelet readiness probe budget when a fresh node hasn't seen this image before
(or when Flux triggers a Deployment recreate, invalidating ephemeral caches).

This script runs at build time so the compiled artifacts land in the image
layer. Pod cold-loads then skip the slow path.

Cache locations (image-baked):
  /opt/venv/lib/python3.12/site-packages/aiter/jit/build/   AITER .so files
  /root/.cache/vllm/                                        vLLM compilation cache
  /root/.cache/triton/                                      Triton JIT cache (partial)

Run as part of `Dockerfile.runtime` AFTER vLLM is installed and any source
patches are applied. The script is best-effort: it should not fail the build
on a non-critical warm-up step. Hard failures (e.g. import errors) do fail
because they indicate a real problem with the install.

Refs:
  .loom/slice3-v1-sandbox-rms-norm-falsified-2026-05-15.md (post-promotion
    failure section; the cold-load issue this script addresses)
  .loom/21-product-spec-vllm-feature-parity-2026-05-15.md (Slice 7 forward
    option Z2: pre-warm JIT caches)
"""

from __future__ import annotations

import os
import pathlib
import sys
import time


def _section(name: str) -> None:
    print(f"\n[prewarm] === {name} ===")


def _ls_count(path: str) -> int:
    """Count files/dirs in a path, 0 if missing. Recursive."""
    p = pathlib.Path(path)
    if not p.exists():
        return 0
    return sum(1 for _ in p.rglob("*"))


def _summarize_caches(label: str) -> None:
    """Print cache directory sizes/counts for logging."""
    print(f"[prewarm] cache state {label}:")
    for path in (
        "/opt/venv/lib/python3.12/site-packages/aiter/jit/build",
        "/opt/venv/lib/python3.12/site-packages/aiter/jit",
        "/root/.cache/vllm",
        "/root/.cache/triton",
        "/var/lib/flexinfer/compile-cache",
    ):
        count = _ls_count(path)
        exists = "exists" if pathlib.Path(path).exists() else "missing"
        print(f"  {path}: {exists}, {count} entries")


def main() -> int:
    t_start = time.time()
    print(f"[prewarm] start (python={sys.version.split()[0]}, cwd={os.getcwd()})")
    _summarize_caches("before")

    # 1. Import vLLM core. Triggers any module-level compile in vllm._custom_ops,
    #    pulls in torch + HIP runtime initialization (without GPU device init).
    _section("import vllm core")
    import vllm  # noqa: F401

    print(f"[prewarm] vllm.__version__ = {vllm.__version__}")
    from vllm import _custom_ops as ops  # noqa: F401

    print(f"[prewarm] vllm._custom_ops OK")

    # 2. Import the gemma4 model executor module. This triggers AITER's
    #    `start build [module_aiter_enum]` HIP compile on first import — the
    #    most visible slow step in the pod startup logs.
    _section("import vllm gemma4 model executor")
    from vllm.model_executor.models.gemma4 import Gemma4ForCausalLM, Gemma4MoE

    print(
        f"[prewarm] gemma4 model classes OK: {Gemma4ForCausalLM.__name__}, {Gemma4MoE.__name__}"
    )

    # 3. Import the relevant layer modules. RMSNorm imports trigger the
    #    forward_hip code path registration; FusedMoE import registers the
    #    Triton kernel module (compile happens lazily on first call).
    _section("import layer modules")
    from vllm.model_executor.layers.layernorm import RMSNorm, GemmaRMSNorm
    from vllm.model_executor.layers.fused_moe import FusedMoE

    print(
        f"[prewarm] layer imports OK: {RMSNorm.__name__}, {GemmaRMSNorm.__name__}, {FusedMoE.__name__}"
    )

    # 4. Import the attention backend modules so any per-platform dispatch
    #    table is initialized.
    _section("import attention backends")
    try:
        from vllm.attention import Attention  # noqa: F401

        print("[prewarm] vllm.attention.Attention OK")
    except ImportError as e:
        print(f"[prewarm] vllm.attention import failed (non-fatal): {e}")

    # 5. Force AITER JIT build directly. Pod logs show
    #    "[aiter] start build [module_aiter_enum]" during engine init, which
    #    suggests aiter's submodules are loaded lazily. Import the same
    #    submodule explicitly here so the .so is built into the image layer.
    #    Best-effort: if aiter isn't available, skip; if the build fails, log
    #    and continue (runtime can still JIT at pod startup as fallback).
    _section("trigger AITER JIT build")
    try:
        import aiter  # noqa: F401

        print(
            f"[prewarm] aiter package imported (version={getattr(aiter, '__version__', '<unknown>')})"
        )
        # Try importing the specific submodule observed in pod logs. AITER may
        # expose it under aiter.jit.* depending on version; try several paths.
        triggered = False
        for candidate in (
            "aiter.ops.shuffle",
            "aiter.jit.module_aiter_enum",
            "aiter.ops.norm",
        ):
            try:
                __import__(candidate)
                print(f"[prewarm] {candidate} imported (build may have triggered)")
                triggered = True
            except Exception as e:
                print(f"[prewarm] {candidate} import failed: {type(e).__name__}: {e}")
        if not triggered:
            print(
                "[prewarm] no AITER submodule imports succeeded; cold-load may still JIT"
            )
    except ImportError as e:
        print(f"[prewarm] aiter package not available (skip): {e}")

    # 5. Final summary — show what landed in the cache dirs.
    _section("cache state after warmup")
    _summarize_caches("after")

    # AITER may have built artifacts into either jit/ (when run directly) or
    # jit/build/ (the "start build" path observed in pod logs). Either is
    # acceptable; report what we see.
    aiter_root = pathlib.Path("/opt/venv/lib/python3.12/site-packages/aiter/jit")
    so_files = list(aiter_root.rglob("*.so"))
    if so_files:
        print(f"[prewarm] AITER .so artifacts baked into image:")
        for so in so_files:
            try:
                size = so.stat().st_size
                print(f"  {so} ({size} bytes)")
            except OSError:
                print(f"  {so}")
    else:
        print("[prewarm] WARNING: no AITER .so files found post-warmup.")
        print("  Cold-load may still hit the JIT build step at pod startup.")

    elapsed = time.time() - t_start
    print(f"\n[prewarm] done in {elapsed:.1f}s")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
