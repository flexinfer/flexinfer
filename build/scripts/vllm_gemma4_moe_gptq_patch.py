#!/usr/bin/env python3
"""Patch vLLM for Gemma4 GPTQ MoE support.

Two patches:
1. GPTQConfig.get_quant_method: Check modules_in_block_to_quantize before
   applying MoE quantization. When experts are not quantized, return None
   (unquantized) for FusedMoE layers.
2. MoeWNA16: Relax activation assertion from silu-only to silu+gelu.
   Gemma4 uses GELU in its MoE experts. The Triton fused_moe_kernel is
   activation-agnostic (applied post-kernel via apply_moe_activation),
   but the assertion blocks loading.

Usage:
    python3 vllm_gemma4_moe_gptq_patch.py [vllm_root]

If vllm_root is omitted, the script auto-detects the vLLM package location.
"""
import pathlib
import re
import sys


def find_vllm_root() -> pathlib.Path:
    """Find the vLLM package root directory."""
    if len(sys.argv) > 1:
        return pathlib.Path(sys.argv[1])
    try:
        import vllm

        return pathlib.Path(vllm.__file__).resolve().parent
    except ImportError:
        # Common locations
        for candidate in [
            "/opt/venv/lib/python3.12/site-packages/vllm",
            "/opt/venv/lib/python3.11/site-packages/vllm",
        ]:
            p = pathlib.Path(candidate)
            if p.is_dir():
                return p
        raise FileNotFoundError("Cannot find vLLM installation")


def patch_gptq_config(vllm_root: pathlib.Path) -> bool:
    """Patch GPTQConfig.get_quant_method to skip MoE quantization when
    experts are excluded from GPTQ."""
    gptq_py = vllm_root / "model_executor" / "layers" / "quantization" / "gptq.py"
    if not gptq_py.exists():
        print(f"[gemma4-moe-patch] SKIP: {gptq_py} not found")
        return False

    src = gptq_py.read_text()

    # Check if already patched
    if "GEMMA4_MOE_GPTQ_PATCH" in src:
        print("[gemma4-moe-patch] Already patched, skipping")
        return True

    # Find the get_quant_method that handles FusedMoE
    # We need to add a check BEFORE the MoeWNA16 fallback.
    #
    # Original code pattern:
    #   if isinstance(layer, FusedMoE):
    #       from .moe_wna16 import MoeWNA16Config
    #       ...
    #       return MoeWNA16Config.from_config(config).get_quant_method(layer, prefix)
    #
    # Patched: add early return if experts are not quantized.

    old_pattern = (
        "        if isinstance(layer, FusedMoE):\n"
        "            # GPTQ MoE support: fall back to MoeWNA16 for broad compatibility\n"
        "            from .moe_wna16 import MoeWNA16Config"
    )

    new_code = (
        "        if isinstance(layer, FusedMoE):\n"
        "            # GEMMA4_MOE_GPTQ_PATCH: Check if MoE experts are actually\n"
        "            # quantized. When modules_in_block_to_quantize is set and\n"
        "            # contains only attention modules (no MoE-related entries),\n"
        "            # the experts remain unquantized — return None so FusedMoE\n"
        "            # uses UnquantizedFusedMoEMethod with plain weight params.\n"
        "            if getattr(self, 'modules_in_block_to_quantize', None):\n"
        "                _has_moe = any(\n"
        '                    "moe" in m or "expert" in m\n'
        "                    for m in self.modules_in_block_to_quantize\n"
        "                )\n"
        "                if not _has_moe:\n"
        "                    return None\n"
        "            # GPTQ MoE support: fall back to MoeWNA16 for broad compatibility\n"
        "            from .moe_wna16 import MoeWNA16Config"
    )

    if old_pattern not in src:
        # Try a more flexible match
        print(f"[gemma4-moe-patch] WARNING: exact pattern not found, trying regex")
        pattern = re.compile(
            r"(\s+if isinstance\(layer, FusedMoE\):\n)"
            r"(\s+#[^\n]*\n)"
            r"(\s+from \.moe_wna16 import MoeWNA16Config)"
        )
        match = pattern.search(src)
        if not match:
            print(
                f"[gemma4-moe-patch] ERROR: Could not find GPTQConfig.get_quant_method FusedMoE block"
            )
            return False
        indent = "        "
        replacement = (
            f"{indent}if isinstance(layer, FusedMoE):\n"
            f"{indent}    # GEMMA4_MOE_GPTQ_PATCH: Skip MoE quantization when\n"
            f"{indent}    # experts are not in modules_in_block_to_quantize.\n"
            f"{indent}    if getattr(self, 'modules_in_block_to_quantize', None):\n"
            f"{indent}        _has_moe = any(\n"
            f'{indent}            "moe" in m or "expert" in m\n'
            f"{indent}            for m in self.modules_in_block_to_quantize\n"
            f"{indent}        )\n"
            f"{indent}        if not _has_moe:\n"
            f"{indent}            return None\n"
            f"{match.group(2)}"
            f"{match.group(3)}"
        )
        src = pattern.sub(replacement, src, count=1)
    else:
        src = src.replace(old_pattern, new_code, 1)

    gptq_py.write_text(src)
    print(f"[gemma4-moe-patch] Patched {gptq_py}")
    return True


def patch_moe_wna16_activation(vllm_root: pathlib.Path) -> bool:
    """Patch MoeWNA16 to accept GELU activation in addition to SiLU.

    Gemma4 uses GELU in its MoE expert FFN. The Triton fused_moe_kernel_gptq_awq
    is activation-agnostic — activation is applied post-kernel via
    apply_moe_activation() which already supports GELU. Only the assertion
    in MoeWNA16Method blocks us.
    """
    moe_wna16_py = (
        vllm_root / "model_executor" / "layers" / "quantization" / "moe_wna16.py"
    )
    if not moe_wna16_py.exists():
        print(f"[gemma4-moe-patch] SKIP: {moe_wna16_py} not found")
        return False

    src = moe_wna16_py.read_text()

    # Check if already patched
    if "GEMMA4_MOE_ACTIVATION_PATCH" in src:
        print("[gemma4-moe-patch] MoeWNA16 activation already patched, skipping")
        return True

    # Find the silu-only assertion. Two known patterns:
    # 1. String-based: assert layer.activation == "silu", "Only SiLU ..."
    # 2. Enum-based:   assert layer.activation == MoEActivation.SILU, "Only SiLU ..."
    # Handle both with a regex that captures the full assertion.
    patterns = [
        # Enum pattern with multi-line error message in parens:
        #   assert layer.activation == MoEActivation.SILU, (
        #       f"Only SiLU activation is supported, not {layer.activation}."
        #   )
        # Uses [\s\S]*? to match across newlines inside the parens.
        (
            re.compile(
                r"([ \t]+)assert layer\.activation\s*==\s*MoEActivation\.SILU\s*,"
                r"\s*\([\s\S]*?\)"
            ),
            "enum",
        ),
        # Enum pattern single-line (no parens around error message)
        (
            re.compile(
                r"([ \t]+)assert layer\.activation\s*==\s*MoEActivation\.SILU\s*,"
                r"[^\n]*"
            ),
            "enum",
        ),
        # String pattern
        (
            re.compile(r'([ \t]+)assert layer\.activation\s*==\s*"silu"[^\n]*'),
            "string",
        ),
    ]

    for pattern, style in patterns:
        match = pattern.search(src)
        if match:
            indent = match.group(1)
            if style == "enum":
                replacement = (
                    f"{indent}# GEMMA4_MOE_ACTIVATION_PATCH: Gemma4 MoE uses GELU.\n"
                    f"{indent}# Triton fused_moe_kernel is activation-agnostic;\n"
                    f"{indent}# apply_moe_activation() handles both silu and gelu.\n"
                    f"{indent}assert layer.activation in (MoEActivation.SILU, MoEActivation.GELU), (\n"
                    f'{indent}    f"MoeWNA16 requires silu or gelu activation, got {{layer.activation}}."\n'
                    f"{indent})"
                )
            else:
                replacement = (
                    f"{indent}# GEMMA4_MOE_ACTIVATION_PATCH: Gemma4 MoE uses GELU.\n"
                    f"{indent}# Triton fused_moe_kernel is activation-agnostic;\n"
                    f"{indent}# apply_moe_activation() handles both silu and gelu.\n"
                    f'{indent}assert layer.activation in ("silu", "gelu"), \\\n'
                    f'{indent}    f"MoeWNA16 requires silu or gelu activation, got {{layer.activation}}"'
                )

            src = src[: match.start()] + replacement + src[match.end() :]
            moe_wna16_py.write_text(src)
            print(
                f"[gemma4-moe-patch] Patched MoeWNA16 activation assertion ({style}) "
                f"in {moe_wna16_py}"
            )
            return True

    # Check if the file already supports gelu (maybe newer vLLM)
    if "gelu" in src.lower() and "layer.activation" in src:
        print("[gemma4-moe-patch] MoeWNA16 already supports gelu, skipping")
        return True

    print(
        "[gemma4-moe-patch] WARNING: Could not find MoeWNA16 activation assertion to patch"
    )
    return False


def main():
    vllm_root = find_vllm_root()
    print(f"[gemma4-moe-patch] vLLM root: {vllm_root}")

    ok1 = patch_gptq_config(vllm_root)
    ok2 = patch_moe_wna16_activation(vllm_root)

    if not ok1:
        print("[gemma4-moe-patch] FAILED — GPTQConfig patch could not be applied")
        sys.exit(1)
    if not ok2:
        print(
            "[gemma4-moe-patch] WARNING — MoeWNA16 activation patch failed (non-fatal)"
        )

    print("[gemma4-moe-patch] All patches applied successfully")


if __name__ == "__main__":
    main()
