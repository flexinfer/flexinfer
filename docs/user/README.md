---
title: FlexInfer user docs
description: Concepts and workflows for deploying and operating models with FlexInfer.
---

# User docs

FlexInfer is a Kubernetes-native set of controllers and agents for running AI workloads (LLMs and image generation) on GPU nodes with sane defaults for a homelab.

## Start here

- `docs/user/quickstart.md` for install + first model.
- `docs/user/models-v1alpha2.md` for the recommended CRD (`Model`).
- `docs/user/workflow-profiles.md` for stable serving-intent names over existing models.
- `docs/user/proxy.md` for how requests are routed (OpenAI-style payloads supported).

## API versions

- **Recommended**: `ai.flexinfer/v1alpha2` `Model` (single resource).
- **Serving intent**: `ai.flexinfer/v1alpha2` `WorkflowProfile` (stable names over existing `Model` resources).
- **Legacy**: `ai.flexinfer/v1alpha1` `ModelDeployment` + `ModelCache` + `GPUGroup` (more knobs, more moving parts).

## Backend guides

- `docs/user/backends-cpu.md` - CPU-only inference (no GPU required)
- `docs/user/backends-maxwell.md` - NVIDIA Maxwell GPUs (GTX 980 Ti, etc.)
- `docs/user/backends-rocm-gfx1100.md` - AMD RX 7900 series (RDNA3)

## Caching guides

- `docs/user/caching.md` - Model caching strategies overview
- `docs/user/caching-oci.md` - OCI registry model sources (Harbor, GHCR, ECR)
- `docs/user/quantization.md` - GGUF/AWQ/GPTQ quantization workflows for ModelCache
- `docs/user/abliteration.md` - Refusal-direction removal before quantization
