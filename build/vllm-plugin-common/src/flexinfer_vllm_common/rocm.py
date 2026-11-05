"""ROCm-specific workarounds shared by FlexInfer's vLLM plugins."""

import os
import sys

# Consumer ROCm targets whose LLVM codegen stalls on the high-stage FLA configs.
CONSUMER_ROCM_ARCHS = frozenset({"gfx906", "gfx1100"})

# Import the KKT kernel from whichever layout exists so the autotuner pruning
# below sees the modules the Qwen3.5 GDN prefill path actually loads. A
# layout this list does not know is a hard error: silently skipping the
# import would leave the 4-stage autotune configs in place and the engine
# stuck in LLVM codegen on consumer ROCm (the 2026-09-13 S2 canary failed
# before this point with ModuleNotFoundError on the old path).
FLA_OPS_PREFIXES = (
    "vllm.model_executor.layers.fla.ops.",
    "vllm.third_party.flash_linear_attention.ops.",
)
FLA_KKT_MODULE = "chunk_scaled_dot_kkt"

# How deep to unwrap a Triton autotuner's ``.fn`` chain looking for ``configs``.
_MAX_AUTOTUNER_DEPTH = 5

# How far the FLA autotuners are pruned on consumer ROCm:
#   single  — keep one config per autotuner, the first among the lowest
#             (stages, warps). The historical contract; default.
#   relaxed — drop only the configs that stall LLVM codegen (num_stages >= 4)
#             and let Triton's autotune choose warps and tiles among the rest.
#             An autotuner left with no config keeps its ``single`` pick.
# Triton's Config defaults num_stages to 3, so configs that do not set it
# survive ``relaxed``.
FLA_AUTOTUNE_POLICIES = ("single", "relaxed")
FLA_AUTOTUNE_ENV = "FLEXINFER_FLA_AUTOTUNE"
_RELAXED_MAX_STAGES = 3
_TRITON_DEFAULT_STAGES = 3


def fla_autotune_policy(environ=os.environ) -> str:
    """Return the configured FLA pruning policy; an unknown value is fatal."""
    value = environ.get(FLA_AUTOTUNE_ENV, "single")
    if value not in FLA_AUTOTUNE_POLICIES:
        raise RuntimeError(
            f"{FLA_AUTOTUNE_ENV} must be one of {FLA_AUTOTUNE_POLICIES}, got {value!r}"
        )
    return value


def _single_config(configs):
    _, safe = min(
        enumerate(configs),
        key=lambda item: (
            getattr(item[1], "num_stages", 1) or 1,
            getattr(item[1], "num_warps", 1) or 1,
            item[0],
        ),
    )
    return [safe]


def _relaxed_configs(configs):
    kept = [
        config
        for config in configs
        if (getattr(config, "num_stages", None) or _TRITON_DEFAULT_STAGES)
        <= _RELAXED_MAX_STAGES
    ]
    return kept or _single_config(configs)


def import_fla_kkt_module():
    """Import the FLA KKT kernel module from the layout this vLLM ships."""
    import importlib

    errors = []
    for prefix in FLA_OPS_PREFIXES:
        name = prefix + FLA_KKT_MODULE
        try:
            return importlib.import_module(name)
        except ModuleNotFoundError as exc:
            errors.append(f"{name}: {exc}")
    raise RuntimeError(
        "flexinfer_vllm_common: no known flash-linear-attention layout in this "
        "vLLM; extend FLA_OPS_PREFIXES. Tried: " + "; ".join(errors)
    )


def select_rocm_safe_fla_configs(prefixes=FLA_OPS_PREFIXES, policy="single") -> int:
    """Prune loaded FLA autotuners to configs that finish consumer-ROCm codegen.

    The 4-stage variant can remain in LLVM codegen indefinitely on consumer
    ROCm targets. Qwen3.5's GDN prefill path contains several independent
    autotuners, so pruning only KKT still leaves the engine stuck compiling
    ``chunk_delta_h`` and later kernels. ``single`` preserves the first config
    among the lowest stage/warp candidates for every loaded FLA autotuner;
    ``relaxed`` removes only the 4+-stage configs (``FLA_AUTOTUNE_POLICIES``).

    Returns the number of autotuners pruned. Raises if none were found, because
    a silent zero means the kernels moved and the workaround stopped applying.
    """
    if policy not in FLA_AUTOTUNE_POLICIES:
        raise ValueError(f"unknown FLA autotune policy {policy!r}")
    prune = _relaxed_configs if policy == "relaxed" else _single_config
    prefixes = tuple(prefixes)
    pruned: set[int] = set()
    for module_name, module in tuple(sys.modules.items()):
        if not module_name.startswith(prefixes) or module is None:
            continue
        for value in vars(module).values():
            candidate = value
            for _ in range(_MAX_AUTOTUNER_DEPTH):
                configs = getattr(candidate, "configs", None)
                if isinstance(configs, (list, tuple)) and configs:
                    candidate_id = id(candidate)
                    if candidate_id not in pruned:
                        candidate.configs = prune(configs)
                        pruned.add(candidate_id)
                    break
                candidate = getattr(candidate, "fn", None)
                if candidate is None:
                    break
    if not pruned:
        raise RuntimeError("vLLM FLA safe autotune configurations are missing")
    return len(pruned)


def needs_safe_fla_config(torch_module) -> bool:
    """Return whether this process targets a consumer ROCm architecture.

    ``PYTORCH_ROCM_ARCH`` wins when set, so a build can declare its target
    without a live device; otherwise the running device is queried.
    """
    configured = os.environ.get("PYTORCH_ROCM_ARCH", "")
    if configured:
        return configured in CONSUMER_ROCM_ARCHS
    try:
        if torch_module.cuda.is_available():
            arch = torch_module.cuda.get_device_properties(0).gcnArchName
            return str(arch).split(":", 1)[0] in CONSUMER_ROCM_ARCHS
    except (AttributeError, RuntimeError):
        pass
    return False


def prune_fla_configs_if_needed(
    torch_module, prefixes=FLA_OPS_PREFIXES, policy=None
) -> int:
    """Prune FLA autotuners when running HIP on a consumer ROCm target.

    Convenience wrapper: returns 0 on non-HIP or non-consumer targets so a
    plugin's ``register()`` stays a single call. ``policy`` defaults to
    ``FLEXINFER_FLA_AUTOTUNE``, which is validated on every target so a typo
    fails the boot instead of waiting for a GPU that needs it.
    """
    if policy is None:
        policy = fla_autotune_policy()
    version = getattr(torch_module, "version", None)
    if version is None or getattr(version, "hip", None) is None:
        return 0
    if not needs_safe_fla_config(torch_module):
        return 0
    return select_rocm_safe_fla_configs(prefixes, policy)
