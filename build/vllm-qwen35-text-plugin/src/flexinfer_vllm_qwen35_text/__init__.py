"""Fill the gaps vLLM still has for text-only Qwen3.5 models on consumer ROCm.

Targets vLLM 0.32 or later, which registers ``Qwen3_5ForCausalLM`` /
``Qwen3_5MoeForCausalLM`` itself with ``IsHybrid``, ``SupportsMRoPE`` and the
mamba state helpers on ``Qwen3_5ForCausalLMBase``. The plugin keeps only what
the runtime still lacks: the consumer-ROCm FLA autotuner pruning
(``FLEXINFER_FLA_AUTOTUNE``), the text-only MTP config contract, the MTP head
quantization policy (``FLEXINFER_QWEN35_MTP_HEAD``) and the MoE expert-name
normalization. It also installs three opt-in, model-agnostic patches, since
this is the plugin every gfx1100 serving image loads: the segmented attention
autotune at the runtime block-table width
(``FLEXINFER_SEGMENTED_RUNTIME_TABLES``), the input-embedding table in mapped
host memory (``FLEXINFER_EMBED_HOST``, 0.12.0) and fp8 KV on segmented
attention for gfx11 (``FLEXINFER_SEGMENTED_FP8_GFX11``, 0.12.0).

0.11.0 removed the vLLM 0.23/0.26 compatibility code: the text-class
registration, the hybrid-state copy and the GPTQ fused-expert loader hook
(``load_fused_expert_weights`` no longer exists from 0.26). ``register()`` now
refuses to run on a vLLM whose registry lacks the text classes, instead of
silently registering them on a runtime nothing qualifies any more. The frozen
gfx906 vLLM 0.20.1 image keeps the 0.8.0 plugin it was certified with.

ROCm autotune pruning and those three patches live in
``flexinfer_vllm_common``, shared with every other FlexInfer vLLM plugin so the
copies cannot drift. Install ``build/vllm-plugin-common`` before this package.
"""

import os

from flexinfer_vllm_common.rocm import (
    FLA_KKT_MODULE as _FLA_KKT_MODULE,
    FLA_OPS_PREFIXES as _FLA_OPS_PREFIXES,
    fla_autotune_policy,
    import_fla_kkt_module as _import_fla_kkt_module,
    needs_safe_fla_config as _needs_safe_fla_config,
    prune_fla_configs_if_needed,
    select_rocm_safe_fla_configs as _select_rocm_safe_fla_configs,
)
from flexinfer_vllm_common.embed_host import install_embed_host_if_enabled
from flexinfer_vllm_common.segmented import (
    install_segmented_runtime_tables_if_enabled,
)
from flexinfer_vllm_common.segmented_fp8 import install_segmented_fp8_if_enabled

ARCHITECTURES = {
    "Qwen3_5ForCausalLM": "dense",
    "Qwen3_5MoeForCausalLM": "moe",
}
_TEXT_ONLY_ROPE_PARAMETERS = {
    "rope_type": "default",
    "rope_theta": 10_000_000,
    "partial_rotary_factor": 0.25,
}


# Maps fused-expert names ``.moe.gate_up_proj`` / ``.moe.down_proj`` onto the
# ``.mlp.experts.*`` names vLLM's Qwen3.5 MoE loader expects.
def _rename_expert_weights(weights):
    for name, weight in weights:
        name = name.replace(".moe.gate_up_proj", ".mlp.experts.gate_up_proj").replace(
            ".moe.down_proj", ".mlp.experts.down_proj"
        )
        yield name, weight


def _strict_env_flag(name: str) -> bool:
    value = os.environ.get(name, "0")
    if value not in {"0", "1"}:
        raise RuntimeError(f"{name} must be 0 or 1, got {value!r}")
    return value == "1"


# How the MTP head's linear layers are quantized when the target is GPTQ:
#   plain      — the historical contract: every ``mtp.*`` linear layer is
#                served unquantized (the Qwen MTP artifacts kept the head in
#                floating point). Default, unchanged behaviour.
#   checkpoint — leave the decision to vLLM's AutoGPTQConfig, which reads
#                ``modules_in_block_to_quantize`` (or, from 0.32, derives the
#                quantized-module census from the safetensors dtypes). Needed
#                for artifacts whose MTP block IS INT4, such as
#                biMEMO/Qwen3.8-27B-int4-AutoRound: forcing those layers to the
#                unquantized method loads garbage drafts without an error.
MTP_HEAD_POLICIES = ("plain", "checkpoint")
MTP_HEAD_POLICY_ENV = "FLEXINFER_QWEN35_MTP_HEAD"


def _mtp_head_policy() -> str:
    value = os.environ.get(MTP_HEAD_POLICY_ENV, "plain")
    if value not in MTP_HEAD_POLICIES:
        raise RuntimeError(
            f"{MTP_HEAD_POLICY_ENV} must be one of {MTP_HEAD_POLICIES}, got {value!r}"
        )
    return value


def _upstream_registers(registry, architecture: str) -> bool:
    """Whether this vLLM registers ``architecture`` itself.

    vLLM 0.32 lists the Qwen3.5 text classes in its own registry. A registry
    without ``get_supported_archs`` reports nothing, which ``register()``
    treats as a runtime it does not support.
    """
    getter = getattr(registry, "get_supported_archs", None)
    if not callable(getter):
        return False
    return architecture in set(getter())


def _patch_qwen35_text_mtp_config(speculative_config) -> None:
    """Teach vLLM's MTP override the text-only model type and its rope contract.

    vLLM 0.23 does not map ``qwen3_5_text`` / ``qwen3_5_moe_text`` at all; 0.32
    maps them but keeps the artifact's MRoPE ``rope_parameters``. The wrapper
    takes whatever upstream's signature is: 0.33 added
    ``transformers_mtp_overrides`` and calls the override positionally and
    through ``functools.partial``. A call that carries a value in any of those
    extra arguments belongs to upstream (the Transformers modeling backend
    builds its draft from them and wants none of the rewrites), so only calls
    whose extras are all ``None`` get the text-only rewrite.
    """
    if getattr(speculative_config, "_flexinfer_qwen35_text_mtp", False):
        return

    original_hf_config_override = speculative_config.hf_config_override

    def hf_config_override(hf_config, *args, **kwargs):
        model_type = getattr(hf_config, "model_type", None)
        extras_unset = all(value is None for value in (*args, *kwargs.values()))
        if extras_unset and model_type in {"qwen3_5_text", "qwen3_5_moe_text"}:
            n_predict = getattr(hf_config, "mtp_num_hidden_layers", None)
            hf_config.model_type = "qwen3_5_mtp"
            hf_config.update(
                {
                    "n_predict": n_predict,
                    "architectures": [
                        (
                            "Qwen3_5MoeMTP"
                            if model_type == "qwen3_5_moe_text"
                            else "Qwen3_5MTP"
                        )
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
        return original_hf_config_override(hf_config, *args, **kwargs)

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


def register() -> None:
    """Install the ROCm, MTP and MoE patches on a vLLM that registers Qwen3.5.

    General plugins run in both the API process and EngineCore, so the patches
    survive multiprocessing. Raises before patching anything when the runtime
    does not register the text classes itself (vLLM older than 0.32): there a
    text-only checkpoint would fall through to the multimodal wrapper, and the
    compatibility code that used to cover that was removed in 0.11.0.
    """
    import torch
    from vllm import ModelRegistry
    from vllm.config.speculative import SpeculativeConfig

    _import_fla_kkt_module()
    from vllm.model_executor.layers.fused_moe import (
        RoutedExperts,
        UnquantizedFusedMoEMethod,
    )
    from vllm.model_executor.layers.linear import (
        LinearBase,
        UnquantizedLinearMethod,
    )
    from vllm.model_executor.layers.quantization.auto_gptq import AutoGPTQConfig
    from vllm.model_executor.models.qwen3_5 import Qwen3_5MoeForCausalLM

    missing = [
        architecture
        for architecture in ARCHITECTURES
        if not _upstream_registers(ModelRegistry, architecture)
    ]
    if missing:
        raise RuntimeError(
            "flexinfer_qwen35_text needs a vLLM that registers the Qwen3.5 text "
            f"classes itself (0.32 or later); this one lacks {missing}. The "
            "0.23/0.26 registration fallback was removed in plugin 0.11.0."
        )

    fla_policy = fla_autotune_policy()
    safe_fla_kernels = prune_fla_configs_if_needed(torch, policy=fla_policy)
    segmented_runtime_tables = install_segmented_runtime_tables_if_enabled()
    embed_host = install_embed_host_if_enabled()
    segmented_fp8 = install_segmented_fp8_if_enabled()
    _patch_qwen35_text_mtp_config(SpeculativeConfig)
    mtp_head_policy = _mtp_head_policy()
    quantized_mtp_experts = _strict_env_flag("FLEXINFER_QWEN35_MTP_EXPERTS_GPTQ")
    if mtp_head_policy == "plain":
        _patch_qwen35_mtp_gptq(
            AutoGPTQConfig,
            LinearBase,
            RoutedExperts,
            UnquantizedLinearMethod,
            UnquantizedFusedMoEMethod,
            quantized_mtp_experts=quantized_mtp_experts,
        )
    elif quantized_mtp_experts:
        raise RuntimeError(
            "FLEXINFER_QWEN35_MTP_EXPERTS_GPTQ only applies to "
            f"{MTP_HEAD_POLICY_ENV}=plain; with 'checkpoint' AutoGPTQConfig "
            "decides every MTP layer from the checkpoint"
        )

    if not getattr(Qwen3_5MoeForCausalLM, "_flexinfer_expert_names", False):
        original_load_weights = Qwen3_5MoeForCausalLM.load_weights

        def load_weights(self, weights):
            return original_load_weights(self, _rename_expert_weights(weights))

        Qwen3_5MoeForCausalLM.load_weights = load_weights
        Qwen3_5MoeForCausalLM._flexinfer_expert_names = True

    print(
        "flexinfer: patched upstream text-only Qwen3.5 dense + MoE architectures "
        "with ROCm-safe FLA/MTP GPTQ handling "
        f"(archs={','.join(ARCHITECTURES)}, "
        f"fla_autotuners={safe_fla_kernels}, "
        f"fla_autotune={fla_policy}, "
        f"segmented_runtime_tables={'on' if segmented_runtime_tables else 'off'}, "
        f"embed_host={'on' if embed_host else 'off'}, "
        f"segmented_fp8_gfx11={'on' if segmented_fp8 else 'off'}, "
        f"mtp_head={mtp_head_policy}, "
        f"mtp_experts={'gptq' if quantized_mtp_experts else 'plain'})"
    )
