"""Complete vLLM 0.23's registration for text-only Qwen3.5 MoE models."""

import os

ARCHITECTURE = "Qwen3_5MoeForCausalLM"
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


def _select_gfx1100_safe_fla_config(kernel) -> None:
    """Avoid a Triton/LLVM codegen cliff in the FLA KKT autotuner.

    The 4-stage gfx1100 variant can remain in LLVM codegen indefinitely. The
    first 2-stage configuration is already compiled and proven on this model.
    """
    autotuner = kernel.fn
    safe = [
        config
        for config in autotuner.configs
        if config.kwargs.get("BK") == 32
        and config.num_warps == 2
        and config.num_stages == 2
    ]
    if not safe:
        raise RuntimeError("vLLM FLA safe autotune configuration is missing")
    autotuner.configs = safe


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
        if getattr(hf_config, "model_type", None) == "qwen3_5_moe_text":
            n_predict = getattr(hf_config, "mtp_num_hidden_layers", None)
            hf_config.model_type = "qwen3_5_mtp"
            hf_config.update(
                {
                    "n_predict": n_predict,
                    "architectures": ["Qwen3_5MoeMTP"],
                }
            )
            return hf_config
        return original_hf_config_override(hf_config)

    speculative_config.hf_config_override = staticmethod(hf_config_override)
    speculative_config._flexinfer_qwen35_text_mtp = True


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
            if isinstance(layer, routed_experts) and not quantized_mtp_experts:
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
    from vllm.model_executor.layers.fla.ops.chunk_scaled_dot_kkt import (
        chunk_scaled_dot_kkt_fwd_kernel,
    )
    from vllm.model_executor.layers.fused_moe import (
        RoutedExperts,
        UnquantizedFusedMoEMethod,
    )
    from vllm.model_executor.layers.linear import (
        LinearBase,
        UnquantizedLinearMethod,
    )
    from vllm.model_executor.layers.quantization.auto_gptq import AutoGPTQConfig
    from vllm.model_executor.models.qwen3_5 import (
        Qwen3_5ForConditionalGeneration,
        Qwen3_5Model,
        Qwen3_5MoeForCausalLM,
    )
    from vllm.model_executor.models.qwen3_5_mtp import Qwen3_5MultiTokenPredictor

    if torch.version.hip is not None:
        _select_gfx1100_safe_fla_config(chunk_scaled_dot_kkt_fwd_kernel)
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

    Qwen3_5MoeForCausalLM.is_hybrid = True
    for method_name in _MAMBA_METHODS:
        setattr(
            Qwen3_5MoeForCausalLM,
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

    ModelRegistry.register_model(ARCHITECTURE, Qwen3_5MoeForCausalLM)
    print(
        f"flexinfer: registered {ARCHITECTURE} with hybrid-state support "
        "and ROCm-safe FLA/MTP GPTQ handling "
        f"(mtp_experts={'gptq' if quantized_mtp_experts else 'plain'})"
    )
