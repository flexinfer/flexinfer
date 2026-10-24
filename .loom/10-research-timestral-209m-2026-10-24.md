# Research: Timestral-209M (Mistral time-series forecaster) on FlexInfer (2026-10-24)

**Question**: What is `mistralai/Timestral-209M`, is it worth a slice on this
fleet, on which hardware, and exactly how would we try it — under the owner's
rule that new backends stack into **one runtime image per GPU class** and are
demand-swapped in place inside the long-lived runtime pod, not as new
Deployments?

**Verdict (TL;DR)**: **Worth one small slice, gated on a cheap offline
kill-test — not a program.** Timestral-209M is a zero-shot *time-series
forecaster* (not an LLM, not audio), currently first on GIFT-Eval, TIME and
fev-bench with results the GIFT-Eval maintainers reproduced; it is 837 MB of
plain fp32 PyTorch with no custom ops and no new dependencies, so it drops into
the existing gfx906 and gfx1100 stacked runtime images as a thin snac-style
`timestral` backend (no base bump, ~0.1 MB image delta, estimated 5–10 s cold
swap). Its only realistic home today is the **Radeon VII runtime pod**
(`radeonvii-models`, ~2 GB next to the retrieval plane, which leaves 10.7 GB of
the card free right now): the XTX runtime pod is starved by the whole-card warm
Mills lane, the 5930k has no runtime pod, the 980 Ti has no runtime pod and no
torch, and `gpu.vendor: cpu` is forced onto a dedicated Deployment by the
controller. Nothing in the fleet forecasts today except news-analyzer's
trailing-7-day mean, so build only after §5's backtest shows it beats that
baseline by ≥ 15 % MASE.

Branch: `claude/gfx1100-flexinfer-support-904425`. No cluster writes were made;
the only live reads are four read-only Prometheus queries on 2026-10-24 ~14:21Z.

Legend: **[V]** verified from a primary artifact (config/LICENSE/SDK source,
HF or GitHub API, repo file) · **[I]** inferred or estimated (arithmetic,
analogy, untested) · the model card itself is thin (no limitations, no training
data, tech report "coming soon"), so most architecture facts below come from
the SDK source and `config.json`, not the card.

---

## 1. What the model is and does

### 1.1 Identity

| Item | Value |
|---|---|
| Task | Univariate and multivariate **probabilistic time-series forecasting**, zero-shot; HF `pipeline_tag: time-series-forecasting`, `library_name: timestral` [V] |
| Family | 5M / 28M / 123M / 209M, same architecture and recipe; fp32 weights 21 MB / 111 MB / 490 MB / 837 MB [V] |
| Released | 2026-10-15 (blog, HF and GitHub same day) [V] |
| HF revision | `9874d252ab0aae16f7f7c999f98f98d6ad7d8365`; files: `config.json` (1,130 B), `model.safetensors` (837,271,648 B, LFS sha256 `1456effb9927e3daa8c7234f10497e3424233869245a9c72b8ecebaf7ad2b7fa`), `LICENSE`, `README.md`, a PNG [V] |
| Parameters | **209.31 M** recomputed from `config.json` (transformer 201.76 M + patch embedding 2.61 M + quantile head 4.94 M), × 4 B = 837 MB, matching the file [V] |
| `config.json` | Not a transformers config: **no `model_type`**; it is the SDK's own `TimestralConfig` dataclass, which rejects unknown keys [V] |
| SDK | `timestral` 1.0.0, Apache-2.0, single commit `a534bdbd244d64f3d9d743f46c7bf6fc330ed4ff` ("Timestral release"), deps `torch>=2.4, numpy>=1.24, safetensors>=0.4, huggingface_hub>=0.23`, Python ≥ 3.10. **Not on PyPI** (404; issue #5). Mistral does not accept PRs [V] |
| Hosted | docs.mistral.ai lists "Timestral 1.0 209M" as Public Preview, no endpoint or pricing on the page; the blog points at Mistral Forge for fine-tuning and on-prem [V] |

### 1.2 Architecture (from `config.json` and `src/timestral/*.py`)

- **Patching**: 32 time steps per patch; each patch is embedded from
  `[values, missing-indicator]` (64 inputs) by a residual MLP 64 → 3072 → 768 [V].
- **Normalization**: causal per-patch mean/std (Welford running statistics,
  computed in **float64**), then an `asinh` squash; outputs are inverted with
  `sinh` from the last context patch's frame [V].
- **Stack**: 24 pre-norm blocks, d_model 768, 12 heads, SwiGLU MLP (hidden
  2048), RMSNorm, no biases. Schedule `time, time, time, time_variate` × 6, so
  every 4th block adds a **variate attention** across the rows of one series.
  Time attention uses partial RoPE (half of each head, θ = 10⁴); both
  attentions are sigmoid-gated `F.scaled_dot_product_attention` with a dense
  boolean mask [V].
- **Decoupled Horizon Forecasting (DHF)**: the sequence is `[context patches ;
  learned forecast-query tokens]`; the context is causal and never reads the
  horizon, queries read all valid context (split two-pass attention). Up to 32
  query patches (1,024 steps) per forward pass; longer horizons decode block by
  block, feeding each block's median back as context [V].
- **Head**: residual MLP 768 → 3072 → 672 = 32 steps × **21 quantiles**
  (0.01, 0.05, 0.10 … 0.95, 0.99), made monotone by a softplus cumsum around a
  free median [V].
- **Covariates**: rows carry a role (target / past-only / known-future);
  firewall masks stop covariates from reading targets and known-future rows from
  relaying a target's future. Categorical covariates are target-encoded per
  series in NumPy before the model sees them [V].
- **Inference-time defaults** ("clampsym"): `symmetric_averaging=True` runs a
  second pass on `-context` and averages the reflected quantiles (**doubles
  compute**); `clip_nonnegative=True` clamps series whose context is entirely
  non-negative [V].
- **Limits**: context 32,768 steps (1,024 patches; older steps dropped), horizon
  1,024 per pass, "Inference was validated in float32" (`loading.py`) [V].

### 1.3 Training data and license

- **Data** (blog): synthetic-first. Univariate generators (trend, seasonality,
  noise) plus a multivariate synthesizer with nonlinear cross-series
  dependencies; stage 1 pretrains on synthetic only at 8,192 context, stage 2
  post-trains on "a mix of real and synthetic data" at 32,768. Real datasets and
  cutoff dates are **undisclosed** (issue #3, unanswered). The GIFT-Eval PR
  declares "Test data leakage: No" and the fev PR `trained_on_datasets = []` [V].
- **Weights license**: "Modified MIT" (HF `license: other`). Attribution notice
  required, and **no rights at all if your company's (or employer's) global
  consolidated monthly revenue exceeded $20 M in the preceding month**, extended
  to derivatives and combined works [V, raw `LICENSE`]. The model card's
  summary line mentions only third-party IP; the revenue cap is in the file. The
  cap does not bind a homelab, but note the precedent: the 2026-10-23 census
  rejected LFM2.5-24B for a "revenue-capped licence" (§5 of
  `10-research-gfx1100-next-feature-2026-10-23.md`). **Owner decision.** The
  unrestricted alternative is Chronos-2 (Apache-2.0, §1.4).

### 1.4 Quality and competitors

Model card table (zero-shot, single checkpoint, no ensembling) [V]:

| Benchmark | Metric | **Timestral-209M** | t0-gamma | TimesFM-3 | Toto-2.0-2.5B | Chronos-2 |
|---|---|---|---|---|---|---|
| GIFT-Eval | MASE ↓ | **0.658** | 0.661 | 0.667 | 0.696 | 0.698 |
| GIFT-Eval | CRPS ↓ | **0.446** | 0.452 | 0.456 | 0.476 | 0.485 |
| TIME | MASE ↓ | **0.626** | 0.637 | 0.640 | 0.642 | 0.662 |
| TIME | CRPS ↓ | **0.522** | 0.534 | 0.536 | 0.539 | 0.556 |
| fev-bench | SQL skill ↑ | **50.39** | 49.42 | 48.99 | 44.42 | 47.28 |
| fev-bench | MASE skill ↑ | **39.49** | 37.99 | 37.82 | 32.54 | 35.50 |

Family: 123M is within noise of 209M (GIFT-Eval MASE 0.659 vs 0.658; fev SQL
50.18 vs 50.39), 28M is 0.678 / 48.42, 5M 0.705 / 46.19. The blog puts
CPU latency at ~10 ms (5M) and ~140 ms (209M) for one series of three weeks
of 15-minute history → one day ahead on an 8-thread server CPU, with 123M at
~¾ of 209M's latency and 28M ~5× faster [V].

Independent status: GIFT-Eval PR #234 merged 2026-10-16 and the maintainer
"reproduced identical results across all model sizes"; fev PR #202 (wrapper +
results) merged 2026-10-16 [V]. No technical report yet; the Papers-with-Code
"paper" is the blog post [V].

Competitors in this niche (sizes and licenses from the HF API) [V]:

| Model | Params | License | Note |
|---|---|---|---|
| Chronos-2 (Amazon, 2025-10) | 119.5 M | Apache-2.0 | 8,192 context, covariates, `chronos-forecasting` 2.3.2 (needs `transformers>=4.41,<6`, `accelerate`, `einops`, `pandas` — would also fit the stacked images [I]); the practical unrestricted alternative |
| TimesFM-3 (Google, 2026-08) | 330.7 M | **TimesFM Non-Commercial v1.0** | weights not cleared for production |
| Toto-2.0 (Datadog, 2026-05) | 4 M – 2.45 B | Apache-2.0 | top of GIFT-Eval only in the fine-tuned/ensemble category |
| t0-gamma | ? | ? | named on the card only; not verified |

Known failure modes [V unless marked]:

1. **Upper-quantile blow-ups** (issue #7, 2026-10-24): 209M with the default
   symmetric averaging plus a sign-fixed known covariate (a price) on low-volume
   non-negative series gives q99 in the thousands for ~2 units/day (M5 repro and
   synthetic repro). 28M is not affected; `symmetric_averaging=False` or
   `quantile_real_cap_k=10` removes it.
2. **Memory and time quadratic in rows** (issue #6 comment): variate attention
   runs over every packed row of one `forecast()` call (series × (1 +
   covariates)); an unchunked call tried to allocate a 62 GiB buffer. Chunk to
   16–64 series. CPU time grows faster than linear with covariates (635 series
   × 822 steps on an M3 Max: 7 s / 118 s / 340 s with 0 / 7 / 22 covariates).
3. **Apple MPS crashes** on the float64 statistics (issue #6); a community
   fork routes them through the CPU (2–2.5× over CPU). CUDA and ROCm support
   float64, so this does not apply to the fleet [I for ROCm; verified in §4].
4. A degenerate input raises `ValueError` ("forecast produced N NaN values") —
   a server must map it to 4xx [V].
5. Training data is undisclosed, so leakage on *public* datasets cannot be
   ruled out by us (issue #3) [V].

---

## 2. Runtime support matrix

| Runtime | State (2026-10-24) | ROCm |
|---|---|---|
| **`timestral` SDK** (official) | `TimestralForecaster.from_pretrained(repo_or_dir, device=...)` → `forecast(context, prediction_length, observation_mask=, past_covariates=, known_covariates=, categorical_*=, symmetric_averaging=, clip_nonnegative=)`. Context `(B,V,T)` / `(B,T)` / `(T,)`, NaN = missing; returns `Forecast.quantiles (B,V,H,21)`, `.median`, `.quantile(q)`. Batching = the leading B axis. In-process only: **no HTTP server, no streaming, no OpenAI-style API** [V] | Pure PyTorch, no custom ops, no Triton/flash-attn/xformers; device `"cuda"` works on ROCm builds [V source, I runtime] |
| transformers | **None.** No `model_type`; issue huggingface/transformers#49884 (open, 2026-10-22) asks to add TimesFM-3, Timestral and Chronos-2; no PR [V] | — |
| vLLM | **None.** 0 issues/PRs mention Timestral; vLLM has no time-series model class [V search, I scope] | — |
| llama.cpp / Ollama | **None.** 0 issues/PRs. Community GGUFs exist but target other runtimes: `mudler/timestral-cpp-gguf` for **timestral.cpp** (ggml port by the LocalAI team, created 2026-10-18, CPU only, "slower than the PyTorch reference", Q8_0 fails its quality bound, GPU "not yet") and `EvoAwaken-Workshop/Timestral-209M-gguf` for a Rust runtime. Neither loads in llama.cpp [V] | No |
| ONNX | Community only: `mrfakename/Timestral-209M-ONNX` (fp32 841 MB / fp16 422 MB, single series, no covariates), `amine40/Timestral-209M-ONNX` (fp16 weights, covariates), `onnx-community/Timestral-28M-ONNX` (fixed 512/32). **All export only the network**: the float64 normalization, refine frame, block decode and symmetric averaging stay in the caller. 0 issues in optimum-onnx / onnxruntime [V] | ORT ROCm/MIGraphX on gfx906/gfx1100 not evaluated; not recommended [I] |
| MLX / CoreML / LiteRT | Community ports exist (`*/timestral-mlx`, `*-CoreAI`, `*-LiteRT`) [V listing] | n/a |
| Benchmark harnesses | fev wrapper (`autogluon/fev` #202, merged) and GIFT-Eval notebook (#234, merged) both wrap the SDK [V] | — |

### 2.1 Hardware needs

- **dtype**: fp32 only (validated); weights 837 MB. bf16/fp16 untested
  upstream; quantization is pointless at this size [V/I].
- **Compute** (arithmetic from `config.json`, matmul + attention, symmetric
  averaging on) [I]:

  | Shape | Patches (ctx+h) | GFLOP / series |
  |---|---|---|
  | 3 weeks @ 15 min → 1 day (blog) | 63 + 3 | ~55 |
  | 2 weeks @ 5 min → 1 day | 126 + 9 | ~113 |
  | 32,768 → 1,024 (max) | 1,024 + 32 | ~1,030 |

  The blog's 140 ms implies ~390 GFLOP/s effective on 8 threads. Scaling by
  peak FMA throughput [I]: the 5930k node (actually a Xeon E5-2680 v4, 14c/28t
  Broadwell-EP, AVX2 — matrix row of 2026-05-14; node-exporter reports 28 CPUs)
  ~200 ms/series on 6 threads; the 7900xtx host (Ryzen 9 7900X3D, Zen 4, 24
  CPUs) ~90 ms; the Radeon VII host reports 28 CPUs (node-exporter instance
  matched by pod CIDR; CPU model unknown). A
  64-series batch at the blog shape is ~6–14 s on CPU vs ~0.5 s of math on the
  Radeon VII and ~0.15 s on a 7900 XTX.
- **What a GPU buys**: not single-series latency (a 66-token forward is
  launch-bound, ~20–50 ms on a GPU vs ~100–200 ms on these CPUs [I]) but batch
  throughput, covariate-heavy calls (issue #6: CPU time explodes with
  covariates) and **not stealing host CPU** from co-tenants (the 5930k and XTX
  hosts feed Python-dispatch-sensitive vLLM lanes). CPU serving is realistic
  for daily or hourly batch consumers of a few hundred series.
- **VRAM**: ~0.84 GB weights + ~0.3–0.4 GB HIP context + transient
  activations. The math-path attention scores are rows × 12 × S² × 4 B: 13 MiB
  at 64 rows × 66 patches, 817 MiB at 16 rows × 1,056 patches, 3.3 GiB at 64 ×
  1,056. With row chunking a **2,000 MB** estimate is safe [I].
- **ROCm blockers**: none found in the source. Points to verify on the card:
  float64 cumsum/cummax (Vega20 runs fp64 at ¼ rate, RDNA3 at 1/32 — both
  irrelevant at these tensor sizes), `F.scaled_dot_product_attention` with a
  boolean mask (gfx1100 may take the AOTriton mem-efficient kernel under
  `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1`; gfx906 has no AOTriton and takes
  the math fallback), `scatter_reduce_`, `torch.unique(return_inverse=True)`,
  `gather`, `unfold`. Upstream's `uv.lock` tested torch **2.14.1** / numpy 2.x
  / huggingface-hub 2.2.0; the fleet images run torch 2.9 / numpy < 2 — inside
  the declared ranges but not the tested combination [V lock, I risk]. None
  of the repo's seven issues and none of the searches in §7 report an AMD/ROCm
  run [V].

---

## 3. Fit with this operator

### 3.1 No existing backend serves it

`backend/` registers vllm, vllm-omni, llamacpp, ollama, mlc-llm, diffusers,
comfyui, snac, steam, sunshine (`backend/registry.go`). A backend implements
`Name/Aliases/Image/Port/Command/Args/Env/ReadinessProbe/LivenessProbe/
StartupProbe/StartupTimeout/NeedsVolume/SupportsGPUVendor/IsImageGeneration/
DefaultIdleTimeout` (`backend/interface.go`). None can host Timestral: vLLM and
the llama.cpp family have no architecture for it (§2), `server-diffusers.py` is
pipeline-specific, and `server-snac.py` is hard-wired to SNAC. **The template is
SNAC**: a ~70-line Go backend whose `Image()` returns `""` (runtime-only), plus
a ~120-line FastAPI server COPYed into every stacked image and launched by the
runtime manager (`internal/runtime/manager.go:1690` `inferCommand`). SNAC landed
as 9 files / 281 lines (df01b020e) + 4 Dockerfiles × 2 lines (97afadeb0), then
three days of placement fixes — the lessons are folded in below.

### 3.2 Placement: where a runtime-pod subprocess can actually live

| Node / image | Runtime pod? | Verdict for Timestral |
|---|---|---|
| cblevins-7900xtx, `gfx1100` DaemonSet on `runtime@cfefae18` (gfx1100-serving persona, multi-model on) | yes | **No room.** `7900xtx-textgen` holds `qwen38-27b-xtx-warm-canary` (dedicated, `vramEstimateMB: 23200`, minReplicas 1, priority 470) at 0.96 GPU utilization: the election budget (23,200 + 2,000 > 24,576) parks a Timestral member, and the manager's real-VRAM admission (`canAdmit`, `manager.go:418`) would refuse it anyway. The GPUProfile comment already says the runtime pod "hosts nothing until voice returns to this card". Bundle the backend here anyway (free) so the profile stays uniform |
| cblevins-5930k, `gfx1100-5930k` DaemonSet | **desired = 0**; the workhorse is a dedicated pod | No runtime pod. (A GPU kill-test is running there; nothing in this plan touches the node) |
| cblevins-radeonvii, `gfx906` DaemonSet on `runtime@90648c35` (multi-model on, `FLEXINFER_RUNTIME_MULTI_MODEL=true`, request drain on) | yes | **Home.** Warm `radeonvii-models` members by estimate: bge 1,500 + reranker 700 + gte-qwen2 2,600 + orpheus 3,300 = 8,100 of 16,384 MB; live: 6.41 GB used / 10.75 GB free of 17.16 GB (agent metrics, 14:21Z). Timestral at 2,000 MB and priority 105 sits below every retrieval/voice member; a Humanizer wake (11,000, priority 130) evicts it with orpheus, and it reloads on demand |
| cblevins-gtx980ti, `cuda-maxwell` image | **none** (`deploy/gpuprofiles/sm_52.yaml`: every Model there is its own Deployment) | No. The `cuda-maxwell` profile ships no torch (vllm and diffusers off); adding torch for CUDA 11.8 is a multi-GB layer [I] for a node with no runtime pod |
| CPU anywhere | — | `gpu.vendor: cpu` is never runtime-eligible (`pkg/runtime/payload.go:125`, #62: written for the single-slot runtime) → dedicated Deployment, which the owner's rule excludes. Lifting this for `features.multiModel` profiles is a ~0.5–1 day controller change; keep it as the fallback if §4 step 2 fails |

### 3.3 Dependency coexistence in the stacked images

| Stacked image (as pinned today) | torch / numpy / hub / safetensors / Python | Timestral needs | Bump? |
|---|---|---|---|
| gfx1100 `gfx1100-serving` (`Dockerfile.runtime-serving`, base `rocm/vllm-dev` rocm7.2 / torch 2.9 / py3.12, 17.0 GB) and the broad `gfx1100` (`Dockerfile.runtime`) | torch 2.9, `numpy<2`, huggingface-hub (pulled with transformers 5.x), safetensors, FastAPI/uvicorn (already used by `server-snac.py`) | torch ≥ 2.4, numpy ≥ 1.24, hub ≥ 0.23, safetensors ≥ 0.4, py ≥ 3.10 | **None** |
| gfx906 (`Dockerfile.runtime-gfx906`, base `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3`, ~37 GB) | torch 2.9 (so py ≥ 3.10; exact version not in the repo), `numpy>=1.26,<2`, `huggingface_hub`, `safetensors` | same | **None** |
| cuda-maxwell (CUDA 11.8 runtime) | no torch | torch | Not proposed (§3.2) |

Timestral does **not import transformers**, so the `transformers_constraint`,
the Gemma4 source pins and vLLM's pins are untouched. Install with `--no-deps`
so pip cannot move torch or numpy. Two import-time facts to confirm inside the
exact digests (§4 step 2): `from huggingface_hub.utils import GatedRepoError,
HFValidationError, RepositoryNotFoundError` under the image's hub version, and
the SDK on numpy 1.26 (its NumPy use is limited to `asarray`, `unique`,
`searchsorted`, `bincount`).

**Image delta**: the SDK is 78,767 bytes of source across 9 files (GitHub tree
API) plus bytecode, and the server ~8 KB: **~0.1–0.3 MB per image, no new
wheels** [V sizes, I bytecode]. Weights stay out of the image: 837 MB once per
node in the Local cache.

### 3.4 The thin backend — files that change

| File | Change |
|---|---|
| `backend/timestral.go` (new, ~80 LOC) + `backend/timestral_test.go` | Copy of `snac.go`: `Image()` → `""`; `NeedsVolume()` → **true** (SNAC's `false` + a Local cache deadlocked eligibility, 27d4fb193; Timestral wants the Local cache); `Env`: `TIMESTRAL_MODEL_PATH=spec.ModelPath`, `HF_HUB_OFFLINE=1`, `PORT`, `TIMESTRAL_DEVICE`, `TIMESTRAL_THREADS`, `TIMESTRAL_MAX_ROWS`, `TIMESTRAL_ATTN_BUDGET_MB`, `TIMESTRAL_MAX_CONTEXT`, `TIMESTRAL_KNOWN_COV_SYMMETRIC`, plus `ROCmEnvVars(spec.GPUArch)` and `DeviceIsolationEnvVars(spec)`; `/health` readiness + startup probes; startup timeout 5 min |
| `backend/registry.go`, `backend/constants.go` | `NameTimestral = "timestral"`, `PortTimestral` (8000; the manager re-allocates ports in multi-model mode) |
| `internal/runtime/manager.go` (+ test) | `inferCommand`: `case backend.NameTimestral: return "python", []string{"/opt/flexinfer/server-timestral.py"}` |
| `build/server-timestral.py` (new, ~200 LOC) | §3.5 |
| `build/Dockerfile.runtime-serving`, `build/Dockerfile.runtime`, `build/Dockerfile.runtime-gfx906` (+ `Dockerfile.unified-gfx906` while it is still built) | the 3-line layer in §3.5, next to the snac lines (242 / 566 / 316 / 204) |
| `.gitlab/ci/runtime-publish.yml` (lines 202, 385) and `.gitlab-ci.yml` change lists | add `build/server-timestral.py` wherever `build/server-snac.py` is listed, so an edit rebuilds the images (check `scripts/check-runtime-patch-contracts.py` for name assertions) |
| `deploy/gpuprofiles/gfx906.yaml`, `deploy/gpuprofiles/gfx1100.yaml` | `backends.timestral.support: full` and `runtime.bundledBackends += timestral` — **only after** the image carrying the server is on the DaemonSet, or `RuntimeProfileBundlesBackend` admits a load the pod cannot run |
| `deploy/system/values-k3s.yaml` (`runtime.profiles` gfx906 line 677, gfx1100 576/637), GPUProfile `runtime.image`, prewarm lists | the usual #74 repin to the new digest |
| `api/v1alpha2/model_types.go` | doc comment "Supported:" list (cosmetic; `spec.backend` has no enum, so no CRD regen) |
| `docs/agents/gpu-compatibility.md`, `.loom/60-validation-matrix.md` | table row, evidence row |

No proxy change: the generic handler routes any JSON POST by its `model` field
(`internal/proxy/resolver.go:392`), and `flexinfer.ai/serve-paths: /v1/forecast`
keeps chat probes from waking the lane (`serving_policy.go`).

**Effort** [I]: code + tests 1 day; one CI build per stacked image, prewarm,
repin and DaemonSet roll 0.5–1 attended day (the 2026-10-23 gfx906 repin
!1118/!1119 is the template: retrieval plane back in 56–72 s); canary and probes
0.5 day. **≈ 2–2.5 days in flexinfer**, plus ~0.5 day in the consumer. The
DaemonSet roll restarts the whole VII runtime pod once, so **ride the next
planned gfx906 repin** instead of rolling for Timestral alone; after that,
every swap is an in-place subprocess start.

### 3.5 Server sketch (`build/server-timestral.py`)

API:

```text
GET  /health        {"status":"ok|loading","device":"cuda:0|cpu","probe":"…","model":"/models/…","sdk":"a534bdb"}
POST /v1/forecast   {
  "model": "timestral-209m-radeonvii-canary",        # proxy routing key
  "context": [[…], …],                                # (B,T) or (B,V,T) floats, null = missing
  "prediction_length": 96,
  "quantile_levels": [0.1, 0.5, 0.9],                # subset of the 21; default all
  "past_covariates": …, "known_covariates": …,       # optional, SDK shapes
  "categorical_past_covariates": …, "categorical_known_covariates": …,
  "symmetric_averaging": true, "clip_nonnegative": true
}
→ 200 {"object":"forecast","model":…,"prediction_length":96,"quantile_levels":[…],
        "forecasts":[{"median":[…],"quantiles":{"0.1":[…],"0.9":[…]}}, …],
        "usage":{"series":B,"rows":R,"context_steps":T,"chunks":k,"elapsed_ms":…,"device":"cuda:0"}}
→ 422 on shape errors or the SDK's NaN ValueError; 503 while loading
```

Behaviour worth encoding (each traces to §1.4 or §2.1):

- Device probe copied from `server-snac.py` (`torch.ones(1).cuda()+1` check;
  a failed probe falls back to CPU with a WARNING, never silently).
- `torch.set_num_threads(TIMESTRAL_THREADS)` (default 4) so a CPU path or the
  NumPy target encoding never starves co-tenants.
- Chunk rows so `rows × 12 × S² × 4 B ≤ TIMESTRAL_ATTN_BUDGET_MB` (default
  768) and `rows ≤ TIMESTRAL_MAX_ROWS` (default 32), keeping a series' variates
  and covariates in one chunk (issue #6).
- Default `symmetric_averaging=false` **only when known covariates are
  present** (issue #7), overridable per request.
- One lock around the forward (sync FastAPI handlers run in a thread pool);
  `torch.inference_mode()`.
- A warm-up forecast before `/health` turns `ok`, so the first request does not
  pay rocBLAS/kernel loading.
- Load with `TimestralForecaster.from_pretrained(TIMESTRAL_MODEL_PATH,
  device=…, local_files_only=True)`. Optional later: build the module on the
  meta device and `load_state_dict(assign=True)` to skip the ~0.5–1.5 s random
  init of 209 M parameters [I].

Dockerfile layer (each stacked Dockerfile, beside the snac lines; the tarball
URL needs no `git` in the image):

```dockerfile
ARG TIMESTRAL_REF=a534bdbd244d64f3d9d743f46c7bf6fc330ed4ff
RUN pip install --no-cache-dir --no-deps \
      "https://github.com/mistralai/timestral/archive/${TIMESTRAL_REF}.tar.gz" && \
    python3 -c "import timestral, torch, numpy, huggingface_hub; print('timestral', timestral.__version__, 'torch', torch.__version__, 'numpy', numpy.__version__, 'hub', huggingface_hub.__version__)"
COPY build/server-timestral.py /opt/flexinfer/server-timestral.py
```

### 3.6 Cold-swap budget (the latency the owner cares about)

| Step | Estimate [I] | Note |
|---|---|---|
| subprocess spawn + `import torch` (ROCm) + FastAPI | 2–4 s | dominant term |
| `TimestralModel(config)` random init, 209 M fp32 | 0.5–1.5 s | avoidable (meta device) |
| `load_file` 837 MB from the NVMe-backed `/var/lib/flexinfer/models` | 0.1–0.6 s | page cache vs cold |
| `load_state_dict` + `.to("cuda")` over the VII's PCIe 3.0 x16 | ~0.2–0.3 s | |
| HIP context + warm-up forward | 1–3 s | |
| **load → `/health ok`** | **≈ 5–10 s** (CPU path ≈ 3–6 s) | SNAC reference: Ready in ≤ 16 s on 2026-10-23, including a hub fetch |

The 837 MB read is not the bottleneck; Python + HIP start-up is. Measured by
`flexinfer_runtime_model_swap_duration_seconds{model,backend}`
(`internal/runtime/metrics.go:72`, load request → Ready, including any
incumbent unload). Levers if it matters: Timestral-123M (490 MB, same score),
meta-device construction, or warm-pinning (`minReplicas: 1`, ~2 GB VRAM +
~2–3 GB host RAM on the VII, which has room).

---

## 4. Try-it plan (smallest experiments first)

**Step 0 — the kill-test in §5** (offline, CPU, ~2–3 h). Stop here if it fails.

**Step 1 — local CPU run with the SDK** (owner's Mac, CPU; MPS crashes, §1.4).

```bash
uv venv -p 3.12 ~/.venvs/timestral && source ~/.venvs/timestral/bin/activate
uv pip install "https://github.com/mistralai/timestral/archive/a534bdbd244d64f3d9d743f46c7bf6fc330ed4ff.tar.gz" pandas
# exactly the two files the SDK loader reads (allow_patterns), plus LICENSE for attribution
hf download mistralai/Timestral-209M config.json model.safetensors LICENSE \
  --revision 9874d252ab0aae16f7f7c999f98f98d6ad7d8365 --local-dir ~/models/timestral-209m
shasum -a 256 ~/models/timestral-209m/model.safetensors   # 1456effb…ad2b7fa
# comparators for §5: Timestral-123M @ 5a147287addda85f6acd8da3fb8858fe9af7e8f7,
#                     Timestral-28M  @ 49b26409ac87b5935539a5d8ffe7f397b720db47
```

Probe: nine deterministic fixtures (sine, random walk, trend + weekly season,
intermittent Poisson(2), NaN gaps, 3 joint variates, 2 past covariates, 1 known
covariate, an 8,192-step context), each forecast twice at `device="cpu"`; write
`reference.json` (all 21 quantiles), wall times at the blog shape (B = 1, T =
2,016, H = 96) and for B = 64. **Pass**: finite, monotone along the quantile
axis, bit-identical across the two runs, and the sine fixture's median MAE
≤ 5 % of the sine amplitude. This file is the parity reference for steps 2
and 4.

**Step 2 — zero-displacement gfx906 parity probe (throwaway, no operator code).**
Faster than any Model-CR path because nothing has to be built: a Job in the
production gfx906 runtime digest, shaped like
`deploy/debug/embeddinggemma-2-gpu-qualification.yaml` (privileged, `/dev/kfd`
+ `/dev/dri` hostPath, no `amd.com/gpu` claim, co-resident with the retrieval
plane). Applied by the operator in a window when Humanizer is idle:

```yaml
apiVersion: batch/v1
kind: Job
metadata: {name: timestral-gfx906-parity-probe, namespace: flexinfer-system}
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 900
  ttlSecondsAfterFinished: 86400
  template:
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      hostNetwork: true          # as the embeddinggemma-2 qualification Job (HF + GitHub egress)
      dnsPolicy: Default
      nodeSelector: {kubernetes.io/hostname: cblevins-radeonvii}
      containers:
      - name: probe
        image: registry.harbor.lan/flexinfer/runtime@sha256:90648c350783694d4858e88a31aefcdf682d171571801905c3ddd64c64c530d7
        command: [/usr/local/bin/runtime-entrypoint.sh, bash, /scripts/run.sh]
        securityContext: {privileged: true}
        env:
        - {name: HSA_ENABLE_SDMA, value: "0"}
        - {name: HSA_USE_SVM, value: "0"}
        - {name: ROCR_VISIBLE_DEVICES, value: "0"}
        - {name: HF_HUB_DISABLE_XET, value: "1"}   # gfx906 profile env
        - {name: TIMESTRAL_REF, value: a534bdbd244d64f3d9d743f46c7bf6fc330ed4ff}
        - {name: HF_REVISION, value: 9874d252ab0aae16f7f7c999f98f98d6ad7d8365}
        resources:
          requests: {cpu: "1", memory: 4Gi}
          limits: {cpu: "4", memory: 8Gi}
        volumeMounts:
        - {name: kfd, mountPath: /dev/kfd}
        - {name: dri, mountPath: /dev/dri}
        - {name: script, mountPath: /scripts}
        - {name: work, mountPath: /work}
      volumes:
      - {name: kfd, hostPath: {path: /dev/kfd, type: CharDevice}}
      - {name: dri, hostPath: {path: /dev/dri, type: Directory}}
      - {name: script, configMap: {name: timestral-gfx906-parity-probe}}   # run.sh, probe.py, reference.json
      - {name: work, emptyDir: {sizeLimit: 4Gi}}
```

`run.sh`: `pip install --no-deps --target /work/site` the pinned tarball (or
ship the nine SDK files in the ConfigMap for a hermetic run), fetch the two HF
files at `HF_REVISION` into `/work`, print `python3 --version`, torch, numpy and
hub versions, then run the step-1 fixtures on `cuda` and on `cpu` in the same
pod. **Pass**: `import timestral` succeeds in the unchanged image; every
fixture's GPU result within `1e-3 × max|reference|` of the step-1 CPU reference
(the ONNX ports report ≤ 3e-4); load → first forecast ≤ 15 s; steady
`torch.cuda.max_memory_allocated()` ≤ 1.6 GB at the default chunking (≤ 2,000 MB
by `rocm-smi`); blog-shape p50 ≤ 100 ms and 64-series ≤ 2 s; zero amdgpu errors
in the kernel log; bge / reranker / gte answers unchanged. **Fail** (crash or
parity miss on gfx906) → the home moves to CPU, which needs the
`DirectRuntimeLoadEligibility` change from §3.2 before step 4.

**Step 3 — ship the thin backend** (§3.4) on the next planned gfx906 repin
(gfx1100 rides along).

**Step 4 — runtime-managed debug canary.** Draft for
`deploy/debug/timestral-209m-radeonvii-canary.yaml` (not written to `deploy/`
yet; needs step 3):

```yaml
# Timestral-209M zero-shot forecasting canary on the Radeon VII, served IN the
# gfx906 runtime pod by the bundled server-timestral.py (backend `timestral`):
# no Deployment, no image of its own. Research:
# .loom/10-research-timestral-209m-2026-10-24.md. Operator applies by hand
# after the gfx906 runtime digest that bundles the backend is on the DaemonSet
# AND deploy/gpuprofiles/gfx906.yaml lists `timestral` in bundledBackends.
#
# priority 105: below every warm retrieval/voice member (bge 120, reranker 115,
# gte 114, orpheus 113, openjev 112); a Humanizer wake evicts it.
# vramEstimateMB 2000: 837 MB fp32 weights + HIP context + the 768 MiB
# attention budget. Local cache marker = sha256(spec.source) only: a later
# hfRevision bump needs the node marker removed first.
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: timestral-209m-radeonvii-canary
  namespace: flexinfer-system
  annotations:
    flexinfer.ai/serve-paths: /v1/forecast
    flexinfer.ai/purpose: "Timestral-209M forecasting canary (gfx906 runtime pod)"
spec:
  backend: timestral
  source: HF://mistralai/Timestral-209M
  cache:
    strategy: Local
    hostPath: /var/lib/flexinfer/models
    size: 2Gi
  gpu:
    vendor: amd
    count: 1
    shared: radeonvii-models
    priority: 105
    vramEstimateMB: 2000
  serverless:
    enabled: true
    minReplicas: 0
    idleTimeout: 30m
    coldStartTimeout: 5m
  config:
    hfRevision: 9874d252ab0aae16f7f7c999f98f98d6ad7d8365
    device: auto              # cuda if the in-process probe passes, else cpu (logged)
    threads: 4
    maxRowsPerCall: 32        # issue #6: variate attention is quadratic in rows
    attnBudgetMB: 768
    maxContext: 32768
    knownCovSymmetric: false  # issue #7: no symmetric averaging with known covariates by default
    rocrVisibleDevices: "0"
  nodeSelector:
    kubernetes.io/hostname: cblevins-radeonvii
  resources:
    requests: {cpu: "1", memory: 3Gi}
    limits: {cpu: "4", memory: 6Gi}
  # Advertised so the proxy's parked-model gate cold-starts it by name
  # (snac / stable-audio convention for cold on-demand lanes).
  litellm:
    enabled: true
    servedModelName: timestral-209m-radeonvii-canary
```

Probe (a new `scripts/verify-timestral.py`, through the proxy): the nine
fixtures against `reference.json`, three repeats, single and 64-series timing,
then the §5 backtest replayed through `/v1/forecast`. **Pass**: Model Ready
**with no Deployment created** (runtime-managed); `/health` reports
`cuda:0`; first `flexinfer_runtime_model_swap_duration_seconds` ≤ 15 s with the
cache staged and first proxied request from cold ≤ 30 s; parity and latency bars
as in step 2; VRAM delta ≤ 2,000 MB on `flexinfer_gpu_vram_used_bytes`; runtime
pod RSS +≤ 3 GiB; retrieval and voice probes unchanged and no evictions caused by
the canary; after 30 min idle the subprocess exits and the VRAM returns; the
replayed backtest matches step 0 within 1e-3. **Rollback**: delete the Model;
the backend stays dormant in the image.

**Step 5 — consumer and promotion**: news-analyzer calls `/v1/forecast` from
`_compute_forecasts` (§6); promote to `deploy/models/` with the smallest model
size that passed §5.

---

## 5. Riskiest assumption and kill-test

**Assumption**: on the fleet's own series, Timestral zero-shot beats what the
fleet does today by a margin a consumer would act on — **≥ 15 % lower
geometric-mean MASE than news-analyzer's trailing-7-day mean on its daily
tag/topic counts**, with sane upper quantiles. The research above is
positive-only (public-benchmark wins, a clean runtime fit); none of it shows
value on homelab series, which are short, intermittent, non-negative counts —
the shape issue #7 broke on. If this is false, every slice in §4 is wasted; if
the gfx906 run (step 2) is what fails instead, the work survives on the CPU
path. That makes value, not ROCm, the load-bearing risk.

**Kill-test** (no cluster writes; CPU only):

1. Operator exports, read-only: news-analyzer `daily_metrics (kind, key,
   metric_date, count)` for every tag/topic key with ≥ 56 days of history.
   Secondary, informational: `sum by (model)
   (increase(flexinfer_proxy_requests_total[15m]))` at a 15 m step. Prometheus
   keeps only **8 days** (TSDB lowest timestamp 2026-10-16T18:00Z), so the proxy
   series test daily seasonality only — and a predictive-prewarm consumer would
   need its own longer history.
2. Rolling origins every 7 days over the last 8 weeks, horizon 7 days, context
   = all history before the origin with missing days as 0 (that is what the
   current SQL assumes). Baselines: the current trailing-7-day mean, weekly
   seasonal-naive. Candidates: Timestral-209M with SDK defaults, 209M with
   `symmetric_averaging=False`, 123M, 28M.
3. Score MASE (scaled by in-sample weekly seasonal-naive), WQL over the 21
   quantiles, q10–q90 empirical coverage, and blow-ups (q99 > 10 × the
   context max).
4. **Pass**: Timestral/trailing-mean GM-MASE ratio ≤ 0.85 **and** WQL ratio ≤
   0.85, coverage 70–90 %, zero blow-ups, ≤ 0.5 s per series on CPU. Choose the
   smallest size within 2 % of 209M's MASE as the served model. **Kill**: ratio
   > 0.95 → park Timestral (this doc stays as the record; build nothing). 0.85–0.95 →
   build only if the consumer wants the uncertainty bands rather than point
   accuracy.

Core loop of the backtest:

```python
import numpy as np, pandas as pd
from timestral import TimestralForecaster

def mase(y, f, hist, m=7):
    scale = np.mean(np.abs(hist[m:] - hist[:-m])) or 1.0
    return np.mean(np.abs(y - f)) / scale

df = pd.read_csv("daily_metrics.csv", parse_dates=["metric_date"])
wide = (df.pivot_table(index="metric_date", columns=["kind", "key"], values="count", aggfunc="sum")
          .asfreq("D").fillna(0.0))
model = TimestralForecaster.from_pretrained("~/models/timestral-209m", device="cpu")
rows = []
for origin in wide.index[-56::7]:
    hist, fut = wide.loc[:origin - pd.Timedelta(days=1)], wide.loc[origin:origin + pd.Timedelta(days=6)]
    keep = [c for c in wide.columns if (hist[c] > 0).sum() >= 14 and len(fut) == 7]
    ctx = hist[keep].to_numpy().T      # (B, T); chunk B to <= 64 if there are many keys (issue #6)
    q = model.forecast(ctx, prediction_length=7).quantiles[:, 0].numpy()   # (B, 7, 21)
    for i, c in enumerate(keep):
        h, y = hist[c].to_numpy(), fut[c].to_numpy()
        rows.append({"key": c, "origin": origin,
                     "mase_ts": mase(y, q[i, :, 10], h),
                     "mase_mean7": mase(y, np.full(7, h[-7:].mean()), h),
                     "blowup": q[i, :, -1].max() > 10 * max(h.max(), 1)})
r = pd.DataFrame(rows)
print("GM-MASE ratio", np.exp(np.log(r.mase_ts + 1e-9).mean() - np.log(r.mase_mean7 + 1e-9).mean()),
      "blow-ups", int(r.blowup.sum()))
```

---

## 6. Where it would sit in the fleet

Consumers among the `AGENTS.md` portfolio tiers, most concrete first:

| Repo (tier) | Use | Status today |
|---|---|---|
| `services/news-analyzer` (1) | Replace `_compute_forecasts` (`summarizer/analysis.py:170`), whose docstring says "Very simple baseline forecast: next h days = trailing 7-day mean", for the top-5 tags and topics, stored as `trend_forecasts.forecast` JSONB `{date, yhat}` and served at `/analytics/forecast`. Timestral adds the median plus q10/q90 bands; one call per daily cron | **The only forecasting code in the fleet**, and a drop-in point. The §5 kill-test data |
| `services/flexinfer` (1) | Predictive prewarm: forecast per-model demand (`flexinfer_proxy_requests_total`, `flexinfer_proxy_scale_ups_total`) and raise `minReplicas` ahead of it — aimed straight at the owner's cold-start latency goal | Needs a new controller feature (own spec) and more history than Prometheus's 8 days |
| `services/flexdeck` (1) and `platform/gitops` monitoring (1) | GPU/K8s capacity forecasts with bands (VRAM, temperature, power, root-disk fill on the Radeon VII and Harbor) | No forecasting or `predict_linear` rules under `platform/gitops/k3s` today |
| `services/smarthome` (1) | Climate and energy forecasts from the Home Assistant recorder history (Midea AC, sensors): the blog's lead use case | Not on the roadmap |
| `services/loom-flightdeck` (1) | Agent token-spend and latency trends | Speculative |
| `services/procmodel` (1) | Not a fit: its `Forecast` (`generalize/forecast.go`) is a Monte Carlo over fitted lognormal activity durations, not a time series | — |

**Displacement**: no inference lane is displaced; no forecasting model exists
in the fleet. On the Radeon VII it takes ~2 GB of the headroom next to the
retrieval plane and yields to every retrieval, voice and Humanizer member by
priority. The only code it replaces is news-analyzer's trailing-mean heuristic.
If the owner holds a strict no-revenue-cap license policy, the same thin-backend
design serves Chronos-2 (Apache-2.0, 119.5 M) instead.

---

## 7. Sources

- Model card and files: https://huggingface.co/mistralai/Timestral-209M
  (README raw, `config.json` raw, `LICENSE` raw), HF API
  https://huggingface.co/api/models/mistralai/Timestral-209M and
  `/tree/main` (revision `9874d252…`, file sizes, LFS sha256); family
  revisions via the same API for Timestral-123M / 28M / 5M; collection
  https://huggingface.co/collections/mistralai/timestral
- SDK: https://github.com/mistralai/timestral at `a534bdbd244d64f3d9d743f46c7bf6fc330ed4ff`
  (`README.md`, `pyproject.toml`, `uv.lock`, `src/timestral/{config,loading,
  layers,model,masks,scaler,forecaster,covariate_encoding}.py`,
  `tests/test_forecaster.py`), tree sizes via
  https://api.github.com/repos/mistralai/timestral/git/trees/main?recursive=1;
  PyPI https://pypi.org/pypi/timestral/json (404)
- Issues: https://github.com/mistralai/timestral/issues/1 … /7 (#3 training
  data, #4 MPS fork, #5 PyPI, #6 MPS float64 crash and row-quadratic memory,
  #7 q99 blow-ups)
- Announcement: https://mistral.ai/news/timestral (2026-10-15); docs
  https://docs.mistral.ai/models/timestral-1-0-209m.md; Papers with Code
  https://paperswithcode.co/paper/123626
- Benchmarks: https://github.com/SalesforceAIResearch/gift-eval/pull/234 (merged,
  reproduced), https://github.com/autogluon/fev/pull/202 (merged),
  https://huggingface.co/spaces/Salesforce/GIFT-Eval,
  https://huggingface.co/spaces/Real-TSF/TIME-leaderboard,
  https://huggingface.co/spaces/autogluon/fev-bench
- Upstream runtimes: https://github.com/huggingface/transformers/issues/49884;
  GitHub issue search for "timestral" in vllm-project/vllm, ggml-org/llama.cpp,
  huggingface/optimum-onnx, microsoft/onnxruntime (0 results, 2026-10-24)
- Community ports: https://huggingface.co/mrfakename/Timestral-209M-ONNX,
  https://huggingface.co/amine40/Timestral-209M-ONNX,
  https://huggingface.co/onnx-community/Timestral-28M-ONNX,
  https://huggingface.co/mudler/timestral-cpp-gguf,
  https://github.com/mudler/timestral.cpp,
  https://huggingface.co/EvoAwaken-Workshop/Timestral-209M-gguf
- Competitors: https://huggingface.co/amazon/chronos-2,
  https://arxiv.org/abs/2510.15821, https://pypi.org/project/chronos-forecasting/
  (2.3.2 requirements), https://huggingface.co/google/timesfm-3.0-pytorch,
  https://huggingface.co/Datadog/Toto-2.0-2.5B,
  https://www.datadoghq.com/blog/ai/toto-2/, https://arxiv.org/abs/2605.20119
- Fleet: `AGENTS.md`, `.loom/00-index.md`,
  `.loom/10-research-gfx1100-next-feature-2026-10-23.md` §1/§5/§7,
  `.loom/60-validation-matrix.md` (rows `gfx1100-serving-2026-10-23`,
  `gfx906-2026-09-13`, the 2026-05-14 host-CPU row), `backend/interface.go`,
  `backend/registry.go`, `backend/snac.go`, `backend/diffusers.go`,
  `build/server-snac.py`, `build/runtime.yaml`, `build/Dockerfile.runtime`,
  `build/Dockerfile.runtime-serving`, `build/Dockerfile.runtime-gfx906`,
  `build/Dockerfile.unified-gfx906`, `.gitlab/ci/runtime-publish.yml`,
  `internal/runtime/manager.go`, `internal/runtime/metrics.go`,
  `pkg/runtime/payload.go`, `controllers/model_controller.go`,
  `controllers/model_deployment.go`, `internal/proxy/{resolver,serving_policy,
  metrics}.go`, `deploy/gpuprofiles/{gfx1100,gfx906,sm_52}.yaml`,
  `deploy/system/values-k3s.yaml`, `deploy/models/{snac-decode-gfx1100,
  gte-qwen2-1p5b-cpu-5930k,qwen3-1.7b-tools-radeonvii,bge-reranker-radeonvii,
  embeddinggemma-2-radeonvii,qwen38-27b-xtx-warm-canary,kustomization}.yaml`,
  `deploy/debug/embeddinggemma-2-gpu-qualification.yaml`; commits df01b020e,
  97afadeb0, 27d4fb193
- Consumers: `services/news-analyzer/summarizer/analysis.py`,
  `services/news-analyzer/frontend/src/lib/api/endpoints.ts`,
  `services/procmodel/generalize/forecast.go`,
  `services/smarthome/k8s/base/home-assistant-bootstrap-config.yaml`
- Live, read-only (Prometheus, 2026-10-24 ~14:21Z): `flexinfer_gpu_vram_{used,free,total}_bytes{node="cblevins-radeonvii"}`,
  `count by (instance) (node_cpu_seconds_total{mode="idle"})`,
  `topk(15, sum by (model) (increase(flexinfer_proxy_requests_total[7d])))`,
  `prometheus_tsdb_lowest_timestamp_seconds`
- Local arithmetic: parameter, FLOP and memory counts from `config.json`
  (CPU-only Python, no model download)
