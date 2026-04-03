#!/usr/bin/env python3
"""Patch vLLM source for FlexInfer's Gemma4 ROCm runtime needs.

This currently applies six source patches:
1. env_override.py Torch 2.9 CaptureOutput compatibility.
2. KV-sharing helper fix so shared layers are restored for metadata only,
   without incorrectly allocating duplicate KV cache tensors.
3. GPU model runner metadata fix so shared layers inherit their target
   layer's KV cache spec when grouping attention backends.
4. GPU model runner reshape fix so per-token-head scale padding is
   respected when deriving backend KV tensor shapes.
5. Attention-layer fix so explicit non-quantized KV cache dtypes are honored
   for Gemma4 instead of being silently overridden back to quantized KV mode.
6. Backend selection fix so Gemma4 models keep TRITON attention when an
   explicit non-quantized KV cache dtype is requested.
"""

from __future__ import annotations

import pathlib
import sys


ENV_OVERRIDE_OLD = """    from torch._dynamo.convert_frame import GraphCaptureOutput

    _original_get_runtime_env = GraphCaptureOutput.get_runtime_env

    def _safe_builtins_dict(builtins_dict: dict) -> dict:
        \"\"\"Filter a builtins dict to only picklable entries for serialization.\"\"\"
        result = {}
        for k, v in builtins_dict.items():
            try:
                pickle.dumps(v)
                result[k] = v
            except Exception:
                pass
        return result

    def _patched_get_runtime_env(self):  # type: ignore[no-untyped-def]
        runtime_env = _original_get_runtime_env(self)
        for ref in runtime_env.external_refs:
            if ref not in runtime_env.used_globals:
                if ref.startswith(\"__builtins_dict__\") and ref in self.f_globals:
                    runtime_env.used_globals[ref] = _safe_builtins_dict(
                        self.f_globals[ref]
                    )
                elif hasattr(_builtins, ref):
                    runtime_env.used_globals[ref] = getattr(_builtins, ref)
        return runtime_env

    GraphCaptureOutput.get_runtime_env = _patched_get_runtime_env
"""

ENV_OVERRIDE_NEW = """    try:
        from torch._dynamo.convert_frame import GraphCaptureOutput as _CaptureOutput
    except ImportError:
        # torch 2.9 exposes CaptureOutput instead of GraphCaptureOutput.
        from torch._dynamo.convert_frame import CaptureOutput as _CaptureOutput

    if hasattr(_CaptureOutput, \"get_runtime_env\"):
        _original_get_runtime_env = _CaptureOutput.get_runtime_env

        def _safe_builtins_dict(builtins_dict: dict) -> dict:
            \"\"\"Filter a builtins dict to only picklable entries for serialization.\"\"\"
            result = {}
            for k, v in builtins_dict.items():
                try:
                    pickle.dumps(v)
                    result[k] = v
                except Exception:
                    pass
            return result

        def _patched_get_runtime_env(self):  # type: ignore[no-untyped-def]
            runtime_env = _original_get_runtime_env(self)
            for ref in runtime_env.external_refs:
                if ref not in runtime_env.used_globals:
                    if ref.startswith(\"__builtins_dict__\") and ref in self.f_globals:
                        runtime_env.used_globals[ref] = _safe_builtins_dict(
                            self.f_globals[ref]
                        )
                    elif hasattr(_builtins, ref):
                        runtime_env.used_globals[ref] = getattr(_builtins, ref)
            return runtime_env

        _CaptureOutput.get_runtime_env = _patched_get_runtime_env
    else:
        print(
            \"skip GraphCaptureOutput runtime-env patch: torch CaptureOutput \"
            \"has no get_runtime_env\"
        )
"""

KV_SHARING_OLD = """    for layer_name, target_layer_name in shared_kv_cache_layers.items():
        tgt_kv_cache_group = layer_to_kv_cache_group[target_layer_name]
        tgt_kv_cache_group.layer_names.append(layer_name)

        if runner_only_attn_layers is not None:
            runner_only_attn_layers.add(layer_name)
"""

KV_SHARING_NEW = """    for layer_name, target_layer_name in shared_kv_cache_layers.items():
        tgt_kv_cache_group = layer_to_kv_cache_group[target_layer_name]
        tgt_kv_cache_group.layer_names.append(layer_name)
        if isinstance(tgt_kv_cache_group.kv_cache_spec, UniformTypeKVCacheSpecs):
            tgt_kv_cache_group.kv_cache_spec.kv_cache_specs[layer_name] = (
                tgt_kv_cache_group.kv_cache_spec.kv_cache_specs[target_layer_name]
            )

        if runner_only_attn_layers is not None:
            runner_only_attn_layers.add(layer_name)
"""

GPU_MODEL_RUNNER_OLD = """                layer_kv_cache_spec = kv_cache_group_spec.kv_cache_spec
                if isinstance(layer_kv_cache_spec, UniformTypeKVCacheSpecs):
                    layer_kv_cache_spec = layer_kv_cache_spec.kv_cache_specs[layer_name]
"""

GPU_MODEL_RUNNER_NEW = """                layer_kv_cache_spec = kv_cache_group_spec.kv_cache_spec
                if isinstance(layer_kv_cache_spec, UniformTypeKVCacheSpecs):
                    if layer_name in layer_kv_cache_spec.kv_cache_specs:
                        layer_kv_cache_spec = layer_kv_cache_spec.kv_cache_specs[layer_name]
                    else:
                        layer_kv_cache_spec = layer_kv_cache_spec.kv_cache_specs[
                            self.shared_kv_cache_layers[layer_name]
                        ]
"""

GPU_MODEL_RUNNER_RESHAPE_OLD = """                    kv_cache_shape = attn_backend.get_kv_cache_shape(
                        kernel_num_blocks,
                        kernel_block_size,
                        kv_cache_spec.num_kv_heads,
                        kv_cache_spec.head_size,
                        cache_dtype_str=self.cache_config.cache_dtype,
                    )
"""

GPU_MODEL_RUNNER_RESHAPE_NEW = """                    kv_cache_shape = attn_backend.get_kv_cache_shape(
                        kernel_num_blocks,
                        kernel_block_size,
                        kv_cache_spec.num_kv_heads,
                        kv_cache_spec.head_size,
                        cache_dtype_str=(
                            "fp8_per_token_head"
                            if getattr(kv_cache_spec, "kv_quant_mode", None).name
                            == "FP8_PER_TOKEN_HEAD"
                            else "int8_per_token_head"
                            if getattr(kv_cache_spec, "kv_quant_mode", None).name
                            == "INT8_PER_TOKEN_HEAD"
                            else self.cache_config.cache_dtype
                        ),
                    )
"""

GPU_MODEL_RUNNER_SHARED_SKIP_OLD = """            for layer_name in group.layer_names:
                if layer_name in self.runner_only_attn_layers:
                    continue
                raw_tensor = kv_cache_raw_tensors[layer_name]
"""

GPU_MODEL_RUNNER_SHARED_SKIP_NEW = """            for layer_name in group.layer_names:
                if (
                    layer_name in self.runner_only_attn_layers
                    or layer_name in self.shared_kv_cache_layers
                ):
                    continue
                raw_tensor = kv_cache_raw_tensors[layer_name]
"""

GPU_MODEL_RUNNER_PADDED_RESHAPE_OLD = """                    )
                    dtype = kv_cache_spec.dtype
                    try:
"""

GPU_MODEL_RUNNER_PADDED_RESHAPE_NEW = """                    )
                    dtype = kv_cache_spec.dtype
                    padded_last_dim = None
                    page_size_padded = getattr(kv_cache_spec, "page_size_padded", None)
                    if page_size_padded is not None and kv_cache_shape[-1] > 0:
                        dtype_size = get_dtype_size(dtype)
                        denom = (
                            2
                            * kv_cache_spec.block_size
                            * kv_cache_spec.num_kv_heads
                            * dtype_size
                        )
                        if denom > 0 and page_size_padded % denom == 0:
                            padded_last_dim = page_size_padded // denom

                    if padded_last_dim is None:
                        view_numel = raw_tensor.view(dtype).numel()
                        target_numel = 1
                        for dim in kv_cache_shape:
                            target_numel *= dim
                        if view_numel != target_numel and kv_cache_shape[-1] > 0:
                            base_numel = target_numel // kv_cache_shape[-1]
                            if base_numel > 0 and view_numel % base_numel == 0:
                                padded_last_dim = view_numel // base_numel

                    if (
                        padded_last_dim is not None
                        and padded_last_dim > kv_cache_shape[-1]
                    ):
                        kv_cache_shape = (*kv_cache_shape[:-1], padded_last_dim)
                    try:
"""

GPU_MODEL_RUNNER_VIEW_OLD = """                    kv_caches[layer_name] = (
                        kv_cache_raw_tensors[layer_name]
                        .view(dtype)
                        .view(kv_cache_shape)
                        .permute(*inv_order)
                    )
"""

GPU_MODEL_RUNNER_VIEW_NEW = """                    try:
                        kv_caches[layer_name] = (
                            kv_cache_raw_tensors[layer_name]
                            .view(dtype)
                            .view(kv_cache_shape)
                            .permute(*inv_order)
                        )
                    except RuntimeError as e:
                        logger.error(
                            "KV reshape failed for layer=%s target=%s spec=%s "
                            "kv_quant_mode=%s page_size_bytes=%s page_size_padded=%s "
                            "num_blocks=%s kernel_num_blocks=%s raw_numel=%s "
                            "view_numel=%s kv_cache_shape=%s stride_order=%s",
                            layer_name,
                            self.shared_kv_cache_layers.get(layer_name),
                            kv_cache_spec,
                            getattr(kv_cache_spec, "kv_quant_mode", None),
                            getattr(kv_cache_spec, "page_size_bytes", None),
                            getattr(kv_cache_spec, "page_size_padded", None),
                            num_blocks,
                            kernel_num_blocks,
                            raw_tensor.numel(),
                            raw_tensor.view(dtype).numel(),
                            kv_cache_shape,
                            kv_cache_stride_order,
                        )
                        raise
"""

ATTENTION_OVERRIDE_OLD = """        # llm-compressor mdls need to set cache_dtype to "fp8" manually.
        kv_cache_scheme = getattr(quant_config, "kv_cache_scheme", None)
        if kv_cache_scheme is not None:
            kv_cache_dtype = "fp8"
            calculate_kv_scales = False
            if cache_config is not None:
                cache_config.cache_dtype = "fp8"
                cache_config.calculate_kv_scales = False

        # Check if per-head quant scales are required based on kv_cache_scheme
        use_per_head_quant_scales = (
            kv_cache_scheme is not None
            and kv_cache_scheme.get("strategy") == "attn_head"
        )
"""

ATTENTION_OVERRIDE_NEW = """        # llm-compressor models may advertise a kv-cache quantization scheme,
        # but Gemma4 on the ROCm/TRITON path currently needs the explicit
        # non-quantized request to win over that override.
        kv_cache_scheme = getattr(quant_config, "kv_cache_scheme", None)
        hf_config = getattr(vllm_config.model_config, "hf_config", None)
        text_hf_config = getattr(hf_config, "text_config", None)
        model_types = {
            getattr(hf_config, "model_type", None),
            getattr(text_hf_config, "model_type", None),
        }
        explicit_nonquant_kv = (
            any(
                isinstance(model_type, str) and model_type.startswith("gemma4")
                for model_type in model_types
            )
            and cache_config is not None
            and cache_config.cache_dtype in ("float16", "bfloat16")
        )
        if kv_cache_scheme is not None and not explicit_nonquant_kv:
            kv_cache_dtype = "fp8"
            calculate_kv_scales = False
            if cache_config is not None:
                cache_config.cache_dtype = "fp8"
                cache_config.calculate_kv_scales = False
        elif explicit_nonquant_kv:
            kv_cache_scheme = None
            kv_cache_dtype = cache_config.cache_dtype
            calculate_kv_scales = False

        # Check if per-head quant scales are required based on kv_cache_scheme
        use_per_head_quant_scales = (
            kv_cache_scheme is not None
            and kv_cache_scheme.get("strategy") == "attn_head"
        )
"""

ATTENTION_BACKEND_OLD = """                kv_cache_dtype,
                use_mla=False,
                has_sink=self.has_sink,
                use_mm_prefix=self.use_mm_prefix,
                use_per_head_quant_scales=use_per_head_quant_scales,
                attn_type=attn_type,
            )
"""

ATTENTION_BACKEND_NEW = """                kv_cache_dtype,
                use_mla=False,
                has_sink=self.has_sink,
                use_mm_prefix=self.use_mm_prefix,
                use_per_head_quant_scales=(
                    use_per_head_quant_scales and not explicit_nonquant_kv
                ),
                attn_type=attn_type,
            )
"""


def _replace_once(path: pathlib.Path, old: str, new: str) -> None:
    text = path.read_text()
    if new in text:
        print(f"already patched: {path}")
        return
    if old not in text:
        print(f"unexpected file contents: {path}", file=sys.stderr)
        raise SystemExit(1)
    path.write_text(text.replace(old, new, 1))
    print(f"patched: {path}")


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: patch_vllm_env_override_torch29.py <vllm-source-root>", file=sys.stderr)
        return 2

    root = pathlib.Path(sys.argv[1])
    env_override = root / "vllm" / "env_override.py"
    kv_sharing = root / "vllm" / "v1" / "worker" / "utils.py"
    gpu_model_runner = root / "vllm" / "v1" / "worker" / "gpu_model_runner.py"
    attention = root / "vllm" / "model_executor" / "layers" / "attention" / "attention.py"

    _replace_once(env_override, ENV_OVERRIDE_OLD, ENV_OVERRIDE_NEW)
    _replace_once(kv_sharing, KV_SHARING_OLD, KV_SHARING_NEW)
    _replace_once(gpu_model_runner, GPU_MODEL_RUNNER_OLD, GPU_MODEL_RUNNER_NEW)
    _replace_once(
        gpu_model_runner,
        GPU_MODEL_RUNNER_RESHAPE_OLD,
        GPU_MODEL_RUNNER_RESHAPE_NEW,
    )
    _replace_once(
        gpu_model_runner,
        GPU_MODEL_RUNNER_SHARED_SKIP_OLD,
        GPU_MODEL_RUNNER_SHARED_SKIP_NEW,
    )
    _replace_once(
        gpu_model_runner,
        GPU_MODEL_RUNNER_PADDED_RESHAPE_OLD,
        GPU_MODEL_RUNNER_PADDED_RESHAPE_NEW,
    )
    _replace_once(
        gpu_model_runner,
        GPU_MODEL_RUNNER_VIEW_OLD,
        GPU_MODEL_RUNNER_VIEW_NEW,
    )
    _replace_once(attention, ATTENTION_OVERRIDE_OLD, ATTENTION_OVERRIDE_NEW)
    _replace_once(attention, ATTENTION_BACKEND_OLD, ATTENTION_BACKEND_NEW)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
