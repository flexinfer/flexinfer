"""K2: load a Qwen-Image 2.1 GGUF transformer through diffusers' single-file
GGUF loader on CPU and run tiny forward passes.

Checks, in order:
  1. static key/shape check: the K1 header names, passed through diffusers'
     own QwenImage21 checkpoint mapping fn, against a meta-device model built
     from the repo config (missing / unexpected / shape-mismatched keys);
  2. the real `from_single_file(..., quantization_config=GGUFQuantizationConfig)`
     load, with the loader's own "weights not used" warning captured and every
     parameter checked off the meta device;
  3. forward passes on a 16x16 latent (256 image tokens) with seeded random
     text embeddings: one prediction at sigma=1 and an 8-step Euler run.
     "Non-noise" = finite output, and the predicted clean latent has spatial
     structure (lag-1 neighbour correlation over the 16x16 grid) that the pure
     noise input does not have.

With the optional trailing `shim` argument, GGUFParameters that land outside a
GGUFLinear (diffusers 0.41.0 only dequantizes nn.Linear weights; a BF16-typed
norm weight otherwise reaches RMSNorm as raw bytes) are dequantized to bf16
after loading. Without it the run is stock diffusers.

usage: k2_load_forward.py <gguf> <k1-header.json> <config-repo> <config-rev> <label> <out-dir> [shim]
"""

import json
import logging
import math
import resource
import sys
import time

import torch
from accelerate import init_empty_weights

import diffusers
from diffusers import GGUFQuantizationConfig, QwenImage21Transformer2DModel
from diffusers.loaders.single_file_model import _should_convert_state_dict_to_diffusers
from diffusers.loaders.single_file_utils import (
    convert_qwen_image21_transformer_checkpoint_to_diffusers,
)
from diffusers.quantizers.gguf.utils import (
    GGUFLinear,
    GGUFParameter,
    dequantize_gguf_tensor,
)

# GGUF compute dtype. fp32 on CPU: torch's arm64 bf16 GEMM has no BLAS path and
# falls back to a scalar dot kernel (minutes per forward). Weights stay quantized
# either way; the GPU lane would use bf16.
DTYPE = torch.float32
GRID = 16  # latent grid side: 16x16 tokens = a 256x256 px image at the VAE's 16x
TEXT_LEN = 32
STEPS = 8
SEED = 1234
# Non-noise bar, fixed before the first forward: pure noise has ~0 lag-1
# neighbour correlation; a working DiT's clean-latent estimate is smooth.
MIN_NEIGHBOUR_CORR = 0.25


def dequantize_non_linear(model):
    """Dequantize GGUFParameters held by modules that never dequantize them."""
    names = []
    for mod_name, module in model.named_modules():
        if isinstance(module, GGUFLinear):
            continue
        for p_name, p in list(module.named_parameters(recurse=False)):
            if isinstance(p, GGUFParameter):
                module._parameters[p_name] = torch.nn.Parameter(
                    dequantize_gguf_tensor(p).to(DTYPE), requires_grad=False
                )
                names.append(f"{mod_name}.{p_name}")
    return names


class Capture(logging.Handler):
    def __init__(self):
        super().__init__(logging.WARNING)
        self.records = []

    def emit(self, record):
        self.records.append(record.getMessage())


def rss_gib():
    # ru_maxrss is bytes on macOS
    return resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 2**30


def static_key_check(header_json, model_state):
    with open(header_json) as f:
        header = json.load(f)["tensors"]
    ckpt = {n: torch.empty(v["shape"], device="meta") for n, v in header.items()}
    converted = ckpt
    if _should_convert_state_dict_to_diffusers(model_state, ckpt):
        converted = convert_qwen_image21_transformer_checkpoint_to_diffusers(dict(ckpt))
    missing = sorted(set(model_state) - set(converted))
    unexpected = sorted(set(converted) - set(model_state))
    shape_mismatch = sorted(
        f"{k}: ckpt {list(converted[k].shape)} vs model {list(model_state[k].shape)}"
        for k in set(converted) & set(model_state)
        if tuple(converted[k].shape) != tuple(model_state[k].shape)
    )
    return {
        "header_tensors": len(header),
        "model_tensors": len(model_state),
        "missing": missing,
        "unexpected": unexpected,
        "shape_mismatch": shape_mismatch,
    }


def neighbour_corr(latent):
    """Mean lag-1 correlation along both grid axes; latent (1, GRID*GRID, C)."""
    x = latent.float().reshape(GRID, GRID, -1)
    x = (x - x.mean(dim=(0, 1))) / (x.std(dim=(0, 1)) + 1e-6)
    h = (x[:, 1:] * x[:, :-1]).mean()
    v = (x[1:, :] * x[:-1, :]).mean()
    return float((h + v) / 2)


def cosine(a, b):
    a, b = a.float().flatten(), b.float().flatten()
    return float(torch.dot(a, b) / (a.norm() * b.norm()))


def call(model, x, text, text_mask, sigma):
    img_shapes = [[(1, GRID, GRID)]]
    img_mask = torch.cat(
        [
            torch.zeros(1, TEXT_LEN, dtype=torch.bool),
            torch.ones(1, (GRID * GRID) // 4, dtype=torch.bool),
        ],
        dim=1,
    )
    out = model(
        hidden_states=x,
        encoder_hidden_states=text,
        encoder_hidden_states_mask=text_mask,
        timestep=torch.tensor([sigma], dtype=DTYPE),
        img_shapes=img_shapes,
        img_mask=img_mask,
        return_dict=False,
    )[0]
    return out[:, -GRID * GRID :]


def main():
    gguf_path, header_json, cfg_repo, cfg_rev, label, out_dir = sys.argv[1:7]
    shim = len(sys.argv) > 7 and sys.argv[7] == "shim"
    result = {
        "label": label,
        "gguf": gguf_path,
        "config_repo": cfg_repo,
        "config_revision": cfg_rev,
        "diffusers": diffusers.__version__,
        "torch": torch.__version__,
        "compute_dtype": str(DTYPE),
    }
    cap = Capture()
    logging.getLogger("diffusers").addHandler(cap)

    config = QwenImage21Transformer2DModel.load_config(
        cfg_repo, subfolder="transformer", revision=cfg_rev
    )
    with init_empty_weights():
        meta_model = QwenImage21Transformer2DModel.from_config(config)
    result["static"] = static_key_check(header_json, meta_model.state_dict())
    del meta_model
    print(json.dumps(result["static"], indent=1)[:2000], flush=True)

    t0 = time.time()
    model = QwenImage21Transformer2DModel.from_single_file(
        gguf_path,
        quantization_config=GGUFQuantizationConfig(compute_dtype=DTYPE),
        config=cfg_repo,
        subfolder="transformer",
        revision=cfg_rev,
        torch_dtype=DTYPE,
    )
    result["load_seconds"] = round(time.time() - t0, 1)
    on_meta = [
        n
        for n, p in list(model.named_parameters()) + list(model.named_buffers())
        if p.device.type == "meta"
    ]
    kinds = {}
    for _, p in model.named_parameters():
        key = f"{type(p).__name__}:{getattr(p, 'quant_type', p.dtype)}"
        kinds[key] = kinds.get(key, 0) + 1
    result["load"] = {
        "params_left_on_meta": on_meta,
        "param_kinds": kinds,
        "loader_warnings": [
            m for m in cap.records if "not used" in m or "missing" in m.lower()
        ],
        "rss_gib_after_load": round(rss_gib(), 2),
    }
    print(json.dumps(result["load"], indent=1, default=str)[:2000], flush=True)

    if shim:
        shimmed = dequantize_non_linear(model)
        result["shim_dequantized"] = {"count": len(shimmed), "names": shimmed}
        print(f"shim: dequantized {len(shimmed)} non-Linear GGUFParameters", flush=True)

    gen = torch.Generator().manual_seed(SEED)
    noise = torch.randn(1, GRID * GRID, 64, generator=gen).to(DTYPE)
    text = torch.randn(1, TEXT_LEN, 4096, generator=gen).to(DTYPE)
    text_mask = torch.ones(1, TEXT_LEN, dtype=torch.bool)

    with torch.inference_mode():
        t0 = time.time()
        try:
            v1 = call(model, noise, text, text_mask, 1.0)
        except Exception as exc:  # record the stock failure, then fail the run
            result["forward"] = {"error": f"{type(exc).__name__}: {exc}"}
            with open(f"{out_dir}/{label}.json", "w") as f:
                json.dump(result, f, indent=1, default=str)
            print(f"VERDICT {label}: FAIL (forward raised {type(exc).__name__}: {exc})")
            raise
        fwd_s = time.time() - t0
        print(f"forward sigma=1 done in {fwd_s:.1f}s", flush=True)
        x0_hat = (
            noise.float() - v1.float()
        )  # flow matching: x_t = (1-s) x0 + s eps, v = eps - x0

        # 8-step Euler from pure noise, sigmas 1 -> 1/8 then to 0.
        sigmas = [1.0 - i / STEPS for i in range(STEPS)] + [0.0]
        x = noise.clone()
        traj = []
        for s, s_next in zip(sigmas[:-1], sigmas[1:]):
            v = call(model, x, text, text_mask, s)
            x = (x.float() + (s_next - s) * v.float()).to(DTYPE)
            print(f"euler sigma={s:.3f} v_std={float(v.float().std()):.4f}", flush=True)
            traj.append(
                {
                    "sigma": s,
                    "v_std": round(float(v.float().std()), 4),
                    "x_std": round(float(x.float().std()), 4),
                }
            )

    finite = bool(torch.isfinite(v1).all() and torch.isfinite(x.float()).all())
    result["forward"] = {
        "grid": f"{GRID}x{GRID} latent tokens + {TEXT_LEN} text tokens",
        "seconds_per_forward": round(fwd_s, 1),
        "finite": finite,
        "v_sigma1_std": round(float(v1.float().std()), 4),
        "cos_v_vs_noise_sigma1": round(cosine(v1, noise), 4),
        "neighbour_corr_noise_input": round(neighbour_corr(noise), 4),
        "neighbour_corr_x0_hat_sigma1": round(neighbour_corr(x0_hat), 4),
        "neighbour_corr_euler_final": round(neighbour_corr(x), 4),
        "euler_final_std": round(float(x.float().std()), 4),
        "euler_trajectory": traj,
        "rss_gib_peak": round(rss_gib(), 2),
    }
    print(json.dumps(result["forward"], indent=1), flush=True)

    torch.save({"v1": v1, "x0_hat": x0_hat, "final": x}, f"{out_dir}/{label}.pt")
    with open(f"{out_dir}/{label}.json", "w") as f:
        json.dump(result, f, indent=1, default=str)
    s = result["static"]
    fw = result["forward"]
    keys_ok = (
        not s["missing"]
        and not s["unexpected"]
        and not s["shape_mismatch"]
        and not on_meta
        and not result["load"]["loader_warnings"]
    )
    non_noise = (
        finite
        and fw["neighbour_corr_x0_hat_sigma1"] >= MIN_NEIGHBOUR_CORR
        and fw["neighbour_corr_euler_final"] >= MIN_NEIGHBOUR_CORR
    )
    ok = keys_ok and non_noise
    print(
        f"VERDICT {label}: {'PASS' if ok else 'FAIL'} keys_ok={keys_ok}"
        f" non_noise={non_noise} shim={bool(shim)}"
    )
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
