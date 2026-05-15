#!/usr/bin/env python3
"""Patch vLLM RMSNorm.forward_hip to fall back to forward_native on ROCm.

vLLM 0.19's RMSNorm.forward_hip() passes the layer's weight tensor (FP16/BF16
in gemma4 and many other models) directly to the ROCm `rms_norm` C++ custom op
via torch.ops._C.rms_norm. That op was compiled expecting FP32 weights and
rejects half-precision input with:

  RuntimeError: expected scalar type Float but found Half

The CUDA equivalent is overloaded for half-precision; the ROCm variant is not.
This blocks gemma4-26b-a4b-gptq (and any other model with half-precision
RMSNorm weights) from booting on gfx1100 against a vLLM 0.19.1 source build.

Falsification doc: .loom/slice3-v1-sandbox-rms-norm-falsified-2026-05-15.md
Wave 1 spec: .loom/21-product-spec-vllm-feature-parity-2026-05-15.md (Slice 3)

This patch routes `forward_hip` through `forward_native` (the pure-PyTorch
implementation that handles any dtype on any backend). Remove this patch when
upstream vllm-project/vllm fixes the rocm rms_norm dtype mismatch — track via
the falsification doc.

Usage:
    python3 patch_vllm_rocm_rms_norm_dtype.py /path/to/vllm-source-clone

The script applies in-place to vllm/model_executor/layers/layernorm.py and is
idempotent (no-op when already applied).
"""

from __future__ import annotations

import pathlib
import sys


# vLLM 0.19.1 RMSNorm.forward_hip — original method body (lines ~367-381).
# Identifying the entire method body (signature + body) keeps the substitution
# unambiguous even if the surrounding code changes around it.
FORWARD_HIP_OLD = """    def forward_hip(
        self,
        x: torch.Tensor,
        residual: torch.Tensor | None = None,
    ) -> torch.Tensor | tuple[torch.Tensor, torch.Tensor]:
        if self.variance_size_override is not None:
            return self.forward_native(x, residual)

        add_residual = residual is not None
        if add_residual:
            return self.rocm_norm_func_with_add(
                x, residual, self.weight.data, self.variance_epsilon
            )
        else:
            return self.rocm_norm_func(x, self.weight.data, self.variance_epsilon)
"""


FORWARD_HIP_NEW = """    def forward_hip(
        self,
        x: torch.Tensor,
        residual: torch.Tensor | None = None,
    ) -> torch.Tensor | tuple[torch.Tensor, torch.Tensor]:
        # FlexInfer patch (slice3-v1-sandbox-rms-norm-falsified): the ROCm
        # custom rms_norm op rejects FP16/BF16 weights with
        # "expected scalar type Float but found Half". Fall back to the
        # PyTorch-native implementation which handles half-precision
        # weights correctly. Remove this patch when upstream fixes the
        # ROCm rms_norm dtype mismatch.
        return self.forward_native(x, residual)
"""


# Idempotency marker — presence in file indicates the patch has been applied.
ALREADY_PATCHED_MARKER = "FlexInfer patch (slice3-v1-sandbox-rms-norm-falsified)"


def patch_layernorm(vllm_root: pathlib.Path) -> None:
    target = vllm_root / "vllm" / "model_executor" / "layers" / "layernorm.py"
    if not target.is_file():
        raise SystemExit(f"layernorm.py not found at {target}")

    contents = target.read_text(encoding="utf-8")

    if ALREADY_PATCHED_MARKER in contents:
        print(f"[patch_vllm_rocm_rms_norm_dtype] already applied to {target}")
        return

    if FORWARD_HIP_OLD not in contents:
        # Help future debugging: emit a tail-of-file snippet so it's clear
        # the patch script and the vLLM source have drifted.
        snippet_idx = contents.find("def forward_hip(")
        snippet = (
            contents[snippet_idx : snippet_idx + 800]
            if snippet_idx != -1
            else "<no forward_hip found>"
        )
        raise SystemExit(
            "[patch_vllm_rocm_rms_norm_dtype] expected forward_hip hunk not "
            f"found in {target}. Source drift suspected. forward_hip neighborhood:\n{snippet}"
        )

    patched = contents.replace(FORWARD_HIP_OLD, FORWARD_HIP_NEW, 1)

    if patched.count(FORWARD_HIP_NEW) != 1:
        raise SystemExit(
            "[patch_vllm_rocm_rms_norm_dtype] substitution did not produce "
            "exactly one instance of the new hunk; aborting to avoid double-patch."
        )

    target.write_text(patched, encoding="utf-8")
    print(f"[patch_vllm_rocm_rms_norm_dtype] patched {target}")


def main() -> int:
    if len(sys.argv) != 2:
        print(f"usage: {sys.argv[0]} <vllm-source-root>", file=sys.stderr)
        return 2
    patch_layernorm(pathlib.Path(sys.argv[1]).resolve())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
