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

2026-05-18 Triton dump-dir compatibility follow-up:

- Live validation with the MR !421 image got past `default_cache_dir` and again
  reached ROCm attention backend selection.
- Startup then failed on the adjacent missing Triton cache API,
  `triton.runtime.cache.default_dump_dir`.
- MR !422 extends the same site-packages compatibility hook to provide
  `default_dump_dir`, preferring `TRITON_DUMP_DIR` and otherwise falling back to
  `~/.triton/dump`.
- The rebuilt image was published and pinned as
  `registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:8350619f10e31e5172fd94e8686e9b185292d6182c911711d4e026e4acce23d6`.

2026-05-18 Triton override-dir and cache-refresh follow-up:

- Live validation with the MR !422 image got past `default_dump_dir`; the
  remaining import from vLLM 0.7.3's custom Triton cache manager was
  `triton.runtime.cache.default_override_dir`.
- The same validation also proved the shared-GPU cache-refresh guard was still
  incomplete: `ensureCache` could transiently report not-ready and force
  `desiredReplicas=0` while an active shared model was still pulling/loading.
- MR !424 adds `default_override_dir` to the site-packages compatibility hook
  and preserves active shared-GPU `Pending`/`Loading` models during cache
  refresh if they remain inside their cold-start window.
- The rebuilt image was published and pinned as
  `registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:021d31f322b2ff789a0d7bfa1f79c713b8a1cbcf3498e2bc58ddb0a5fe26386d`.

2026-05-18 PyTorch ROCm memory-info follow-up:

- Live validation with the MR !424 image and MR !426 controller guard kept the
  active pod running through cache refresh and got past all Triton compatibility
  imports.
- Startup then failed in vLLM's memory snapshot because
  `torch.cuda.mem_get_info()` returned HIP `invalid argument` on gfx906.
- MR !427 adds a guarded PyTorch ROCm compatibility hook that falls back to
  device properties plus PyTorch allocation counters when `mem_get_info` fails.
- The rebuilt image was published and pinned as
  `registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:84f0ae2bb1ea46163885aad55181540bee9995b4b4b0c656f3943b7580e07e1e`.

2026-05-19 runtime manager lock follow-up:

- MR !428 switched the canary source to `HF://Qwen/Qwen2.5-1.5B-Instruct`
  because vLLM 0.7.3 does not have a native `Qwen3ForCausalLM`
  implementation and falls back to the slower/failed Transformers path.
- The Qwen2.5 smoke still timed out after 900 seconds. Live inspection showed
  the standalone vLLM Deployment was scaled to zero while the persistent
  `flexinfer-runtime-gfx906` pod was `0/1 Running`: `/healthz` returned 200,
  but `/readyz`, `/api/v1/status`, model health checks, and model load calls
  timed out.
- Controller logs showed repeated `gonzalomo-fluxpony-imagegen` load attempts
  and `qwen3-1p7b-tools-radeonvii` unload/load health checks against the same
  runtime. The runtime manager held its state lock while waiting for backend
  shutdown and also allowed unload and monitor paths to race on `cmd.Wait()`;
  a stubborn backend could starve runtime status/ready endpoints and keep the
  controller in a duplicate load loop.
- This follow-up changes the runtime manager so lifecycle operations are
  serialized separately from state reads, shutdown waits happen without holding
  the state lock, and the monitor goroutine owns `cmd.Wait()`. Regression test:
  `TestUnloadDoesNotBlockStatusWhileWaitingForBackendExit`.

2026-05-19 CK flash-attention follow-up:

- Retrying the Qwen2.5 smoke after the runtime recovered proved the image-store
  move is sufficient for the current pinned image: the 10.4 GB vLLM image pull
  completed in 7m25s and `cblevins-radeonvii` stayed `DiskPressure=False`.
- The canary reached vLLM engine initialization and started loading
  `Qwen/Qwen2.5-1.5B-Instruct`, then crashed with vLLM's gfx906 warning that
  Qwen2 sliding-window attention is not supported by ROCm Triton flash
  attention and to use CK flash attention by setting
  `VLLM_USE_TRITON_FLASH_ATTN=0`.
- This follow-up adds that env var to `GPUProfile/gfx906` so controller-created
  vLLM deployments inherit the profile-level CK flash attention fallback.

2026-05-19 sliding-window follow-up:

- A live retry showed the pod received `VLLM_USE_TRITON_FLASH_ATTN=0`, but
  vLLM 0.7.3 still failed while initializing Qwen2.5's sliding-window attention
  path on gfx906.
- Image inspection showed vLLM already reads the env var; the remaining unsafe
  path is the model's SWA config. FlexInfer now exposes
  `config.disableSlidingWindow` as vLLM's `--disable-sliding-window` flag, and
  the Radeon VII canary sets it until this backend has positive HTTP 200
  evidence.

2026-05-19 OPT canary pivot:

- The merged `disableSlidingWindow` path rendered correctly, but live validation
  showed vLLM 0.7.3 rejects `--disable-sliding-window` for Qwen2.5 because the
  model also has `rope_scaling`.
- The canary now uses `HF://facebook/opt-125m` / `opt-125m-vllm`. This keeps the
  validation target narrow: prove the gfx906 standalone vLLM image can import a
  supported text-generation architecture and return HTTP 200 before revisiting
  Qwen-family SWA/rope support.
