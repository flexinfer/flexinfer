"""Complete vLLM's registration for text-only Qwen3.5 models.

ROCm autotune pruning and the hybrid-state contract live in
``flexinfer_vllm_common``, shared with every other FlexInfer vLLM plugin so the
two copies cannot drift. Install ``build/vllm-plugin-common`` before this
package.
"""

import os

from flexinfer_vllm_common import apply_hybrid_state
from flexinfer_vllm_common.hybrid import MAMBA_STATE_METHODS as _MAMBA_METHODS
from flexinfer_vllm_common.rocm import (
    FLA_KKT_MODULE as _FLA_KKT_MODULE,
    FLA_OPS_PREFIXES as _FLA_OPS_PREFIXES,
    import_fla_kkt_module as _import_fla_kkt_module,
    needs_safe_fla_config as _needs_safe_fla_config,
    prune_fla_configs_if_needed,
    select_rocm_safe_fla_configs as _select_rocm_safe_fla_configs,
)

ARCHITECTURES = {
    "Qwen3_5ForCausalLM": "dense",
    "Qwen3_5MoeForCausalLM": "moe",
}
_TEXT_ONLY_ROPE_PARAMETERS = {
    "rope_type": "default",
    "rope_theta": 10_000_000,
    "partial_rotary_factor": 0.25,
}
# vLLM's flash-linear-attention (GDN) kernels moved between releases:
#   <= 0.23  vllm.model_executor.layers.fla.ops.*
#   >= 0.26  vllm.third_party.flash_linear_attention.ops.*
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


def _patch_gptq_expert_loader(model_class) -> bool:
    """Normalize vLLM's mapped GPTQ expert parameter names for one model class.

    Returns whether the hook is installed. vLLM 0.23 loads fused MoE experts
    through ``load_fused_expert_weights`` on the model class; vLLM 0.26 no
    longer has that method (expert loading moved into the generic weight
    loader), so there is nothing to wrap and the MoE GPTQ expert-name contract
    is NOT provided on that runtime. Dense Qwen3.5/3.8 lanes never reach this
    path; MoE GPTQ lanes must stay on a runtime where the hook installs until
    the 0.26 expert loader is re-qualified.
    """
    if model_class.__dict__.get("_flexinfer_gptq_expert_names", False):
        return True
    if not hasattr(model_class, "load_fused_expert_weights"):
        return False
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
    return True


def register() -> None:
    """Register the existing text class and its hybrid-state contract.

    vLLM 0.23 and 0.26 ship the class but only register the multimodal wrapper. A
    text-only checkpoint consequently falls through to that wrapper and tries
    to access ``vision_config``. The text class also omits the hybrid marker
    and state helpers that the wrapper supplies. General plugins run in both
    the API process and EngineCore, so this repair survives multiprocessing.
    """
    import torch
    from vllm import ModelRegistry
    from vllm.config.speculative import SpeculativeConfig
    _import_fla_kkt_module()
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

    safe_fla_kernels = prune_fla_configs_if_needed(torch)
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

    apply_hybrid_state(
        (Qwen3_5ForCausalLM, Qwen3_5MoeForCausalLM),
        Qwen3_5ForConditionalGeneration,
    )

    if not getattr(Qwen3_5MoeForCausalLM, "_flexinfer_expert_names", False):
        original_load_weights = Qwen3_5MoeForCausalLM.load_weights

        def load_weights(self, weights):
            return original_load_weights(self, _rename_expert_weights(weights))

        Qwen3_5MoeForCausalLM.load_weights = load_weights
        Qwen3_5MoeForCausalLM._flexinfer_expert_names = True

    expert_loader_hooks = sum(
        1
        for model_class in (Qwen3_5Model, Qwen3_5MultiTokenPredictor)
        if _patch_gptq_expert_loader(model_class)
    )

    ModelRegistry.register_model("Qwen3_5ForCausalLM", Qwen3_5ForCausalLM)
    ModelRegistry.register_model("Qwen3_5MoeForCausalLM", Qwen3_5MoeForCausalLM)
    print(
        "flexinfer: registered text-only Qwen3.5 dense + MoE architectures "
        "with hybrid-state support "
        "and ROCm-safe FLA/MTP GPTQ handling "
        f"(fla_autotuners={safe_fla_kernels}, "
        f"mtp_experts={'gptq' if quantized_mtp_experts else 'plain'}, "
        f"gptq_expert_loader_hooks={expert_loader_hooks})"
    )
