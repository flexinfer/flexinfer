import base64
import gc
import io
import os
import sys
import threading
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
# Total-pixel budget. A single edge <= MAX_IMAGE_EDGE is not enough on small
# cards: 768x768 (590k px) has 2.25x the pixels of 512x512 (262k px) and OOMs a
# 6 GiB Maxwell card mid-decode, while 768x512 (393k px) fits. Cap on area so the
# server returns a clean 400 instead of an OOM 500 that renders as a black tile.
# Default budget = 768*512 px: allows 512x512, 768x512, 512x768; rejects 768x768.
MAX_IMAGE_PIXELS = int(os.environ.get("MAX_IMAGE_PIXELS", str(768 * 512)))
# Floor on inference steps. SD 1.5 + Euler at 1 step is degenerate: the latent is
# never denoised and the VAE decodes to a fully black image (max pixel = 0). Clamp
# up so a low slider value still produces a real image. Turbo models want ~4.
MIN_INFERENCE_STEPS = int(os.environ.get("MIN_INFERENCE_STEPS", "4"))
# /health reports unhealthy below this much free VRAM once a model is resident, so
# the liveness probe recycles the pod if the card ever saturates (fragmentation /
# leak backstop). Set well below normal peak (512x512 leaves ~3 GiB free).
HEALTH_MIN_FREE_MB = int(os.environ.get("HEALTH_MIN_FREE_MB", "256"))

# Serialize all GPU work behind one lock. The server holds a single global
# pipeline whose scheduler is STATEFUL (step_index): two concurrent generations
# race that state — one request's loop sees step_index past the end and raises
# `sigmas[step_index + 1]` IndexError (-> 500) or yields a black image. The lone
# 6 GiB card can only run one diffusion at a time anyway, so queue instead of
# colliding. Requests wait up to GEN_LOCK_TIMEOUT_SECONDS, then 503 (retryable).
_GEN_LOCK = threading.Lock()
GEN_LOCK_TIMEOUT_SECONDS = int(os.environ.get("GEN_LOCK_TIMEOUT_SECONDS", "300"))

app = FastAPI()

pipe = None
current_model = None


def _apply_scheduler(pipeline, scheduler_name: Optional[str] = None):
    """Override the pipeline scheduler if requested via the DEFAULT_SCHEDULER env.

    Some checkpoints ship a scheduler whose multistep update indexes
    ``sigmas[step_index + 1]`` on the final step and raises an IndexError
    (notably DEISMultistepScheduler — e.g. Lykon/dreamshaper-8). Allowing an
    explicit override lets a Model pin a robust scheduler (euler, dpm++2m, ...)
    instead of the checkpoint default. Mirrors the ROCm diffusers server.
    """
    if not scheduler_name:
        scheduler_name = os.environ.get("DEFAULT_SCHEDULER", "")
    if not scheduler_name:
        return
    from diffusers import (
        DDIMScheduler,
        DPMSolverMultistepScheduler,
        EulerAncestralDiscreteScheduler,
        EulerDiscreteScheduler,
        UniPCMultistepScheduler,
    )

    cfg = pipeline.scheduler.config
    scheduler_map = {
        "euler": lambda c: EulerDiscreteScheduler.from_config(c),
        "euler-a": lambda c: EulerAncestralDiscreteScheduler.from_config(c),
        "dpm++2m": lambda c: DPMSolverMultistepScheduler.from_config(c),
        "dpm++2m-karras": lambda c: DPMSolverMultistepScheduler.from_config(
            c, use_karras_sigmas=True
        ),
        "unipc": lambda c: UniPCMultistepScheduler.from_config(c),
        "ddim": lambda c: DDIMScheduler.from_config(c),
    }
    key = scheduler_name.lower().strip()
    if key in scheduler_map:
        pipeline.scheduler = scheduler_map[key](cfg)
        sys.stdout.write(
            f"Scheduler overridden to: {key} ({type(pipeline.scheduler).__name__})\n"
        )
    else:
        sys.stdout.write(
            f"WARNING: unknown scheduler '{scheduler_name}', keeping "
            f"{type(pipeline.scheduler).__name__}. Options: {list(scheduler_map)}\n"
        )
    sys.stdout.flush()


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
    if w * h > MAX_IMAGE_PIXELS:
        raise HTTPException(
            status_code=400,
            detail=(
                f"size {w}x{h} ({w * h} px) exceeds this GPU's pixel budget "
                f"({MAX_IMAGE_PIXELS} px). Try 512x512, 768x512, or 512x768."
            ),
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

    # Override the checkpoint's default scheduler if requested (e.g. to avoid
    # the DEISMultistepScheduler final-step IndexError on SD 1.5 checkpoints).
    _apply_scheduler(pipe)

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
    cuda_ok = torch.cuda.is_available()
    free_mb = None
    if cuda_ok:
        try:
            free_b, _total_b = torch.cuda.mem_get_info()
            free_mb = free_b // (1024 * 1024)
        except Exception:
            free_mb = None
    # Once a model is resident, treat a near-full card as unhealthy so the
    # liveness probe recycles the pod (fragmentation / leak self-heal). Skip the
    # check before the model loads — free VRAM is naturally high then.
    if (
        cuda_ok
        and pipe is not None
        and free_mb is not None
        and free_mb < HEALTH_MIN_FREE_MB
    ):
        raise HTTPException(
            status_code=503,
            detail=f"low VRAM: {free_mb} MiB free (< {HEALTH_MIN_FREE_MB})",
        )
    return {"status": "ok", "cuda": cuda_ok, "vram_free_mb": free_mb}


@app.post("/v1/images/generations")
def images_generations(req: ImageGenerationRequest):
    # Pin to the checkpoint this pod actually serves (MODEL_ID env). The request's
    # `model` is an alias / served-name chosen upstream and is NOT a loadable
    # repo id — honoring it made `_load_model` try `from_pretrained("imagegen")`
    # etc., which failed/garbled into 500s and black images. Prefer the env so
    # every alias maps to the one resident pipeline.
    model_id = os.environ.get("MODEL_ID") or os.environ.get("MODEL") or req.model
    if not model_id:
        raise HTTPException(status_code=400, detail="Missing model id")

    # Validate cheap request params BEFORE queuing on the GPU lock, so bad
    # requests fail fast without holding up real work.
    width, height = _parse_size(req.size)
    steps = (
        req.num_inference_steps
        if req.num_inference_steps is not None
        else _default_steps(model_id)
    )
    # Clamp up: 1 step is degenerate (black image) on non-turbo SD checkpoints.
    steps = max(steps, MIN_INFERENCE_STEPS)
    guidance = (
        req.guidance_scale
        if req.guidance_scale is not None
        else _default_guidance(model_id)
    )

    # Serialize load + generate: the shared pipeline/scheduler is not concurrency
    # safe (see _GEN_LOCK). Concurrent callers queue here rather than corrupting
    # each other.
    if not _GEN_LOCK.acquire(timeout=GEN_LOCK_TIMEOUT_SECONDS):
        raise HTTPException(
            status_code=503,
            detail="image generator busy; retry shortly",
        )
    try:
        _load_model(model_id)

        images = []
        try:
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
        except RuntimeError as e:
            # CUDA OOM bubbles as torch.cuda.OutOfMemoryError (a RuntimeError
            # subclass) or a driver-level "CUDA error: out of memory" RuntimeError.
            # Return a clean 507 instead of a 500 that the UI renders as a black
            # tile. Cleanup still runs in finally; if the context is wedged, the
            # VRAM-aware /health check recycles the pod.
            if "out of memory" in str(e).lower():
                raise HTTPException(
                    status_code=507,
                    detail=(
                        f"GPU out of memory generating {width}x{height} (n={req.n}). "
                        "Reduce the image size or batch count."
                    ),
                ) from e
            raise
        finally:
            # Always free tensors + GPU cache, including on the error path — a
            # mid-generation failure that skipped cleanup is what let VRAM creep
            # to the ceiling over time and OOM every subsequent request.
            gc.collect()
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
    finally:
        _GEN_LOCK.release()

    return {"created": int(time.time()), "data": images}


def main():
    import uvicorn

    port = int(os.environ.get("PORT", "8000"))
    uvicorn.run(app, host="0.0.0.0", port=port, log_level="info")


if __name__ == "__main__":
    main()
