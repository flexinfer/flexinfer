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

Step 3 compiles against the layer's own bound KV cache (the K/V views and
scales its backend launches with), not the tuner's synthetic one. Triton keys
a kernel on each pointer's specialization as well as its constexprs, and on
the AMD backend that includes whether the tensor's storage fits in 2 GiB
(``tt.pointer_range = 32``, the ``S`` in ``'DS'``). The synthetic cache always
does; a live 32K cache (3.2 GiB on the workhorse) does not, so winners
compiled on the synthetic cache were keyed ``'DS'`` while traffic launched
``'D'`` and none was reused (S4, 2026-11-05: 16 ``jit_monitor`` lines against
15 without the flag). Plugin-common 0.6.0 fixed that. A layer with no bound
cache falls back to the synthetic one and says so on the boot line.

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
# Triton's AMD backend adds ``tt.pointer_range = 32`` (the ``S`` in a ``'DS'``
# key) to a tensor argument whose storage is at most this many bytes.
POINTER_RANGE_32_MAX_BYTES = 2**31 - 1


class _State:
    def __init__(self) -> None:
        self.layer_widths: dict[int, int] = {}
        self.width: int | None = None
        self.layer = None
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


def live_kv_cache(layer, kv_dtype):
    """The K/V cache views and scales ``layer``'s backend launches with, or None.

    Mirrors ``RocmSegmentedAttentionImpl.forward``: the bound ``layer.kv_cache``
    split by the impl's own ``_split_kv_cache`` and, for a quantized cache,
    viewed as the kernel's dtype. Returns ``(kc, vc, k_scale, v_scale)``;
    ``None`` when the layer has no bound cache or the views cannot be made to
    match ``kv_dtype``, so the caller keeps the synthetic cache.
    """
    cache = getattr(layer, "kv_cache", None)
    if isinstance(cache, (list, tuple)):
        cache = cache[0] if cache else None
    split = getattr(getattr(layer, "impl", None), "_split_kv_cache", None)
    if cache is None or not callable(split) or cache.numel() == 0:
        return None
    try:
        kc, vc = split(cache)
        if kc.dtype != kv_dtype:
            if kc.element_size() != getattr(kv_dtype, "itemsize", None):
                return None
            kc, vc = kc.view(kv_dtype), vc.view(kv_dtype)
    except (RuntimeError, TypeError, ValueError):
        return None
    k_scale = getattr(layer, "_k_scale", None)
    v_scale = getattr(layer, "_v_scale", None)
    return kc, vc, k_scale, v_scale


def _storage_bytes(tensor) -> int:
    return tensor.untyped_storage().size()


def _compile_winners(warmup, arguments, width, live=None) -> None:
    """Compile each tuned record's winner at ``width``; never raises on a record.

    ``live`` is ``live_kv_cache``'s result for the layer being warmed: its K/V
    views (and scales) replace the synthetic cache so the compiled kernels
    carry the same pointer specialization as traffic. Layers whose caches
    differ in that respect compile separately.
    """
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
    pointer_class = (
        None
        if live is None
        else tuple(
            _storage_bytes(t) <= POINTER_RANGE_32_MAX_BYTES for t in live[:2]
        )
    )
    if (key, width, pointer_class) in _state.compiled:
        return
    _state.compiled.add((key, width, pointer_class))
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
                k_scale = v_scale = kv_scale
                if live is not None:
                    kc, vc = live[0], live[1]
                    k_scale = kv_scale if live[2] is None else live[2]
                    v_scale = kv_scale if live[3] is None else live[3]
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
                    k_scale,
                    v_scale,
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
    if live is None:
        against = "synthetic KV cache (no live cache bound to the layer)"
    else:
        against = (
            f"live KV cache ({_storage_bytes(live[0]) / 2**30:.2f} GiB storage, "
            f"pointer_range_32={'yes' if all(pointer_class) else 'no'})"
        )
    print(
        "flexinfer: segmented runtime tables: block-table width "
        f"{width} (page {a['page']}), compiled {compiled} tuned winners "
        f"({skipped} skipped) against the {against} in "
        f"{time.monotonic() - start:.1f}s",
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
        _state.layer = layer
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
            _state.layer = None

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
            live = (
                None
                if _state.layer is None
                else live_kv_cache(_state.layer, bound.arguments["kv_dtype"])
            )
            _compile_winners(warmup, bound.arguments, _state.width, live)
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
