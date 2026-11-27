#!/usr/bin/env python3
"""fp8 -> fp16 conversion variants for vLLM's ROCm segmented attention (stdlib).

Why this exists. On gfx1100 the fp8 KV cache costs about 0.74 ms of decode
step per 1K context tokens against about 0.11 ms for fp16, although it reads
half the bytes (128K lane vs the 64K workhorse, 2026-11-21..23). RDNA3 has no
fp8 matrix instruction, so the stage kernel converts every fp8 K and V element
to fp16 and then uses the fp16 WMMA. Triton 3.8 lowers that conversion
(``tt.fp_to_fp f8E4M3FN -> f16``) to nine compare-and-select pairs per
element: NaN, zero and the seven subnormal codes each get their own. In a
compiled decode-shaped kernel those are 6,811 of 16,293 instructions.

This module holds cheaper conversions written as Triton helpers, a pure-Python
model of each one (so their exactness is provable on a CPU, for all 256
codes), and a rewriter that swaps them into vLLM's
``segmented_attention.py`` source. It imports neither vLLM, Triton nor torch
at module scope. ``scripts/bench-gfx1100-fp8-attention.py`` runs the result on
a card.

Format. ``torch.float8_e4m3fn`` (Triton ``f8E4M3FN``), which is what vLLM's
fp8 KV cache holds on ROCm: sign(1) exponent(4, bias 7) mantissa(3). Exponent
0 is zero or a subnormal ``m * 2**-9``. 0x7F and 0xFF are NaN. There is no
infinity; the largest value is 448.

The fp16 bit pattern of a normal code is ``((code & 0x7F) << 7) + 0x2000``
plus the sign, with no table and no branch. Only exponent 0 and NaN need
anything else, and the variants differ in how they treat those.

Variants:

  stock        vLLM as shipped (``k.to(q.dtype)``). The baseline.
  exact        Bit-identical to stock for all 256 codes. Exponent 0 goes
               through one integer-to-float conversion (``m * 2**-9`` is exact
               in fp16), NaN through one select.
  exact_nonan  As exact without the NaN select: 0x7F and 0xFF decode to +-480.
               Identical to stock on every code a quantizer can write, since
               ``x.to(float8_e4m3fn)`` yields NaN only from a NaN input.
  approx       The normal formula for every code. Zero and the subnormals come
               out as ``2**-7 * (1 + m/8)`` instead of ``m * 2**-9``: an
               absolute error of at most 2**-7 in scaled units, where one fp8
               step near 1.0 is 2**-3. Not bit-identical. It is the floor for
               how cheap a conversion can get, and needs a quality gate.
  lut          A 256-entry fp16 table gathered per element. Exact.
"""

import hashlib
import importlib.util
import math
import os
import struct
import sys

# sha256 of the vllm/v1/attention/ops/segmented_attention.py the anchors below
# were written against. A different file may still rewrite cleanly; the anchor
# counts decide, not the hash.
KNOWN_SOURCES = {
    "e84a6dddfbf2eac7595dc99a5781880834a7afd8d7d598faeaaf3ee812f40689": (
        "vLLM 0.33.0+rocm72, Triton 3.8.0 (runtime@1a2dd85c, 2026-11-24)"
    ),
}

VARIANTS = ("stock", "exact", "exact_nonan", "approx", "lut", "fmul")
HELPER_NAME = "_fx_fp8_to_f16"
NAN_BITS = 0x7E00
SUBNORMAL_STEP = 2.0**-9


class RewriteError(RuntimeError):
    """The vLLM source does not have the shape the rewriter was written for."""


# --- the format, and a model of each variant --------------------------------


def e4m3fn_value(code: int) -> float:
    """The value of one ``float8_e4m3fn`` code, from the format definition."""
    if not 0 <= code <= 0xFF:
        raise ValueError(f"not a byte: {code!r}")
    sign = -1.0 if code & 0x80 else 1.0
    exponent, mantissa = (code >> 3) & 0xF, code & 0x7
    if exponent == 0xF and mantissa == 0x7:
        return math.nan
    if exponent == 0:
        return sign * mantissa * SUBNORMAL_STEP
    return sign * (1.0 + mantissa / 8.0) * 2.0 ** (exponent - 7)


def f16_bits(value: float) -> int:
    """IEEE half bits of ``value`` (which must be exactly representable)."""
    return struct.unpack("<H", struct.pack("<e", value))[0]


def f16_value(bits: int) -> float:
    return struct.unpack("<e", struct.pack("<H", bits & 0xFFFF))[0]


def stock_bits(code: int) -> int:
    """What Triton's ``f8E4M3FN -> f16`` conversion yields, as fp16 bits."""
    sign = (code & 0x80) << 8
    if code & 0x7F == 0x7F:
        return NAN_BITS | sign
    return f16_bits(abs(e4m3fn_value(code))) | sign


def _normal_bits(magnitude: int) -> int:
    return ((magnitude << 7) + 0x2000) & 0xFFFF


def exact_bits(code: int) -> int:
    magnitude = code & 0x7F
    if magnitude < 8:
        # uint16 -> fp16, times 2**-9: exact, and zero falls out of it.
        bits = f16_bits(magnitude * SUBNORMAL_STEP)
    else:
        bits = _normal_bits(magnitude)
    if magnitude == 0x7F:
        bits = NAN_BITS
    return bits | ((code & 0x80) << 8)


def exact_nonan_bits(code: int) -> int:
    magnitude = code & 0x7F
    if magnitude < 8:
        bits = f16_bits(magnitude * SUBNORMAL_STEP)
    else:
        bits = _normal_bits(magnitude)
    return bits | ((code & 0x80) << 8)


def approx_bits(code: int) -> int:
    return _normal_bits(code & 0x7F) | ((code & 0x80) << 8)


def lut_bits(code: int) -> int:
    return stock_bits(code)


def fmul_bits(code: int) -> int:
    """The arithmetic of Triton's later software cast (triton-lang/triton#12492).

    The fp8 bits sit in fp16 position with the fp8 bias, and one fp16 multiply
    by 2**8 rebiases the normal codes and normalizes exponent 0 exactly. One
    select covers NaN. Bit-identical to stock on all 256 codes.
    """
    magnitude = code & 0x7F
    bits = f16_bits(f16_value(magnitude << 7) * 256.0)
    if magnitude == 0x7F:
        bits = NAN_BITS
    return bits | ((code & 0x80) << 8)


MODELS = {
    "stock": stock_bits,
    "exact": exact_bits,
    "exact_nonan": exact_nonan_bits,
    "approx": approx_bits,
    "lut": lut_bits,
    "fmul": fmul_bits,
}


def lut_table() -> list:
    """The 256 fp16 bit patterns the ``lut`` variant gathers from."""
    return [stock_bits(code) for code in range(256)]


def differing_codes(variant: str) -> list:
    """Codes whose fp16 bits differ from stock under ``variant``."""
    model = MODELS[variant]
    return [code for code in range(256) if model(code) != stock_bits(code)]


def max_abs_error(variant: str) -> float:
    """Largest ``|variant - stock|`` over the codes where both are finite."""
    worst = 0.0
    model = MODELS[variant]
    for code in range(256):
        want, got = f16_value(stock_bits(code)), f16_value(model(code))
        if math.isnan(want) or math.isnan(got):
            continue
        worst = max(worst, abs(got - want))
    return worst


# --- the Triton helpers ------------------------------------------------------

# Each helper takes the fp8 tile exactly as the kernel loaded it and returns
# fp16. Every integer step is pinned to uint16 so a Python literal cannot widen
# the result before the final bitcast.
_HELPER_HEAD = f"""
@triton.jit
def {HELPER_NAME}(x):
    b = x.to(tl.uint8, bitcast=True).to(tl.uint16)
    mag = (b & 0x7F).to(tl.uint16)
"""

_HELPER_SUBNORMAL = """\
    sub = (mag.to(tl.float16) * 0.001953125).to(tl.uint16, bitcast=True)
    bits = tl.where(mag < 8, sub, ((mag << 7) + 0x2000).to(tl.uint16))
"""

_HELPER_TAIL = """\
    bits = (bits.to(tl.uint16) | ((b & 0x80) << 8).to(tl.uint16)).to(tl.uint16)
    return bits.to(tl.float16, bitcast=True)
"""

HELPERS = {
    "exact": (
        _HELPER_HEAD
        + _HELPER_SUBNORMAL
        + "    bits = tl.where(mag == 0x7F, 0x7E00, bits)\n"
        + _HELPER_TAIL
    ),
    "exact_nonan": _HELPER_HEAD + _HELPER_SUBNORMAL + _HELPER_TAIL,
    "approx": (
        _HELPER_HEAD + "    bits = ((mag << 7) + 0x2000).to(tl.uint16)\n" + _HELPER_TAIL
    ),
    # A proxy for the compiler's own form after triton-lang/triton#12492: the
    # same arithmetic written in Triton source. The compiler may schedule its
    # packed fp16 multiply more tightly than this.
    "fmul": (
        _HELPER_HEAD
        + "    half = (mag << 7).to(tl.uint16).to(tl.float16, bitcast=True)\n"
        + "    bits = (half * 256.0).to(tl.float16).to(tl.uint16, bitcast=True)\n"
        + "    bits = tl.where(mag == 0x7F, 0x7E00, bits)\n"
        + _HELPER_TAIL
    ),
    "lut": f"""
@triton.jit
def {HELPER_NAME}(x, FXLUT):
    return tl.load(FXLUT + x.to(tl.uint8, bitcast=True).to(tl.int32))
""",
}


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


# --- the rewriter ------------------------------------------------------------

_STAGE_DEF = "@triton.jit\ndef _segmented_attention_stage(\n"
_K_SITE = "                scores = tl.dot(q, k.to(q.dtype), scores)\n"
_V_SPLIT_SITE = (
    "                acc_low = tl.dot(prob, v_low.to(prob.dtype), acc_low)\n"
    "                acc_high = tl.dot(prob, v_high.to(prob.dtype), acc_high)\n"
)
_V_FP8_SITE = "v.to(Q.dtype.element_ty)"
_SIGNATURE_SITE = "    OUT,\n    Q0: tl.constexpr,\n"
_LAUNCH_SITE = "        out,\n        q.stride(0),\n"


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


def rewrite_source(source: str, variant: str) -> str:
    """Return ``source`` with the stage kernel's fp8 conversions replaced.

    ``stock`` returns the source unchanged. Every other variant changes only
    what an fp8 cache executes: with ``FP8`` false the kernel takes the same
    ``.to(...)`` calls as before.
    """
    if variant not in VARIANTS:
        raise ValueError(f"unknown variant {variant!r}; choose from {VARIANTS}")
    if variant == "stock":
        return source
    call = HELPER_NAME + ("({}, FXLUT)" if variant == "lut" else "({})")

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
    support = HELPERS[variant]
    if variant == "lut":
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
        support = _lut_support() + support
    return _replace(
        out,
        _STAGE_DEF,
        support.strip("\n") + "\n\n\n" + _STAGE_DEF,
        1,
        "stage kernel definition",
    )


def load_variant_module(source: str, variant: str, directory: str):
    """Import ``rewrite_source(source, variant)`` as a sibling of vLLM's module.

    The file is written to ``directory`` first: Triton reads a kernel's source
    back through ``inspect``, which needs a real file. The module is named
    inside ``vllm.v1.attention.ops`` so its relative imports resolve.
    """
    os.makedirs(directory, exist_ok=True)
    path = os.path.join(directory, f"fx_segmented_attention_{variant}.py")
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(rewrite_source(source, variant))
    name = f"vllm.v1.attention.ops._fx_segmented_attention_{variant}"
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


def main(argv=None) -> int:
    """Print each variant's exactness against stock; no GPU needed."""
    del argv
    for variant in VARIANTS[1:]:
        codes = differing_codes(variant)
        print(
            f"{variant:12s} differs from stock on {len(codes):3d} of 256 codes"
            f"  max |error| {max_abs_error(variant):.6g}"
            + (f"  codes {[hex(c) for c in codes]}" if 0 < len(codes) <= 16 else "")
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
