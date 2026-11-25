"""The fp8 -> fp16 variants must be what their names say, and the rewriter must
fail loudly on a vLLM source it does not recognise."""

import ast
import importlib.util
import math
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location(
    "fx", Path(__file__).with_name("fp8_convert_variants.py")
)
fx = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fx)

# The lines of vLLM 0.33.0's segmented_attention.py that the rewriter touches,
# verbatim, inside just enough structure to parse. The fp16 branch
# (`tl.dot(..., v, acc)`) is there because it must survive untouched.
FIXTURE = """\
import torch

from vllm.triton_utils import tl, triton


@triton.jit
def _segmented_attention_stage(
    Q,
    K,
    V,
    OUT,
    Q0: tl.constexpr,
    FP8: tl.constexpr,
    PV_SPLIT: tl.constexpr,
):
    for start in tl.range(0, 4, 1):
        for ki in tl.range(0, 4, 1):
            if True:
                scores = tl.dot(q, k.to(q.dtype), scores)
        if True:
            if PV_SPLIT:
                acc_low = tl.dot(prob, v_low.to(prob.dtype), acc_low)
                acc_high = tl.dot(prob, v_high.to(prob.dtype), acc_high)
            else:
                if FP8 and D == 256 and BM >= 32:
                    acc = tl.dot(
                        p.to(Q.dtype.element_ty), v.to(Q.dtype.element_ty), acc
                    )
                elif FP8:
                    acc += (
                        tl.dot(p.to(Q.dtype.element_ty), v.to(Q.dtype.element_ty))
                        * v_scale
                    )
                else:
                    acc = tl.dot(p.to(Q.dtype.element_ty), v, acc)


@triton.jit
def _segmented_attention_reduce(
    PART,
    OUT,
    O0: tl.constexpr,
):
    pass


def _launch_segmented_attention(q, out, kc, vc):
    stage_args = (
        q,
        kc,
        vc,
        out,
        q.stride(0),
    )
    return stage_args
"""


class Format(unittest.TestCase):
    def test_reference_values(self):
        self.assertEqual(fx.e4m3fn_value(0x00), 0.0)
        self.assertEqual(math.copysign(1.0, fx.e4m3fn_value(0x80)), -1.0)
        self.assertEqual(fx.e4m3fn_value(0x01), 2.0**-9)
        self.assertEqual(fx.e4m3fn_value(0x07), 7 * 2.0**-9)
        self.assertEqual(fx.e4m3fn_value(0x08), 2.0**-6)
        self.assertEqual(fx.e4m3fn_value(0x38), 1.0)
        self.assertEqual(fx.e4m3fn_value(0x7E), 448.0)
        self.assertEqual(fx.e4m3fn_value(0xFE), -448.0)
        self.assertTrue(math.isnan(fx.e4m3fn_value(0x7F)))
        self.assertTrue(math.isnan(fx.e4m3fn_value(0xFF)))

    def test_stock_model_matches_the_constants_in_triton_s_lowering(self):
        # Read from the LLVM IR of a cached gfx1100 stage kernel (2026-11-24):
        # the seven subnormal selects, the NaN select and the normal formula.
        lowered = {1: 6144, 2: 7168, 3: 7680, 4: 8192, 5: 8448, 6: 8704, 7: 8960}
        for code, bits in lowered.items():
            self.assertEqual(fx.stock_bits(code), bits, hex(code))
        self.assertEqual(fx.stock_bits(0x00), 0x0000)
        self.assertEqual(fx.stock_bits(0x80), 0x8000)
        self.assertEqual(fx.stock_bits(0x7F), 0x7E00)
        self.assertEqual(fx.stock_bits(0xFF), 0xFE00)
        for code in range(8, 0x7F):
            self.assertEqual(fx.stock_bits(code), (code << 7) + 0x2000, hex(code))

    def test_every_finite_code_is_exact_in_fp16(self):
        for code in range(256):
            value = fx.e4m3fn_value(code)
            if not math.isnan(value):
                self.assertEqual(fx.f16_value(fx.stock_bits(code)), value, hex(code))


class Variants(unittest.TestCase):
    def test_exact_and_lut_are_bit_identical_to_stock(self):
        for variant in ("exact", "lut"):
            self.assertEqual(fx.differing_codes(variant), [], variant)
        self.assertEqual(fx.lut_table(), [fx.stock_bits(c) for c in range(256)])

    def test_exact_nonan_differs_only_on_the_two_nan_codes(self):
        self.assertEqual(fx.differing_codes("exact_nonan"), [0x7F, 0xFF])
        self.assertEqual(fx.f16_value(fx.exact_nonan_bits(0x7F)), 480.0)
        self.assertEqual(fx.f16_value(fx.exact_nonan_bits(0xFF)), -480.0)

    def test_approx_differs_only_at_exponent_zero_and_nan(self):
        want = sorted(
            [code for code in range(256) if (code >> 3) & 0xF == 0] + [0x7F, 0xFF]
        )
        self.assertEqual(fx.differing_codes("approx"), want)
        self.assertEqual(len(want), 18)
        self.assertEqual(fx.max_abs_error("approx"), 2.0**-7)
        # Zero is the worst case: it becomes +-2**-7, keeping its sign.
        self.assertEqual(fx.f16_value(fx.approx_bits(0x00)), 2.0**-7)
        self.assertEqual(fx.f16_value(fx.approx_bits(0x80)), -(2.0**-7))
        self.assertEqual(fx.f16_value(fx.approx_bits(0x07)), 2.0**-7 * 1.875)

    def test_helpers_carry_the_constants_their_models_use(self):
        for variant in ("exact", "exact_nonan"):
            self.assertIn("mag < 8", fx.HELPERS[variant])
            self.assertIn("0.001953125", fx.HELPERS[variant])  # 2**-9
        self.assertEqual(0.001953125, fx.SUBNORMAL_STEP)
        self.assertIn("0x7E00", fx.HELPERS["exact"])
        self.assertNotIn("0x7E00", fx.HELPERS["exact_nonan"])
        self.assertNotIn("tl.where", fx.HELPERS["approx"])
        for variant in ("exact", "exact_nonan", "approx"):
            self.assertIn("(mag << 7) + 0x2000", fx.HELPERS[variant])
            self.assertIn("bitcast=True", fx.HELPERS[variant])


class Rewriter(unittest.TestCase):
    def test_stock_is_the_source_unchanged(self):
        self.assertEqual(fx.rewrite_source(FIXTURE, "stock"), FIXTURE)

    def test_every_variant_rewrites_to_valid_python(self):
        for variant in fx.VARIANTS[1:]:
            out = fx.rewrite_source(FIXTURE, variant)
            ast.parse(out)
            self.assertNotIn("k.to(q.dtype), scores", out, variant)
            self.assertNotIn("v_low.to(prob.dtype), acc_low", out, variant)
            self.assertEqual(out.count(f"def {fx.HELPER_NAME}("), 1, variant)
            # The helper is defined before the kernel that calls it.
            self.assertLess(
                out.index(f"def {fx.HELPER_NAME}("),
                out.index("def _segmented_attention_stage("),
            )

    def test_fp16_paths_keep_their_original_conversions(self):
        out = fx.rewrite_source(FIXTURE, "exact")
        self.assertIn("k_f16 = k.to(q.dtype)", out)
        self.assertIn("v_low_f16 = v_low.to(prob.dtype)", out)
        self.assertIn("v_high_f16 = v_high.to(prob.dtype)", out)
        self.assertIn("acc = tl.dot(p.to(Q.dtype.element_ty), v, acc)", out)
        self.assertEqual(out.count(f"{fx.HELPER_NAME}(v).to(Q.dtype.element_ty)"), 2)
        self.assertIn(f"k_f16 = {fx.HELPER_NAME}(k).to(q.dtype)", out)

    def test_only_the_lut_variant_changes_the_kernel_signature(self):
        for variant in ("exact", "exact_nonan", "approx"):
            out = fx.rewrite_source(FIXTURE, variant)
            self.assertNotIn("FXLUT", out, variant)
            self.assertNotIn("_fx_lut", out, variant)
        out = fx.rewrite_source(FIXTURE, "lut")
        self.assertIn("    OUT,\n    FXLUT,\n    Q0: tl.constexpr,\n", out)
        self.assertIn("        out,\n        _fx_lut(q.device),\n", out)
        self.assertIn(f"{fx.HELPER_NAME}(k, FXLUT)", out)
        # The reduce kernel has an `OUT,` parameter too and must not gain one.
        reduce = out[out.index("def _segmented_attention_reduce(") :]
        self.assertNotIn("FXLUT", reduce.split("def _launch_segmented_attention")[0])
        namespace = {}
        exec(  # the table literal alone, without torch
            out[out.index("_FX_LUT_INT16 = ") : out.index("_fx_lut_cache")], namespace
        )
        unsigned = [bits & 0xFFFF for bits in namespace["_FX_LUT_INT16"]]
        self.assertEqual(unsigned, fx.lut_table())

    def test_a_changed_source_is_refused(self):
        for missing in (
            "k.to(q.dtype), scores",
            "v_low.to(prob.dtype)",
            "v.to(Q.dtype.element_ty), acc",
            "def _segmented_attention_stage(",
        ):
            with self.assertRaises(fx.RewriteError, msg=missing):
                fx.rewrite_source(FIXTURE.replace(missing, "CHANGED"), "exact")
        with self.assertRaises(fx.RewriteError):
            fx.rewrite_source(FIXTURE.replace("        q.stride(0),\n", ""), "lut")
        with self.assertRaises(ValueError):
            fx.rewrite_source(FIXTURE, "fastest")

    def test_known_source_hash_is_a_full_sha256(self):
        for digest in fx.KNOWN_SOURCES:
            self.assertEqual(len(digest), 64)
            int(digest, 16)


if __name__ == "__main__":
    unittest.main()
