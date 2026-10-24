"""Hybrid (mamba / linear-attention) state contract wiring.

Some Qwen3.5-family classes ship without the hybrid marker and state helpers
that the multimodal wrapper supplies, so a text-only or pooling lane has to
apply them. ``vllm.general_plugins`` entry points have no guaranteed ordering,
so every plugin that needs the contract must be able to apply it itself,
idempotently, without assuming a sibling ran first.
"""

MAMBA_STATE_METHODS = (
    "get_mamba_state_shape_from_config",
    "get_mamba_state_dtype_from_config",
    "get_mamba_state_copy_func",
)


def apply_hybrid_state(model_classes, donor, methods=MAMBA_STATE_METHODS) -> int:
    """Mark classes hybrid and copy the state helpers from ``donor``.

    Idempotent and additive: a method the class already resolves — defined on
    it directly or inherited from a base — is left alone, so repeated
    application (or application after a sibling plugin) is a no-op. vLLM 0.32
    ships the helpers on ``Qwen3_5ForCausalLMBase`` (via ``IsHybrid``), so on
    that runtime nothing is copied and the upstream implementation, which reads
    ``hf_text_config``, stays in charge; on 0.23/0.26 the text classes lack
    them and the donor's are copied as before. Returns the number of methods
    actually copied, for logging.
    """
    copied = 0
    for model_class in model_classes:
        model_class.is_hybrid = True
        for method_name in methods:
            if getattr(model_class, method_name, None) is not None:
                continue
            setattr(model_class, method_name, donor.__dict__[method_name])
            copied += 1
    return copied
