"""RDNA3 W4A16 GPTQ GEMM for vLLM with persistent split-K tickets.

vLLM's RDNA3W4A16LinearKernel calls ``torch.ops._rocm_C.gptq_gemm_rdna3``.
Its fp16 path allocates the split-K arrival tickets with ``at::zeros`` on
every call: one fill kernel and one dispatch gap in front of every GEMM, 260
per decode step of the Qwen3.8-27B workhorse, about 3.4 % of the step on the
7900 XTX (.loom/70-result-s4-step0-decode-anatomy-2026-11-05.md §4). This
package carries vLLM v0.33.0's kernel source with that allocation replaced by
a pool that is zeroed once (the upstream change in ``upstream/``), compiled
into its own op library, and swaps it in as the CUDA kernel of
``_rocm_C::gptq_gemm_rdna3``. The GEMM kernel's code is unchanged, so outputs
are bit-identical to vLLM's.

Opt-in per lane with the environment variable ``FLEXINFER_RDNA3_W4A16``:

* unset, empty or ``off``: nothing happens (vLLM's own kernel runs);
* ``tickets``: the persistent-ticket kernel;
* ``dot2``: the dot2 candidate for fp16 M = 1..10 where it fits, the
  persistent-ticket kernel elsewhere. Measurement only: its accumulation
  order differs, so outputs are not bit-identical.

Any other value fails plugin loading on purpose, so a typo cannot turn an A/B
arm into a silent copy of the control.
"""

from __future__ import annotations

import importlib.util
import logging
import os
from collections.abc import Mapping

ENV = "FLEXINFER_RDNA3_W4A16"
MODES = {"off": 0, "0": 0, "tickets": 1, "1": 1, "dot2": 2, "2": 2}
MODE_NAMES = {0: "off", 1: "tickets", 2: "dot2"}
# vLLM releases whose csrc/rocm/q_gemm_rdna3.cu and qdq_4_rdna3.cuh equal the
# vendored copy's base (v0.33.0, identical to v0.32.0) and whose
# gptq_gemm_rdna3 schema matches. Any other release keeps its own kernel.
SUPPORTED_VLLM = ("0.32.0", "0.33.0")
# The file string csrc/bindings.cpp registers the override under.
OVERRIDE_MARKER = "flexinfer_rdna3_w4a16_override"
OP = "_rocm_C::gptq_gemm_rdna3"


def _logger() -> logging.Logger:
    try:
        from vllm.logger import init_logger

        return init_logger(__name__)
    except Exception:  # pragma: no cover - outside vLLM
        return logging.getLogger(__name__)


def requested_mode(environ: Mapping[str, str] | None = None) -> int:
    """Mode from FLEXINFER_RDNA3_W4A16; raises ValueError on an unknown value."""
    raw = (os.environ if environ is None else environ).get(ENV, "")
    value = raw.strip().lower()
    if not value:
        return 0
    if value not in MODES:
        raise ValueError(f"{ENV}={raw!r}: expected off, tickets or dot2 (or unset)")
    return MODES[value]


def vllm_release(version: str) -> str:
    """``0.33.0+rocm723`` -> ``0.33.0``."""
    return version.split("+", 1)[0]


def library_path() -> str:
    spec = importlib.util.find_spec(f"{__name__}._C")
    if spec is None or not spec.origin:
        raise RuntimeError(f"{__name__}: compiled library _C is not installed")
    return spec.origin


def active_cuda_kernel(torch_module) -> str:
    """The CUDA line of the op's computed dispatch table ('' if none)."""
    table = torch_module._C._dispatch_dump_table(OP)
    for line in table.splitlines():
        if line.startswith("CUDA:") or line.startswith("CUDA "):
            return line
    return ""


def install(mode: int, torch_module=None) -> str:
    """Load the library and make ``mode`` the active kernel of the op.

    Needs ``_rocm_C`` loaded (``import vllm._custom_ops``). Returns the
    dispatch table's CUDA line after the change, and raises when it does not
    show the requested state.
    """
    if torch_module is None:
        import torch as torch_module
    if not hasattr(torch_module.ops.flexinfer_rdna3, "install"):
        torch_module.ops.load_library(library_path())
    torch_module.ops.flexinfer_rdna3.install(mode)
    line = active_cuda_kernel(torch_module)
    if (OVERRIDE_MARKER in line) != (mode != 0):
        raise RuntimeError(
            f"{__name__}: asked for mode {MODE_NAMES[mode]} but the active "
            f"CUDA kernel of {OP} is {line!r}"
        )
    return line


def register() -> None:
    """vLLM general plugin entry point; runs in every vLLM process."""
    mode = requested_mode()
    if mode == 0:
        return
    log = _logger()

    import torch

    if torch.version.hip is None:
        log.info("%s: %s=%s ignored: not a ROCm build", __name__, ENV, MODE_NAMES[mode])
        return
    from vllm.platforms import current_platform

    if not current_platform.is_rocm():
        log.info(
            "%s: %s=%s ignored: the vLLM platform is not ROCm",
            __name__,
            ENV,
            MODE_NAMES[mode],
        )
        return
    # Only importable once the platform is ROCm: on a host with no visible
    # GPU its module-level arch probe raises.
    from vllm.platforms.rocm import on_gfx1100

    if not on_gfx1100():
        log.info(
            "%s: %s=%s ignored: not a gfx1100 device", __name__, ENV, MODE_NAMES[mode]
        )
        return

    import vllm

    release = vllm_release(vllm.__version__)
    if release not in SUPPORTED_VLLM:
        log.warning(
            "%s: %s=%s ignored: vLLM %s is not one of %s, whose RDNA3 W4A16 "
            "kernel source the vendored copy matches",
            __name__,
            ENV,
            MODE_NAMES[mode],
            vllm.__version__,
            ", ".join(SUPPORTED_VLLM),
        )
        return

    import vllm._custom_ops  # noqa: F401  registers the fake kernels
    import vllm._rocm_C  # noqa: F401  the op's schema; _custom_ops loads it on ROCm

    if not hasattr(torch.ops._rocm_C, "gptq_gemm_rdna3"):
        log.warning(
            "%s: %s=%s ignored: this vLLM build has no %s",
            __name__,
            ENV,
            MODE_NAMES[mode],
            OP,
        )
        return

    line = install(mode, torch)
    log.info(
        "%s: %s=%s, the CUDA kernel of %s is now %s (pid %d)",
        __name__,
        ENV,
        MODE_NAMES[mode],
        OP,
        line.split(":", 1)[-1].strip(),
        os.getpid(),
    )
