"""Complete vLLM's registration for text-only Qwen3.5 models."""

import os
import sys

ARCHITECTURES = {
    "Qwen3_5ForCausalLM": "dense",
    "Qwen3_5MoeForCausalLM": "moe",
}
_TEXT_ONLY_ROPE_PARAMETERS = {
    "rope_type": "default",
    "rope_theta": 10_000_000,
    "partial_rotary_factor": 0.25,
}
_MAMBA_METHODS = (
    "get_mamba_state_shape_from_config",
    "get_mamba_state_dtype_from_config",
    "get_mamba_state_copy_func",
)


def _rename_expert_weights(weights):
    for name, weight in weights:
        name = name.replace(
            ".moe.gate_up_proj", ".mlp.experts.gate_up_proj"
        ).replace(".moe.down_proj", ".mlp.experts.down_proj")
        yield name, weight


def _gptq_expert_parameter_name(name: str) -> str:
    for projection in ("w13", "w2"):
        for suffix in ("qweight", "scales", "qzeros", "g_idx", "g_idx_sort_indices"):
            name = name.replace(
                f"{projection}_weight.{suffix}", f"{projection}_{suffix}"
            )
    return name


def _select_rocm_safe_fla_configs() -> int:
    """Collapse loaded FLA autotuners to conservative consumer-ROCm configs.

    The 4-stage variant can remain in LLVM codegen indefinitely on consumer
    ROCm targets. Qwen3.5's GDN prefill path contains several independent
    autotuners, so pruning only KKT still leaves the engine stuck compiling
    ``chunk_delta_h`` and later kernels. Preserve the first config among the
    lowest stage/warp candidates for every loaded FLA autotuner.
    """
    prefix = "vllm.model_executor.layers.fla.ops."
    pruned: set[int] = set()
    for module_name, module in tuple(sys.modules.items()):
        if not module_name.startswith(prefix) or module is None:
            continue
        for value in vars(module).values():
            candidate = value
            for _ in range(5):
                configs = getattr(candidate, "configs", None)
                if isinstance(configs, (list, tuple)) and configs:
                    candidate_id = id(candidate)
                    if candidate_id not in pruned:
                        _, safe = min(
                            enumerate(configs),
                            key=lambda item: (
                                getattr(item[1], "num_stages", 1) or 1,
                                getattr(item[1], "num_warps", 1) or 1,
                                item[0],
                            ),
                        )
                        candidate.configs = [safe]
                        pruned.add(candidate_id)
                    break
                candidate = getattr(candidate, "fn", None)
                if candidate is None:
                    break
    if not pruned:
        raise RuntimeError("vLLM FLA safe autotune configurations are missing")
    return len(pruned)


def _strict_env_flag(name: str) -> bool:
    value = os.environ.get(name, "0")
    if value not in {"0", "1"}:
        raise RuntimeError(f"{name} must be 0 or 1, got {value!r}")
    return value == "1"


def _patch_qwen35_text_mtp_config(speculative_config) -> None:
    """Teach vLLM 0.23's MTP override about the text-only model type."""
    if getattr(speculative_config, "_flexinfer_qwen35_text_mtp", False):
        return

    original_hf_config_override = speculative_config.hf_config_override

    def hf_config_override(hf_config):
        model_type = getattr(hf_config, "model_type", None)
        if model_type in {"qwen3_5_text", "qwen3_5_moe_text"}:
            n_predict = getattr(hf_config, "mtp_num_hidden_layers", None)
            hf_config.model_type = "qwen3_5_mtp"
            hf_config.update(
                {
                    "n_predict": n_predict,
                    "architectures": [
                        "Qwen3_5MoeMTP"
                        if model_type == "qwen3_5_moe_text"
                        else "Qwen3_5MTP"
                    ],
                    # The target text model receives this same non-MRoPE
                    # contract through --hf-overrides. SpeculativeConfig
                    # otherwise reloads the raw multimodal artifact config,
                    # marks the runner as MRoPE, and sends [3, N] positions to
                    # the generic Qwen3Next rotary embedding.
                    "rope_parameters": dict(_TEXT_ONLY_ROPE_PARAMETERS),
                }
            )
            return hf_config
        return original_hf_config_override(hf_config)

    speculative_config.hf_config_override = staticmethod(hf_config_override)
    speculative_config._flexinfer_qwen35_text_mtp = True


def _needs_safe_fla_config(torch_module) -> bool:
    """Return whether this process targets a consumer ROCm architecture."""
    configured = os.environ.get("PYTORCH_ROCM_ARCH", "")
    if configured:
        return configured in {"gfx906", "gfx1100"}
    try:
        if torch_module.cuda.is_available():
            arch = torch_module.cuda.get_device_properties(0).gcnArchName
            return str(arch).split(":", 1)[0] in {"gfx906", "gfx1100"}
    except (AttributeError, RuntimeError):
        pass
    return False


def _patch_qwen35_mtp_gptq(
    auto_gptq_config,
    linear_base,
    routed_experts,
    unquantized_linear_method,
    unquantized_fused_moe_method,
    *,
    quantized_mtp_experts: bool = False,
) -> None:
    """Select the checkpoint-declared quantization contract for the MTP head.

    The normal Qwen artifact preserves all ``mtp.*`` weights in floating point.
    The gfx1100 surgery artifact keeps only MTP linear layers floating point and
    stores its routed experts as GPTQ W4G128. The latter must be explicitly
    enabled by the probe; the default remains the upstream plain-head contract.
    """
    mode = "quantized-experts" if quantized_mtp_experts else "plain"
    if auto_gptq_config.__dict__.get("_flexinfer_qwen35_mtp_mode") == mode:
        return
    if "_flexinfer_qwen35_mtp_mode" in auto_gptq_config.__dict__:
        raise RuntimeError("Qwen3.5 MTP GPTQ mode cannot change after registration")

    original_get_quant_method = auto_gptq_config.get_quant_method

    def get_quant_method(self, layer, prefix):
        if "mtp" in prefix.split("."):
            if (
                routed_experts is not None
                and isinstance(layer, routed_experts)
                and not quantized_mtp_experts
            ):
                return unquantized_fused_moe_method(layer.moe_config)
            if isinstance(layer, linear_base):
                return unquantized_linear_method()
        return original_get_quant_method(self, layer, prefix)

    auto_gptq_config.get_quant_method = get_quant_method
    auto_gptq_config._flexinfer_qwen35_mtp_mode = mode


def _patch_gptq_expert_loader(model_class) -> None:
    """Normalize vLLM's mapped GPTQ expert parameter names for one model class."""
    if model_class.__dict__.get("_flexinfer_gptq_expert_names", False):
        return
    original_load_fused_expert_weights = model_class.load_fused_expert_weights

    def load_fused_expert_weights(
        self,
        name,
        params_dict,
        loaded_weight,
        shard_id,
        num_experts,
    ):
        name = _gptq_expert_parameter_name(name)
        if name not in params_dict:
            return False
        return original_load_fused_expert_weights(
            self,
            name,
            params_dict,
            loaded_weight,
            shard_id,
            num_experts,
        )

    model_class.load_fused_expert_weights = load_fused_expert_weights
    model_class._flexinfer_gptq_expert_names = True


def register() -> None:
    """Register the existing text class and its hybrid-state contract.

    vLLM 0.23 ships the class but only registers the multimodal wrapper. A
    text-only checkpoint consequently falls through to that wrapper and tries
    to access ``vision_config``. The text class also omits the hybrid marker
    and state helpers that the wrapper supplies. General plugins run in both
    the API process and EngineCore, so this repair survives multiprocessing.
    """
    import torch
    from vllm import ModelRegistry
    from vllm.config.speculative import SpeculativeConfig
    import vllm.model_executor.layers.fla.ops.chunk_scaled_dot_kkt  # noqa: F401
    from vllm.model_executor.layers.fused_moe import UnquantizedFusedMoEMethod
    from vllm.model_executor.layers.linear import (
        LinearBase,
        UnquantizedLinearMethod,
    )
    try:
        from vllm.model_executor.layers.fused_moe import RoutedExperts
    except ImportError:
        # vLLM <=0.20 predates the modular RoutedExperts abstraction. Dense
        # Qwen3.5 MTP only needs the LinearBase override; keep MoE registration
        # available while declining to invent an unsafe expert type mapping.
        RoutedExperts = None
    try:
        from vllm.model_executor.layers.quantization.auto_gptq import AutoGPTQConfig
    except ImportError:
        from vllm.model_executor.layers.quantization.gptq import (
            GPTQConfig as AutoGPTQConfig,
        )
    from vllm.model_executor.models.qwen3_5 import (
        Qwen3_5ForCausalLM,
        Qwen3_5ForConditionalGeneration,
        Qwen3_5Model,
        Qwen3_5MoeForCausalLM,
    )
    from vllm.model_executor.models.qwen3_5_mtp import Qwen3_5MultiTokenPredictor

    safe_fla_kernels = 0
    if torch.version.hip is not None and _needs_safe_fla_config(torch):
        safe_fla_kernels = _select_rocm_safe_fla_configs()
    _patch_qwen35_text_mtp_config(SpeculativeConfig)
    quantized_mtp_experts = _strict_env_flag(
        "FLEXINFER_QWEN35_MTP_EXPERTS_GPTQ"
    )
    _patch_qwen35_mtp_gptq(
        AutoGPTQConfig,
        LinearBase,
        RoutedExperts,
        UnquantizedLinearMethod,
        UnquantizedFusedMoEMethod,
        quantized_mtp_experts=quantized_mtp_experts,
    )

    for model_class in (Qwen3_5ForCausalLM, Qwen3_5MoeForCausalLM):
        model_class.is_hybrid = True
        for method_name in _MAMBA_METHODS:
            setattr(
                model_class,
                method_name,
                Qwen3_5ForConditionalGeneration.__dict__[method_name],
            )

    if not getattr(Qwen3_5MoeForCausalLM, "_flexinfer_expert_names", False):
        original_load_weights = Qwen3_5MoeForCausalLM.load_weights

        def load_weights(self, weights):
            return original_load_weights(self, _rename_expert_weights(weights))

        Qwen3_5MoeForCausalLM.load_weights = load_weights
        Qwen3_5MoeForCausalLM._flexinfer_expert_names = True

    _patch_gptq_expert_loader(Qwen3_5Model)
    _patch_gptq_expert_loader(Qwen3_5MultiTokenPredictor)

    ModelRegistry.register_model("Qwen3_5ForCausalLM", Qwen3_5ForCausalLM)
    ModelRegistry.register_model("Qwen3_5MoeForCausalLM", Qwen3_5MoeForCausalLM)
    print(
        "flexinfer: registered text-only Qwen3.5 dense + MoE architectures "
        "with hybrid-state support "
        "and ROCm-safe FLA/MTP GPTQ handling "
        f"(fla_autotuners={safe_fla_kernels}, "
        f"mtp_experts={'gptq' if quantized_mtp_experts else 'plain'})"
    )
