"""Serve Qwen3.5 cross-encoder checkpoints as vLLM pooling models.

Sibling of ``flexinfer-vllm-qwen35-text``. That plugin completes vLLM's
registration for text-only *generative* Qwen3.5; this one registers the
*sequence-classification* heads so a cross-encoder such as
``AlexWortega/openjev`` is served by the same engine, through vLLM's pooling
routers (``/v1/rerank``, ``/v1/score``, ``/classify``, ``/pooling``), with no
new backend and no Go change.

Registering a class already wrapped by ``as_seq_cls_model`` makes it report
``is_pooling_model``, and vLLM consults the registry before falling back to the
architecture-name suffix — so ``--runner auto`` selects the pooling runner with
no extra CLI flag.

Two model-specific problems, both handled here:

1. **The label count is read from the wrong config.** vLLM sizes the score head
   from ``get_text_config().num_labels``. A composite Qwen3.5 config builds
   ``text_config`` from the checkpoint's ``text_config`` block, which carries no
   ``id2label``, so that yields the ``PretrainedConfig`` default of 2 while the
   checkpoint's ``score.weight`` is ``[3, hidden]``. A wrong head does not
   crash: it silently serves a two-class distribution while ``id2label`` still
   advertises three. ``flexinfer_vllm_common.labels`` repairs the input and both
   the built head and the incoming tensor are asserted.

2. **The checkpoint is not extracted.** Our existing Qwen3.5 lanes serve a
   flattened text artifact; a cross-encoder served straight from its repo keeps
   ``model.language_model.*``, an unused ``model.visual.*`` tower and a
   top-level ``score.weight``. The language prefix is remapped and the vision
   tower skipped, which also saves its VRAM.

ROCm autotune pruning and the hybrid-state contract come from
``flexinfer_vllm_common``, shared with every other FlexInfer vLLM plugin.
"""

from flexinfer_vllm_common import (
    apply_hybrid_state,
    repair_text_config_labels,
    resolve_num_labels,  # noqa: F401  (re-exported for tests and sibling plugins)
)
from flexinfer_vllm_common.rocm import (
    import_fla_kkt_module,
    prune_fla_configs_if_needed,
)

# Registered names -> the generative base class whose weights they reuse.
ARCHITECTURES = {
    "Qwen3_5ForSequenceClassification": "dense",
    "Qwen3_5MoeForSequenceClassification": "moe",
}

# Prefix of the language tower inside an un-extracted composite checkpoint.
LANGUAGE_PREFIX = "model.language_model."
# Prefixes a text-only pooling lane must not load.
SKIP_PREFIXES = ("mtp.", "model.visual.")
# The classification head lives at the TOP level and must not be routed into
# the language submodule by the prefix remap.
SCORE_WEIGHT = "score.weight"


def _tap_score_rows(weights):
    """Pass weights through, capturing the row count of ``score.weight``.

    Returns ``(generator, box)``; ``box[0]`` holds the observed row count once
    the tensor has passed through.
    """
    box: list = [None]

    def _tap():
        for name, weight in weights:
            if name == SCORE_WEIGHT:
                shape = getattr(weight, "shape", None)
                if shape is not None and len(shape) >= 1:
                    box[0] = int(shape[0])
            yield name, weight

    return _tap(), box


def _built_head_rows(model):
    """Row count of the constructed score head, or None if it cannot be read."""
    score = getattr(model, "score", None)
    if score is None:
        return None
    for attr in ("output_size", "out_features"):
        value = getattr(score, attr, None)
        if isinstance(value, int):
            return value
    weight = getattr(score, "weight", None)
    shape = getattr(weight, "shape", None)
    if shape is not None and len(shape) >= 1:
        return int(shape[0])
    return None


def build_seq_cls_base(base_class, weights_mapper_cls, auto_weights_loader_cls):
    """Adapt a generative Qwen3.5 class to load an un-extracted cross-encoder.

    Remaps the language tower onto the plain ``model.`` prefix that released
    builds expect, and skips the unused vision tower and MTP head.
    """

    class _SeqClsBase(base_class):
        hf_to_vllm_mapper = weights_mapper_cls(
            orig_to_new_prefix={LANGUAGE_PREFIX: "model."},
        )

        def load_weights(self, weights):
            loader = auto_weights_loader_cls(self, skip_prefixes=list(SKIP_PREFIXES))
            return loader.load_weights(weights, mapper=self.hf_to_vllm_mapper)

    _SeqClsBase.__name__ = f"{base_class.__name__}SeqClsBase"
    _SeqClsBase.__qualname__ = _SeqClsBase.__name__
    return _SeqClsBase


def with_label_repair(wrapped_class):
    """Resolve the label count before construction; assert the head after.

    The assertions turn the silent-wrong-head failure mode into a loud one, both
    for the head vLLM builds and for the tensor the checkpoint supplies.
    """

    class _LabelRepaired(wrapped_class):
        def __init__(self, *, vllm_config, prefix: str = "", **kwargs):
            resolved = repair_text_config_labels(vllm_config.model_config.hf_config)
            self._flexinfer_resolved_num_labels = resolved
            super().__init__(vllm_config=vllm_config, prefix=prefix, **kwargs)

            built = _built_head_rows(self)
            if built is not None and built != resolved:
                raise RuntimeError(
                    "flexinfer qwen3.5 seqcls: score head has "
                    f"{built} rows but the config declares {resolved}; "
                    "refusing to serve a mis-sized classification head"
                )

        def load_weights(self, weights):
            tapped, box = _tap_score_rows(weights)
            result = super().load_weights(tapped)
            observed = box[0]
            expected = getattr(self, "_flexinfer_resolved_num_labels", None)
            if observed is not None and expected is not None and observed != expected:
                raise RuntimeError(
                    "flexinfer qwen3.5 seqcls: checkpoint "
                    f"{SCORE_WEIGHT} has {observed} rows but the head was built "
                    f"for {expected}; the label space was resolved incorrectly"
                )
            return result

    _LabelRepaired.__name__ = f"{wrapped_class.__name__}LabelRepaired"
    _LabelRepaired.__qualname__ = _LabelRepaired.__name__
    return _LabelRepaired


def register() -> None:
    """Register Qwen3.5 sequence-classification architectures as pooling models.

    General plugins run in both the API process and EngineCore, so this survives
    multiprocessing.
    """
    import torch
    from vllm import ModelRegistry
    from vllm.model_executor.models.adapters import as_seq_cls_model
    from vllm.model_executor.models.qwen3_5 import (
        Qwen3_5ForCausalLM,
        Qwen3_5ForConditionalGeneration,
        Qwen3_5MoeForCausalLM,
    )
    from vllm.model_executor.models.utils import AutoWeightsLoader, WeightsMapper

    # Load the KKT kernel from whichever layout this vLLM ships so the
    # autotuners are in sys.modules before pruning.
    import_fla_kkt_module()
    safe_fla_kernels = prune_fla_configs_if_needed(torch)

    # Entry-point ordering is not guaranteed, so apply the hybrid contract here
    # too rather than assuming the sibling text plugin ran first. Idempotent.
    copied = apply_hybrid_state(
        (Qwen3_5ForCausalLM, Qwen3_5MoeForCausalLM),
        Qwen3_5ForConditionalGeneration,
    )

    registered = []
    for name, variant in ARCHITECTURES.items():
        base = Qwen3_5ForCausalLM if variant == "dense" else Qwen3_5MoeForCausalLM
        seq_cls = as_seq_cls_model(
            build_seq_cls_base(base, WeightsMapper, AutoWeightsLoader)
        )
        ModelRegistry.register_model(name, with_label_repair(seq_cls))
        registered.append(name)

    print(
        "flexinfer: registered Qwen3.5 sequence-classification architectures "
        "as vLLM pooling models with composite-config label repair "
        f"(archs={','.join(registered)}, "
        f"language_prefix={LANGUAGE_PREFIX}, "
        f"skipped={','.join(SKIP_PREFIXES)}, "
        f"hybrid_methods_copied={copied}, "
        f"fla_autotuners={safe_fla_kernels})"
    )
