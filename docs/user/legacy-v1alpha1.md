---
title: Legacy API (v1alpha1)
description: ModelDeployment + ModelCache + GPUGroup (more knobs, more moving parts).
---

# Legacy API (v1alpha1)

`ai.flexinfer/v1alpha1` is the original multi-resource API. It is still supported, but the project direction is:

- Prefer `v1alpha2` `Model` for new setups.
- Use `v1alpha1` only when you need knobs that haven’t been ported yet.

## Resources

- `ModelDeployment`: the primary “served model” object
- `ModelCache`: model artifact caching + pre-warming
- `GPUGroup`: multi-model GPU sharing with preemption, anti-thrashing, and demand signaling

## When v1alpha1 is still useful

- You want explicit `GPUGroup` swap policies and anti-thrashing controls.
- You’re relying on older example manifests and workflows.
- You want to pre-stage downloads via `ModelCache` and reuse a cache across multiple deployments.

## Quick mental model

In v1alpha1 you typically create:

1. (Optional) `ModelCache` to download and store model artifacts
2. `ModelDeployment` to run the backend container
3. (Optional) `GPUGroup` to coordinate multiple `ModelDeployment`s sharing a GPU

## Examples

- `services/flexinfer/examples/serverless-multi-backend.yaml`
- `services/flexinfer/examples/gpugroup-multi-model.yaml`
- `services/flexinfer/examples/ram-cached-models.yaml`

