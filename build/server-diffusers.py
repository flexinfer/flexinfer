"""
Diffusers API Server with OpenAI-compatible /v1/images/generations and /v1/images/edits endpoints.
ROCm-compatible version for AMD GPUs (gfx1100 / RX 7900 XTX).
Supports text2image, inpainting, and InstructPix2Pix modes via PIPELINE_MODE env var.
"""

import asyncio
import gc
import os
import io
import base64
import json
import uuid
import time
import sys
import re
from typing import Optional, List
from contextlib import asynccontextmanager

import torch

# Polyfill torch.nn.RMSNorm for PyTorch < 2.4 (required by diffusers 0.36+ FLUX transformer).
if not hasattr(torch.nn, "RMSNorm"):

    class _RMSNorm(torch.nn.Module):
        def __init__(self, normalized_shape, eps=1e-6, elementwise_affine=True):
            super().__init__()
            if isinstance(normalized_shape, int):
                normalized_shape = (normalized_shape,)
            self.normalized_shape = tuple(normalized_shape)
            self.eps = eps
            self.elementwise_affine = elementwise_affine
            if elementwise_affine:
                self.weight = torch.nn.Parameter(torch.ones(self.normalized_shape))
            else:
                self.register_parameter("weight", None)

        def forward(self, x):
            norm = x * torch.rsqrt(x.pow(2).mean(-1, keepdim=True) + self.eps)
            if self.weight is not None:
                norm = norm * self.weight
            return norm

        def extra_repr(self):
            return f"{self.normalized_shape}, eps={self.eps}, elementwise_affine={self.elementwise_affine}"

    torch.nn.RMSNorm = _RMSNorm
    print("Patched torch.nn.RMSNorm for PyTorch < 2.4 compatibility")

# Polyfill enable_gqa for scaled_dot_product_attention (PyTorch < 2.5).
# diffusers 0.36+ FLUX attention processors pass enable_gqa=True, which
# PyTorch 2.3 doesn't accept. Strip it and fall back to standard SDPA.
import torch.nn.functional as F

_torch_version = tuple(int(x) for x in torch.__version__.split("+")[0].split(".")[:2])
if _torch_version < (2, 5):
    _orig_sdpa = F.scaled_dot_product_attention

    def _patched_sdpa(*args, **kwargs):
        kwargs.pop("enable_gqa", None)
        return _orig_sdpa(*args, **kwargs)

    F.scaled_dot_product_attention = _patched_sdpa
    print(
        f"Patched F.scaled_dot_product_attention to strip enable_gqa (PyTorch {torch.__version__} < 2.5)"
    )

from PIL import Image
from fastapi import FastAPI, HTTPException, UploadFile, File, Form
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field
from diffusers import (
    DiffusionPipeline,
    AutoencoderKL,
    StableDiffusionXLPipeline,
    StableDiffusionPipeline,
    StableDiffusionInstructPix2PixPipeline,
    StableDiffusionXLInpaintPipeline,
    FluxPipeline,
    FluxFillPipeline,
)


# Lazy-import AutoPipeline* to avoid cascading import of all pipelines
# (CogView4, QwenImage etc. require transformers >= 4.49)
def _lazy_auto_pipeline_text2image():
    from diffusers import AutoPipelineForText2Image

    return AutoPipelineForText2Image


# Global pipeline cache
pipeline = None
current_model = None
current_model_family = None
gpu_info = None

# Pipeline mode: "text2image" (default), "inpainting", or "instruct"
PIPELINE_MODE = os.environ.get("PIPELINE_MODE", "text2image")


def check_gpu():
    """Check GPU availability and print diagnostics."""
    global gpu_info
    print("=" * 60)
    print("GPU Diagnostics (ROCm/HIP)")
    print("=" * 60)
    print(f"PyTorch version: {torch.__version__}")

    # Check for ROCm/HIP version
    hip_version = getattr(torch.version, "hip", None)
    cuda_version = getattr(torch.version, "cuda", None)
    print(f"HIP version: {hip_version}")
    print(f"CUDA version: {cuda_version}")

    # Environment variables
    print(
        f"HSA_OVERRIDE_GFX_VERSION: {os.environ.get('HSA_OVERRIDE_GFX_VERSION', 'not set')}"
    )
    print(f"PYTORCH_ROCM_ARCH: {os.environ.get('PYTORCH_ROCM_ARCH', 'not set')}")
    print(
        f"TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL: {os.environ.get('TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL', 'not set')}"
    )

    print(f"CUDA/ROCm available: {torch.cuda.is_available()}")
    if torch.cuda.is_available():
        print(f"Device count: {torch.cuda.device_count()}")
        for i in range(torch.cuda.device_count()):
            props = torch.cuda.get_device_properties(i)
            print(f"  Device {i}: {props.name}")
            print(f"    Total memory: {props.total_memory / 1024**3:.1f} GB")
            print(
                f"    Architecture: gfx{props.major}{props.minor}0"
                if hasattr(props, "major")
                else "    Architecture: unknown"
            )
        gpu_info = torch.cuda.get_device_properties(0).name

        # Skip basic tensor test - torch.randn on ROCm gfx1100 can cause SIGSEGV
        # GPU functionality will be verified during model loading
        print("  GPU detected, will verify during model load")
    else:
        print("WARNING: No CUDA/ROCm GPU detected!")
        gpu_info = "CPU only"
    print("=" * 60)
    sys.stdout.flush()


class ImageGenerationRequest(BaseModel):
    prompt: str
    model: Optional[str] = None
    n: int = Field(default=1, ge=1, le=10)
    size: str = "1024x1024"
    quality: str = "standard"
    response_format: str = "b64_json"  # or "url"
    style: Optional[str] = None
    negative_prompt: Optional[str] = None
    # If omitted, defaults are derived from env vars and/or the model type.
    # This keeps SDXL Turbo fast by default (few steps, low/zero guidance).
    num_inference_steps: Optional[int] = Field(default=None, ge=1, le=100)
    guidance_scale: Optional[float] = Field(default=None, ge=0.0, le=20.0)


def _env_int(name: str) -> Optional[int]:
    value = os.environ.get(name)
    if value is None or value == "":
        return None
    try:
        return int(value)
    except ValueError:
        return None


def _env_float(name: str) -> Optional[float]:
    value = os.environ.get(name)
    if value is None or value == "":
        return None
    try:
        return float(value)
    except ValueError:
        return None


def _env_bool(name: str, default: bool = False) -> bool:
    value = os.environ.get(name)
    if value is None or value == "":
        return default
    return value.strip().lower() in ("1", "true", "yes", "on")


def _is_sdxl_turbo(model_id: str) -> bool:
    model_id = (model_id or "").lower()
    return "sdxl-turbo" in model_id or "sdxl_turbo" in model_id


def _normalize_model_family(value: Optional[str]) -> Optional[str]:
    value = (value or "").strip().lower()
    aliases = {
        "fluxfill": "flux",
        "flux-fill": "flux",
        "flux.fill": "flux",
        "sdxl-turbo": "sdxl",
        "stable-diffusion-xl": "sdxl",
        "stable-diffusion-3": "sd3",
        "stable-diffusion-3.5": "sd3",
        "stable-diffusion-1.5": "sd15",
        "stable-diffusion-v1-5": "sd15",
        "sd-1.5": "sd15",
    }
    value = aliases.get(value, value)
    return value if value in {"flux", "sdxl", "sd3", "sd15"} else None


def _configured_model_family() -> Optional[str]:
    return _normalize_model_family(os.environ.get("MODEL_FAMILY"))


def _class_name_model_family(class_name: Optional[str]) -> Optional[str]:
    class_name = (class_name or "").strip()
    if not class_name:
        return None
    if class_name in ("FluxPipeline", "FluxFillPipeline"):
        return "flux"
    if "StableDiffusion3" in class_name:
        return "sd3"
    if "StableDiffusionXL" in class_name:
        return "sdxl"
    if class_name.startswith("StableDiffusion"):
        return "sd15"
    return None


def _looks_like_flux_id(model_id: Optional[str]) -> bool:
    return bool(
        re.search(r"(^|[\/_-])flux(?:[._-]?1)?($|[\/._-])", (model_id or "").lower())
    )


def _heuristic_model_family(model_id: Optional[str]) -> Optional[str]:
    lower = (model_id or "").lower()
    if not lower:
        return None
    if "stable-diffusion-3.5" in lower or "stable-diffusion-3" in lower:
        return "sd3"
    if "sdxl" in lower or "stable-diffusion-xl" in lower:
        return "sdxl"
    if (
        "stable-diffusion-v1-5" in lower
        or "stable-diffusion-1.5" in lower
        or re.search(r"(^|[\/_-])sd15($|[\/._-])", lower)
    ):
        return "sd15"
    if _looks_like_flux_id(lower):
        return "flux"
    return None


def _model_family(
    model_id: Optional[str] = None,
    pipe=None,
    model_index_class: Optional[str] = None,
) -> Optional[str]:
    explicit = _configured_model_family()
    if explicit is not None:
        return explicit
    if pipe is not None:
        family = _class_name_model_family(type(pipe).__name__)
        if family is not None:
            return family
    if current_model == model_id and current_model_family is not None:
        return current_model_family
    family = _class_name_model_family(model_index_class)
    if family is not None:
        return family
    return _heuristic_model_family(model_id)


def _is_flux(model_id: str) -> bool:
    return _model_family(model_id=model_id) == "flux"


def _is_flux_schnell(model_id: str) -> bool:
    return _is_flux(model_id) and "schnell" in (model_id or "").lower()


def _single_file_pipeline_override() -> str:
    return os.environ.get("SINGLE_FILE_PIPELINE", "").strip().lower()


def _single_file_config_override() -> Optional[str]:
    value = os.environ.get("SINGLE_FILE_CONFIG", "").strip()
    return value or None


def _single_file_strict() -> bool:
    return os.environ.get("SINGLE_FILE_STRICT", "").strip().lower() in (
        "1",
        "true",
        "yes",
        "on",
    )


def _pipeline_is_flux_like(pipe=None, model_id: Optional[str] = None) -> bool:
    if pipe is not None:
        return isinstance(pipe, (FluxPipeline, FluxFillPipeline))
    override = _single_file_pipeline_override()
    if override in ("flux", "flux-fill"):
        return True
    return _model_family(model_id=model_id or current_model) == "flux"


def _default_steps(model_id: str) -> int:
    # Per-deployment override via env
    steps = _env_int("DEFAULT_NUM_INFERENCE_STEPS")
    if steps is not None and steps > 0:
        return steps
    family = _model_family(model_id=model_id)
    if _is_sdxl_turbo(model_id):
        return 4
    if family == "flux" and _is_flux_schnell(model_id):
        return 4
    if family == "flux":
        return 28
    if family == "sd3":
        return 28
    return 20


def _default_guidance_scale(model_id: str) -> float:
    scale = _env_float("DEFAULT_GUIDANCE_SCALE")
    if scale is not None and scale >= 0.0:
        return scale
    family = _model_family(model_id=model_id)
    if _is_sdxl_turbo(model_id):
        return 0.0
    if family == "flux" and _is_flux_schnell(model_id):
        return 0.0
    if family == "flux":
        return 3.5
    if family == "sd3":
        return 4.5
    return 7.5


def _default_negative_prompt() -> Optional[str]:
    """Return the server-wide default negative prompt from env, or None."""
    return os.environ.get("DEFAULT_NEGATIVE_PROMPT") or None


def _default_strength() -> float:
    """Return the default denoising strength for inpainting."""
    val = _env_float("DEFAULT_STRENGTH")
    return val if val is not None else 0.75


def _default_image_guidance_scale() -> float:
    """Return the default image guidance scale for InstructPix2Pix."""
    val = _env_float("DEFAULT_IMAGE_GUIDANCE_SCALE")
    return val if val is not None else 1.5


def _decode_image(data: bytes) -> Image.Image:
    """Decode raw bytes (PNG/JPEG) or base64 string into a PIL RGB image."""
    try:
        img = Image.open(io.BytesIO(data))
    except Exception:
        img = Image.open(io.BytesIO(base64.b64decode(data)))
    return img.convert("RGB")


def _decode_mask(data: bytes, target_size: tuple) -> Image.Image:
    """Decode mask bytes into a grayscale PIL image, resized to target_size."""
    try:
        mask = Image.open(io.BytesIO(data))
    except Exception:
        mask = Image.open(io.BytesIO(base64.b64decode(data)))
    mask = mask.convert("L").resize(target_size, Image.NEAREST)
    return mask


def _resize_for_pipeline(
    img: Image.Image, target_size: Optional[str] = None
) -> Image.Image:
    """Resize image so both dimensions are properly aligned.

    FLUX uses 16x16 patch tokenization with 4x VAE compression = 64px alignment.
    Other pipelines (SDXL, etc.) need 8px alignment.
    """
    if target_size:
        try:
            w, h = map(int, target_size.split("x"))
            img = img.resize((w, h), Image.LANCZOS)
        except ValueError:
            pass
    w, h = img.size
    # FLUX needs 64px alignment (16x16 patches * 4x VAE); others need 8px.
    align = 64 if _pipeline_is_flux_like(pipeline, current_model) else 8
    w = (w // align) * align
    h = (h // align) * align
    if (w, h) != img.size:
        img = img.resize((w, h), Image.LANCZOS)
    return img


def _apply_scheduler(pipe, scheduler_name: Optional[str]):
    """Override the pipeline scheduler if requested via env or config."""
    if not scheduler_name:
        scheduler_name = os.environ.get("DEFAULT_SCHEDULER", "")
    if not scheduler_name:
        return

    from diffusers import (
        DPMSolverMultistepScheduler,
        DPMSolverSinglestepScheduler,
        EulerDiscreteScheduler,
        EulerAncestralDiscreteScheduler,
        UniPCMultistepScheduler,
        DDIMScheduler,
    )

    scheduler_map = {
        "dpm++2m": lambda cfg: DPMSolverMultistepScheduler.from_config(
            cfg, use_karras_sigmas=False
        ),
        "dpm++2m-karras": lambda cfg: DPMSolverMultistepScheduler.from_config(
            cfg, use_karras_sigmas=True
        ),
        "dpm++2m-sde-karras": lambda cfg: DPMSolverMultistepScheduler.from_config(
            cfg, use_karras_sigmas=True, algorithm_type="sde-dpmsolver++"
        ),
        "dpm++sde": lambda cfg: DPMSolverSinglestepScheduler.from_config(cfg),
        "dpm++sde-karras": lambda cfg: DPMSolverSinglestepScheduler.from_config(
            cfg, use_karras_sigmas=True
        ),
        "euler": lambda cfg: EulerDiscreteScheduler.from_config(cfg),
        "euler-a": lambda cfg: EulerAncestralDiscreteScheduler.from_config(cfg),
        "unipc": lambda cfg: UniPCMultistepScheduler.from_config(cfg),
        "ddim": lambda cfg: DDIMScheduler.from_config(cfg),
    }

    key = scheduler_name.lower().strip()
    if key in scheduler_map:
        pipe.scheduler = scheduler_map[key](pipe.scheduler.config)
        print(f"Scheduler overridden to: {key} ({type(pipe.scheduler).__name__})")
    else:
        print(
            f"WARNING: Unknown scheduler '{scheduler_name}', keeping default. Options: {list(scheduler_map.keys())}"
        )
    sys.stdout.flush()


def _compile_mode() -> Optional[str]:
    """Return the requested torch.compile mode, or None if disabled."""
    raw = os.environ.get("COMPILE_MODE", "").strip()
    if not raw or raw.lower() in ("0", "false", "no", "off", "disabled", "none"):
        return None
    lowered = raw.lower()
    if lowered in ("1", "true", "yes", "on", "auto"):
        return "reduce-overhead"
    return raw


def _compile_target_name(pipe, family: Optional[str]) -> Optional[str]:
    """Choose the module attribute to compile for the loaded pipeline."""
    if family == "sd3" and hasattr(pipe, "transformer"):
        return "transformer"
    if hasattr(pipe, "unet"):
        return "unet"
    if hasattr(pipe, "transformer"):
        return "transformer"
    return None


def _apply_startup_lora(pipe, family: Optional[str]) -> None:
    """Load an optional LoRA adapter at startup, failing soft on any error."""
    lora_path = os.environ.get("LORA_PATH", "").strip()
    lora_repo = os.environ.get("LORA_REPO", "").strip()
    if not lora_path and not lora_repo:
        return

    if family not in {"sdxl", "sd3", "sd15"}:
        print(
            f"Skipping startup LoRA for unsupported family: {family or 'unknown'}"
        )
        return

    if not hasattr(pipe, "load_lora_weights"):
        print("WARNING: pipeline does not support load_lora_weights; skipping LoRA")
        return

    adapter_name = os.environ.get("LORA_ADAPTER_NAME", "startup").strip() or "startup"
    weight_name = os.environ.get("LORA_WEIGHT_NAME", "").strip() or None
    lora_scale = _env_float("LORA_SCALE")
    sources = []
    if lora_path:
        sources.append(("local", lora_path))
    if lora_repo and lora_repo != lora_path:
        sources.append(("repo", lora_repo))

    for source_kind, source in sources:
        try:
            print(f"Loading startup LoRA from {source_kind} source: {source}")
            sys.stdout.flush()
            load_kwargs = {"adapter_name": adapter_name}
            if weight_name:
                load_kwargs["weight_name"] = weight_name
            pipe.load_lora_weights(source, **load_kwargs)
            print(f"Loaded startup LoRA adapter: {adapter_name}")

            if lora_scale is not None:
                scale_applied = False
                if hasattr(pipe, "set_adapters"):
                    try:
                        pipe.set_adapters(adapter_name, lora_scale)
                        print(f"Applied startup LoRA scale via set_adapters: {lora_scale}")
                        scale_applied = True
                    except Exception as exc:
                        print(f"WARNING: set_adapters failed for LoRA scale: {exc}")
                elif hasattr(pipe, "fuse_lora"):
                    try:
                        pipe.fuse_lora(lora_scale=lora_scale)
                        print(f"Applied startup LoRA scale via fuse_lora: {lora_scale}")
                        scale_applied = True
                    except Exception as exc:
                        print(f"WARNING: fuse_lora failed for LoRA scale: {exc}")
                if not scale_applied and hasattr(pipe, "fuse_lora"):
                    try:
                        pipe.fuse_lora(lora_scale=lora_scale)
                        print(f"Applied startup LoRA scale via fuse_lora fallback: {lora_scale}")
                        scale_applied = True
                    except Exception as exc:
                        print(f"WARNING: fuse_lora fallback failed for LoRA scale: {exc}")
                if not scale_applied:
                    print(
                        "WARNING: LoRA scale requested but pipeline exposes no scaling hook"
                    )
            return
        except Exception as exc:
            print(f"WARNING: failed to load startup LoRA from {source}: {exc}")

    print("WARNING: startup LoRA requested but could not be loaded; continuing")


def _apply_compile_controls(pipe, family: Optional[str], cpu_offload: bool) -> None:
    """Optionally compile the most expensive pipeline module, failing soft."""
    raw_compile_mode = os.environ.get("COMPILE_MODE", "").strip()
    compile_requested = any(
        os.environ.get(name, "").strip() != ""
        for name in (
            "COMPILE_MODE",
            "COMPILE_FULLGRAPH",
            "COMPILE_DYNAMIC",
            "COMPILE_REPEATED_BLOCKS",
        )
    )
    if raw_compile_mode.lower() in ("0", "false", "no", "off", "disabled", "none"):
        return
    if not compile_requested:
        return
    compile_mode = _compile_mode() or "reduce-overhead"

    if cpu_offload:
        print("Skipping torch.compile because CPU offload is enabled")
        return

    if family not in {"sdxl", "sd3", "sd15"}:
        print(f"Skipping torch.compile for unsupported family: {family or 'unknown'}")
        return

    if not hasattr(torch, "compile"):
        print("WARNING: torch.compile is unavailable; skipping compile controls")
        return

    target_name = _compile_target_name(pipe, family)
    if target_name is None:
        print("WARNING: no compile target found on pipeline; skipping compile")
        return

    target = getattr(pipe, target_name, None)
    if target is None:
        print(f"WARNING: pipeline target '{target_name}' missing; skipping compile")
        return

    fullgraph = _env_bool("COMPILE_FULLGRAPH", True)
    dynamic = _env_bool("COMPILE_DYNAMIC", False)
    repeated_blocks = _env_bool("COMPILE_REPEATED_BLOCKS", False)
    compile_kwargs = {"mode": compile_mode, "fullgraph": fullgraph, "dynamic": dynamic}

    try:
        if repeated_blocks:
            repeated_error = None
            compiled_target = None
            if hasattr(target, "compile_repeated_blocks"):
                try:
                    compiled_target = target.compile_repeated_blocks(
                        fullgraph=fullgraph, dynamic=dynamic
                    )
                    if compiled_target is None:
                        compiled_target = target
                    print(
                        f"Compiled repeated blocks on {target_name} via native hook (fullgraph={fullgraph}, dynamic={dynamic})"
                    )
                except Exception as exc:
                    repeated_error = exc
            if compiled_target is None:
                try:
                    from accelerate.utils import compile_regions

                    compiled_target = compile_regions(
                        target, fullgraph=fullgraph, dynamic=dynamic
                    )
                    print(
                        f"Compiled repeated blocks on {target_name} via accelerate.compile_regions (fullgraph={fullgraph}, dynamic={dynamic})"
                    )
                except Exception as exc:
                    if repeated_error is not None:
                        print(
                            f"WARNING: repeated-block compilation failed on {target_name}: {repeated_error}"
                        )
                    raise exc
        else:
            compiled_target = torch.compile(target, **compile_kwargs)
            print(
                f"Compiled {target_name} with torch.compile (mode={compile_mode}, fullgraph={fullgraph}, dynamic={dynamic})"
            )
    except Exception as exc:
        print(f"WARNING: compile controls failed for {target_name}: {exc}")
        return

    if compiled_target is not target:
        setattr(pipe, target_name, compiled_target)
        print(f"Replaced pipeline.{target_name} with compiled module")
    else:
        print(f"Kept pipeline.{target_name} in place after compilation")

    if torch.cuda.is_available():
        try:
            torch.cuda.synchronize()
        except Exception:
            pass


class ImageData(BaseModel):
    b64_json: Optional[str] = None
    url: Optional[str] = None
    revised_prompt: Optional[str] = None


class ImageGenerationResponse(BaseModel):
    created: int
    data: List[ImageData]


def _detect_model_format(model_path: str) -> tuple:
    """Detect whether the model path is diffusers format or a single checkpoint file.

    Returns:
        (format, path) where format is "diffusers", "single_file", or "unknown"
        and path is the resolved path to use for loading.
    """
    if not os.path.isdir(model_path):
        # Not a directory - check if it's a direct file path
        if os.path.isfile(model_path) and model_path.endswith(".safetensors"):
            return "single_file", model_path
        return "unknown", model_path

    # Check for diffusers format marker
    if os.path.isfile(os.path.join(model_path, "model_index.json")):
        return "diffusers", model_path

    # Look for safetensors files (single-file checkpoint from CivitAI etc.)
    safetensors_files = [
        f
        for f in os.listdir(model_path)
        if f.endswith(".safetensors") and not f.startswith(".")
    ]
    if len(safetensors_files) == 1:
        return "single_file", os.path.join(model_path, safetensors_files[0])
    if len(safetensors_files) > 1:
        # Multiple safetensors but no model_index.json - pick the largest
        # (the main checkpoint, not a VAE or text encoder)
        largest = max(
            safetensors_files,
            key=lambda f: os.path.getsize(os.path.join(model_path, f)),
        )
        print(f"Multiple safetensors found, using largest: {largest}")
        return "single_file", os.path.join(model_path, largest)

    # Check subdirectories (prefetch may nest inside a named subdirectory)
    for entry in os.listdir(model_path):
        subdir = os.path.join(model_path, entry)
        if os.path.isdir(subdir):
            if os.path.isfile(os.path.join(subdir, "model_index.json")):
                return "diffusers", subdir
            sub_safetensors = [
                f for f in os.listdir(subdir) if f.endswith(".safetensors")
            ]
            if len(sub_safetensors) >= 1:
                largest = max(
                    sub_safetensors,
                    key=lambda f: os.path.getsize(os.path.join(subdir, f)),
                )
                return "single_file", os.path.join(subdir, largest)

    return "unknown", model_path


def _read_model_index_class_name(model_path: str) -> Optional[str]:
    """Return the diffusers pipeline class declared in model_index.json, if present."""
    if not model_path or not os.path.isdir(model_path):
        return None

    model_index = os.path.join(model_path, "model_index.json")
    if not os.path.isfile(model_index):
        return None

    try:
        with open(model_index, "r", encoding="utf-8") as fh:
            payload = json.load(fh)
        class_name = payload.get("_class_name")
        if isinstance(class_name, str) and class_name.strip():
            return class_name.strip()
    except Exception as exc:
        print(f"WARNING: failed to read model_index.json from {model_path}: {exc}")
        sys.stdout.flush()
    return None


def _model_index_is_flux(class_name: Optional[str]) -> bool:
    if not class_name:
        return False
    return class_name in ("FluxPipeline", "FluxFillPipeline")


def _load_single_file(checkpoint_path: str, model_id: str, dtype, vae=None):
    """Load a pipeline from a single safetensors checkpoint (CivitAI format).

    Tries a FLUX pipeline first when the model id indicates FLUX; otherwise falls
    back to the SDXL / SD 1.5 heuristics used for CivitAI-style checkpoints.
    """
    file_size_gb = os.path.getsize(checkpoint_path) / (1024**3)
    print(
        f"Loading single-file checkpoint: {os.path.basename(checkpoint_path)} ({file_size_gb:.1f} GB)"
    )
    sys.stdout.flush()

    sf_kwargs = {"torch_dtype": dtype, "use_safetensors": True}
    flux_kwargs = dict(sf_kwargs)
    if vae is not None:
        sf_kwargs["vae"] = vae
    config_override = _single_file_config_override()
    if config_override:
        sf_kwargs["config"] = config_override
        flux_kwargs["config"] = config_override
        print(f"Using single-file config override: {config_override}")

    order = []
    override = _single_file_pipeline_override()
    strict = _single_file_strict()

    explicit_map = {
        "flux": ("FLUX", FluxPipeline, flux_kwargs),
        "flux-fill": ("FLUX Fill", FluxFillPipeline, flux_kwargs),
        "sdxl": ("SDXL", StableDiffusionXLPipeline, sf_kwargs),
        "sd15": ("SD 1.5", StableDiffusionPipeline, sf_kwargs),
    }
    if override:
        selected = explicit_map.get(override)
        if selected is None:
            raise RuntimeError(
                "Unsupported SINGLE_FILE_PIPELINE="
                f"{override!r}; expected one of {sorted(explicit_map.keys())}"
            )
        order.append(selected)
        print(f"Using single-file pipeline override: {override}")

    if not order and _is_flux(model_id):
        if PIPELINE_MODE == "inpainting":
            order.append(("FLUX Fill", FluxFillPipeline, flux_kwargs))
        else:
            order.append(("FLUX", FluxPipeline, flux_kwargs))

    if not strict:
        seen = {item[1] for item in order}
        if file_size_gb > 4.0:
            heuristic = [
                ("SDXL", StableDiffusionXLPipeline, sf_kwargs),
                ("SD 1.5", StableDiffusionPipeline, sf_kwargs),
            ]
        else:
            heuristic = [
                ("SD 1.5", StableDiffusionPipeline, sf_kwargs),
                ("SDXL", StableDiffusionXLPipeline, sf_kwargs),
            ]
        for item in heuristic:
            if item[1] not in seen:
                order.append(item)
    elif len(order) == 0:
        raise RuntimeError("SINGLE_FILE_STRICT requires SINGLE_FILE_PIPELINE")

    last_err = None
    for label, pipeline_cls, load_kwargs in order:
        try:
            print(f"  Trying {label} pipeline ({pipeline_cls.__name__})...")
            sys.stdout.flush()
            pipe = pipeline_cls.from_single_file(checkpoint_path, **load_kwargs)
            print(f"  Loaded as {label} pipeline")
            return pipe
        except Exception as e:
            print(f"  {label} failed: {e}")
            last_err = e

    raise RuntimeError(f"Could not load single-file checkpoint: {last_err}")


def load_pipeline(model_id: str):
    """Load or reload the diffusion pipeline."""
    global pipeline, current_model, current_model_family

    if pipeline is not None and current_model == model_id:
        return pipeline

    # Check for local model path first (e.g., /models mounted from PVC)
    local_model_path = os.environ.get("LOCAL_MODEL_PATH", "/models")
    resolved_model_id = model_id
    use_local = False
    if os.path.isfile(local_model_path):
        print(f"Found local single-file model at: {local_model_path}")
        resolved_model_id = local_model_path
        use_local = True
    elif os.path.isdir(local_model_path) and os.listdir(local_model_path):
        print(f"Found local model at: {local_model_path}")
        resolved_model_id = local_model_path
        use_local = True

    print(f"Loading model: {resolved_model_id} (id: {model_id})")
    sys.stdout.flush()

    # FP16 is faster and uses less VRAM, but the default SDXL VAE can produce NaN
    # artifacts (graininess) in fp16 without the madebyollin/sdxl-vae-fp16-fix.
    # Default to fp32 for safety; set USE_FP16=1 when VAE_PATH points to the fixed VAE.
    is_rocm = hasattr(torch.version, "hip") and torch.version.hip is not None
    fp16_default = "0" if is_rocm else "1"
    use_fp16 = os.environ.get("USE_FP16", fp16_default) == "1"
    dtype = torch.float16 if use_fp16 else torch.float32
    print(f"Using dtype: {dtype}")
    print(f"USE_FP16 env: {os.environ.get('USE_FP16', 'not set')}")

    # Load fixed VAE when available. Prefer a staged local directory beside the
    # model root, but fall back to the configured repo id when the local path is
    # absent. This keeps unified runtimes robust across Local/SharedPVC layout
    # differences and avoids gray/washed-out SDXL decodes on AMD.
    vae = None
    vae_path = os.environ.get("VAE_PATH", "").strip()
    vae_repo = os.environ.get("VAE_REPO", "").strip()

    def _candidate_vae_paths(raw_path: str, model_root: str) -> list[str]:
        candidates = []
        if not raw_path:
            return candidates

        trimmed = raw_path.strip()
        if os.path.isabs(trimmed):
            candidates.append(trimmed)
            if model_root:
                candidates.append(os.path.join(model_root, ".vae", os.path.basename(trimmed)))
        else:
            if model_root:
                candidates.append(os.path.join(model_root, trimmed))
            candidates.append(trimmed)
        return candidates

    local_vae_path = None
    for candidate in _candidate_vae_paths(vae_path, local_model_path):
        if os.path.isdir(candidate):
            local_vae_path = candidate
            break

    if local_vae_path:
        print(f"Loading fixed VAE from staged path: {local_vae_path}")
        sys.stdout.flush()
        try:
            vae = AutoencoderKL.from_pretrained(
                local_vae_path,
                torch_dtype=dtype,
                local_files_only=True,
            )
            print("Fixed VAE loaded successfully from local path")
        except Exception as e:
            print(f"WARNING: Failed to load fixed VAE from local path: {e}")
            print("Will try repo fallback if configured")
            vae = None
    elif vae_path:
        print(f"WARNING: VAE_PATH set but no staged directory found: {vae_path}")

    if vae is None and vae_repo:
        print(f"Loading fixed VAE from repo fallback: {vae_repo}")
        sys.stdout.flush()
        try:
            vae = AutoencoderKL.from_pretrained(
                vae_repo,
                torch_dtype=dtype,
            )
            print("Fixed VAE loaded successfully from repo fallback")
        except Exception as e:
            print(f"WARNING: Failed to load fixed VAE from repo fallback: {e}")
            print("Falling back to model's default VAE (may crash in fp16)")

    # CRITICAL: On ROCm gfx1100, do NOT call any torch.cuda operations before
    # the model is fully loaded on CPU. Early GPU context initialization causes
    # SIGSEGV crashes during model loading.
    print(f"GPU available: {torch.cuda.is_available()}")
    sys.stdout.flush()

    # Detect model format: diffusers (multi-folder) or single-file (CivitAI)
    model_format, resolved_path = _detect_model_format(resolved_model_id)
    print(f"Detected model format: {model_format}")
    sys.stdout.flush()

    model_index_class = None
    if model_format == "diffusers":
        model_index_class = _read_model_index_class_name(resolved_path)
        if model_index_class:
            print(f"Detected diffusers pipeline class: {model_index_class}")
            sys.stdout.flush()

    if model_format == "single_file":
        # Single safetensors checkpoint (CivitAI, etc.)
        pipeline = _load_single_file(resolved_path, model_id, dtype, vae=vae)
    else:
        # Standard diffusers format (from_pretrained)
        # When model files are local, skip network access for speed and offline safety.
        # When resolved_model_id is a HF repo ID (no local files), allow downloading.
        local_only = use_local or os.environ.get("LOCAL_FILES_ONLY", "0") == "1"
        pipeline_kwargs = {
            "torch_dtype": dtype,
            "use_safetensors": True,
            "local_files_only": local_only,
            "safety_checker": None,
            "low_cpu_mem_usage": True,
        }
        # When using fp16, request the fp16 variant of safetensors directly.
        # This avoids loading fp32 weights and casting, saving load time.
        # Safe: diffusers 0.25+ falls back to the default variant if fp16 files don't exist.
        if use_fp16:
            pipeline_kwargs["variant"] = "fp16"
        if vae is not None:
            pipeline_kwargs["vae"] = vae

        print(f"Pipeline mode: {PIPELINE_MODE}")

        # Quantization mode — read early so it's available for all pipeline paths.
        # Prevents NameError when non-FLUX pipelines check quant_mode for offload decisions.
        quant_mode = os.environ.get("QUANTIZATION", "").lower()

        # Prefer the cached diffusers metadata when present; repo-name heuristics are only
        # a fallback for remote or legacy layouts without model_index.json.
        detected_family = _model_family(
            model_id=model_id,
            model_index_class=model_index_class,
        )
        is_flux_pipeline = detected_family == "flux"

        # FLUX pipelines use explicit classes and do not use safety_checker or custom VAE overrides.
        if is_flux_pipeline:
            flux_kwargs = {
                k: v
                for k, v in pipeline_kwargs.items()
                if k not in ("safety_checker", "vae", "variant")
            }
            # Optional NF4 quantization via bitsandbytes (reduces ~24GB FP16 to ~6GB NF4)
            _flux_nf4 = False
            if quant_mode == "nf4":
                try:
                    from diffusers import BitsAndBytesConfig as DiffusersBnBConfig
                    from diffusers.quantizers import PipelineQuantizationConfig

                    # BNB NF4 compute dtype — controls dequantization precision.
                    # bfloat16: ~2x speedup on gfx1100 (123 vs 61 TFLOPS).
                    # float32: safe fallback if bf16 produces artifacts.
                    _bnb_dtype_name = os.environ.get(
                        "BNB_COMPUTE_DTYPE", "bfloat16"
                    ).lower()
                    _bnb_dtype_map = {
                        "float32": torch.float32,
                        "bfloat16": torch.bfloat16,
                        "float16": torch.float16,
                    }
                    _bnb_compute_dtype = _bnb_dtype_map.get(
                        _bnb_dtype_name, torch.float32
                    )
                    bnb_config = DiffusersBnBConfig(
                        load_in_4bit=True,
                        bnb_4bit_compute_dtype=_bnb_compute_dtype,
                        bnb_4bit_quant_type="nf4",
                    )
                    print(
                        f"NF4 BNB compute dtype: {_bnb_dtype_name} → {_bnb_compute_dtype}"
                    )
                    flux_kwargs["quantization_config"] = PipelineQuantizationConfig(
                        quant_mapping={"transformer": bnb_config}
                    )
                    # Match torch_dtype to compute_dtype so all non-quantized layers
                    # (norms, projections, embeddings) have matching dtypes.
                    # bfloat16 is safe for T5-XXL (same exponent range as float32,
                    # max ~3.4e38 — only float16 overflows at ~65504).
                    # float32 fallback: load everything as float32, then downcast
                    # text encoders to bfloat16 post-load to save VRAM.
                    if _bnb_compute_dtype == torch.bfloat16:
                        flux_kwargs["torch_dtype"] = torch.bfloat16
                    else:
                        flux_kwargs["torch_dtype"] = torch.float32
                    _flux_nf4 = True
                    print(
                        f"NF4 quantization enabled for FLUX pipeline "
                        f"(compute dtype: {_bnb_dtype_name}, "
                        f"load dtype: {flux_kwargs['torch_dtype']})"
                    )
                except (ImportError, Exception) as e:
                    print(
                        f"WARNING: NF4 quantization not available ({e}), loading without quantization"
                    )
            if PIPELINE_MODE == "inpainting":
                print("Loading FluxFillPipeline (loading to CPU, local files only)...")
                sys.stdout.flush()
                pipeline = FluxFillPipeline.from_pretrained(
                    resolved_path,
                    **flux_kwargs,
                )
            else:
                print("Loading FluxPipeline (loading to CPU, local files only)...")
                sys.stdout.flush()
                pipeline = FluxPipeline.from_pretrained(
                    resolved_path,
                    **flux_kwargs,
                )
            # NF4 post-load dtype fixups (only needed for float32 loading path).
            # When torch_dtype matches compute_dtype (e.g. both bfloat16),
            # all layers already have consistent dtypes — no fixup needed.
            if _flux_nf4 and flux_kwargs.get("torch_dtype") == torch.float32:
                # Downcast text encoders to bfloat16 to save VRAM and prevent
                # T5-XXL overflow (float16 max ~65504 is too small).
                for enc_attr in ("text_encoder", "text_encoder_2"):
                    enc = getattr(pipeline, enc_attr, None)
                    if enc is not None:
                        enc.to(torch.bfloat16)
                        param_mb = (
                            sum(p.numel() * p.element_size() for p in enc.parameters())
                            / 1024**2
                        )
                        print(f"Downcast {enc_attr} to bfloat16 ({param_mb:.0f} MB)")
                # Wrap encode_prompt to cast bfloat16 text embeddings → compute dtype
                # so the transformer receives matching dtypes for Q/K/V projections.
                _target_dtype = _bnb_compute_dtype
                _orig_encode = pipeline.encode_prompt

                def _encode_prompt_cast(*args, _target=_target_dtype, **kwargs):
                    result = _orig_encode(*args, **kwargs)
                    return tuple(
                        (
                            r.to(_target)
                            if isinstance(r, torch.Tensor) and r.dtype == torch.bfloat16
                            else r
                        )
                        for r in result
                    )

                pipeline.encode_prompt = _encode_prompt_cast
                print(f"Wrapped encode_prompt to cast bfloat16 → {_bnb_dtype_name}")
        elif PIPELINE_MODE == "inpainting":
            print(
                "Loading StableDiffusionXLInpaintPipeline (loading to CPU, local files only)..."
            )
            sys.stdout.flush()
            pipeline = StableDiffusionXLInpaintPipeline.from_pretrained(
                resolved_path,
                **pipeline_kwargs,
            )
        elif PIPELINE_MODE == "instruct":
            print(
                "Loading StableDiffusionInstructPix2PixPipeline (loading to CPU, local files only)..."
            )
            sys.stdout.flush()
            # InstructPix2Pix does not use safety_checker kwarg
            instruct_kwargs = {
                k: v for k, v in pipeline_kwargs.items() if k != "safety_checker"
            }
            pipeline = StableDiffusionInstructPix2PixPipeline.from_pretrained(
                resolved_path,
                **instruct_kwargs,
            )
        else:
            try:
                print(
                    "Attempting AutoPipelineForText2Image (loading to CPU, local files only)..."
                )
                if vae is not None:
                    print("  Using custom VAE (madebyollin/sdxl-vae-fp16-fix)")
                sys.stdout.flush()
                AutoPipelineForText2Image = _lazy_auto_pipeline_text2image()
                pipeline = AutoPipelineForText2Image.from_pretrained(
                    resolved_path,
                    requires_safety_checker=False,
                    **pipeline_kwargs,
                )
            except Exception as e:
                print(f"AutoPipeline failed: {e}, trying DiffusionPipeline...")
                sys.stdout.flush()
                pipeline = DiffusionPipeline.from_pretrained(
                    resolved_path,
                    **pipeline_kwargs,
                )

    loaded_family = _model_family(
        model_id=model_id,
        pipe=pipeline,
        model_index_class=model_index_class,
    )

    print("Pipeline loaded to CPU, preparing GPU transfer...")
    sys.stdout.flush()

    _apply_startup_lora(pipeline, loaded_family)

    # Now that model is safely on CPU, we can initialize GPU context
    if torch.cuda.is_available():
        print(f"Initializing GPU context...")
        torch.cuda.empty_cache()
        torch.cuda.synchronize()
        print(
            f"GPU memory after cache clear: {torch.cuda.memory_allocated() / 1024**3:.2f} GB"
        )
        sys.stdout.flush()

    # Determine whether to use CPU offloading.
    # USE_CPU_OFFLOAD env: "1" = force on, "0" = force off, unset = auto-detect.
    # Auto-detect: SDXL pipelines use CPU offload by default on ROCm/gfx1100
    # because bulk .to("cuda") triggers memory access faults.
    offload_env = os.environ.get("USE_CPU_OFFLOAD", "")
    if offload_env == "1":
        use_cpu_offload = True
    elif offload_env == "0":
        use_cpu_offload = False
    else:
        # Auto-detect: use CPU offload for SDXL and FLUX pipelines on ROCm
        is_sdxl = "XL" in type(pipeline).__name__
        is_flux_pipe = "Flux" in type(pipeline).__name__
        is_rocm = hasattr(torch.version, "hip") and torch.version.hip is not None
        use_cpu_offload = (is_sdxl or is_flux_pipe) and is_rocm
        if use_cpu_offload:
            print(
                f"Auto-detected {type(pipeline).__name__} on ROCm, enabling CPU offload"
            )

    # NF4/bitsandbytes: CPU offload is no longer forced. BNB dispatch hooks handle
    # per-layer GPU placement even without enable_model_cpu_offload(). With NF4 FLUX
    # at ~15.4GB, the model fits in 24GB VRAM without offload, avoiding the ~20-30%
    # step overhead from CPU↔GPU transfers. Set cpuOffload: "true" in the model CR
    # to re-enable if OOM occurs on smaller GPUs.
    if quant_mode == "nf4" and not use_cpu_offload:
        print(
            "NF4 quantization detected: skipping forced CPU offload (model fits in VRAM)"
        )

    if use_cpu_offload and torch.cuda.is_available():
        try:
            print("Enabling model CPU offload (safer for gfx1100)...")
            sys.stdout.flush()
            pipeline.enable_model_cpu_offload()
            print("CPU offload enabled successfully")
            print(
                f"GPU memory after offload setup: {torch.cuda.memory_allocated() / 1024**3:.2f} GB"
            )
        except Exception as e:
            print(f"CPU offload failed: {e}, trying sequential offload...")
            try:
                pipeline.enable_sequential_cpu_offload()
                print("Sequential CPU offload enabled")
            except Exception as e2:
                print(
                    f"Sequential offload also failed: {e2}, falling back to full GPU transfer"
                )
                try:
                    pipeline = pipeline.to("cuda")
                    torch.cuda.synchronize()
                    print(
                        f"GPU memory after load: {torch.cuda.memory_allocated() / 1024**3:.2f} GB"
                    )
                except Exception as e3:
                    print(f"GPU transfer failed: {e3}, using CPU only")
                    pipeline = pipeline.to("cpu")
    elif quant_mode == "nf4" and torch.cuda.is_available():
        # BNB-quantized models can't use pipeline.to("cuda") — the accelerate
        # dispatch hooks on the transformer conflict with bulk .to(). Move
        # non-quantized components (text encoders, VAE) to GPU individually.
        # The BNB transformer's dispatch hooks handle GPU placement on forward pass.
        print("NF4 model: moving non-quantized components to GPU individually...")
        sys.stdout.flush()
        for comp_name in ["text_encoder", "text_encoder_2", "vae"]:
            comp = getattr(pipeline, comp_name, None)
            if comp is not None:
                try:
                    comp.to("cuda")
                    print(f"  Moved {comp_name} to GPU")
                except Exception as e:
                    print(f"  Warning: could not move {comp_name} to GPU: {e}")
        torch.cuda.synchronize()
        print(
            f"GPU memory after component transfer: {torch.cuda.memory_allocated() / 1024**3:.2f} GB"
        )
        # Enable VAE tiling for NF4 to reduce peak decode memory on consecutive generations
        if hasattr(pipeline, "vae"):
            pipeline.vae.enable_tiling()
            print("Enabled VAE tiling for NF4 mode (reduces peak decode memory)")
    else:
        # Move to GPU with explicit synchronization
        try:
            print("Moving model to GPU (this may take a moment)...")
            sys.stdout.flush()
            pipeline = pipeline.to("cuda")
            torch.cuda.synchronize()
            print(
                f"GPU memory after load: {torch.cuda.memory_allocated() / 1024**3:.2f} GB"
            )
        except Exception as e:
            print(f"ERROR moving to GPU: {e}")
            print(f"Exception type: {type(e).__name__}")
            import traceback

            traceback.print_exc()
            print("Falling back to CPU...")
            sys.stdout.flush()
            pipeline = pipeline.to("cpu")

    # Optional memory optimizations (trade speed for memory).
    # Defaults are OFF for performance; enable explicitly via env.
    if os.environ.get("ENABLE_ATTENTION_SLICING", "0") == "1":
        try:
            if hasattr(pipeline, "enable_attention_slicing"):
                pipeline.enable_attention_slicing(
                    os.environ.get("ATTENTION_SLICING", "max")
                )
                print(
                    f"Enabled attention slicing ({os.environ.get('ATTENTION_SLICING', 'max')})"
                )
        except Exception as e:
            print(f"Could not enable attention slicing: {e}")

    if os.environ.get("ENABLE_VAE_SLICING", "0") == "1":
        try:
            if hasattr(pipeline, "enable_vae_slicing"):
                pipeline.enable_vae_slicing()
                print("Enabled VAE slicing")
        except Exception as e:
            print(f"Could not enable VAE slicing: {e}")

    _apply_compile_controls(pipeline, loaded_family, use_cpu_offload)

    # Apply scheduler override if configured
    _apply_scheduler(pipeline, None)

    current_model = model_id
    current_model_family = loaded_family
    print(f"Model loaded successfully: {resolved_model_id} (id: {model_id})")
    if current_model_family:
        print(f"  Model family: {current_model_family}")
    print(f"  Scheduler: {type(pipeline.scheduler).__name__}")
    print(f"  Default steps: {_default_steps(model_id)}")
    print(f"  Default guidance: {_default_guidance_scale(model_id)}")
    neg = _default_negative_prompt()
    if neg:
        print(f"  Default negative prompt: {neg[:80]}...")
    sys.stdout.flush()
    return pipeline


def _parse_warmup_resolutions():
    """Parse warmup resolutions from env vars.

    Priority:
      1. WARMUP_RESOLUTIONS (comma-separated WxH, e.g. "512x512,1024x1024")
      2. Legacy WARMUP_WIDTH / WARMUP_HEIGHT (single resolution)
      3. Default 512x512
    """
    res_str = os.environ.get("WARMUP_RESOLUTIONS", "")
    if res_str:
        resolutions = []
        for entry in res_str.split(","):
            entry = entry.strip()
            if not entry:
                continue
            try:
                w, h = map(int, entry.split("x"))
                resolutions.append((w, h))
            except ValueError:
                print(f"WARNING: skipping invalid warmup resolution '{entry}'")
        if resolutions:
            return resolutions

    # Legacy fallback
    w = int(os.environ.get("WARMUP_WIDTH", "512"))
    h = int(os.environ.get("WARMUP_HEIGHT", "512"))
    return [(w, h)]


def warmup_inference():
    """Run tiny dummy inference at each configured resolution to compile
    ROCm GPU kernels (MIOpen/Triton). MIOpen kernels are resolution-specific,
    so warming up at multiple resolutions eliminates the first-request
    recompilation penalty. With a persistent compilation cache, subsequent
    container starts skip recompilation entirely.
    """
    global pipeline
    if pipeline is None or os.environ.get("SKIP_WARMUP", "0") == "1":
        return

    resolutions = _parse_warmup_resolutions()
    warmup_steps = 4
    warmup_guidance = _default_guidance_scale(current_model)
    print(
        f"Running warmup inference at {len(resolutions)} resolution(s): "
        f"{', '.join(f'{w}x{h}' for w, h in resolutions)} "
        f"(steps={warmup_steps}, guidance={warmup_guidance})"
    )
    sys.stdout.flush()

    for w, h in resolutions:
        t0 = time.time()
        try:
            gen = torch.Generator(device="cpu").manual_seed(42)
            kw = {
                "prompt": "warmup",
                "num_inference_steps": warmup_steps,
                "generator": gen,
                "height": h,
                "width": w,
                "guidance_scale": warmup_guidance,
            }
            if PIPELINE_MODE == "inpainting":
                kw["image"] = Image.new("RGB", (w, h), (128, 128, 128))
                kw["mask_image"] = Image.new("L", (w, h), 255)
                if not _pipeline_is_flux_like(pipeline, current_model):
                    kw["strength"] = 0.5
            elif PIPELINE_MODE == "instruct":
                kw["image"] = Image.new("RGB", (w, h), (128, 128, 128))
            with torch.inference_mode():
                pipeline(**kw)
            if torch.cuda.is_available():
                torch.cuda.synchronize()
            elapsed = time.time() - t0
            print(f"  Warmup {w}x{h} complete in {elapsed:.1f}s")
        except Exception as e:
            elapsed = time.time() - t0
            print(f"  Warmup {w}x{h} failed after {elapsed:.1f}s (non-fatal): {e}")

        # Free VRAM between resolutions to avoid OOM on the next size
        gc.collect()
        if torch.cuda.is_available():
            torch.cuda.empty_cache()
        sys.stdout.flush()

    print("All warmup passes complete")
    sys.stdout.flush()


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Startup: check GPU and preload model if specified
    check_gpu()
    model_id = os.environ.get("MODEL_ID", os.environ.get("MODEL", ""))
    if model_id:
        load_pipeline(model_id)
        warmup_inference()
    yield
    # Shutdown
    global pipeline
    pipeline = None


app = FastAPI(title="Diffusers API", lifespan=lifespan)

# Serialize generation requests to avoid GPU OOM from concurrent inference
_generation_lock = asyncio.Lock()


@app.get("/health")
async def health():
    return {"status": "healthy", "model": current_model, "gpu": gpu_info}


@app.get("/v1/models")
async def list_models():
    models = []
    if current_model:
        models.append(
            {
                "id": current_model,
                "object": "model",
                "created": int(time.time()),
                "owned_by": "diffusers",
            }
        )
    return {"object": "list", "data": models}


@app.post("/v1/images/generations")
async def generate_images(request: ImageGenerationRequest):
    global pipeline

    # Prioritize environment variable (set by FlexInfer ModelDeployment) over request model
    # request.model may be an alias like "image-gen" rather than the actual HuggingFace model ID
    model_id = os.environ.get("MODEL_ID") or os.environ.get("MODEL") or request.model
    if not model_id:
        raise HTTPException(status_code=400, detail="No model specified")

    # Load pipeline if needed
    pipe = load_pipeline(model_id)

    steps = (
        request.num_inference_steps
        if request.num_inference_steps is not None
        else _default_steps(model_id)
    )
    guidance_scale = (
        request.guidance_scale
        if request.guidance_scale is not None
        else _default_guidance_scale(model_id)
    )
    negative_prompt = request.negative_prompt or _default_negative_prompt()

    # Parse size
    try:
        width, height = map(int, request.size.split("x"))
    except ValueError:
        width, height = 1024, 1024

    def _run_inference():
        """Run blocking pipeline inference in a thread so the event loop stays responsive."""
        # FluxFillPipeline is an inpainting model — route text2image through
        # a blank image + full mask so a single Fill model can serve both
        # /v1/images/generations and /v1/images/edits endpoints.
        is_fill = isinstance(pipe, FluxFillPipeline)
        results = []
        for _ in range(request.n):
            with torch.inference_mode():
                if is_fill:
                    # Text2image via fill: blank canvas + full mask = generate from scratch
                    blank = Image.new("RGB", (width, height), (128, 128, 128))
                    mask = Image.new("L", (width, height), 255)
                    gen_kwargs = {
                        "prompt": request.prompt,
                        "image": blank,
                        "mask_image": mask,
                        "num_inference_steps": steps,
                        "guidance_scale": guidance_scale,
                        "height": height,
                        "width": width,
                    }
                else:
                    gen_kwargs = {
                        "prompt": request.prompt,
                        "num_inference_steps": steps,
                        "guidance_scale": guidance_scale,
                        "width": width,
                        "height": height,
                    }
                    # FLUX doesn't support negative_prompt
                    if not _pipeline_is_flux_like(pipe, model_id) and negative_prompt:
                        gen_kwargs["negative_prompt"] = negative_prompt
                result = pipe(**gen_kwargs)
            img = result.images[0]
            buffer = io.BytesIO()
            img.save(buffer, format="PNG")
            b64_data = base64.b64encode(buffer.getvalue()).decode("utf-8")
            results.append(ImageData(b64_json=b64_data))
            del result, img, buffer  # break reference cycles
        # Critical: gc.collect() BEFORE empty_cache() to free PyTorch tensors
        # held by Python reference cycles from the diffusers pipeline + CPU offload hooks.
        # Without this, consecutive generations leak ~10-20GB of host RAM.
        gc.collect()
        if torch.cuda.is_available():
            torch.cuda.empty_cache()
        return results

    # Run generation in a thread pool so health checks keep responding
    async with _generation_lock:
        images = await asyncio.to_thread(_run_inference)

    return ImageGenerationResponse(created=int(time.time()), data=images)


@app.post("/v1/images/edits")
async def edit_images(
    prompt: str = Form(...),
    image: Optional[UploadFile] = File(None),
    mask: Optional[UploadFile] = File(None),
    model: Optional[str] = Form(None),
    n: int = Form(1),
    size: Optional[str] = Form(None),
    response_format: str = Form("b64_json"),
    num_inference_steps: Optional[int] = Form(None),
    guidance_scale: Optional[float] = Form(None),
    strength: Optional[float] = Form(None),
    image_guidance_scale: Optional[float] = Form(None),
    negative_prompt: Optional[str] = Form(None),
):
    """OpenAI-compatible image editing endpoint.

    Supports inpainting (mask-based) and instruction-based editing
    depending on PIPELINE_MODE. When no image is provided, falls back
    to text-to-image generation (blank canvas + full mask).
    """
    global pipeline

    model_id = os.environ.get("MODEL_ID") or os.environ.get("MODEL") or model
    if not model_id:
        raise HTTPException(status_code=400, detail="No model specified")

    pipe = load_pipeline(model_id)

    if PIPELINE_MODE not in ("inpainting", "instruct"):
        raise HTTPException(
            status_code=400,
            detail=f"PIPELINE_MODE '{PIPELINE_MODE}' does not support /v1/images/edits. "
            f"Use 'inpainting' or 'instruct'.",
        )

    # Read uploaded files before entering the thread
    image_bytes = await image.read() if image else None
    mask_bytes = await mask.read() if mask else None

    steps = (
        num_inference_steps
        if num_inference_steps is not None
        else _default_steps(model_id)
    )
    cfg_scale = (
        guidance_scale
        if guidance_scale is not None
        else _default_guidance_scale(model_id)
    )
    neg = negative_prompt or _default_negative_prompt()

    def _run_edit():
        # No image provided: fall back to text-to-image generation
        # using blank canvas + full mask (same as /v1/images/generations)
        if image_bytes is None:
            try:
                w, h = map(int, size.lower().split("x")) if size else (1024, 1024)
            except ValueError:
                w, h = 1024, 1024
            blank = Image.new("RGB", (w, h), (128, 128, 128))
            full_mask = Image.new("L", (w, h), 255)
            results = []
            for _ in range(n):
                with torch.inference_mode():
                    if _pipeline_is_flux_like(pipe, model_id):
                        result = pipe(
                            prompt=prompt,
                            image=blank,
                            mask_image=full_mask,
                            num_inference_steps=steps,
                            guidance_scale=cfg_scale,
                            height=h,
                            width=w,
                        )
                    else:
                        s = strength if strength is not None else _default_strength()
                        result = pipe(
                            prompt=prompt,
                            image=blank,
                            mask_image=full_mask,
                            strength=s,
                            guidance_scale=cfg_scale,
                            num_inference_steps=steps,
                            negative_prompt=neg,
                        )
                img = result.images[0]
                buffer = io.BytesIO()
                img.save(buffer, format="PNG")
                b64_data = base64.b64encode(buffer.getvalue()).decode("utf-8")
                results.append(ImageData(b64_json=b64_data))
                del result, img, buffer
            gc.collect()
            if torch.cuda.is_available():
                torch.cuda.empty_cache()
            return results

        src_img = _decode_image(image_bytes)
        src_img = _resize_for_pipeline(src_img, size)

        results = []
        for _ in range(n):
            with torch.inference_mode():
                if PIPELINE_MODE == "inpainting":
                    if mask_bytes is None:
                        raise ValueError("Inpainting mode requires a mask image")
                    mask_img = _decode_mask(mask_bytes, src_img.size)
                    if _pipeline_is_flux_like(pipe, model_id):
                        # FluxFillPipeline: no strength, no negative_prompt
                        result = pipe(
                            prompt=prompt,
                            image=src_img,
                            mask_image=mask_img,
                            guidance_scale=cfg_scale,
                            num_inference_steps=steps,
                            height=src_img.size[1],
                            width=src_img.size[0],
                        )
                    else:
                        s = strength if strength is not None else _default_strength()
                        result = pipe(
                            prompt=prompt,
                            image=src_img,
                            mask_image=mask_img,
                            strength=s,
                            guidance_scale=cfg_scale,
                            num_inference_steps=steps,
                            negative_prompt=neg,
                        )
                elif PIPELINE_MODE == "instruct":
                    img_scale = (
                        image_guidance_scale
                        if image_guidance_scale is not None
                        else _default_image_guidance_scale()
                    )
                    result = pipe(
                        prompt=prompt,
                        image=src_img,
                        guidance_scale=cfg_scale,
                        image_guidance_scale=img_scale,
                        num_inference_steps=steps,
                    )

            img = result.images[0]
            buffer = io.BytesIO()
            img.save(buffer, format="PNG")
            b64_data = base64.b64encode(buffer.getvalue()).decode("utf-8")
            results.append(ImageData(b64_json=b64_data))
            del result, img, buffer  # break reference cycles
        # Critical: gc.collect() BEFORE empty_cache() — see _run_inference comment.
        gc.collect()
        if torch.cuda.is_available():
            torch.cuda.empty_cache()
        return results

    async with _generation_lock:
        try:
            images = await asyncio.to_thread(_run_edit)
        except ValueError as e:
            raise HTTPException(status_code=400, detail=str(e))

    return ImageGenerationResponse(created=int(time.time()), data=images)


if __name__ == "__main__":
    import uvicorn

    port = int(os.environ.get("PORT", 8000))
    uvicorn.run(app, host="0.0.0.0", port=port)
