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
- `coldStartTimeout`: request timeout budget during activation

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
- `endpoint`: service URL (cluster-internal)
- `lastActiveTime`: last time the proxy observed traffic (used for scale-to-zero)
- `cache`: cache readiness details (`ready`, plus the prefetch/check Job state)

## Examples

- `services/flexinfer/examples/v1alpha2/model-basic.yaml`
- `services/flexinfer/examples/v1alpha2/model-shared-gpu.yaml`
- `services/flexinfer/examples/v1alpha2/model-amd-rocm.yaml`
- `services/flexinfer/examples/v1alpha2/model-image-gen.yaml`
