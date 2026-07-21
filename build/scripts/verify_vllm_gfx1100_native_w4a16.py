#!/usr/bin/env python3
"""Fail a runtime build that lacks vLLM's native RDNA3 W4A16 GPTQ kernel."""

from __future__ import annotations

import importlib
import importlib.metadata
import pathlib
import re


MINIMUM_VLLM_VERSION = (0, 23, 0)
KERNEL_RELATIVE_PATH = pathlib.Path(
    "model_executor/kernels/linear/mixed_precision/rdna3_w4a16.py"
)


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


def main() -> None:
    import torch
    import vllm

    version = importlib.metadata.version("vllm")
    vllm_root = pathlib.Path(vllm.__file__).resolve().parent
    verify_source_contract(vllm_root, version)

    # Importing the kernel wrapper loads vLLM's custom-op extension and
    # registers the ROCm op without requiring a GPU during image assembly.
    importlib.import_module(
        "vllm.model_executor.kernels.linear.mixed_precision.rdna3_w4a16"
    )
    if not hasattr(torch.ops._rocm_C, "gptq_gemm_rdna3"):
        raise RuntimeError(
            "vLLM package has the RDNA3 wrapper but its compiled _rocm_C "
            "extension does not register gptq_gemm_rdna3"
        )

    print(
        "verified vLLM",
        version,
        "native gfx1100 W4A16 op: torch.ops._rocm_C.gptq_gemm_rdna3",
    )


if __name__ == "__main__":
    main()
