---
title: Caching
description: Keep models warm (RAM) or warm-ish (PVC) to reduce cold start time.
---

# Caching

FlexInfer supports caching at two layers:

- **Artifact caching**: model weights are present locally (disk or RAM)
- **Runtime warmup**: backend is already running and ready to serve

## v1alpha2 cache strategies

`Model.spec.cache.strategy`:

- `Memory`: fastest reloads; uses RAM (best for homelabs)
- `Local`: a hostPath directory on the GPU node (e.g. NVMe), under
  `<hostPath>/<namespace>/<model-name>/`. Fast and cheap, but the weights are
  pinned to one node and can only be cleared from that node.
- `SharedPVC`: persistent storage shared across nodes (slower than RAM, but durable)
- `None`: always download/prepare on demand (slowest)

If `spec.cache` is omitted, FlexInfer infers a strategy based on GPU sharing.

## v1alpha2 SharedPVC provisioning

For backends that mount `/models`:

- If `spec.cache.strategy: SharedPVC` and `spec.cache.pvcName` is omitted, the controller auto-creates a PVC named `<model>-cache`.
- Defaults:
  - `spec.cache.storageClass`: `longhorn`
  - `spec.cache.size`: `50Gi`
- `status.cache.ready` reflects whether the model artifact has been verified/prefetched onto the mounted volume.
- `status.cache.jobName` / `status.cache.jobPhase` show the Job responsible for prefetch/verification.

If you use `spec.source: pvc://<pvc-name>/<path>`, FlexInfer mounts that PVC directly at `/models` and uses the `/<path>` subdirectory as the model path.

## HuggingFace prefetch

For `HF://...` sources with `SharedPVC`, the controller runs a one-shot prefetch Job that materializes the repo under:

- `/models/<model-name>/`

This makes cold starts deterministic and keeps `status.cache.ready` meaningful.

FlexInfer also sets HuggingFace cache env vars to keep secondary caches under the `/models` volume:

- `HF_HOME=/models/.cache/huggingface`
- `HF_HUB_CACHE=/models/.cache/huggingface/hub`

## Prefetch policy

By default the controller stages weights as soon as a `Model` reconciles, whether
or not anything has asked for it. That makes cold starts cheap and costs cache
storage for models that may never be used. `spec.cache.prefetch` lets you make
that trade per model:

```yaml
spec:
  cache:
    strategy: SharedPVC
    prefetch:
      policy: OnDemand    # Eager (default) | OnDemand | Disabled
      priority: 80        # 0-100; higher is evicted last
      idleTTL: 72h        # mark reclaimable after this long idle
```

- `Eager` — stage on reconcile. The behavior before this field existed, and the
  default, so omitting `prefetch` changes nothing.
- `OnDemand` — defer staging until something wants the model: a request has
  arrived within its `idleTimeout`, `minReplicas` is above zero, a warm policy
  holds it up, or a prefetch is requested explicitly. The first request after an
  idle period pays the download.
- `Disabled` — never stage automatically. Only an explicit request populates the
  cache. Use for models parked behind a slow or metered link.

A deferred model reports `status.cache.prefetchDeferred: true` with the reason in
`status.cache.message`, and its `Cached` condition carries reason
`PrefetchDeferred`. Activation stays gated while deferred, exactly as it is while
a download runs.

`idleTTL` never deletes anything on its own. It only marks a staged cache as a
reclaim candidate for `flexinfer cache capacity`; reclaiming is always explicit.

### Requesting a prefetch

Set `flexinfer.ai/prefetch-requested-at` to an RFC3339 timestamp to stage a
model's weights immediately, whatever its policy says:

```bash
flexinfer cache prefetch qwen38-27b-autoround-workhorse
```

The controller honors a request strictly newer than
`status.cache.prefetchRequestedAt`, so re-applying a manifest does not replay it.
A request also clears a **failed** staging Job so it retries; a Job that is still
running is left alone rather than restarted.

This stages weights only. It does not scale the model up or claim a GPU.

## Cache commands

```bash
# Cache state for every model: strategy, policy, warm/cold, staging message
flexinfer cache status

# Stage a model's weights now, without starting its GPU pod
flexinfer cache prefetch MODEL [--wait]

# Block until the weights are staged (non-zero exit on failure or timeout)
flexinfer cache wait MODEL --timeout 30m

# PVC pressure, which models share a volume, and reclaim candidates
flexinfer cache capacity

# Reclaim a model's cache storage (destructive; re-downloads on next activation)
flexinfer cache evict MODEL --confirm
```

`evict` deletes the PVC backing the cache and is deliberately narrow. It refuses
unless the PVC carries a controller owner reference back to that one `Model`, so
a PVC named in `spec.cache.pvcName` or shared with another model is never
deleted. `Local` and `Memory` caches live on a node's disk and cannot be cleared
this way at all — that needs a privileged pod on the node.

## Flash-loader

Flash-loader preloads model artifacts from the mounted cache volume into
`/dev/shm` before the backend container starts. This is useful for ROCm/gfx1100
deployments where repeated model swaps should avoid slow PVC reads.

Global defaults are configured on the controller through Helm:

```yaml
controller:
  runtime:
    shmSizeLimit: 8Gi
    flashLoader:
      enabled: true
      image: registry.harbor.lan/flexinfer/flexinfer-flash-loader:latest
      concurrency: 4
      tmpfsSizeLimit: 16Gi
```

The controller resolves flash-loader settings in this order:

1. Helm-rendered controller env defaults.
2. Matching v1alpha1 `ModelCache.spec.flashLoader`.
3. v1alpha2 `Model.spec.cache.flashLoader`.

The v1alpha2 model cache setting wins when multiple layers are present. Shared
models using `cache.strategy: Local` are auto-enabled for flash-loader unless
`spec.cache.flashLoader.enabled: false` is set.

Shared models use a persistent host path at
`/dev/shm/flexinfer/<namespace>/<model-name>` so a warm cache can survive pod
replacement on the same node. Non-shared models use an ephemeral `emptyDir`
tmpfs, optionally capped by `tmpfsSizeLimit`. On a persistent destination,
flash-loader reuses files whose sizes match the source and checks free space
only for missing or size-mismatched files. Its startup log reports the planned
copy and reuse file counts and bytes.

## v1alpha1 `ModelCache`

`ModelCache` lets you pre-download (and optionally pre-warm) a model artifact.

Common pattern:

- Create a `ModelCache` (SharedPVC or Memory)
- Reference it from `ModelDeployment.spec.modelCacheRef`

Examples:

- `services/flexinfer/examples/ram-cached-models.yaml`
- `docs/DEVELOPMENT.md` (MLC-LLM caching example)

## Troubleshooting cache issues

- Inspect `ModelCache` status:
  ```bash
  kubectl -n flexinfer-system get modelcache -o wide
  kubectl -n flexinfer-system describe modelcache <name>
  ```
- If a v1alpha1 `ModelCache` stays in `Provisioning`, check the
  `DownloadJobScheduled` condition. `Reason=DownloadJobUnschedulable` means the
  downloader pod is blocked by scheduling constraints such as a cordoned node,
  taints, or a node selector that no Ready node can satisfy.
- Check downloader logs (v1alpha1):
  ```bash
  kubectl -n flexinfer-system logs -l job-name=<cache-name>-downloader
  ```
