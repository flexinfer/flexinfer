#!/usr/bin/env python3
"""Fail a runtime build that lacks vLLM's native RDNA3 W4A16 GPTQ kernel."""

from __future__ import annotations

import importlib
import importlib.metadata
import pathlib
import re
from typing import Any, Callable


MINIMUM_VLLM_VERSION = (0, 23, 0)
KERNEL_RELATIVE_PATH = pathlib.Path(
    "model_executor/kernels/linear/mixed_precision/rdna3_w4a16.py"
)
TEXT_PLUGIN_ENTRY_POINT = "flexinfer_qwen35_text"


def _parse_version(raw: str) -> tuple[int, int, int]:
    match = re.match(r"^(\d+)\.(\d+)(?:\.(\d+))?", raw)
    if match is None:
        raise RuntimeError(f"cannot parse vLLM version: {raw!r}")
    return tuple(int(part or 0) for part in match.groups())


def verify_source_contract(vllm_root: pathlib.Path, version: str) -> None:
    """Verify the package contains the gfx1100-native GPTQ dispatch wrapper."""
    if _parse_version(version) < MINIMUM_VLLM_VERSION:
        raise RuntimeError(
            f"native gfx1100 W4A16 requires vLLM >= 0.23.0; found {version}"
        )

    kernel_path = vllm_root / KERNEL_RELATIVE_PATH
    if not kernel_path.is_file():
        raise RuntimeError(f"missing RDNA3 W4A16 kernel wrapper: {kernel_path}")

    source = kernel_path.read_text()
    if "gptq_gemm_rdna3" not in source:
        raise RuntimeError(
            f"{kernel_path} does not dispatch torch.ops._rocm_C.gptq_gemm_rdna3"
        )


def verify_compiled_contract(
    torch_module: Any,
    importer: Callable[[str], Any] = importlib.import_module,
) -> None:
    """Load the ROCm extension directly and verify its gfx1100 operator."""
    try:
        importer("vllm._rocm_C")
    except (ImportError, OSError) as exc:
        raise RuntimeError(
            "failed to load vLLM's compiled ROCm extension vllm._rocm_C"
        ) from exc

    rocm_ops = getattr(torch_module.ops, "_rocm_C", None)
    if rocm_ops is None or not hasattr(rocm_ops, "gptq_gemm_rdna3"):
        raise RuntimeError(
            "vLLM's compiled _rocm_C extension loaded but does not register "
            "gptq_gemm_rdna3"
        )


def verify_text_plugin_contract(entry_points: Any) -> None:
    """Verify the text-only Qwen3.5 adapter will load in every vLLM process."""
    plugins = entry_points(group="vllm.general_plugins")
    if not any(plugin.name == TEXT_PLUGIN_ENTRY_POINT for plugin in plugins):
        raise RuntimeError(
            "missing flexinfer_qwen35_text vLLM general plugin; text-only "
            "Qwen3.5 configs would fall through to the multimodal wrapper"
        )


def verify_text_plugin_registers(entry_points: Any, environ: dict) -> None:
    """Run the plugin's register() the way vLLM will, on this exact vLLM.

    The entry-point check above only proves the plugin is installed. The
    2026-09-13 S2 canary (issue #74) shipped an image whose plugin imported a
    vLLM 0.23 kernel path that 0.26 no longer has; every engine start died in
    load_general_plugins() before the model loaded. Executing register() at
    image-build time catches that class of drift with no GPU: it exercises
    every vLLM import the plugin relies on and the ModelRegistry hooks.
    PYTORCH_ROCM_ARCH is cleared for the call because the consumer-ROCm FLA
    autotuner pruning needs Triton's kernels, which a driverless build
    container does not load; that path is verified by the runtime log line
    (fla_autotuners > 0) on the first GPU start instead.
    """
    environ.pop("PYTORCH_ROCM_ARCH", None)
    (plugin,) = [
        ep for ep in entry_points(group="vllm.general_plugins")
        if ep.name == TEXT_PLUGIN_ENTRY_POINT
    ]
    plugin.load()()
    from vllm import ModelRegistry

    supported = set(ModelRegistry.get_supported_archs())
    missing = {"Qwen3_5ForCausalLM", "Qwen3_5MoeForCausalLM"} - supported
    if missing:
        raise RuntimeError(
            f"flexinfer_qwen35_text register() ran but did not register {sorted(missing)}"
        )


def main() -> None:
    import os
    import torch
    import vllm

    version = importlib.metadata.version("vllm")
    vllm_root = pathlib.Path(vllm.__file__).resolve().parent
    verify_source_contract(vllm_root, version)
    verify_text_plugin_contract(importlib.metadata.entry_points)
    verify_text_plugin_registers(importlib.metadata.entry_points, os.environ)

    # Image assembly has no GPU, so vLLM cannot detect ROCm and its generic
    # platform loader does not import this extension. Load it explicitly: the
    # Torch operator registration itself does not require a visible device.
    verify_compiled_contract(torch)

    print(
        "verified vLLM",
        version,
        "native gfx1100 W4A16 op: torch.ops._rocm_C.gptq_gemm_rdna3;",
        "flexinfer_qwen35_text register() OK",
    )


if __name__ == "__main__":
    main()
