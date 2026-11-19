#!/usr/bin/env python3
"""Render gate: scoring on small synthetic images and one run against a local
stub endpoint. Needs Pillow (the script itself does); skips without it.

Run: python3 scripts/test_render_gate.py
"""

import base64
import contextlib
import importlib.util
import io
import json
import os
import random
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

try:
    from PIL import Image
except ImportError:  # pragma: no cover - CI images without Pillow
    Image = None

gate = None
if Image is not None:
    spec = importlib.util.spec_from_file_location(
        "render_gate", Path(__file__).with_name("render-gate.py")
    )
    gate = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(gate)

SIZE = 256  # 16 blocks of 64x64


def sound(seed=0, mode="RGB", size=SIZE):
    """A textured image: gradients plus per-pixel noise, like a decoded render."""
    rng = random.Random(seed)
    image = Image.new("RGB", (size, size))
    image.putdata(
        [
            (
                min(255, x + rng.randrange(40)),
                min(255, y + rng.randrange(40)),
                min(255, (x + y) // 2 + rng.randrange(40)),
            )
            for y in range(size)
            for x in range(size)
        ]
    )
    if mode == "RGBA":
        image.putalpha(255)
    return image


def tiled_garbage(size=SIZE):
    """The 2026-11-19 shape: a transparent canvas with a few flat colour tiles
    and one noise tile."""
    image = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    image.paste((128, 128, 192, 255), (0, 0, 64, 64))
    image.paste((128, 192, 192, 255), (64, 64, 128, 128))
    noise = Image.effect_noise((64, 64), 120).convert("RGBA")
    image.paste(noise, (128, 128))
    return image


def save(image, directory, name):
    path = os.path.join(directory, name)
    image.save(path, format="PNG")
    return path


def png_bytes(image):
    buffer = io.BytesIO()
    image.save(buffer, format="PNG")
    return buffer.getvalue()


@unittest.skipUnless(Image is not None, "needs Pillow")
class ScoreTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def score(self, image, **kwargs):
        return gate.score_image(save(image, self.dir, "x.png"), **kwargs)

    def failed(self, checks):
        return [k for k, v in checks.items() if v is False]

    def test_sound_rgb_and_rgba_pass(self):
        for mode in ("RGB", "RGBA"):
            checks, metrics = self.score(sound(mode=mode), expected_size="256x256")
            self.assertEqual(self.failed(checks), [], (mode, metrics))
            self.assertEqual(metrics["flat_tile_share"], 0.0)

    def test_require_rgb_rejects_alpha(self):
        checks, _ = self.score(sound(mode="RGBA"), require_rgb=True)
        self.assertEqual(self.failed(checks), ["mode"])

    def test_tiled_garbage_fails_on_transparency_and_flat_tiles(self):
        checks, metrics = self.score(tiled_garbage())
        self.assertIn("opaque", self.failed(checks))
        self.assertIn("flat_tiles", self.failed(checks))
        self.assertGreater(metrics["flat_tile_share"], 0.9)

    def test_opaque_flat_tiles_fail_even_without_alpha(self):
        image = tiled_garbage().convert("RGB")  # transparent area becomes black
        checks, _ = self.score(image)
        self.assertIn("flat_tiles", self.failed(checks))
        self.assertNotIn("opaque", self.failed(checks))

    def test_a_flat_backdrop_under_a_quarter_passes(self):
        image = sound()
        image.paste((240, 240, 240), (0, 0, 192, 64))  # 3 of 16 blocks flat
        checks, metrics = self.score(image)
        self.assertEqual(metrics["flat_tile_share"], 3 / 16)
        self.assertTrue(checks["flat_tiles"])

    def test_mean_and_std_thresholds(self):
        checks, _ = self.score(Image.new("RGB", (SIZE, SIZE), (2, 2, 2)))
        self.assertFalse(checks["channel_mean"])
        self.assertFalse(checks["channel_std"])

    def test_wrong_size_and_undecodable(self):
        checks, _ = self.score(sound(size=128), expected_size="256x256")
        self.assertEqual(self.failed(checks), ["size"])
        bad = os.path.join(self.dir, "bad.png")
        with open(bad, "wb") as f:
            f.write(b"not a png")
        checks, metrics = gate.score_image(bad)
        self.assertFalse(checks["decodes"])
        self.assertIn("decode_error", metrics)

    def test_seeds(self):
        self.assertEqual(gate.seeds_for(10, 4), [10, 11, 12, 10])
        self.assertEqual(gate.seeds_for(10, 2), [10, 10])
        with self.assertRaises(ValueError):
            gate.seeds_for(10, 1)


@unittest.skipUnless(Image is not None, "needs Pillow")
class VerdictTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp()

    def report(self, images, statuses=None):
        renders = []
        for i, (seed, image) in enumerate(images, start=1):
            entry = {"i": i, "seed": seed, "status": (statuses or {}).get(i, 200)}
            if image is not None:
                entry["file"] = f"r{i}.png"
                save(image, self.dir, entry["file"])
            renders.append(entry)
        return {"args": {"size": "256x256"}, "renders": renders}

    def test_sound_run_passes(self):
        report = self.report(
            [(1, sound(0)), (2, sound(1)), (3, sound(2)), (1, sound(0))]
        )
        ok, result = gate.verdict(report, self.dir)
        self.assertTrue(ok, result)
        self.assertEqual(result["same_seed"][0]["identical"], True)

    def test_garbage_first_render_fails_the_run(self):
        report = self.report(
            [(1, tiled_garbage()), (2, sound(1)), (3, sound(2)), (1, sound(0))]
        )
        ok, result = gate.verdict(report, self.dir)
        self.assertFalse(ok)
        self.assertEqual(result["images"][0]["failed"], ["opaque", "flat_tiles"])
        self.assertEqual(result["same_seed"][0]["pass"], False)

    def test_same_seed_tolerates_two_levels_not_three(self):
        base = sound(0)
        for delta, want in ((2, True), (3, False)):
            shifted = base.point(lambda v, d=delta: min(255, v + d) if v < 200 else v)
            report = self.report([(1, base), (2, sound(1)), (1, shifted)])
            ok, result = gate.verdict(report, self.dir)
            self.assertEqual(result["same_seed"][0]["max_diff"], delta)
            self.assertEqual(ok, want, result["same_seed"])

    def test_http_error_fails(self):
        report = self.report([(1, sound(0)), (2, None), (1, sound(0))], {2: 500})
        ok, result = gate.verdict(report, self.dir)
        self.assertFalse(ok)
        self.assertEqual(result["images"][1]["checks"]["http_200"], False)


class _Stub(BaseHTTPRequestHandler):
    """Returns tiled garbage for the first request, then a seed-keyed sound image."""

    seen = []

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        _Stub.seen.append(body)
        image = tiled_garbage() if len(_Stub.seen) == 1 else sound(body["seed"])
        raw = json.dumps(
            {"data": [{"b64_json": base64.b64encode(png_bytes(image)).decode()}]}
        ).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def log_message(self, *args):
        pass


@unittest.skipUnless(Image is not None, "needs Pillow")
class RunTest(unittest.TestCase):
    def test_run_against_a_stub_endpoint(self):
        server = ThreadingHTTPServer(("127.0.0.1", 0), _Stub)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)  # cleanups run last-in first-out
        out = tempfile.mkdtemp()
        args = type(
            "Args",
            (),
            dict(
                url=f"http://127.0.0.1:{server.server_address[1]}",
                model="m",
                prompt="p",
                size="256x256",
                seed=7,
                renders=4,
                require_rgb=False,
                auth="",
                timeout=10,
                out_dir=out,
            ),
        )()
        with contextlib.redirect_stdout(io.StringIO()):
            report = gate.run(args)
        self.assertEqual([b["seed"] for b in _Stub.seen], [7, 8, 9, 7])
        self.assertEqual(_Stub.seen[0]["response_format"], "b64_json")
        self.assertTrue(os.path.exists(os.path.join(out, "r1-seed7.png")))
        ok, result = gate.verdict(report, out)
        self.assertFalse(ok)
        self.assertEqual(result["images"][0]["failed"], ["opaque", "flat_tiles"])
        self.assertTrue(all(i["pass"] for i in result["images"][1:]))


if __name__ == "__main__":
    unittest.main(verbosity=2)
