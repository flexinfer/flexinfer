---
title: FlexInfer user docs
description: Concepts and workflows for deploying and operating models with FlexInfer.
---

# User docs

FlexInfer is a Kubernetes-native set of controllers and agents for running AI workloads (LLMs and image generation) on GPU nodes with sane defaults for a homelab.

## Start here

- `docs/user/quickstart.md` for install + first model.
- `docs/user/models-v1alpha2.md` for the recommended CRD (`Model`).
- `docs/user/proxy.md` for how requests are routed (OpenAI-style payloads supported).

## API versions

- **Recommended**: `ai.flexinfer/v1alpha2` `Model` (single resource).
- **Legacy**: `ai.flexinfer/v1alpha1` `ModelDeployment` + `ModelCache` + `GPUGroup` (more knobs, more moving parts).

