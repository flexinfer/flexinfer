"""Label-space resolution for composite (multimodal) model configs.

A composite checkpoint declares its label space on the TOP-LEVEL config, while
the nested ``text_config`` block carries no ``id2label``. Sizing a
classification head from ``get_text_config().num_labels`` therefore returns the
``PretrainedConfig`` default and builds a head of the wrong size — which does
not crash, it silently serves the wrong number of classes.

vLLM 0.32 sizes the head with its own ``_resolve_num_labels`` (models/adapters.py),
so nothing here mutates the config any more (the 0.23/0.26 in-place repair was
removed in 0.4.0). ``resolve_num_labels`` mirrors the upstream rule so the
seqcls plugin can assert that the head vLLM built, and the checkpoint tensor it
loads, both match the declared label space.
"""

# transformers' historical default when nobody declares a label space.
_FALLBACK_DEFAULT_NUM_LABELS = 2


def default_num_labels() -> int:
    """The ``PretrainedConfig`` default that signals 'nobody declared labels'."""
    try:
        from transformers import PretrainedConfig

        return PretrainedConfig().num_labels
    except Exception:
        # transformers is always present in a vLLM image; the fallback exists so
        # these helpers stay unit-testable in a bare Python image.
        return _FALLBACK_DEFAULT_NUM_LABELS


def resolve_num_labels(hf_config, text_config) -> int:
    """Prefer the top-level label space whenever it declares one.

    Mirrors upstream vLLM's ``_resolve_num_labels`` (0.32 and later).
    """
    if text_config is hf_config:
        return hf_config.num_labels
    if hf_config.num_labels != default_num_labels():
        return hf_config.num_labels
    return text_config.num_labels
