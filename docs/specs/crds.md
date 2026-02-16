---
title: CRDs
description: Kubernetes API surface for FlexInfer.
---

# CRDs

FlexInfer exposes Kubernetes APIs under the `ai.flexinfer` group.

## v1alpha2 (recommended)

- `Model` (`ai.flexinfer/v1alpha2`)
- `LoRAAdapter` (`ai.flexinfer/v1alpha2`)
- `ModelCatalog` (`ai.flexinfer/v1alpha2`)
- `Cluster` (`ai.flexinfer/v1alpha2`, Phase 5.1 scaffold)
- `FederatedModel` (`ai.flexinfer/v1alpha2`, Phase 5.2 scaffold)
- `GlobalProxy` (`ai.flexinfer/v1alpha2`, Phase 5.3 scaffold)

Generated CRD:

- `services/flexinfer/config/crd/ai.flexinfer_models.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_loraadapters.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_modelcatalogs.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_clusters.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_federatedmodels.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_globalproxies.yaml`

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
  - `services/flexinfer/api/v1alpha2/lora_types.go`
  - `services/flexinfer/api/v1alpha2/catalog_types.go`
  - `services/flexinfer/api/v1alpha2/cluster_types.go`
  - `services/flexinfer/api/v1alpha2/federatedmodel_types.go`
  - `services/flexinfer/api/v1alpha2/globalproxy_types.go`
  - `services/flexinfer/api/v1alpha1/types.go`
  - `services/flexinfer/api/v1alpha1/modelcache_types.go`
  - `services/flexinfer/api/v1alpha1/gpugroup_types.go`
- Generation:
  - `make manifests` writes CRDs to `services/flexinfer/config/crd/`

## Helm installation note

The Helm chart also vendors CRDs under:

- `services/flexinfer/charts/flexinfer/crds/`

Those should match `services/flexinfer/config/crd/` (same schemas, different packaging).
