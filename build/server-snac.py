"""Import-light FastAPI server for bundled SNAC 24 kHz waveform decoding."""

import base64
import binascii
import io
import logging
import os
import struct
import wave
from contextlib import asynccontextmanager
from typing import List

from fastapi import FastAPI, HTTPException
from fastapi.responses import Response
from pydantic import BaseModel, Field

LOGGER = logging.getLogger("flexinfer.snac")
MODEL_ID = os.environ.get("SNAC_MODEL_ID", "hubertsiuzdak/snac_24khz")
SAMPLE_RATE = 24_000

_model = None
_torch = None
_device = "uninitialized"
_probe = "not run"


class DecodeRequest(BaseModel):
    """Three base64 little-endian int32 streams at SNAC's 1:2:4 rates."""

    frames: List[str] = Field(min_length=3, max_length=3)


def _probe_device(torch):
    if not torch.cuda.is_available():
        return "cpu", "GPU unavailable; using CPU fallback"
    try:
        device = torch.device("cuda:0")
        verdict = (torch.ones(1, device=device) + 1).cpu().item()
        if verdict != 2:
            raise RuntimeError(f"unexpected probe result {verdict}")
        name = torch.cuda.get_device_name(0)
        return str(device), f"GPU probe passed on {name}"
    except Exception as exc:  # A broken allocation must never become silent fallback.
        return "cpu", f"GPU probe failed ({type(exc).__name__}: {exc}); using CPU fallback"


def load_model():
    global _model, _torch, _device, _probe
    import torch
    from snac import SNAC

    _torch = torch
    _device, _probe = _probe_device(torch)
    log = LOGGER.info if _device != "cpu" else LOGGER.warning
    log("SNAC accelerator verdict: %s", _probe)
    _model = SNAC.from_pretrained(MODEL_ID).eval().to(_device)
    LOGGER.info("Loaded %s on %s", MODEL_ID, _device)


def _decode_stream(encoded: str, index: int):
    try:
        raw = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError) as exc:
        raise HTTPException(422, f"frames[{index}] is not valid base64") from exc
    if not raw or len(raw) % 4:
        raise HTTPException(422, f"frames[{index}] must contain little-endian int32 values")
    return list(struct.unpack(f"<{len(raw) // 4}i", raw))


def parse_codes(frames: List[str]):
    streams = [_decode_stream(frame, i) for i, frame in enumerate(frames)]
    lengths = [len(stream) for stream in streams]
    if lengths[1] != lengths[0] * 2 or lengths[2] != lengths[0] * 4:
        raise HTTPException(422, f"SNAC stream lengths must have a 1:2:4 ratio; got {lengths}")
    if any(code < 0 or code >= 4096 for stream in streams for code in stream):
        raise HTTPException(422, "SNAC codes must be between 0 and 4095")
    return [_torch.tensor(stream, dtype=_torch.int32, device=_device).unsqueeze(0) for stream in streams]


def pcm_wav(audio) -> bytes:
    samples = audio.detach().float().cpu().reshape(-1).clamp(-1, 1)
    pcm = (samples * 32767).to(_torch.int16).numpy().tobytes()
    output = io.BytesIO()
    with wave.open(output, "wb") as wav:
        wav.setnchannels(1)
        wav.setsampwidth(2)
        wav.setframerate(SAMPLE_RATE)
        wav.writeframes(pcm)
    return output.getvalue()


@asynccontextmanager
async def lifespan(_app):
    load_model()
    yield


app = FastAPI(title="FlexInfer SNAC Decoder", lifespan=lifespan)


@app.get("/health")
def health():
    return {"status": "ok" if _model is not None else "loading", "device": _device, "probe": _probe, "model": MODEL_ID}


@app.post("/v1/audio/decode")
def decode(request: DecodeRequest):
    if _model is None:
        raise HTTPException(503, "SNAC model is not loaded")
    codes = parse_codes(request.frames)
    try:
        with _torch.inference_mode():
            audio = _model.decode(codes)
    except Exception as exc:
        LOGGER.exception("SNAC decode failed")
        raise HTTPException(422, f"SNAC decode failed: {exc}") from exc
    return Response(pcm_wav(audio), media_type="audio/wav")


if __name__ == "__main__":
    import uvicorn

    logging.basicConfig(level=os.environ.get("LOG_LEVEL", "INFO"))
    uvicorn.run(app, host="0.0.0.0", port=int(os.environ.get("PORT", "8000")))
