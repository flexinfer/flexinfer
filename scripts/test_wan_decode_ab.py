#!/usr/bin/env python3
"""S9 Wan harness (scripts/wan-decode-ab.py): metrics, verdict arithmetic and
collect over synthetic runs. The verdict tests are pure Python; the metric and
collect tests need numpy and skip without it.

Run: python3 scripts/test_wan_decode_ab.py
"""

import copy
import importlib.util
import json
import math
import tempfile
import unittest
from pathlib import Path
from unittest import mock

spec = importlib.util.spec_from_file_location(
    "wanab", Path(__file__).with_name("wan-decode-ab.py")
)
wanab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(wanab)

try:
    import numpy as np
except ImportError:  # the CI image is python:3.12-slim
    np = None


def clip(
    cid,
    total,
    decode,
    denoise=None,
    shape=(33, 480, 832, 3),
    nan=0,
    rss=20000.0,
    since=None,
):
    return {
        "id": cid,
        "timings": {
            "total_seconds": total,
            "decode_postprocess_seconds": decode,
            "denoise_seconds": denoise if denoise is not None else total - decode - 1,
        },
        "nan_count": nan,
        "frames_shape": list(shape),
        "maxrss_mib": rss,
        "since_t0_seconds": since,
    }


def t2v_runs():
    a = {
        "label": "t2v-A",
        "maxrss_mib": 21000.0,
        "clips": [clip("c0", 300, 7.0), clip("c1", 124, 6.4), clip("c2", 126, 6.6)],
    }
    b = {
        "label": "t2v-B",
        "vae_decode_qualname": "_without_miopen.<locals>.call",
        "maxrss_mib": 22000.0,
        "clips": [clip("c0", 320, 7.5), clip("c1", 130, 7.0), clip("c2", 131, 7.2)],
    }
    stats = {
        "frames": 33,
        "height": 480,
        "width": 832,
        "luma_mean_min": 60.0,
        "luma_mean_max": 140.0,
        "luma_std_min": 30.0,
    }
    sims = {
        "stats": {
            f"{r}-{c}": dict(stats)
            for r in ("t2v-A", "t2v-B")
            for c in ("c0", "c1", "c2")
        },
        "decode_equivalence": {"psnr_mean": 41.0, "psnr_min": 38.0, "ssim_mean": 0.995},
        "a_decode_only_vs_pipeline": {"identical": True, "psnr_mean": math.inf},
        "e2e_c1": {"psnr_mean": 27.0, "ssim_mean": 0.9},
        "e2e_c2": {"psnr_mean": 26.0, "ssim_mean": 0.88},
    }
    host = {
        "b_bypass_line": wanab.BYPASS_LINE + " (naive 3D conv kernels)",
        "b_cold_clip_seconds": 240.0,
        "b_first_cold_seconds": 600.0,
        "a_vram_peak_mib": 12000.0,
        "b_vram_peak_mib": 12500.0,
        "ffprobe": {
            f"{r}-{c}": ["h264", 33]
            for r in ("t2v-A", "t2v-B")
            for c in ("c0", "c1", "c2")
        },
        "kernel_error_lines": 0,
    }
    return a, b, host, sims


def failing(result):
    return [b["bar"] for b in result["bars"] + result["validity"] if not b["pass"]]


class VerdictT2VTest(unittest.TestCase):
    def test_passing_run(self):
        result = wanab.verdict_t2v(*t2v_runs())
        self.assertEqual(failing(result), [])
        self.assertEqual(result["decision"], "pass")

    def test_slow_decode_fails_bar_2(self):
        a, b, host, sims = t2v_runs()
        for c in b["clips"]:
            c["timings"]["decode_postprocess_seconds"] = 12.0
        result = wanab.verdict_t2v(a, b, host, sims)
        self.assertEqual(failing(result), ["2 warm decode"])
        self.assertEqual(result["decision"], "fail")

    def test_decode_over_15_seconds_fails_even_within_1_5x(self):
        a, b, host, sims = t2v_runs()
        for run in (a, b):
            for c in run["clips"]:
                c["timings"]["decode_postprocess_seconds"] = 16.0 if run is b else 11.0
        result = wanab.verdict_t2v(a, b, host, sims)
        self.assertIn("2 warm decode", failing(result))

    def test_total_bar_cold_and_memory_bars(self):
        a, b, host, sims = t2v_runs()
        for c in b["clips"]:
            c["timings"]["total_seconds"] = 160.0
        host.update(
            b_cold_clip_seconds=271.0,
            b_first_cold_seconds=901.0,
            b_vram_peak_mib=13100.0,
        )
        b["clips"][1]["maxrss_mib"] = 41000.0
        result = wanab.verdict_t2v(a, b, host, sims)
        self.assertEqual(
            failing(result),
            [
                "3 warm total",
                "4 cold clip, warm compile cache",
                "5 first-ever cold clip",
                "6 peak card VRAM",
                "7 B peak RSS",
            ],
        )

    def test_missing_bypass_or_bad_output_fails(self):
        a, b, host, sims = t2v_runs()
        host["b_bypass_line"] = None
        host["ffprobe"]["t2v-B-c2"] = ["hevc", 33]
        sims["stats"]["t2v-A-c1"]["luma_std_min"] = 1.0
        result = wanab.verdict_t2v(a, b, host, sims)
        self.assertEqual(failing(result), ["1 bypass active on B", "8 outputs valid"])
        self.assertEqual(result["bars"][7]["value"], ["t2v-A-c1", "t2v-B-c2"])

    def test_e2e_miss_alone_is_conditional(self):
        a, b, host, sims = t2v_runs()
        sims["e2e_c2"] = {"psnr_mean": 19.0, "ssim_mean": 0.7}
        result = wanab.verdict_t2v(a, b, host, sims)
        self.assertEqual(failing(result), ["10 end-to-end same seed"])
        self.assertEqual(result["decision"], "conditional")

    def test_decode_equivalence_miss_is_a_fail(self):
        a, b, host, sims = t2v_runs()
        sims["decode_equivalence"]["psnr_min"] = 31.0
        result = wanab.verdict_t2v(a, b, host, sims)
        self.assertEqual(result["decision"], "fail")

    def test_harness_validity_gates_the_verdict(self):
        a, b, host, sims = t2v_runs()
        for c in a["clips"]:
            c["timings"]["total_seconds"] = 200.0
        result = wanab.verdict_t2v(a, b, host, sims)
        self.assertEqual(result["decision"], "report-not-judged")


class VerdictOtherRowsTest(unittest.TestCase):
    def vace_runs(self):
        a = {"label": "vace-A", "clips": [clip("v0", 700, 380), clip("v1", 600, 375)]}
        b = {
            "label": "vace-B",
            "maxrss_mib": 25000.0,
            "clips": [clip("v0", 650, 300), clip("v1", 560, 290)],
        }
        stats = {"luma_mean_min": 50.0, "luma_mean_max": 150.0, "luma_std_min": 20.0}
        sims = {
            "stats": {
                f"{r}-{c}": stats for r in ("vace-A", "vace-B") for c in ("v0", "v1")
            }
        }
        host = {
            "b_vace_keep_line": wanab.FP32_KEEP_LINE,
            "b_vace_vram_peak_mib": 15000.0,
            "ffprobe": {
                f"{r}-{c}": ["h264", 33]
                for r in ("vace-A", "vace-B")
                for c in ("v0", "v1")
            },
            "kernel_error_lines": 0,
        }
        return a, b, host, sims

    def test_vace_pass_and_slow_fail(self):
        a, b, host, sims = self.vace_runs()
        self.assertEqual(wanab.verdict_vace(a, b, host, sims)["decision"], "pass")
        b["clips"][1]["timings"]["total_seconds"] = 760.0
        self.assertEqual(
            failing(wanab.verdict_vace(a, b, host, sims)), ["12b warm total"]
        )

    def test_vace_vae_variant(self):
        v0 = {"clips": [clip("v1", 560, 290)]}
        variant = {"clips": [clip("v1", 300, 20)], "decodes": [{"nan_count": 0}]}
        sims = {"decode_bf16": {"psnr_mean": 36.0, "psnr_min": 33.0, "ssim_mean": 0.97}}
        host = {"bf16_vram_peak_mib": 14000.0, "kernel_error_lines": 0}
        self.assertEqual(
            wanab.verdict_vace_vae(v0, variant, host, sims, "bf16")["decision"], "pass"
        )
        sims["decode_bf16"]["ssim_mean"] = 0.94
        variant["clips"][0]["timings"]["decode_postprocess_seconds"] = 80.0
        self.assertEqual(
            failing(wanab.verdict_vace_vae(v0, variant, host, sims, "bf16")),
            ["K1 decode", "K3 decode quality (same latents as V0)"],
        )

    def test_cache(self):
        base = {
            "clips": [clip("c1", 130, 7, denoise=115), clip("c2", 131, 7, denoise=116)]
        }
        cached = {
            "clips": [clip("c1", 80, 7, denoise=70), clip("c2", 82, 7, denoise=72)]
        }
        sims = {
            "cache_fbc_c1": {"psnr_mean": 30.0, "ssim_mean": 0.93, "ssim_min": 0.9},
            "cache_fbc_c2": {"psnr_mean": 29.0, "ssim_mean": 0.92, "ssim_min": 0.88},
        }
        self.assertEqual(
            wanab.verdict_cache(base, cached, sims, "fbc")["decision"], "pass"
        )
        sims["cache_fbc_c2"]["ssim_min"] = 0.8
        self.assertEqual(
            failing(wanab.verdict_cache(base, cached, sims, "fbc")),
            ["C2 quality vs uncached, same seed"],
        )
        cached["clips"][0]["timings"]["denoise_seconds"] = 110
        self.assertIn(
            "C1 warm denoise", failing(wanab.verdict_cache(base, cached, sims, "fbc"))
        )

    def test_audio(self):
        def render(rid, total):
            return {
                "id": rid,
                "timings": {"total_seconds": total},
                "duration": 20.0,
                "seconds_requested": 20.0,
                "nan_count": 0,
                "maxrss_mib": 9000.0,
            }

        a = {"clips": [render("a0", 60), render("a1", 40)]}
        b = {"clips": [render("a0", 70), render("a1", 45)]}
        host = {
            "b_sa_scheduler_line": "Scheduler: CosineDPMSolverMultistepScheduler",
            "b_sa_dummy_errors": 0,
            "b_sa_vram_peak_mib": 5000.0,
            "kernel_error_lines": 0,
        }
        self.assertEqual(wanab.verdict_audio(a, b, host)["decision"], "pass")
        b["clips"][1]["duration"] = 19.5
        host["b_sa_dummy_errors"] = 1
        self.assertEqual(
            failing(wanab.verdict_audio(a, b, host)),
            [
                "SA2 duration matches the request",
                "SA3 scheduler loads, no dummy-object error",
            ],
        )


@unittest.skipIf(np is None, "numpy not installed")
class MetricsTest(unittest.TestCase):
    def frames(self, seed=0, n=3):
        rng = np.random.default_rng(seed)
        base = rng.integers(0, 256, size=(n, 48, 64, 3)).astype(np.uint8)
        # Smooth it so SSIM has structure to measure.
        return (
            (
                base.astype(np.float64)
                + np.roll(base, 1, axis=2)
                + np.roll(base, 1, axis=1)
            )
            / 3
        ).astype(np.uint8)

    def test_identical_frames(self):
        f = self.frames()
        result = wanab.compare_frames(f, f.copy())
        self.assertTrue(result["identical"])
        self.assertAlmostEqual(result["ssim_mean"], 1.0, places=6)

    def test_small_noise_scores_high_and_shuffle_scores_low(self):
        f = self.frames()
        rng = np.random.default_rng(1)
        noisy = np.clip(
            f.astype(np.int16) + rng.integers(-2, 3, size=f.shape), 0, 255
        ).astype(np.uint8)
        close = wanab.compare_frames(f, noisy)
        self.assertGreater(close["psnr_mean"], 40)
        self.assertGreater(close["ssim_mean"], 0.95)
        other = wanab.compare_frames(f, self.frames(seed=7))
        self.assertLess(other["psnr_mean"], 20)
        self.assertLess(other["ssim_mean"], 0.5)

    def test_psnr_formula(self):
        a = np.zeros((4, 4, 3), dtype=np.uint8)
        b = np.full((4, 4, 3), 10, dtype=np.uint8)
        self.assertAlmostEqual(
            wanab.psnr(a, b), 10 * math.log10(255**2 / 100), places=6
        )

    def test_shape_mismatch(self):
        self.assertEqual(
            wanab.compare_frames(self.frames(n=2), self.frames(n=3))["error"],
            "shape mismatch",
        )

    def test_collect_builds_host_and_sims(self):
        a, b, _, _ = t2v_runs()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            f = self.frames(n=2)
            for run in (a, b):
                d = root / run["label"]
                d.mkdir()
                for c in run["clips"]:
                    c["frames_file"] = f"{run['label']}-{c['id']}.frames.npz"
                    c["mp4_file"] = f"{run['label']}-{c['id']}.mp4"
                    np.savez_compressed(d / c["frames_file"], frames=f)
                (d / f"{run['label']}.json").write_text(json.dumps(run))
            b["decodes"] = [
                {"id": "t2v-B-xA", "nan_count": 0, "frames_file": "t2v-B-xA.frames.npz"}
            ]
            np.savez_compressed(root / "t2v-B" / "t2v-B-xA.frames.npz", frames=f)
            (root / "t2v-B" / "t2v-B.json").write_text(json.dumps(b))
            (root / "t2v-B" / "run.log").write_text(
                "x\n" + wanab.BYPASS_LINE + " (naive)\n"
            )
            (root / "baseline-t2v-B.txt").write_text(str(512 * wanab.MIB))
            (root / "vram-t2v-B.tsv").write_text(
                f"1.0 {600 * wanab.MIB}\n2.0 {9000 * wanab.MIB}\nbad\n"
            )
            (root / "kernel-t2v-B.log").write_text(
                "amdgpu: ring gfx timeout\nnothing here\n"
            )
            with mock.patch.object(wanab, "_ffprobe", return_value=["h264", 33]):
                result = wanab.collect(root)
        host, sims = result["host"], result["sims"]
        self.assertEqual(host["b_vram_peak_mib"], 8488.0)
        self.assertTrue(host["b_bypass_line"].startswith(wanab.BYPASS_LINE))
        self.assertEqual(host["kernel_error_lines"], 1)
        self.assertTrue(sims["decode_equivalence"]["identical"])
        self.assertTrue(sims["e2e_c1"]["identical"])
        self.assertIn("t2v", result["verdicts"])
        self.assertEqual(host["ffprobe"]["t2v-A-c1"], ["h264", 33])

    def test_collect_judges_fix_variant_and_its_cache_arm(self):
        a, b, _, _ = t2v_runs()
        tile = {**b, "label": "t2v-B-tile"}
        cached = {**b, "label": "cache-B-fbc005-fix"}
        crashed = {"label": "t2v-B", "error": "OutOfMemoryError", "clips": []}
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            f = self.frames(n=2)
            for run in (a, tile, cached, crashed):
                d = root / run["label"]
                d.mkdir()
                run["clips"] = [dict(c) for c in run["clips"]]
                for c in run["clips"]:
                    c["frames_file"] = f"{run['label']}-{c['id']}.frames.npz"
                    np.savez_compressed(d / c["frames_file"], frames=f)
                (d / f"{run['label']}.json").write_text(json.dumps(run))
            a["decodes"] = [{"id": "own", "frames_file": "t2v-A-own.frames.npz"}]
            np.savez_compressed(root / "t2v-A" / "t2v-A-own.frames.npz", frames=f)
            (root / "t2v-A" / "t2v-A.json").write_text(json.dumps(a))
            tile["decodes"] = [{"id": "xA", "frames_file": "t2v-B-tile-xA.frames.npz"}]
            np.savez_compressed(
                root / "t2v-B-tile" / "t2v-B-tile-xA.frames.npz", frames=f
            )
            (root / "t2v-B-tile" / "t2v-B-tile.json").write_text(json.dumps(tile))
            (root / "t2v-B-tile" / "run.log").write_text(wanab.BYPASS_LINE + "\n")
            with mock.patch.object(wanab, "_ffprobe", return_value=["h264", 33]):
                result = wanab.collect(root)
        sims, verdicts = result["sims"], result["verdicts"]
        self.assertTrue(sims["t2v-B-tile:decode_equivalence"]["identical"])
        self.assertEqual(sims["t2v-B-tile:e2e_c1"]["b"], "t2v-B-tile-c1")
        self.assertEqual(sims["cache_fbc005-fix_c1"]["a"], "t2v-B-tile-c1")
        self.assertIn("t2v-B-tile", verdicts)
        self.assertIn("cache-fbc005-fix", verdicts)
        self.assertEqual(verdicts["t2v"]["decision"], "fail")


class ClipSpecTest(unittest.TestCase):
    def test_parse(self):
        self.assertEqual(
            wanab._parse_clip("c1:boat:42"), {"id": "c1", "prompt": "boat", "seed": 42}
        )
        self.assertEqual(
            wanab._parse_clip("c1:boat:42:kf.png")["keyframe_out"], "kf.png"
        )
        with self.assertRaises(Exception):
            wanab._parse_clip("c1:unknown:42")


if __name__ == "__main__":
    unittest.main(verbosity=2)
