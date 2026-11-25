"""The fp8 attention microbench's arithmetic and inputs, without a GPU."""

import argparse
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location(
    "fp8bench", Path(__file__).with_name("bench-gfx1100-fp8-attention.py")
)
bench = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bench)

try:
    import torch
except ImportError:  # the pure tests still run
    torch = None


class Arithmetic(unittest.TestCase):
    def test_int_lists(self):
        self.assertEqual(bench.parse_int_list("16384,65536"), [16384, 65536])
        for bad in ("", "0", "8,-1"):
            with self.assertRaises(argparse.ArgumentTypeError):
                bench.parse_int_list(bad)

    def test_kv_bytes_is_k_plus_v_for_one_layer(self):
        # 4 KV heads x 256 dims, K and V.
        self.assertEqual(bench.kv_bytes(1000, 2), 1000 * 4 * 256 * 2 * 2)
        self.assertEqual(bench.kv_bytes(1000, 1) * 2, bench.kv_bytes(1000, 2))
        self.assertEqual(bench.gb_per_s(2_000_000_000, 4.0), 500.0)
        self.assertIsNone(bench.gb_per_s(1, 0))

    def test_a_step_is_sixteen_target_layers_and_two_draft_passes(self):
        self.assertEqual(bench.step_attention_ms({1: 1.0, 3: 2.0}), 16 * 2.0 + 2 * 1.0)
        self.assertIsNone(bench.step_attention_ms({3: 2.0}))


class Verdict(unittest.TestCase):
    def test_pass_needs_an_exact_variant_within_the_bar_at_every_long_context(self):
        got = bench.verdict(
            {
                16384: {"fp16": 1.0, "stock": 9.0, "exact": 8.0},  # below 64K: ignored
                65536: {"fp16": 4.0, "stock": 30.0, "exact": 7.0, "lut": 9.0},
                122880: {"fp16": 8.0, "stock": 60.0, "exact": 15.0, "lut": 14.0},
            }
        )
        self.assertTrue(got["pass"])
        self.assertEqual(sorted(got["by_context"]), [65536, 122880])
        self.assertEqual(got["by_context"][65536]["best_exact"], "exact")
        self.assertEqual(got["by_context"][65536]["ratio_to_fp16"], 1.75)
        self.assertEqual(got["by_context"][65536]["stock_ratio_to_fp16"], 7.5)
        self.assertEqual(got["by_context"][122880]["best_exact"], "lut")

    def test_one_long_context_over_the_bar_fails(self):
        got = bench.verdict(
            {
                65536: {"fp16": 4.0, "stock": 30.0, "exact": 7.0},
                122880: {"fp16": 8.0, "stock": 60.0, "exact": 17.0},
            }
        )
        self.assertFalse(got["pass"])
        self.assertTrue(got["by_context"][65536]["pass"])
        self.assertFalse(got["by_context"][122880]["pass"])

    def test_the_approximate_variant_never_counts(self):
        got = bench.verdict({65536: {"fp16": 4.0, "stock": 30.0, "approx": 4.5}})
        self.assertFalse(got["pass"])
        self.assertIn("missing", got["by_context"][65536]["reason"])

    def test_no_long_context_is_not_a_pass(self):
        self.assertFalse(bench.verdict({16384: {"fp16": 1.0, "exact": 1.0}})["pass"])


@unittest.skipIf(torch is None, "torch not installed")
class Inputs(unittest.TestCase):
    def test_inputs_have_the_shapes_the_kernel_launcher_checks(self):
        got = bench.build_inputs(torch, 2000, 3, 832, 0.5, 7, device="cpu")
        k8, v8 = got["fp8"]
        k16, v16 = got["fp16"]
        self.assertEqual(tuple(k8.shape), (3, 832, 4, 256))  # ceil(2000 / 832) blocks
        self.assertEqual(k8.dtype, torch.float8_e4m3fn)
        self.assertEqual(k16.dtype, torch.float16)
        self.assertEqual(v8.shape, k8.shape)
        self.assertEqual(tuple(got["q"].shape), (3, 24, 256))
        self.assertEqual(got["q"].dtype, torch.float16)
        self.assertEqual(got["table"].dtype, torch.int32)
        self.assertEqual(tuple(got["table"].shape), (1, 3))
        self.assertEqual(got["starts"].tolist(), [0, 3])
        self.assertEqual(got["lengths"].tolist(), [2000])
        self.assertEqual(got["scale"].dtype, torch.float32)
        # The fp16 cache is exactly what the fp8 cache decodes to.
        self.assertTrue(torch.equal(k8.to(torch.float16), k16))
        # With half the elements made small, exponent-0 codes are common.
        self.assertGreater(got["codes_exp0"] / got["codes_total"], 0.3)


if __name__ == "__main__":
    unittest.main()
