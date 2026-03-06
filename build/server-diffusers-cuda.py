import base64
import gc
import io
import os
import sys
import time
import uuid
from typing import Optional

import torch
from PIL import Image
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

# Newer diffusers releases reference torch.xpu even when the installed torch build
# doesn't provide it. Provide a tiny shim to keep imports working on standard CUDA
# torch builds.
if not hasattr(torch, "xpu"):
    class _XPU:
        @staticmethod
        def empty_cache():
            return None

        @staticmethod
        def device_count():
            return 0

        @staticmethod
        def is_available():
            return False

        @staticmethod
        def current_device():
            return 0

    torch.xpu = _XPU()  # type: ignore[attr-defined]

from diffusers import AutoPipelineForText2Image

DEFAULT_SIZE = os.environ.get("DEFAULT_SIZE", "512x512")
MAX_IMAGE_EDGE = int(os.environ.get("MAX_IMAGE_EDGE", "768"))

app = FastAPI()

pipe = None
current_model = None


def _parse_size(size: str) -> tuple[int, int]:
    if not size:
        size = DEFAULT_SIZE
    try:
        w_s, h_s = size.lower().split("x", 1)
        w, h = int(w_s), int(h_s)
    except Exception as e:
        raise HTTPException(status_code=400, detail=f"Invalid size '{size}': {e}")
    if w <= 0 or h <= 0:
        raise HTTPException(status_code=400, detail="size must be positive")
    if w > MAX_IMAGE_EDGE or h > MAX_IMAGE_EDGE:
        raise HTTPException(
            status_code=400,
            detail=f"size too large for this GPU (max edge {MAX_IMAGE_EDGE})",
        )
    return w, h


def _is_turbo(model_id: str) -> bool:
    m = (model_id or "").lower()
    return "turbo" in m


def _default_steps(model_id: str) -> int:
    v = os.environ.get("DEFAULT_NUM_INFERENCE_STEPS")
    if v:
        try:
            return max(1, int(v))
        except ValueError:
            pass
    return 4 if _is_turbo(model_id) else 20


def _default_guidance(model_id: str) -> float:
    v = os.environ.get("DEFAULT_GUIDANCE_SCALE")
    if v:
        try:
            return float(v)
        except ValueError:
            pass
    return 0.0 if _is_turbo(model_id) else 7.5


def _load_model(model_id: str):
    global pipe, current_model
    if pipe is not None and current_model == model_id:
        return

    if not torch.cuda.is_available():
        raise RuntimeError("CUDA GPU not available")

    t0 = time.time()
    torch.cuda.empty_cache()

    # Prefer fp16 weights where available.
    try:
        pipe = AutoPipelineForText2Image.from_pretrained(
            model_id,
            torch_dtype=torch.float16,
            variant="fp16",
        )
    except Exception:
        pipe = AutoPipelineForText2Image.from_pretrained(
            model_id,
            torch_dtype=torch.float16,
        )

    pipe.to("cuda")
    pipe.enable_attention_slicing()
    try:
        pipe.enable_vae_slicing()
        pipe.enable_vae_tiling()
    except Exception:
        pass

    current_model = model_id
    sys.stdout.write(f"Loaded model {model_id} in {time.time() - t0:.1f}s\\n")
    sys.stdout.flush()


class ImageGenerationRequest(BaseModel):
    prompt: str
    model: Optional[str] = None
    n: int = Field(default=1, ge=1, le=4)
    size: str = DEFAULT_SIZE
    response_format: str = "b64_json"
    negative_prompt: Optional[str] = None
    num_inference_steps: Optional[int] = Field(default=None, ge=1, le=50)
    guidance_scale: Optional[float] = Field(default=None, ge=0.0, le=20.0)


@app.get("/health")
def health():
    return {"status": "ok", "cuda": torch.cuda.is_available()}


@app.post("/v1/images/generations")
def images_generations(req: ImageGenerationRequest):
    model_id = req.model or os.environ.get("MODEL_ID") or os.environ.get("MODEL")
    if not model_id:
        raise HTTPException(status_code=400, detail="Missing model id")

    _load_model(model_id)

    width, height = _parse_size(req.size)
    steps = req.num_inference_steps if req.num_inference_steps is not None else _default_steps(model_id)
    guidance = req.guidance_scale if req.guidance_scale is not None else _default_guidance(model_id)

    images = []
    with torch.inference_mode(), torch.autocast("cuda", dtype=torch.float16):
        for _ in range(req.n):
            out = pipe(
                prompt=req.prompt,
                negative_prompt=req.negative_prompt,
                num_inference_steps=steps,
                guidance_scale=guidance,
                width=width,
                height=height,
            )
            image = out.images[0]
            if not isinstance(image, Image.Image):
                image = Image.fromarray(image)
            buf = io.BytesIO()
            image.save(buf, format="PNG")
            b64 = base64.b64encode(buf.getvalue()).decode("utf-8")
            images.append({"b64_json": b64})
            del out, image, buf  # break reference cycles

    # Free leaked tensors from reference cycles before clearing GPU cache
    gc.collect()
    if torch.cuda.is_available():
        torch.cuda.empty_cache()

    return {"created": int(time.time()), "data": images}


def main():
    import uvicorn

    port = int(os.environ.get("PORT", "8000"))
    uvicorn.run(app, host="0.0.0.0", port=port, log_level="info")


if __name__ == "__main__":
    main()
