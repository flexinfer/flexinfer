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

### `spec.gpu` (optional)

Controls GPU allocation and optional time-sharing.

- `vendor`: `auto`, `nvidia`, `amd`, or `cpu`
- `shared`: group name; models with the same value compete for the same GPU
- `priority`: higher wins preemption decisions
- `count`: GPUs required (default 1)
- `vramEstimateMB`: hint for scheduling/binpacking

Notes:

- If you omit `spec.gpu`, the model runs CPU-only.
- If you set `spec.gpu.vendor: cpu`, the controller will not request GPU resources even if `count` is set.
- If you set `spec.gpu.vendor: nvidia` or `amd`, the controller will only schedule on matching GPU nodes (it will not auto-fallback to the other vendor).

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
