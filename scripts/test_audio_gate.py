#!/usr/bin/env python3
"""Audio gate: request shape, run and score against a local stub endpoint,
the WAV soundness checks, evidence parsing and the verdict arithmetic run on
the bare interpreter; the spectral case needs numpy and skips without it.

Run: python3 scripts/test_audio_gate.py
"""

import array
import base64
import contextlib
import copy
import importlib.util
import io
import json
import math
import tempfile
import threading
import unittest
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).parent


def _load(name, file):
    spec = importlib.util.spec_from_file_location(name, HERE / file)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


gate = _load("audio_gate", "audio-gate.py")

try:
    import numpy  # noqa: F401

    HAVE_NUMPY = True
except ImportError:  # pragma: no cover - CI images without numpy
    HAVE_NUMPY = False

RATE = 44100


def tone(freq=220.0, amp=0.25, seconds=10.0, channels=2):
    """Interleaved int16 sine, the same in every channel."""
    n = int(RATE * seconds)
    out = array.array("h")
    for i in range(n):
        s = max(-1.0, min(1.0, amp * math.sin(2 * math.pi * freq * i / RATE)))
        v = int(s * 32767)  # clamped like the server
        out.extend([v] * channels)
    return out


def wav_bytes(pcm, channels=2, rate=RATE):
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(channels)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(pcm.tobytes())
    return buf.getvalue()


def stats_of(pcm, channels=2, rate=RATE):
    info, samples = gate.read_wav(wav_bytes(pcm, channels, rate))
    return gate.audio_stats(info, samples)


class _Stub(BaseHTTPRequestHandler):
    bodies = []
    fail_at = None
    cache = {}

    def log_message(self, *a):  # quiet
        pass

    def do_POST(self):  # noqa: N802
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        type(self).bodies.append((self.path, body))
        if type(self).fail_at == len(type(self).bodies):
            self.send_response(500)
            self.end_headers()
            self.wfile.write(b'{"detail":"RecursionError"}')
            return
        seed = body["seed"]
        if seed not in type(self).cache:
            type(self).cache[seed] = wav_bytes(
                tone(freq=200.0 + 10 * seed, seconds=body["seconds"])
            )
        audio = type(self).cache[seed]
        out = {
            "data": [
                {
                    "b64_json": base64.b64encode(audio).decode(),
                    "mime_type": "audio/wav",
                    "sample_rate": RATE,
                    "duration": body["seconds"],
                    "seed": seed,
                    "timings": {"denoise_seconds": 11.0, "total_seconds": 12.0},
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


def run_arm(url, arm, d):
    with contextlib.redirect_stdout(io.StringIO()):
        return gate.main(
            ["run", "--url", url, "--model", "sao-x", "--arm", arm, "--out-dir", d]
        )


def media(**over):
    m = {
        "sample_rate": 44100,
        "channels": 2,
        "duration_s": 10.0,
        "rms_dbfs": -15.0,
        "peak": 8000,
        "clip_frac": 0.0,
        "min_value_frac": 0.0,
        "silent_frac": 0.0,
    }
    m.update(over)
    return m


def render(i, clip, total, wall=None, **media_over):
    return {
        "i": i,
        "clip": clip,
        "status": 200,
        "wall_s": wall or total + 1,
        "timings": {"total_seconds": total},
        "media": media(**media_over),
    }


def good_metrics():
    A = [render(1, "a1", 30.0), render(2, "a2", 12.2), render(3, "a1", 12.0)]
    B = [
        render(1, "a1", 14.0, wall=200.0),
        render(2, "a2", 12.5),
        render(3, "a1", 12.4),
        render(4, "a1", 12.6),
    ]
    same = {"identical": True, "snr_db": math.inf, "spectral_cosine": 1.0}
    return {
        "A": {"renders": A},
        "B": {"renders": B},
        "cmp": {
            "A_a1_r3": same,
            "B_a1_r3": same,
            "B_a1_r4": same,
            "e2e_a1": {"identical": False, "snr_db": 1.2, "spectral_cosine": 0.97},
            "e2e_a2": {"identical": False, "snr_db": 0.4, "spectral_cosine": 0.95},
        },
        "vram": {"A_peak_mib": 9000.0, "B_peak_mib": 9400.0},
        "runtime_peak_gib": 22.0,
        "evidence": {
            "pins": True,
            "warning_lines": 0,
            "kernel_lines": 0,
            "root_rw": True,
            "owner_listen": True,
        },
    }


class RequestTest(unittest.TestCase):
    def test_request_body(self):
        b = gate.request_body("sao-x", "a2")
        self.assertEqual(b["model"], "sao-x")
        self.assertEqual(b["prompt"], gate.PROMPTS["guitar"])
        self.assertEqual(
            (b["seed"], b["seconds"], b["num_inference_steps"]), (7, 10.0, 100)
        )
        self.assertEqual(b["response_format"], "b64_json")

    def test_sequences(self):
        self.assertEqual(gate.SEQUENCES["A"], ["a1", "a2", "a1"])
        self.assertEqual(gate.SEQUENCES["B"], ["a1", "a2", "a1", "a1"])


class RunTest(unittest.TestCase):
    def test_arm_b_renders_four_clips(self):
        with stub_server() as url, tempfile.TemporaryDirectory() as d:
            self.assertEqual(run_arm(url, "B", d), 0)
            self.assertEqual([b["seed"] for _, b in _Stub.bodies], [42, 7, 42, 42])
            self.assertTrue(all(p == "/v1/audio/generations" for p, _ in _Stub.bodies))
            rep = json.loads((Path(d) / "audio-gate-B.json").read_text())
            self.assertEqual(
                [r["file"] for r in rep["renders"]],
                ["B-r1-a1.wav", "B-r2-a2.wav", "B-r3-a1.wav", "B-r4-a1.wav"],
            )
            self.assertEqual(rep["renders"][0]["resp_sample_rate"], RATE)

    def test_run_stops_on_error(self):
        with stub_server(fail_at=2) as url, tempfile.TemporaryDirectory() as d:
            self.assertEqual(run_arm(url, "A", d), 1)
            rep = json.loads((Path(d) / "audio-gate-A.json").read_text())
            self.assertEqual([r["status"] for r in rep["renders"]], [200, 500])
            self.assertIn("RecursionError", rep["renders"][1]["body"])


class SoundTest(unittest.TestCase):
    def test_tone_is_sound(self):
        s = stats_of(tone(amp=0.25))
        self.assertAlmostEqual(s["rms_dbfs"], 20 * math.log10(0.25 / math.sqrt(2)), 1)
        self.assertEqual(
            (s["sample_rate"], s["channels"], s["duration_s"]), (RATE, 2, 10.0)
        )
        self.assertTrue(gate.sound({"status": 200, "media": s}))

    def test_silence_fails(self):
        s = stats_of(array.array("h", [0]) * (RATE * 10 * 2))
        self.assertEqual(s["silent_frac"], 1.0)
        self.assertFalse(gate.sound({"status": 200, "media": s}))

    def test_nan_cast_run_fails(self):
        # A NaN latent clamps to NaN and casts to int16 -32768.
        pcm = tone(amp=0.25)
        for i in range(len(pcm) // 2, len(pcm)):
            pcm[i] = -32768
        s = stats_of(pcm)
        self.assertGreater(s["min_value_frac"], 0.4)
        self.assertFalse(gate.sound({"status": 200, "media": s}))

    def test_clipping_fails(self):
        s = stats_of(tone(amp=1.4))
        self.assertGreater(s["clip_frac"], gate.CLIP_FRAC_MAX)
        self.assertFalse(gate.sound({"status": 200, "media": s}))

    def test_short_or_mono_fails(self):
        self.assertFalse(
            gate.sound({"status": 200, "media": stats_of(tone(seconds=5.0))})
        )
        mono = stats_of(tone(channels=1), channels=1)
        self.assertFalse(gate.sound({"status": 200, "media": mono}))

    def test_non_16_bit_is_an_error(self):
        buf = io.BytesIO()
        with wave.open(buf, "wb") as w:
            w.setnchannels(1)
            w.setsampwidth(1)
            w.setframerate(RATE)
            w.writeframes(b"\x80" * 100)
        with self.assertRaises(ValueError):
            gate.read_wav(buf.getvalue())

    def test_snr(self):
        a = tone(amp=0.25, seconds=0.5)
        self.assertEqual(gate.snr_db(a, a), math.inf)
        b = array.array("h", (x + (7 if i % 2 else -7) for i, x in enumerate(a)))
        self.assertGreater(gate.snr_db(a, b), 50.0)
        self.assertLess(gate.snr_db(a, tone(freq=330.0, amp=0.25, seconds=0.5)), 5.0)
        self.assertIsNone(gate.snr_db(a, a[:-2]))

    @unittest.skipUnless(HAVE_NUMPY, "needs numpy")
    def test_spectral_cosine(self):
        a = tone(freq=220.0, seconds=1.0)
        self.assertAlmostEqual(gate.spectral_cosine(a, a, RATE, 2), 1.0, 5)
        near = gate.spectral_cosine(a, tone(freq=222.0, seconds=1.0), RATE, 2)
        far = gate.spectral_cosine(a, tone(freq=3000.0, seconds=1.0), RATE, 2)
        self.assertGreater(near, far)


class EvidenceTest(unittest.TestCase):
    def test_recursion_error_counts_on_lane_lines(self):
        log = "\n".join(
            [
                "[sao-x] RecursionError: maximum recursion depth exceeded",
                "[sao-x] Traceback (most recent call last):",
                "[sao-x] [audio] model=sao-x seconds=10.0 steps=100 seed=42",
                "[wan-x] RecursionError: elsewhere",
            ]
        )
        ev = gate.runtime_log_evidence(log, "sao-x")
        self.assertEqual((ev["warning_lines"], ev["lane_lines"]), (2, 3))


class VerdictTest(unittest.TestCase):
    def test_all_pass(self):
        v = gate.verdict(good_metrics())
        self.assertEqual(v["decision"], "pass", v)

    def test_no_listen_is_incomplete(self):
        m = good_metrics()
        m["evidence"]["owner_listen"] = None
        v = gate.verdict(m)
        self.assertEqual((v["decision"], v["not_measured"]), ("incomplete", [3]))

    def test_listen_fail_alone_is_conditional(self):
        m = good_metrics()
        m["evidence"]["owner_listen"] = False
        v = gate.verdict(m)
        self.assertEqual((v["decision"], v["failed_bars"]), ("conditional", [3]))

    def test_corrupt_later_clip_fails(self):
        m = good_metrics()
        m["B"]["renders"][2]["media"]["min_value_frac"] = 0.5
        m["cmp"]["B_a1_r3"] = {"identical": False, "snr_db": -3.0}
        v = gate.verdict(m)
        self.assertEqual((v["decision"], v["failed_bars"]), ("fail", [1, 2]))

    def test_slow_warm_total_fails(self):
        m = good_metrics()
        for r in m["B"]["renders"][1:]:
            r["timings"]["total_seconds"] = 16.0  # > 1.25 x 12.1 s
        self.assertEqual(gate.verdict(m)["failed_bars"], [4])

    def test_warnings_and_kernel_fail(self):
        m = copy.deepcopy(good_metrics())
        m["evidence"]["warning_lines"] = 1
        m["evidence"]["kernel_lines"] = 2
        self.assertEqual(gate.verdict(m)["failed_bars"], [8, 9])

    def test_invalid_control_and_range_override(self):
        m = good_metrics()
        for r in m["A"]["renders"]:
            r["timings"]["total_seconds"] = 60.0  # outside 6-36 s
        self.assertEqual(gate.verdict(m)["decision"], "invalid")
        m["a_warm_total_range"] = [50.0, 70.0]
        self.assertEqual(gate.verdict(m)["decision"], "pass")


class ScoreTest(unittest.TestCase):
    def test_score_stub_arms(self):
        with stub_server() as url, tempfile.TemporaryDirectory() as root:
            a, b = Path(root) / "A", Path(root) / "B"
            self.assertEqual(run_arm(url, "A", str(a)), 0)
            self.assertEqual(run_arm(url, "B", str(b)), 0)
            ra = json.loads((a / "audio-gate-A.json").read_text())
            rb = json.loads((b / "audio-gate-B.json").read_text())
            vram_a, vram_b = Path(root) / "va.jsonl", Path(root) / "vb.jsonl"
            vram_a.write_text(json.dumps({"t": ra["t0"], "used_mib": 9000.0}) + "\n")
            vram_b.write_text(json.dumps({"t": rb["t0"], "used_mib": 9100.0}) + "\n")
            env = Path(root) / "env.txt"
            pin = "garbage_collection_threshold:0.9,max_split_size_mb:512,expandable_segments:False"
            env.write_text(f"PYTORCH_ALLOC_CONF={pin}\0PYTORCH_HIP_ALLOC_CONF={pin}\0")
            log = Path(root) / "rt.log"
            log.write_text(
                "[sao-x] [audio] model=sao-x seconds=10.0 steps=100 seed=42\n"
            )
            kern = Path(root) / "kern.txt"
            kern.write_text("")
            out = Path(root) / "v.json"
            common = [
                "score",
                str(a),
                str(b),
                "--json",
                str(out),
            ]
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(gate.main(common), 1)
            v = json.loads(out.read_text())
            self.assertEqual(v["decision"], "incomplete")
            self.assertTrue(v["bars"][0]["pass"] and v["bars"][1]["pass"])
            self.assertTrue(v["metrics"]["cmp"]["e2e_a1"]["identical"])
            with contextlib.redirect_stdout(io.StringIO()):
                rc = gate.main(
                    common
                    + [
                        "--vram-a",
                        str(vram_a),
                        "--vram-b",
                        str(vram_b),
                        "--runtime-peak-gib",
                        "20",
                        "--process-env",
                        str(env),
                        "--runtime-log",
                        str(log),
                        "--kernel-log",
                        str(kern),
                        "--root-mode",
                        "rw",
                        "--owner-listen",
                        "pass",
                    ]
                )
            v = json.loads(out.read_text())
            self.assertEqual((rc, v["decision"]), (0, "pass"), v["bars"])


if __name__ == "__main__":
    unittest.main()
