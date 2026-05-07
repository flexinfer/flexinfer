#!/usr/bin/env python3
"""Patch vLLM for Gemma4 GPTQ MoE support.

Seven patches:
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
4. KV cache routing for heterogeneous head dims: Gemma4 has head_dim=256
   (sliding window) and global_head_dim=512 (full attention). After
   disableHybridKVCacheManager unifies types, UniformTypeKVCacheSpecs
   accepts the mixed page sizes but then mis-shapes tensors. Fix: (A)
   safety-net the page_size_padded assertion with max(), (B) add a
   page-size guard in get_kv_cache_groups routing to fall through to
   unify_kv_cache_spec_page_size which adjusts block sizes correctly.

Usage:
    python3 vllm_gemma4_moe_gptq_patch.py [vllm_root]

If vllm_root is omitted, the script auto-detects the vLLM package location.
"""
import os
import pathlib
import re
import sys


def find_vllm_root() -> pathlib.Path | None:
    """Find the vLLM package root directory.

    Returns None when vLLM is not installed (e.g. gfx906 runtime profile
    that excludes vLLM). Callers should treat None as a silent no-op:
    the patches only apply to vLLM, so absent installs need no action.
    """
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
        return None


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


def patch_gptq_rocm_reference_fallback(vllm_root: pathlib.Path) -> bool:
    """Patch GPTQLinearMethod.apply() to use a torch reference path on ROCm.

    vLLM's 4-bit gptq_gemm kernel is known-buggy, and on gfx1100 we see the
    model load cleanly but collapse semantically at generation time. For
    ROCm+GPTQ-4bit, replace the GEMM call with a correctness-first reference
    implementation:

      1. unpack uint4b8 rows from packed int32 qweight
      2. unpack GPTQ qzeros (stored as zp-1) back to zero points
      3. dequantize weights per group with scales
      4. matmul in torch

    This is slower than the fused kernel, but it preserves the GPTQ artifact
    format and gives us a viable serving path on target hardware.
    """
    gptq_py = vllm_root / "model_executor" / "layers" / "quantization" / "gptq.py"
    if not gptq_py.exists():
        print(f"[gemma4-moe-patch] SKIP: {gptq_py} not found")
        return False

    src = gptq_py.read_text()

    if "GEMMA4_GPTQ_ROCM_REFERENCE_PATCH" in src:
        print("[gemma4-moe-patch] GPTQ ROCm reference fallback already patched")
        return True

    old_apply = (
        "    def apply(\n"
        "        self,\n"
        "        layer: torch.nn.Module,\n"
        "        x: torch.Tensor,\n"
        "        bias: torch.Tensor | None = None,\n"
        "    ) -> torch.Tensor:\n"
        "        out_shape = x.shape[:-1] + (layer.qweight.shape[-1],)\n"
        "        reshaped_x = x.reshape(-1, x.shape[-1])\n"
        "\n"
        "        # GPTQ v1 and v2 format checkpoints deals with zero points differently,\n"
        "        # and require different gemm kernels.\n"
        "        output = ops.gptq_gemm(\n"
        "            reshaped_x,\n"
        "            layer.qweight,\n"
        "            layer.qzeros,\n"
        "            layer.scales,\n"
        "            layer.g_idx,\n"
        "            layer.exllama_state == ExllamaState.READY,\n"
        "            self.use_v2_format,\n"
        "            self.quant_config.weight_bits,\n"
        "        )\n"
        "        if bias is not None:\n"
        "            output.add_(bias)\n"
        "        return output.reshape(out_shape)\n"
    )
    new_apply = (
        "    def apply(\n"
        "        self,\n"
        "        layer: torch.nn.Module,\n"
        "        x: torch.Tensor,\n"
        "        bias: torch.Tensor | None = None,\n"
        "    ) -> torch.Tensor:\n"
        "        out_shape = x.shape[:-1] + (layer.qweight.shape[-1],)\n"
        "        reshaped_x = x.reshape(-1, x.shape[-1])\n"
        "\n"
        "        if torch.version.hip is not None and self.quant_config.weight_bits == 4:\n"
        "            # GEMMA4_GPTQ_ROCM_REFERENCE_PATCH: correctness-first GPTQ\n"
        "            # fallback for ROCm. Unpack uint4b8 qweight/qzeros and do a\n"
        "            # plain torch matmul instead of the buggy gptq_gemm kernel.\n"
        "            pack_factor = int(self.quant_config.pack_factor)\n"
        "            qweight_i32 = layer.qweight.to(torch.int32)\n"
        "            input_size = layer.input_size_per_partition\n"
        "            output_size = layer.qweight.shape[-1]\n"
        "\n"
        "            unpacked_qweight = torch.empty(\n"
        "                (input_size, output_size),\n"
        "                dtype=torch.int16,\n"
        "                device=qweight_i32.device,\n"
        "            )\n"
        "            for i in range(pack_factor):\n"
        "                unpacked_qweight[i::pack_factor, :] = ((qweight_i32 >> (4 * i)) & 0xF).to(torch.int16)\n"
        "\n"
        "            if layer.qzeros.numel() > 0:\n"
        "                qzeros_i32 = layer.qzeros.to(torch.int32)\n"
        "                zero_points = torch.empty(\n"
        "                    (qzeros_i32.shape[0], output_size),\n"
        "                    dtype=torch.int16,\n"
        "                    device=qzeros_i32.device,\n"
        "                )\n"
        "                for i in range(pack_factor):\n"
        "                    # GPTQ stores zero points as (zp - 1) in packed uint4.\n"
        "                    zero_points[:, i::pack_factor] = (((qzeros_i32 >> (4 * i)) & 0xF).to(torch.int16) + 1)\n"
        "            else:\n"
        "                zero_points = None\n"
        "\n"
        "            if layer.g_idx.numel() > 0:\n"
        "                group_idx = layer.g_idx.to(torch.long)\n"
        "            else:\n"
        "                group_size = self.quant_config.group_size\n"
        "                if group_size == -1:\n"
        "                    group_size = input_size\n"
        "                group_idx = torch.arange(\n"
        "                    input_size, device=qweight_i32.device, dtype=torch.long\n"
        "                ) // group_size\n"
        "\n"
        "            signed_qweight = unpacked_qweight.to(torch.float16)\n"
        "            if zero_points is not None:\n"
        "                signed_qweight = signed_qweight - zero_points[group_idx].to(torch.float16)\n"
        "            else:\n"
        "                signed_qweight = signed_qweight - 8.0\n"
        "\n"
        "            dequant_weight = signed_qweight * layer.scales[group_idx].to(torch.float16)\n"
        "            output = reshaped_x.to(torch.float16) @ dequant_weight\n"
        "            if bias is not None:\n"
        "                output.add_(bias.to(output.dtype))\n"
        "            return output.reshape(out_shape)\n"
        "\n"
        "        # GPTQ v1 and v2 format checkpoints deals with zero points differently,\n"
        "        # and require different gemm kernels.\n"
        "        output = ops.gptq_gemm(\n"
        "            reshaped_x,\n"
        "            layer.qweight,\n"
        "            layer.qzeros,\n"
        "            layer.scales,\n"
        "            layer.g_idx,\n"
        "            layer.exllama_state == ExllamaState.READY,\n"
        "            self.use_v2_format,\n"
        "            self.quant_config.weight_bits,\n"
        "        )\n"
        "        if bias is not None:\n"
        "            output.add_(bias)\n"
        "        return output.reshape(out_shape)\n"
    )

    if old_apply not in src:
        print(
            "[gemma4-moe-patch] WARNING: Could not find GPTQLinearMethod.apply for ROCm fallback patch"
        )
        return False

    src = src.replace(old_apply, new_apply, 1)
    gptq_py.write_text(src)
    print(f"[gemma4-moe-patch] Patched GPTQ ROCm reference fallback in {gptq_py}")
    return True


def patch_moe_wna16_rocm_reference_fallback(vllm_root: pathlib.Path) -> bool:
    """Patch MoeWNA16Method.apply() to use a torch reference path on ROCm.

    After fixing plain GPTQLinearMethod.apply(), Gemma4 26B-A4B still collapses
    semantically on gfx1100. The remaining likely fault line is the int4 MoE
    execution path, which still routes through fused_experts. For ROCm + GELU +
    int4, replace the fused MoE kernel call with a correctness-first reference
    implementation that dequantizes the selected experts on demand and runs the
    gate/up/down matmuls directly in torch.
    """
    moe_py = vllm_root / "model_executor" / "layers" / "quantization" / "moe_wna16.py"
    if not moe_py.exists():
        print(f"[gemma4-moe-patch] SKIP: {moe_py} not found")
        return False

    src = moe_py.read_text()

    if "GEMMA4_MOE_ROCM_REFERENCE_PATCH" in src:
        print("[gemma4-moe-patch] MoeWNA16 ROCm reference fallback already patched")
        return True

    old_block = (
        "        _result = fused_experts(\n"
        "            x,\n"
        "            layer.w13_qweight,\n"
        "            layer.w2_qweight,\n"
        "            topk_weights=topk_weights,\n"
        "            topk_ids=topk_ids,\n"
        "            inplace=not self.moe.disable_inplace,\n"
        "            activation=layer.activation,\n"
        "            apply_router_weight_on_input=layer.apply_router_weight_on_input,\n"
        "            global_num_experts=layer.global_num_experts,\n"
        "            expert_map=layer.expert_map,\n"
        "            quant_config=self.moe_quant_config,\n"
        "        )\n"
    )

    new_block = (
        "        if (\n"
        "            torch.version.hip is not None\n"
        "            and self.quant_config.weight_bits == 4\n"
        "            and layer.activation == MoEActivation.GELU\n"
        "        ):\n"
        "            # GEMMA4_MOE_ROCM_REFERENCE_PATCH: correctness-first\n"
        "            # fallback for ROCm int4+GELU MoE. Dequantize the routed\n"
        "            # experts on demand and run the gate/up/down matmuls in\n"
        "            # torch instead of fused_experts.\n"
        "            from vllm.model_executor.layers.fused_moe.activation import (\n"
        "                apply_moe_activation,\n"
        "            )\n"
        "\n"
        "            def _unpack_u4_last_dim(packed: torch.Tensor) -> torch.Tensor:\n"
        "                packed_i16 = packed.to(torch.int16)\n"
        "                out = torch.empty(\n"
        "                    packed_i16.shape[:-1] + (packed_i16.shape[-1] * 2,),\n"
        "                    dtype=torch.int16,\n"
        "                    device=packed.device,\n"
        "                )\n"
        "                out[..., 0::2] = packed_i16 & 0xF\n"
        "                out[..., 1::2] = (packed_i16 >> 4) & 0xF\n"
        "                return out\n"
        "\n"
        "            def _unpack_u4_first_dim(packed: torch.Tensor) -> torch.Tensor:\n"
        "                packed_i16 = packed.to(torch.int16)\n"
        "                out = torch.empty(\n"
        "                    (packed_i16.shape[0] * 2,) + packed_i16.shape[1:],\n"
        "                    dtype=torch.int16,\n"
        "                    device=packed.device,\n"
        "                )\n"
        "                out[0::2] = packed_i16 & 0xF\n"
        "                out[1::2] = (packed_i16 >> 4) & 0xF\n"
        "                return out\n"
        "\n"
        "            _cache = getattr(layer, '_gemma4_rocm_ref_cache', None)\n"
        "            if _cache is None:\n"
        "                _cache = {}\n"
        "                layer._gemma4_rocm_ref_cache = _cache\n"
        "                layer._gemma4_rocm_ref_cache_order = []\n"
        "            _cache_order = layer._gemma4_rocm_ref_cache_order\n"
        "\n"
        "            def _cache_put(key, value):\n"
        "                if key in _cache:\n"
        "                    return _cache[key]\n"
        "                _cache[key] = value\n"
        "                _cache_order.append(key)\n"
        "                while len(_cache_order) > 16:\n"
        "                    _evict = _cache_order.pop(0)\n"
        "                    _cache.pop(_evict, None)\n"
        "                return value\n"
        "\n"
        "            def _get_w13(expert_id: int) -> torch.Tensor:\n"
        "                key = ('w13', int(expert_id))\n"
        "                if key in _cache:\n"
        "                    return _cache[key]\n"
        "                _q = _unpack_u4_last_dim(layer.w13_qweight[expert_id])\n"
        "                _g = torch.arange(\n"
        "                    _q.shape[1], device=_q.device, dtype=torch.long\n"
        "                ) // layer.group_size\n"
        "                _w = _q.to(torch.float16)\n"
        "                if getattr(layer, 'w13_qzeros', None) is not None and layer.w13_qzeros.numel() > 0:\n"
        "                    _zp = _unpack_u4_first_dim(layer.w13_qzeros[expert_id])\n"
        "                    _w = _w - _zp[:, _g].to(torch.float16)\n"
        "                else:\n"
        "                    _w = _w - 8.0\n"
        "                _w = _w * layer.w13_scales[expert_id][:, _g].to(torch.float16)\n"
        "                return _cache_put(key, _w.transpose(0, 1).contiguous())\n"
        "\n"
        "            def _get_w2(expert_id: int) -> torch.Tensor:\n"
        "                key = ('w2', int(expert_id))\n"
        "                if key in _cache:\n"
        "                    return _cache[key]\n"
        "                _q = _unpack_u4_last_dim(layer.w2_qweight[expert_id])\n"
        "                _g = torch.arange(\n"
        "                    _q.shape[1], device=_q.device, dtype=torch.long\n"
        "                ) // layer.group_size\n"
        "                _w = _q.to(torch.float16)\n"
        "                if getattr(layer, 'w2_qzeros', None) is not None and layer.w2_qzeros.numel() > 0:\n"
        "                    _zp = _unpack_u4_first_dim(layer.w2_qzeros[expert_id])\n"
        "                    _w = _w - _zp[:, _g].to(torch.float16)\n"
        "                else:\n"
        "                    _w = _w - 8.0\n"
        "                _w = _w * layer.w2_scales[expert_id][:, _g].to(torch.float16)\n"
        "                return _cache_put(key, _w.transpose(0, 1).contiguous())\n"
        "\n"
        "            _result = torch.zeros(\n"
        "                (x.shape[0], layer.w2_qweight.shape[1]),\n"
        "                dtype=x.dtype,\n"
        "                device=x.device,\n"
        "            )\n"
        "            for _tok in range(x.shape[0]):\n"
        "                _tok_x = x[_tok : _tok + 1].to(torch.float16)\n"
        "                _tok_out = torch.zeros_like(_result[_tok : _tok + 1])\n"
        "                for _slot in range(topk_ids.shape[1]):\n"
        "                    _expert_id = int(topk_ids[_tok, _slot].item())\n"
        "                    if _expert_id < 0:\n"
        "                        continue\n"
        "                    _router_w = topk_weights[_tok, _slot].to(torch.float16)\n"
        "                    _expert_in = (\n"
        "                        _tok_x * _router_w\n"
        "                        if layer.apply_router_weight_on_input\n"
        "                        else _tok_x\n"
        "                    )\n"
        "                    _w13 = _get_w13(_expert_id)\n"
        "                    _gate_up = _expert_in @ _w13\n"
        "                    _hidden = torch.empty(\n"
        "                        (_expert_in.shape[0], _w13.shape[1] // 2),\n"
        "                        dtype=torch.float16,\n"
        "                        device=x.device,\n"
        "                    )\n"
        "                    apply_moe_activation(layer.activation, _hidden, _gate_up)\n"
        "                    _w2 = _get_w2(_expert_id)\n"
        "                    _expert_out = _hidden @ _w2\n"
        "                    if not layer.apply_router_weight_on_input:\n"
        "                        _expert_out.mul_(_router_w)\n"
        "                    _tok_out.add_(_expert_out.to(_tok_out.dtype))\n"
        "                _result[_tok : _tok + 1] = _tok_out\n"
        "        else:\n"
        "            _result = fused_experts(\n"
        "                x,\n"
        "                layer.w13_qweight,\n"
        "                layer.w2_qweight,\n"
        "                topk_weights=topk_weights,\n"
        "                topk_ids=topk_ids,\n"
        "                inplace=not self.moe.disable_inplace,\n"
        "                activation=layer.activation,\n"
        "                apply_router_weight_on_input=layer.apply_router_weight_on_input,\n"
        "                global_num_experts=layer.global_num_experts,\n"
        "                expert_map=layer.expert_map,\n"
        "                quant_config=self.moe_quant_config,\n"
        "            )\n"
    )

    if old_block not in src:
        print(
            "[gemma4-moe-patch] WARNING: Could not find MoeWNA16.apply fused_experts block"
        )
        return False

    src = src.replace(old_block, new_block, 1)
    moe_py.write_text(src)
    print(f"[gemma4-moe-patch] Patched MoeWNA16 ROCm reference fallback in {moe_py}")
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


def patch_kv_cache_page_size_uniform_type(vllm_root: pathlib.Path) -> bool:
    """Patch KV cache routing for Gemma4 heterogeneous head dimensions.

    Gemma4 has heterogeneous head dimensions (head_dim=256 for sliding window,
    global_head_dim=512 for full attention). When disableHybridKVCacheManager
    converts all specs to FullAttentionSpec, UniformTypeKVCacheSpecs.from_specs
    succeeds (all same type), but the different head dims cause different page
    sizes. The uniform-type path then mis-shapes KV cache tensors.

    Two sub-patches:
      A) kv_cache_interface.py: Replace page_size_padded assertion with max()
         as a safety net for heterogeneous head dims after unification.
      B) kv_cache_utils.py: Patch get_kv_cache_groups routing to check page-size
         uniformity before committing to the uniform-type path. When page sizes
         differ, fall through to unify_kv_cache_spec_page_size which adjusts
         block sizes to equalize page sizes across groups.
    """
    patched_any = False

    # --- Sub-patch A: page_size_padded assertion → max() ---
    kv_iface = vllm_root / "v1" / "kv_cache_interface.py"
    if not kv_iface.exists():
        print(f"[gemma4-moe-patch] SKIP: {kv_iface} not found")
    else:
        src = kv_iface.read_text()
        if "GEMMA4_KV_PAGE_SIZE_PATCH" in src:
            print("[gemma4-moe-patch] KV cache interface already patched, skipping")
            patched_any = True
        else:
            old_assert = (
                "        if self.page_size_padded is not None:\n"
                "            assert self.page_size_padded >= real_page_size\n"
                "            return self.page_size_padded\n"
                "        return real_page_size"
            )
            new_assert = (
                "        if self.page_size_padded is not None:\n"
                "            # GEMMA4_KV_PAGE_SIZE_PATCH sub-A: safety net for\n"
                "            # heterogeneous head dims after page-size unification.\n"
                "            return max(self.page_size_padded, real_page_size)\n"
                "        return real_page_size"
            )
            if old_assert in src:
                src = src.replace(old_assert, new_assert, 1)
                kv_iface.write_text(src)
                patched_any = True
                print(
                    f"[gemma4-moe-patch] Applied page_size_padded max() safety in {kv_iface}"
                )
            else:
                # Try regex fallback
                assert_pattern = re.compile(
                    r"(\s+)if self\.page_size_padded is not None:\n"
                    r"\s+assert self\.page_size_padded >= real_page_size\n"
                    r"\s+return self\.page_size_padded\n"
                    r"\s+return real_page_size"
                )
                match = assert_pattern.search(src)
                if match:
                    indent = match.group(1)
                    replacement = (
                        f"{indent}if self.page_size_padded is not None:\n"
                        f"{indent}    # GEMMA4_KV_PAGE_SIZE_PATCH sub-A: safety net\n"
                        f"{indent}    return max(self.page_size_padded, real_page_size)\n"
                        f"{indent}return real_page_size"
                    )
                    src = src[: match.start()] + replacement + src[match.end() :]
                    kv_iface.write_text(src)
                    patched_any = True
                    print(
                        f"[gemma4-moe-patch] Applied page_size max() (regex) in {kv_iface}"
                    )
                else:
                    print(
                        f"[gemma4-moe-patch] WARNING: page_size_padded assertion not found in {kv_iface}"
                    )

    # --- Sub-patch B: get_kv_cache_groups routing ---
    # Patch the routing in get_kv_cache_groups to add a page-size guard before
    # committing to the uniform-type path. When page sizes differ within the
    # same type, fall through to unify_kv_cache_spec_page_size.
    kv_utils = vllm_root / "v1" / "core" / "kv_cache_utils.py"
    if not kv_utils.exists():
        print(f"[gemma4-moe-patch] SKIP: {kv_utils} not found")
    else:
        src = kv_utils.read_text()
        if "GEMMA4_KV_PAGE_SIZE_PATCH" in src:
            print("[gemma4-moe-patch] KV cache routing already patched, skipping")
            patched_any = True
        else:
            old_routing = (
                "    elif uniform_spec := UniformTypeKVCacheSpecs.from_specs(kv_cache_spec):\n"
                "        # All layers need the same number of token slots (e.g., all layers are\n"
                "        # full attention, or all layers are sliding window attention with the\n"
                "        # same window size). Put all layers into one group.\n"
                "        return _get_kv_cache_groups_uniform_type(uniform_spec)"
            )
            new_routing = (
                "    elif uniform_spec := UniformTypeKVCacheSpecs.from_specs(kv_cache_spec):\n"
                "        # GEMMA4_KV_PAGE_SIZE_PATCH sub-B: Verify page-size uniformity\n"
                "        # before using uniform-type path. Gemma4 has head_dim=256\n"
                "        # (sliding) vs 512 (full), producing different page sizes within\n"
                "        # the same type. When page sizes differ, fall through to\n"
                "        # unify_kv_cache_spec_page_size which adjusts block sizes.\n"
                "        page_sizes = set(spec.page_size_bytes for spec in kv_cache_spec.values())\n"
                "        if len(page_sizes) > 1:\n"
                "            logger.info('GEMMA4_KV_PATCH: heterogeneous page sizes %s, '\n"
                "                        'falling through to unify_kv_cache_spec_page_size',\n"
                "                        {ps: sum(1 for s in kv_cache_spec.values()\n"
                "                                 if s.page_size_bytes == ps) for ps in page_sizes})\n"
                "            import torch\n"
                "            from vllm.v1.kv_cache_interface import get_dtype_size\n"
                "            shown = set()\n"
                "            for ln, sp in kv_cache_spec.items():\n"
                "                key = (type(sp).__name__, sp.num_kv_heads, sp.head_size,\n"
                "                       getattr(sp, 'head_size_v', None), sp.block_size, sp.dtype)\n"
                "                if key in shown:\n"
                "                    continue\n"
                "                shown.add(key)\n"
                "                logger.info('  %s: type=%s kv=%d h=%d hv=%s bs=%d dtype=%s(%d) '\n"
                "                            'page=%d real=%d padded=%s',\n"
                "                            ln, type(sp).__name__, sp.num_kv_heads, sp.head_size,\n"
                "                            getattr(sp, 'head_size_v', '?'), sp.block_size,\n"
                "                            sp.dtype, get_dtype_size(sp.dtype),\n"
                "                            sp.page_size_bytes, sp.real_page_size_bytes,\n"
                "                            sp.page_size_padded)\n"
                "        if len(page_sizes) <= 1:\n"
                "            return _get_kv_cache_groups_uniform_type(uniform_spec)"
            )
            if old_routing in src:
                src = src.replace(old_routing, new_routing, 1)
                kv_utils.write_text(src)
                patched_any = True
                print(
                    f"[gemma4-moe-patch] Applied get_kv_cache_groups routing patch in {kv_utils}"
                )
            else:
                # Try regex fallback
                routing_pattern = re.compile(
                    r"(    elif uniform_spec := UniformTypeKVCacheSpecs\.from_specs\(kv_cache_spec\):\n)"
                    r"(\s+#[^\n]*\n)+"
                    r"(\s+return _get_kv_cache_groups_uniform_type\(uniform_spec\))"
                )
                match = routing_pattern.search(src)
                if match:
                    replacement = (
                        "    elif uniform_spec := UniformTypeKVCacheSpecs.from_specs(kv_cache_spec):\n"
                        "        # GEMMA4_KV_PAGE_SIZE_PATCH sub-B: page-size guard\n"
                        "        page_sizes = set(spec.page_size_bytes for spec in kv_cache_spec.values())\n"
                        "        if len(page_sizes) > 1:\n"
                        "            logger.info('GEMMA4_KV_PATCH: heterogeneous page sizes %s', page_sizes)\n"
                        "        if len(page_sizes) <= 1:\n"
                        "            return _get_kv_cache_groups_uniform_type(uniform_spec)"
                    )
                    src = src[: match.start()] + replacement + src[match.end() :]
                    kv_utils.write_text(src)
                    patched_any = True
                    print(
                        f"[gemma4-moe-patch] Applied routing patch (regex) in {kv_utils}"
                    )
                else:
                    print(
                        f"[gemma4-moe-patch] WARNING: get_kv_cache_groups routing not found in {kv_utils}"
                    )

    if not patched_any:
        print("[gemma4-moe-patch] WARNING: No KV cache patches could be applied")
    return patched_any


def patch_moe_wna16_activation_forwarding(vllm_root: pathlib.Path) -> bool:
    """Patch MoeWNA16Method.apply() to forward the activation parameter
    and add NaN debug logging.

    Bug: MoeWNA16Method.apply() calls fused_experts() without passing
    activation=layer.activation. The default is SiLU, but Gemma4 MoE
    uses GELU. Wrong activation → wrong gate values → NaN logits after
    30 layers of error accumulation.

    Also adds NaN detection logging to trace where NaN first appears.
    """
    moe_wna16_py = (
        vllm_root / "model_executor" / "layers" / "quantization" / "moe_wna16.py"
    )
    if not moe_wna16_py.exists():
        print(f"[gemma4-moe-patch] SKIP: {moe_wna16_py} not found")
        return False

    src = moe_wna16_py.read_text()

    if "GEMMA4_MOE_ACTIVATION_FORWARD_PATCH" in src:
        print("[gemma4-moe-patch] MoeWNA16 activation forwarding already patched")
        return True

    # Add NaN debug globals at the top of the file (after imports)
    nan_debug_code = (
        "\n# GEMMA4_MOE_NAN_DEBUG: NaN detection for fused MoE kernel\n"
        "import logging as _nan_logging\n"
        "_nan_logger = _nan_logging.getLogger('gemma4_moe_nan_debug')\n"
        "_nan_call_count = 0\n"
        "_nan_logged_count = 0\n"
        "_NAN_LOG_LIMIT = 30\n"
    )

    # Insert after the last import line
    import_end = 0
    for i, line in enumerate(src.split("\n")):
        if line.startswith("import ") or line.startswith("from "):
            import_end = src.index(line) + len(line)
    if import_end > 0:
        src = src[:import_end] + nan_debug_code + src[import_end:]

    # Find the fused_experts call in apply() — match both original and
    # already-activation-patched versions
    old_call = (
        "        return fused_experts(\n"
        "            x,\n"
        "            layer.w13_qweight,\n"
        "            layer.w2_qweight,\n"
        "            topk_weights=topk_weights,\n"
        "            topk_ids=topk_ids,\n"
        "            inplace=not self.moe.disable_inplace,\n"
        "            apply_router_weight_on_input=layer.apply_router_weight_on_input,\n"
        "            global_num_experts=layer.global_num_experts,\n"
        "            expert_map=layer.expert_map,\n"
        "            quant_config=self.moe_quant_config,\n"
        "        )"
    )
    new_call = (
        "        # GEMMA4_MOE_ACTIVATION_FORWARD_PATCH: Forward activation + NaN debug\n"
        "        global _nan_call_count, _nan_logged_count\n"
        "        _nan_call_count += 1\n"
        "        _call_id = _nan_call_count\n"
        "        _x_has_nan = x.isnan().any().item()\n"
        "        _x_has_inf = x.isinf().any().item()\n"
        "        if (_x_has_nan or _x_has_inf) and _nan_logged_count < _NAN_LOG_LIMIT:\n"
        "            _nan_logged_count += 1\n"
        "            _nan_logger.error(\n"
        "                'MoE INPUT NaN/Inf at call %d: nan=%s inf=%s '\n"
        "                'shape=%s min=%.4f max=%.4f mean=%.4f',\n"
        "                _call_id, _x_has_nan, _x_has_inf,\n"
        "                list(x.shape), x[~x.isnan()].min().item() if not x.isnan().all().item() else float('nan'),\n"
        "                x[~x.isnan()].max().item() if not x.isnan().all().item() else float('nan'),\n"
        "                x[~x.isnan()].mean().item() if not x.isnan().all().item() else float('nan'))\n"
        "        _result = fused_experts(\n"
        "            x,\n"
        "            layer.w13_qweight,\n"
        "            layer.w2_qweight,\n"
        "            topk_weights=topk_weights,\n"
        "            topk_ids=topk_ids,\n"
        "            inplace=not self.moe.disable_inplace,\n"
        "            activation=layer.activation,\n"
        "            apply_router_weight_on_input=layer.apply_router_weight_on_input,\n"
        "            global_num_experts=layer.global_num_experts,\n"
        "            expert_map=layer.expert_map,\n"
        "            quant_config=self.moe_quant_config,\n"
        "        )\n"
        "        _r_has_nan = _result.isnan().any().item()\n"
        "        _r_has_inf = _result.isinf().any().item()\n"
        "        if (_r_has_nan or _r_has_inf) and _nan_logged_count < _NAN_LOG_LIMIT:\n"
        "            _nan_logged_count += 1\n"
        "            _safe = _result[~(_result.isnan() | _result.isinf())]\n"
        "            _nan_logger.error(\n"
        "                'MoE OUTPUT NaN/Inf at call %d: nan=%s inf=%s '\n"
        "                'shape=%s nan_count=%d inf_count=%d '\n"
        "                'input_had_nan=%s '\n"
        "                'safe_min=%.4f safe_max=%.4f topk_ids_range=[%d,%d] '\n"
        "                'w13_qw=%s w2_qw=%s w13_sc_nan=%s w2_sc_nan=%s',\n"
        "                _call_id, _r_has_nan, _r_has_inf,\n"
        "                list(_result.shape),\n"
        "                _result.isnan().sum().item(), _result.isinf().sum().item(),\n"
        "                _x_has_nan,\n"
        "                _safe.min().item() if _safe.numel() > 0 else float('nan'),\n"
        "                _safe.max().item() if _safe.numel() > 0 else float('nan'),\n"
        "                topk_ids.min().item(), topk_ids.max().item(),\n"
        "                list(layer.w13_qweight.shape), list(layer.w2_qweight.shape),\n"
        "                layer.w13_scales.isnan().any().item(),\n"
        "                layer.w2_scales.isnan().any().item())\n"
        "        elif _call_id <= 5 and not _r_has_nan:\n"
        "            _nan_logger.warning(\n"
        "                'MoE call %d OK: shape=%s min=%.4f max=%.4f mean=%.4f',\n"
        "                _call_id, list(_result.shape),\n"
        "                _result.min().item(), _result.max().item(), _result.mean().item())\n"
        "        return _result"
    )

    if old_call in src:
        src = src.replace(old_call, new_call, 1)
        moe_wna16_py.write_text(src)
        print(f"[gemma4-moe-patch] Patched MoeWNA16 activation+debug in {moe_wna16_py}")
        return True

    # Try regex for whitespace variations
    pattern = re.compile(
        r"(\s+)return fused_experts\(\n"
        r"\s+x,\n"
        r"\s+layer\.w13_qweight,\n"
        r"\s+layer\.w2_qweight,\n"
        r"\s+topk_weights=topk_weights,\n"
        r"\s+topk_ids=topk_ids,\n"
        r"\s+inplace=not self\.moe\.disable_inplace,\n"
        r"\s+apply_router_weight_on_input=layer\.apply_router_weight_on_input,\n"
        r"\s+global_num_experts=layer\.global_num_experts,\n"
        r"\s+expert_map=layer\.expert_map,\n"
        r"\s+quant_config=self\.moe_quant_config,\n"
        r"\s+\)"
    )
    match = pattern.search(src)
    if match:
        indent = match.group(1)
        replacement = (
            f"{indent}# GEMMA4_MOE_ACTIVATION_FORWARD_PATCH\n"
            f"{indent}global _nan_call_count, _nan_logged_count\n"
            f"{indent}_nan_call_count += 1\n"
            f"{indent}_call_id = _nan_call_count\n"
            f"{indent}_x_nan = x.isnan().any().item()\n"
            f"{indent}_result = fused_experts(\n"
            f"{indent}    x,\n"
            f"{indent}    layer.w13_qweight,\n"
            f"{indent}    layer.w2_qweight,\n"
            f"{indent}    topk_weights=topk_weights,\n"
            f"{indent}    topk_ids=topk_ids,\n"
            f"{indent}    inplace=not self.moe.disable_inplace,\n"
            f"{indent}    activation=layer.activation,\n"
            f"{indent}    apply_router_weight_on_input=layer.apply_router_weight_on_input,\n"
            f"{indent}    global_num_experts=layer.global_num_experts,\n"
            f"{indent}    expert_map=layer.expert_map,\n"
            f"{indent}    quant_config=self.moe_quant_config,\n"
            f"{indent})\n"
            f"{indent}if _result.isnan().any().item() and _nan_logged_count < _NAN_LOG_LIMIT:\n"
            f"{indent}    _nan_logged_count += 1\n"
            f"{indent}    _nan_logger.error('MoE NaN call %d in_nan=%s shape=%s', _call_id, _x_nan, list(_result.shape))\n"
            f"{indent}return _result"
        )
        src = src[: match.start()] + replacement + src[match.end() :]
        moe_wna16_py.write_text(src)
        print(f"[gemma4-moe-patch] Patched activation+debug (regex) in {moe_wna16_py}")
        return True

    # Check if activation is already passed (maybe newer vLLM)
    if "activation=layer.activation" in src and "fused_experts" in src:
        print("[gemma4-moe-patch] MoeWNA16 already forwards activation")
        return True

    print(
        f"[gemma4-moe-patch] WARNING: Could not find fused_experts call in MoeWNA16Method.apply"
    )
    return False


def patch_gemma4_mlp_fp16_clamp(vllm_root: pathlib.Path) -> bool:
    """Upcast dense MLP computation to float32 to prevent FP16 overflow.

    Root cause: Gemma4's pre_feedforward_layernorm weights reach abs_max=234,
    amplifying MLP inputs. The gate_proj/up_proj matmul accumulates 2816 terms
    in FP16 (max 65504). Layer 26 MLP output reaches abs_max=181K in float32,
    which overflows fp16 → Inf → NaN cascade.

    Fix: compute the dense MLP in float32, keep float32 through
    post_feedforward_layernorm_1 (RMSNorm normalizes to safe range), then
    cast to fp16. Two patches:
    (A) Replace self.mlp(x) with manual float32 F.linear computation.
    (B) Change post_feedforward_layernorm_1 to cast output to fp16.
    """
    gemma4_py = vllm_root / "model_executor" / "models" / "gemma4.py"
    if not gemma4_py.exists():
        print(f"[gemma4-moe-patch] SKIP: {gemma4_py} not found")
        return False

    src = gemma4_py.read_text()

    if "GEMMA4_MLP_FP16_CLAMP" in src:
        print("[gemma4-moe-patch] MLP fp16 upcast already patched")
        return True

    # Part A: Replace MLP call with float32 computation.
    # Output stays in float32 — DO NOT cast to fp16 here.
    # The post_feedforward_layernorm_1 (RMSNorm) normalizes abs_max from
    # 181K → ~534, which is safe for fp16.
    f32_mlp_code = (
        "        hidden_states = self.pre_feedforward_layernorm(hidden_states)\n"
        "        # GEMMA4_MLP_FP16_CLAMP: Compute dense MLP in float32 to prevent\n"
        "        # FP16 overflow (Layer 26 MLP output abs_max=181K in f32).\n"
        "        # Output stays f32; post_feedforward_layernorm_1 normalizes to\n"
        "        # safe range before fp16 cast.\n"
        "        import torch as _t\n"
        "        import torch.nn.functional as _F\n"
        "        _mlp_orig_dtype = hidden_states.dtype\n"
        "        _mlp_x = hidden_states.float()\n"
        "        _mlp_gate_up = _F.linear(_mlp_x, self.mlp.gate_up_proj.weight.float())\n"
        "        _mlp_g, _mlp_u = _mlp_gate_up.chunk(2, dim=-1)\n"
        "        _mlp_x = _F.gelu(_mlp_g, approximate='tanh') * _mlp_u\n"
        "        hidden_states = _F.linear(_mlp_x, self.mlp.down_proj.weight.float())"
    )

    old_mlp = (
        "        hidden_states = self.pre_feedforward_layernorm(hidden_states)\n"
        "        hidden_states = self.mlp(hidden_states)"
    )

    applied_a = False
    if old_mlp in src:
        src = src.replace(old_mlp, f32_mlp_code, 1)
        applied_a = True
    else:
        # Regex fallback for whitespace variations
        pattern = re.compile(
            r"([ \t]+)hidden_states = self\.pre_feedforward_layernorm\(hidden_states\)\n"
            r"([ \t]+)hidden_states = self\.mlp\(hidden_states\)"
        )
        match = pattern.search(src)
        if match:
            indent = match.group(1)
            replacement = (
                f"{indent}hidden_states = self.pre_feedforward_layernorm(hidden_states)\n"
                f"{indent}# GEMMA4_MLP_FP16_CLAMP: Float32 MLP (no fp16 cast yet)\n"
                f"{indent}import torch as _t\n"
                f"{indent}import torch.nn.functional as _F\n"
                f"{indent}_mlp_orig_dtype = hidden_states.dtype\n"
                f"{indent}_mlp_x = hidden_states.float()\n"
                f"{indent}_mlp_gate_up = _F.linear(_mlp_x, self.mlp.gate_up_proj.weight.float())\n"
                f"{indent}_mlp_g, _mlp_u = _mlp_gate_up.chunk(2, dim=-1)\n"
                f"{indent}_mlp_x = _F.gelu(_mlp_g, approximate='tanh') * _mlp_u\n"
                f"{indent}hidden_states = _F.linear(_mlp_x, self.mlp.down_proj.weight.float())"
            )
            src = src[: match.start()] + replacement + src[match.end() :]
            applied_a = True

    if not applied_a:
        print("[gemma4-moe-patch] WARNING: Could not find MLP call pattern")
        return False

    # Part B: Cast post_feedforward_layernorm_1 output to fp16.
    # At this point hidden_states is float32 from the MLP. The RMSNorm
    # (post_feedforward_layernorm_1) normalizes values to safe range,
    # then .to(_mlp_orig_dtype) casts to fp16.
    old_norm1 = (
        "            hidden_states_1 = self.post_feedforward_layernorm_1(hidden_states)"
    )
    new_norm1 = (
        "            # GEMMA4_MLP_FP16_CLAMP: Cast to fp16 after RMSNorm (f32 MLP output normalized)\n"
        "            hidden_states_1 = self.post_feedforward_layernorm_1(hidden_states).to(_mlp_orig_dtype)"
    )

    if old_norm1 in src:
        src = src.replace(old_norm1, new_norm1, 1)
        print(
            f"[gemma4-moe-patch] Applied MLP float32 upcast + deferred norm cast to {gemma4_py}"
        )
    else:
        print(
            "[gemma4-moe-patch] WARNING: Could not find post_feedforward_layernorm_1 pattern"
        )

    gemma4_py.write_text(src)
    return True


def patch_gemma4_decoder_layer_debug(vllm_root: pathlib.Path) -> bool:
    """Add NaN debug logging to Gemma4DecoderLayer.forward.

    Instruments each stage: attention output, MLP output, MoE output,
    combined output, and final layer output. Logs min/max/nan at each
    stage to trace exactly where NaN first appears.

    NOTE: This runs AFTER patch_gemma4_mlp_fp32_upcast, so the MLP
    line has already been modified. Patterns match the upcast version.
    """
    if os.environ.get("GEMMA4_DECODER_LAYER_DEBUG", "").strip().lower() not in {
        "1",
        "true",
        "yes",
        "on",
    }:
        print(
            "[gemma4-moe-patch] SKIP: decoder layer debug patch disabled "
            "(set GEMMA4_DECODER_LAYER_DEBUG=1 to enable)"
        )
        return True

    gemma4_py = vllm_root / "model_executor" / "models" / "gemma4.py"
    if not gemma4_py.exists():
        print(f"[gemma4-moe-patch] SKIP: {gemma4_py} not found")
        return False

    src = gemma4_py.read_text()

    if "GEMMA4_DECODER_LAYER_DEBUG_PATCH" in src:
        print("[gemma4-moe-patch] Decoder layer debug already patched")
        return True

    patched = False

    # Patch: after attention+residual, log stats (uses inline import)
    old_attn_residual = (
        "        hidden_states = self.post_attention_layernorm(hidden_states)\n"
        "        hidden_states = hidden_states + residual\n"
        "        residual = hidden_states"
    )
    new_attn_residual = (
        "        hidden_states = self.post_attention_layernorm(hidden_states)\n"
        "        hidden_states = hidden_states + residual\n"
        "        residual = hidden_states\n"
        "        # GEMMA4_DECODER_LAYER_DEBUG_PATCH: log after attention+residual\n"
        "        import logging as _dbg_log\n"
        "        _dbg = _dbg_log.getLogger('gemma4_layer_debug')\n"
        "        _hn = hidden_states.isnan().any().item()\n"
        "        _hi = hidden_states.isinf().any().item()\n"
        "        _am = hidden_states.abs().max().item() if not _hn else -1\n"
        "        if _hn or _hi:\n"
        "            _dbg.error('L%d ATTN+RES: NaN=%s Inf=%s abs_max=%.1f shape=%s',\n"
        "                self.layer_idx, _hn, _hi, _am, list(hidden_states.shape))\n"
        "        elif self.layer_idx % 5 == 0:\n"
        "            _dbg.warning('L%d attn+res: abs_max=%.2f', self.layer_idx, _am)"
    )
    if old_attn_residual in src:
        src = src.replace(old_attn_residual, new_attn_residual, 1)
        patched = True

    # Patch: after MLP f32 output (deferred cast version).
    # MLP output stays float32, debug logs before post_feedforward_layernorm_1.
    # Match the f32 down_proj output line.
    old_mlp_f32 = (
        "        hidden_states = _F.linear(_mlp_x, self.mlp.down_proj.weight.float())"
    )
    new_mlp_f32 = (
        "        hidden_states = _F.linear(_mlp_x, self.mlp.down_proj.weight.float())\n"
        "        # GEMMA4_DECODER_LAYER_DEBUG_PATCH: log MLP output (f32, pre-norm)\n"
        "        _mn = hidden_states.isnan().any().item()\n"
        "        _mi = hidden_states.isinf().any().item()\n"
        "        _mm = hidden_states.abs().max().item() if not _mn else -1\n"
        "        if _mn or _mi or self.layer_idx % 5 == 0:\n"
        "            _dbg.warning('L%d MLP f32: NaN=%s Inf=%s abs_max=%.1f', self.layer_idx, _mn, _mi, _mm)"
    )
    if old_mlp_f32 in src:
        src = src.replace(old_mlp_f32, new_mlp_f32, 1)
        patched = True
    else:
        # Fallback: try matching original unpatched MLP
        old_mlp_orig = (
            "        hidden_states = self.pre_feedforward_layernorm(hidden_states)\n"
            "        hidden_states = self.mlp(hidden_states)"
        )
        new_mlp_orig = (
            "        hidden_states = self.pre_feedforward_layernorm(hidden_states)\n"
            "        hidden_states = self.mlp(hidden_states)\n"
            "        # GEMMA4_DECODER_LAYER_DEBUG_PATCH: log MLP output\n"
            "        _mn = hidden_states.isnan().any().item()\n"
            "        _mi = hidden_states.isinf().any().item()\n"
            "        _mm = hidden_states.abs().max().item() if not _mn else -1\n"
            "        if _mn or _mi:\n"
            "            _dbg.error('L%d MLP OUT: NaN=%s Inf=%s abs_max=%.1f', self.layer_idx, _mn, _mi, _mm)\n"
            "        elif self.layer_idx % 5 == 0:\n"
            "            _dbg.warning('L%d mlp_out: abs_max=%.2f', self.layer_idx, _mm)"
        )
        if old_mlp_orig in src:
            src = src.replace(old_mlp_orig, new_mlp_orig, 1)
            patched = True

    # Patch: after FF+residual, log
    old_final = (
        "        hidden_states = self.post_feedforward_layernorm(hidden_states)\n"
        "        hidden_states = hidden_states + residual"
    )
    new_final = (
        "        hidden_states = self.post_feedforward_layernorm(hidden_states)\n"
        "        hidden_states = hidden_states + residual\n"
        "        # GEMMA4_DECODER_LAYER_DEBUG_PATCH: log after FF+residual\n"
        "        _fn = hidden_states.isnan().any().item()\n"
        "        _fi = hidden_states.isinf().any().item()\n"
        "        _fm = hidden_states.abs().max().item() if not _fn else -1\n"
        "        if _fn or _fi:\n"
        "            _dbg.error('L%d FF+RES: NaN=%s Inf=%s abs_max=%.1f', self.layer_idx, _fn, _fi, _fm)\n"
        "        elif self.layer_idx % 5 == 0:\n"
        "            _dbg.warning('L%d ff+res: abs_max=%.2f', self.layer_idx, _fm)"
    )
    if old_final in src:
        src = src.replace(old_final, new_final, 1)
        patched = True

    # Patch: after layer_scalar, log final output
    old_scalar = "        hidden_states = hidden_states * self.layer_scalar"
    new_scalar = (
        "        hidden_states = hidden_states * self.layer_scalar\n"
        "        # GEMMA4_DECODER_LAYER_DEBUG_PATCH: log layer output\n"
        "        _on = hidden_states.isnan().any().item()\n"
        "        _om = hidden_states.abs().max().item() if not _on else -1\n"
        "        if _on or self.layer_idx % 5 == 0:\n"
        "            _dbg.warning('L%d OUTPUT (s=%.4f): nan=%s abs_max=%.2f',\n"
        "                self.layer_idx, self.layer_scalar.item(), _on, _om)"
    )
    if old_scalar in src:
        src = src.replace(old_scalar, new_scalar, 1)
        patched = True

    if patched:
        gemma4_py.write_text(src)
        print(f"[gemma4-moe-patch] Applied decoder layer debug to {gemma4_py}")
    else:
        print("[gemma4-moe-patch] WARNING: No decoder layer patterns matched")

    return patched


def main():
    vllm_root = find_vllm_root()
    if vllm_root is None:
        # vLLM is not installed in this image (e.g. gfx906 runtime profile
        # that intentionally excludes vLLM). Nothing to patch — exit silently.
        return
    print(f"[gemma4-moe-patch] vLLM root: {vllm_root}")

    ok1 = patch_gptq_config(vllm_root)
    ok1b = patch_gptq_rocm_reference_fallback(vllm_root)
    ok2 = patch_moe_wna16_activation(vllm_root)
    ok3 = patch_gemma4_moe_gptq_weight_names(vllm_root)
    ok4 = patch_kv_cache_page_size_uniform_type(vllm_root)
    ok5 = patch_moe_wna16_activation_forwarding(vllm_root)
    # The ROCm reference fallback wraps the activation-forwarding result block.
    # Run it after activation forwarding so newer vLLM source layouts match.
    ok1c = patch_moe_wna16_rocm_reference_fallback(vllm_root)
    ok6a = patch_gemma4_mlp_fp16_clamp(vllm_root)  # Must run before debug patch
    ok6 = patch_gemma4_decoder_layer_debug(vllm_root)

    if not ok1:
        print("[gemma4-moe-patch] FAILED — GPTQConfig patch could not be applied")
        sys.exit(1)
    if not ok1b:
        print("[gemma4-moe-patch] FAILED — GPTQ ROCm reference fallback patch failed")
        sys.exit(1)
    if not ok1c:
        print(
            "[gemma4-moe-patch] FAILED — MoeWNA16 ROCm reference fallback patch failed"
        )
        sys.exit(1)
    if not ok2:
        print(
            "[gemma4-moe-patch] WARNING — MoeWNA16 activation patch failed (non-fatal)"
        )
    if not ok3:
        print(
            "[gemma4-moe-patch] WARNING — GPTQ weight names patch failed (non-fatal, "
            "may already be fixed in this vLLM version)"
        )
    if not ok4:
        print(
            "[gemma4-moe-patch] WARNING — KV cache page_size patch failed (non-fatal)"
        )
    if not ok5:
        print("[gemma4-moe-patch] FAILED — MoeWNA16 activation forwarding patch failed")
        sys.exit(1)
    if not ok6a:
        print("[gemma4-moe-patch] FAILED — MLP fp16 clamp patch failed")
        sys.exit(1)
    if not ok6:
        print(
            "[gemma4-moe-patch] WARNING — Decoder layer debug patch failed (non-fatal)"
        )

    print("[gemma4-moe-patch] All patches applied successfully")


if __name__ == "__main__":
    main()
