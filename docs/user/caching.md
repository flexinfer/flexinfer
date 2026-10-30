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

### Spec edits and a staged `Local` cache

Once an `HF://` source is staged into a `Local` cache, `status.cache.stagedKey`
records what the stage put on disk and where: a digest of `spec.source`, the
resolved download options (`ggufFile`, a relative `mmproj`, `hfAllowPatterns`,
`hfIgnorePatterns`, `hfRevision`), `vaeRepo`, the transformer side artifact
(`transformerRepo`, `transformerFile`, `transformerRevision`), the cache
directory (`spec.cache.hostPath`) and `spec.nodeSelector`. A spec edit that
leaves the key unchanged, such as a backend flag, `contextSize`, resources or
labels, keeps the cache ready across the generation bump. No stage Job runs,
and a runtime-served lane stays on its runtime without a reload. An edit that
changes the key stages again before the lane is served from the new files.

A status written before `stagedKey` existed is read by generation, as before;
the controller records the key on its next pass over a ready cache.

### Transformer side artifact (diffusers)

A diffusers Model can take its transformer from a second Hugging Face repo, for
example a GGUF quant of the official checkpoint: set `transformerRepo`,
`transformerFile` and (recommended, as a full commit SHA) `transformerRevision`.
The `HF://` stage, Local or SharedPVC, fetches that one file into
`<model cache dir>/.transformer/<transformerFile>` after the snapshot, and the
diffusers server loads it from there.

The cache marker vouches for the snapshot only, so the file is checked by
presence: a re-run with the file in place downloads nothing, and a changed
`transformerFile` (switching quants) fetches the new file alone and removes the
old one, without downloading the snapshot again. A stage whose download ends
without the file fails instead of writing a ready cache.

Limits: a SharedPVC prefetch Job is recreated only when `spec.source` changes,
so a `transformerFile` change on a SharedPVC cache needs that Job deleted by
hand. The SharedPVC copy Job (`pvc://` caches) does not copy dot-directories,
so there the server falls back to downloading the file from `transformerRepo`
at load time.

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

For `HF://...` sources with `SharedPVC`, the controller runs a one-shot `<model>-cache-prefetch` Job that materializes the repo under:

- `/models/<model-name>/`

This makes cold starts deterministic and keeps `status.cache.ready` meaningful.

FlexInfer also sets HuggingFace cache env vars to keep secondary caches under the `/models` volume:

- `HF_HOME=/models/.cache/huggingface`
- `HF_HUB_CACHE=/models/.cache/huggingface/hub`

The Job finishes by writing `/models/<model-name>/.flexinfer_cached_<sha256>`,
keyed like the Local stage marker below: `spec.source` plus the resolved allow
patterns, `hfIgnorePatterns` and `hfRevision`. A Job that finds the marker for
its options exits without downloading. The Job never expires and carries its
marker in the `flexinfer.ai/cache-marker` annotation. When the spec resolves to
a different marker (a `ggufFile` rename, an `mmproj` or pattern change, an
`hfRevision` bump, a new `spec.source`), the controller replaces the Job and the
cache reports `re-fetching shared cache: source or download options changed`
until the new Job succeeds. A Job still downloading for the old options is
deleted in the foreground first, so two downloads never share the directory.

Unlike the Local stage, the directory is not wiped. `snapshot_download` skips
every file whose download metadata or sha256 already matches the repo, so a
re-fetch transfers only what changed. Files that only the old options asked for
stay on the PVC; delete them by hand to reclaim the space. The Job refuses to
write its marker when a `ggufFile` or relative `mmproj` is missing after the
download.

With `spec.quantize`, the quantized output was built from the old files. Before
replacing the prefetch Job the controller deletes the `<model>-quantize` Job and
clears `status.cache.quantization`, so the next successful prefetch quantizes
again.

MLC sources are cloned with git, which ignores the download options; they keep
the bare `.flexinfer_cached` marker.

Caches prefetched before October 2026 hold a bare `.flexinfer_cached` marker.
Prefetch Jobs from before the upgrade still exist and carry their download
options in their env, so the controller rebuilds their marker from it and leaves
a Job whose options are current alone: the controller roll starts no Job. The
first new Job for a lane adopts the bare marker without downloading when the
files still satisfy the current options:

- the directory holds at least one file;
- every `ggufFile` and relative `mmproj` is present and non-empty;
- every allow pattern matches a file on disk or a file huggingface_hub recorded
  downloading;
- with `hfRevision` set, at least one huggingface_hub download record
  (`.cache/huggingface/download/<file>.metadata`) exists and every one names
  that commit.

The download records stand in for the directory listing because a quantizer may
have deleted the downloaded weights (AWQ does) and written its own output
beside them. A Job that replaced one built for another `spec.source` never
adopts the bare marker, since the marker does not say which repo it was written
for. The first new Job to find the bare marker consumes it either way. A cache
it does not adopt falls through to the incremental download, and the Job log
names the check that failed.

## Local HuggingFace staging

For `HF://...` sources with `strategy: Local` and a `nodeSelector`, a one-shot
`<model>-cache-stage` Job downloads the repo into
`<hostPath>/<namespace>/<model-name>/` on the node. It runs for a new Model, a
generation change, or a cache that is not ready. A cache already staged for the
current generation reports `local cache previously staged` and starts no Job.

The Job finishes by writing a marker, `.flexinfer_cached_<sha256>`, whose hash
covers `spec.source` and the resolved download options: the allow patterns
(`ggufFile`, a relative `mmproj`, `hfAllowPatterns`), `hfIgnorePatterns`, and
`hfRevision`. A Job that finds the marker for its options exits without
downloading. Changing any of those options names a marker the node does not
hold, so the Job wipes the directory (keeping `.cache/`) and downloads again. A
`ggufFile` renamed upstream or an `hfRevision` bump therefore re-stages on its
own. The Job carries the marker in its `flexinfer.ai/cache-marker` annotation,
and a Job left over from other options is deleted rather than read as success.

The marker does not see upstream changes under an unpinned revision, or
`vaeRepo` (whose directory is checked separately). Pin `hfRevision` when an
upstream update should re-stage.

Caches staged before October 2026 carry a marker hashed from `spec.source`
alone. The first stage Job to find one adopts it without downloading when the
files on disk still satisfy the current options:

- the directory holds at least one file;
- every `ggufFile` and relative `mmproj` is present and non-empty;
- every allow pattern matches a staged file;
- with `hfRevision` set, huggingface_hub's download metadata
  (`.cache/huggingface/download/<file>.metadata`) records that commit for every
  staged file.

Adoption renames the old marker, so it happens once. A cache that fails a check
is wiped and downloaded, and the Job log names the check that failed.

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

## Compilation cache

`spec.cache.compilationCache` keeps GPU kernel compilation caches on a node
hostPath, so a restarted lane loads its kernels from disk instead of
recompiling them. It is on by default for `gpu.shared` AMD Models and can be set
explicitly for any Model whose backend supports it (vLLM, vLLM-Omni, diffusers,
ComfyUI). A dedicated pod mounts the directory at `/cache/compile` and gets:

| Variable | Path | Backends |
|---|---|---|
| `TRITON_CACHE_DIR` | `/cache/compile/triton` | all four |
| `TORCHINDUCTOR_CACHE_DIR` | `/cache/compile/inductor` | all four |
| `MIOPEN_CUSTOM_CACHE_DIR`, `MIOPEN_USER_DB_PATH` | `/cache/compile/miopen`, `…/miopen/user.db` | all four |
| `TORCH_HOME` | `/cache/compile/torch` | all four |
| `VLLM_CACHE_ROOT` | `/cache/compile/vllm` | vLLM, vLLM-Omni |
| `XDG_CACHE_HOME` | `/cache/compile/xdg` (comgr's `llvmcache-*`) | vLLM, vLLM-Omni |

These names are reserved: `spec.config.env` cannot override them.

### Scope: one cache per Model or per image

`compilationCache.scope` chooses which lanes share a directory under
`compilationCache.hostPath` (default `/var/lib/flexinfer/compile-cache`):

- `Model` (the default): `<hostPath>/<namespace>/<model-name>/`, one cache per
  lane. Every canary or probe of an image compiles from scratch and keeps a
  full copy.
- `Image`: `<hostPath>/shared/<gpuArch>/<digest12>/`, where `<digest12>` is the
  first 12 hex digits of the serving image's sha256 digest (the same digits
  as Harbor's `pinned-<digest12>` tags). Every lane on that node, image digest
  and GPU arch uses one directory.

```yaml
spec:
  image: registry.harbor.lan/flexinfer/runtime@sha256:b06b06fe1c02…
  cache:
    compilationCache:
      enabled: true
      scope: Image
      # Optional, once: copy a lane's earlier per-Model cache in.
      seedFrom: /var/lib/flexinfer/compile-cache-qwen38-27b-v032-s4-5930k/flexinfer-system/qwen38-27b-autoround-workhorse
```

Sharing is safe because none of these caches is keyed by the Model. Each tool
keys its own entries:

- Triton keys each kernel by source, compile options, Triton version and GPU
  target.
- vLLM's torch.compile output lives in `vllm/torch_compile_cache/<hash>/`, where
  the hash covers the vLLM config, code and compiler. Different models or
  envelopes therefore get different subtrees on their own.
- The ROCm segmented-attention autotune tables
  (`vllm/rocm_segmented_attention/<hash>.json`) are keyed by GPU, toolchain,
  kernel source and shapes.
- MIOpen names its databases after the GPU and its CU count.

A fresh lane on an image another lane already warmed therefore skips the
segmented autotune (`missing=0`) and the first-use Triton compiles. The digest
scopes the directory rather than the tag: a digest pins the toolchain, and a
retired image's directory can be deleted as a unit.

The image scope needs a digest-pinned image (`repo@sha256:…`, from `spec.image`
or `spec.imageDigest`) and a known GPU arch. Without either, the lane falls
back to `Model` and the controller logs why. Only lanes with the same
`hostPath` share a directory; keep the default to share with the runtime pod.
Switching scope changes the pod template, so the lane restarts once and boots
from the new directory. The old per-Model directory is left in place, which
keeps a rollback warm.

Every consumer must see the directory at the same in-container path,
`/cache/compile`. The Triton release in the serving images (3.8) writes
absolute file paths into its `__grp__*.json` manifests and treats a missing
path as a miss. A directory mounted at two paths would make each side
recompile what the other wrote.

#### Seeding

`compilationCache.seedFrom` names a node directory, normally the lane's earlier
per-Model cache, to copy into an image-scoped cache that has not been seeded
yet. A `compile-cache-prepare` init container (serving image, `/bin/sh`) does
it once per directory:

1. It copies the seed into a staging directory on the cache mount.
2. It hard-links the staged files into place without replacing any existing
   entry. A hard link publishes a file atomically, so a lane already using the
   directory never reads a half-copied kernel.
3. It writes `.flexinfer-seeded`.

A missing or empty seed is skipped, and a failed copy boots the lane with the
cache as it is. Seeding never blocks a lane. `seedFrom` can be dropped after
the first boot. It is mounted read-only, and the source is never modified.
Without `seedFrom`, the first lane on an image pays the compiles once, and
every later lane on that image reuses them.

### Runtime pod

Runtime-managed loads with `scope: Image` can share the same directory. Set
`sharedCompileCache: true` on the runtime profile:

```yaml
runtime:
  profiles:
    - name: gfx1100
      image: registry.harbor.lan/flexinfer/runtime@sha256:…   # must be a digest
      gpuArch: gfx1100
      sharedCompileCache: true
```

The DaemonSet then mounts `/var/lib/flexinfer/compile-cache/shared/<gpuArch>/<digest12>`
of the profile's own image at `/cache/compile`, which is the host directory
and path a dedicated pod on the same digest uses. It also sets
`FLEXINFER_SHARED_COMPILE_CACHE_DIR=/cache/compile`. The load payload carries
the per-Model cache env plus `FLEXINFER_COMPILE_CACHE_SCOPE=Image`. A runtime
with the mount re-points the cache env to `/cache/compile`. A runtime without
the mount (the profile setting is off, or an older runtime image) keeps the
per-Model directory. Seeding is not available on this path, because the
runtime pod cannot mount new host directories at load time. A dedicated lane
on the same digest seeds the directory for both paths.

The runtime pod's own process env (the `…/compile-cache/runtime/*` paths in
`deploy/system/values-k3s.yaml`) is unchanged. It still covers loads that
request no compile cache.

### Concurrent writers

Two processes can write one shared directory: the runtime pod and a dedicated
lane on the same image, or two lanes back to back. What each tool does:

| Tool | Writes | Concurrent writers |
|---|---|---|
| Triton (`triton/`) | temp dir `tmp.pid_<pid>_<uuid>` then `os.replace`; `__grp__` manifest last | safe; a manifest whose files are missing reads as a miss |
| TorchInductor (`inductor/`) | `write_atomic` (temp `.<pid>.<tid>.tmp`, rename); locks under `locks/` | safe |
| vLLM AOT compile (`vllm/torch_compile_cache/torch_aot_compile/`) | `<path>.<pid>.tmp` then `os.replace`; a load failure recompiles | safe |
| vLLM standalone artifacts | `write_atomic` (binary format) | safe |
| vLLM compile index (`vllm_compile_cache.py`) | plain `open(..., "w")`; read with `ast.literal_eval`, no fallback | **unsafe**, see below |
| Segmented-attention autotune (`vllm/rocm_segmented_attention/`) | `<hash>.json` guarded by `<hash>.json.lock` | safe |
| vLLM model info (`vllm/modelinfos/`) | plain write; a failed read recomputes | safe |
| MIOpen (`miopen/`) | file-locked perf DB, SQLite kernel cache | safe |
| comgr (`xdg/comgr/`) | LLVM cache: temp file then rename | safe |

The vLLM compile index is shared only by processes with the same vLLM config
hash. Two such engines booting together on one node is rare: shared-GPU
election runs one lane per card at a time, and a GPU Deployment never surges.
If it does happen, a boot that reads a half-written index fails once, and its
restart reads the finished file. The case that does not heal is a write cut
short, for example when a pod is killed mid-save. Every later boot of that
hash then crashes. That was true of a per-Model directory too, but a shared
directory spreads it to every lane with the same config. The guard: before
the engine starts, the `compile-cache-prepare` init container (dedicated path)
and the runtime manager (runtime path) rename aside any
`vllm_compile_cache.py` that does not end in `}`. vLLM writes the index as a
pprint'ed dict, so an index that ends in `}` is complete. That config then
recompiles once.

Nothing reclaims compile caches yet, in either scope.

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
