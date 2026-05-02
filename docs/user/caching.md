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
      image: registry.harbor.lan/flexinfer/flash-loader:latest
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
tmpfs, optionally capped by `tmpfsSizeLimit`.

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
