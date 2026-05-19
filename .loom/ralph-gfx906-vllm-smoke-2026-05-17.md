# RALPH: gfx906 vLLM Canary Smoke

Date: 2026-05-17
Branch: `codex/gfx906-vllm-smoke`

## Goal

Run the next feature-gap slice after the GPUProfile vLLM defaults work: prove
whether `qwen3-1.7b-vllm-radeonvii` can now smoke on the persistent gfx906
runtime, and leave the roadmap matrix with a clear promote/block decision.

## Acceptance

- The Radeon VII vLLM canary is safe to reconcile from `deploy/models`.
- Explicit canary traffic can exercise the model without stealing the bare
  `qwen3-1.7b` alias from the CPU fallback tool router.
- The validation matrix records the live outcome and the next blocker.
- The cluster is restored to the known-good `qwen3-1p7b-tools-radeonvii`
  fallback after the experiment.

## Slice

The existing canary resource name used a dot:
`qwen3-1.7b-vllm-radeonvii`. That is legal for the Model CR but unsafe for
child Services and runtime-managed Kubernetes resources. It also used
`SharedPVC`, so the runtime-managed load path did not get the same concrete
local cache layout used by the proven GGUF fallback.

This slice renames the resource to `qwen3-1p7b-vllm-radeonvii`, switches it to
`cache.strategy: Local`, and raises priority to `130`. The public served model
name and aliases stay dotted and canary-scoped: `qwen3-1.7b-vllm`,
`qwen3-1.7b-vllm-vii`, and `vllm-canary`.

The live smoke then exposed the next controller/runtime gap, so the slice also
patches the controller to send load/unload requests to a Running runtime pod
with a PodIP even when Kubernetes Ready is false. Pending runtime pods still
block load requests until they have an address.

## Evidence

- `kubectl kustomize deploy/models` rendered the renamed model with
  `strategy: Local` and `priority: 130`.
- `kubectl apply --dry-run=server -k deploy/models` accepted the manifest and
  would create `model.ai.flexinfer/qwen3-1p7b-vllm-radeonvii`.
- Live hot apply with Flux suspended staged the HF model into local cache:
  `artifact staged to local cache`, then `local cache previously staged`.
- A canary proxy request moved the canary into shared-group `Active` and
  preempted `qwen3-1p7b-tools-radeonvii`, proving priority/demand selection.

Evidence directory: `/tmp/flexinfer-gfx906-vllm-fixed-smoke-20260517T193341Z`.

## Result

The canary did not reach an HTTP 200 smoke. Once the CPU fallback was
preempted, the persistent runtime remained API-alive but pod-not-Ready. The
controller repeatedly logged:

```text
Runtime pod exists but not ready, waiting for it to start
```

The runtime `/healthz` endpoint returned OK, but `/readyz` hung after the
aborted vLLM load, so the controller never issued a fresh load request for the
active cached model. The code change in this MR makes a PodIP, not pod Ready,
the gate for using the management API.

## Recovery

The live test model was deleted, Flux `flexinfer-models` was resumed, and the
stuck `flexinfer-runtime-gfx906` pod was recycled. The fallback recovered:

```text
qwen3-1p7b-tools-radeonvii Ready ready=True
```

Direct service smoke returned:

```json
{"choices":[{"message":{"content":"Blue"}}]}
```

## Decision

Keep gfx906 vLLM at `experimental` and mark the `qwen3-1p7b-vllm-radeonvii`
smoke as `block`, not `pending`: the canary prerequisites and controller load
gate are fixed here, but support promotion still needs a post-merge live vLLM
HTTP 200.

## Next Slice

Only consider `deploy/gpuprofiles/gfx906.yaml` `vllm.support` promotion after a
real HTTP 200 vLLM response. Before rerunning the canary command, fix the
Radeon VII image-store capacity issue so the standalone
`registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:beadf394fc81c031799799f5d965664e419e5f3ffb4c5873a9d7677f0e1e06b8`
image can be pulled without triggering DiskPressure.

2026-05-18 follow-up:

- `deploy/gpuprofiles/gfx906.yaml` now pins the vLLM standalone profile image to
  the same digest used by the canary evidence row.
- `deploy/system/values-k3s.yaml` now sets `vllm.gfx906Image` to that digest.
- Live validation proved the standalone vLLM image cannot currently be held
  warm on `cblevins-radeonvii`: the prewarm DaemonSet repeatedly evicted during
  pull with ephemeral-storage pressure and flipped the node to
  `DiskPressure=True`.
- The unsafe prewarm profile was removed. The next slice should free or move the
  node image store, then re-attempt the prewarm before activating the canary.

2026-05-18 imagefs/auth follow-up:

- The Radeon VII k3s containerd image store was moved off root LVM and onto the
  NVMe-backed `/mnt/nvme/longhorn/k3s-containerd/containerd` bind mount.
- Post-move validation showed `cblevins-radeonvii` `Ready=True` and
  `DiskPressure=False`; the gfx906 runtime DaemonSet recovered after recycling
  the stale post-restart pod.
- Retrying the vLLM canary reached the pinned standalone image pull path without
  storage pressure. The local cache stage completed, and cleanup returned
  `qwen3-1p7b-vllm-radeonvii` to `Idle` with its Deployment at zero replicas.
- The current blocker is Harbor authorization for
  `registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:beadf394fc81c031799799f5d965664e419e5f3ffb4c5873a9d7677f0e1e06b8`.
  Both `harbor-creds` and a temporary dockerconfig built from
  `harbor-oci-creds` returned `401 Unauthorized`.
- This slice adds controller/Helm support for default model pod
  `imagePullSecrets`, but the canary still needs Harbor project permissions or
  a republished image in a project readable by the existing pull secret before
  another smoke can produce an HTTP 200.

2026-05-18 tokenizer compatibility follow-up:

- Harbor pull access and the NVMe-backed image store were enough to start the
  standalone vLLM canary image on Radeon VII.
- The first container startup failed before readiness because vLLM 0.7.3 called
  `Qwen2Tokenizer.all_special_tokens_extended`, but the installed
  `transformers` 5.8.0 tokenizer no longer exposes that attribute.
- MR !420 patches the gfx906 vLLM image with a small site-packages compatibility
  hook and pins the profile image to
  `registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:0eebd5a70e184d31c706457ef4b7f393b10d4193a7b728dd5112a17d3457797f`.
- The controller also preserves an already-active Pending/Loading shared-GPU
  leader while cache readiness is revalidated during the cold-start window, so
  a transient cache condition cannot scale down the canary mid-activation.

2026-05-18 Triton compatibility follow-up:

- Live validation with the MR !420 image got past the tokenizer blocker and
  initialized the vLLM engine far enough to select the ROCm attention backend.
- Startup then failed because vLLM 0.7.3 imports
  `triton.runtime.cache.default_cache_dir`, which is no longer exported by the
  Triton package in the current base image.
- The next image rebuild extends the same site-packages compatibility hook to
  provide `default_cache_dir`, preferring `TRITON_CACHE_DIR` and otherwise
  falling back to `~/.triton/cache`.
- The rebuilt image was published and pinned as
  `registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:bec2ad7d136ce9c2add97c692901dec8e10a9240ecdc2960ed5b028cd18e24e1`.
