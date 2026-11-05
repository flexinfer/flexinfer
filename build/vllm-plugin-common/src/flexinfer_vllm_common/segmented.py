"""Tune ROCm segmented attention at the engine's block-table width (opt-in).

vLLM's ``ROCM_SEGMENTED_ATTN`` stage kernel takes every stride as a
``tl.constexpr``, the block-table row stride included. Its startup autotuner
(``vllm.model_executor.warmup.rocm_segmented_attn_autotune_warmup``) builds a
synthetic table ``ceil(seq_len / page)`` columns wide for each workload, while
the engine launches with the model runner's table, ``ceil(max_model_len /
block)`` columns rounded up to vLLM's 128-token alignment. No tuned variant
matches a live launch, so every (query bucket, config) compiles on its first
request, 0.5-4.7 s each on gfx1100 (gap G7). Upstream also unloads every kernel
it compiled while tuning, and a boot that finds its tuning table on disk
compiles nothing, so a right-width tuning pass alone would still leave each
first request a Triton cache load.

``FLEXINFER_SEGMENTED_RUNTIME_TABLES=1`` patches the warmup module so that:

1. ``rocm_segmented_attn_autotune_warmup`` first records, per attention layer,
   the row width of the block table the model runner launches it with;
2. ``_make_inputs`` builds its tables at that width, so tuning measures, and
   writes to Triton's disk cache, the variants live traffic launches;
3. after each ``warmup_segmented_attention`` pass, every record's tuned winner
   is compiled at that width outside the tuner's unload scope, so it is
   resident when the first request needs it.

Unset or ``0`` leaves vLLM untouched. A runner whose block tables cannot be
found keeps upstream's tables and logs why. Like the rest of this package, the
module imports neither vLLM nor torch at module scope.
"""

import gc
import importlib
import inspect
import os
import time

SEGMENTED_RUNTIME_TABLES_ENV = "FLEXINFER_SEGMENTED_RUNTIME_TABLES"
SEGMENTED_WARMUP_MODULE = (
    "vllm.model_executor.warmup.rocm_segmented_attn_autotune_warmup"
)
# Everything the patch replaces or calls. A vLLM that renamed any of them must
# fail the boot that asked for the patch, not run without it.
SEGMENTED_WARMUP_API = (
    "_TABLES",
    "_key",
    "_make_inputs",
    "_warmup_segmented_attention",
    "compile_segmented_attention",
    "rocm_segmented_attn_autotune_warmup",
    "segmented_query_capacity",
    "segmented_workspace_shapes",
    "torch",
    "warmup_segmented_attention",
)
_WARMUP_PARAMETERS = (
    "device",
    "dtype",
    "heads",
    "kv_heads",
    "dim",
    "page",
    "scale",
    "max_tokens",
    "kv_dtype",
    "sliding_window",
    "causal",
    "has_sinks",
    "physical_max_len",
)
PATCH_MARKER = "_flexinfer_runtime_tables"


class _State:
    def __init__(self) -> None:
        self.layer_widths: dict[int, int] = {}
        self.width: int | None = None
        self.compiled: set = set()
        self.warned = False


_state = _State()


def segmented_runtime_tables_enabled(environ=os.environ) -> bool:
    value = environ.get(SEGMENTED_RUNTIME_TABLES_ENV, "0")
    if value not in {"0", "1"}:
        raise RuntimeError(
            f"{SEGMENTED_RUNTIME_TABLES_ENV} must be 0 or 1, got {value!r}"
        )
    return value == "1"


def _group_widths(model_runner):
    """Row stride of each KV-cache group's forward block table, or None.

    Model Runner V2 (``vllm/v1/worker/gpu``) keeps one table per group in
    ``block_tables.input_block_tables``; the v1 runner keeps them in
    ``input_batch.block_table.block_tables``.
    """
    tables = getattr(model_runner, "block_tables", None)
    if hasattr(tables, "input_block_tables"):
        return [table.stride(0) for table in tables.input_block_tables]
    multi = getattr(getattr(model_runner, "input_batch", None), "block_table", None)
    if hasattr(multi, "block_tables"):
        return [table.block_table.gpu.stride(0) for table in multi.block_tables]
    return None


def runtime_layer_widths(worker) -> dict[int, int]:
    """Map ``id(layer)`` to the block-table width it is launched with.

    Hybrid models put attention and mamba state in separate KV-cache groups
    whose tables can share a block size but not a width, so the width is
    resolved through the layer's group rather than its page size. Returns an
    empty mapping when the runner's layout is not one this module knows.
    """
    runner = worker.model_runner
    groups = runner.kv_cache_config.kv_cache_groups
    widths = _group_widths(runner)
    if widths is None or len(widths) != len(groups):
        return {}
    group_of = {
        name: index for index, group in enumerate(groups) for name in group.layer_names
    }
    context = worker.vllm_config.compilation_config.static_forward_context
    return {
        id(layer): widths[group_of[name]]
        for name, layer in context.items()
        if name in group_of
    }


def _widen(table, width):
    rows, columns = table.shape
    if columns >= width:
        return table
    wide = table.new_zeros((rows, width))
    wide[:, :columns] = table
    return wide


def _compile_winners(warmup, arguments, width) -> None:
    """Compile each tuned record's winner at ``width``; never raises on a record."""
    a = arguments
    key = warmup._key(
        a["device"],
        a["dtype"],
        a["kv_dtype"],
        a["heads"],
        a["kv_heads"],
        a["dim"],
        a["page"],
        a["scale"],
        a["sliding_window"],
        a["causal"],
        a["has_sinks"],
    )
    if (key, width) in _state.compiled:
        return
    _state.compiled.add((key, width))
    records = warmup._TABLES.get(key, {}).get("records", ())
    if not records:
        return
    torch = warmup.torch
    # Upstream tunes sink + sliding-window layers against the full physical
    # length and every other layer against the workload's own.
    physical = (
        a["physical_max_len"] if a["has_sinks"] and a["sliding_window"] >= 0 else None
    )
    start = time.monotonic()
    compiled = skipped = 0
    with torch.inference_mode():
        for record in records:
            workload = tuple(record["workload"])
            batch, query_len, _ = workload
            config = dict(record["best"])
            try:
                q, _, _, kc, vc, table, starts, lengths, kv_scale = warmup._make_inputs(
                    a["device"],
                    a["dtype"],
                    a["kv_dtype"],
                    a["heads"],
                    a["kv_heads"],
                    a["dim"],
                    a["page"],
                    a["max_tokens"],
                    workload,
                    physical,
                )
                shapes = warmup.segmented_workspace_shapes(
                    batch,
                    warmup.segmented_query_capacity(query_len),
                    a["heads"],
                    a["kv_heads"],
                    a["dim"],
                    config["splits"],
                )
                workspace = (
                    None
                    if shapes is None
                    else tuple(
                        torch.empty(shape, dtype=torch.float32, device=a["device"])
                        for shape in shapes
                    )
                )
                sinks = (
                    torch.linspace(
                        -1, 1, a["heads"], dtype=torch.float32, device=a["device"]
                    )
                    if a["has_sinks"]
                    else None
                )
                warmup.compile_segmented_attention(
                    q,
                    torch.empty_like(q),
                    kc,
                    vc,
                    table,
                    starts,
                    lengths,
                    query_len,
                    kv_scale,
                    kv_scale,
                    a["scale"],
                    config,
                    workspace,
                    sliding_window=a["sliding_window"],
                    causal=a["causal"],
                    sinks=sinks,
                )
            # A winner that fails here only costs its first live request the
            # compile it pays today, so no failure is worth stopping the boot.
            except Exception as error:  # noqa: BLE001
                skipped += 1
                print(
                    "flexinfer: segmented runtime tables: skipped winner for "
                    f"workload {workload}: {error}",
                    flush=True,
                )
            else:
                compiled += 1
            finally:
                gc.collect()
                torch.accelerator.empty_cache()
    print(
        "flexinfer: segmented runtime tables: block-table width "
        f"{width} (page {a['page']}), compiled {compiled} tuned winners "
        f"({skipped} skipped) in {time.monotonic() - start:.1f}s",
        flush=True,
    )


def install_segmented_runtime_tables(warmup) -> None:
    """Patch vLLM's segmented warmup module in place; a second call is a no-op."""
    missing = [name for name in SEGMENTED_WARMUP_API if not hasattr(warmup, name)]
    if missing:
        raise RuntimeError(
            f"flexinfer_vllm_common.segmented: {warmup.__name__} lacks {missing}; "
            "this vLLM's segmented warmup changed shape"
        )
    if getattr(warmup, PATCH_MARKER, False):
        return
    tune = warmup.warmup_segmented_attention
    signature = inspect.signature(tune)
    absent = [name for name in _WARMUP_PARAMETERS if name not in signature.parameters]
    if absent:
        raise RuntimeError(
            "flexinfer_vllm_common.segmented: warmup_segmented_attention lacks "
            f"parameters {absent}; this vLLM's segmented warmup changed shape"
        )
    autotune = warmup.rocm_segmented_attn_autotune_warmup
    warmup_layer = warmup._warmup_segmented_attention
    make_inputs = warmup._make_inputs

    def rocm_segmented_attn_autotune_warmup(worker):
        _state.layer_widths = runtime_layer_widths(worker)
        try:
            return autotune(worker)
        finally:
            _state.layer_widths = {}

    def _warmup_segmented_attention(layer, device, **limits):
        _state.width = _state.layer_widths.get(id(layer))
        if _state.width is None and not _state.warned:
            _state.warned = True
            print(
                "flexinfer: segmented runtime tables: no runtime block table "
                "found for this layer; tuning at upstream's synthetic width",
                flush=True,
            )
        try:
            return warmup_layer(layer, device, **limits)
        finally:
            _state.width = None

    def _make_inputs(*args, **kwargs):
        inputs = make_inputs(*args, **kwargs)
        if _state.width is None:
            return inputs
        inputs = list(inputs)
        inputs[5] = _widen(inputs[5], _state.width)
        return tuple(inputs)

    def warmup_segmented_attention(*args, **kwargs):
        result = tune(*args, **kwargs)
        if _state.width is not None:
            bound = signature.bind(*args, **kwargs)
            bound.apply_defaults()
            _compile_winners(warmup, bound.arguments, _state.width)
        return result

    warmup.rocm_segmented_attn_autotune_warmup = rocm_segmented_attn_autotune_warmup
    warmup._warmup_segmented_attention = _warmup_segmented_attention
    warmup._make_inputs = _make_inputs
    warmup.warmup_segmented_attention = warmup_segmented_attention
    setattr(warmup, PATCH_MARKER, True)


def install_segmented_runtime_tables_if_enabled(environ=os.environ) -> bool:
    """Install the patch when the flag is set; return whether it is installed."""
    if not segmented_runtime_tables_enabled(environ):
        return False
    try:
        warmup = importlib.import_module(SEGMENTED_WARMUP_MODULE)
    except ModuleNotFoundError as exc:
        raise RuntimeError(
            f"{SEGMENTED_RUNTIME_TABLES_ENV}=1 but this vLLM has no "
            f"{SEGMENTED_WARMUP_MODULE}: {exc}"
        ) from exc
    install_segmented_runtime_tables(warmup)
    return True
