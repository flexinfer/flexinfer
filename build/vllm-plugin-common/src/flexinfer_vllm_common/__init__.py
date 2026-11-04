"""Shared helpers for FlexInfer's vLLM general plugins.

Every plugin we ship into a runtime image hits the same problems, so they live
here once instead of being copied per plugin:

* ``rocm`` — pruning vLLM's FLA autotuners to configs that actually finish
  codegen on consumer ROCm targets (``FLEXINFER_FLA_AUTOTUNE``).
* ``segmented`` — tuning ROCm segmented attention at the engine's block-table
  width, opt-in (``FLEXINFER_SEGMENTED_RUNTIME_TABLES``).
* ``labels`` — resolving the label space of a composite (multimodal) config,
  whose ``text_config`` does not carry ``id2label``.
* ``hybrid`` — applying the hybrid/mamba state contract to model classes that
  ship without it.

This package imports neither vLLM nor torch at module scope, so it is safe to
import from a plugin's module body and can be unit-tested in a bare Python
image. Callers pass the modules and classes they already hold.
"""

from .hybrid import MAMBA_STATE_METHODS, apply_hybrid_state
from .labels import default_num_labels, repair_text_config_labels, resolve_num_labels
from .mrope import CONFIG_ATTR as MROPE_CONFIG_ATTR
from .mrope import attach_mrope_support, needs_mrope
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
    "MAMBA_STATE_METHODS",
    "MROPE_CONFIG_ATTR",
    "SEGMENTED_RUNTIME_TABLES_ENV",
    "apply_hybrid_state",
    "attach_mrope_support",
    "needs_mrope",
    "default_num_labels",
    "fla_autotune_policy",
    "import_fla_kkt_module",
    "install_segmented_runtime_tables",
    "install_segmented_runtime_tables_if_enabled",
    "needs_safe_fla_config",
    "prune_fla_configs_if_needed",
    "repair_text_config_labels",
    "resolve_num_labels",
    "select_rocm_safe_fla_configs",
]
