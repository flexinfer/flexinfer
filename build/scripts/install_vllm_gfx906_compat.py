#!/usr/bin/env python3
"""Install legacy vLLM/gfx906 compatibility hooks into site-packages.

The standalone gfx906 vLLM image intentionally carries an old vLLM release
against a newer ROCm/PyTorch/Triton/Transformers base. Keep the hooks here so
the Dockerfile stays auditable and local validation can compile/import them.
"""

from __future__ import annotations

import argparse
import pathlib
import site
import textwrap


HOOKS = {
    "flexinfer_vllm_transformers_compat.py": r"""
        def _install():
            try:
                from transformers.tokenization_utils_base import PreTrainedTokenizerBase
            except Exception:
                return

            if hasattr(PreTrainedTokenizerBase, "all_special_tokens_extended"):
                return

            @property
            def all_special_tokens_extended(self):
                values = []
                try:
                    mapping = self.special_tokens_map_extended
                except Exception:
                    mapping = {}
                for value in mapping.values():
                    if isinstance(value, (list, tuple)):
                        values.extend(value)
                    else:
                        values.append(value)
                if values:
                    return values
                return list(getattr(self, "all_special_tokens", []))

            PreTrainedTokenizerBase.all_special_tokens_extended = all_special_tokens_extended

        _install()
    """,
    "flexinfer_vllm_triton_compat.py": r"""
        def _install():
            try:
                import os
                import pathlib
                import triton.runtime.cache as triton_cache
            except Exception:
                return

            def default_cache_dir():
                configured = os.environ.get("TRITON_CACHE_DIR")
                if configured:
                    return configured
                return str(pathlib.Path.home() / ".triton" / "cache")

            def default_dump_dir():
                configured = os.environ.get("TRITON_DUMP_DIR")
                if configured:
                    return configured
                return str(pathlib.Path.home() / ".triton" / "dump")

            def default_override_dir():
                configured = os.environ.get("TRITON_OVERRIDE_DIR")
                if configured:
                    return configured
                return str(pathlib.Path.home() / ".triton" / "override")

            if not hasattr(triton_cache, "default_cache_dir"):
                triton_cache.default_cache_dir = default_cache_dir
            if not hasattr(triton_cache, "default_dump_dir"):
                triton_cache.default_dump_dir = default_dump_dir
            if not hasattr(triton_cache, "default_override_dir"):
                triton_cache.default_override_dir = default_override_dir

        _install()
    """,
    "flexinfer_vllm_torch_rocm_compat.py": r"""
        def _install():
            try:
                import torch
            except Exception:
                return

            original = getattr(torch.cuda, "mem_get_info", None)
            if original is None or getattr(original, "_flexinfer_gfx906_safe", False):
                return

            def safe_mem_get_info(device=None):
                try:
                    return original(device)
                except Exception:
                    if device is None:
                        device = torch.cuda.current_device()
                    props = torch.cuda.get_device_properties(device)
                    total = int(getattr(props, "total_memory", 0) or 0)
                    try:
                        reserved = int(torch.cuda.memory_reserved(device))
                    except Exception:
                        reserved = 0
                    try:
                        allocated = int(torch.cuda.memory_allocated(device))
                    except Exception:
                        allocated = 0
                    used = max(reserved, allocated)
                    free = max(total - used, 0)
                    return free, total

            safe_mem_get_info._flexinfer_gfx906_safe = True
            torch.cuda.mem_get_info = safe_mem_get_info
            try:
                import torch.cuda.memory as cuda_memory
                cuda_memory.mem_get_info = safe_mem_get_info
            except Exception:
                pass

        _install()
    """,
    "flexinfer_vllm_torch_init_compat.py": r"""
        def _install():
            try:
                import torch
                import torch.nn.init as init
            except Exception:
                return

            if not getattr(torch.version, "hip", None):
                return

            def _patch_in_place(name):
                original = getattr(init, name, None)
                if original is None or getattr(original, "_flexinfer_gfx906_safe", False):
                    return

                def safe(tensor, *args, **kwargs):
                    if not tensor.is_cuda:
                        return original(tensor, *args, **kwargs)
                    cpu_tensor = torch.empty(
                        tensor.shape, dtype=tensor.dtype, device="cpu"
                    )
                    # Delegate to the original function on a CPU tensor so the
                    # CPU normal_/uniform_ kernel runs (the HIP RNG kernel is
                    # what segfaults on Vega20). This keeps signature parity
                    # for variants that pass a generator positionally or call
                    # a sequence of kernels (e.g. _no_grad_trunc_normal_ which
                    # is uniform_ + erfinv_ + mul_ + add_ + clamp_).
                    original(cpu_tensor, *args, **kwargs)
                    with torch.no_grad():
                        tensor.copy_(cpu_tensor)
                    return tensor

                safe._flexinfer_gfx906_safe = True
                setattr(init, name, safe)

            # gfx906/Vega20 segfaults inside the HIP random kernels invoked by
            # torch.Tensor.normal_/uniform_ during module __init__ (observed at
            # OPT-125M Embedding init via _no_grad_normal_). Random init is
            # overwritten by vLLM's pretrained-weight load, so route the in-place
            # init through CPU and copy back to the HIP tensor.
            _patch_in_place("_no_grad_normal_")
            _patch_in_place("_no_grad_uniform_")
            _patch_in_place("_no_grad_trunc_normal_")

        _install()
    """,
    "flexinfer_vllm_worker_diagnostics.py": r"""
        def _install():
            try:
                import faulthandler
                import multiprocessing.process
                import os
                import sys
                import traceback
            except Exception:
                return

            try:
                faulthandler.enable(all_threads=True)
            except Exception:
                pass

            original_hook = sys.excepthook
            if not getattr(original_hook, "_flexinfer_gfx906_diag", False):
                def excepthook(exc_type, exc, tb):
                    try:
                        print(
                            f"[flexinfer-gfx906-vllm] uncaught exception in pid={os.getpid()}",
                            file=sys.stderr,
                            flush=True,
                        )
                        traceback.print_exception(exc_type, exc, tb)
                    finally:
                        if original_hook not in (None, excepthook):
                            original_hook(exc_type, exc, tb)

                excepthook._flexinfer_gfx906_diag = True
                sys.excepthook = excepthook

            base_process = multiprocessing.process.BaseProcess
            original_run = base_process.run
            if getattr(original_run, "_flexinfer_gfx906_diag", False):
                return

            def run_with_trace(self):
                try:
                    return original_run(self)
                except BaseException:
                    print(
                        f"[flexinfer-gfx906-vllm] multiprocessing child failed "
                        f"pid={os.getpid()} name={getattr(self, 'name', '<unknown>')}",
                        file=sys.stderr,
                        flush=True,
                    )
                    traceback.print_exc()
                    raise

            run_with_trace._flexinfer_gfx906_diag = True
            base_process.run = run_with_trace

        _install()
    """,
}


PTH_IMPORTS = "\n".join(f"import {name.removesuffix('.py')}" for name in HOOKS) + "\n"


def default_site_packages() -> pathlib.Path:
    return pathlib.Path(site.getsitepackages()[0])


def install(target: pathlib.Path) -> None:
    target.mkdir(parents=True, exist_ok=True)
    for filename, source in HOOKS.items():
        target.joinpath(filename).write_text(textwrap.dedent(source).lstrip())
    target.joinpath("flexinfer_vllm_gfx906_compat.pth").write_text(PTH_IMPORTS)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--target",
        type=pathlib.Path,
        default=default_site_packages(),
        help="site-packages directory to install into",
    )
    args = parser.parse_args()
    install(args.target)
    print(f"installed gfx906 vLLM compatibility hooks into {args.target}")


if __name__ == "__main__":
    main()
