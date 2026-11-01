#!/usr/bin/env python3
"""CPU-only tests for build/server-pyannote.py (D5-a, the pyannote backend).

The server imports fastapi at module scope and torch, torchaudio_compat and
pyannote.audio when it loads the pipeline. Each one that is not installed is
replaced by a stub before the server loads, so the tests run on a bare
python:3.12-slim (the CI job) without torch: they pin the request contract
(speaker bounds, token handling, response shape) and the load sequence, not
pyannote's numbers. KT-DIAR measures those on the Radeon VII.

Run: python3 scripts/test_server_pyannote.py
"""

import importlib
import importlib.util
import io
import os
import sys
import tempfile
import types
import unittest
from pathlib import Path

SERVER = Path(__file__).resolve().parent.parent / "build" / "server-pyannote.py"


def _missing(name: str) -> bool:
    try:
        importlib.import_module(name)
        return False
    except ImportError:
        return True


class StubHTTPException(Exception):
    def __init__(self, status_code, detail=None):
        super().__init__(detail)
        self.status_code = status_code
        self.detail = detail


def _stub_fastapi() -> None:
    module = types.ModuleType("fastapi")

    class FastAPI:
        def __init__(self, *args, **kwargs):
            self.kwargs = kwargs
            self.routes = {}

        def _route(self, method, path):
            def register(fn):
                self.routes[(method, path)] = fn
                return fn

            return register

        def get(self, path, **_):
            return self._route("GET", path)

        def post(self, path, **_):
            return self._route("POST", path)

    module.FastAPI = FastAPI
    module.HTTPException = StubHTTPException
    module.UploadFile = type("UploadFile", (), {})
    module.File = lambda default=None, **_: default
    module.Form = lambda default=None, **_: default
    sys.modules["fastapi"] = module


class FakeCuda:
    def __init__(self, available=True, broken=False):
        self.available = available
        self.broken = broken

    def is_available(self):
        return self.available

    def get_device_name(self, _index):
        return "AMD Radeon VII"


class FakeTensor:
    def __init__(self, value):
        self.value = value

    def __add__(self, other):
        return FakeTensor(self.value + other)

    def cpu(self):
        return self

    def item(self):
        return self.value


def _fake_torch(cuda: FakeCuda) -> types.ModuleType:
    torch = types.ModuleType("torch")
    torch.cuda = cuda

    class device(str):  # str(torch.device("cuda:0")) == "cuda:0"
        pass

    def ones(_n, device=None):
        if cuda.broken:
            raise RuntimeError("hipErrorNoBinaryForGpu")
        return FakeTensor(1)

    torch.device = device
    torch.ones = ones
    return torch


class FakePipeline:
    loaded = []

    def __init__(self, checkpoint, token):
        self.checkpoint = checkpoint
        self.token = token
        self.device = None
        self.calls = []
        self.result = None
        self.error = None

    @classmethod
    def from_pretrained(cls, checkpoint, use_auth_token=None):
        pipeline = cls(checkpoint, use_auth_token)
        cls.loaded.append(pipeline)
        return pipeline

    def to(self, device):
        self.device = device
        return None  # pyannote 3.x returns the pipeline; the server must not rely on it

    def __call__(self, path, **kwargs):
        self.calls.append((path, kwargs))
        if self.error:
            raise self.error
        return self.result


def _install_pipeline_modules(cuda: FakeCuda) -> None:
    sys.modules["torch"] = _fake_torch(cuda)
    sys.modules["torchaudio_compat"] = types.ModuleType("torchaudio_compat")
    pyannote = types.ModuleType("pyannote")
    audio = types.ModuleType("pyannote.audio")
    audio.Pipeline = FakePipeline
    pyannote.audio = audio
    sys.modules["pyannote"] = pyannote
    sys.modules["pyannote.audio"] = audio


if _missing("fastapi"):
    _stub_fastapi()
HTTPException = importlib.import_module("fastapi").HTTPException

spec = importlib.util.spec_from_file_location("server_pyannote", SERVER)
server = importlib.util.module_from_spec(spec)
spec.loader.exec_module(server)


class Segment:
    def __init__(self, start, end):
        self.start = start
        self.end = end


class FakeAnnotation:
    def __init__(self, tracks):
        self.tracks = tracks

    def itertracks(self, yield_label=False):
        assert yield_label
        for start, end, label in self.tracks:
            yield Segment(start, end), "_", label


class FakeUpload:
    def __init__(self, data: bytes, filename="meeting.wav"):
        self.file = io.BytesIO(data)
        self.filename = filename


class TokenTests(unittest.TestCase):
    def test_reads_the_mounted_secret_file(self):
        with tempfile.NamedTemporaryFile("w", delete=False) as f:
            f.write("hf_secret\n")
        self.addCleanup(os.unlink, f.name)
        env = {"PYANNOTE_HF_TOKEN_FILE": f.name, "HF_TOKEN": "ignored"}
        self.assertEqual(server.read_hf_token(env), "hf_secret")

    def test_a_named_file_that_is_missing_fails_loudly(self):
        with self.assertRaisesRegex(RuntimeError, "hfTokenSecret"):
            server.read_hf_token(
                {"PYANNOTE_HF_TOKEN_FILE": "/nonexistent/flexinfer-hf-token/HF_TOKEN"}
            )

    def test_an_empty_file_fails(self):
        with tempfile.NamedTemporaryFile("w", delete=False) as f:
            f.write("  \n")
        self.addCleanup(os.unlink, f.name)
        with self.assertRaisesRegex(RuntimeError, "is empty"):
            server.read_hf_token({"PYANNOTE_HF_TOKEN_FILE": f.name})

    def test_falls_back_to_hf_token(self):
        self.assertEqual(server.read_hf_token({"HF_TOKEN": " hf_env "}), "hf_env")
        self.assertEqual(server.read_hf_token({}), "")


class SpeakerBoundsTests(unittest.TestCase):
    def test_accepts(self):
        self.assertEqual(server.speaker_bounds(None, None, None), {})
        self.assertEqual(server.speaker_bounds(4, None, None), {"num_speakers": 4})
        self.assertEqual(
            server.speaker_bounds(None, 2, 5), {"min_speakers": 2, "max_speakers": 5}
        )

    def test_rejects(self):
        for args in [(0, None, None), (None, 65, None), (2, 1, None), (None, 5, 2)]:
            with self.assertRaises(HTTPException) as ctx:
                server.speaker_bounds(*args)
            self.assertEqual(ctx.exception.status_code, 400, args)


class ResponseTests(unittest.TestCase):
    def test_turns_are_sorted_and_rounded(self):
        annotation = FakeAnnotation(
            [
                (12.9701, 24.13, "SPEAKER_01"),
                (0.031, 12.97, "SPEAKER_00"),
                (12.5, 13.0, "SPEAKER_00"),
            ]
        )
        turns = server.turns_from_annotation(annotation)
        self.assertEqual(
            turns,
            [
                {"start": 0.031, "end": 12.97, "speaker": "SPEAKER_00"},
                {"start": 12.5, "end": 13.0, "speaker": "SPEAKER_00"},
                {"start": 12.97, "end": 24.13, "speaker": "SPEAKER_01"},
            ],
        )
        body = server.diarization_response(turns, 24.5)
        self.assertEqual(body["object"], "audio.diarization")
        self.assertEqual(body["model"], server.PIPELINE_ID)
        self.assertEqual(body["num_speakers"], 2)
        self.assertEqual(body["speakers"], ["SPEAKER_00", "SPEAKER_01"])
        self.assertEqual(body["duration"], 24.5)
        self.assertEqual(body["segments"], turns)


def diarize(upload, num_speakers=None, min_speakers=None, max_speakers=None):
    """Calls the handler with every form field, as FastAPI does."""
    return server.diarize(
        file=upload,
        model="pyannote-diarization-radeonvii",
        num_speakers=num_speakers,
        min_speakers=min_speakers,
        max_speakers=max_speakers,
    )


class LoadAndServeTests(unittest.TestCase):
    def setUp(self):
        self.saved = {
            k: sys.modules.get(k)
            for k in ("torch", "torchaudio_compat", "pyannote", "pyannote.audio")
        }
        self.env = dict(os.environ)
        FakePipeline.loaded.clear()
        server._pipeline = None

    def tearDown(self):
        for name, module in self.saved.items():
            if module is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = module
        os.environ.clear()
        os.environ.update(self.env)
        server._pipeline = None

    def _load(self, cuda: FakeCuda, **env):
        _install_pipeline_modules(cuda)
        os.environ.pop("PYANNOTE_HF_TOKEN_FILE", None)
        os.environ.pop("PYANNOTE_DEVICE", None)
        os.environ.update(env)
        server.load_pipeline()
        return FakePipeline.loaded[-1]

    def test_loads_on_the_gpu_with_the_token(self):
        pipeline = self._load(FakeCuda(), HF_TOKEN="hf_x")
        self.assertEqual(pipeline.checkpoint, "pyannote/speaker-diarization-3.1")
        self.assertEqual(pipeline.token, "hf_x")
        self.assertEqual(str(pipeline.device), "cuda:0")
        self.assertIs(server._pipeline, pipeline)
        self.assertEqual(server.health()["status"], "ok")
        self.assertIn("Radeon VII", server.health()["probe"])

    def test_a_failed_gpu_probe_is_reported_not_silent(self):
        pipeline = self._load(FakeCuda(broken=True), HF_TOKEN="hf_x")
        self.assertEqual(str(pipeline.device), "cpu")
        self.assertIn("GPU probe failed", server.health()["probe"])

    def test_cpu_on_request(self):
        pipeline = self._load(FakeCuda(), HF_TOKEN="hf_x", PYANNOTE_DEVICE="cpu")
        self.assertEqual(str(pipeline.device), "cpu")

    def test_diarize(self):
        pipeline = self._load(FakeCuda(), HF_TOKEN="hf_x")
        pipeline.result = FakeAnnotation(
            [(0.0, 1.5, "SPEAKER_00"), (1.5, 3.0, "SPEAKER_01")]
        )
        body = diarize(FakeUpload(b"RIFF....WAVE"), min_speakers=2, max_speakers=3)
        self.assertEqual(body["num_speakers"], 2)
        self.assertEqual(len(body["segments"]), 2)
        path, kwargs = pipeline.calls[-1]
        self.assertTrue(path.endswith(".wav"))
        self.assertEqual(kwargs, {"min_speakers": 2, "max_speakers": 3})
        self.assertFalse(
            os.path.exists(path), "the upload must not outlive the request"
        )

    def test_diarize_errors(self):
        with self.assertRaises(HTTPException) as ctx:
            diarize(FakeUpload(b"x"))
        self.assertEqual(ctx.exception.status_code, 503)
        pipeline = self._load(FakeCuda(), HF_TOKEN="hf_x")
        pipeline.error = ValueError("unsupported audio")
        with self.assertRaises(HTTPException) as ctx:
            diarize(FakeUpload(b"x"))
        self.assertEqual(ctx.exception.status_code, 422)
        with self.assertRaises(HTTPException) as ctx:
            diarize(FakeUpload(b"x"), num_speakers=2, max_speakers=3)
        self.assertEqual(ctx.exception.status_code, 400)

    def test_routes(self):
        if not hasattr(server.app, "routes") or not isinstance(server.app.routes, dict):
            self.skipTest("real fastapi installed; the stub records routes")
        self.assertIn(("GET", "/health"), server.app.routes)
        self.assertIn(("POST", "/v1/audio/diarizations"), server.app.routes)


class ServePathContractTests(unittest.TestCase):
    def test_path_matches_the_go_backend(self):
        constants = (SERVER.parent.parent / "backend" / "constants.go").read_text()
        self.assertIn('PyannoteDiarizationPath = "/v1/audio/diarizations"', constants)
        self.assertIn('@app.post("/v1/audio/diarizations")', SERVER.read_text())

    def test_layer_installs_what_the_backend_launches(self):
        root = SERVER.parent.parent
        backend = (root / "backend" / "pyannote.go").read_text()
        layer = (root / "build" / "Dockerfile.runtime-gfx906-pyannote").read_text()
        self.assertIn('PyannotePython = "/opt/pyannote/bin/python"', backend)
        self.assertIn('PyannoteServerScript = "/opt/flexinfer/server-pyannote.py"', backend)
        self.assertIn("python3 -m venv --system-site-packages --without-pip /opt/pyannote", layer)
        self.assertIn("COPY build/server-pyannote.py /opt/flexinfer/server-pyannote.py", layer)
        self.assertIn("COPY build/scripts/torchaudio_compat.py /opt/flexinfer/torchaudio_compat.py", layer)


if __name__ == "__main__":
    unittest.main(verbosity=2)
