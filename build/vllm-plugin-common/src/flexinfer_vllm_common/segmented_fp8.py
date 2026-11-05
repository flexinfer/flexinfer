"""Let ROCm segmented attention serve an fp8 KV cache on gfx11 (opt-in).

vLLM 0.32 and 0.33 gate fp8 KV in ``ROCM_SEGMENTED_ATTN`` to gfx12 (gap G3 in
``.loom/10-research-backend-currency-gfx1100-gfx906-2026-11-04.md``), although
the Triton stage kernel converts every fp8 K/V tile to the query dtype before
``tl.dot`` and gfx11 runs the same software conversion today in
``TRITON_ATTN`` with an fp8 cache. The gate sits in three places, each asking
``vllm.platforms.rocm.on_gfx12x()`` at call time:

1. ``RocmSegmentedAttentionBackend.supports_combination`` (backend selection
   rejects ``fp8`` / ``fp8_e4m3``);
2. ``RocmSegmentedAttentionImpl.__init__`` (raises for an fp8 layer);
3. ``vllm.v1.attention.ops.segmented_attention.segmented_attention``, the
   dispatcher the backend's ``forward`` calls, which otherwise sends an fp8
   cache on gfx11 silently to the unified Triton kernel.

``FLEXINFER_SEGMENTED_FP8_GFX11=1`` wraps exactly those three call sites so
that, for an fp8 KV dtype on gfx11 only, ``on_gfx12x`` answers like
``on_gfx1x`` for the duration of the call. Nothing else in vLLM sees the
change (``on_gfx12x`` also picks gfx12-only GEMM paths, which must stay off),
the warmup autotuner already launches the fp8 kernels without a gate, and on
gfx12 or for an fp16 cache every wrapper calls straight through.

The dispatcher wrapper counts fp8 calls that took the segmented kernel and
those that still fell back (``fp8_dispatch_counts``) and prints both counts
whenever either reaches 1, 10, 100, ..., so a canary's engine log shows which
path served it. Graph replays bypass Python and are not counted. Like the rest
of this package, the module imports neither vLLM nor torch at module scope.
"""

import contextlib
import importlib
import inspect
import os

SEGMENTED_FP8_ENV = "FLEXINFER_SEGMENTED_FP8_GFX11"
PLATFORM_MODULE = "vllm.platforms.rocm"
BACKEND_MODULE = "vllm.v1.attention.backends.rocm_segmented_attn"
OPS_MODULE = "vllm.v1.attention.ops.segmented_attention"
FP8_KV_DTYPES = ("fp8", "fp8_e4m3")
PATCH_MARKER = "_flexinfer_segmented_fp8_gfx11"
# Everything the patch wraps or reads. A vLLM that renamed any of them must
# fail the boot that asked for the patch, not run without it.
BACKEND_API = (
    "RocmSegmentedAttentionBackend",
    "RocmSegmentedAttentionImpl",
    "segmented_attention",
)
PLATFORM_API = ("on_gfx11", "on_gfx1x", "on_gfx12x")
OPS_API = ("segmented_attention", "run_segmented_attention")


class _State:
    def __init__(self) -> None:
        self.segmented = 0
        self.fallback = 0


_state = _State()


def _is_power_of_ten(count: int) -> bool:
    while count >= 10 and count % 10 == 0:
        count //= 10
    return count == 1


def segmented_fp8_enabled(environ=os.environ) -> bool:
    value = environ.get(SEGMENTED_FP8_ENV, "0")
    if value not in {"0", "1"}:
        raise RuntimeError(f"{SEGMENTED_FP8_ENV} must be 0 or 1, got {value!r}")
    return value == "1"


def fp8_dispatch_counts() -> dict:
    """fp8 dispatcher calls since boot: segmented kernel vs unified fallback."""
    return {"segmented": _state.segmented, "fallback": _state.fallback}


@contextlib.contextmanager
def gfx11_fp8_gate_open(platform):
    """Within the block, ``platform.on_gfx12x`` answers like ``on_gfx1x``."""
    original = platform.on_gfx12x
    platform.on_gfx12x = platform.on_gfx1x
    try:
        yield
    finally:
        platform.on_gfx12x = original


def _kv_dtype_reader(function, name="kv_cache_dtype"):
    """Return ``read(args, kwargs)`` giving ``function``'s ``name`` argument."""
    signature = inspect.signature(function)
    if name not in signature.parameters:
        raise RuntimeError(
            f"flexinfer_vllm_common.segmented_fp8: {function.__qualname__} has "
            f"no {name!r} parameter; this vLLM's segmented backend changed shape"
        )

    def read(args, kwargs):
        if name in kwargs:
            return kwargs[name]
        try:
            return signature.bind_partial(*args, **kwargs).arguments.get(name)
        except TypeError:
            return None

    return read


def _wrap_gated(function, platform, read_kv_dtype):
    def wrapped(*args, **kwargs):
        if read_kv_dtype(args, kwargs) in FP8_KV_DTYPES:
            with gfx11_fp8_gate_open(platform):
                return function(*args, **kwargs)
        return function(*args, **kwargs)

    wrapped.__wrapped__ = function
    wrapped.__name__ = getattr(function, "__name__", "wrapped")
    wrapped.__qualname__ = getattr(function, "__qualname__", wrapped.__name__)
    return wrapped


def _wrap_dispatcher(dispatcher, runner_holder, platform, read_kv_dtype):
    """Gate-open wrapper that also records which path an fp8 call took."""

    def segmented_attention(*args, **kwargs):
        if read_kv_dtype(args, kwargs) not in FP8_KV_DTYPES:
            return dispatcher(*args, **kwargs)
        runner = runner_holder.run_segmented_attention
        took_kernel = []

        def run_segmented_attention(*r_args, **r_kwargs):
            took_kernel.append(True)
            return runner(*r_args, **r_kwargs)

        runner_holder.run_segmented_attention = run_segmented_attention
        try:
            with gfx11_fp8_gate_open(platform):
                result = dispatcher(*args, **kwargs)
        finally:
            runner_holder.run_segmented_attention = runner
        if took_kernel:
            _state.segmented += 1
            count = _state.segmented
        else:
            _state.fallback += 1
            count = _state.fallback
        if _is_power_of_ten(count):
            print(
                "flexinfer: segmented fp8 (gfx11): fp8 dispatches "
                f"segmented={_state.segmented} fallback={_state.fallback} "
                "(eager calls and graph captures; graph replays are not counted)",
                flush=True,
            )
        return result

    segmented_attention.__wrapped__ = dispatcher
    return segmented_attention


def install_segmented_fp8(backend_module, ops_module, platform) -> bool:
    """Wrap the three gfx12 checks; return whether this host is gfx11.

    On anything but gfx11 nothing is patched (gfx12 already allows fp8, and
    non-RDNA hosts cannot select the backend). A second call is a no-op.
    """
    for module, api in (
        (backend_module, BACKEND_API),
        (ops_module, OPS_API),
        (platform, PLATFORM_API),
    ):
        missing = [name for name in api if not hasattr(module, name)]
        if missing:
            raise RuntimeError(
                f"flexinfer_vllm_common.segmented_fp8: {module.__name__} lacks "
                f"{missing}; this vLLM's segmented attention changed shape"
            )
    if not platform.on_gfx11():
        return False
    if getattr(backend_module, PATCH_MARKER, False):
        return True
    backend = backend_module.RocmSegmentedAttentionBackend
    impl = backend_module.RocmSegmentedAttentionImpl

    # 1. Backend selection. supports_combination is a classmethod: wrap the
    # function and bind it again so ``cls`` stays the class it is called on.
    combination = backend.__dict__["supports_combination"].__func__
    backend.supports_combination = classmethod(
        _wrap_gated(combination, platform, _kv_dtype_reader(combination))
    )

    # 2. The impl's constructor check.
    init = impl.__init__
    impl.__init__ = _wrap_gated(init, platform, _kv_dtype_reader(init))

    # 3. The dispatcher, through the name the backend's forward resolves.
    dispatcher = backend_module.segmented_attention
    backend_module.segmented_attention = _wrap_dispatcher(
        dispatcher, ops_module, platform, _kv_dtype_reader(dispatcher)
    )
    setattr(backend_module, PATCH_MARKER, True)
    return True


def install_segmented_fp8_if_enabled(environ=os.environ) -> bool:
    """Install the gate lift when the flag is set; return whether it is active."""
    if not segmented_fp8_enabled(environ):
        return False
    try:
        platform = importlib.import_module(PLATFORM_MODULE)
        backend_module = importlib.import_module(BACKEND_MODULE)
        ops_module = importlib.import_module(OPS_MODULE)
    except ModuleNotFoundError as exc:
        raise RuntimeError(
            f"{SEGMENTED_FP8_ENV}=1 but this vLLM lacks {exc.name}"
        ) from exc
    active = install_segmented_fp8(backend_module, ops_module, platform)
    if not active:
        print(
            f"flexinfer: {SEGMENTED_FP8_ENV}=1 on a host that is not gfx11; "
            "segmented attention left as upstream ships it",
            flush=True,
        )
    return active
