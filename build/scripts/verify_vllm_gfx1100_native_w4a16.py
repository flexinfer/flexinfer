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


def main() -> None:
    import torch
    import vllm

    version = importlib.metadata.version("vllm")
    vllm_root = pathlib.Path(vllm.__file__).resolve().parent
    verify_source_contract(vllm_root, version)
    verify_text_plugin_contract(importlib.metadata.entry_points)

    # Image assembly has no GPU, so vLLM cannot detect ROCm and its generic
    # platform loader does not import this extension. Load it explicitly: the
    # Torch operator registration itself does not require a visible device.
    verify_compiled_contract(torch)

    print(
        "verified vLLM",
        version,
        "native gfx1100 W4A16 op: torch.ops._rocm_C.gptq_gemm_rdna3",
    )


if __name__ == "__main__":
    main()
