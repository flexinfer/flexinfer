#!/usr/bin/env python3
"""FastAPI /diarize service for pyannote speaker-diarization-3.1 on gfx906.

Loads the pipeline once at startup (GPU if available) and serves multipart
audio at POST /diarize, returning a JSON segments array. Registered in
flexinfer-proxy as a static upstream so ICC sees a single base URL (Slice 5).

Env:
    HF_TOKEN          required (gated models)
    PYANNOTE_MODEL    optional, default pyannote/speaker-diarization-3.1
"""
import os
import tempfile

import torch
import torchaudio_compat  # noqa: F401  — must precede pyannote (torchaudio 2.9 shim)
from fastapi import FastAPI, Form, HTTPException, UploadFile
from pyannote.audio import Pipeline

app = FastAPI(title="pyannote-diarization", version="3.1")
_pipeline = None


def _load_pipeline():
    global _pipeline
    if _pipeline is not None:
        return _pipeline
    token = os.environ.get("HF_TOKEN")
    if not token:
        raise RuntimeError("HF_TOKEN not set")
    model = os.environ.get("PYANNOTE_MODEL", "pyannote/speaker-diarization-3.1")
    pipe = Pipeline.from_pretrained(model, use_auth_token=token)
    if torch.cuda.is_available():
        pipe.to(torch.device("cuda"))
    _pipeline = pipe
    return pipe


@app.on_event("startup")
def _startup():
    _load_pipeline()


@app.get("/health")
def health():
    return {
        "status": "ok" if _pipeline is not None else "loading",
        "cuda": torch.cuda.is_available(),
        "device": torch.cuda.get_device_name(0) if torch.cuda.is_available() else "cpu",
    }


@app.post("/diarize")
async def diarize(file: UploadFile, num_speakers: int | None = Form(None)):
    pipe = _load_pipeline()
    suffix = os.path.splitext(file.filename or "audio.wav")[1] or ".wav"
    with tempfile.NamedTemporaryFile(suffix=suffix, delete=True) as tmp:
        tmp.write(await file.read())
        tmp.flush()
        try:
            kwargs = {"num_speakers": num_speakers} if num_speakers else {}
            diarization = pipe(tmp.name, **kwargs)
        except Exception as e:  # noqa: BLE001
            raise HTTPException(
                status_code=500, detail=f"diarization failed: {e}"
            ) from e

    segments = [
        {"start": round(turn.start, 3), "end": round(turn.end, 3), "speaker": label}
        for turn, _, label in diarization.itertracks(yield_label=True)
    ]
    speakers = sorted({s["speaker"] for s in segments})
    return {"segments": segments, "num_speakers": len(speakers), "speakers": speakers}
