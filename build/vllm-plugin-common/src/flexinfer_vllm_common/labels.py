"""Label-space resolution for composite (multimodal) model configs.

A composite checkpoint declares its label space on the TOP-LEVEL config, while
the nested ``text_config`` block carries no ``id2label``. Sizing a
classification head from ``get_text_config().num_labels`` therefore returns the
``PretrainedConfig`` default and builds a head of the wrong size — which does
not crash, it silently serves the wrong number of classes.
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

    Mirrors upstream vLLM's ``_resolve_num_labels``, which released builds do
    not yet carry.
    """
    if text_config is hf_config:
        return hf_config.num_labels
    if hf_config.num_labels != default_num_labels():
        return hf_config.num_labels
    return text_config.num_labels


def repair_text_config_labels(hf_config) -> int:
    """Resolve the label count and write it onto the nested text config.

    Repairs the INPUT rather than patching vLLM internals, so the fix is
    version-independent and becomes a no-op against a build that already
    resolves labels correctly. Returns the resolved count.
    """
    text_config = hf_config.get_text_config()
    resolved = resolve_num_labels(hf_config, text_config)
    if resolved is None or int(resolved) < 1:
        raise RuntimeError(
            f"could not resolve a label count from the model config (got {resolved!r})"
        )
    resolved = int(resolved)
    if text_config is not hf_config:
        text_config.num_labels = resolved
    return resolved
