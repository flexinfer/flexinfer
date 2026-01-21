---
title: CRDs
description: Kubernetes API surface for FlexInfer.
---

# CRDs

FlexInfer exposes Kubernetes APIs under the `ai.flexinfer` group.

## v1alpha2 (recommended)

- `Model` (`ai.flexinfer/v1alpha2`)

Generated CRD:

- `services/flexinfer/config/crd/ai.flexinfer_models.yaml`

## v1alpha1 (legacy)

- `ModelDeployment` (`ai.flexinfer/v1alpha1`)
- `ModelCache` (`ai.flexinfer/v1alpha1`)
- `GPUGroup` (`ai.flexinfer/v1alpha1`)

Generated CRDs:

- `services/flexinfer/config/crd/ai.flexinfer_modeldeployments.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_modelcaches.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_gpugroups.yaml`

## Where schemas come from

- Go types:
  - `services/flexinfer/api/v1alpha2/model_types.go`
  - `services/flexinfer/api/v1alpha1/types.go`
  - `services/flexinfer/api/v1alpha1/modelcache_types.go`
  - `services/flexinfer/api/v1alpha1/gpugroup_types.go`
- Generation:
  - `make manifests` writes CRDs to `services/flexinfer/config/crd/`

## Helm installation note

The Helm chart also vendors CRDs under:

- `services/flexinfer/charts/flexinfer/crds/`

Those should match `services/flexinfer/config/crd/` (same schemas, different packaging).

