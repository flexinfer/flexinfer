---
title: flexinfer-system Snapshot
description: A point-in-time snapshot of the currently deployed models, aliases, and benchmarks.
---

# flexinfer-system Snapshot

This page is intentionally cluster-specific and will go stale. It exists to answer "what is deployed right now?" quickly.

Snapshot taken: **2026-02-14**

Update (2026-05-16): the 7900 XTX text posture is now intentionally simple.
Only the two warm Gemma4 26B-A4B GPTQ primaries should occupy the 7900 XTX
chat lanes:

- `gemma4-26b-a4b-gptq` on `cblevins-7900xtx`
- `gemma4-26b-a4b-gptq-5930k` on `cblevins-5930k`

The shared default chat aliases (`fast-chat`, `fast-text`, `gpt-3.5-turbo`,
`copilot`, `gpt-4`, `quality-chat`, `mid-chat`, `qwen3-default`, and
`project-mgmt`) are declared as `serviceLabels` on both Models so
`flexinfer-proxy` can spread traffic across Ready members. Node-specific
`litellm.aliases` are kept separate.

Quick current-state checks:

```bash
kubectl -n flexinfer-system get model \
  gemma4-26b-a4b-gptq gemma4-26b-a4b-gptq-5930k \
  qwen3-8b-fast-7900xtx qwen3-8b-fast-fallback-5930k

kubectl -n flexinfer-system get svc \
  gemma4-26b-a4b-gptq gemma4-26b-a4b-gptq-5930k \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\\n"}{.metadata.annotations.litellm\\.flexinfer\\.ai/aliases}{"\\n\\n"}{end}'
```

Namespace: `flexinfer-system`

## Deployed v1alpha2 Models

Current `ai.flexinfer/v1alpha2` `Model` resources:

| Model | Backend | Source | GPU | Shared Group | Priority | Serverless (min) | LiteLLM | Phase | Endpoint |
|---|---|---|---|---|---|---|---|---|---|
| `deepseek-r1-reasoning` | `mlc-llm` | `HF://mlc-ai/DeepSeek-R1-Distill-Qwen-14B-q4f16_1-MLC` | `amd` | `7900xtx-quality` | `70` | `true (0)` | `false` | `Idle` | `http://deepseek-r1-reasoning.flexinfer-system.svc:8000` |
| `glm47-flash-opus45` | `llamacpp` | `pvc://glm47-flash-opus45-cache/glm47-flash-opus45/glm-4.7-flash-claude-4.5-opus.q4_k_m.gguf` | `amd` | `5930k-models` | `130` | `true (1)` | `true` | `Idle` | `http://glm47-flash-opus45.flexinfer-system.svc:8080` |
| `qwen3-14b-mlc` | `mlc-llm` | `pvc://mlc-models-nvme-7900xtx` | `amd` | `7900xtx-quality` | `50` | `true (0)` | `false` | `Idle` | `http://qwen3-14b-mlc.flexinfer-system.svc:8000` |
| `qwen3-32b-quality` | `mlc-llm` | `HF://mlc-ai/Qwen3-32B-q4f16_1-MLC` | `amd` | `7900xtx-quality` | `80` | `true (0)` | `false` | `Idle` | `http://qwen3-32b-quality.flexinfer-system.svc:8000` |
| `qwen3-8b-fast` | `mlc-llm` | `pvc://mlc-8b-abliterated-nvme-5930k/Qwen3-8B-abliterated-q4f32_1-MLC` | `amd` | `5930k-models` | `140` | `true (0)` | `true` | `Idle` | `http://qwen3-8b-fast.flexinfer-system.svc:8000` |
| `qwen3-vl-vision` | `llamacpp` | `pvc://vision-models-nvme-5930k/Qwen3-VL-8B-Instruct-abliterated-v2.0.Q4_K_M.gguf` | `amd` | `5930k-models` | `100` | `true (1)` | `true` | `Ready` | `http://qwen3-vl-vision.flexinfer-system.svc:8080` |
| `sdxl-turbo-imagegen` | `diffusers` | `HF://stabilityai/sd-turbo` | `nvidia` | _none_ | `100` | `true (1)` | `true` | `Ready` | `http://sdxl-turbo-imagegen.flexinfer-system.svc:8000` |

Notes:
- The name `sdxl-turbo-imagegen` is historical; the current source is `HF://stabilityai/sd-turbo`.
- Models in `Idle` are typically scale-to-zero or shared-GPU queued/preempted states; `Ready` implies an active backend pod.

## GPU Topology And Time-Sharing

FlexInfer GPU time-sharing is configured via `spec.gpu.shared` (a string group name). Only one model in a shared group is `Active` at a time; others are `Queued` or `Preempted`.

Current groups observed in `Model.spec.gpu.shared`:

- `5930k-models`: `qwen3-vl-vision` is `Ready` and `Active`; `qwen3-8b-fast` and `glm47-flash-opus45` are currently `Queued`.
- `7900xtx-quality`: `deepseek-r1-reasoning`, `qwen3-14b-mlc`, `qwen3-32b-quality` are configured for sharing this group (currently `Idle`).

Note: `Model.status.sharedGroup` can be stale if `spec.gpu.shared` was removed later; prefer `spec.gpu.shared` as the source of truth for time-sharing configuration.

### Image Generation On GTX 980 Ti

Image generation is deployed on the Maxwell node `cblevins-gtx980ti`:

- `Model/sdxl-turbo-imagegen`
- Requests `nvidia.com/gpu: 1`
- `spec.nodeSelector.kubernetes.io/hostname: cblevins-gtx980ti`

## LiteLLM Alias Map (Service Annotations)

LiteLLM discovery is implemented via `Service` annotations that the v1alpha2 controller reconciles:

| Service | Served Model | Aliases | Copilot Alias |
|---|---|---|---|
| `glm47-flash-opus45` | `glm47-flash-opus45` | `experimental-chat,fast-chat,fast-chat-canary,glm-flash,textgen` | _none_ |
| `qwen3-8b-fast` | `qwen3-8b-fast` | `copilot,fast-chat-fallback,fast-text,gpt-3.5-turbo` | `copilot` |
| `qwen3-vl-vision` | `qwen3-vl-vision` | `gpt-4-vision-preview,gpt-4o,multimodal,ocr,qwen3-vl,vision` | _none_ |
| `sdxl-turbo-imagegen` | `sdxl-turbo-imagegen` | `dall-e-3,image-gen,text-to-image` | _none_ |

Models with `spec.litellm.enabled: false` are not exposed via these annotations (even if they are otherwise routable in-cluster).

## Benchmarks

Benchmarks currently live in `ConfigMap` resources in `flexinfer-system`.

Per-model benchmark ConfigMaps observed:

| ConfigMap | Backend | Model | GPU | Tokens/sec | VRAM Used | Notes |
|---|---|---|---|---:|---|---|
| `qwen3-8b-fast-benchmark-results` | `mlc-llm` | `Qwen3-8B-abliterated-q4f32_1-MLC` | `AMD gfx1100` | 106.01 | `8Gi` |  |
| `qwen3-14b-quality-benchmark-results` | `mlc-llm` | `Qwen3-14B-q4f16_1-MLC` | `AMD gfx1100` | 82.39 | `16Gi` |  |
| `qwen3-14b-abliterated-benchmark-results` | `mlc-llm` | `Qwen3-14B-abliterated-q4f16_1-MLC` | `AMD gfx1100` | 75.0 | `16Gi` |  |
| `qwen3-14b-mlc-debug-benchmark-results` | `mlc-llm` | `Qwen3-14B-q4f16_1-MLC` | `AMD gfx1100` | 77.82 | `16Gi` |  |
| `sdxl-turbo-fast-benchmark-results` | `diffusers` | `stabilityai/sdxl-turbo` | `AMD gfx1100` | 3696.29 | `10Gi` | Includes ROCm fp16 VAE compatibility notes |
| `bge-large-embeddings-benchmark-results` | `tei` | `BAAI/bge-large-en-v1.5` | `CPU` | 203.25 | `2Gi` | Appears to be a legacy/non-Model workload |

There is also a global benchmark map ConfigMap:

- `ConfigMap/flexinfer-benchmark-results`

## Reproduce This Snapshot

```bash
# v1alpha2 Models
kubectl -n flexinfer-system get model -o wide

# Model spec + status for a single model
kubectl -n flexinfer-system get model qwen3-8b-fast -o yaml

# LiteLLM alias map (service annotations)
kubectl -n flexinfer-system get svc -o yaml | rg litellm

# See which pods are actually running (scale-to-zero means many models show 0/0 deploys)
kubectl -n flexinfer-system get pods -o wide

# Benchmark ConfigMaps
kubectl -n flexinfer-system get cm | rg benchmark
kubectl -n flexinfer-system get cm qwen3-8b-fast-benchmark-results -o yaml
kubectl -n flexinfer-system get cm flexinfer-benchmark-results -o yaml
```
