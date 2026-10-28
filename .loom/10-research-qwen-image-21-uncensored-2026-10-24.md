# Research: Qwen-Image 2.1 "Uncensored-Quantized" Candidate + Image-Inference Support Audit (2026-10-24)

**Question**: Is `abenzerps/Qwen-Image-2.1-Uncensored-Quantized` worth a canary on the fleet,
on which node and runtime, and what does FlexInfer's image-inference support need so that this
model family (and the other current families) can run, under the owner's constraint that every
backend stacks into **one runtime image per GPU class** and is demand-swapped in place inside the
long-lived per-node runtime pod?

**Verdict (TL;DR)**: **Bring Qwen-Image 2.1 to gfx1100, and give this derivative a canary only as
the second arm behind an official-weights control, on `cblevins-7900xtx`, in the stacked gfx1100
runtime pod.** Qwen-Image 2.1 (7.1B single-stream DiT, Qwen3-VL-8B text encoder, its own
64-channel VAE, text-to-image + editing + RGBA, released 2026-09-20) is the first open image model
with editing, RGBA output and legible text that fits a 24 GB RDNA3 card. The fleet today serves
image generation only as SD 1.5 on the GTX 980 Ti. The stacked runtime is the *closest* image to
running it: the XTX DaemonSet's serving persona already carries torch 2.9, transformers 5.17,
`gguf` and bitsandbytes. It needs one pin (diffusers 0.40 → 0.41.x) and a `qwenimage21` +
GGUF-transformer path in `server-diffusers.py`. The dedicated diffusers images (torch 2.3/2.6,
transformers 4.48, diffusers 0.36) are three blockers away. Stacking and the Qwen-Image goal
point the same way. The derivative's only unique bytes are its transformer files. Its text encoder
and VAE are byte-identical to Comfy-Org's. Its "uncensoring" is a merged 33.6 MB LoRA whose
training the author has not disclosed. Its licence is the Qwen Research License (research and
evaluation only).

**Top risk**: the GGUFs were converted with stable-diffusion.cpp. A third-party model card reports
that sd.cpp's Qwen-Image 2.1 layout (265 tensors, fused `img_mlp.gate_up`, a
`model.diffusion_model.` prefix) "is not interchangeable" with the 297-tensor layout diffusers'
`QwenImage21Transformer2DModel` expects. A CPU-only kill-test of about 30 minutes (§6) decides
between the diffusers path and an sd.cpp backend before any GPU time is spent. Two more risks.
Operationally, every image request evicts a text lane that fills the whole card (the Mills warm
lane on the XTX reloads in 2–3 min). On safety, `images.flexinfer.ai` has no ingress auth in
gitops, and imagegen-ui sends every prompt to `gpt-4o-mini` for "enhance" by default.

**Kill-test result (§6b, run 2026-10-24)**: **FAIL on stock diffusers 0.41.0, PASS with a
~12-line loader shim. Do not fall back to sd.cpp for this derivative.**

- The layout premise above is wrong for this repository. Its GGUFs already use diffusers' 297
  tensor names: split `gate_layer` / `proj`, no prefix. The 265-tensor fused layout is
  Comfy-Org's bf16 safetensors.
- The UC Q8_0 loads with zero missing or unexpected keys. Its first forward then raises in an
  RMSNorm: sd.cpp stored the 65 norm weights as GGUF BF16, and diffusers 0.41.0/0.42.0 leave BF16
  outside `nn.Linear` as raw bytes.
- With those 65 weights dequantized after load, the forward passes the non-noise bar. It agrees
  with a diffusers-layout control at cosine 0.985 on identical inputs.
- R3 has to carry the shim (§6b).

**R1–R3 landed (2026-10-25, §4a)** on branch `feat/qwen-image21-diffusers-r1-r3`, in one MR with
the draft arm-A canary `deploy/debug/qwen-image-21-canary.yaml` (official weights, NF4 on load).
The diffusers 0.41.0 overlay and its build-time checks passed off-CI on the deployed serving
digest `cfefae18`. The serving image itself builds only on master. Not yet run: anything on the
GPU, image quality, and the cold-swap budget. Those are R4.

**Demand-based lane (2026-10-26, §4b)**: `deploy/models/qwen-image-21-gfx1100.yaml` is a
scale-to-zero member of `5930k-textgen` at priority 500, with aliases `qwen-image` /
`qwen-image-2.1`. It runs arm A's config and is on its own branch, stacked on R1–R3, with the
image digest to fill from the R1–R3 master build. It is on the 5930k because the XTX card is the
warm Mills lane's and drops under sustained compute. §4b has the operator's GPU verification
plan.

Branch `claude/gfx1100-flexinfer-support-904425`. No cluster writes were made. The 5930k S2 MTP
kill-test was left alone. Upstream facts come from the Hugging Face API, model cards and docs, and
from third-party reports cited in §7.

Evidence tags used below:

- **[V]**: verified against a primary source (repository file, HF API/file, official docs).
- **[R]**: reported by a third party and not reproduced here.
- **[I]**: inferred (arithmetic or code reading, not run).

---

## 1. Verdict in one table

| Question | Answer |
|---|---|
| Worth a canary? | **Yes for the family; conditional yes for this derivative.** Run it as arm B after an official-weights arm A. Run it only after the §6 CPU kill-test says its GGUF loads in diffusers (else move to sd.cpp, slice R6). Never put it on the public `image-gen` alias. |
| Node | `cblevins-7900xtx`. It hosts the only running gfx1100 runtime pod (serving persona `cfefae18`, `bundledBackends: [vllm, diffusers, snac]` [V]). Its displaced lane is the lower-traffic Mills warm lane: 336 requests/7d vs 12,048 on the 5930k workhorse [V 10-23 doc]. The 336 is depressed because the lane was parked 10-10 → 10-23, but Mills is still the smaller tenant. The 5930k is busy with the S2 kill-test, and its runtime DaemonSet is desired=0 [V values-k3s]. |
| Runtime | Stacked `gfx1100-serving` runtime image with diffusers pinned to 0.41.x (late overlay layer) plus `server-diffusers.py` gaining `qwenimage21`, a signature-aware call contract, a GGUF transformer and an NF4 text encoder. The first GPU run may be a **throwaway** `dedicatedDeployment` of that same digest (§5), then runtime-managed. |
| Variant | UC **Q8_0** (7.59 GB) as primary: GGUF Q8_0 dequant is the cheapest, and 24 GB has room [I]. UC Q4_K_M (4.60 GB) is the A/B arm. The text encoder comes from `Qwen/Qwen-Image-2.1` and is quantized to NF4 on load. The VAE comes from the base repo. |
| Expected speed | 1024², 40 steps, no CFG: **~55–100 s** warm on the XTX [I]. AMD reports range from 26 s (ComfyUI int8, 25 steps) to 42 s (Q4_K_M, 7900 XT, 25 steps) [R]. Turbo (8 steps, a separate official checkpoint, not UC) would be ~12–20 s [I]. |
| VRAM | ~10.4 GiB resident with the Q4_K_M DiT, or ~13.2 GiB with Q8_0. Peak is ~15–17 GiB at 1024² [I, by analogy with a reported 15.2 GB NF4 peak]. `vramEstimateMB: 18000`. |
| Posture | `flexinfer.ai/base-model-posture: uncensored`. Base source `Qwen/Qwen-Image-2.1`, artifact pinned to the derivative's commit `7959e658…`. The control arm is `clean`. |
| Top risk | sd.cpp-layout GGUF vs diffusers layout (§6). **Retired 2026-10-24 (§6b)**: the layout matches. Stock diffusers fails on BF16 norm weights, and an R3 shim fixes it. Remaining: whole-card text-lane eviction on every image burst, and the public, unauthenticated imagegen-ui. |

## 2. The candidate (Part 1)

### 2a. The derivative: `abenzerps/Qwen-Image-2.1-Uncensored-Quantized`

**Metadata and maker:**

- Repository facts [V HF API]: created 2026-09-20T15:51Z (release day of the base) and last
  modified 2026-10-12. Head commit `7959e658915f52274897fb573ffeda49de8dc16b`. Card fields:
  `license: other`, `license_name: qwen-research`, `base_model: Qwen/Qwen-Image-2.1`,
  `base_model_relation: quantized`, `library_name: gguf`. 2,303,585 downloads last month and 4,936
  likes. GGUF metadata reports `architecture: qwen_image21` and `total: 7,115,124,736` parameters.
- Maker [V HF profile]: Ahmet Benzer (`@abenzerps`), 45 models. Almost all are GGUF or MLX
  conversions of other models ("GSQ-RCO-GGUF", "Spark-X2.6"), and the other repos have
  ~0.3k–10k downloads. This is a prolific quantizer, not a fine-tuning lab. The repo is an outlier
  in popularity, not in provenance.

**How it was built:**

- Conversion [V card]: stable-diffusion.cpp commit `1330cebae8f2ba99249df846cc0c9444fcbd4308` plus
  comfy-kitchen. Source model `Qwen/Qwen-Image-2.1` at revision
  `b3179ad355be050328e483a9dfdd9e60cd62adfa`. Text encoder and VAE are taken from
  `Comfy-Org/Qwen-Image-2.1`.
- **What "uncensored" means.**
  - The card says only that the release "has no built-in safety checker or content filter" [V].
  - In discussion #49 the author says the bundled `qwen-image-2.1-uncensored-lora.safetensors`
    (33,586,704 B) "contains the model's weight changes, which are already included in the UC
    versions" [V].
  - So the UC transformers are the **base plus a merged LoRA delta** [V]. Its rank, training data
    and method are undisclosed [V absence].
  - A user's question about what the LoRA adds over an "already uncensored" base is unanswered [V].
  - It is not abliteration. It cannot be audited from the card.
- **Integrity**: `SHA256SUMS` lists 18 files [V]. It covers the GGUFs (UC and base), the MLX and
  NVFP4 files, and the text encoder and VAE. It **omits** `UC-fp8.safetensors`,
  `UC-int8_convrot.safetensors` and the LoRA [V].
- The companion files are byte-identical to Comfy-Org's [V, HF LFS oids]:
  - `qwen3vl_8b_bf16.safetensors` `68bdc82b…`
  - `qwen3vl_8b_int8_convrot.safetensors` `8bfd0f6e…`
  - `qwen_image_2.1_vae_bf16.safetensors` `bb21f747…`
- **Licence wrinkle**: Qwen Research License §4(c) forbids using "Qwen" as the primary name of a
  derivative [V LICENSE]. This repo does exactly that. That is the uploader's problem, not ours,
  but it tells you how carefully the release was made.

**File listing** (bytes, main branch) [V HF tree API]:

| File | Size | Format / who can load it |
|---|---|---|
| `qwen-image-2.1-UC-BF16.gguf` | 14,230,272,800 | GGUF BF16 (sd.cpp layout) |
| `qwen-image-2.1-UC-Q8_0.gguf` | 7,591,557,920 | GGUF |
| `qwen-image-2.1-UC-Q6_K.gguf` | 5,876,556,576 | GGUF |
| `qwen-image-2.1-UC-Q5_K_M.gguf` | 5,221,284,640 | GGUF |
| `qwen-image-2.1-UC-Q4_K_M.gguf` (card's pick) | 4,604,558,112 | GGUF |
| `qwen-image-2.1-UC-Q4_0.gguf` | 4,151,573,280 | GGUF |
| `qwen-image-2.1-UC-fp8.safetensors` | 7,116,818,183 | ComfyUI scaled fp8 (no FP8 hardware on RDNA3) |
| `qwen-image-2.1-UC-int8_convrot.safetensors` | 7,256,796,840 | ComfyUI / comfy-kitchen int8 ConvRot only |
| `qwen-image-2.1-UC-NVFP4.safetensors` | 4,197,574,696 | Blackwell FP4 (not usable on AMD) |
| `qwen-image-2.1-UC-MLX-{4,6,8}bit.safetensors` | 4,002,363,741 / 5,781,141,974 / 7,559,920,348 | Apple MLX only |
| `qwen-image-2.1-uncensored-lora.safetensors` | 33,586,704 | the UC delta (format unverified) |
| `text_encoders/qwen3vl_8b_bf16.safetensors` | 17,534,334,616 | = Comfy-Org file |
| `text_encoders/qwen3vl_8b_int8_convrot.safetensors` | 9,350,798,360 | = Comfy-Org file (ComfyUI only) |
| `vae/qwen_image_2.1_vae_bf16.safetensors` | 675,509,688 | = Comfy-Org file |

A `base` branch carries un-uncensored GGUFs from the same converter (Q8_0 … Q4_0) [V card and
SHA256SUMS]. That gives a same-converter control that isolates the UC delta from the loader.

**Community threads** [V discussions list, 45 total]:

- Loader confusion dominates.
  - #24 "Unexpected architecture type in GGUF file: 'qwen_image21'" was fixed by switching to
    leejet's ComfyUI-GGUF fork (city96's loader rejects the arch string).
  - #31 and #33 report that the UNet loader does not see the model.
- Quality and format reports:
  - #22 blank output with the fp8 file.
  - #37 a token-length boundary pinned at 64.
  - #25 the int8_convrot file does not apply a character LoRA.
- Gaps: no AMD/ROCm thread, no diffusers thread, and no provenance or safety discussion. #14 is
  the author's own sample thread, with no settings or timings.

### 2b. The base: `Qwen/Qwen-Image-2.1`

- **Release and licence**:
  - HF repo created 2026-09-14 and last modified 2026-09-30 [V API].
  - Public release 2026-09-20 [R, several sources; matches the licence date].
  - Licence: *Qwen Research License Agreement*, dated 2026-09-20 [V]. "Non-Commercial" means "for
    research or evaluation purposes only". §2(b) requires a separate licence for commercial use.
    §4(b) requires "Built with Qwen" on distributed derivatives. §4(c) forbids "Qwen" as a
    derivative's primary name.
- **Components** (`model_index.json`, authored on diffusers `0.37.0.dev0`) [V]:
  - `QwenImage21Pipeline`
  - `Qwen3VLProcessor`
  - `FlowMatchEulerDiscreteScheduler`
  - `Qwen3VLForConditionalGeneration` (transformers 4.57.1 in its config)
  - `QwenImage21Transformer2DModel`
  - `AutoencoderKLQwenImage21`
- **Transformer** (`transformer/config.json`) [V]:
  - 32 layers × 32 heads × 128 head dim (4096 hidden), `mlp_ratio` 3.
  - `in_channels`/`out_channels` 64, `patch_size` 1, `context_in_dim` 4096.
  - `axes_dims_rope` [16, 56, 56], `causal_condition: true`.
  - The card calls it "7B parameters… 32 Single-Stream DiT layers" with "mixed-granularity
    attention and prefix KV cache reuse" [V].
- **Text encoder**: Qwen3-VL. The text tower has 36 layers at 4096 hidden, GQA 32/8, and a 262,144
  context. The vision tower is 27 deep at 1152 [V config]. It is about 8B parameters: the bf16 file
  is 17.53 GB [V], and "8B" is in Comfy's file name [V].
- **VAE**: its own `AutoencoderKLQwenImage21` with a 64-channel latent [V docs]. Earlier Qwen-Image
  VAEs are not interchangeable [R]. At 1024² the latent grid is 64×64 (16× compression). This is
  derived from ComfyUI's decode-memory formula, as quoted by smeltcore [R/I].
- **Tasks** [V card + diffusers docs]:
  - Text-to-image.
  - Image-conditioned editing with up to 10 reference images.
  - Local edits marked with circles, painted annotations or separate masks. "Inpainting" is done
    this way, not with a separate pipeline.
  - Native RGBA generation and editing, and subject extraction.
- **Resolution**: native 2048² plus seven aspect presets (2400×1792 … 1536×2752) [V card].
  diffusers derives the size from `output_resolution` (default 1024) [V].
- **Sampling** [V docs]:
  - Defaults are 40 steps and `true_cfg_scale = 1.0`, meaning no CFG.
  - CFG needs `true_cfg_scale > 1` **and** a `negative_prompt`, and it doubles the work.
  - `use_kv_cache=True` caches text and condition K/V after step 1.
  - There is **no `guidance_scale` argument** in `QwenImage21Pipeline.__call__` (v0.41.0
    signature).
- **Requirements**: the card says `torch>=2.4.0` and `transformers>=5.17`, with diffusers from git
  at launch [V]. diffusers **v0.41.0** docs document `QwenImage21Pipeline` and
  `QwenImage21Transformer2DModel.from_single_file`, with the Comfy-Org bf16 file as the example
  [V]. 0.41.0 reached PyPI on 2026-10-06 [R artrealmai; SourceForge mirror]. The integration is
  PR #14804 [R QwenLM README].
- **Turbo**: `Qwen/Qwen-Image-2.1-Turbo` was created 2026-10-09 [V API]. It uses the same
  `QwenImage21Pipeline` with a shipped `sample_sigmas` grid of about 8 steps [R/V docs].
  diffusers' modular Turbo blocks are on `main`, not in the v0.41.0 page [V].
- **Day-0 runtimes** [R mer.vin, QwenLM README]: ComfyUI v0.37.0 (PR #16400), stable-diffusion.cpp
  (2026-09-20), vLLM-Omni, SGLang (PR #39983), LightX2V, DiffSynth.
- **Edit and caching quirks**:
  - On the reported side: a diamond-grid VAE artifact at high resolution (base repo discussion #12)
    [R], and vLLM-Omni rejects a fifth reference image [R].
  - On the verified side: toggling `use_kv_cache` changes samples visibly in reduced precision, so
    fix the flag per lane [V docs].

### 2c. Quantization formats and who loads them

| Runtime | Loads this derivative's files? | Notes |
|---|---|---|
| **diffusers** GGUF (`from_single_file` + `GGUFQuantizationConfig`) | **Unproven: the §6 kill-test** | GGUF arrived in 0.32.0 [R]. Types: BF16, Q4_0/1, Q5_0/1, Q8_0, Q2_K–Q6_K [V]. Weights stay in uint8 and are dequantized per forward to `compute_dtype` [V]. The optimized kernels are CUDA-only (`DIFFUSERS_GGUF_CUDA_KERNELS`, capability > 7) [V], so ROCm uses the torch dequant path [I]. Pipelines cannot load GGUF directly: load the transformer, then pass `transformer=` [V]. m9ter1a reports the sd.cpp layout is not interchangeable with diffusers' [R]. Earlier Qwen GGUF single-file failures exist: #12098, and #12891's `unexpected keyword argument 'config'` [R]. |
| diffusers bnb NF4 (load-time from bf16) | n/a (needs the bf16 base) | RTX 5090, 40 steps: NF4 weights 10.6 GB, peak 15.2 GB, 19.2 s at 1024². fp8 (torchao): 16.6 GB weights, peak 21.3 / 22.6 GB at 1024² / 2048², 19.3 / 115.7 s [R, HF discussion #32 via openclawdc]. bnb ships gfx1100 HIP kernels and NF4 is proven on gfx1100 in this repo (FLUX, finetune) [V repo]. |
| ComfyUI + ComfyUI-GGUF | Yes, with **leejet's** fork [V discussion #24] | city96's loader fails "Unknown model architecture" [V/R]. The int8 ConvRot path is native on AMD; fp8 and NVFP4 are emulated, and RDNA3 widens fp8 to bf16 [R smeltcore]. |
| **stable-diffusion.cpp** (`sd-cli` / `sd-server`) | **Yes, natively** (it made these files) [V card] | ROCm, Vulkan and CUDA backends [R]. `sd-server` exposes an OpenAI-compatible `/v1/images/generations` [R Backend.AI GO]. `--offload-to-cpu`, `--diffusion-fa`, a LoRA directory, `--llm` and `--llm_vision` for the Qwen3-VL encoder [R]. It also covers FLUX.2/FLUX.3 Klein, Z-Image, Chroma, SD3.5, HiDream-O1, Wan and LTX-2.5 [R README]. |
| nunchaku / SVDQuant | No | No Qwen-Image 2.1 SVDQuant build was found. nunchaku's kernels are NVIDIA-only [I]. |
| Forge | No | No Qwen-Image 2.1 support was found [I]. |
| vLLM-Omni / SGLang | Base weights only (FP8 path) | Day-0 support [R]. FP8 has no RDNA3 hardware. The fleet's `vllm-omni` image is 0.14.0 with shims (`build/Dockerfile.vllm-omni-rocm-gfx1100`) [V], so it is stale. |

Diffusers-layout GGUFs exist as controls: `m9ter1a/Qwen-Image-2.1-Turbo-GGUF` (Turbo, 297 tensors,
Q8_0 7.66 GB … Q4_K_M 4.30 GB) [R card]. `unsloth/Qwen-Image-2.1-GGUF` (F16 … Q2_K, Q4_K_M
4,199,565,024 B) [V sizes] documents an `sd-cli` command, so its layout is likely sd.cpp's [R/I].

### 2d. ROCm / gfx1100 feasibility

**AMD timings found.** None is a diffusers run; none was run here.

| Card / stack | Weights | Res, steps | Result | Source |
|---|---|---|---|---|
| RX 7900 XTX, Linux, torch 2.13 + ROCm 10.0 (HIP 7.15), ComfyUI, DynamicVRAM | int8 DiT + int8 TE | 1024², 25 | 19 s sampling (1.26 it/s); prompt executed in 26.49 s. Output not described | ComfyUI #16521 [R via smeltcore] |
| RX 7900 XT 20 GB, Windows, torch 2.13 + rocm10.0, ComfyUI 0.37.0 | **GGUF Q4_K_M** | 1024², 25, CFG 1 | **~40–42 s warm.** int8 ran ~1 s/step but filled 20 GB (worst case 267 s end to end). Turbo GGUF 8 steps: 23–26 s; peak ~19.3 GB | note.com/elbobaggins [R] |
| RX 7900 XTX, Linux, torch 2.12 + ROCm 7.1 | bf16 DiT + bf16 TE | 2720×1536, 40 | 5.14 s/it, ~205 s/image, correct output | ComfyUI #16447 [R via smeltcore] |
| RX 7900 XTX, Windows, torch 2.9.1 + ROCm 7.2.1 | Q4_K_M + w4a8 TE | 512², 8 | ~0.6 s/step; 18.1 s cold incl. ~14 s load; correct | MiguelCarrascoB playground [R via smeltcore] |
| Radeon PRO W7900D (gfx1100, 48 GB), ROCm 7.2.4 / torch 2.10 | int8 | 1024², 25 | 32.7 s → **25.0 s** with patched INT8 attention + RMSNorm/RoPE HIP kernels | zihaomu/Qwen-Image-2.1-Rocm [R] |
| Radeon PRO W7900, LightX2V | int8 + compile + sage | ?, 40 | ~46 s ("correct output", no image) | LightX2V PR #1549 [R via smeltcore] |

**Our diffusers path, estimated [I]:**

- About 71 TFLOP per step at 1024². That is ≈ 61 TFLOP for the linears (7.1B params × 2 × ~4.3k
  tokens) plus ≈ 10 TFLOP for attention over 32 layers.
- The XTX sustained 56.8–73 TFLOPS on the fp16 matmul burns in the 10-21/10-22 soaks [V index].
- With GGUF torch dequant and SDPA at 50–70 % efficiency that is **1.4–2.4 s/step**: **55–100 s
  for 40 steps**, or 35–60 s for 25 steps.
- Cross-checks: the 7900 XT Q4_K_M row scales to ~1.3 s/step on the XTX's 96 CUs. The 2100 MHz
  overdrive cap costs ~6 % [V index].

**VRAM budget on 24 GB [I]:**

| Configuration | Resident weights | Peak at 1024² | Fits? |
|---|---|---|---|
| UC Q4_K_M GGUF DiT + NF4 TE + bf16 VAE | 4.29 + ~5.5 + 0.63 ≈ **10.4 GiB** | ~15–16 GiB | Yes, no offload |
| UC **Q8_0** GGUF DiT + NF4 TE + VAE | 7.07 + ~5.5 + 0.63 ≈ **13.2 GiB** | ~17–18 GiB | Yes. Q8_0 dequant is the cheapest GGUF path |
| UC Q4_K_M + **bf16** TE (16.33 GiB) + VAE | ≈ 21.3 GiB | > 24 GiB | Only with model CPU offload: the TE shuttles ~17.5 GB per request (~1.5 s on PCIe 4 x16), and 17.5 GB of host RAM sits inside the runtime pod's 48 Gi limit |
| bf16 DiT + bf16 TE | 29.6 GiB | — | No (a ComfyUI user measured 30.2 GiB of weights [R]) |
| 2048² native | +VAE decode ≈ 7 GiB untiled [R ComfyUI formula] | — | Only with `enableVaeTiling` |

**Known ROCm issues for this family and card:**

1. **Silent wrong output from quantized DiTs on gfx1100** [R]. ComfyUI #15314 (MiniMax H3) gives
   noise with no error. #15452 reports black or bad SDXL output under DynamicVRAM. #16437 reports
   Qwen-Image 2.1 corrupt after any reload under DynamicVRAM (gfx1201, plus a 7900 XTX "after 3–4
   gens" report).
   - Lesson for us: test **after a reload and after a prompt change**, not only the first image
     (§5).
2. **Flash attention rejects a mask.** "Flash Attention failed, using default SDPA: Mask must not
   be set" was logged 32× in #16521 [R]. diffusers' default `QwenImage21AttnProcessor` runs one
   call per prefix segment [V], so it may avoid masks entirely [I].
   - AOTriton is enabled in our images (`TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1`) [V].
3. **No FP8 or FP4 hardware on RDNA3.** The fp8 and NVFP4 files are pointless here. int8 has
   native WMMA. bf16 is native on gfx1100: use bf16 compute, because fp16 activations risk overflow
   in a bf16-trained 7B DiT [I].
4. **torch.compile.** The flex-attention processor is fast only when compiled; uncompiled it
   materializes fp32 scores and OOMs at high resolution [V docs]. Triton flex attention on ROCm is
   unverified [I].
   - `_apply_compile_controls` skips compile for families outside {sdxl, sd3, sd15, flux, wan} [V].
5. **RDNA3 VAE decode crash above 1024 px** (ROCm/ROCm#4729). Already mitigated by
   `MIOPEN_FIND_MODE=2` (`backend/diffusers.go`) [V].
6. **Fleet-specific: the XTX SMU wedge** under sustained load above ~2140 MHz. The boot unit now
   caps at 2100 MHz and the 600 s soaks passed [V index]. Diffusion is a sustained-compute burst
   that matches the trigger profile. The GPU health sentinel parks the node if it drops [V index].

**Radeon VII and GTX 980 Ti.**

- **Radeon VII (gfx906, 16 GB): no.** Q4_K_M + NF4 TE (~10.4 GiB) only fits if the warm retrieval
  plane (~6.8 GiB) plus orpheus (4.8 GiB) are evicted. That is exactly the 2026-06-20 starvation
  incident that moved image generation off this card [V kustomization comments]. gfx906 has no
  WMMA and no native bf16. At ~27 TFLOPS fp16 peak, expect ~4–6 min per 1024² image [I].
- **GTX 980 Ti (sm_52, 6 GB): no.**
  - The Q4_0 DiT (4.15 GB) + VAE (0.68 GB) + activations exceed 6 GB at 1024² without sequential
    offload. The 8B TE would run on the CPU inside a 12 Gi pod limit [V manifest].
  - The node's image is torch 2.1.2 / diffusers 0.30.2 [V], so it cannot load `QwenImage21`.
  - sd.cpp CUDA with `--offload-to-cpu` is the only route: an RTX 3050 6 GB took 4 min 50 s at
    1184², 25 steps [R]. The tensor-core-less 980 Ti would be several times slower [I].

### 2e. Safety and posture

- **Repository convention** [V `docs/dev/daily-driver-promotion-checklist.md`, `deploy/models/*`]:
  - Every lane records `flexinfer.ai/base-model-source` and `flexinfer.ai/base-model-posture`.
  - Values in use: `clean`, `abliterated`, `uncensored-heretic`,
    `uncensored-heretic-fable-fusion`. The checklist vocabulary is
    `abliterated | uncensored | aligned | distilled`.
  - "This workspace's daily-drivers are uncensored by policy."
  - The NSFW-RP precedent (`qwen35-9b-ablit-rp` + `nsfw-rp` LoRA) stayed "off the public fleet
    until the kill-test passes". Its quality re-test was "operator-gated — it generates NSFW
    output" [V `.loom/plan-qwen35-9b-nsfw-rp-lora-2026-07-18.md`].
- **This candidate's posture**: `uncensored`. Method: a merged LoRA delta of undisclosed origin.
  Add `flexinfer.ai/artifact-source` with the commit pin. The control arm is `clean`.
- **The image stack has no safety classifier.** `server-diffusers.py` passes
  `safety_checker: None` and `requires_safety_checker=False` [V]. Posture therefore has to be
  enforced by routing.
- **imagegen-ui** [V repo + gitops]:
  - It defaults to `DEFAULT_GENERATION_MODEL=image-gen` with `GENERATION_MODELS=["image-gen"]`. It
    sends generation and edit calls straight to `flexinfer-proxy` (bypassing LiteLLM to keep
    `num_inference_steps` / `guidance_scale` / `negative_prompt`). `EDIT_MODELS=[]`.
  - `image-gen` is currently an alias of `dreamshaper8-imagegen-gtx980ti` [V manifest].
  - The ingress serves `images.lan` and **`images.flexinfer.ai` with TLS and no auth annotation**.
    Whether Cloudflare or anything else gates it externally is not visible in git [unknown].
  - **Prompt enhance defaults to on** (`ENABLE_PROMPT_ENHANCE` unset → true) with
    `ENHANCE_MODEL=gpt-4o-mini` through LiteLLM [V code defaults + deployment env]. An uncensored
    lane would ship explicit prompts to a vendor model unless enhance is turned off.
- **familyforge is not an image-inference consumer** [V AGENTS.md + grep]. Its "image automation"
  is Flux `ImageUpdateAutomation` for its container tags. Its safety surface is health-data
  privacy: "never send data to third parties", the FlexInfer insight path "flag-gated and off by
  default", and a "not a medical device" disclaimer. It needs nothing from this work.

## 3. The operator's image-inference support today (Part 2)

### 3a. What serves image, video and audio today, and from which image

| Lane | State | Image actually run | How it runs |
|---|---|---|---|
| `dreamshaper8-imagegen-gtx980ti` (SD 1.5) | **Live**, warm (the only image lane in `deploy/models/kustomization.yaml`) | sm_52 GPUProfile `diffusers:cuda-maxwell-0b19f7a8@sha256:145ee5…` (CUDA 11.8, torch 2.1.2, diffusers 0.30.2, transformers 4.44.2, `server-diffusers-cuda.py`) [V] | Dedicated Deployment. There is no cuda-maxwell runtime DaemonSet, and `build/runtime.yaml` `cuda-maxwell` has `diffusers: false` [V] |
| `wan21-t2v-1p3b-gfx1100`, `wan21-vace-1p3b-i2v-gfx1100` | Listed, on demand (5930k-textgen, priority 500/600) | `diffusers-video:rocm-gfx1100` (`Dockerfile.diffusers-rocm` with `ROCM_PYTORCH_TAG=rocm6.4.1…pytorch_2.6.0`; diffusers 0.36.0, transformers 4.48.0, accelerate 0.34.0, bnb 0.49.2) [V CI job + requirements] | `dedicatedDeployment: true` |
| `stable-audio-open-gfx1100` | Listed, on demand (priority 450) | `diffusers:rocm-gfx1100-audio2` (ROCm 6.2.3, torch 2.3.0, diffusers 0.36.0, bnb 0.49.2) [V] | A Deployment **only because the 5930k runtime DaemonSet is desired=0**. `spec.image` is not a runtime opt-out (`pkg/runtime/payload.go` `DirectRuntimeLoadEligibility` checks only `dedicatedDeployment`, CPU vendor, bundled backends and cache) [V code]. Re-enabling that DaemonSet would silently move this lane onto the runtime image [I] |
| SDXL, FLUX, inpaint, pix2pix manifests | On disk, **not listed** (`image-gen.yaml`, `sdxl-*`, `flux-fill-inpainting.yaml`, `gonzalomo-fluxpony-imagegen.yaml`, `instruct-pix2pix.yaml`) [V] | GPUProfile `diffusers:rocm-gfx1100` / `rocm-gfx906` (torch 2.3 family) | — |
| XTX runtime pod (`gfx1100` DaemonSet, serving persona `cfefae18`) | Running; hosts `snac-decode-gfx1100` | torch 2.9, ROCm 7.2, py3.12, vLLM 0.14; **diffusers 0.40 and transformers 5.17 came along unpinned** [V index row `gfx1100-serving-2026-10-23`]. `gguf` comes from the vLLM extras layer and bnb from the wheel, both unpinned [V `Dockerfile.runtime-serving`]. `server-diffusers.py` is at `/opt/flexinfer/` [V] | "No diffusers lane runs through the XTX runtime pod today" [V index]. The runtime path is unexercised for image |

**Runtime mechanics that matter for in-place swaps** [V code]:

- The runtime pod is privileged with `/dev/kfd` and `/dev/dri` mounted and does **not** request
  device-plugin GPUs (`charts/flexinfer/templates/runtime-daemonset.yaml`). It coexists with
  dedicated lanes, and the shared-group election arbitrates VRAM.
- The manager launches `python /opt/flexinfer/server-diffusers.py` as a subprocess
  (`internal/runtime/manager.go` `inferCommand`) and sets `LOCAL_MODEL_PATH` to the node-local
  cache dir.
- **The env precedence is a latent bug for diffusers on this path.** The backend env sets
  `TORCH_BLAS_PREFER_HIPBLASLT=0` for gfx110x (`backend/diffusers.go`). GPUProfile env is then
  pushed into `req.Env` and overlaid last (`pkg/runtime/payload.go` `applyGPUProfileRuntimeEnv`,
  then `overlayEnvVars`). The gfx1100 GPUProfile sets `TORCH_BLAS_PREFER_HIPBLASLT=1`, so in the
  runtime pod diffusers runs with hipBLASLt preferred [I from code; not run].

### 3b. What the diffusers backend serves (contract and config keys)

`backend/diffusers.go` implements the `Backend` contract (`backend/interface.go`: Name, Image,
Port 8000, Args=nil, Env, probes, StartupTimeout 900 s, NeedsVolume, IsImageGeneration,
DefaultIdleTimeout 10 m). It also implements `CompilationCacheConfigurer`. It does **not**
implement `LoRASupporter` or `QuantizationSupporter` [V], so there is no LoRAAdapter CRD
integration for image models.

All behaviour lives in `build/server-diffusers.py` (2,693 lines), driven by env that is mapped from
Model `config`:

- **Model selection**: `modelFamily`, `pipelineMode`, `singleFile{Config,Pipeline,Strict}`.
- **Quantization and dtype**: `quantization: nf4`, `computeDtype`, `dtype`, `useFp16`.
- **Components**: `vaePath` / `vaeRepo`, `lora{Path,Repo,WeightName,AdapterName,Scale}`,
  `controlnet{Path,Repo,Scale}`.
- **Sampling defaults**: `scheduler`, `numInferenceSteps`, `guidanceScale`, `negativePrompt`,
  `strength`, `imageGuidanceScale`.
- **Video**: `video{Frames,Fps,Size}`.
- **Memory and performance**: `cpuOffload`, `enableVaeTiling`, `compile{Mode,Fullgraph,Dynamic,
  RepeatedBlocks}`.
- **Warmup and ROCm knobs**: `warmup{Resolutions,Width,Height}`, `skipWarmup`, `miopenFindMode`,
  `useRocmAiterRopeBackend`.

The family allowlist is `{flux, sdxl, sd3, sd15, wan}`; anything else falls through to
`AutoPipelineForText2Image` and then `DiffusionPipeline` [V].

### 3c. Gap table: model families and capabilities

"Classes in" gives the diffusers version that added the pipeline. [V] means release notes or docs
were seen; [I] marks training knowledge or code reading only.

| Family / capability | Classes in | `server-diffusers.py` today | Dedicated diffusers images (0.36.0 / tf 4.48 / torch 2.3–2.6) | Stacked runtime (0.40 / tf 5.17 / torch 2.9) | Missing |
|---|---|---|---|---|---|
| SD 1.5 (t2i, InstructPix2Pix, single-file) | old | **Served** (`sd15`, `instruct`) | works | should work, untested [I] | — |
| SDXL (t2i, img2img, inpaint, ControlNet, CivitAI single-file + hot-swap, startup LoRA) | old | **Served** | works (manifests parked) | untested | — |
| FLUX.1 dev/schnell/Fill/img2img/ControlNet + NF4 transformer | 0.30 [I] | **Served** (`flux`; NF4 only here) | works (torch-2.3 polyfills for `RMSNorm` / `enable_gqa`) | untested | NF4 text encoder |
| FLUX.1 Kontext (edit) | 0.35 [V title] | no family or edit mapping | — | classes present [I] | family, edit route |
| SD3 / SD3.5 | 0.29 / 0.31 [I] | family defaults and compile only; loads via AutoPipeline; the GPUProfile comment points SD3 at a vLLM-Omni example [V] | partial | untested | explicit path |
| Qwen-Image v1 (20B) / Edit / Edit-Plus / Inpaint / ControlNet / Layered | 0.35–0.36 [V docs list] | not routed | **blocked: needs transformers ≥ 4.49** (the server's own comment) [V] | classes present; a 20B model needs GGUF/NF4 on 24 GB | family + GGUF/NF4 |
| **Qwen-Image 2.1 / Turbo** | **0.41.0** [V docs] | not routed; **`guidance_scale` is always passed** to the pipeline in generation and warmup [V], but `QwenImage21Pipeline.__call__` has no such argument [V signature] → TypeError [I] | blocked: diffusers < 0.41, transformers < 5.17, no `gguf` | **blocked only by diffusers 0.40** | pin + family + kwarg contract + GGUF |
| FLUX.2 dev / klein | 0.36 [V release snippet] | not routed | blocked (TE needs newer transformers) [I] | classes present [I] | family; FLUX.2 dev is too big without GGUF |
| FLUX.3 Klein / Klein Base | sd.cpp day-0 2026-10-16 [R]; diffusers unknown | — | — | unknown | watch |
| Z-Image (Turbo) | 0.36 [V release snippet] | not routed | blocked (Qwen3 TE) [I] | classes present [I] | family |
| HiDream-I1 (17B), Chroma, Krea 2, MiniMax-H3, Ideogram4 | 0.33–0.41 [I; LoRA mixins seen in docs V] | not routed | mostly blocked | present [I] | family; GGUF for 17B |
| Wan 2.1 T2V / VACE I2V (video) | 0.33–0.34 [I] | **Served** (`text2video`, `image2video`) | works (torch 2.6 image) | untested; lacks `ftfy` / `imageio(-ffmpeg)` [V pip list diff] | deps in overlay; requalify |
| Wan 2.2, LTX-2.5 (video) | 0.35 / **0.40** [V title / 09-13 doc] | not routed (`ltx2` mode proposed in the 09-13 doc) | blocked | classes present | modes |
| Stable Audio Open | old | **Served** (`text2audio`) | works (torch 2.3 audio2) | untested; lacks `torchsde` [V pip list diff] | dep; requalify |
| **GGUF loading** | 0.32 [R] | **none** | no `gguf` package | `gguf` present [V], untested | loader path + side-artifact staging |
| NF4 (bnb) | 0.31 [I] | FLUX transformer only | bnb 0.49.2 pinned (0.50 needs torch ≥ 2.4) [V] | bnb unpinned wheel [V] | any family, plus text encoders |
| torchao / fp8 / SVDQuant | — | none | — | — | not useful on RDNA3 (no FP8; nunchaku is NVIDIA-only) [I] |
| LoRA | — | startup only, families {sdxl, sd3, sd15, flux} [V] | — | — | widen the gate. **No `QwenImage21LoraLoaderMixin`** in the diffusers main loader reference (only `QwenImageLoraLoaderMixin`) [V docs scan] |
| img2img / inpaint / edit / multi-reference | — | SDXL, FLUX, SD1.5 only; `/v1/images/edits` takes a single image + mask [V] | — | — | `image=[...]` list for QwenImage21; `EDIT_MODELS` in imagegen-ui |
| ControlNet | — | SDXL + FLUX [V] | — | — | Qwen-Image 2.1 "fun controlnet union" exists in the Comfy layout only [V Comfy tree] |
| Request schema | — | `guidance_scale` 0–20, `negative_prompt`, steps, seed, `control_image` [V] | — | — | `true_cfg_scale`, `sigmas`, `output_resolution`, `use_kv_cache` |

### 3d. Runtime-image pin delta needed for Qwen-Image 2.1

| Package | `diffusers:rocm-gfx1100(-audio2)` | `diffusers-video:rocm-gfx1100` | Stacked `gfx1100-serving` (`cfefae18`) | Needed |
|---|---|---|---|---|
| base / torch | rocm/pytorch ROCm 6.2.3, py3.10, **torch 2.3.0** | ROCm 6.4.1, py3.10, torch 2.6.0 | rocm/vllm-dev ROCm 7.2, py3.12, **torch 2.9**, vLLM 0.14 | torch ≥ 2.4 [V card] |
| diffusers | 0.36.0 | 0.36.0 | **0.40 (unpinned)** | **≥ 0.41.0** (`QwenImage21*` + single-file) [V docs] |
| transformers | **4.48.0** | 4.48.0 | 5.17 (unpinned) | ≥ 5.17 [V card] (Qwen3-VL first appeared in 4.57 [I]) |
| accelerate | 0.34.0 | 0.34.0 | unpinned | recent |
| bitsandbytes | 0.49.2 (pinned for torch 2.3) | 0.49.2 | unpinned wheel with gfx1100 kernels | ≥ 0.49.2 for the NF4 TE |
| gguf | **absent** | **absent** | present (vLLM extras) | ≥ 0.10 [I] |
| ftfy, torchsde, imageio, imageio-ffmpeg | present | present | **absent** | needed only to fold Wan / Stable Audio in |

**Why the stacked image is at 0.40 when 0.41.0 shipped 2026-10-06** [I]: the
`Dockerfile.runtime-serving` diffusers `RUN` (line 113) is an unpinned `pip install` whose text has
not changed. BuildKit's imported inline cache reused the layer from the first build. Unpinned
layers therefore freeze at whatever was current when the layer was first built. They do not drift
forward, and they are not reproducible from a cold cache either. Pin them.

### 3e. Folding the image/video diffusers stack into the stacked gfx1100 image (owner constraint)

**Which image is the "stacked" one.**

- `build/runtime.yaml` builds `gfx1100` (broad, `Dockerfile.runtime`, `values_runtime_profiles:
  false`, the GPUProfile fallback) and `gfx1100-serving` (`Dockerfile.runtime-serving`,
  `values_runtime_profiles: true`, **the image the runtime DaemonSet runs**) [V].
- Every diffusers change must land in `Dockerfile.runtime-serving` and be mirrored in
  `Dockerfile.runtime` (gfx1100 and gfx906 profiles). `cuda-maxwell` has `diffusers: false` and no
  DaemonSet.
- The text lanes still run dedicated runtime digests (`ad93ae72` v0.26 native-W4A16, `2e9652ed`)
  [V]. So "one image per class" is a target the image lanes can move toward first.

**Dependency coexistence with vLLM.** There is no resolver conflict today, because diffusers and
transformers are installed `--no-deps` [V]. The live risks are at the API level:

1. **Hub and safetensors drift across three Python stacks in one site-packages.** Verify by a
   build-time import probe: `import vllm, diffusers, transformers, gguf, bitsandbytes`, plus
   `from diffusers import QwenImage21Pipeline, GGUFQuantizationConfig` and
   `from transformers import Qwen3VLForConditionalGeneration`.
2. **A future vLLM base can pin transformers below 5.17.** The 10-23 doc lists "transformers 5.15"
   in vLLM 0.28's breaking column [V]. If the runtime converges on the v0.32 native-W4A16 base
   (torch 2.13, ROCm 7.2.3), check its transformers bound against Qwen-Image 2.1's ≥ 5.17. Qwen3-VL
   exists from 4.57, so a lower bound may be tolerable; test it [I].
3. **The torch-2.3 polyfills in `server-diffusers.py` go inert on 2.9**, which is harmless [V].
   The bnb 0.49.2 pin rationale no longer applies [V].
4. **ROCm 7.2 MIOpen and hipBLASLt behaviour differs from 6.2.3/6.4.1.** Re-validate VAE decode
   (2D and Wan's 3D convs), keep `MIOPEN_FIND_MODE=2`, and fix or A/B the hipBLASLt env precedence
   noted in §3a.

**Image size and layer placement** [I]:

- The `diffusers` wheel is single-digit MB, about tens of MB installed. `ftfy`, `torchsde`,
  `imageio` and `imageio-ffmpeg` (which bundles an ffmpeg binary) add ~50–80 MB.
- The serving image is ~11 GB [V workhorse comment: "11 GB runtime image: 10 min re-pull"].
- **Where the change goes decides the node pull.** Editing the existing diffusers `RUN` (line 113)
  invalidates every later layer, including the vLLM layers (line 145 onward), so nodes pull most of
  the image again. A **final overlay layer** (`pip install --no-deps diffusers==0.41.x …` just
  before `COPY build/server-diffusers.py`) means nodes pull only tens of MB. Prewarm stays cheap
  and the swap-latency budget is untouched.

**What folding Wan and Stable Audio needs:**

- Add the four missing deps above.
- Requalify each lane against its original gate:
  - Wan T2V 832×480×81 wall time within 10 % of the dedicated image, after recompiling under the
    torch 2.9 inductor cache.
  - VACE first-frame preservation.
  - Stable Audio 10 s at 100 steps ≤ 13 s.
- Then drop `dedicatedDeployment` and `spec.image` from those manifests.
- Then either enable the 5930k runtime DaemonSet (desired=0 behind `quantization-pause`) or move
  video to the XTX.
- That retires `diffusers-video:rocm-gfx1100` and `diffusers:rocm-gfx1100-audio2`: two fewer
  11 GB-class images per node. Fewer distinct images directly reduces both the 09-13 disk-pressure
  eviction class and the 10-21 Harbor-retention loss class [V index].

**Cold-swap cost in place (runtime pod) for Qwen-Image 2.1** (local cache staged under
`/var/lib/flexinfer/models`; all [I], to be measured in §5):

| Phase | Work | Estimate |
|---|---|---|
| Subprocess start + `import torch, diffusers, transformers` | — | 8–15 s |
| Read the UC GGUF (Q8_0 / Q4_K_M) and convert keys; weights stay uint8 | 7.6 / 4.6 GB | 3–6 s |
| Read the TE bf16 shards and NF4-quantize onto the GPU | 17.5 GB → ~5.5 GiB | 8–20 s; 2–5 s if the TE is pre-quantized to NF4 on disk |
| VAE, processor, scheduler | 0.68 GB | < 1 s |
| Warmup 1024², 4 steps (MIOpen/Triton from the compile-cache hostPath) | — | 5–10 s; +30–120 s once, the first time ever |
| **Total, staged cache** | ~23–26 GB read | **~25–50 s** (≈ 20–35 s with a pre-quantized TE) |
| Same model as a dedicated Deployment | + schedule, container create, startup probe | +15–40 s if the image is resident; **+~10 min** if the 11 GB image is not |
| **Displaced text lane coming back** (XTX Mills lane) | — | **2 m 15 s – 3 m 05 s** [V index] |

The table's conclusion: once the image is resident, the in-place swap saves ~15–40 s per swap.
But the user-visible cost of an image burst on a 24 GB gfx1100 card is dominated by the
whole-card text lane it displaces. No co-residency is possible: an 18 GB image lane cannot share
the card with a 22–23 GB text lane. `snac` (1.5 GB) can stay co-resident under `multiModel` [I].
Keep image lanes on a short `idleTimeout` (~10 min) and, for production, prefer the 8-step Turbo
checkpoint to shrink GPU occupancy.

## 4. Ranked slices for image inference (stacked image first)

R1–R3 ship in **one** runtime image build, because `server-diffusers.py` is baked into the image.
R4 (the Qwen-Image 2.1 canary) is gated on that build and on the §6 kill-test.

| # | Slice | What changes | Files | Size | Unblocks | Kill-test |
|---|---|---|---|---|---|---|
| **R1** | **diffusers 0.41.x pin in the stacked runtime, as a late overlay layer** | Final `RUN pip install --no-deps diffusers==0.41.x` (plus exact-pin the transformers already resolved, 5.17.x, to stop drift). Build-time asserts: `QwenImage21Pipeline`, `GGUFQuantizationConfig`, `Qwen3VLForConditionalGeneration`, `gguf ≥ 0.10`, `bitsandbytes` HIP present, `import vllm` still OK. Mirror in the broad gfx1100 and gfx906 profiles | `build/Dockerfile.runtime-serving`, `build/Dockerfile.runtime`, `build/runtime.yaml` (comment), `.gitlab-ci.yml` (none expected) | 0.5 d + 1 CI build | Every family whose pipeline landed in 0.35–0.41 (Qwen-Image v1/2.1, FLUX.2, Z-Image, HiDream, Chroma, LTX-2.5) | New digest: snac decodes, `vllm` imports, prewarm pull delta < 200 MB. **S0 smoke**: an SD 1.5 debug lane loads *through the XTX runtime pod* (no Deployment object created) and returns a coherent 512² image. This is the first proof that the stacked diffusers path works at all |
| **R2** | **Family-agnostic call contract** | (a) Filter generation, edit and warmup kwargs by `inspect.signature(pipe.__call__)`. (b) Map `guidanceScale` / request `guidance_scale` → `true_cfg_scale` for `qwenimage*`. Send `negative_prompt` only when the pipeline accepts it and CFG is on. (c) Families `qwenimage`, `qwenimage21`, `flux2`, `zimage`, `chroma`, `hidream` with defaults (`qwenimage21`: 40 steps, cfg 1.0; Turbo: no steps, honour `sample_sigmas`). (d) LoRA gate = `hasattr(pipe, "load_lora_weights")`, not an allowlist. (e) Request fields `true_cfg_scale`, `output_resolution`. (f) Make the hipBLASLt override survive the GPUProfile overlay, or A/B it | `build/server-diffusers.py`, `backend/diffusers.go` (+`_test.go`: `trueCfgScale` env), new CPU unit test for the kwarg filter | 1 d | Any new family without per-family `if` ladders; fixes a TypeError class on every guidance-free pipeline | CPU test: a fake pipeline with QwenImage21's signature receives no `guidance_scale`; the SDXL path unchanged |
| **R3** | **Quantized component loading** | (a) `transformerQuantization: gguf` + `transformerFile` → `<Transformer class from model_index>.from_single_file(path, quantization_config=GGUFQuantizationConfig(compute_dtype=bf16), config=<root>, subfolder="transformer")`, then dequantize every `GGUFParameter` that is not held by a `GGUFLinear` (the §6b shim; without it sd.cpp-converted GGUFs with BF16 norm weights raise on the first forward), then `Pipeline.from_pretrained(root, transformer=…)`. (b) `quantization: nf4` and `textEncoderQuantization: nf4` for any family via `PipelineQuantizationConfig` (transformer and/or text_encoder). (c) **Side-artifact staging**: generalize the `vaeRepo` → `/models/.vae/<name>` pattern in the cache jobs to `transformerRepo` + `transformerFile` + `transformerRevision` → `/models/.transformer/<file>`, with the same "marker exists but side artifact missing" check. The Local cache marker is sha256(`spec.source`) only [V memory note], so a side file must be checked by presence, not by the marker. (d) Optional: persist the NF4 TE (`save_pretrained`) to cut the cold swap by ~5–15 s | `build/server-diffusers.py`, `backend/diffusers.go`, `controllers/model_cache_download.go`, `controllers/model_cache_jobs.go`, `controllers/model_cache_jobs_local.go` (+ tests) | 1.5–2 d | GGUF for Qwen-Image 2.1, FLUX, Qwen-Image v1, HiDream; NF4 for any TE; the 24 GB fit in §2d | §6 K2 runs this exact loader on CPU; cache-job unit test stages a side GGUF and is idempotent on re-run |
| **R4** | **Qwen-Image 2.1 canary (this candidate)** | §5: arm A control (the derivative's `base`-branch GGUF from the same converter, or a diffusers-layout GGUF if §6 says layouts differ), arm B UC Q8_0 then Q4_K_M. Debug Model on the XTX, first as a throwaway dedicated Deployment of the R1–R3 digest, then runtime-managed | `deploy/debug/qwen-image-21-uc-canary.yaml` (§5 block), `scripts/verify-diffusers-image.py` (new probe), evidence row in `.loom/60-validation-matrix.md` | 1 d + operator window | A real modern image lane; measured cold-swap and s/image numbers for every later image decision | §5 pass bar |
| R5 | Consumer and safety gating (before any UC alias is advertised) | Distinct alias (`image-gen-uc`), never `image-gen`. `litellm.enabled: false` or a non-public name. imagegen-ui: `ENABLE_PROMPT_ENHANCE=false`, or route enhance to a local model, whenever a UC model is selectable. Decide auth for `images.flexinfer.ai` (no ingress auth in gitops today). Promotion checklist §1 provenance annotations | `deploy/models/*`, `services/imagegen-ui` (env / allowlist), `platform/gitops/k3s/ai/imagegen-ui/*` | 0.5–1 d | Any uncensored image lane beyond a debug window | `curl` from outside the LAN cannot reach UC generation; enhance calls never carry UC prompts |
| R6 | **stable-diffusion.cpp backend (`sdcpp`) in the stacked images** | Go backend (`sd-server` args: `--diffusion-model`, `--vae`, `--llm`, `--llm_vision`, `--lora-model-dir`, `--offload-to-cpu`, `--diffusion-fa`). Builder stage mirroring the llama.cpp stage (`GGML_HIP` for gfx1100/gfx906, CUDA sm_52 for Maxwell). `inferCommand`, proxy route, multi-file cache | `backend/sdcpp.go` (+test), `backend/constants.go`, `internal/runtime/manager.go`, `internal/runtime/ingress.go`, `build/Dockerfile.runtime*`, `build/runtime.yaml` | 3–5 d | Native loading of every community GGUF (including this derivative's sd.cpp-layout files) and runtime LoRA directories (one base plus a UC LoRA toggle instead of two weight sets). No Python coexistence with vLLM. The only plausible route on the 980 Ti or VII. Binary ~100–300 MB [I] | `sd-server` Qwen-Image 2.1 Q4_K_M, 1024², 40 steps on gfx1100: coherent and ≤ 1.5× the R4 diffusers time. **Promote to #2 if §6 fails** |
| R7 | Fold Wan T2V/VACE and Stable Audio into the stacked image; retire two dedicated images | §3e: deps in the overlay, requalify, drop `dedicatedDeployment` / `spec.image`, decide the 5930k DaemonSet | `build/Dockerfile.runtime-serving`, `deploy/models/wan21-*.yaml`, `deploy/models/stable-audio-open-gfx1100.yaml`, `deploy/system/values-k3s.yaml` | 2–4 d | One gfx1100 image for all diffusers work; two fewer images resident | Original lane gates (§3e) |
| R8 | Qwen-Image 2.1 edit, Turbo and RGBA surface | `/v1/images/edits` → `QwenImage21Pipeline(image=[…])` multi-reference. Turbo lane (official, clean) as the latency lane. RGBA passthrough to PNG. imagegen-ui `EDIT_MODELS` | `build/server-diffusers.py`, `deploy/models/*`, imagegen-ui | 1–2 d | Editing in imagegen-ui (currently off) | Edit of a reference keeps the subject; RGBA PNG has a non-trivial alpha |
| R9 | Watch | vLLM-Omni refresh (day-0 Qwen-Image 2.1 with prefix KV and CUDA graphs; ROCm diffusion unproven; ours is 0.14). cuda-maxwell diffusers stacking (low value: the 980 Ti stays SD 1.5). FLUX.3 Klein in diffusers | — | — | — | — |

### 4a. R1–R3 as landed (2026-10-25)

Branch `feat/qwen-image21-diffusers-r1-r3`, one MR. Nothing was applied to the cluster, and no
uncensored alias exists. The dedicated diffusers images (`Dockerfile.diffusers-rocm*`,
`-cuda`) are untouched. They pick up the new `server-diffusers.py` on their next rebuild. All
new imports are lazy and configured-only, so the server still loads on diffusers 0.30–0.40 [I].

**R1: diffusers overlay** (`build/Dockerfile.runtime-serving`, `build/check-runtime-diffusers.py`)

- The layer sits after the vLLM layers and before `COPY build/server-diffusers.py`. It runs
  `pip install --no-deps diffusers==0.41.0 transformers==5.17.0`. The transformers pin is a no-op
  on `cfefae18`, and holds the version on a cold-cache rebuild.
- **huggingface-hub pin (2026-10-28).** The first master build of the overlay (pipeline 35473, job
  426127) failed its own check three times: `huggingface-hub>=1.5.0,<2.0 is required ... but found
  huggingface-hub==2.3.0`. The early diffusers layer installs `huggingface-hub` unpinned and its
  cached 2026-10-23 layer already held 2.3.0; transformers 5.17.0 (`<2.0,>=1.5.0`) and diffusers
  0.41.0 (`<2.0,>=1.32.0`) both refuse to import against it. `cfefae18` carried the same mismatch
  latently, since no diffusers lane runs through the XTX runtime pod. The overlay RUN now also
  installs `huggingface-hub>=1.32,<2.0` (`HF_HUB_OVERLAY_CONSTRAINT`, `--no-deps` like the rest),
  `check-runtime-diffusers.py --huggingface-hub` fails the build when the installed hub leaves that
  range, and `check-runtime-patch-contracts.py` asserts the pin rides in the overlay RUN. The early
  layer is untouched so the cached ~17 GB layers stay valid.
- `scripts/check-runtime-patch-contracts.py` now enforces that position, the COPY and the check.
- 0.41.0 rather than the newer 0.42.0: it is what §6b ran. 0.42.0 has the same GGUF BF16
  behaviour, so a bump buys nothing for Qwen-Image 2.1.
- The check runs after the COPY and fails the build unless all of these hold:
  - the two pins are what imports;
  - `QwenImage21Pipeline` and `QwenImage21Transformer2DModel.from_single_file` exist;
  - the pipeline's `__call__` takes `true_cfg_scale`, `negative_prompt`, `image` and
    `output_resolution`, and **no `guidance_scale`**;
  - the `GGUFQuantizationConfig` / `BitsAndBytesConfig` / `PipelineQuantizationConfig` classes and
    the shim's `GGUFLinear` / `GGUFParameter` / `dequantize_gguf_tensor` import;
  - `Qwen3VLForConditionalGeneration` imports;
  - gguf ≥ 0.10, and bitsandbytes ships a ROCm library;
  - `import vllm` still works;
  - `server-diffusers.py` imports.
- **Off-CI proof** (2026-10-25 21:50–21:53Z) [V]. The serving image builds only on master. So the
  exact overlay section was layered onto the deployed digest `cfefae18` on the `7900xtx` Docker
  context, with CI's build args and no GPU devices.
  - diffusers went 0.40.0 → 0.41.0; transformers 5.17.0 was already installed.
  - Every check passed: gguf 0.17.1, bitsandbytes 0.50.2 with `libbitsandbytes_rocm72.so`,
    vLLM 0.17.0.
  - The overlay layer is **53.4 MB**, against the < 200 MB pull bar.
  - The XTX kernel log showed no amdgpu, SMU or PCIe lines through the run. The proof images were
    removed afterwards, and the host Docker store is back to one image.
- **Not done**: the broad `Dockerfile.runtime` (gfx1100 unified, gfx906) still carries unpinned
  diffusers. Only the serving persona runs the runtime DaemonSet. Mirror the overlay there when a
  lane needs it.

**R2: call contract** (`build/server-diffusers.py`)

- **The adapter.** `_call_pipeline` → `_adapt_call_kwargs` → `_filter_call_kwargs` reads
  `inspect.signature(pipe.__call__)` once per class.
  - A pipeline that takes `**kwargs` (SDXL, SD 1.5) receives the same kwargs as before.
  - A strict signature drops what it lacks and logs the drop once, instead of raising TypeError.
  - Routed through it: generation, every `/v1/images/edits` branch, variations and warmup. Video
    and audio are unchanged.
- **Families `qwenimage21` and `qwenimage`.** Routed by `modelFamily`, by the model_index class
  (`QwenImage21*` / `QwenImage*`) and by repo id. A dedicated load branch uses
  `DiffusionPipeline` with the model_index class, ahead of the SDXL edit-mode branches.
  - Defaults: 40 steps (Turbo 8; it samples `sample_sigmas` anyway) and true-CFG 1.0. v1: 50 steps,
    4.0.
- **CFG mapping.** The request `guidance_scale` → `true_cfg_scale`. An explicit request
  `true_cfg_scale` or config `trueCfgScale` (`DEFAULT_TRUE_CFG_SCALE`) wins. Above 1 a blank
  negative prompt is supplied, because the pipeline runs CFG only with one. At or below 1 the
  negative prompt is dropped.
- **New request fields**: `true_cfg_scale`, `output_resolution`.
- **Startup LoRA** is gated on `hasattr(pipe, "load_lora_weights")`, not on a family allowlist.
- **Reference edits (the cheap half of R8).** On a `qwenimage21` pipeline in the default
  text2image mode, `/v1/images/edits` sends the upload as `image=[...]`.
  - Size comes from `size`, or else from the reference's aspect ratio.
  - A `mask` gets a 400: mark the region in the image instead.
  - Not yet built: multi-reference uploads, RGBA, and imagegen-ui `EDIT_MODELS`.
- **Not done:**
  - (c) families `flux2`, `zimage`, `chroma`, `hidream`. The filter plus the AutoPipeline fallback
    now carry their call shape, but their defaults are still the generic 20 steps / 7.5.
  - (f) the hipBLASLt precedence on the runtime path. The canary manifest says to A/B it in the
    stage B window.

**R3: quantized component loading** (`build/server-diffusers.py`, `backend/diffusers.go`,
`controllers/model_cache_side_artifacts.go`, `controllers/model_cache_jobs*.go`,
`controllers/model_cache.go`)

- **`transformerQuantization: gguf`.**
  - The class comes from model_index (or `transformerClass`).
  - The file is `transformerPath`, or `<model root>/.transformer/<transformerFile>`. Failing both,
    it is fetched from `transformerRepo@transformerRevision` with a warning.
  - The load is `from_single_file(…, GGUFQuantizationConfig(compute_dtype), config=<snapshot>,
    subfolder="transformer")`, then the **§6b shim** (`_dequantize_non_linear_gguf`), then the
    model is passed in as `transformer=`. FLUX takes a GGUF transformer through the same path.
- **NF4 on load.**
  - `transformerQuantization: nf4` (or `quantization: nf4`) quantizes the transformer or UNet for
    any family. FLUX keeps its own NF4 path.
  - `textEncoderQuantization: nf4` quantizes every transformers text encoder, using the
    transformers `BitsAndBytesConfig` inside one `PipelineQuantizationConfig`.
  - `computeDtype` sets the compute dtype of both.
- **GPU placement.** NF4 GPU placement is now generic: bitsandbytes components stay where they
  were loaded, and every other module moves individually. This is detected and configured, so the
  FLUX NF4 path behaves as before.
  - It also removes an `UnboundLocalError` (`quant_mode`) that any single-file checkpoint would
    have hit after loading [I].
- **Side-artifact staging.** The `HF://` stage, Local and SharedPVC, fetches `transformerFile`
  from `transformerRepo` at `transformerRevision` into `<model cache dir>/.transformer/` after the
  snapshot. The check is presence, not the marker:
  - a re-run with the file in place downloads nothing;
  - a changed `transformerFile` (the Q8_0 ↔ Q4_K_M A/B) fetches the new file alone and removes the
    old one, without touching the snapshot;
  - a download that ends without the file fails the Job.
- **Staged key.** `status.cache.stagedKey` gains `transformerRepo` / `File` / `Revision` with
  `omitempty`, so every existing staged Local lane keeps its key and the controller roll stages
  nothing.
- **Not done:**
  - (d) persisting the NF4 text encoder.
  - A SharedPVC prefetch Job is recreated only on a source change (the !1138 bug class), so a
    `transformerFile` change there needs the Job deleted by hand.
  - The SharedPVC copy Job skips dot-directories.
  - A `transformerFile` edit inside the 300 s TTL of a finished Local stage Job reads that Job's
    success. !1137's stale-Job check keys on download options only, so it should add the
    side-artifact fields when it lands.
- **Merge interplay with !1137 / !1138** (both open): they rewrite the same two stage scripts, so
  the second MR to land rebases. !1137's legacy-marker adoption should skip `.transformer/` the way
  it skips `.vae/`. Without that, a pinned lane with a side artifact re-stages once; no live lane
  has one.

**Tests**

- `scripts/test_server_diffusers.py`, new CI job `server_diffusers_test`. 35 cases, run three
  ways:
  - **Bare `python:3.12-slim`** (heavy libraries stubbed): 33 run, 2 skip.
  - **Host, mixed** real torch / fastapi / pydantic / PIL: 32 run.
  - **Inside the overlay image**, real ROCm torch, diffusers 0.41.0, transformers 5.17.0:
    34 run, 1 stub-only skip.
  - What they cover:
    - a QwenImage21-signature fake receives no `guidance_scale`, and generation, warmup and edits
      map guidance onto `true_cfg_scale`;
    - the SDXL kwargs are unchanged;
    - family routing;
    - side-file resolution;
    - the GGUF / NF4 component kwargs, checked against the real config classes in the image;
    - bitsandbytes placement;
    - **the shim on a real BF16 `GGUFParameter`**, after which `RMSNorm` runs;
    - the real 0.41.0 signature.
- `controllers/model_cache_side_artifacts_test.go`. Besides env wiring and the backward-compatible
  staged key, it **executes the real Local stage script** against a temp dir with a fake
  huggingface_hub, in four cases: fresh stage, idempotent re-run, A/B switch without re-fetching
  the snapshot, and a failed download → FATAL.
- `backend/diffusers_test.go` covers the new env keys.

**Draft canary**: `deploy/debug/qwen-image-21-canary.yaml`. It is arm A, official weights, NF4
transformer + NF4 text encoder, not applied and not in `deploy/debug/kustomization.yaml`. It
corrects the §5 sketch on four points:

- `gpu.count: 2` with `hipVisibleDevices: "0"`: the XTX exposes the Raphael iGPU as the second
  `amd.com/gpu`, like the warm Mills lane;
- the official repo is 33.13 GB, with a 1.35 GB VAE file;
- head `d26bb612` has weights byte-identical to `b3179ad3` (LFS oids checked), so both arms share
  one snapshot;
- `warmPolicy: ondemand` with `minReplicas: 1` for the window.

Arm B's delta sits at the end of the file.

### 4b. The demand-based lane (2026-10-26)

Owner ask (2026-10-26): serve Qwen-Image 2.1 on demand so it can take one of the gfx1100 cards.
That is `deploy/models/qwen-image-21-gfx1100.yaml`, arm A's config (official weights, NF4
transformer + NF4 text encoder, no CFG, 40 steps) as a scale-to-zero member of a shared-GPU
election group. It is on branch `feat/qwen-image21-5930k-lane` (Draft !1175), stacked on the R1–R3 MR (!1165). It
cannot merge before that MR does, because its image is the serving build master CI makes from the
R1–R3 merge.

**Placement: the 5930k, not the XTX runtime pod.** The stacked-image goal points at the XTX runtime
DaemonSet, but two facts rule it out today:

- **The XTX card is the warm Mills lane's.** `qwen38-27b-xtx-warm-canary` is warm-pinned
  (minReplicas 1, `warmPolicy: primary`, priority 470, vramEstimateMB 23200 = the whole card).
  The owner decided on 2026-10-03 that it never loses its card [V manifest]. An 18 GB image lane
  cannot sit beside it, so any priority that wins the card would evict Mills for a 2–3 min reboot
  per image session. The election allows that (a strictly higher-priority demand preempts even a
  warm-pinned leader, and `warmPolicy: primary` reclaims the card once the borrower idles) [V
  `controllers/model_shared_gpu.go`]. It is the owner's call, not this lane's.
- **The XTX card drops off the bus under sustained compute.** The last drop was 2026-10-24
  14:03Z, under the 2100 MHz cap, and it failed all seven XTX lanes until a hard power cycle [V
  memory note]. Diffusion at 1024² is compute-bound at full board power, which is the matmul-burn
  signature that wedged it. The 5930k card runs flat out for days.

The 5930k fits:

- `5930k-textgen` is already the demand-elected group for the generation lanes (wan21 T2V and
  VACE, stable-audio).
- No member there is warm-pinned outside a canary window. On 2026-10-26 every member was
  minReplicas 0 except the S3 attention canary [V cluster].

The cost is pod churn. The 5930k runtime DaemonSet is desired=0, so the lane is a dedicated
Deployment like every lane on that card. It can move in-pod at S6b/S6e (gfx1100 research §7e):
once the 5930k persona (vLLM 0.32, diffusers 0.42) also ships bitsandbytes, drop
`dedicatedDeployment`. Until then `dedicatedDeployment: true` is load-bearing. Without it,
un-parking that DaemonSet would load the lane into an image with no NF4.

**Election.** Priority 500 is the borrow-idle posture of the 5930k workhorse lanes and wan21 T2V:

- An image request takes the card from any lower-priority holder at once.
- From an equal-priority holder (the 27B workhorse), it takes the card once that holder has had no
  request for 2 min (`sharedDemandWindow`).
- It never cuts a text completion in flight: the proxy heartbeats `LastActiveTime` while a request
  is open [V `internal/proxy/connections.go`].
- VACE (600) and the 9B RP twin (550) still outrank it.
- Hand-back: after 2 idle minutes, equal-priority demand swaps the card back. With no demand at
  all, the lane scales to zero at `idleTimeout: 15m`.
- The 5 min anti-thrash cooldown applies in both directions.
- 520 would let images preempt busy text. That is a one-line change if the owner wants it.

**Routing.**

- LiteLLM-advertised as `qwen-image-21-gfx1100` with aliases `qwen-image` and `qwen-image-2.1`,
  plus the same two serviceLabels. Advertising it is also what lets the proxy cold-start it by
  name.
- It does **not** take `image-gen` / `dall-e-3` / `text-to-image`. Those stay on the warm
  Dreamshaper lane on the 980 Ti, which answers in seconds.
- imagegen-ui sends generations and edits straight to flexinfer-proxy. It lists only the models
  in `GENERATION_MODELS` / `EDIT_MODELS` that resolve in LiteLLM's catalog
  (`server/app.py` `resolve_configured_models`) [V]. Adding `qwen-image` to both is a platform/gitops
  change to `k3s/ai/imagegen-ui/deployment.yaml` (Draft platform/gitops!996), to merge after the verification below.
  The edit tab can paint a mask, which this model rejects with a 400 (§4a). Unmasked reference
  edits work.

**Image and prewarm.**

- The lane pins the R1–R3 serving build, `rocm-gfx1100-serving-<merge sha>` (~18.2 GB).
- That build's vLLM 0.17 base is not resident on the 5930k: `cfefae18` was released there on
  2026-10-24 to clear DiskPressure. So the 5930k `imagePrewarm` profile lists the digest, which
  means one full pull.
- Disk: 280 GB free (70 %) on 2026-10-26, so ~229 GB after the image and the 33 GB staged cache.
  That is above the ≥ 200 GB rule and the 15 % DiskPressure floor.
- Without the prewarm entry, the node's 60/50 image GC would drop the image between sessions, and
  every cold start would re-pull 18 GB.
- `config.env` pins `TORCH_BLAS_PREFER_HIPBLASLT: "0"` (controller 37317c95 applies Model env last
  on both launch paths). That closes R2f at the manifest: the dedicated Deployment and a future
  in-pod load now agree.

**Also on the R1–R3 MR.** `publish_serving_rocm_gfx1100_v032` rebuilds whenever
`server-diffusers.py` changes. Its `verify_runtime_serving_stack.py` fails the build on any
import outside a try block that does not resolve. The §6b shim's `diffusers.quantizers.gguf.utils`
import pulls in the `gguf` package, so it is now inside a try block and raises a clear error
instead. Every other name R3 imports exists in diffusers 0.42 without the optional extras [I].
That gate is first exercised by the master build after the merge.

**Merge order.**

1. Merge the R1–R3 MR. Master CI's `publish_serving_rocm_gfx1100` runs
   `check-runtime-diffusers.py` inside the build, so a green job is R1's proof on the real image.
2. Put that job's digest into the lane's `spec.image` and the 5930k prewarm entry. Both read
   `R1R3_SERVING_DIGEST` until then, and the lane MR stays Draft.
3. The operator merges the lane MR. That is the GPU go-ahead. Flux applies the Model and the
   prewarm list. The prewarm pulls the image and the cache Job stages 33 GB, neither on the GPU.
   The lane sits Idle until the first request.
4. Run the verification below, then merge the imagegen-ui change.

**GPU verification (operator window on the 5930k).** Start only after the S3 attention canary is
deleted. Use the §5 prompt set and pass bar; this lane has no stage B.

- **V0 preflight (no GPU):**
  - `flexinfer.ai/gpu.health=ok` on the 5930k;
  - no warm-pinned member left in `5930k-textgen`;
  - the prewarm DaemonSet Ready with the digest, and ≥ 200 GB free;
  - `status.cache.ready` with `sizeBytes` ≈ 33 GB;
  - `journalctl -k` clean.
- **V1 cold start:**
  - Request: one `POST /v1/images/generations` through flexinfer-proxy with `model:
    qwen-image-21-gfx1100`, P2, 1024², `response_format: b64_json`, timed client-side.
  - Record the swap (holder Preempted → Pod Scheduled → Ready), the load lines (`NF4
    quantization on load for: ['text_encoder', 'transformer']`, then the load time) and the
    4-step warmup.
  - Record the first image's wall time.
  - Fail on any `TypeError` about `guidance_scale`. The server instead logs one
    `QwenImage21Pipeline.__call__ does not accept [...]; dropping them` line per distinct set.
- **V2 warm set:**
  - P1–P6 at 1024², 40 steps; median s/image.
  - Peak VRAM from the amdgpu series or `rocm-smi` every 10 s.
  - Host RSS of the pod during load (40 Gi limit).
  - The seven §5 correctness checks.
- **V3 edits:**
  - `POST /v1/images/edits` with V2's P2 image and "make the apple green, keep everything else" →
    coherent.
  - The same with a mask → 400.
- **V4 hand-back and swap back:**
  - After ≥ 5 min (the cooldown) and ≥ 2 idle minutes, send one short `workhorse` completion. Pass:
    the lane is Preempted and the workhorse Ready within its cached cold start (~3 min, recorded
    as the swap time).
  - Then one image request after the workhorse has idled 2 min. Pass: P7 (after reload) is clean.
- **V5 scale-to-zero:** 15 min with no traffic → the lane goes Idle and the card frees.
- **V6 (optional) hipBLASLt A/B:** a one-line MR flips `config.env` to `"1"`, then re-run P1–P5
  warm. Keep the faster setting only if V2's correctness holds.
- **Pass bar:** §5's rows for warm s/image, peak VRAM, correctness and stability, plus these
  collateral checks:
  - the workhorse returns on its next request;
  - wan21, VACE and stable-audio still win their elections;
  - no 5930k DiskPressure.
- **Evidence:** a `.loom/60-validation-matrix.md` row.
- **Rollback:** remove the kustomization entry, or delete the Model to stop it at once. The
  workhorse takes the card on its next request either way.

**Not done:**

- the in-pod move (S6b + bitsandbytes in the 0.32 persona, i.e. S6e);
- `check-runtime-diffusers.py` against diffusers 0.42 in that persona (S6e);
- 1024-class size presets in imagegen-ui's generate tab (its presets stop at 768; "Model default"
  gives 1024²);
- the Turbo checkpoint;
- the uncensored arm, which stays a debug-window canary gated on R5.

## 5. "Try it" plan for the candidate

**Step 0: CPU only, no cluster (§6 K1 + K2).** Header dump first (~16 MB ranged download), then a
CPU `from_single_file` of the UC Q8_0 or Q4_K_M in devbox (`devbox_exec_async`; first build of a
new worktree image is ~15 min [V memory note]). Downloading the 4.6–7.6 GB GGUF needs the
operator's OK. **If K2 fails, stop the diffusers route for this derivative.** Run R6 (sd.cpp) for
it, and keep R1–R3 for official weights.

**Step 1: build.** R1–R3 land as one MR. CI publishes `runtime:rocm-gfx1100-serving-<sha>`. Prewarm
the digest on the XTX: the pull delta should be small thanks to the late overlay layer.

**Step 2: stage A, the throwaway dedicated canary.**

- This is **not faster to build**: it uses the same digest as stage B.
- It is **faster to roll back** and keeps the runtime pod (snac) out of the blast radius. It also
  qualifies the digest before the DaemonSet is repinned (which means values-k3s, the drift gate,
  prewarm and a snac restart).
- Skip stage A if the R1 S0 smoke already ran on the repinned DaemonSet.
- Delete the CR in the same operator window.

**Step 3: stage B, runtime-managed.** Repin the XTX runtime DaemonSet to the R1–R3 digest (the
!1114/!1116 pattern), remove `image` and `dedicatedDeployment` from the manifest, and measure the
in-place cold swap.

**Debug manifest** (the 2026-10-24 sketch for arm B; the R2/R3 keys it marks exist since §4a).
Arm A, official weights, is drafted in `deploy/debug/qwen-image-21-canary.yaml`. That file
supersedes this block on `gpu.count` (2 on the XTX) and on the arm order.

```yaml
# deploy/debug/qwen-image-21-uc-canary.yaml — NOT APPLIED. Requires R1–R3 and §6 K2 PASS.
# Operator window only: priority 480 > the Mills warm lane (470, minReplicas 1) on
# 7900xtx-textgen, so applying this preempts mills-warm / mills-council for the
# window (Mills falls back per its routing). Delete the CR to end the window;
# the Mills lane returns in ~2–3 min (resident image).
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: qwen-image-21-uc-canary
  namespace: flexinfer-system
  annotations:
    flexinfer.ai/base-model-source: Qwen/Qwen-Image-2.1
    flexinfer.ai/base-model-posture: uncensored        # merged LoRA delta, method undisclosed
    flexinfer.ai/artifact-source: "HF://abenzerps/Qwen-Image-2.1-Uncensored-Quantized@7959e658915f52274897fb573ffeda49de8dc16b qwen-image-2.1-UC-Q8_0.gguf"
    flexinfer.ai/serve-paths: /v1/images/generations
    flexinfer.ai/purpose: "Qwen-Image 2.1 UC debug canary (research/evaluation use only: Qwen Research License)"
    flexinfer.ai/plan: .loom/10-research-qwen-image-21-uncensored-2026-10-24.md
    flexinfer.ai/promotion-state: debug-canary-qwen-image-21-uc
spec:
  backend: diffusers
  # Stage A only: the R1–R3 serving digest as its own Deployment. Stage B: delete
  # `image` and `config.dedicatedDeployment` so the XTX runtime pod loads it in place.
  image: registry.harbor.lan/flexinfer/runtime@sha256:<R1-R3 gfx1100-serving digest>
  source: HF://Qwen/Qwen-Image-2.1     # processor + scheduler + text_encoder + vae
  cache:
    strategy: Local
    storageClass: nvme-1r-gpu
    size: 40Gi                         # ~18.2 GB base minus transformer + 7.6 GB GGUF
    flashLoader:
      enabled: false
    compilationCache:
      enabled: true
      hostPath: /var/lib/flexinfer/compile-cache
      sizeLimit: 6Gi
  gpu:
    vendor: amd
    count: 1
    shared: 7900xtx-textgen
    priority: 480
    vramEstimateMB: 18000              # Q8_0 DiT + NF4 TE + VAE, 1024² peak (§2d)
  serverless:
    enabled: true
    minReplicas: 1                     # forces activation for the window
    idleTimeout: 10m
    coldStartTimeout: 15m
  config:
    dedicatedDeployment: true          # stage A only
    hfRevision: b3179ad355be050328e483a9dfdd9e60cd62adfa   # revision named by the derivative's card; confirm it resolves
    hfIgnorePatterns: ["transformer/*.safetensors", "assets/*"]
    modelFamily: qwenimage21           # R2
    trueCfgScale: "1.0"                # R2 (model default: no CFG)
    numInferenceSteps: "40"
    dtype: bfloat16
    computeDtype: bfloat16
    transformerRepo: abenzerps/Qwen-Image-2.1-Uncensored-Quantized    # R3
    transformerRevision: 7959e658915f52274897fb573ffeda49de8dc16b     # R3
    transformerFile: qwen-image-2.1-UC-Q8_0.gguf                      # R3 (A/B: UC-Q4_K_M)
    transformerQuantization: gguf                                     # R3
    textEncoderQuantization: nf4                                      # R3
    cpuOffload: "false"
    enableVaeTiling: "false"           # flip to "true" for the 2048² informational run
    warmupResolutions: "1024x1024"
    hipVisibleDevices: "0"
    warmPolicy: primary
  nodeSelector:
    kubernetes.io/hostname: cblevins-7900xtx
  resources:
    requests:
      cpu: "4"
      memory: 16Gi
    limits:
      cpu: "8"
      memory: 40Gi
  litellm:
    enabled: false                     # warm + de-advertised: proxy by name only, no public alias
  tolerations:
    - effect: NoSchedule
      key: node-role.kubernetes.io/control-plane
      operator: Exists
```

The control arm is the same manifest named `qwen-image-21-base-canary` with
`flexinfer.ai/base-model-posture: clean`. It uses the derivative's `base`-branch
`qwen-image-2.1-Q8_0.gguf`: same converter, so it isolates the UC delta from the loader. If K1
shows sd.cpp ≠ Comfy layout but K2 passes anyway, keep it. Otherwise use a diffusers-layout GGUF,
or the Comfy-Org bf16 single file with `transformerQuantization: nf4`.

**Probe: `scripts/verify-diffusers-image.py`** (new; modelled on `scripts/verify-vllm-canary.py`).

- POST `/v1/images/generations` through `flexinfer-proxy` by model name with
  `response_format=b64_json`, 1024², n=1, and fixed seeds.
- Record client wall time and the server `[gen]` log line, and save PNGs to an evidence dir.
- Compute per-image channel means, standard deviation and mean-absolute-gradient. Flag:
  - an all-black or near-constant image (std < 5);
  - the ComfyUI #16437 channel-equality signature (R≡B and G≡A);
  - a NaN-decode grey image.
- Read peak VRAM from the scraped `amdgpu_*` VRAM series over the run window. Collect pod and
  subprocess restarts and node dmesg amdgpu/SMU lines.

**Fixed-prompt visual sanity set.** This is SFW. Checking adult-content capability is an
operator-gated, manual step, as with the `nsfw-rp` re-test.

| ID | Prompt | Seed | Checks |
|---|---|---|---|
| P1 | `A neon shop sign that reads "FLEXINFER 2.1", rainy night, reflections on wet pavement` | 42 | text legible (eyeball; optionally the vision-ocr lane) |
| P2 | `a red apple on a wooden table, studio light` | 7 | the cross-runtime reference used by the Windows 7900 XTX report; hue is red, not green or blue (catches channel swap) |
| P3 | `studio portrait of an elderly fisherman, Rembrandt lighting, 85mm` | 11 | skin tones, no grid artifacts |
| P4 | `three cats on a green sofa: an orange cat on the left, a black cat in the middle, a white cat on the right` | 3 | counting and attribute binding |
| P5 | `This is an RGBA image with transparency. A cute cartoon dragon sticker` | 5 | alpha channel has both ~0 and ~255 regions |
| P6 | P1 again, same seed | 42 | byte-identical (fixed `use_kv_cache`) |
| P7 | P2 with seed 8, **after a forced unload and reload** (stage B) | 8 | clean after reload (the DynamicVRAM-class regression) |

Run P1–P5 on arm A and arm B with identical seeds. On SFW prompts, UC should stay close to the
base. A large divergence means the merged LoRA damaged general quality.

**Pass / fail bar:**

| Metric | PASS | MARGINAL (try Q4_K_M, 25 steps, or the clean Turbo lane) | FAIL |
|---|---|---|---|
| Warm s/image, 1024², 40 steps, median P1–P5 | ≤ 90 s | 90–150 s | > 150 s |
| Peak VRAM at 1024² | ≤ 20 GiB | 20–22.5 GiB | OOM or > 22.5 GiB |
| Stage B cold swap (load request → first image) | ≤ 60 s | 60–150 s | > 150 s |
| Correctness | 7/7 checks; no stats flag | — | any noise, black or channel-swap image |
| Stability | 0 restarts, 0 amdgpu/SMU errors, `gpu.health=ok` throughout | — | any GPU-health `degraded` or SMU error |
| Collateral | Mills lane Ready ≤ 5 min after the CR is deleted; snac untouched | — | Mills lane fails to return |

## 6. Riskiest assumption + kill-test

**Load-bearing assumption**: diffusers **0.41.x**
`QwenImage21Transformer2DModel.from_single_file(…, quantization_config=GGUFQuantizationConfig(compute_dtype=torch.bfloat16), config="Qwen/Qwen-Image-2.1", subfolder="transformer")`
loads this derivative's stable-diffusion.cpp-converted GGUF (`general.architecture = qwen_image21`)
with zero missing or unexpected keys after the Comfy→diffusers key conversion, and denoises to a
finite, non-noise output.

**Evidence**:

- **Positive**: the v0.41.0 docs show `from_single_file` for the Comfy-Org bf16 file [V], so a
  Comfy-layout converter exists. diffusers already splits fused, quantized GGUF tensors for FLUX
  (city96 GGUFs in the BFL layout) [I]. ComfyUI loads these GGUFs through leejet's fork [V], which
  suggests a Comfy-compatible tensor naming [I].
- **Negative**: m9ter1a's card says the sd.cpp layout (265 tensors, fused `img_mlp.gate_up`,
  `model.diffusion_model.` prefix) and the diffusers layout (297 tensors, separate `gate_layer` /
  `proj`) "are not interchangeable" [R]. The Qwen-Image single-file GGUF path has had bugs: #12098,
  and #12891's `unexpected keyword argument 'config'` [R]. city96's ComfyUI loader rejects the
  arch string [V].

**Kill test** (≤ 30 min of work plus downloads, CPU only):

1. **K1, header dump (~5 min).**
   - Ranged-download the first 16 MiB of `qwen-image-2.1-UC-Q8_0.gguf`, plus the 8-byte length and
     JSON header of the Comfy-Org `qwen_image_2.1_bf16.safetensors`.
   - Parse the GGUF tensor-info table with a ~60-line pure-Python reader. `gguf-py`'s
     `GGUFReader` wants the whole file.
   - Compare names and shapes: is there a prefix, is `gate_up` fused, and do the counts match?
     Equal modulo prefix means a good sign. A different structure from Comfy means likely fail.
2. **K2, CPU load (~20 min plus a 7.6 GB download).**
   - Environment: devbox with CPU torch, `diffusers==0.41.x`, `transformers==5.17.x`, `gguf`.
   - Run `from_single_file` as above. Assert `missing_keys == unexpected_keys == []`; diffusers
     raises on a mismatch.
   - Run one forward at a 16×16 latent (256 tokens) with random `encoder_hidden_states` of width
     4096. Assert the output is finite with std > 0.
   - Control: repeat with a diffusers-layout GGUF (`m9ter1a/Qwen-Image-2.1-Turbo-GGUF` Q4_K_M) to
     prove the harness works.
3. **Outcome.** PASS = the UC file loads and runs its forward like the control. FAIL = a key or
   shape mismatch, or the `config` TypeError.

**Failure mode if wrong**: R3's GGUF path still ships for diffusers-layout GGUFs and official
weights, but it cannot load *this* derivative. The UC canary then needs one of three things:

- R6 (sd.cpp, which made these files), promoted to #2;
- a lossless row-split repack of the fused `gate_up` into diffusers layout (GGUF blocks run along
  the input dimension, so splitting output rows is byte-exact [I]);
- or a merge of the 33.6 MB UC LoRA into official bf16 weights offline, followed by NF4 on load.
  No `QwenImage21LoraLoaderMixin` exists yet, so the merge needs a key map.

Without the kill-test, R4 would burn an operator window and preempt the Mills lane to discover a
load error that K1 finds in five minutes.

**Second-order assumption** (operational, not technical): an on-demand 18 GB image lane is
acceptable on cards whose text lanes fill the whole card. R4 measures the real cost: the Mills
lane gone for the window plus a 2–3 min reload. If image demand turns out to be frequent, the
answer is the 8-step Turbo lane and request batching, not a different node. The VII and 980 Ti
cannot host the model (§2d).

**Status**: run 2026-10-24. The result is in §6b.

## 6b. Kill-test result (2026-10-24)

**Verdict: FAIL on stock diffusers 0.41.0 by the §6 bar. PASS once a ~12-line shim dequantizes the
BF16 norm weights after loading. The diffusers route stands for this derivative: do not promote R6
(sd.cpp) on account of it. R3 must carry the shim.**

- **Stock (the strict §6 bar).** The UC Q8_0 loads key-clean: 297/297 tensors, zero missing,
  unexpected or shape-mismatched keys, nothing left on the meta device, and no loader warning.
  The first forward then raises `RuntimeError: The size of tensor a (4096) must match the size of
  tensor b (8192) at non-singleton dimension 2` in `txt_in.text_norm`. diffusers 0.42.0 has the
  same code [V wheel diff], so a pin bump does not fix it.
- **With the shim.** The same file runs a finite, non-noise forward and an 8-step Euler run. On
  identical seeded inputs it agrees with the diffusers-layout control at cosine 0.985.
- **If the shim is rejected** (stock diffusers only), R6 remains the fallback, as §6 planned.

Everything below was run CPU-only on the workstation (Apple M4, 24 GB, macOS 26.6.2) in a scratch
`uv` venv with no cluster access:

- torch 2.14.1 (CPU), diffusers 0.41.0, transformers 5.17.0, gguf 0.19.0, accelerate 1.15.0;
- downloads (operator-approved): UC Q8_0 7,591,557,920 B and control Q4_K_M 4,296,481,568 B, plus
  four 16 MiB header ranges;
- both full files matched their HF LFS sha256 oids.

### K1: headers

All rows are pinned by commit. Names and shapes are compared after collapsing block indices.

| File | Tensors | `general.architecture` | Tensor types | vs UC Q8_0 |
|---|---|---|---|---|
| `abenzerps/…-Quantized@7959e658` `qwen-image-2.1-UC-Q8_0.gguf` | 297 | `qwen_image21` | Q8_0 229, BF16 68 | — |
| same repo, `base` branch `@4db8efdb` `qwen-image-2.1-Q8_0.gguf` | 297 | **none: the file has zero KV metadata** | Q8_0 229, BF16 68 | identical names and shapes |
| `m9ter1a/Qwen-Image-2.1-Turbo-GGUF@a94df596` `…-turbo-Q4_K_M.gguf` (diffusers layout, the control) | 297 | `qwen_image` | Q4_K 187, Q6_K 31, Q5_K 7, **F32 67**, BF16 5 | **identical: 297/297, 0 shape mismatches** |
| `Comfy-Org/Qwen-Image-2.1@0217efd1` `qwen_image_2.1_bf16.safetensors` | 265 | — | BF16 265 | 233 common. Comfy fuses `img_mlp.gate_up` [24576, 4096] where the GGUFs split it into `gate_layer` + `proj` [12288, 4096] (×32) |

**What K1 changes in this doc:**

- **The §2c / §6 layout premise is wrong for this repository.** The derivative's sd.cpp-converted
  GGUFs already use diffusers names: `transformer_blocks.N.attn.to_q`, `img_mlp.gate_layer` /
  `proj` / `out`, `txt_in.*`, a shared `modulation.1`.
- **No file carries a `model.diffusion_model.` prefix.** m9ter1a's card describes "265 tensors,
  fused `gate_up`". That matches Comfy-Org's bf16 safetensors in count and fusion, though that file
  has no prefix either. It does not match these GGUFs.
- **The gate split is already done.** diffusers 0.41.0's
  `convert_qwen_image21_transformer_checkpoint_to_diffusers` strips the prefix and splits
  `gate_up` gate-rows-first, so Comfy's file loads too. The GGUF names pass through it unchanged.
  The §6 row-split repack is moot.
- **The `base`-branch GGUF has no metadata at all.** Loaders that dispatch on the architecture
  string (city96's ComfyUI-GGUF; compare the derivative's discussion #24) cannot identify it.
  diffusers ignores the field.
- **The difference that matters is tensor types, not names.** sd.cpp stored 68 tensors as GGUF
  BF16: the 65 norm weights (`attn.norm_q` / `attn.norm_k` ×32, `txt_in.text_norm`) and 3
  Linears (`img_in`, `txt_in.in_layer`, `txt_in.out_layer`). m9ter1a stored the norms as F32.
- The GGUF tensor-info table ends at byte ~23.3 K, so a 64 KiB range is enough next time.

### K2: load + forward

**Harness**: `k2_load_forward.py`. It runs three checks:

1. A static key check: the K1 names go through diffusers' own mapping fn and are compared with a
   meta-device `QwenImage21Transformer2DModel` built from the pinned repo config.
2. The real
   `QwenImage21Transformer2DModel.from_single_file(path, quantization_config=GGUFQuantizationConfig(compute_dtype=…), config=<repo>, subfolder="transformer", revision=<sha>)`.
   `from_single_file` only *warns* on unexpected keys and leaves missing ones on the meta device,
   so the harness checks both explicitly.
3. Forwards on a 16×16 latent (256 image tokens plus 32 seeded random text tokens of width 4096):
   one prediction at σ = 1, then an 8-step Euler run from the same seeded noise.

**Non-noise bar, fixed before the first forward:**

- finite output;
- lag-1 neighbour correlation over the 16×16 grid ≥ 0.25 for both the σ = 1 clean-latent estimate
  (`x0_hat = noise − v`) and the Euler result, where pure noise scores ≈ 0;
- UC vs control cosine ≥ 0.5 on identical inputs.

**Compute dtype**: fp32. torch's arm64 CPU bf16 GEMM has no BLAS path. A bf16 run sampled inside
`gemm_transa_` / `bf16_dot_with_fp32_arith` and had produced no forward result after ~8 min past
load, so it was stopped. The GGUF weights stay
quantized and are dequantized per forward either way. The GPU lane would compute in bf16.

| Run | Static keys | Load | Forward | Verdict |
|---|---|---|---|---|
| Control: m9ter1a Turbo Q4_K_M, config `Qwen/Qwen-Image-2.1-Turbo@fc13ba19`, **stock** | 297/297, 0 missing / 0 unexpected / 0 shape | 140 s cold; 0 on meta; no warning; 67 F32 plain + 230 GGUFParameter | finite; noise input −0.011 → x0_hat 0.306 → Euler 0.633; cos(v, noise) 0.866; 74.6 s per forward; peak 5.7 GB | **PASS** (the harness and the stock GGUF path work) |
| UC Q8_0, config `Qwen/Qwen-Image-2.1@b3179ad3`, **stock** | 297/297, 0 / 0 / 0 | 30.5 s with a warm page cache (135 s in the preceding shim run); 0 on meta; no warning; 297 GGUFParameter (Q8_0 229, BF16 68) | **raises** `RuntimeError … (4096) … (8192) … dimension 2` in `QwenImage21ZeroCenterRMSNorm` (`txt_in.text_norm`) | **FAIL** |
| UC Q8_0, **+ shim** (65 non-Linear GGUFParameters dequantized) | same | same | finite; noise −0.011 → x0_hat 0.268 → Euler 0.763; cos(v, noise) 0.850; 102.3 s per forward; peak 9.0 GB | **PASS** |

**UC (shim) vs control on identical inputs** (`k2_compare.py`):

- Noise-orthogonal cosine: **0.985** for x0_hat (raw cosine also 0.985) and **0.973** for the Euler
  result (raw 0.964). Unrelated noise scores −0.002.
- x0_hat is itself nearly orthogonal to the input noise (−0.13 and −0.15), so this agreement comes
  from the model, not the shared seed.
- A swapped `gate_layer` / `proj`, a wrong dequant or a garbled block would break it. The control
  is a distilled Turbo checkpoint from a different converter, so it shares none of those failure
  modes.
- This proves the UC weights load and compute like a correct Qwen-Image 2.1 DiT. It says nothing
  about image quality or what the merged LoRA does: there was no text encoder or VAE in this test.

**Root cause** [V code, diffusers 0.41.0 and 0.42.0]:

- `models/model_loading_utils.py` `load_gguf_checkpoint` keeps only F32/F16 tensors as plain
  tensors (`is_gguf_quant = quant_type not in [F32, F16]`). Every BF16 tensor becomes a
  `GGUFParameter` holding raw bytes: a [4096] BF16 vector is stored as uint8 [8192].
- The GGUF quantizer swaps only `nn.Linear` for `GGUFLinear`, which dequantizes on the fly.
  `create_quantized_param` leaves any other module's `GGUFParameter` raw.
- `QwenImage21ZeroCenterRMSNorm.forward` then multiplies by `self.weight.float() + 1`, which is
  8192 byte values.
- Nothing here is device-specific, so the ROCm runtime pod would raise identically. The control
  loads because its norms are F32.

**The shim** (R3, `server-diffusers.py`, run right after `from_single_file`):

```python
from diffusers.quantizers.gguf.utils import GGUFLinear, GGUFParameter, dequantize_gguf_tensor

def dequantize_non_linear_gguf(model, dtype):
    """diffusers <= 0.42 only dequantizes GGUF weights inside nn.Linear; sd.cpp GGUFs store
    norm weights as BF16, which would otherwise reach RMSNorm as raw bytes."""
    for _, module in model.named_modules():
        if isinstance(module, GGUFLinear):
            continue
        for name, p in list(module.named_parameters(recurse=False)):
            if isinstance(p, GGUFParameter):
                module._parameters[name] = torch.nn.Parameter(
                    dequantize_gguf_tensor(p).to(dtype), requires_grad=False)
```

- Memory cost: 65 vectors of 128 or 4096 values, which is negligible.
- It is a no-op for files that already store norms as F32/F16.
- Add a CPU unit test with a toy module that holds a BF16 `GGUFParameter` norm.
- Alternatives: an upstream diffusers fix (treat BF16 like F16 in `load_gguf_checkpoint`, since
  torch has bf16), or an offline lossless repack of the 65 norms to F32. Both are outside this
  repository.

**What this changes elsewhere in the doc:**

- §6's failure-mode list: R6 (sd.cpp) is **not** needed for this derivative. The row-split repack
  and the offline LoRA merge are moot.
- R6 stays in the backlog for its other reasons: the 980 Ti / VII route and runtime LoRA toggling.
- R3 gains the shim (added to its row in §4).
- The §5 control arm can stay the derivative's `base`-branch Q8_0: same names and types as the UC
  file, so the same shim covers both.

**Still unproven** (needs R1–R3 and the §5 window):

- bf16 numerics and dequant speed on gfx1100 under ROCm;
- end-to-end image quality with the Qwen3-VL text encoder and the VAE;
- what the UC delta does relative to `base`;
- the cold-swap budget.

The CPU forward times (75–102 s for 288 tokens, fp32) say nothing about GPU speed.

**Reproduce**: the harness, `run.sh` and result files are in
`.loom/killtest-qwen-image21-gguf-2026-10-24/`.

```bash
cd .loom/killtest-qwen-image21-gguf-2026-10-24
WORK=/path/with/25GB/free ./run.sh
```

`run.sh` does the following, in order:

1. Builds the venv (`uv venv --python 3.12`, then
   `uv pip install "diffusers==0.41.0" "transformers>=5.17,<5.18" torch gguf accelerate safetensors huggingface_hub numpy`).
2. Pulls the four header ranges with `curl -r 0-16777215`. For Comfy's file it pulls the 8-byte
   length first, then the JSON header.
3. Runs `k1_headers.py` and `k1_diff.py`.
4. Downloads the two GGUFs at the pinned commits and checks them with
   `shasum -a 256 -c` (UC `cde456c7…affa8`, control `d56b6a27…1fa0a`).
5. Runs `k2_load_forward.py` three times (control stock, UC stock, UC `shim`), then
   `k2_compare.py`.

Results from this run are in `results/`: `k1-headers.txt`, `k1-diff.txt`, the three K2 JSONs and
`k2-compare-uc-shim-vs-control.txt`. The model files were deleted afterwards.

## 7. Sources

**Candidate (Hugging Face):**

- Model card: https://huggingface.co/abenzerps/Qwen-Image-2.1-Uncensored-Quantized
- API metadata (createdAt, sha, GGUF architecture and params, downloads):
  https://huggingface.co/api/models/abenzerps/Qwen-Image-2.1-Uncensored-Quantized
- File tree and sizes:
  https://huggingface.co/api/models/abenzerps/Qwen-Image-2.1-Uncensored-Quantized/tree/main?recursive=true
- SHA256SUMS:
  https://huggingface.co/abenzerps/Qwen-Image-2.1-Uncensored-Quantized/raw/main/SHA256SUMS
- Discussions: https://huggingface.co/abenzerps/Qwen-Image-2.1-Uncensored-Quantized/discussions
  (#49 LoRA: …/discussions/49; #24 arch error: …/discussions/24; #14 samples: …/discussions/14)
- Author: https://huggingface.co/abenzerps

**Base model, Turbo and repackages (Hugging Face):**

- Base card: https://huggingface.co/Qwen/Qwen-Image-2.1
- Base API: https://huggingface.co/api/models/Qwen/Qwen-Image-2.1
- Configs: https://huggingface.co/Qwen/Qwen-Image-2.1/raw/main/model_index.json ,
  …/raw/main/transformer/config.json , …/raw/main/text_encoder/config.json
- Licence: https://huggingface.co/Qwen/Qwen-Image-2.1/raw/main/LICENSE
- Turbo: https://huggingface.co/api/models/Qwen/Qwen-Image-2.1-Turbo
- Comfy-Org repack (files and LFS oids):
  https://huggingface.co/api/models/Comfy-Org/Qwen-Image-2.1/tree/main?recursive=true ,
  …/tree/main/text_encoders , …/tree/main/vae
- Unsloth GGUF sizes: https://huggingface.co/api/models/unsloth/Qwen-Image-2.1-GGUF/tree/main
- Layout claim and diffusers-layout Turbo GGUF: https://huggingface.co/m9ter1a/Qwen-Image-2.1-Turbo-GGUF

**diffusers:**

- QwenImage21 docs (v0.41.0): https://huggingface.co/docs/diffusers/v0.41.0/en/api/pipelines/qwenimage21
- QwenImage21 docs (main): https://huggingface.co/docs/diffusers/main/en/api/pipelines/qwenimage21
- GGUF: https://huggingface.co/docs/diffusers/main/en/quantization/gguf
- Qwen-Image v1 pipelines and `true_cfg_scale`:
  https://huggingface.co/docs/diffusers/main/en/api/pipelines/qwenimage
- LoRA loaders: https://huggingface.co/docs/diffusers/main/en/api/loaders/lora
- 0.41.0 release (secondary): https://artrealmai.com/article/diffusers-0-41-qwen-image-2-1-ltx-2-5-dfr ,
  https://sourceforge.net/projects/diffusers.mirror/files/v0.41.0
- Releases (0.35.0 / 0.36.0 snippets): https://github.com/huggingface/diffusers/releases ,
  https://newreleases.io/project/pypi/diffusers/release/0.35.0
- 0.32.0 GGUF: https://newreleases.io/project/github/huggingface/diffusers/release/v0.32.0
- Qwen GGUF single-file issues: https://github.com/huggingface/diffusers/issues/12098 ,
  https://github.com/huggingface/diffusers/issues/12891

**Runtimes and release ecosystem:**

- QwenLM README (PR #14804, day-0 runtimes): https://github.com/QwenLM/Qwen-Image-2.1
- Runtime table, SGLang PR #39983:
  https://mer.vin/news/qwen-image-2-1-whats-new-in-alibabas-latest-open-weight-image-model
- stable-diffusion.cpp: https://github.com/leejet/stable-diffusion.cpp ;
  sd-server API: https://go.backend.ai/en/manual/acceleration/stable-diffusion-cpp ;
  compose example: https://www.alonsoruibal.com/running-qwen-image-2-1-locally-with-stable-diffusion-cpp ;
  sd-server discussion: https://github.com/leejet/stable-diffusion.cpp/discussions/1334
- Memory tiers, RTX 5090 NF4/fp8, RTX 3050 6 GB sd.cpp:
  https://openclawdc.com/blog/can-i-run-qwen-image-2-1-locally

**AMD / ROCm reports:**

- 7900 XTX int8 recipe and a summary of every AMD run (ComfyUI #16521, #16447, #15314, #15452;
  LightX2V PR #1549; sd.cpp #2015):
  https://smeltcore.com/recipes/qwen-image-2-1-on-rx-7900-xtx-int8-comfyui-on-rocm-7-2-4-resident-in-24-gb-output-check
- RX 7900 XT 20 GB Q4_K_M / int8 / Turbo timings: https://note.com/elbobaggins/n/n14e375b7df4d
- W7900D (gfx1100) fast path: https://github.com/zihaomu/Qwen-Image-2.1-Rocm
- DynamicVRAM corruption: https://github.com/Comfy-Org/ComfyUI/issues/16437
- gfx1100 noise / black-image report summary: https://localaimaster.com/blog/comfyui-amd-rocm-image-generation

**Repository (this worktree):**

- Backend: `backend/diffusers.go`, `backend/comfyui.go`, `backend/vllm_omni.go`,
  `backend/interface.go`, `backend/image.go`
- Server: `build/server-diffusers.py` (families L282–L372, loader L1270–L1839, warmup L1871–L1937,
  generation L2289–L2401), `build/server-diffusers-cuda.py`
- Dedicated images: `build/Dockerfile.diffusers-rocm`, `build/Dockerfile.diffusers-rocm-gfx906`,
  `build/Dockerfile.diffusers-cuda`, `build/requirements-diffusers-rocm.txt`
- Stacked images: `build/Dockerfile.runtime-serving` (diffusers RUN L113, vLLM L145, COPY L243),
  `build/Dockerfile.runtime`, `build/runtime.yaml`; CI `.gitlab-ci.yml` (`publish_diffusers_video_rocm_gfx1100`)
- Runtime path: `pkg/runtime/payload.go` (`DirectRuntimeLoadEligibility`,
  `applyGPUProfileRuntimeEnv`), `internal/runtime/manager.go` (`inferCommand`, `overlayEnvVars`),
  `controllers/model_controller.go` (runtime vs dedicated branch),
  `charts/flexinfer/templates/runtime-daemonset.yaml`, `deploy/system/values-k3s.yaml` (runtime
  profiles)
- Cache staging: `controllers/model_cache_download.go`, `controllers/model_cache_jobs.go`
- Profiles and manifests: `deploy/gpuprofiles/{gfx1100,gfx906,sm_52}.yaml`,
  `deploy/models/kustomization.yaml`, `deploy/models/{dreamshaper8-imagegen-gtx980ti,wan21-t2v-1p3b-gfx1100,wan21-vace-1p3b-i2v-gfx1100,stable-audio-open-gfx1100,qwen38-27b-xtx-warm-canary,qwen38-27b-autoround-workhorse,snac-decode-gfx1100}.yaml`
- Posture and safety: `docs/dev/daily-driver-promotion-checklist.md`,
  `.loom/plan-qwen35-9b-nsfw-rp-lora-2026-07-18.md`
- Context: `.loom/00-index.md` (rows `gfx1100-serving-2026-10-23`, XTX soaks),
  `.loom/10-research-gfx1100-next-feature-2026-10-23.md`,
  `.loom/10-research-flexinfer-utilization-loom-mills-2026-09-13.md` §6b (LTX-2.5, diffusers 0.40)
- Consumers: `services/imagegen-ui/README.md`, `services/imagegen-ui/server/app.py`,
  `platform/gitops/k3s/ai/imagegen-ui/{deployment,ingress}.yaml`,
  `platform/gitops/k3s/ai/litellm/virtual-keys.yaml`, `services/familyforge/AGENTS.md`
