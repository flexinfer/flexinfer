"""KT-RERANK harness: the pure parts, and the copy embedded in the canary manifest."""

import importlib.util
from pathlib import Path
import unittest

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("ktrerank", HERE / "kt-rerank-batch.py")
kt = importlib.util.module_from_spec(spec)
spec.loader.exec_module(kt)

MANIFEST = HERE.parent / "deploy" / "debug" / "kt-rerank-batch-gfx906-canary.yaml"


class Requests(unittest.TestCase):
    def test_requests_are_deterministic_and_shaped(self):
        q1, d1 = kt.make_request(7, 24)
        q2, d2 = kt.make_request(7, 24)
        self.assertEqual((q1, d1), (q2, d2))
        self.assertEqual(len(d1), 24)
        self.assertNotEqual(kt.make_request(8, 24)[1], d1)
        lengths = sorted(len(d.split()) for d in d1)
        # a fifth of the documents are the long ones
        self.assertEqual(lengths.count(150), 24 // 5 + (1 if 24 % 5 > 3 else 0))
        self.assertEqual(lengths[0], 75)

    def test_shapes_cover_the_live_median_and_p95(self):
        self.assertIn(24, [n for n, _ in kt.SHAPES])
        self.assertIn(80, [n for n, _ in kt.SHAPES])


class Parity(unittest.TestCase):
    def test_identical_sets(self):
        a = [[0.1, 2.0, -3.0, 4.5, 0.7, 0.2]]
        self.assertEqual(
            kt.parity(a, a),
            {"max_abs_diff": 0.0, "min_spearman": 1.0, "top5_order_equal": True},
        )

    def test_a_swap_in_the_top_five_is_reported(self):
        a = [[9.0, 8.0, 7.0, 6.0, 5.0, 1.0]]
        b = [[9.0, 7.5, 7.6, 6.0, 5.0, 1.0]]
        got = kt.parity(a, b)
        self.assertFalse(got["top5_order_equal"])
        self.assertAlmostEqual(got["max_abs_diff"], 0.6, places=4)
        self.assertLess(got["min_spearman"], 1.0)

    def test_mismatched_sets_are_not_compared(self):
        self.assertIsNone(kt.parity([[1.0, 2.0]], [[1.0]]))
        self.assertIsNone(kt.parity(None, [[1.0]]))

    def test_spearman_of_a_reversed_list(self):
        self.assertAlmostEqual(kt.spearman([1, 2, 3, 4], [4, 3, 2, 1]), -1.0)


class Arms(unittest.TestCase):
    def test_live_arm_renders_the_live_lane_flags(self):
        name, key, parallel, ctx, batch, ubatch = kt.ARMS[0]
        self.assertEqual(name, "q8-live")
        cmd = kt.server_cmd("/m.gguf", parallel, ctx, batch, ubatch)
        self.assertNotIn("--parallel", cmd)
        tail = cmd[cmd.index("--model") :]
        self.assertEqual(
            tail,
            [
                "--model",
                "/m.gguf",
                "--ctx-size",
                "512",
                "--n-gpu-layers",
                "999",
                "--batch-size",
                "2048",
                "--ubatch-size",
                "512",
                "--reranking",
                "--metrics",
                "-fit",
                "off",
            ],
        )

    def test_wide_arms_keep_512_tokens_per_slot_and_one_ubatch(self):
        for name, key, parallel, ctx, batch, ubatch in kt.ARMS:
            if parallel:
                self.assertEqual(ctx, 512 * parallel, name)
                self.assertEqual(batch, ubatch, name)
                self.assertIn(
                    "--parallel", kt.server_cmd("/m", parallel, ctx, batch, ubatch)
                )

    def test_first_and_last_arms_are_the_same_configuration(self):
        self.assertEqual(kt.ARMS[0][1:], kt.ARMS[-1][1:])


class Rows(unittest.TestCase):
    def test_row_prints_ratios_against_the_reference(self):
        ref = {"shapes": {"24": {"p50_ms": 300.0}}}
        arm = {
            "arm": "q8-w1024",
            "total_slots": 8,
            "n_ctx_slot": 512,
            "shapes": {"24": {"p50_ms": 240.0}},
            "tokens_per_pass": 900.0,
            "vram_peak_mib": 800.0,
            "queues": 3,
            "card_queues_peak": 21,
            "live_gte": {"p95_ms": 20.0},
        }
        line = kt.row(arm, ref)
        self.assertIn("24: 240.0 ms (0.80x)", line)
        self.assertIn("slots 8 ctx/slot 512", line)

    def test_row_of_a_skipped_arm(self):
        self.assertEqual(
            kt.row({"arm": "f16-live", "skipped": "no file"}, None),
            "KT_ROW f16-live | no file",
        )


class Manifest(unittest.TestCase):
    def test_embedded_script_matches_this_file(self):
        text = MANIFEST.read_text()
        start = text.index("  kt-rerank-batch.py: |\n") + len(
            "  kt-rerank-batch.py: |\n"
        )
        end = text.index("\n---\n", start)
        embedded = "\n".join(
            line[4:] if line.startswith("    ") else line
            for line in text[start:end].split("\n")
        )
        source = (HERE / "kt-rerank-batch.py").read_text()
        self.assertEqual(embedded.rstrip("\n"), source.rstrip("\n"))


if __name__ == "__main__":
    unittest.main()
