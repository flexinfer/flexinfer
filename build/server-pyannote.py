"""Import-light FastAPI server for bundled pyannote speaker diarization.

The `pyannote` backend (backend/pyannote.go) runs this inside flexinfer-runtime
as a /v1/audio/diarizations lane, D5-a of
.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md §6.5. It
supersedes build/scripts/pyannote_server.py, which serves POST /diarize for the
hand-written Deployment, with the same pipeline and the same torchaudio shim.

It is present only in a runtime built with the pyannote layer
(build/Dockerfile.runtime-gfx906-pyannote); the controller refuses pyannote
Models until the GPUProfile certifies that image.

Env (set by the backend):
    PYANNOTE_PIPELINE       HF pipeline id, default pyannote/speaker-diarization-3.1
    PYANNOTE_HF_TOKEN_FILE  file holding the HF token (a mounted Secret key);
                            HF_TOKEN is the fallback for local runs
    PYANNOTE_DEVICE         cuda (default) or cpu
    PORT                    listen port
"""

import logging
import os
import tempfile
import threading
from contextlib import asynccontextmanager
from typing import Optional

from fastapi import FastAPI, File, Form, HTTPException, UploadFile

LOGGER = logging.getLogger("flexinfer.pyannote")
PIPELINE_ID = os.environ.get("PYANNOTE_PIPELINE", "pyannote/speaker-diarization-3.1")
MAX_SPEAKERS = 64

_pipeline = None
# One diarization at a time: requests run in FastAPI's threadpool, and two
# pipelines at once would double the lane's VRAM past its vramEstimateMB.
_pipeline_lock = threading.Lock()
_device = "uninitialized"
_probe = "not run"


def read_hf_token(environ=os.environ) -> str:
    """The HF token from PYANNOTE_HF_TOKEN_FILE, else HF_TOKEN.

    A named file that cannot be read is an error rather than a silent
    anonymous download: the pipeline is gated and would fail later with a less
    useful 401.
    """
    path = environ.get("PYANNOTE_HF_TOKEN_FILE", "").strip()
    if path:
        try:
            with open(path, encoding="utf-8") as handle:
                token = handle.read().strip()
        except OSError as exc:
            raise RuntimeError(
                f"PYANNOTE_HF_TOKEN_FILE={path} is not readable ({exc.strerror}); "
                "mount the Secret named by spec.config.hfTokenSecret into the runtime pod"
            ) from exc
        if not token:
            raise RuntimeError(f"PYANNOTE_HF_TOKEN_FILE={path} is empty")
        return token
    return environ.get("HF_TOKEN", "").strip()


def speaker_bounds(num_speakers, min_speakers, max_speakers) -> dict:
    """pyannote's speaker-count keyword arguments, validated."""
    values = {
        "num_speakers": num_speakers,
        "min_speakers": min_speakers,
        "max_speakers": max_speakers,
    }
    for name, value in values.items():
        if value is not None and not 1 <= value <= MAX_SPEAKERS:
            raise HTTPException(400, f"{name} must be between 1 and {MAX_SPEAKERS}")
    if num_speakers is not None and (
        min_speakers is not None or max_speakers is not None
    ):
        raise HTTPException(400, "num_speakers excludes min_speakers and max_speakers")
    if (
        min_speakers is not None
        and max_speakers is not None
        and min_speakers > max_speakers
    ):
        raise HTTPException(400, "min_speakers must not exceed max_speakers")
    return {name: value for name, value in values.items() if value is not None}


def turns_from_annotation(annotation) -> list:
    """Speaker turns, sorted by start, from a pyannote Annotation."""
    turns = [
        {
            "start": round(float(turn.start), 3),
            "end": round(float(turn.end), 3),
            "speaker": str(label),
        }
        for turn, _, label in annotation.itertracks(yield_label=True)
    ]
    turns.sort(key=lambda t: (t["start"], t["end"], t["speaker"]))
    return turns


def diarization_response(turns: list, duration) -> dict:
    speakers = sorted({t["speaker"] for t in turns})
    return {
        "object": "audio.diarization",
        "model": PIPELINE_ID,
        "duration": duration,
        "num_speakers": len(speakers),
        "speakers": speakers,
        "segments": turns,
    }


def _audio_duration(path: str):
    try:
        import soundfile

        return round(float(soundfile.info(path).duration), 3)
    except Exception:  # Any format soundfile cannot read still diarizes.
        return None


def _probe_device(torch):
    if os.environ.get("PYANNOTE_DEVICE", "cuda").strip().lower() == "cpu":
        return "cpu", "CPU requested"
    if not torch.cuda.is_available():
        return "cpu", "GPU unavailable; using CPU fallback"
    try:
        device = torch.device("cuda:0")
        verdict = (torch.ones(1, device=device) + 1).cpu().item()
        if verdict != 2:
            raise RuntimeError(f"unexpected probe result {verdict}")
        return str(device), f"GPU probe passed on {torch.cuda.get_device_name(0)}"
    except Exception as exc:  # A broken allocation must never become silent fallback.
        return (
            "cpu",
            f"GPU probe failed ({type(exc).__name__}: {exc}); using CPU fallback",
        )


def load_pipeline():
    global _pipeline, _device, _probe
    import torch
    import torchaudio_compat  # noqa: F401  (must precede pyannote; torchaudio 2.9 shim)
    from pyannote.audio import Pipeline

    token = read_hf_token()
    _device, _probe = _probe_device(torch)
    log = LOGGER.info if _device != "cpu" else LOGGER.warning
    log("pyannote accelerator verdict: %s", _probe)
    pipeline = Pipeline.from_pretrained(PIPELINE_ID, use_auth_token=token or None)
    if pipeline is None:
        raise RuntimeError(
            f"{PIPELINE_ID} did not load; check the HF token and the gated model terms"
        )
    pipeline.to(torch.device(_device))
    _pipeline = pipeline
    LOGGER.info("Loaded %s on %s", PIPELINE_ID, _device)


@asynccontextmanager
async def lifespan(_app):
    load_pipeline()
    yield


app = FastAPI(title="FlexInfer pyannote Diarization", lifespan=lifespan)


@app.get("/health")
def health():
    return {
        "status": "ok" if _pipeline is not None else "loading",
        "device": _device,
        "probe": _probe,
        "model": PIPELINE_ID,
    }


@app.post("/v1/audio/diarizations")
def diarize(
    file: UploadFile = File(...),
    model: Optional[str] = Form(None),
    num_speakers: Optional[int] = Form(None),
    min_speakers: Optional[int] = Form(None),
    max_speakers: Optional[int] = Form(None),
):
    del model  # routed by the proxy; one pipeline per lane
    if _pipeline is None:
        raise HTTPException(503, "pyannote pipeline is not loaded")
    kwargs = speaker_bounds(num_speakers, min_speakers, max_speakers)
    suffix = os.path.splitext(file.filename or "audio.wav")[1] or ".wav"
    with tempfile.NamedTemporaryFile(suffix=suffix) as tmp:
        tmp.write(file.file.read())
        tmp.flush()
        try:
            with _pipeline_lock:
                annotation = _pipeline(tmp.name, **kwargs)
        except Exception as exc:
            LOGGER.exception("diarization failed")
            raise HTTPException(422, f"diarization failed: {exc}") from exc
        duration = _audio_duration(tmp.name)
    return diarization_response(turns_from_annotation(annotation), duration)


if __name__ == "__main__":
    import uvicorn

    logging.basicConfig(level=os.environ.get("LOG_LEVEL", "INFO"))
    uvicorn.run(app, host="0.0.0.0", port=int(os.environ.get("PORT", "8000")))
