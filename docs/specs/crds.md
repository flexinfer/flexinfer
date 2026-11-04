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

### `Model`: structured-output opt-in

`spec.capabilities.structuredOutput: true` asks the controller to render vLLM's
`--structured-outputs-config {"backend":"auto"}` behind the GPUProfile
certificate `vllm/structured-outputs` (`docs/agents/gpu-compatibility.md`). It
renders only on a vllm Model with `spec.config.dedicatedDeployment: true` whose
digest-pinned image matches the certificate. Otherwise nothing renders and the
Model keeps serving. The outcome is condition `StructuredOutput`:

| Status | Reason | Meaning |
|---|---|---|
| True | `StructuredOutputCertified` | flag rendered; message names the certified image, since and evidence |
| False | `StructuredOutputNotCertified` | no GPUProfile, no or partial certificate, image not digest-pinned, or certificate bound to another image |
| False | `StructuredOutputUnsupported` | backend is not vllm, or the lane loads into a runtime pod |

The condition is absent when the field is unset or false. `structuredOutput` is
also matched by WorkflowProfile `capabilities` and `requiredCapabilities`, as a
declaration, like `toolCalling`.

### `Model`: vLLM release and release-specific keys

`spec.config.vllmVersion` declares the vLLM release a `vllm` Model's image
carries, as a quoted string: `"0.33.0"`, `"v0.33.0"`, `"0.33.0+rocm72"` or
`"0.33"`. Like `llamacppBuild` for llama.cpp it is controller metadata: it never
reaches the server, it only selects what the backend renders for that release.
Leave it unset and the lane renders exactly what it rendered before the key
existed. Set it when a lane moves to vLLM 0.32 or later, and in the same change
as the image pin, because `failOnEnvironValidation: true` makes vLLM refuse to
start on any `VLLM_*` variable its `vllm/envs.py` does not define.

| Key / output | Unset `vllmVersion` | `vllmVersion` ≥ 0.32.0 | ≥ 0.33.0 |
|---|---|---|---|
| `VLLM_ROCM_USE_AITER_PAGED_ATTN` (from `disableAiter`, `rocmUseAiter: false`, `rocmCustomPagedAttn`) | rendered | dropped (not in 0.32.0 or later) | dropped |
| `VLLM_USE_TRITON_FLASH_ATTN` (from `enableFlashAttention`) | rendered | dropped | dropped |
| `VLLM_ROCM_USE_AITER_FP4_ASM_GEMM` (from `disableAiter`, `rocmUseAiter: false`) | rendered | rendered | dropped (removed in 0.33.0, vLLM #61278) |
| `mambaCacheMode` → `--mamba-cache-mode` | rendered | rendered (0.32 already picks `align` when prefix caching is on) | not rendered: a deprecated no-op in 0.33.0 that 0.34 removes; prefix caching on hybrids follows `enablePrefixCaching` |

The AITER keys (`enableAiter`, `disableAiter`, `rocmUseAiter`) do nothing on
gfx1100: vLLM uses AITER only on CDNA 3 or newer (gfx942, gfx950).

Pass-through keys, each rendered only when set (all exist in vLLM 0.32.0 and
0.33.0):

| Key | Flag | Value |
|---|---|---|
| `prefixMatchUnit` | `--prefix-match-unit` | integer > 0; the token boundary a prefix-cache hit can land on. Every KV cache group's block size must divide by it |
| `enableMambaSharedPrefixCheckpoint` | `--enable-mamba-shared-prefix-checkpoint` / `--no-…` | bool; also checkpoints Mamba state where an MTP/EAGLE sibling resumes. Needs prefix caching (Mamba `align` mode), MTP or EAGLE and a `prefixMatchUnit` below the Mamba block size |
| `kvTransferConfig` | `--kv-transfer-config` | JSON object, as an encoded string or a YAML map |
| `sleepPreserveParameterNames` | `--sleep-preserve-parameter-names` | list of parameter-name globs kept across a level-2 sleep, e.g. `["draft.*"]`; one token per glob |

The controller refuses these with Phase `Failed`, `Schedulable=False` and reason
`ConfigInvalid` before anything renders:

- `maxNumPartialPrefills` or `maxLongPartialPrefills`: vLLM has no
  `--max-num-partial-prefills` / `--max-long-partial-prefills` flag (absent from
  0.32.0, 0.33.0 and main), so the server would abort in argument parsing.
- a `vllmVersion` that is not a quoted release string.

### `Model`: certificate-gated backends

A backend that only a GPUProfile certificate admits (`pyannote` today, D5-a) is
refused on a profile that does not certify the runtime image it would load in.
The profile must list the backend in `spec.runtime.bundledBackends`, pin
`spec.runtime.image` by digest, and carry the complete
`flexinfer.ai/backend-certificate-pyannote-speaker-diarization` triple bound to
that digest. The outcome is condition `BackendCertificate`, and a refusal also
sets Phase `Failed` and `Schedulable=False` with the same reason:

| Status | Reason | Meaning |
|---|---|---|
| True | `BackendCertified` | the runtime image is certified; message names it, since and evidence |
| False | `BackendNotCertified` | no GPUProfile, backend not bundled, runtime image not digest-pinned, no or partial certificate, or certificate bound to another image |
| False | `BackendUnsupported` | `spec.config.dedicatedDeployment: true` or `gpu.vendor: cpu`: the backend runs only in a GPU runtime pod |
| False | `ConfigInvalid` | `spec.config.hfTokenSecret` / `hfTokenSecretKey` is not a Secret name / key |

The condition is absent for other backends. A GPUProfile change re-reconciles
the Model, so certifying the profile un-fails it without a re-apply. The proxy
reads `BackendCertificate=True` on a diarization lane before it accepts a
`diarize=true` transcription.

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
