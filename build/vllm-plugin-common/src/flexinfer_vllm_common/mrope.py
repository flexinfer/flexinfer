"""M-RoPE support for text-only wrappers over a composite (multimodal) config.

A Qwen3.5 cross-encoder keeps the full composite config even when only its
language tower is served. vLLM decides whether to build M-RoPE positions from
the CONFIG, not from the weights:

    _uses_mrope(config) -> "mrope_section" in config.rope_parameters

openjev's text_config carries ``mrope_section: [11, 11, 10]``, so
``ModelConfig.uses_mrope`` is True and the runner calls
``_init_mrope_positions``, which asserts ``supports_mrope(model)``. A wrapper
built on ``Qwen3_5ForCausalLM`` fails that assert — the M-RoPE implementation
lives on ``Qwen3_5ForConditionalGeneration``, the multimodal class — and the
engine dies on the FIRST request, after loading cleanly:

    AssertionError: M-RoPE support is not implemented.

Two things make delegating to the donor the right fix rather than stripping
mrope out of the config:

1. It preserves the model's real positional encoding, so logits stay
   comparable with the HF reference by construction.
2. For a text-only request it is a no-op anyway. With no multimodal items the
   donor's loop never runs and the tail returns
   ``np.broadcast_to(np.arange(n), (3, n))`` with ``mrope_position_delta = 0``
   — all three sections get identical sequential positions, which is exactly
   standard RoPE. Stripping the config would reach the same numbers by a
   route that silently diverges the moment anything is not text.

The donor's ``_get_mrope_input_positions`` is a staticmethod taking the FULL
composite config: it reads ``video_token_id``, ``vision_start_token_id``,
``vision_end_token_id`` and ``vision_config.spatial_merge_size``. Those are
evaluated as call arguments, so they are touched even when there are zero
multimodal items — passing the text config (which is what a causal class
stores in ``self.config``) raises AttributeError. Hence CONFIG_ATTR: the
wrapper stashes the composite config at construction and we read it back here.
"""

# Instance attribute holding the FULL composite hf_config. The wrapper sets it
# in __init__; self.config on a causal class is the TEXT config and is missing
# the vision fields the donor reads.
CONFIG_ATTR = "_flexinfer_mrope_config"

# The donor's staticmethod. Kept as a name so a vLLM rename fails loudly in
# attach_mrope_support rather than at the first request.
DONOR_METHOD = "_get_mrope_input_positions"


def attach_mrope_support(wrapped_class, donor):
    """Give ``wrapped_class`` the composite model's M-RoPE implementation.

    Mutates and returns ``wrapped_class``. Raises if the donor does not expose
    the expected staticmethod, because the alternative is an engine that loads
    happily and then dies on its first request.
    """
    impl = getattr(donor, DONOR_METHOD, None)
    if impl is None:
        raise RuntimeError(
            f"flexinfer_vllm_common.mrope: {donor.__name__} has no {DONOR_METHOD}; "
            "this vLLM moved the M-RoPE implementation and the wrapper would "
            "fail the runner's supports_mrope assert on the first request"
        )

    def get_mrope_input_positions(self, input_tokens, mm_features):
        config = getattr(self, CONFIG_ATTR, None)
        if config is None:
            raise RuntimeError(
                "flexinfer_vllm_common.mrope: "
                f"{type(self).__name__} did not set {CONFIG_ATTR}; the donor "
                "needs the composite config, not self.config (the text config "
                "has no vision_config/video_token_id)"
            )
        return impl(
            input_tokens=input_tokens,
            mm_features=mm_features,
            config=config,
        )

    wrapped_class.supports_mrope = True
    wrapped_class.get_mrope_input_positions = get_mrope_input_positions
    return wrapped_class


def needs_mrope(hf_config) -> bool:
    """Whether this config would send the runner down the M-RoPE path.

    Mirrors vLLM's own check across the composite config and its text config,
    without importing vLLM.
    """
    for cfg in (hf_config, getattr(hf_config, "text_config", None)):
        if cfg is None:
            continue
        params = getattr(cfg, "rope_parameters", None)
        if isinstance(params, dict) and "mrope_section" in params:
            return True
    return False
