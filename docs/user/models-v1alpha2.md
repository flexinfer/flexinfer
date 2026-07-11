---
title: Models (v1alpha2)
description: The recommended single-resource API for running models.
---

# Models (v1alpha2)

`ai.flexinfer/v1alpha2` introduces a simplified CRD:

- `kind: Model`
- One resource per served model
- Optional GPU sharing via `spec.gpu.shared`
- Optional scale-to-zero via `spec.serverless`
- Optional proxy/LiteLLM discovery via annotations

## Cluster snapshot (homelab)

For a point-in-time view of what is currently deployed in `flexinfer-system` (models, shared groups, LiteLLM aliases, benchmarks), see:

- `docs/user/flexinfer-system-snapshot.md`

## Minimal example

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: llama3-8b
spec:
  backend: ollama
  source: ollama://llama3:8b
```

## `spec` fields (high-level)

### `spec.backend` (required)

Backend plugin name. Common values:

- `ollama`
- `vllm`
- `mlc-llm` (alias: `mlc`)
- `llamacpp` (alias: `llama.cpp`)
- `diffusers`
- `comfyui`
- `vllm-omni`

Exact images/args/ports are defined by the backend registry in `services/flexinfer/backend/`.

### `spec.source` (required)

Model source URI. Supported formats:

- `HF://org/model` (HuggingFace model)
- `ollama://model:tag` (Ollama registry name)
- `file:///path/to/model` (host path inside the container)
- `pvc://pvc-name/path` (PVC-backed model path)

Notes:

- For `backend: llamacpp` with `HF://` sources, set `spec.config.ggufFile: <file>.gguf` to select a GGUF file within the downloaded repo.
- FlexInfer now auto-prefetches only required llama.cpp files for HF GGUF repos:
  - If `spec.config.ggufFile` is set, the prefetch job downloads just that GGUF (plus optional relative `spec.config.mmproj`).
  - This avoids pulling full multi-quant repos by default.
- Optional advanced download controls (all in `spec.config`):
  - `hfAllowPatterns`: list (or comma-separated string) passed to `snapshot_download(..., allow_patterns=...)`
  - `hfIgnorePatterns`: list (or comma-separated string) passed to `snapshot_download(..., ignore_patterns=...)`
  - `hfRevision`: revision/tag/commit passed to `snapshot_download(..., revision=...)`

### `spec.gpu` (optional)

Controls GPU allocation and optional time-sharing.

- `vendor`: `auto`, `nvidia`, `amd`, or `cpu`
- `shared`: group name; models with the same value compete for the same GPU
- `priority`: higher wins preemption decisions
- `count`: GPUs required (default 1)
- `vramEstimateMB`: hint for scheduling/binpacking

Notes:

- If you omit `spec.gpu`, the model runs CPU-only.
- If you set `spec.gpu.vendor: cpu`, omit `spec.gpu.count` (it is rejected by CRD validation).
- If you set `spec.gpu.vendor: nvidia` or `amd`, the controller will only schedule on matching GPU nodes (it will not auto-fallback to the other vendor).
- `vramEstimateMB` is optional but strongly recommended on mixed GPU clusters (e.g., Maxwell 6GB + gfx1100 24GB). The scheduler extender uses it, along with the node agent's `flexinfer.ai/gpu-free-memory` annotation, to avoid placing large models onto low-VRAM nodes.

### `spec.serverless` (optional)

Scale-to-zero behavior.

- `enabled`: default true (homelab-friendly)
- `idleTimeout`: scale down after this idle window
- `coldStartTimeout`: request timeout budget during activation. For `vllm`,
  the pod startup probe uses at least this budget, so slow first-start compile
  passes can finish instead of being killed by kubelet before the proxy timeout.

### `spec.cache` (optional)

Model caching strategy.

- `strategy`: `Memory`, `SharedPVC`, or `None`
- `pvcName` / `storageClass` / `size`: only relevant for `SharedPVC`

Notes:

- If `spec.cache.strategy: SharedPVC` and `spec.cache.pvcName` is omitted, the controller auto-creates a PVC named `<model>-cache`.
- If `spec.source` is `pvc://...`, FlexInfer mounts that PVC at `/models` and ignores `spec.cache` for volume provisioning.

### `spec.config` (optional)

Backend-specific configuration as JSON (passed through to the backend plugin).

Example:

```yaml
spec:
  config:
    mode: server
    maxNumSequence: 4
```

For image-generation models, prefer explicit family routing instead of relying
on the source string alone:

- `flux` for FLUX.1 and FluxFill pipelines
- `sdxl` for SDXL and SDXL-derived models, including historical names like
  Gonzalomo/FluxPony or RealVisXL
- `sd3` for Stable Diffusion 3 / 3.5 families
- `sd15` for Stable Diffusion 1.5-derived pipelines such as InstructPix2Pix

Common imagegen knobs in this repo include `pipelineMode`, `modelFamily`,
`cpuOffload`, `quantization`, `useFp16`, `vaeRepo`, `vaePath`, `guidanceScale`,
`numInferenceSteps`, `warmupResolutions`, and `warmPolicy`.

Common `vllm` knobs include `maxModelLen`, `gpuMemoryUtilization`,
`maxNumSeqs`, `maxNumBatchedTokens`, `startupTimeout` or
`startupTimeoutSeconds`, `cudagraphCaptureSizes`,
`maxCudagraphCaptureSize`, `compilationConfig`, and `languageModelOnly`.
Set `languageModelOnly: true` for a multimodal checkpoint when the endpoint only
serves text; FlexInfer passes vLLM's `--language-model-only` flag so the unused
vision encoder is not loaded or profiled and the freed VRAM can hold more KV
cache. Set `dedicatedDeployment: true` when a Model's explicit image must bypass
the persistent node runtime—for example, when canarying a newer vLLM release.
Without that opt-out, a backend bundled in the selected `GPUProfile` runs inside
the persistent runtime and the Model's image is not used. `startupTimeout`
accepts duration strings such as `15m`; `startupTimeoutSeconds` accepts a
second count. If neither is set, vLLM uses the larger of its backend default
and `spec.serverless.coldStartTimeout`.

#### Maxwell (sm_5x) notes

On NVIDIA Maxwell GPUs (compute capability 5.x, e.g. GTX 980 Ti `sm_52`), FlexInfer enforces backend compatibility:

- `vllm`, `vllm-omni`, and `diffusers` are rejected on Maxwell.
- `mlc-llm` requires a pre-compiled library (FP32 quantization only). Prefer compiling to `/models/<modelName>/maxwell-lib.so` and setting `jitPolicy: READONLY`.

Example:

```yaml
spec:
  backend: mlc-llm
  gpu:
    vendor: nvidia
  config:
    jitPolicy: READONLY
    # Optional if you compile to /models/<modelName>/maxwell-lib.so:
    # modelLibPath: /models/<modelName>/maxwell-lib.so
```

### `spec.resources` / `spec.nodeSelector` (optional)

Pod resources and node selection. If you omit `nodeSelector`, the controller picks GPU nodes automatically.

### `spec.litellm` (optional)

Adds `litellm.flexinfer.ai/*` annotations so a LiteLLM proxy can discover and route requests.

### `spec.serviceLabels` (optional)

Semantic labels describing the model (for dynamic routing). Example: `["textgen","code","fast"]`.

## `status` fields (high-level)

`status` reflects lifecycle + routing metadata:

- `phase`: `Idle`, `Pending`, `Loading`, `Ready`, `Preempted`, `Failed`
- `loadingSubstage`: while `phase=Loading`, the best-known load stage:
  `ImagePulling`, `Initializing`, `LoadingWeights`, `Compiling`, or
  `HealthCheckPending`
- `message`: a short operator-facing status hint, such as the image pull reason
  or vLLM shard-load progress
- `loadingProgressAt`: when `loadingSubstage` or `message` last changed; the
  proxy uses this to return `503` with `Retry-After` when a weight load stalls
  instead of allowing the activation queue to grow indefinitely
- `endpoint`: service URL (cluster-internal)
- `lastActiveTime`: last time the proxy observed traffic (used for scale-to-zero)
- `cache`: cache readiness details (`ready`, plus the prefetch/check Job state)

## Examples

- `services/flexinfer/examples/v1alpha2/model-basic.yaml`
- `services/flexinfer/examples/v1alpha2/model-shared-gpu.yaml`
- `services/flexinfer/examples/v1alpha2/model-amd-rocm.yaml`
- `services/flexinfer/examples/v1alpha2/model-image-gen.yaml`

## Current Gemma 4 profiles

The homelab currently exposes two managed `gemma-4-E4B-it` profiles through
LiteLLM:

| Model ID | Backing Model CR | Node / lane | Intent |
|----------|------------------|-------------|--------|
| `gemma4-e4b` | `gemma4-e4b-turboquant` | `cblevins-7900xtx` / `7900xtx-textgen` | Default alias; points to the fast profile |
| `gemma4-e4b-fast` | `gemma4-e4b-turboquant` | `cblevins-7900xtx` / `7900xtx-textgen` | Lower-latency interactive textgen |
| `gemma4-e4b-long` | `gemma4-e4b-turboquant-canary` | `cblevins-5930k` / `5930k-textgen` | Long-context TurboQuant profile |

Current operator intent:

- `gemma4-e4b` and `gemma4-e4b-fast` use the same stable managed service.
- `gemma4-e4b-long` is the separate long-context service with a more
  conservative batching profile.
- Old compatibility aliases were removed to keep OpenWebUI and LiteLLM model
  lists short.

Current profile shape:

| Model ID | `maxModelLen` | `maxNumBatchedTokens` | `kvCacheCodec` |
|----------|---------------|-----------------------|----------------|
| `gemma4-e4b` / `gemma4-e4b-fast` | `16384` | `512` | standard float16 KV cache |
| `gemma4-e4b-long` | `32768` | `160` | `turboquant` |

Example request routing:

```bash
curl http://litellm.ai.svc:8000/v1/chat/completions \
  -H "Authorization: Bearer sk-litellm-master-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemma4-e4b-long",
    "messages": [{"role": "user", "content": "Summarize this long document..."}],
    "max_tokens": 512
  }'
```
