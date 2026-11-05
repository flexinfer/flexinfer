"""Serve the target's input-embedding table from mapped host memory (opt-in).

A decode step reads a handful of embedding rows (one 10 KB row per scheduled
token on Qwen3.8-27B), yet the table itself, 248,320 x 5,120 fp16 = 2.37 GiB on
that model, sits in VRAM for the engine's lifetime and is charged to the KV
pool. vLLM's UVA offloader (``--cpu-offload-params``) only wraps decoder layers
and multimodal towers, so it cannot reach ``embed_tokens`` (gap G2 in
``.loom/10-research-backend-currency-gfx1100-gfx906-2026-11-04.md``).

``FLEXINFER_EMBED_HOST=1`` patches the model loader's ``load_model`` so that,
right after the TARGET model is loaded, each eligible input-embedding table is

1. copied to host memory and mapped into the GPU's address space with vLLM's
   own ``get_accelerator_view_from_cpu_tensor`` (the mechanism Model Runner V2
   already uses for its ``UvaBuffer`` inputs). A pageable copy goes in, so the
   op allocates exactly the table's size with ``cudaHostAlloc`` (mapped)
   instead of torch's power-of-two pinned cache, and the returned view keeps
   that allocation alive;
2. swapped in as the parameter's data, after a row spot-check that the mapped
   view reads back the same values as the device table;
3. released from the device, before the caller's ``DeviceMemoryProfiler``
   window closes, so ``model_memory_usage`` and therefore the KV sizing see the
   saving.

The MTP / EAGLE draft is loaded after the target and shares the target's
``embed_tokens`` module (``maybe_share_target_embed``), so it reads the same
mapped table; draft loads themselves are left alone. A table tied to an
``lm_head`` is never moved: the head is read in full every step.

Unset or ``0`` leaves vLLM untouched. With the flag set, a target load that
moves no table fails the boot instead of serving without the saving. Like the
rest of this package, the module imports neither vLLM nor torch at module
scope.
"""

import importlib
import os

EMBED_HOST_ENV = "FLEXINFER_EMBED_HOST"
BASE_LOADER_MODULE = "vllm.model_executor.model_loader.base_loader"
EMBEDDING_MODULE = "vllm.model_executor.layers.vocab_parallel_embedding"
TORCH_UTILS_MODULE = "vllm.utils.torch_utils"
EMBED_ATTRIBUTE = "embed_tokens"
PATCH_MARKER = "_flexinfer_embed_host"
# Rows compared between the device table and its mapped view before the swap.
_SPOT_ROWS = 8


class _State:
    def __init__(self) -> None:
        self.moved: list[dict] = []
        self.loads = 0


_state = _State()


def embed_host_enabled(environ=os.environ) -> bool:
    value = environ.get(EMBED_HOST_ENV, "0")
    if value not in {"0", "1"}:
        raise RuntimeError(f"{EMBED_HOST_ENV} must be 0 or 1, got {value!r}")
    return value == "1"


def is_target_load(vllm_config, model_config) -> bool:
    """Whether a ``load_model`` call loads the target, not a draft.

    Both model runners pass ``vllm_config.model_config`` itself for the target;
    draft loaders pass the speculative config's ``draft_model_config``.
    """
    return model_config is getattr(vllm_config, "model_config", None)


def _storage_ptr(tensor) -> int:
    return tensor.untyped_storage().data_ptr()


def embedding_tables(model, embedding_cls, lm_head_cls):
    """Yield ``(name, module)`` for each input-embedding table worth moving.

    A table qualifies when its module is a ``VocabParallelEmbedding`` (not an
    ``lm_head``, which subclasses it) registered as ``embed_tokens``, its
    weight is a floating-point device tensor, and no ``lm_head`` shares its
    storage (tied word embeddings).
    """
    modules = list(model.named_modules())
    head_storages = {
        _storage_ptr(module.weight)
        for _, module in modules
        if isinstance(module, lm_head_cls) and hasattr(module, "weight")
    }
    seen = set()
    for name, module in modules:
        if not isinstance(module, embedding_cls) or isinstance(module, lm_head_cls):
            continue
        if name.rsplit(".", 1)[-1] != EMBED_ATTRIBUTE:
            continue
        weight = getattr(module, "weight", None)
        if weight is None or not weight.is_floating_point() or not weight.is_cuda:
            continue
        storage = _storage_ptr(weight)
        if storage in head_storages:
            print(
                f"flexinfer: embed_host: {name} is tied to an lm_head; "
                "left in device memory",
                flush=True,
            )
            continue
        if storage in seen:
            continue
        seen.add(storage)
        yield name, module


def _spot_rows(torch, rows: int, device):
    count = min(_SPOT_ROWS, rows)
    if count <= 1:
        return torch.zeros(count, dtype=torch.long, device=device)
    step = (rows - 1) / (count - 1)
    return torch.tensor(
        sorted({round(i * step) for i in range(count)}),
        dtype=torch.long,
        device=device,
    )


def move_table_to_host(name, module, torch, view_from_cpu) -> dict:
    """Swap ``module.weight``'s data for a mapped host view; return a record."""
    weight = module.weight
    device_table = weight.data
    nbytes = device_table.numel() * device_table.element_size()
    host_copy = device_table.to("cpu")
    mapped = view_from_cpu(host_copy)
    del host_copy
    if (
        mapped.shape != device_table.shape
        or mapped.dtype != device_table.dtype
        or not mapped.is_cuda
    ):
        raise RuntimeError(
            f"flexinfer: embed_host: mapped view of {name} is "
            f"{tuple(mapped.shape)} {mapped.dtype} on {mapped.device}, expected "
            f"{tuple(device_table.shape)} {device_table.dtype} on the GPU"
        )
    rows = _spot_rows(torch, device_table.shape[0], device_table.device)
    if not torch.equal(
        mapped.index_select(0, rows), device_table.index_select(0, rows)
    ):
        raise RuntimeError(
            f"flexinfer: embed_host: mapped view of {name} does not read back "
            "the device table; refusing to swap it in"
        )
    weight.data = mapped
    del device_table
    return {
        "name": name,
        "shape": tuple(mapped.shape),
        "dtype": str(mapped.dtype).replace("torch.", ""),
        "bytes": nbytes,
    }


def move_embeddings_to_host(model, torch, embedding_cls, lm_head_cls, view_from_cpu):
    """Move every eligible table of ``model``; return the records."""
    synchronize = getattr(torch.cuda, "synchronize", None)
    allocated = getattr(torch.cuda, "memory_allocated", None)
    before = allocated() if callable(allocated) else None
    records = [
        move_table_to_host(name, module, torch, view_from_cpu)
        for name, module in embedding_tables(model, embedding_cls, lm_head_cls)
    ]
    if records:
        if callable(synchronize):
            synchronize()
        accelerator = getattr(torch, "accelerator", None)
        empty_cache = (
            getattr(accelerator, "empty_cache", None) or torch.cuda.empty_cache
        )
        empty_cache()
    after = allocated() if callable(allocated) else None
    for record in records:
        print(
            f"flexinfer: embed_host: moved {record['name']} {list(record['shape'])} "
            f"{record['dtype']} ({record['bytes'] / 2**30:.2f} GiB) to mapped host "
            "memory",
            flush=True,
        )
    if records and before is not None and after is not None:
        print(
            "flexinfer: embed_host: device allocated "
            f"{before / 2**30:.2f} -> {after / 2**30:.2f} GiB",
            flush=True,
        )
    return records


def install_embed_host(base_loader, torch, embedding_cls, lm_head_cls, view_from_cpu):
    """Wrap ``BaseModelLoader.load_model``; a second call is a no-op."""
    loader_cls = getattr(base_loader, "BaseModelLoader", None)
    if loader_cls is None or not callable(getattr(loader_cls, "load_model", None)):
        raise RuntimeError(
            f"flexinfer_vllm_common.embed_host: {base_loader.__name__} has no "
            "BaseModelLoader.load_model; this vLLM's loader changed shape"
        )
    if getattr(loader_cls, PATCH_MARKER, False):
        return
    load_model = loader_cls.load_model

    def wrapped_load_model(self, vllm_config, model_config, *args, **kwargs):
        model = load_model(self, vllm_config, model_config, *args, **kwargs)
        if not is_target_load(vllm_config, model_config):
            return model
        _state.loads += 1
        records = move_embeddings_to_host(
            model, torch, embedding_cls, lm_head_cls, view_from_cpu
        )
        if not records:
            raise RuntimeError(
                f"{EMBED_HOST_ENV}=1 but {type(model).__name__} has no untied "
                f"device-resident {EMBED_ATTRIBUTE} table to move"
            )
        _state.moved.extend(records)
        return model

    wrapped_load_model.__wrapped__ = load_model
    loader_cls.load_model = wrapped_load_model
    setattr(loader_cls, PATCH_MARKER, True)


def install_embed_host_if_enabled(environ=os.environ) -> bool:
    """Install the loader patch when the flag is set; return whether it is."""
    if not embed_host_enabled(environ):
        return False
    import torch

    try:
        base_loader = importlib.import_module(BASE_LOADER_MODULE)
        embedding = importlib.import_module(EMBEDDING_MODULE)
        torch_utils = importlib.import_module(TORCH_UTILS_MODULE)
    except ModuleNotFoundError as exc:
        raise RuntimeError(
            f"{EMBED_HOST_ENV}=1 but this vLLM lacks {exc.name}"
        ) from exc
    try:
        embedding_cls = embedding.VocabParallelEmbedding
        lm_head_cls = embedding.ParallelLMHead
        view_from_cpu = torch_utils.get_accelerator_view_from_cpu_tensor
    except AttributeError as exc:
        raise RuntimeError(
            f"{EMBED_HOST_ENV}=1 but this vLLM renamed an API the patch needs: {exc}"
        ) from exc
    install_embed_host(base_loader, torch, embedding_cls, lm_head_cls, view_from_cpu)
    return True
