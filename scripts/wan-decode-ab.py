#!/usr/bin/env python3
"""S9 harness: the Wan decode A/B on the 0.32 runtime base, the VACE VAE kill-test
and the Wan denoise-cache kill-test (slice S9 of
.loom/10-research-backend-currency-gfx1100-gfx906-2026-11-04.md).

Pass rules, written before any run: rows gfx1100-wan-decode-ab-v032-2026-11-05,
gfx1100-wan-vace-vae-bypass-2026-11-05 and gfx1100-wan-denoise-cache-2026-11-05
in .loom/60-validation-matrix.md. Procedure:
.loom/32-iteration-plan-s9-wan-decode-ab-2026-11-05.md.

Subcommands:
  run      Inside a serving image on the XTX (torch and the GPU). Loads the
           lane's pipeline through a server-diffusers.py and renders fixed-seed
           clips the way /v1/videos/generations does. Saves timings, memory
           peaks, the final latents, uint8 frames and the MP4 of each clip.
           With --decode it also decodes saved latents alone. Python 3.10+.
  compare  numpy only. PSNR and SSIM between two frame files.
  verdict  pure Python. Applies a row's pass rule to the run and compare JSONs.
"""

from __future__ import annotations

import argparse
import base64
import importlib.util
import json
import math
import os
import resource
import sys
import time
from pathlib import Path

PROMPTS = {
    # The KT-FLEX / KT-JOBS request (scripts/kt-jobs.py VIDEO_PROMPT).
    "boat": "a paper boat drifting down a rain gutter, shallow depth of field",
    "fox": "a red fox trotting through fresh snow at dusk, cinematic lighting",
    "rain": "gentle rain on a tin roof with distant thunder",
}

MIB = 1024 * 1024
BYPASS_LINE = "Wan VAE encode/decode run with MIOpen off"
FP32_KEEP_LINE = "Keeping MIOpen for the Wan VAE in torch.float32"


# --------------------------------------------------------------------------
# run (needs torch)
# --------------------------------------------------------------------------


def _load_server(path: str):
    spec = importlib.util.spec_from_file_location("wan_ab_server", path)
    module = importlib.util.module_from_spec(spec)
    sys.modules["wan_ab_server"] = module
    spec.loader.exec_module(module)
    return module


def _versions() -> dict:
    out = {"python": sys.version.split()[0]}
    for name in ("torch", "diffusers", "transformers", "accelerate"):
        try:
            out[name] = __import__(name).__version__
        except Exception as exc:  # pragma: no cover - recorded, not fatal
            out[name] = f"unavailable: {exc}"
    try:
        import torch

        out["hip"] = getattr(torch.version, "hip", None)
    except Exception:
        pass
    return out


def _sha256(path: str):
    import hashlib

    try:
        return hashlib.sha256(Path(path).read_bytes()).hexdigest()
    except OSError:
        return None


def _maxrss_mib() -> float:
    return round(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024, 1)


def _cgroup_peak_mib():
    for path in (
        "/sys/fs/cgroup/memory.peak",
        "/sys/fs/cgroup/memory/memory.max_usage_in_bytes",
    ):
        try:
            return round(int(Path(path).read_text().strip()) / MIB, 1)
        except Exception:
            continue
    return None


class _VaeClock:
    """Times every vae.encode / vae.decode call, outermost (bypass included)."""

    def __init__(self, torch):
        self.torch = torch
        self.reset()

    def reset(self):
        self.seconds = {"encode": 0.0, "decode": 0.0}
        self.calls = {"encode": 0, "decode": 0}

    def wrap(self, vae):
        for name in ("encode", "decode"):
            if hasattr(vae, name):
                setattr(vae, name, self._timed(name, getattr(vae, name)))

    def _timed(self, name, method):
        torch = self.torch

        def call(*args, **kwargs):
            torch.cuda.synchronize()
            started = time.perf_counter()
            try:
                return method(*args, **kwargs)
            finally:
                torch.cuda.synchronize()
                self.seconds[name] += time.perf_counter() - started
                self.calls[name] += 1

        return call

    def snapshot(self):
        return {
            "encode_seconds": round(self.seconds["encode"], 3),
            "decode_seconds": round(self.seconds["decode"], 3),
            "encode_calls": self.calls["encode"],
            "decode_calls": self.calls["decode"],
        }


def _frames_to_uint8(frames):
    import numpy as np

    arr = np.asarray(frames, dtype=np.float32)
    nan_count = int(np.isnan(arr).sum())
    arr = np.nan_to_num(arr, nan=0.0)
    return (np.clip(arr, 0.0, 1.0) * 255.0).round().astype(np.uint8), nan_count


def _peak(torch):
    return (
        round(torch.cuda.max_memory_allocated() / MIB, 1),
        round(torch.cuda.max_memory_reserved() / MIB, 1),
    )


def _run_video_clip(srv, pipe, model_id, clip, args, out: Path, clock, t0):
    """One clip, the same call and timings as server-diffusers.py _run_inference."""
    import numpy as np
    import torch

    width, height = srv._parse_video_size(args.size)
    num_frames = srv._validate_video_num_frames(args.frames)
    fps = srv._default_video_fps()
    steps = args.steps
    guidance_scale = srv._default_guidance_scale(model_id)
    negative_prompt = srv._resolve_negative_prompt(None)
    prompt = PROMPTS[clip["prompt"]]
    seed = clip["seed"]
    tag = f"{args.label}-{clip['id']}"
    print(
        f"[wan-ab] clip {tag} size={width}x{height} frames={num_frames} steps={steps} "
        f"guidance={guidance_scale} seed={seed}",
        flush=True,
    )

    condition = None
    if srv.PIPELINE_MODE == "image2video":
        if not args.keyframe:
            raise SystemExit("--keyframe is required for image2video")
        image = srv._decode_image(Path(args.keyframe).read_bytes())
        condition = srv._prepare_vace_i2v_condition(image, width, height, num_frames)

    torch.cuda.empty_cache()
    torch.cuda.reset_peak_memory_stats()
    clock.reset()
    marks = {"wall_started": time.time()}
    state = {"last_step_at": None, "latents": None, "denoise_peak": None}
    started_at = time.perf_counter()

    def on_step_end(_pipe, step, _timestep, callback_kwargs):
        if step + 1 == steps:
            torch.cuda.synchronize()
            state["last_step_at"] = time.perf_counter()
            marks["wall_last_step"] = time.time()
            state["denoise_peak"] = _peak(torch)
            torch.cuda.reset_peak_memory_stats()
            state["latents"] = callback_kwargs["latents"].detach().to("cpu", copy=True)
        return callback_kwargs

    kwargs = {
        "prompt": prompt,
        "num_frames": num_frames,
        "num_inference_steps": steps,
        "guidance_scale": guidance_scale,
        "height": height,
        "width": width,
        "generator": torch.Generator(device="cpu").manual_seed(seed),
        "callback_on_step_end": on_step_end,
        "callback_on_step_end_tensor_inputs": ["latents"],
    }
    if negative_prompt:
        kwargs["negative_prompt"] = negative_prompt
    if condition is not None:
        kwargs["video"], kwargs["mask"] = condition
    with torch.inference_mode():
        result = pipe(**kwargs)
    torch.cuda.synchronize()
    returned_at = time.perf_counter()
    marks["wall_returned"] = time.time()
    last_step_at = state["last_step_at"] or returned_at
    decode_peak = _peak(torch)

    from diffusers.utils import export_to_video

    mp4 = out / f"{tag}.mp4"
    export_to_video(
        result.frames[0], output_video_path=str(mp4), fps=fps, macro_block_size=16
    )
    exported_at = time.perf_counter()
    encoded = base64.b64encode(mp4.read_bytes())
    encoded_at = time.perf_counter()
    timings = {
        "denoise_seconds": round(last_step_at - started_at, 3),
        "decode_postprocess_seconds": round(returned_at - last_step_at, 3),
        "pipeline_seconds": round(returned_at - started_at, 3),
        "export_seconds": round(exported_at - returned_at, 3),
        "encode_seconds": round(encoded_at - exported_at, 3),
        "total_seconds": round(encoded_at - started_at, 3),
    }
    print(f"[video-timing] {json.dumps(timings, sort_keys=True)}", flush=True)

    frames, nan_count = _frames_to_uint8(result.frames[0])
    frames_file = out / f"{tag}.frames.npz"
    np.savez_compressed(frames_file, frames=frames)
    latents_file = out / f"{tag}.latents.pt"
    if state["latents"] is not None:
        torch.save(state["latents"], latents_file)
    if clip.get("keyframe_out"):
        from PIL import Image

        Image.fromarray(frames[0]).save(out / clip["keyframe_out"])
    del result
    return {
        "id": clip["id"],
        "prompt": clip["prompt"],
        "seed": seed,
        "frames": num_frames,
        "steps": steps,
        "guidance": guidance_scale,
        "size": f"{width}x{height}",
        "timings": timings,
        "vae": clock.snapshot(),
        "peak_alloc_mib": {
            "denoise": (state["denoise_peak"] or decode_peak)[0],
            "decode": decode_peak[0],
        },
        "peak_reserved_mib": {
            "denoise": (state["denoise_peak"] or decode_peak)[1],
            "decode": decode_peak[1],
        },
        "nan_count": nan_count,
        "frames_shape": list(frames.shape),
        "frames_file": frames_file.name,
        "latents_file": latents_file.name if state["latents"] is not None else None,
        "mp4_file": mp4.name,
        "mp4_bytes": len(encoded) * 3 // 4,
        "maxrss_mib": _maxrss_mib(),
        "since_t0_seconds": round(time.time() - t0, 3),
        "marks": marks,
    }


def _decode_only(pipe, latents_path: Path, tag: str, out: Path, clock):
    """vae.decode on saved latents, the WanPipeline / WanVACEPipeline tail."""
    import numpy as np
    import torch

    latents = torch.load(latents_path, map_location="cpu")
    vae = pipe.vae
    torch.cuda.empty_cache()
    torch.cuda.reset_peak_memory_stats()
    clock.reset()
    marks = {"wall_started": time.time()}
    torch.cuda.synchronize()
    started = time.perf_counter()
    with torch.inference_mode():
        z = latents.to("cuda", dtype=vae.dtype)
        mean = (
            torch.tensor(vae.config.latents_mean)
            .view(1, vae.config.z_dim, 1, 1, 1)
            .to(z.device, z.dtype)
        )
        std = 1.0 / torch.tensor(vae.config.latents_std).view(
            1, vae.config.z_dim, 1, 1, 1
        ).to(z.device, z.dtype)
        video = vae.decode(z / std + mean, return_dict=False)[0]
        frames = pipe.video_processor.postprocess_video(video, output_type="np")[0]
    torch.cuda.synchronize()
    seconds = time.perf_counter() - started
    marks["wall_finished"] = time.time()
    pipe.maybe_free_model_hooks()
    frames_u8, nan_count = _frames_to_uint8(frames)
    frames_file = out / f"{tag}.frames.npz"
    np.savez_compressed(frames_file, frames=frames_u8)
    print(f"[wan-ab] decode {tag}: {seconds:.3f} s", flush=True)
    return {
        "id": tag,
        "latents": latents_path.name,
        "seconds": round(seconds, 3),
        "vae": clock.snapshot(),
        "peak_alloc_mib": _peak(torch)[0],
        "peak_reserved_mib": _peak(torch)[1],
        "nan_count": nan_count,
        "frames_shape": list(frames_u8.shape),
        "frames_file": frames_file.name,
        "maxrss_mib": _maxrss_mib(),
        "marks": marks,
    }


def _run_audio(srv, pipe, args, out: Path, t0, render_id: str = "a0"):
    """One Stable Audio render, the same call as server-diffusers.py generate_audio."""
    import io
    import wave

    import torch

    steps = args.steps
    negative_prompt = srv._resolve_negative_prompt(None)
    torch.cuda.reset_peak_memory_stats()
    started = time.perf_counter()
    marks = {"wall_started": time.time()}
    gen = torch.Generator(device=pipe.device).manual_seed(args.audio_seed)
    audio = pipe(
        prompt=PROMPTS[args.audio_prompt],
        negative_prompt=negative_prompt,
        num_inference_steps=steps,
        audio_end_in_s=float(args.audio_seconds),
        num_waveforms_per_prompt=1,
        generator=gen,
    ).audios[0]
    torch.cuda.synchronize()
    denoise = time.perf_counter() - started
    pcm = audio.T.float().cpu().clamp(-1, 1)
    rate = int(pipe.vae.sampling_rate)
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(pcm.shape[1] if pcm.ndim == 2 else 1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes((pcm * 32767).to(torch.int16).numpy().tobytes())
    total = time.perf_counter() - started
    wav = out / f"{args.label}-{render_id}.wav"
    wav.write_bytes(buf.getvalue())
    marks["wall_finished"] = time.time()
    return {
        "id": render_id,
        "prompt": args.audio_prompt,
        "seed": args.audio_seed,
        "seconds_requested": args.audio_seconds,
        "duration": round(len(pcm) / rate, 3),
        "sample_rate": rate,
        "nan_count": int(torch.isnan(audio.float()).sum()),
        "timings": {
            "denoise_seconds": round(denoise, 3),
            "total_seconds": round(total, 3),
        },
        "peak_alloc_mib": _peak(torch)[0],
        "wav_file": wav.name,
        "maxrss_mib": _maxrss_mib(),
        "since_t0_seconds": round(time.time() - t0, 3),
        "marks": marks,
    }


def _parse_clip(text: str) -> dict:
    parts = text.split(":")
    if len(parts) < 3 or parts[1] not in PROMPTS:
        raise argparse.ArgumentTypeError(
            f"--clip ID:PROMPT:SEED[:keyframe-out.png], got {text!r}"
        )
    clip = {"id": parts[0], "prompt": parts[1], "seed": int(parts[2])}
    if len(parts) > 3:
        clip["keyframe_out"] = parts[3]
    return clip


def cmd_run(args) -> int:
    process_start = time.time()
    t0 = args.t0 or process_start
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)
    record = {
        "label": args.label,
        "server": args.server,
        "server_sha256": _sha256(args.server),
        "image": os.environ.get("WAN_AB_IMAGE"),
        "t0": t0,
        "process_start": process_start,
        "env": {
            k: os.environ.get(k)
            for k in sorted(os.environ)
            if k.startswith(_ENV_PREFIXES)
        },
        "clips": [],
        "decodes": [],
    }
    result_path = out / f"{args.label}.json"

    def save():
        record["maxrss_mib"] = _maxrss_mib()
        record["cgroup_memory_peak_mib"] = _cgroup_peak_mib()
        result_path.write_text(json.dumps(record, indent=2, sort_keys=True))

    import torch

    record["versions"] = _versions()
    props = torch.cuda.get_device_properties(0)
    record["device"] = {"name": props.name, "total_mib": round(props.total_memory / MIB)}
    if props.total_memory < 20 * 1024 * MIB:
        # ROCR_VISIBLE_DEVICES=0 must select the 24 GiB Navi 31, not the iGPU.
        record["error"] = f"wrong device: {record['device']}"
        result_path.write_text(json.dumps(record, indent=2, sort_keys=True))
        raise SystemExit(record["error"])
    srv = _load_server(args.server)
    record["import_seconds"] = round(time.time() - process_start, 3)
    model_id = args.model_id or os.environ.get("MODEL_ID") or os.environ.get("MODEL")
    record["model_id"] = model_id
    record["pipeline_mode"] = srv.PIPELINE_MODE
    load_started = time.time()
    pipe = srv.load_pipeline(model_id)
    record["load_seconds"] = round(time.time() - load_started, 3)
    record["load_marks"] = {"wall_started": load_started, "wall_finished": time.time()}
    vae = getattr(pipe, "vae", None)
    decode_fn = getattr(vae, "decode", None)
    record["vae_dtype"] = str(getattr(vae, "dtype", None))
    record["vae_decode_qualname"] = getattr(decode_fn, "__qualname__", None)
    record["vae_use_tiling"] = getattr(vae, "use_tiling", None)
    record["transformer_class"] = type(getattr(pipe, "transformer", None)).__name__
    save()

    clock = _VaeClock(torch)
    if vae is not None:
        clock.wrap(vae)
    try:
        if srv.PIPELINE_MODE == "text2audio":
            for i in range(args.audio_renders):
                record["clips"].append(_run_audio(srv, pipe, args, out, t0, f"a{i}"))
                save()
        else:
            for clip in args.clip:
                record["clips"].append(
                    _run_video_clip(srv, pipe, model_id, clip, args, out, clock, t0)
                )
                save()
            for item in args.decode:
                path, _, tag = item.partition(":")
                record["decodes"].append(
                    _decode_only(
                        pipe,
                        Path(path),
                        f"{args.label}-{tag or Path(path).stem}",
                        out,
                        clock,
                    )
                )
                save()
    except Exception as exc:
        record["error"] = f"{type(exc).__name__}: {exc}"
        save()
        raise
    save()
    print(f"[wan-ab] wrote {result_path}", flush=True)
    return 0


_ENV_PREFIXES = (
    "COMPILE_",
    "DIFFUSERS_",
    "DEFAULT_",
    "ENABLE_VAE",
    "HIP_",
    "HSA_",
    "MIOPEN_",
    "MODEL",
    "PIPELINE_",
    "PYTORCH_",
    "ROCR_",
    "TORCH",
    "USE_CPU",
    "WAN_",
)


# --------------------------------------------------------------------------
# compare (numpy only)
# --------------------------------------------------------------------------


def _gauss(size: int = 11, sigma: float = 1.5):
    import numpy as np

    x = np.arange(size, dtype=np.float64) - size // 2
    k = np.exp(-(x**2) / (2 * sigma**2))
    return k / k.sum()


def _filter2(img, k):
    import numpy as np

    win = np.lib.stride_tricks.sliding_window_view(img, k.size, axis=1) @ k
    return np.lib.stride_tricks.sliding_window_view(win, k.size, axis=0) @ k


def ssim_luma(a, b) -> float:
    """SSIM (Wang et al. 2004) on BT.601 luma, 11x11 Gaussian window, sigma 1.5."""
    import numpy as np

    w = np.array([0.299, 0.587, 0.114])
    x = a.astype(np.float64) @ w
    y = b.astype(np.float64) @ w
    k = _gauss()
    c1, c2 = (0.01 * 255) ** 2, (0.03 * 255) ** 2
    mx, my = _filter2(x, k), _filter2(y, k)
    sxx = _filter2(x * x, k) - mx * mx
    syy = _filter2(y * y, k) - my * my
    sxy = _filter2(x * y, k) - mx * my
    s = ((2 * mx * my + c1) * (2 * sxy + c2)) / (
        (mx * mx + my * my + c1) * (sxx + syy + c2)
    )
    return float(s.mean())


def psnr(a, b) -> float:
    import numpy as np

    mse = float(np.mean((a.astype(np.float64) - b.astype(np.float64)) ** 2))
    return math.inf if mse == 0 else 10 * math.log10(255.0**2 / mse)


def frame_stats(frames) -> dict:
    import numpy as np

    luma = frames.astype(np.float64) @ np.array([0.299, 0.587, 0.114])
    means = luma.reshape(luma.shape[0], -1).mean(axis=1)
    stds = luma.reshape(luma.shape[0], -1).std(axis=1)
    return {
        "frames": int(frames.shape[0]),
        "height": int(frames.shape[1]),
        "width": int(frames.shape[2]),
        "luma_mean_min": round(float(means.min()), 2),
        "luma_mean_max": round(float(means.max()), 2),
        "luma_std_min": round(float(stds.min()), 2),
    }


def compare_frames(a, b) -> dict:
    if a.shape != b.shape:
        return {
            "shape_a": list(a.shape),
            "shape_b": list(b.shape),
            "error": "shape mismatch",
        }
    p = [psnr(a[i], b[i]) for i in range(a.shape[0])]
    s = [ssim_luma(a[i], b[i]) for i in range(a.shape[0])]
    finite = [v for v in p if math.isfinite(v)]
    identical = all(math.isinf(v) for v in p)
    return {
        "frames": int(a.shape[0]),
        "identical": identical,
        "psnr_mean": (
            math.inf if identical else round(sum(min(v, 100.0) for v in p) / len(p), 3)
        ),
        "psnr_min": math.inf if identical else round(min(finite), 3),
        "ssim_mean": round(sum(s) / len(s), 5),
        "ssim_min": round(min(s), 5),
        "stats_a": frame_stats(a),
        "stats_b": frame_stats(b),
    }


def cmd_compare(args) -> int:
    import numpy as np

    a = np.load(args.a)["frames"]
    b = np.load(args.b)["frames"]
    result = {"a": args.a, "b": args.b, **compare_frames(a, b)}
    text = json.dumps(result, indent=2, sort_keys=True, default=str)
    if args.json:
        Path(args.json).write_text(text)
    print(text)
    return 0


# --------------------------------------------------------------------------
# verdict (pure Python)
# --------------------------------------------------------------------------


def _bar(name, value, limit, ok, note=""):
    return {"bar": name, "value": value, "limit": limit, "pass": bool(ok), "note": note}


def _clips(run, ids):
    by_id = {c["id"]: c for c in run.get("clips", [])}
    return [by_id[i] for i in ids if i in by_id]


def _mean(values):
    values = list(values)
    return sum(values) / len(values) if values else math.nan


def _sane(stats) -> bool:
    return (
        stats["luma_std_min"] >= 5
        and 8 <= stats["luma_mean_min"]
        and stats["luma_mean_max"] <= 247
    )


def verdict_t2v(a, b, host, sims) -> dict:
    """Row gfx1100-wan-decode-ab-v032-2026-11-05, T2V bars 1-11 and validity."""
    bars = []
    warm = ["c1", "c2"]
    a_warm, b_warm = _clips(a, warm), _clips(b, warm)
    a_dec = _mean(c["timings"]["decode_postprocess_seconds"] for c in a_warm)
    b_dec = _mean(c["timings"]["decode_postprocess_seconds"] for c in b_warm)
    a_tot = _mean(c["timings"]["total_seconds"] for c in a_warm)
    b_tot = _mean(c["timings"]["total_seconds"] for c in b_warm)
    bars.append(
        _bar(
            "1 bypass active on B",
            {
                "log_line": host.get("b_bypass_line"),
                "decode_qualname": b.get("vae_decode_qualname"),
            },
            BYPASS_LINE,
            host.get("b_bypass_line")
            and "without_miopen" in str(b.get("vae_decode_qualname")),
        )
    )
    bars.append(
        _bar(
            "2 warm decode",
            round(b_dec, 3),
            f"<= 1.5 x {a_dec:.3f} and <= 15 s",
            b_dec <= 1.5 * a_dec and b_dec <= 15,
        )
    )
    bars.append(
        _bar(
            "3 warm total",
            round(b_tot, 3),
            f"<= 1.25 x {a_tot:.3f}",
            b_tot <= 1.25 * a_tot,
        )
    )
    cold = host.get("b_cold_clip_seconds")
    bars.append(
        _bar(
            "4 cold clip, warm compile cache",
            cold,
            "<= 270 s",
            cold is not None and cold <= 270,
        )
    )
    first = host.get("b_first_cold_seconds")
    bars.append(
        _bar(
            "5 first-ever cold clip",
            first,
            "<= 900 s",
            first is not None and first <= 900,
        )
    )
    a_vram, b_vram = host.get("a_vram_peak_mib"), host.get("b_vram_peak_mib")
    bars.append(
        _bar(
            "6 peak card VRAM",
            b_vram,
            f"<= 18000 MiB and <= {a_vram} + 1024 MiB",
            b_vram is not None
            and a_vram is not None
            and b_vram <= 18000
            and b_vram <= a_vram + 1024,
        )
    )
    rss = max(
        [b.get("maxrss_mib") or 0]
        + [c.get("maxrss_mib") or 0 for c in b.get("clips", [])]
    )
    bars.append(_bar("7 B peak RSS", rss, "<= 40960 MiB", 0 < rss <= 40960))
    outputs_ok = True
    detail = []
    for run in (a, b):
        for c in run.get("clips", []):
            stats = sims.get("stats", {}).get(f"{run['label']}-{c['id']}")
            ok = (
                c["nan_count"] == 0
                and c["frames_shape"][:3] == [33, 480, 832]
                and stats is not None
                and _sane(stats)
                and host.get("ffprobe", {}).get(f"{run['label']}-{c['id']}")
                == ["h264", 33]
            )
            outputs_ok &= ok
            if not ok:
                detail.append(f"{run['label']}-{c['id']}")
    bars.append(
        _bar(
            "8 outputs valid",
            detail or "all",
            "33x480x832, no NaN, H.264 33 frames, not degenerate",
            outputs_ok,
        )
    )
    dec = sims.get("decode_equivalence") or {}
    bars.append(
        _bar(
            "9 decode equivalence (same latents)",
            {k: dec.get(k) for k in ("psnr_mean", "psnr_min", "ssim_mean")},
            "PSNR mean >= 35, min >= 32, SSIM mean >= 0.98",
            dec.get("psnr_mean", 0) >= 35
            and dec.get("psnr_min", 0) >= 32
            and dec.get("ssim_mean", 0) >= 0.98,
        )
    )
    e2e = [sims.get(f"e2e_{cid}") or {} for cid in warm]
    bars.append(
        _bar(
            "10 end-to-end same seed",
            [{k: s.get(k) for k in ("psnr_mean", "ssim_mean")} for s in e2e],
            "each clip: PSNR mean >= 22, SSIM mean >= 0.80",
            all(
                s.get("psnr_mean", 0) >= 22 and s.get("ssim_mean", 0) >= 0.80
                for s in e2e
            ),
        )
    )
    kernel = host.get("kernel_error_lines")
    bars.append(
        _bar("11 host kernel log quiet", kernel, "0 amdgpu/SMU/AER lines", kernel == 0)
    )

    validity = []
    validity.append(
        _bar(
            "V1 A warm total vs 11-04 live 124 s",
            round(a_tot, 3),
            "105-143 s",
            105 <= a_tot <= 143,
        )
    )
    validity.append(
        _bar("V2 A warm decode", round(a_dec, 3), "4-11 s", 4 <= a_dec <= 11)
    )
    own = sims.get("a_decode_only_vs_pipeline") or {}
    validity.append(
        _bar(
            "V3 A decode-only reproduces A's pipeline frames",
            own.get("psnr_mean"),
            "identical or PSNR mean >= 50",
            own.get("identical") or own.get("psnr_mean", 0) >= 50,
        )
    )
    return _summarize("t2v", bars, validity, soft={"10 end-to-end same seed"})


def verdict_vace(a, b, host, sims) -> dict:
    """Row gfx1100-wan-decode-ab-v032-2026-11-05, VACE bar 12 (13 report-only)."""
    bars = []
    a_warm, b_warm = _clips(a, ["v1"]), _clips(b, ["v1"])
    a_tot = _mean(c["timings"]["total_seconds"] for c in a_warm)
    b_tot = _mean(c["timings"]["total_seconds"] for c in b_warm)
    bars.append(
        _bar(
            "12a fp32 VAE keeps MIOpen on B",
            host.get("b_vace_keep_line"),
            FP32_KEEP_LINE,
            bool(host.get("b_vace_keep_line")),
        )
    )
    bars.append(
        _bar(
            "12b warm total",
            round(b_tot, 3),
            f"<= 1.25 x {a_tot:.3f}",
            b_tot <= 1.25 * a_tot,
        )
    )
    ok = True
    for run in (a, b):
        for c in run.get("clips", []):
            stats = sims.get("stats", {}).get(f"{run['label']}-{c['id']}")
            ok &= (
                c["nan_count"] == 0
                and c["frames_shape"][:3] == [33, 480, 832]
                and stats is not None
                and _sane(stats)
                and host.get("ffprobe", {}).get(f"{run['label']}-{c['id']}")
                == ["h264", 33]
            )
    bars.append(
        _bar(
            "12c outputs valid",
            ok,
            "33x480x832, no NaN, H.264 33 frames, not degenerate",
            ok,
        )
    )
    bars.append(
        _bar(
            "12d no ftfy NameError",
            host.get("b_vace_name_error", 0),
            "0",
            host.get("b_vace_name_error", 0) == 0,
        )
    )
    b_vram = host.get("b_vace_vram_peak_mib")
    bars.append(
        _bar(
            "12e peak card VRAM",
            b_vram,
            "<= 18000 MiB",
            b_vram is not None and b_vram <= 18000,
        )
    )
    rss = max(
        [b.get("maxrss_mib") or 0]
        + [c.get("maxrss_mib") or 0 for c in b.get("clips", [])]
    )
    bars.append(_bar("12f B peak RSS", rss, "<= 40960 MiB", 0 < rss <= 40960))
    kernel = host.get("kernel_error_lines")
    bars.append(_bar("12g host kernel log quiet", kernel, "0", kernel == 0))
    report = sims.get("e2e_v1") or {}
    return _summarize(
        "vace",
        bars,
        [],
        report={
            "13 e2e v1 (report only)": {
                k: report.get(k) for k in ("psnr_mean", "ssim_mean")
            }
        },
    )


def verdict_vace_vae(v0, variant, host, sims, name) -> dict:
    """Row gfx1100-wan-vace-vae-bypass-2026-11-05 for one variant against V0."""
    bars = []
    v0_clip = _clips(v0, ["v1"])
    var_clip = _clips(variant, ["v1"])
    v0_dec = _mean(c["timings"]["decode_postprocess_seconds"] for c in v0_clip)
    var_dec = _mean(c["timings"]["decode_postprocess_seconds"] for c in var_clip)
    v0_tot = _mean(c["timings"]["total_seconds"] for c in v0_clip)
    var_tot = _mean(c["timings"]["total_seconds"] for c in var_clip)
    bars.append(
        _bar(
            "K1 decode",
            round(var_dec, 3),
            f"<= 0.25 x {v0_dec:.3f} and <= 30 s",
            var_dec <= 0.25 * v0_dec and var_dec <= 30,
        )
    )
    bars.append(
        _bar(
            "K2 clip total",
            round(var_tot, 3),
            f"<= 0.75 x {v0_tot:.3f}",
            var_tot <= 0.75 * v0_tot,
        )
    )
    dec = sims.get(f"decode_{name}") or {}
    bars.append(
        _bar(
            "K3 decode quality (same latents as V0)",
            {k: dec.get(k) for k in ("psnr_mean", "psnr_min", "ssim_mean")},
            "PSNR mean >= 33, min >= 30, SSIM mean >= 0.95",
            dec.get("psnr_mean", 0) >= 33
            and dec.get("psnr_min", 0) >= 30
            and dec.get("ssim_mean", 0) >= 0.95,
        )
    )
    nan = sum(c["nan_count"] for c in var_clip) + sum(
        d["nan_count"] for d in variant.get("decodes", [])
    )
    bars.append(_bar("K4 no NaN", nan, "0", nan == 0))
    vram = host.get(f"{name}_vram_peak_mib")
    bars.append(
        _bar(
            "K5 peak card VRAM",
            vram,
            "<= 18000 MiB",
            vram is not None and vram <= 18000,
        )
    )
    kernel = host.get("kernel_error_lines")
    bars.append(_bar("K6 host kernel log quiet", kernel, "0", kernel == 0))
    return _summarize(f"vace-vae-{name}", bars, [])


def verdict_cache(base, cached, sims, name) -> dict:
    """Row gfx1100-wan-denoise-cache-2026-11-05 for one cache setting."""
    bars = []
    ids = ["c1", "c2"]
    b_den = _mean(c["timings"]["denoise_seconds"] for c in _clips(base, ids))
    c_den = _mean(c["timings"]["denoise_seconds"] for c in _clips(cached, ids))
    bars.append(
        _bar(
            "C1 warm denoise",
            round(c_den, 3),
            f"<= 0.75 x {b_den:.3f}",
            c_den <= 0.75 * b_den,
        )
    )
    per = [sims.get(f"cache_{name}_{cid}") or {} for cid in ids]
    bars.append(
        _bar(
            "C2 quality vs uncached, same seed",
            [
                {k: s.get(k) for k in ("psnr_mean", "ssim_mean", "ssim_min")}
                for s in per
            ],
            "each clip: PSNR mean >= 28, SSIM mean >= 0.90, SSIM min >= 0.85",
            all(
                s.get("psnr_mean", 0) >= 28
                and s.get("ssim_mean", 0) >= 0.90
                and s.get("ssim_min", 0) >= 0.85
                for s in per
            ),
        )
    )
    nan = sum(c["nan_count"] for c in cached.get("clips", []))
    bars.append(_bar("C3 no NaN", nan, "0", nan == 0))
    return _summarize(f"cache-{name}", bars, [])


def verdict_audio(a, b, host) -> dict:
    """Row gfx1100-wan-decode-ab-v032-2026-11-05, Stable Audio bars SA1-SA6 (runbook D7 offline)."""
    bars = []
    a_warm, b_warm = _clips(a, ["a1"]), _clips(b, ["a1"])
    a_tot = _mean(c["timings"]["total_seconds"] for c in a_warm)
    b_tot = _mean(c["timings"]["total_seconds"] for c in b_warm)
    bars.append(_bar("SA1 warm render", round(b_tot, 3), f"<= 1.25 x {a_tot:.3f}", b_tot <= 1.25 * a_tot))
    durations = [(c["duration"], c["seconds_requested"]) for c in b.get("clips", [])]
    bars.append(
        _bar(
            "SA2 duration matches the request",
            durations,
            "|duration - requested| <= 0.05 s",
            bool(durations) and all(abs(d - r) <= 0.05 for d, r in durations),
        )
    )
    bars.append(
        _bar(
            "SA3 scheduler loads, no dummy-object error",
            {"scheduler": host.get("b_sa_scheduler_line"), "dummy_errors": host.get("b_sa_dummy_errors")},
            "CosineDPMSolverMultistepScheduler, 0",
            bool(host.get("b_sa_scheduler_line")) and host.get("b_sa_dummy_errors", 1) == 0,
        )
    )
    nan = sum(c["nan_count"] for c in b.get("clips", []))
    bars.append(_bar("SA4 no NaN", nan, "0", nan == 0))
    vram = host.get("b_sa_vram_peak_mib")
    rss = max([b.get("maxrss_mib") or 0] + [c.get("maxrss_mib") or 0 for c in b.get("clips", [])])
    bars.append(
        _bar(
            "SA5 memory",
            {"vram_mib": vram, "rss_mib": rss},
            "VRAM <= 7000 MiB (lane estimate), RSS <= 40960 MiB",
            vram is not None and vram <= 7000 and 0 < rss <= 40960,
        )
    )
    kernel = host.get("kernel_error_lines")
    bars.append(_bar("SA6 host kernel log quiet", kernel, "0", kernel == 0))
    return _summarize("stable-audio", bars, [])


def _summarize(name, bars, validity, soft=frozenset(), report=None) -> dict:
    valid = all(v["pass"] for v in validity)
    hard_miss = [b["bar"] for b in bars if not b["pass"] and b["bar"] not in soft]
    soft_miss = [b["bar"] for b in bars if not b["pass"] and b["bar"] in soft]
    if not valid:
        decision = "report-not-judged"
    elif hard_miss:
        decision = "fail"
    elif soft_miss:
        decision = "conditional"
    else:
        decision = "pass"
    out = {"row_part": name, "decision": decision, "bars": bars, "validity": validity}
    if report:
        out["report"] = report
    return out


def cmd_verdict(args) -> int:
    def load(path):
        return json.loads(Path(path).read_text()) if path else {}

    host, sims = load(args.host), load(args.sims)
    if args.part == "t2v":
        result = verdict_t2v(load(args.a), load(args.b), host, sims)
    elif args.part == "vace":
        result = verdict_vace(load(args.a), load(args.b), host, sims)
    elif args.part == "vace-vae":
        result = verdict_vace_vae(load(args.a), load(args.b), host, sims, args.name)
    elif args.part == "audio":
        result = verdict_audio(load(args.a), load(args.b), host)
    else:
        result = verdict_cache(load(args.a), load(args.b), sims, args.name)
    print(json.dumps(result, indent=2, default=str))
    return 0 if result["decision"] in ("pass", "conditional") else 1


# --------------------------------------------------------------------------
# collect (numpy + ffprobe, on the Mac after copying the run root back)
# --------------------------------------------------------------------------

KERNEL_ERROR_RE = (
    r"amdgpu.*(error|fail|timeout|reset|fault|hang)|smu.*(error|fail|timeout)|"
    r"\bAER\b|pcieport.*(error|fail)|ring .* timeout|mes.*(fail|timeout|hang)"
)


def _first_line(path: Path, needle: str):
    if not path.exists():
        return None
    for line in path.read_text(errors="replace").splitlines():
        if needle in line:
            return line.strip()
    return None


def _count_lines(path: Path, needle: str) -> int:
    if not path.exists():
        return 0
    return sum(needle in line for line in path.read_text(errors="replace").splitlines())


def vram_peak_mib(root: Path, phase: str):
    """Peak of the card's mem_info_vram_used over the phase, minus the idle baseline."""
    tsv = root / f"vram-{phase}.tsv"
    if not tsv.exists():
        return None
    used = []
    for line in tsv.read_text().splitlines():
        parts = line.split()
        if len(parts) == 2 and parts[1].isdigit():
            used.append(int(parts[1]))
    if not used:
        return None
    base_file = root / f"baseline-{phase}.txt"
    baseline = int(base_file.read_text().split()[0]) if base_file.exists() else 0
    return round((max(used) - baseline) / MIB, 1)


def _ffprobe(mp4: Path):
    import subprocess

    cmd = [
        "ffprobe", "-v", "error", "-select_streams", "v:0",
        "-show_entries", "stream=codec_name,nb_frames", "-of", "json", str(mp4),
    ]
    try:
        out = subprocess.run(cmd, check=True, capture_output=True, text=True).stdout
        stream = json.loads(out)["streams"][0]
        return [stream.get("codec_name"), int(stream.get("nb_frames", 0))]
    except Exception as exc:
        return [f"error: {exc}", 0]


def _load_run(root: Path, phase: str):
    path = root / phase / f"{phase}.json"
    return json.loads(path.read_text()) if path.exists() else None


def _since(runs, phase, cid):
    for c in (runs.get(phase) or {}).get("clips", []):
        if c["id"] == cid:
            return c.get("since_t0_seconds")
    return None


def collect(root: Path) -> dict:
    import re

    import numpy as np

    runs = {d.name: _load_run(root, d.name) for d in sorted(root.iterdir()) if d.is_dir()}
    runs = {k: v for k, v in runs.items() if v}
    frames = {}
    sims = {"stats": {}}
    host = {"ffprobe": {}, "vram_peak_mib": {}}
    for phase, run in runs.items():
        host["vram_peak_mib"][phase] = vram_peak_mib(root, phase)
        for item in run.get("clips", []) + run.get("decodes", []):
            tag = item["id"] if item["id"].startswith(phase) else f"{phase}-{item['id']}"
            if item.get("frames_file"):
                arr = np.load(root / phase / item["frames_file"])["frames"]
                frames[tag] = arr
                sims["stats"][tag] = frame_stats(arr)
            if item.get("mp4_file"):
                host["ffprobe"][tag] = _ffprobe(root / phase / item["mp4_file"])

    def cmp(key, a, b):
        if a in frames and b in frames:
            result = compare_frames(frames[a], frames[b])
            result.pop("stats_a", None)
            result.pop("stats_b", None)
            sims[key] = {**result, "a": a, "b": b}

    cmp("decode_equivalence", "t2v-A-c1", "t2v-B-xA")
    cmp("a_decode_only_vs_pipeline", "t2v-A-c1", "t2v-A-own")
    cmp("e2e_c1", "t2v-A-c1", "t2v-B-c1")
    cmp("e2e_c2", "t2v-A-c2", "t2v-B-c2")
    cmp("determinism_a", "t2v-A-c0", "t2v-A-c1")
    cmp("determinism_b", "t2v-B-c0", "t2v-B-c1")
    cmp("e2e_v1", "vace-A-v1", "vace-B-v1")
    for name in ("bf16", "fp32"):
        cmp(f"decode_{name}", "vace-B-v1", f"vace-B-{name}-x0")
        cmp(f"e2e_{name}", "vace-B-v1", f"vace-B-{name}-v1")
    cache_phases = [p for p in runs if p.startswith("cache-B-")]
    for phase in cache_phases:
        name = phase[len("cache-B-"):]
        for cid in ("c1", "c2"):
            cmp(f"cache_{name}_{cid}", f"t2v-B-{cid}", f"{phase}-{cid}")

    pattern = re.compile(KERNEL_ERROR_RE, re.IGNORECASE)
    kernel_lines = 0
    for log in root.glob("kernel-*.log"):
        lines = log.read_text(errors="replace").splitlines()
        kernel_lines += sum(bool(pattern.search(line)) for line in lines)
    vram = host["vram_peak_mib"]
    host.update(
        {
            "a_vram_peak_mib": vram.get("t2v-A"),
            "b_vram_peak_mib": vram.get("t2v-B"),
            "a_first_cold_seconds": _since(runs, "t2v-A", "c0"),
            "b_first_cold_seconds": _since(runs, "t2v-B", "c0"),
            "a_cold_clip_seconds": _since(runs, "t2v-A-cold", "k"),
            "b_cold_clip_seconds": _since(runs, "t2v-B-cold", "k"),
            "a_bypass_line": _first_line(root / "t2v-A" / "run.log", BYPASS_LINE),
            "b_bypass_line": _first_line(root / "t2v-B" / "run.log", BYPASS_LINE),
            "b_vace_keep_line": _first_line(root / "vace-B" / "run.log", FP32_KEEP_LINE),
            "b_vace_name_error": _count_lines(root / "vace-B" / "run.log", "NameError"),
            "b_vace_vram_peak_mib": vram.get("vace-B"),
            "bf16_vram_peak_mib": vram.get("vace-B-bf16"),
            "fp32_vram_peak_mib": vram.get("vace-B-fp32"),
            "b_sa_scheduler_line": _first_line(
                root / "sa-B" / "run.log", "CosineDPMSolverMultistepScheduler"
            ),
            "b_sa_dummy_errors": _count_lines(root / "sa-B" / "run.log", "torchsde"),
            "b_sa_vram_peak_mib": vram.get("sa-B"),
            "kernel_error_lines": kernel_lines,
        }
    )
    verdicts = {}
    if "t2v-A" in runs and "t2v-B" in runs:
        verdicts["t2v"] = verdict_t2v(runs["t2v-A"], runs["t2v-B"], host, sims)
    if "vace-A" in runs and "vace-B" in runs:
        verdicts["vace"] = verdict_vace(runs["vace-A"], runs["vace-B"], host, sims)
    for name in ("bf16", "fp32"):
        if "vace-B" in runs and f"vace-B-{name}" in runs:
            verdicts[f"vace-vae-{name}"] = verdict_vace_vae(
                runs["vace-B"], runs[f"vace-B-{name}"], host, sims, name
            )
    for phase in cache_phases:
        if "t2v-B" in runs:
            name = phase[len("cache-B-"):]
            verdicts[f"cache-{name}"] = verdict_cache(runs["t2v-B"], runs[phase], sims, name)
    if "sa-A" in runs and "sa-B" in runs:
        verdicts["stable-audio"] = verdict_audio(runs["sa-A"], runs["sa-B"], host)
    return {"host": host, "sims": sims, "verdicts": verdicts}


def cmd_collect(args) -> int:
    root = Path(args.root)
    result = collect(root)
    for name in ("host", "sims"):
        text = json.dumps(result[name], indent=2, sort_keys=True, default=str)
        (root / f"{name}.json").write_text(text)
    (root / "verdicts.json").write_text(json.dumps(result["verdicts"], indent=2, default=str))
    for name, v in result["verdicts"].items():
        print(f"{name}: {v['decision']}")
        for bar in v["validity"] + v["bars"]:
            mark = "PASS" if bar["pass"] else "MISS"
            print(f"  {mark}  {bar['bar']}: {bar['value']} ({bar['limit']})")
    return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    sub = parser.add_subparsers(dest="cmd", required=True)

    run = sub.add_parser("run")
    run.add_argument("--server", required=True)
    run.add_argument("--out", required=True)
    run.add_argument("--label", required=True)
    run.add_argument("--model-id")
    run.add_argument("--clip", action="append", default=[], type=_parse_clip)
    run.add_argument("--decode", action="append", default=[], help="LATENTS.pt[:tag]")
    run.add_argument("--keyframe")
    run.add_argument("--frames", type=int, default=33)
    run.add_argument("--steps", type=int, default=20)
    run.add_argument("--size", default="832x480")
    run.add_argument("--audio-prompt", default="rain", choices=sorted(PROMPTS))
    run.add_argument("--audio-seed", type=int, default=42)
    # 20 s is the server's default request length (AudioGenerationRequest).
    run.add_argument("--audio-seconds", type=float, default=20.0)
    run.add_argument("--audio-renders", type=int, default=2)
    run.add_argument("--t0", type=float, help="epoch seconds the container was started")

    cmp_ = sub.add_parser("compare")
    cmp_.add_argument("a")
    cmp_.add_argument("b")
    cmp_.add_argument("--json")

    ver = sub.add_parser("verdict")
    ver.add_argument("part", choices=["t2v", "vace", "vace-vae", "cache", "audio"])
    ver.add_argument("--a", required=True)
    ver.add_argument("--b", required=True)
    ver.add_argument("--host")
    ver.add_argument("--sims")
    ver.add_argument("--name", default="")

    col = sub.add_parser("collect")
    col.add_argument("root")

    args = parser.parse_args(argv)
    commands = {
        "run": cmd_run,
        "compare": cmd_compare,
        "verdict": cmd_verdict,
        "collect": cmd_collect,
    }
    return commands[args.cmd](args)


if __name__ == "__main__":
    sys.exit(main())
