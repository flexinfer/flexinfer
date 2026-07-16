"""Complete vLLM 0.23's registration for text-only Qwen3.5 MoE models."""

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
    from vllm.model_executor.models.qwen3_5 import (
        Qwen3_5ForConditionalGeneration,
        Qwen3_5Model,
        Qwen3_5MoeForCausalLM,
    )

    if torch.version.hip is not None:
        _select_gfx1100_safe_fla_config(chunk_scaled_dot_kkt_fwd_kernel)
    _patch_qwen35_text_mtp_config(SpeculativeConfig)

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

    if not getattr(Qwen3_5Model, "_flexinfer_gptq_expert_names", False):
        original_load_fused_expert_weights = Qwen3_5Model.load_fused_expert_weights

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

        Qwen3_5Model.load_fused_expert_weights = load_fused_expert_weights
        Qwen3_5Model._flexinfer_gptq_expert_names = True

    ModelRegistry.register_model(ARCHITECTURE, Qwen3_5MoeForCausalLM)
    print(
        f"flexinfer: registered {ARCHITECTURE} with hybrid-state support "
        "and ROCm-safe FLA autotuning"
    )
