#!/usr/bin/env python3
"""Patch vLLM GPTQConfig to handle unquantized MoE experts.

Problem: GPTQConfig.get_quant_method() always returns MoeWNA16 quantization
for FusedMoE layers, even when the experts are not GPTQ-quantized (e.g.,
Gemma4 26B-A4B where only attention layers are quantized). This causes
FusedMoE to create quantized parameters (w2_qweight, etc.) but the
checkpoint has unquantized weights (w2_weight), leading to KeyError
during weight loading.

Fix: Check modules_in_block_to_quantize before applying MoE quantization.
If the list is set and contains no MoE-related modules, return None
(unquantized) for FusedMoE layers.

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


def main():
    vllm_root = find_vllm_root()
    print(f"[gemma4-moe-patch] vLLM root: {vllm_root}")

    ok = patch_gptq_config(vllm_root)
    if not ok:
        print("[gemma4-moe-patch] FAILED — patch could not be applied")
        sys.exit(1)

    print("[gemma4-moe-patch] All patches applied successfully")


if __name__ == "__main__":
    main()
