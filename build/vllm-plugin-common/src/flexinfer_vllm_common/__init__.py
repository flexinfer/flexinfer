"""Shared helpers for FlexInfer's vLLM general plugins.

Every plugin we ship into a runtime image hits the same problems, so they live
here once instead of being copied per plugin:

* ``rocm`` — pruning vLLM's FLA autotuners to configs that actually finish
  codegen on consumer ROCm targets (``FLEXINFER_FLA_AUTOTUNE``).
* ``segmented`` — tuning ROCm segmented attention at the engine's block-table
  width, opt-in (``FLEXINFER_SEGMENTED_RUNTIME_TABLES``).
* ``labels`` — resolving the label space of a composite (multimodal) config,
  whose ``text_config`` does not carry ``id2label``, for the seqcls plugin's
  head-size assertions.

Every plugin targets vLLM 0.32 or later. The 0.23/0.26 compatibility helpers
(``hybrid``: the hybrid/mamba state contract; ``mrope``: the multimodal M-RoPE
donor; the in-place label repair) were removed in 0.4.0 once no lane pinned
those runtimes: 0.32 ships all three upstream. The frozen gfx906 vLLM 0.20.1
image still needs them; it copies its plugin sources (plugin-common 0.1.0) out
of the image it already runs (build/Dockerfile.vllm-rocm-gfx906-qwen35-mtp).

This package imports neither vLLM nor torch at module scope, so it is safe to
import from a plugin's module body and can be unit-tested in a bare Python
image. Callers pass the modules and classes they already hold.
"""

from .labels import default_num_labels, resolve_num_labels
from .rocm import (
    CONSUMER_ROCM_ARCHS,
    FLA_AUTOTUNE_ENV,
    FLA_AUTOTUNE_POLICIES,
    FLA_OPS_PREFIXES,
    fla_autotune_policy,
    import_fla_kkt_module,
    needs_safe_fla_config,
    prune_fla_configs_if_needed,
    select_rocm_safe_fla_configs,
)
from .segmented import (
    SEGMENTED_RUNTIME_TABLES_ENV,
    install_segmented_runtime_tables,
    install_segmented_runtime_tables_if_enabled,
)

__all__ = [
    "CONSUMER_ROCM_ARCHS",
    "FLA_AUTOTUNE_ENV",
    "FLA_AUTOTUNE_POLICIES",
    "FLA_OPS_PREFIXES",
    "SEGMENTED_RUNTIME_TABLES_ENV",
    "default_num_labels",
    "fla_autotune_policy",
    "import_fla_kkt_module",
    "install_segmented_runtime_tables",
    "install_segmented_runtime_tables_if_enabled",
    "needs_safe_fla_config",
    "prune_fla_configs_if_needed",
    "resolve_num_labels",
    "select_rocm_safe_fla_configs",
]
