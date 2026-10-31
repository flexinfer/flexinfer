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
- `InferenceJob` (`ai.flexinfer/v1alpha2`): one asynchronous inference request (below)

Generated CRD:

- `services/flexinfer/config/crd/ai.flexinfer_models.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_loraadapters.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_modelcatalogs.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_clusters.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_federatedmodels.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_globalproxies.yaml`
- `services/flexinfer/config/crd/ai.flexinfer_inferencejobs.yaml`

### `InferenceJob`

Long work (a render, a transcription, a long chat) handed to the proxy, which runs it against the
named Model through the same cold-start, first-byte budget, breaker and reroute path as a
synchronous request, stores the output and reports it by id. The proxy creates these for
`background: true` requests and `POST /v1/jobs`; they can also be created with `kubectl`
(namespace `flexinfer-system`, short name `ijob`). Phases are the OpenAI Responses background
statuses, so the proxy's job object returns them unchanged.

| Field | Meaning |
|---|---|
| `spec.model` | Model to run against: CR name, served name or alias |
| `spec.path` | OpenAI path, e.g. `/v1/images/generations` (must start `/v1/`) |
| `spec.request` | inline JSON body (up to 256 KiB) |
| `spec.requestRef` | `{kind: ConfigMap\|Secret, name, key}` holding the body, for kubectl-created jobs |
| `spec.contentType` | body media type; default `application/json`, a multipart body carries its boundary |
| `spec.inputRefs[]` | `{name, uri, contentType, bytes, sha256}` inputs in job storage; `name: body` is the request body (multipart uploads) |
| `spec.webhook` | `{url, secretRef, events[]}`; Standard Webhooks headers, signed with the secret; events default to `job.completed`, `job.failed`, `job.cancelled`, `job.incomplete` |
| `spec.ttlAfterFinished` | keep the finished job and its output this long (default 24h) |
| `spec.qosClass` | caller QoS class (D3) |
| `spec.idempotencyKey` | dedupe submissions per caller |
| `spec.cancel` | set true to cancel |
| `status.phase` | `queued`, `in_progress`, `completed`, `failed`, `cancelled`, `incomplete` |
| `status.progress` | 0–100 |
| `status.queuedAt`, `startedAt`, `completedAt`, `expiresAt` | lifecycle times; `expiresAt` = `completedAt` + TTL |
| `status.output` | `{uri, contentType, bytes, sha256}` of the stored response body |
| `status.error` | `{code, message, status}`: an error-envelope code or the lane's own |
| `status.runner`, `status.lane`, `status.attempts` | proxy pod that claimed it, the Model that served it, claims so far |
| `status.heartbeatAt` | the runner's liveness stamp; a job silent for three intervals is reclaimed |
| `status.webhook` | `{delivered[], attempts, lastError, lastAttemptAt}` |

Printer columns: Model, Path, Phase, Completed, Age; `-o wide` adds Progress, Lane, Runner. The
manager deletes a finished job once `status.expiresAt` (completion + `ttlAfterFinished`) passes;
the proxy's `flexinfer.ai/job-storage` finalizer deletes its stored inputs and output first. The
CLI lists and manages jobs: `flexinfer jobs list [--phase P] [--model M]`, `flexinfer jobs get|cancel|delete
<id>`, and `flexinfer jobs content <id> --proxy <url> [-o file]` downloads an output. RBAC:
the proxy's Role reads and writes `inferencejobs` and `inferencejobs/status`, reads ConfigMaps and
Secrets in its namespace (`requestRef`, webhook secrets) and creates the webhook-secret Secret; the
controller's ClusterRole manages them for TTL garbage collection.

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
  - `services/flexinfer/api/v1alpha2/inferencejob_types.go`
  - `services/flexinfer/api/v1alpha1/types.go`
  - `services/flexinfer/api/v1alpha1/modelcache_types.go`
  - `services/flexinfer/api/v1alpha1/gpugroup_types.go`
- Generation:
  - `make manifests` writes CRDs to `services/flexinfer/config/crd/`

## Helm installation note

The Helm chart also vendors CRDs under:

- `services/flexinfer/charts/flexinfer/crds/`

Those should match `services/flexinfer/config/crd/` (same schemas, different packaging).
