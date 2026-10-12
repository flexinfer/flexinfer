---
title: Text-only EmbeddingGemma 2
description: Pinned llama.cpp deployment and FP32 qualification on CPU and Radeon VII.
---

# Text-only EmbeddingGemma 2

Use `llamacpp` with the official text-only GGUF, mean pooling, 8,192-token
context, and normalized 768-dimensional output. The projection is part of the
model; 512-dimensional hidden states are not the serving output. This lane
accepts text through `/v1/embeddings`. Vision, audio, video, and reduced MRL
output dimensions are outside this qualification.

The [model card](https://huggingface.co/google/embeddinggemma-2) specifies task
prefixes and warns against FP16. Both BF16 and Q8_0 are checked against an
independent CPU FP32 SentenceTransformers reference. BF16 is the deployment
default. On gfx906, llama.cpp handles BF16 weights without requiring native
BF16 hardware; the hardware parity gate verifies the resulting computation.

| Artifact | Pinned value |
| --- | --- |
| FP32 checkpoint | `google/embeddinggemma-2@914f7f89142e33e77833254d9c9b90c3cef7303b` |
| GGUF repository | `ggml-org/embeddinggemma-2-GGUF@bfcd298762cc34d0357ece5ebdd31791a3a374d8` |
| llama.cpp GPU build | `b11457`, `gfx906-embeddinggemma2` runtime profile |
| GPU image | `registry.harbor.lan/flexinfer/runtime@sha256:ad85a07230f8d27cfb8859066dedb6a53a351a8e36d9ec28077fd00427015a9b` |
| CPU server | `ghcr.io/ggml-org/llama.cpp:server@sha256:ca61722be4814ebd0b36f2ad02643565a53198e4c752b06609d5666571b56409` |

## Requests and vector spaces

The client supplies prefixes; the server does not rewrite inputs:

```json
{
  "model": "embeddinggemma-2",
  "input": [
    "task: search result | query: How do local embedding caches work?",
    "title: Local caches | text: Model files are staged near the inference runtime."
  ],
  "encoding_format": "float"
}
```

Use `task: code retrieval | query: ...` for code search and
`title: filename | text: ...` for code documents. Use `title: none` for untitled
documents. Budget prefixes and special tokens within the context limit.

Keep separate collections and re-embed both documents and queries when adopting
this model. BGE uses 1,024 dimensions and GTE uses 1,536; matching dimensions by
truncation or padding does not make their vector spaces compatible. The new
manifest exposes only `embeddinggemma-2` and `embeddinggemma-2-text`. Existing
`embeddings`, `rag`, and GTE aliases retain their current meaning.

## Deployment recipes

`deploy/models/embeddinggemma-2-cpu.yaml` is an explicit CPU, scale-to-zero
canary. `deploy/models/embeddinggemma-2-radeonvii.yaml` is the warm shared-runtime
recipe with `embedding: true`, `pooling: mean`, `parallel: 1`, and
`contextSize`, `batchSize`, and `ubatchSize` all set to 8,192. Use a qualified
runtime image before adding the Radeon VII recipe to the model Kustomization.

The gfx906 startup environment must completely unset
`HSA_OVERRIDE_GFX_VERSION`; an empty value can hide the GPU from ROCr. Preserve
`HSA_ENABLE_SDMA=0`, `HSA_USE_SVM=0`, and the HIP memory-info shim. The canary
requires proof that every layer was offloaded and rejects CPU fallback.

Build the overlay with the normal remote builder:

```bash
build/build-runtime.sh gfx906-embeddinggemma2 --push
```

For a fleet rollout, use the normal digest-promotion and GitOps workflow. The
rollback image is
`registry.harbor.lan/flexinfer/runtime@sha256:8d81c14efa8ad22ff86207d0d901a6e8d0c7e84db2fe487af484762d3ba88616`.
Its inherited GPU-discovery problem is recorded in the validation evidence;
rollback restores the preceding fleet image, not a newly certified GPU lane.

## Validation and benchmarking

The CPU Job in `deploy/debug/embeddinggemma-2-cpu-qualification.yaml` builds a
reusable FP32 reference with vision/audio encoders disabled. Its pinned CPU
image includes Torch 2.9.0, Torchvision 0.24.0, SentenceTransformers 6.1.0,
and Transformers 5.19.0. Torchvision is needed by the text processor import path.

The corpus covers 19 short, code, Unicode, multilingual, and near-4K/8K inputs.
Gates require finite normalized 768-dimensional vectors, exact response indices,
actual positive `usage.prompt_tokens`, minimum reference cosine of 0.99 for BF16
and 0.98 for Q8, single/batch cosine at least 0.999, and matching top-ranked
documents for all five retrieval queries. This is correctness parity, not a
retrieval-quality comparison against BGE or GTE.

`flexinfer-bench` supports `--task embeddings`, repeatable `--embedding-input`,
and document batch sizes. It reports input tokens/s and documents/s using
server usage and measured request time, and persists task, prompt tokens, and
document counts. Its generation default retains completion tokens/s. Supply
`--model`, `--model-name`, and `--configmap` as for ordinary benchmark Jobs;
`--batch-size` means documents per request for this task.

```bash
flexinfer-bench --backend llama-cpp --task embeddings \
  --model ggml-org/embeddinggemma-2-GGUF --model-name embeddinggemma-2 \
  --configmap embeddinggemma-2-benchmark --batch-size 4 \
  --embedding-input 'task: search result | query: local inference cache' \
  --embedding-input 'title: none | text: Cache model files near the runtime.'
```

## Results: 2026-10-12

| Lane | Minimum cosine to FP32 | Input tokens/s | Batch p95 | Result |
| --- | ---: | ---: | ---: | --- |
| CPU BF16 | 0.999991741 | 142.1 | 95.125s | PASS |
| CPU Q8_0 | 0.999735590 | 143.5 | 94.094s | PASS |
| gfx906 BF16 | 0.999994790 | 1877.2 | 6.539s | PASS |
| gfx906 Q8_0 | 0.999676559 | 1996.5 | 6.232s | PASS |

All rows cover the same 19 inputs and 13,939 server-accounted tokens. The last
batch contains the near-4K/8K inputs and dominates duration; these rates are not
short-document saturation benchmarks. The final GPU repeat used warmed kernel/cache
state; the first run measured 646.9 BF16 / 1,494.8 Q8 input tokens/s. p95 uses
nearest rank so the longest batch is included. BF16's GPU model/compute allocation was about 2,373 MiB (261 MiB
weights, 2,112 MiB compute); the deployment reserves a 3,000 MB admission estimate.

The isolated **FlexInfer runtime** loaded BGE (25/25 GPU layers), GTE (29/29), and
EmbeddingGemma (25/25). BGE/GTE process IDs remained stable across BF16 unload and
Q8 load. Twenty interleaved neighbor requests passed across both formats, with
correct 1,024/1,536-dimensional normalized outputs. All three were Ready at each
post-qualification snapshot. EmbeddingGemma loaded in 5.5s BF16 / 5.5s Q8 with
local files. The test used a separate manager and did not roll the fleet runtime.

Run from the repository root, using the usual explicit K3s kubeconfig:

```bash
kubectl apply -f deploy/debug/embeddinggemma-2-cpu-qualification.yaml
kubectl -n flexinfer-system wait --for=condition=complete \
  job/embeddinggemma-2-cpu-qualification --timeout=30m
kubectl -n flexinfer-system create configmap embeddinggemma-2-qualification \
  --from-file=qualify.py=scripts/qualify-embeddinggemma2.py
kubectl apply -f deploy/debug/embeddinggemma-2-gpu-qualification.yaml
kubectl -n flexinfer-system wait --for=condition=complete \
  job/embeddinggemma-2-gpu-qualification --timeout=15m
```

Both Jobs write under `llm-models-nfs:/embeddinggemma-2-validation-20261012`.
CPU: `reference.json`, `cpu-BF16.json`, `cpu-Q8_0.json`, `cpu-summary.json`, and
server logs. GPU: `runtime/runtime-summary.json`, per-format reports, and
model-attributed full-offload logs. Recreate these debug Jobs and the ConfigMap
before a rerun; delete the parent Jobs after harvesting evidence. Debug Jobs
have deadlines, resource limits, and TTL cleanup.

The GPU Job mounts existing BGE/GTE node caches read-only, exposes an isolated
localhost runtime API, and shares `/dev/kfd`/`/dev/dri` using the fleet's privileged
ROCm device mechanism. It claims no device-plugin slot and needs free HBM2 for
all three models. Its process group is terminated after the test. Host networking
with node DNS was required by this node's canary download/network path.

Source checks: Python qualification tests, the gfx906 environment regression,
Go unit tests, lint, and runtime publish-contract checks. The sandbox gate hit
`image_pull_backoff`; bounded local validation passed instead. Full evidence is
indexed in `.loom/embeddinggemma-2-qualification-2026-10-12.json` and the runtime
promotion matrix. The model recipes remain outside `deploy/models/kustomization.yaml`
until the fleet runtime upgrade is planned with its other active workloads.
The qualified overlay preserves the preceding manager binary. A fleet upgrade
must incorporate the patched Go manager and current shared-runtime changes;
it is not a security certificate for the historical base image.

CI security gates require Go 1.26.9, x/net 0.60.0, and OpenTelemetry 1.45.0.
The source/build pins carry these fixes. Tracing uses standard environment
configuration and does not call the exporter URL option changed in 1.45.0.
