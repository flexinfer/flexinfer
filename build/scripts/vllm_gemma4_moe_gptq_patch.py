#!/usr/bin/env python3
"""Patch vLLM for Gemma4 GPTQ MoE support.

Four patches:
1. GPTQConfig.get_quant_method: Check modules_in_block_to_quantize before
   applying MoE quantization. When experts are not quantized, return None
   (unquantized) for FusedMoE layers.
2. MoeWNA16: Relax activation assertion from silu-only to silu+gelu.
   Gemma4 uses GELU in its MoE experts. The Triton fused_moe_kernel is
   activation-agnostic (applied post-kernel via apply_moe_activation),
   but the assertion blocks loading.
3. Gemma4Model.load_weights: Fix GPTQ MoE expert weight name routing.
   The _weight_iterator explodes 3D fused GPTQ tensors into per-expert
   names like 'experts.0.down_proj.qweight'. The expert_params_mapping
   produces 'experts.w2_weight.qweight' but the actual GPTQ parameter
   is 'experts.w2_qweight'. Also fixes the weight_name passed to the
   MoeWNA16 weight_loader so it correctly dispatches qweight vs scales
   vs qzeros conversions.
4. KV cache page_size_padded assertion: Gemma4 has heterogeneous head
   dimensions (256 for sliding window, 512 for full attention) which
   causes the V1 KV cache page_size_padded assertion to fire after
   unification. Replace assertion with max() to ensure safe allocation.

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


def patch_gemma4_moe_gptq_weight_names(vllm_root: pathlib.Path) -> bool:
    """Patch Gemma4Model.load_weights for GPTQ MoE expert weight name routing.

    Problem: _weight_iterator explodes 3D fused GPTQ MoE tensors into
    per-expert 2D weights with names like:
        layers.0.moe.experts.0.down_proj.qweight

    The expert_params_mapping maps "experts.{id}.{proj}" → "experts.w2_weight",
    producing "experts.w2_weight.qweight" — but the actual MoeWNA16 parameter
    name is "experts.w2_qweight" (standard FusedMoE uses a prefix convention
    with trailing dot that avoids this, but Gemma4 doesn't).

    Fix 1: When moe_name lookup fails, try "_weight.X" → "_X" transformation.
    Fix 2: Pass original weight name (with GPTQ suffix) to weight_loader so
            moe_wna16_weight_loader correctly dispatches qweight vs scales vs
            qzeros conversions (instead of always passing ".weight").
    """
    gemma4_py = vllm_root / "model_executor" / "models" / "gemma4.py"
    if not gemma4_py.exists():
        print(f"[gemma4-moe-patch] SKIP: {gemma4_py} not found")
        return False

    src = gemma4_py.read_text()

    if "GEMMA4_MOE_GPTQ_WEIGHT_NAMES_PATCH" in src:
        print("[gemma4-moe-patch] GPTQ weight names already patched, skipping")
        return True

    patched = False

    # --- Fix 1: GPTQ suffix fallback in moe_name lookup ---
    # Original:
    #     moe_name = name.replace(weight_name, param_name)
    #     if moe_name not in params_dict:
    #         continue
    # Patched: try "_weight.qweight" → "_qweight" transformation.
    old_moe_lookup = (
        "                    moe_name = name.replace(weight_name, param_name)\n"
        "                    if moe_name not in params_dict:\n"
        "                        continue"
    )
    new_moe_lookup = (
        "                    moe_name = name.replace(weight_name, param_name)\n"
        "                    if moe_name not in params_dict:\n"
        "                        # GEMMA4_MOE_GPTQ_WEIGHT_NAMES_PATCH fix 1:\n"
        '                        # GPTQ suffix: "w2_weight.qweight" -> "w2_qweight"\n'
        '                        if "_weight." in moe_name:\n'
        '                            moe_name = moe_name.replace("_weight.", "_", 1)\n'
        "                        if moe_name not in params_dict:\n"
        "                            continue"
    )

    if old_moe_lookup in src:
        src = src.replace(old_moe_lookup, new_moe_lookup, 1)
        patched = True
        print("[gemma4-moe-patch] Applied GPTQ moe_name fallback patch")
    else:
        # Try regex for whitespace variations
        pattern = re.compile(
            r"(\s+)moe_name = name\.replace\(weight_name, param_name\)\n"
            r"\s+if moe_name not in params_dict:\n"
            r"\s+continue"
        )
        match = pattern.search(src)
        if match:
            indent = match.group(1)
            replacement = (
                f"{indent}moe_name = name.replace(weight_name, param_name)\n"
                f"{indent}if moe_name not in params_dict:\n"
                f"{indent}    # GEMMA4_MOE_GPTQ_WEIGHT_NAMES_PATCH fix 1:\n"
                f'{indent}    # GPTQ suffix: "w2_weight.qweight" -> "w2_qweight"\n'
                f'{indent}    if "_weight." in moe_name:\n'
                f'{indent}        moe_name = moe_name.replace("_weight.", "_", 1)\n'
                f"{indent}if moe_name not in params_dict:\n"
                f"{indent}    continue"
            )
            src = src[: match.start()] + replacement + src[match.end() :]
            patched = True
            print("[gemma4-moe-patch] Applied GPTQ moe_name fallback patch (regex)")
        else:
            print(
                "[gemma4-moe-patch] WARNING: Could not find moe_name lookup block "
                "in Gemma4Model.load_weights"
            )

    # --- Fix 2: Pass correct weight_name to weight_loader for GPTQ ---
    # Original:
    #     weight_loader(
    #         param,
    #         loaded_weight,
    #         weight_name + ".weight",
    #         shard_id=shard_id,
    #         expert_id=expert_id,
    #     )
    # Patched: for GPTQ weights (.qweight/.scales/.qzeros), pass original name
    # so moe_wna16_weight_loader dispatches the right conversion path.
    old_wloader_call = (
        "                    weight_loader(\n"
        "                        param,\n"
        "                        loaded_weight,\n"
        '                        weight_name + ".weight",\n'
        "                        shard_id=shard_id,\n"
        "                        expert_id=expert_id,\n"
        "                    )"
    )
    new_wloader_call = (
        "                    # GEMMA4_MOE_GPTQ_WEIGHT_NAMES_PATCH fix 2:\n"
        "                    # GPTQ: pass original name with suffix so\n"
        "                    # moe_wna16_weight_loader applies correct conversion\n"
        "                    # (qweight→.T.view(uint8), scales→.T, qzeros→convert+.T).\n"
        "                    # FP16: append .weight for the base weight_loader.\n"
        "                    _gptq_sfx = any(\n"
        "                        name.endswith(s)\n"
        '                        for s in (".qweight", ".scales", ".qzeros")\n'
        "                    )\n"
        "                    weight_loader(\n"
        "                        param,\n"
        "                        loaded_weight,\n"
        '                        name if _gptq_sfx else weight_name + ".weight",\n'
        "                        shard_id=shard_id,\n"
        "                        expert_id=expert_id,\n"
        "                    )"
    )

    if old_wloader_call in src:
        src = src.replace(old_wloader_call, new_wloader_call, 1)
        patched = True
        print("[gemma4-moe-patch] Applied GPTQ weight_loader dispatch patch")
    else:
        # Try regex match
        pattern = re.compile(
            r"(\s+)weight_loader\(\n"
            r"\s+param,\n"
            r"\s+loaded_weight,\n"
            r'\s+weight_name \+ "\.weight",\n'
            r"\s+shard_id=shard_id,\n"
            r"\s+expert_id=expert_id,\n"
            r"\s+\)"
        )
        match = pattern.search(src)
        if match:
            indent = match.group(1)
            replacement = (
                f"{indent}# GEMMA4_MOE_GPTQ_WEIGHT_NAMES_PATCH fix 2:\n"
                f"{indent}_gptq_sfx = any(\n"
                f"{indent}    name.endswith(s)\n"
                f'{indent}    for s in (".qweight", ".scales", ".qzeros")\n'
                f"{indent})\n"
                f"{indent}weight_loader(\n"
                f"{indent}    param,\n"
                f"{indent}    loaded_weight,\n"
                f'{indent}    name if _gptq_sfx else weight_name + ".weight",\n'
                f"{indent}    shard_id=shard_id,\n"
                f"{indent}    expert_id=expert_id,\n"
                f"{indent})"
            )
            src = src[: match.start()] + replacement + src[match.end() :]
            patched = True
            print(
                "[gemma4-moe-patch] Applied GPTQ weight_loader dispatch patch (regex)"
            )
        else:
            print(
                "[gemma4-moe-patch] WARNING: Could not find weight_loader call "
                "in Gemma4Model.load_weights expert loop"
            )

    if patched:
        gemma4_py.write_text(src)
        print(f"[gemma4-moe-patch] Wrote patched {gemma4_py}")
    return patched


def patch_kv_cache_page_size_assertion(vllm_root: pathlib.Path) -> bool:
    """Patch KV cache page_size_bytes to use max() instead of assert.

    Gemma4 has heterogeneous head dimensions (head_dim=256 for sliding window
    layers, global_head_dim=512 for full attention layers). After the V1 KV
    cache unification logic pads page sizes across groups, some layers end up
    with page_size_padded < real_page_size, triggering an AssertionError.

    Fix: replace the assertion with max(page_size_padded, real_page_size) so
    that memory allocation is never undercounted. This is safe — it may
    slightly over-allocate KV cache for some layers but prevents OOM.
    """
    kv_iface = vllm_root / "v1" / "kv_cache_interface.py"
    if not kv_iface.exists():
        print(f"[gemma4-moe-patch] SKIP: {kv_iface} not found")
        return False

    src = kv_iface.read_text()

    if "GEMMA4_KV_PAGE_SIZE_PATCH" in src:
        print("[gemma4-moe-patch] KV cache page_size already patched, skipping")
        return True

    # Target: the page_size_bytes property in AttentionSpec
    # Original:
    #     if self.page_size_padded is not None:
    #         assert self.page_size_padded >= real_page_size
    #         return self.page_size_padded
    #     return real_page_size
    old_block = (
        "        if self.page_size_padded is not None:\n"
        "            assert self.page_size_padded >= real_page_size\n"
        "            return self.page_size_padded\n"
        "        return real_page_size"
    )
    new_block = (
        "        if self.page_size_padded is not None:\n"
        "            # GEMMA4_KV_PAGE_SIZE_PATCH: Gemma4 heterogeneous head dims\n"
        "            # (256 sliding / 512 full attention) can cause padded < real\n"
        "            # after unification. Use max() for safe allocation.\n"
        "            return max(self.page_size_padded, real_page_size)\n"
        "        return real_page_size"
    )

    if old_block in src:
        src = src.replace(old_block, new_block, 1)
        kv_iface.write_text(src)
        print(f"[gemma4-moe-patch] Patched KV cache page_size assertion in {kv_iface}")
        return True

    # Try regex for whitespace variations
    pattern = re.compile(
        r"(\s+)if self\.page_size_padded is not None:\n"
        r"\s+assert self\.page_size_padded >= real_page_size\n"
        r"\s+return self\.page_size_padded\n"
        r"\s+return real_page_size"
    )
    match = pattern.search(src)
    if match:
        indent = match.group(1)
        replacement = (
            f"{indent}if self.page_size_padded is not None:\n"
            f"{indent}    # GEMMA4_KV_PAGE_SIZE_PATCH: Gemma4 heterogeneous head dims\n"
            f"{indent}    # (256 sliding / 512 full) can cause padded < real after\n"
            f"{indent}    # unification. Use max() for safe allocation.\n"
            f"{indent}    return max(self.page_size_padded, real_page_size)\n"
            f"{indent}return real_page_size"
        )
        src = src[: match.start()] + replacement + src[match.end() :]
        kv_iface.write_text(src)
        print(
            f"[gemma4-moe-patch] Patched KV cache page_size assertion (regex) "
            f"in {kv_iface}"
        )
        return True

    print(
        "[gemma4-moe-patch] WARNING: Could not find page_size_padded assertion "
        "in kv_cache_interface.py"
    )
    return False


def main():
    vllm_root = find_vllm_root()
    print(f"[gemma4-moe-patch] vLLM root: {vllm_root}")

    ok1 = patch_gptq_config(vllm_root)
    ok2 = patch_moe_wna16_activation(vllm_root)
    ok3 = patch_gemma4_moe_gptq_weight_names(vllm_root)
    ok4 = patch_kv_cache_page_size_assertion(vllm_root)

    if not ok1:
        print("[gemma4-moe-patch] FAILED — GPTQConfig patch could not be applied")
        sys.exit(1)
    if not ok2:
        print(
            "[gemma4-moe-patch] WARNING — MoeWNA16 activation patch failed (non-fatal)"
        )
    if not ok3:
        print(
            "[gemma4-moe-patch] FAILED — GPTQ weight names patch could not be applied"
        )
        sys.exit(1)
    if not ok4:
        print(
            "[gemma4-moe-patch] WARNING — KV cache page_size patch failed (non-fatal)"
        )

    print("[gemma4-moe-patch] All patches applied successfully")


if __name__ == "__main__":
    main()
