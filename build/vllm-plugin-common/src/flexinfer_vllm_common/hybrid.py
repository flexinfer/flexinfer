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

    Idempotent: a method already defined directly on a class is left alone, so
    repeated application (or application after a sibling plugin) is a no-op.
    Returns the number of methods actually copied, for logging.
    """
    copied = 0
    for model_class in model_classes:
        model_class.is_hybrid = True
        for method_name in methods:
            if method_name in model_class.__dict__:
                continue
            setattr(model_class, method_name, donor.__dict__[method_name])
            copied += 1
    return copied
