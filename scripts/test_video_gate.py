#!/usr/bin/env python3
"""Video gate: request shape and run against a local stub endpoint, evidence
parsers and the verdict arithmetic run on the bare interpreter; the decode
case needs numpy, ffmpeg and ffprobe and skips without them.

Run: python3 scripts/test_video_gate.py
"""

import base64
import contextlib
import copy
import importlib.util
import io
import json
import shutil
import subprocess
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).parent


def _load(name, file):
    spec = importlib.util.spec_from_file_location(name, HERE / file)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


gate = _load("video_gate", "video-gate.py")

try:
    import numpy  # noqa: F401

    HAVE_NUMPY = True
except ImportError:  # pragma: no cover - CI images without numpy
    HAVE_NUMPY = False
HAVE_FFMPEG = bool(shutil.which("ffmpeg") and shutil.which("ffprobe"))


def media(**over):
    m = {
        "codec": "h264",
        "frames": 33,
        "width": 832,
        "height": 480,
        "luma_mean_min": 60.0,
        "luma_mean_max": 140.0,
        "luma_std_min": 30.0,
    }
    m.update(over)
    return m


def render(i, clip, decode, total, wall=None, **media_over):
    return {
        "i": i,
        "clip": clip,
        "status": 200,
        "wall_s": wall or total + 1,
        "timings": {"decode_postprocess_seconds": decode, "total_seconds": total},
        "media": media(**media_over),
    }


def good_metrics():
    A = [
        render(1, "c1", 6.0, 250.0),
        render(2, "c2", 6.0, 122.0),
        render(3, "c1", 6.0, 121.0),
    ]
    B = [
        render(1, "c1", 6.2, 66.0, wall=300.0),
        render(2, "c2", 6.1, 62.0),
        render(3, "c1", 6.0, 61.0),
        render(4, "c1", 5.9, 61.5),
    ]
    same = {"identical": True, "psnr_mean": float("inf"), "ssim_mean": 1.0}
    return {
        "A": {"renders": A},
        "B": {"renders": B},
        "cmp": {
            "A_c1_r3": same,
            "B_c1_r3": same,
            "B_c1_r4": same,
            "e2e_c1": {"psnr_mean": 27.5, "ssim_mean": 0.88},
            "e2e_c2": {"psnr_mean": 25.1, "ssim_mean": 0.85},
        },
        "vram": {"A_peak_mib": 24000.0, "B_peak_mib": 24400.0},
        "runtime_peak_gib": 22.0,
        "evidence": {
            "pins": True,
            "bypass_line": True,
            "warning_lines": 0,
            "kernel_lines": 0,
            "root_rw": True,
        },
    }


class PromptsTest(unittest.TestCase):
    def test_prompts_match_s9(self):
        s9 = _load("wan_decode_ab", "wan-decode-ab.py")
        for key, text in gate.PROMPTS.items():
            self.assertEqual(s9.PROMPTS[key], text)

    def test_request_body(self):
        b = gate.request_body("lane-x", "c2")
        self.assertEqual(b["model"], "lane-x")
        self.assertEqual(b["prompt"], gate.PROMPTS["fox"])
        self.assertEqual(
            (b["seed"], b["size"], b["num_frames"], b["num_inference_steps"]),
            (7, "832x480", 33, 20),
        )
        self.assertEqual(b["response_format"], "b64_json")

    def test_sequences(self):
        self.assertEqual(gate.SEQUENCES["A"], ["c1", "c2", "c1"])
        self.assertEqual(gate.SEQUENCES["B"], ["c1", "c2", "c1", "c1"])


class _Stub(BaseHTTPRequestHandler):
    bodies = []
    fail_at = None

    def log_message(self, *a):  # quiet
        pass

    def do_POST(self):  # noqa: N802
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        type(self).bodies.append((self.path, body))
        if type(self).fail_at == len(type(self).bodies):
            self.send_response(503)
            self.end_headers()
            self.wfile.write(b'{"error":{"code":"model_preempted"}}')
            return
        video = f"mp4-{body['seed']}".encode()
        out = {
            "data": [
                {
                    "b64_json": base64.b64encode(video).decode(),
                    "width": 832,
                    "height": 480,
                    "num_frames": 33,
                    "fps": 16,
                    "seed": body["seed"],
                    "timings": {
                        "decode_postprocess_seconds": 6.0,
                        "total_seconds": 60.0,
                    },
                }
            ]
        }
        raw = json.dumps(out).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


@contextlib.contextmanager
def stub_server(fail_at=None):
    _Stub.bodies = []
    _Stub.fail_at = fail_at
    srv = ThreadingHTTPServer(("127.0.0.1", 0), _Stub)
    t = threading.Thread(target=srv.serve_forever, daemon=True)
    t.start()
    try:
        yield f"http://127.0.0.1:{srv.server_address[1]}"
    finally:
        srv.shutdown()
        srv.server_close()


class RunTest(unittest.TestCase):
    def test_arm_b_renders_four_clips(self):
        with stub_server() as url, tempfile.TemporaryDirectory() as d:
            with contextlib.redirect_stdout(io.StringIO()):
                rc = gate.main(
                    [
                        "run",
                        "--url",
                        url,
                        "--model",
                        "lane-x",
                        "--arm",
                        "B",
                        "--out-dir",
                        d,
                    ]
                )
            self.assertEqual(rc, 0)
            self.assertEqual([b["seed"] for _, b in _Stub.bodies], [42, 7, 42, 42])
            self.assertTrue(all(p == "/v1/videos/generations" for p, _ in _Stub.bodies))
            rep = json.loads((Path(d) / "video-gate-B.json").read_text())
            self.assertEqual(
                [r["file"] for r in rep["renders"]],
                ["B-r1-c1.mp4", "B-r2-c2.mp4", "B-r3-c1.mp4", "B-r4-c1.mp4"],
            )
            self.assertEqual((Path(d) / "B-r2-c2.mp4").read_bytes(), b"mp4-7")
            self.assertEqual(rep["renders"][0]["timings"]["total_seconds"], 60.0)
            self.assertLessEqual(rep["t0"], rep["t1"])

    def test_run_stops_on_error(self):
        with stub_server(fail_at=2) as url, tempfile.TemporaryDirectory() as d:
            with contextlib.redirect_stdout(io.StringIO()):
                rc = gate.main(
                    [
                        "run",
                        "--url",
                        url,
                        "--model",
                        "lane-x",
                        "--arm",
                        "A",
                        "--out-dir",
                        d,
                    ]
                )
            self.assertEqual(rc, 1)
            rep = json.loads((Path(d) / "video-gate-A.json").read_text())
            self.assertEqual([r["status"] for r in rep["renders"]], [200, 503])
            self.assertIn("model_preempted", rep["renders"][1]["body"])


class EvidenceTest(unittest.TestCase):
    def test_pins(self):
        ok = (
            "PYTORCH_ALLOC_CONF=garbage_collection_threshold:0.9,max_split_size_mb:512,"
            "expandable_segments:False\0PYTORCH_HIP_ALLOC_CONF=garbage_collection_"
            "threshold:0.9,max_split_size_mb:512,expandable_segments:False\0"
        )
        self.assertTrue(gate.pins_present(ok))
        self.assertFalse(gate.pins_present(ok.replace("HIP_ALLOC_CONF=g", "X=g")))
        self.assertFalse(
            gate.pins_present(
                "PYTORCH_ALLOC_CONF=expandable_segments:True\n"
                "PYTORCH_HIP_ALLOC_CONF=expandable_segments:False\n"
            )
        )

    def test_runtime_log_filters_by_lane(self):
        log = "\n".join(
            [
                '... Wan VAE encode/decode run with MIOpen off {"model": "wan-x"}',
                '... RuntimeWarning: invalid value encountered in cast {"model": "qwen-image"}',
                '... INFO loading shards {"model": "wan-x"}',
                '... nan detected in latents {"model": "wan-x"}',
            ]
        )
        ev = gate.runtime_log_evidence(log, "wan-x")
        self.assertTrue(ev["bypass_line"])
        self.assertEqual(ev["warning_lines"], 1)  # info is not inf, other lane ignored
        self.assertEqual(ev["lane_lines"], 3)

    def test_kernel_lines(self):
        text = (
            "Nov 21 kernel: cni0: port 2(veth1) entered forwarding state\n"
            "Nov 21 kernel: amdgpu 0000:03:00.0: SMU is resuming\n"
            "Nov 21 kernel: ata1.00: failed command: WRITE FPDMA QUEUED\n"
        )
        self.assertEqual(gate.kernel_line_count(text), 2)

    def test_sample_line_and_peak(self):
        self.assertEqual(
            gate.parse_sample_line("100.5 1048576"), {"t": 100.5, "used_mib": 1.0}
        )
        self.assertIsNone(gate.parse_sample_line("card /sys/x 1"))
        with tempfile.NamedTemporaryFile("w", suffix=".jsonl", delete=False) as fh:
            fh.write('{"card": ["/sys/x", "25753026560"]}\n')
            for t, mib in [(1.0, 900.0), (5.0, 24000.0), (9.0, 30000.0)]:
                fh.write(json.dumps({"t": t, "used_mib": mib}) + "\n")
        self.assertEqual(gate.vram_peak(fh.name, 0.0, 6.0), 24000.0)
        self.assertIsNone(gate.vram_peak(fh.name, 20.0, 30.0))


class VerdictTest(unittest.TestCase):
    def test_bar3_psnr_floor_dropped_judges_ssim(self):
        m = good_metrics()
        m["cmp"]["e2e_c1"] = {"identical": False, "psnr_mean": 21.5, "ssim_mean": 0.816}
        self.assertFalse(gate.verdict(m)["bars"][2]["pass"])
        m["e2e_psnr_min"] = 0
        self.assertTrue(gate.verdict(m)["bars"][2]["pass"])
        m["cmp"]["e2e_c1"]["ssim_mean"] = 0.79
        self.assertFalse(gate.verdict(m)["bars"][2]["pass"])

    def test_all_pass(self):
        v = gate.verdict(good_metrics())
        self.assertEqual(v["decision"], "pass", v)
        self.assertEqual(v["failed_bars"], [])

    def test_e2e_alone_is_conditional(self):
        m = good_metrics()
        m["cmp"]["e2e_c2"] = {"psnr_mean": 19.0, "ssim_mean": 0.7}
        v = gate.verdict(m)
        self.assertEqual((v["decision"], v["failed_bars"]), ("conditional", [3]))

    def test_corrupt_later_clip_fails(self):
        # S9's failure: the first VAE use is sound, the later ones are not.
        m = good_metrics()
        m["B"]["renders"][2]["media"]["luma_std_min"] = 0.4
        m["cmp"]["B_c1_r3"] = {"identical": False, "psnr_mean": 9.0, "ssim_mean": 0.1}
        v = gate.verdict(m)
        self.assertEqual(v["decision"], "fail")
        self.assertEqual(v["failed_bars"], [1, 2])

    def test_slow_decode_fails(self):
        m = good_metrics()
        for r in m["B"]["renders"][1:]:
            r["timings"]["decode_postprocess_seconds"] = 16.6  # S9's tiled decode
        v = gate.verdict(m)
        self.assertEqual((v["decision"], v["failed_bars"]), ("fail", [4]))

    def test_short_b_arm_fails(self):
        m = good_metrics()
        m["B"]["renders"] = m["B"]["renders"][:2]
        m["cmp"]["B_c1_r3"] = m["cmp"]["B_c1_r4"] = None
        self.assertEqual(gate.verdict(m)["decision"], "fail")

    def test_missing_evidence_is_incomplete(self):
        m = good_metrics()
        m["vram"] = None
        m["evidence"]["kernel_lines"] = None
        v = gate.verdict(m)
        self.assertEqual((v["decision"], v["not_measured"]), ("incomplete", [7, 10]))

    def test_invalid_control(self):
        m = good_metrics()
        for r in m["A"]["renders"]:
            r["timings"]["total_seconds"] = 200.0  # A outside 105-143 s
        self.assertEqual(gate.verdict(m)["decision"], "invalid")

    def test_vram_and_kernel_bars(self):
        m = copy.deepcopy(good_metrics())
        m["vram"]["B_peak_mib"] = 25100.0
        m["evidence"]["kernel_lines"] = 1
        v = gate.verdict(m)
        self.assertEqual(v["failed_bars"], [7, 10])


@unittest.skipUnless(HAVE_NUMPY and HAVE_FFMPEG, "needs numpy, ffmpeg and ffprobe")
class DecodeTest(unittest.TestCase):
    def test_probe_and_decode_h264(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d) / "t.mp4"
            subprocess.run(
                [
                    "ffmpeg",
                    "-v",
                    "error",
                    "-f",
                    "lavfi",
                    "-i",
                    "testsrc=size=832x480:rate=16",
                    "-frames:v",
                    "33",
                    "-c:v",
                    "libx264",
                    "-pix_fmt",
                    "yuv420p",
                    str(p),
                ],
                check=True,
            )
            info = gate.probe(p)
            self.assertEqual(
                (info["codec"], info["frames"], info["width"], info["height"]),
                ("h264", 33, 832, 480),
            )
            f = gate.decode_rgb(p, 832, 480)
            self.assertEqual(f.shape, (33, 480, 832, 3))
            s9 = gate._s9()
            stats = s9.frame_stats(f)
            self.assertGreater(stats["luma_std_min"], 5)
            same = s9.compare_frames(f, f)
            self.assertTrue(same["identical"])


if __name__ == "__main__":
    unittest.main()
