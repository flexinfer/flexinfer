---
title: CPU-Only Backend Guide
description: Running inference on CPU with llama.cpp (GGUF).
---

# CPU-Only Backend Guide

FlexInfer supports CPU-only inference via the `llamacpp` backend. This is useful for:

- Nodes without GPUs (dev boxes, CI runners, edge nodes)
- Small models (1-3B) and testing
- Low-cost, higher-latency workloads (batch jobs, background tasks)

## Quick Start (v1alpha2)

This example downloads a GGUF repo from HuggingFace to a cache PVC and runs llama.cpp on CPU.

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: tinyllama-cpu
spec:
  backend: llamacpp
  source: HF://TheBloke/TinyLlama-1.1B-Chat-v1.0-GGUF
  gpu:
    vendor: cpu
  config:
    # Required for HF:// sources with llama.cpp:
    # pick a .gguf file inside the repo downloaded to /models/<modelName>/...
    ggufFile: tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf
    threads: 4
    contextSize: 2048
    batchSize: 256
  resources:
    requests:
      cpu: "2"
      memory: 4Gi
    limits:
      cpu: "4"
      memory: 8Gi
  serverless:
    idleTimeout: 10m
```

Notes:

- For `spec.gpu.vendor: cpu`, omit `spec.gpu.count` (it is rejected by CRD validation).
- Many clusters taint GPU nodes with `dedicated=gpu:NoSchedule`. FlexInfer only adds that toleration when GPUs are requested, so CPU models will naturally avoid dedicated GPU nodes.

## Sources And File Paths

llama.cpp needs a **GGUF file path**.

Supported patterns:

- `HF://org/repo` with `spec.config.ggufFile: <file>.gguf`
  - FlexInfer downloads the repo to `/models/<modelName>/` and passes `/models/<modelName>/<ggufFile>` to llama.cpp.
  - For llama.cpp, FlexInfer now defaults to selective HF prefetch and downloads only the configured GGUF (and optional relative `mmproj`) instead of the full repo.
  - Advanced options: `spec.config.hfAllowPatterns`, `spec.config.hfIgnorePatterns`, `spec.config.hfRevision`.
- `pvc://pvc-name/path/to/model.gguf`
  - FlexInfer mounts the PVC at `/models` and passes `/models/path/to/model.gguf` to llama.cpp.
- `file:///absolute/path/to/model.gguf`
  - Use a path that is not hidden by the `/models` volume mount (for example, a file baked into a custom image under `/opt/models/...`).

## Performance Expectations

CPU inference is typically **10-50x slower** than modern GPUs. Real performance depends on:

- Model size and quantization (prefer Q4/Q5 GGUF for CPU)
- CPU ISA (AVX2 is a practical baseline; AVX512 helps)
- Memory bandwidth
- Threading configuration

## llama.cpp Config Keys (v1alpha2)

`spec.config` is passed through to the backend plugin. For llama.cpp, the following keys are supported:

- `ggufFile` (string): Required for `HF://` sources. A `.gguf` filename within the repo.
- `contextSize` (int): `--ctx-size`
- `batchSize` (int): `--batch-size`
- `threads` (int): `--threads` (set to physical cores, not hyperthreads)
- `parallel` (int): `--parallel`
- `nGPULayers` (int): `--n-gpu-layers` (ignored when `gpu.vendor: cpu`, which forces `0`)
- `flashAttention` (bool): `--flash-attn on` (ignored when `gpu.vendor: cpu`)
- `cacheTypeK` / `cacheTypeV` (string): `--cache-type-k` / `--cache-type-v`
- `ubatchSize` (int): `--ubatch-size`
- `metrics` (bool): `--metrics`
- `chatTemplate` (string): `--chat-template`
- `mmproj` (string): `--mmproj` (multimodal projection file)
- `reasoningFormat` (string): `--reasoning-format` (`none`, `deepseek`, `deepseek-legacy`)
- `reasoningBudget` (int): `--reasoning-budget` (`-1` or `0` in current llama-server builds)
- `device` (string): `--device` (explicit device selection on multi-GPU nodes)
- `hipVisibleDevices` / `rocrVisibleDevices` (string): AMD device pinning env vars
- `gpuDeviceOrdinal` (string): sets `GPU_DEVICE_ORDINAL`; also used as fallback for `--device`
