"""Convert fp8 KV to fp16 through a 256-entry table in segmented attention (opt-in).

RDNA3 has no fp8 matrix instruction, so vLLM's ``ROCM_SEGMENTED_ATTN`` stage
kernel converts every fp8 K and V element to fp16 (``k.to(q.dtype)``) and then
uses the fp16 WMMA. Triton 3.8 lowers that conversion (``tt.fp_to_fp f8E4M3FN
-> f16``) to nine compare-and-select pairs per element, 6,811 of the 16,293
instructions of a decode-shaped kernel. On the XTX that makes fp8 attention
5.8-6.1x the fp16 time per decode step at 64K-120K of context; gathering the
same fp16 bits from a 256-entry table instead brings it to 1.17x, bit for bit
identical to stock (matrix row ``gfx1100-fp8-attention-convert-2026-11-24``,
``scripts/bench-gfx1100-fp8-attention.py``).

``FLEXINFER_SEGMENTED_FP8_LUT=1`` installs that kernel on gfx11:

1. vLLM's ``segmented_attention.py`` is read from disk, checked against the
   sha256 the rewrite was written for, and rewritten: a helper that gathers
   from the table replaces the four fp8 conversions, the stage kernel gains
   one pointer parameter (``FXLUT``) and the launcher passes the table. The
   rewritten file is imported as a sibling module, so Triton sees a second
   stage kernel beside upstream's.
2. ``_launch_segmented_attention``, the one function every launch and every
   compile goes through (live traffic, the startup tuner, the runtime-table
   precompile), sends a one-byte KV cache to the rewritten launcher and
   everything else to upstream's. An fp16 cache launches exactly the kernel
   it launches without the flag, from the same Triton cache entries.
3. The startup tuner is told about the swap. Its tuning-table identity gains
   a key for fp8 caches, so the table tuned on the stock kernel is neither
   reused nor overwritten, and its scope that unloads tuning-only kernels
   also covers the rewritten stage kernel.
4. The table tensor is created on the worker's device when the tuner entry
   point runs, which vLLM calls after KV allocation and before graph capture.
   A launch that would have to create it inside a capture fails instead.

The flag does not lift upstream's gfx12 gate: without
``FLEXINFER_SEGMENTED_FP8_GFX11=1`` an fp8 cache never reaches the segmented
kernel on gfx11 and this patch has nothing to do. A vLLM whose source is not
one of ``KNOWN_SOURCES`` fails the boot that asked for the patch. Like the
rest of this package, the module imports neither vLLM nor torch at module
scope.
"""

import contextlib
import hashlib
import importlib
import importlib.util
import inspect
import os
import struct
import sys
import tempfile

from .segmented_fp8 import _is_power_of_ten

SEGMENTED_FP8_LUT_ENV = "FLEXINFER_SEGMENTED_FP8_LUT"
PLATFORM_MODULE = "vllm.platforms.rocm"
OPS_MODULE = "vllm.v1.attention.ops.segmented_attention"
WARMUP_MODULE = "vllm.model_executor.warmup.rocm_segmented_attn_autotune_warmup"
# The rewritten file is imported under vLLM's ops package so its relative
# imports resolve.
VARIANT_MODULE = "vllm.v1.attention.ops._flexinfer_segmented_attention_fp8_lut"
PATCH_MARKER = "_flexinfer_segmented_fp8_lut"
IDENTITY_KEY = "flexinfer_fp8_convert"
# sha256 of every vllm/v1/attention/ops/segmented_attention.py the rewrite has
# been read against. The anchors alone would accept a file that kept those
# lines and changed what surrounds them, so the hash decides.
KNOWN_SOURCES = {
    "e84a6dddfbf2eac7595dc99a5781880834a7afd8d7d598faeaaf3ee812f40689": (
        "vLLM 0.33.0+rocm72, Triton 3.8.0 (runtime@1a2dd85c and runtime@51afd0d3)"
    ),
}
# Everything the patch replaces, calls or reads. A vLLM that renamed any of
# them must fail the boot that asked for the patch, not run without it.
OPS_API = (
    "_launch_segmented_attention",
    "_segmented_attention_reduce",
    "_segmented_attention_stage",
    "compile_segmented_attention",
    "run_segmented_attention",
)
WARMUP_API = (
    "_identity",
    "_temporary_tuning_kernels",
    "rocm_segmented_attn_autotune_warmup",
    "torch",
)
PLATFORM_API = ("on_gfx11",)

HELPER_NAME = "_fx_fp8_to_f16"


class RewriteError(RuntimeError):
    """vLLM's segmented_attention.py is not the shape the rewrite expects."""


class _State:
    def __init__(self) -> None:
        self.launches = 0
        self.compiles = 0


_state = _State()


def segmented_fp8_lut_enabled(environ=os.environ) -> bool:
    value = environ.get(SEGMENTED_FP8_LUT_ENV, "0")
    if value not in {"0", "1"}:
        raise RuntimeError(f"{SEGMENTED_FP8_LUT_ENV} must be 0 or 1, got {value!r}")
    return value == "1"


def fp8_lut_launch_counts() -> dict:
    """fp8 launcher calls since boot that took the table kernel."""
    return {"launches": _state.launches, "compiles": _state.compiles}


# --- the table ---------------------------------------------------------------


def e4m3fn_value(code: int) -> float:
    """The value of one ``float8_e4m3fn`` code (sign 1, exponent 4, mantissa 3).

    Exponent 0 is zero or a subnormal ``m * 2**-9``; 0x7F and 0xFF are NaN;
    there is no infinity.
    """
    sign = -1.0 if code & 0x80 else 1.0
    exponent, mantissa = (code >> 3) & 0xF, code & 0x7
    if code & 0x7F == 0x7F:
        return float("nan")
    if exponent == 0:
        return sign * mantissa * 2.0**-9
    return sign * (1 + mantissa / 8) * 2.0 ** (exponent - 7)


def f16_bits(code: int) -> int:
    """The fp16 bit pattern stock vLLM's conversion yields for ``code``.

    Every finite fp8 value is exact in fp16. NaN becomes the quiet NaN with
    the code's sign, which is what Triton's lowering selects.
    """
    if code & 0x7F == 0x7F:
        return 0x7E00 | ((code & 0x80) << 8)
    (bits,) = struct.unpack("<H", struct.pack("<e", e4m3fn_value(code)))
    return bits


def lut_table() -> list:
    """fp16 bit patterns for fp8 codes 0..255, in code order."""
    return [f16_bits(code) for code in range(256)]


# --- the rewrite -------------------------------------------------------------

_HELPER = f"""
@triton.jit
def {HELPER_NAME}(x, FXLUT):
    return tl.load(FXLUT + x.to(tl.uint8, bitcast=True).to(tl.int32))
"""

_STAGE_DEF = "@triton.jit\ndef _segmented_attention_stage(\n"
_K_SITE = "                scores = tl.dot(q, k.to(q.dtype), scores)\n"
_V_SPLIT_SITE = (
    "                acc_low = tl.dot(prob, v_low.to(prob.dtype), acc_low)\n"
    "                acc_high = tl.dot(prob, v_high.to(prob.dtype), acc_high)\n"
)
_V_FP8_SITE = "v.to(Q.dtype.element_ty)"
_SIGNATURE_SITE = "    OUT,\n    Q0: tl.constexpr,\n"
_LAUNCH_SITE = "        out,\n        q.stride(0),\n"


def _lut_support() -> str:
    signed = [bits - 0x10000 if bits & 0x8000 else bits for bits in lut_table()]
    return f"""
_FX_LUT_INT16 = {signed!r}
_fx_lut_cache = {{}}


def _fx_lut(device):
    lut = _fx_lut_cache.get(device)
    if lut is None:
        lut = torch.tensor(_FX_LUT_INT16, dtype=torch.int16, device=device).view(
            torch.float16
        )
        _fx_lut_cache[device] = lut
    return lut
"""


def _replace(source: str, old: str, new: str, count: int, what: str) -> str:
    found = source.count(old)
    if found != count:
        raise RewriteError(
            f"{what}: expected {count} occurrence(s) of {old!r}, found {found}; "
            "this vLLM's segmented_attention.py changed shape"
        )
    return source.replace(old, new)


def source_sha256(source: str) -> str:
    return hashlib.sha256(source.encode()).hexdigest()


def rewrite_source(source: str) -> str:
    """Return ``source`` with the stage kernel's fp8 conversions gathered.

    Only what an fp8 cache executes changes: with ``FP8`` false the kernel
    takes the same ``.to(...)`` calls as before. The output is the ``lut``
    variant of ``scripts/fp8_convert_variants.py``, the text the kill test
    ran; ``build/scripts/test_vllm_plugin_common.py`` holds the two equal.
    """
    call = HELPER_NAME + "({}, FXLUT)"
    out = _replace(
        source,
        _K_SITE,
        "                if FP8:\n"
        f"                    k_f16 = {call.format('k')}.to(q.dtype)\n"
        "                else:\n"
        "                    k_f16 = k.to(q.dtype)\n"
        "                scores = tl.dot(q, k_f16, scores)\n",
        1,
        "K conversion",
    )
    out = _replace(
        out,
        _V_SPLIT_SITE,
        "                if FP8:\n"
        f"                    v_low_f16 = {call.format('v_low')}.to(prob.dtype)\n"
        f"                    v_high_f16 = {call.format('v_high')}.to(prob.dtype)\n"
        "                else:\n"
        "                    v_low_f16 = v_low.to(prob.dtype)\n"
        "                    v_high_f16 = v_high.to(prob.dtype)\n"
        "                acc_low = tl.dot(prob, v_low_f16, acc_low)\n"
        "                acc_high = tl.dot(prob, v_high_f16, acc_high)\n",
        1,
        "split V conversion",
    )
    # Both remaining sites sit under `if FP8 ...` / `elif FP8`.
    out = _replace(
        out,
        _V_FP8_SITE,
        f"{call.format('v')}.to(Q.dtype.element_ty)",
        2,
        "fp8 V conversion",
    )
    out = _replace(
        out,
        _SIGNATURE_SITE,
        "    OUT,\n    FXLUT,\n    Q0: tl.constexpr,\n",
        1,
        "stage kernel signature",
    )
    out = _replace(
        out,
        _LAUNCH_SITE,
        "        out,\n        _fx_lut(q.device),\n        q.stride(0),\n",
        1,
        "stage launch arguments",
    )
    support = _lut_support() + _HELPER
    return _replace(
        out,
        _STAGE_DEF,
        support.strip("\n") + "\n\n\n" + _STAGE_DEF,
        1,
        "stage kernel definition",
    )


def load_variant_module(rewritten: str):
    """Import ``rewritten`` as ``VARIANT_MODULE`` from a private temp file.

    Triton reads a kernel's source back through ``inspect``, which needs a
    real file. Its kernel cache key hashes the source, not the path, so the
    directory changing from boot to boot does not cost a recompile.
    """
    directory = tempfile.mkdtemp(prefix="flexinfer-fp8-lut-")
    path = os.path.join(directory, "flexinfer_segmented_attention_fp8_lut.py")
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(rewritten)
    spec = importlib.util.spec_from_file_location(VARIANT_MODULE, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[VARIANT_MODULE] = module
    try:
        spec.loader.exec_module(module)
    except BaseException:
        del sys.modules[VARIANT_MODULE]
        raise
    return module


# --- the install -------------------------------------------------------------


def _argument_reader(function, name):
    """Return ``read(args, kwargs)`` giving ``function``'s ``name`` argument."""
    signature = inspect.signature(function)
    if name not in signature.parameters:
        raise RuntimeError(
            f"flexinfer_vllm_common.segmented_fp8_lut: {function.__qualname__} "
            f"has no {name!r} parameter; this vLLM's segmented warmup changed shape"
        )

    def read(args, kwargs):
        return signature.bind_partial(*args, **kwargs).arguments.get(name)

    return read


def _table_ready(variant, torch, device) -> None:
    """Create the table for ``device`` outside a graph capture, or refuse."""
    if device in variant._fx_lut_cache:
        return
    if torch.cuda.is_current_stream_capturing():
        raise RuntimeError(
            f"{SEGMENTED_FP8_LUT_ENV}=1: the fp8 conversion table for {device} "
            "would be created inside a graph capture; vLLM did not run "
            "rocm_segmented_attn_autotune_warmup before capturing"
        )
    variant._fx_lut(device)


def install_segmented_fp8_lut(
    ops, warmup, platform, *, load_variant=load_variant_module
) -> bool:
    """Swap the fp8 stage kernel for the table one; return whether it is active.

    On anything but gfx11 nothing is patched: the table kernel has only been
    measured there. A second call is a no-op.
    """
    for module, api in (
        (ops, OPS_API),
        (warmup, WARMUP_API),
        (platform, PLATFORM_API),
    ):
        missing = [name for name in api if not hasattr(module, name)]
        if missing:
            raise RuntimeError(
                f"flexinfer_vllm_common.segmented_fp8_lut: {module.__name__} lacks "
                f"{missing}; this vLLM's segmented attention changed shape"
            )
    if not platform.on_gfx11():
        return False
    if getattr(ops, PATCH_MARKER, False):
        return True
    with open(ops.__file__, encoding="utf-8") as handle:
        source = handle.read()
    digest = source_sha256(source)
    if digest not in KNOWN_SOURCES:
        raise RuntimeError(
            f"{SEGMENTED_FP8_LUT_ENV}=1 but {ops.__file__} (sha256 {digest}) is "
            "not a segmented_attention.py the table kernel was written "
            f"against; known: {sorted(KNOWN_SOURCES)}"
        )
    rewritten = rewrite_source(source)
    variant_digest = source_sha256(rewritten)
    identity = warmup._identity
    read_kv_dtype = _argument_reader(identity, "kv_dtype")
    variant = load_variant(rewritten)
    # One reduce kernel: the rewrite does not touch it, and upstream's own
    # object is the one its graphs and its unload scope already hold.
    variant._segmented_attention_reduce = ops._segmented_attention_reduce
    torch = warmup.torch

    # 2. The launcher, through the module global every caller resolves.
    stock_launch = ops._launch_segmented_attention
    lut_launch = variant._launch_segmented_attention

    def _launch_segmented_attention(q, out, kc, vc, *args, **kwargs):
        if kc.element_size() != 1:
            return stock_launch(q, out, kc, vc, *args, **kwargs)
        _table_ready(variant, torch, q.device)
        if kwargs.get("compile_only"):
            _state.compiles += 1
        else:
            _state.launches += 1
            if _is_power_of_ten(_state.launches):
                print(
                    "flexinfer: segmented fp8 lut: fp8 launches through the "
                    f"table kernel={_state.launches} compiles={_state.compiles} "
                    "(eager calls and graph captures; graph replays are not "
                    "counted)",
                    flush=True,
                )
        return lut_launch(q, out, kc, vc, *args, **kwargs)

    _launch_segmented_attention.__wrapped__ = stock_launch

    # 3a. A tuning table of its own for fp8 caches.
    def _identity(*args, **kwargs):
        result = identity(*args, **kwargs)
        if getattr(read_kv_dtype(args, kwargs), "itemsize", None) == 1:
            result[IDENTITY_KEY] = f"lut:{variant_digest}"
        return result

    # 3b. The tuner's unload scope, widened to the rewritten stage kernel.
    temporary_tuning_kernels = warmup._temporary_tuning_kernels

    @contextlib.contextmanager
    def _temporary_tuning_kernels(device):
        index = torch.device(device).index
        if index is None:
            index = torch.accelerator.current_device_index()
        cache = variant._segmented_attention_stage.device_caches[index][0]
        existing = set(cache)
        with temporary_tuning_kernels(device):
            try:
                yield
            finally:
                # Upstream's exit then collects and empties the allocator.
                torch.accelerator.synchronize(device)
                for key in cache.keys() - existing:
                    del cache[key]

    # 4. The table, before anything can capture a launch.
    autotune = warmup.rocm_segmented_attn_autotune_warmup

    def rocm_segmented_attn_autotune_warmup(worker):
        device = torch.empty(0, device=worker.device).device
        _table_ready(variant, torch, device)
        return autotune(worker)

    ops._launch_segmented_attention = _launch_segmented_attention
    warmup._identity = _identity
    warmup._temporary_tuning_kernels = _temporary_tuning_kernels
    warmup.rocm_segmented_attn_autotune_warmup = rocm_segmented_attn_autotune_warmup
    setattr(ops, PATCH_MARKER, variant_digest)
    print(
        "flexinfer: segmented fp8 lut: fp8 KV launches use the table kernel "
        f"(vLLM source {digest[:12]} = {KNOWN_SOURCES[digest]}; rewritten "
        f"{variant_digest[:12]}; module {variant.__file__})",
        flush=True,
    )
    return True


def install_segmented_fp8_lut_if_enabled(environ=os.environ) -> bool:
    """Install the table kernel when the flag is set; return whether it is active."""
    if not segmented_fp8_lut_enabled(environ):
        return False
    try:
        platform = importlib.import_module(PLATFORM_MODULE)
        ops = importlib.import_module(OPS_MODULE)
        warmup = importlib.import_module(WARMUP_MODULE)
    except ModuleNotFoundError as exc:
        raise RuntimeError(
            f"{SEGMENTED_FP8_LUT_ENV}=1 but this vLLM lacks {exc.name}"
        ) from exc
    active = install_segmented_fp8_lut(ops, warmup, platform)
    if not active:
        print(
            f"flexinfer: {SEGMENTED_FP8_LUT_ENV}=1 on a host that is not gfx11; "
            "segmented attention left as upstream ships it",
            flush=True,
        )
    return active
