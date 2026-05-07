# GPUProfile Contract Follow-ups

Status: open as of 2026-05-06 (commit landing `feat/gpuprofile-contract-slice`).

This MR introduced `backend.ResolveBackendImage(b, profile, vendor, arch)` and
migrated the two raw `b.Image(vendor, arch)` call sites in `controllers/` to
prefer the GPUProfile-declared image override before falling back to the
in-code arch rules. Tests in `backend/gpu_compat_test.go` cover the four
precedence states (profile wins, explicit nil, profile-without-entry,
profile-with-empty-image).

The next agent should keep the slices small and shippable. Pick the highest
priority item, ship it, then update this doc.

## Priority 1 — Push GPUProfile-first into env-vars on the runtime/Job paths — COMPLETED

Shipped in `feat/gpuprofile-env-helper` (slice 2).

`backend.ResolveBackendROCmEnv(profile, vendor, arch)` (and the underlying
`backend.EnvFromProfile(profile)` accessor) live next to `ResolveBackendImage`
in `backend/gpu_compat.go`. Precedence: `profile.Env` (when non-empty) wins;
otherwise the helper falls through to `backend.ROCmEnvVars(arch)` for AMD
vendors (and returns nil for other vendors so ROCm env never leaks into NVIDIA
or CPU pods).

Migrated callers:

- `controllers/model_deployment.go` now overlays `profile.Env` on top of the
  ROCmEnvVars baseline that `b.Env(spec)` already injects. The merge is a
  no-op when the profile declares no env entries, which makes the
  GPUProfile-first contract explicit at the call site.
- `pkg/runtime/payload.go::applyGPUProfileRuntimeEnv` pushes `profile.Env`
  into the runtime load payload so `internal/runtime/manager.go`'s
  `overlayEnvVars(req.Env)` step overrides the in-code ROCmEnvVars baseline.
  This was the actual gap on the runtime path — previously profile env was
  never plumbed into the runtime load request.
- `internal/runtime/manager.go` keeps its `backend.ROCmEnvVars(m.gpuArch)`
  call as the in-code fallback. A comment now documents that profile env
  arrives via `req.Env` and overrides this baseline.

Tests in `backend/gpu_compat_test.go` cover the four precedence states
(profile wins, explicit nil, profile-without-env, profile-with-empty-env),
plus a non-AMD-without-profile guard and two `EnvFromProfile` accessor cases.

## Priority 2 — Replace per-backend `Image()` rules with profile-first inside the backend — COMPLETED

Shipped in `refactor/backend-imagerules-cleanup` (slice 3).

All seven backend rule slices now declare `gfx110` and `gfx906` entries as
env-only — the `Default:` field was deleted on those rows. Per-arch images
moved to `deploy/gpuprofiles/gfx1100.yaml` and `gfx906.yaml` under
`backends.<name>.image`, where `ResolveBackendImage` reads them via the
profile-first contract introduced in slice 1. Nodes without a GPUProfile
fall through the rule chain to the AMD-generic image (or to the env override
when set), which is the documented backstop and matches the existing
`ResolveImage` cascade.

Validation:
- `deploy/gpuprofiles/gfx1100.yaml` declares images for `vllm`, `vllm-omni`,
  `diffusers`, `comfyui`, and `mlc-llm`. `llamacpp` and `ollama` had env-only
  arch rules already; the AMD-generic rule preserves prior behavior.
- `deploy/gpuprofiles/gfx906.yaml` declares images for `vllm`, `llamacpp`,
  `diffusers`, `comfyui`, and `mlc-llm`. `ollama` had an env-only arch rule
  already.
- `backend/gpu_compat_test.go::TestResolveBackendImage_RealBackendsArchEnvOnly`
  asserts the slice-3 contract end-to-end across all seven backends:
  profile.Image wins, env override fires when profile is nil, and the
  vendor-generic fallback is the documented backstop.
- `controllers/model_controller_test.go` adds
  `TestValidateBackendGPUCompatibility_ComfyUIOnGFX906_NoProfile` to document
  the new fallback signal: no profile → ExperimentalGPUSupport warning fires,
  which is the operator cue to onboard the node.

NVIDIA `sm_5` (Maxwell) and CPU rules retained their `Default:` fields. The
sm-52 GPUProfile already declares `ollama` and `llamacpp` images; folding the
remaining Maxwell entries (`mlc-llm`, `llamacpp` Maxwell) into a follow-up
keeps this slice scoped to the documented `gfx110/gfx906` cleanup.

Files migrated:
- `backend/comfyui.go:11-19` — gfx110+gfx906 env-only
- `backend/diffusers.go:11-19` — gfx110+gfx906 env-only
- `backend/llamacpp.go:13-25` — gfx906 env-only (gfx110 was already env-only)
- `backend/mlc_llm.go:12-24` — gfx110+gfx906 env-only
- `backend/ollama.go:9-27` — comment refresh only (gfx110+gfx906 were already env-only)
- `backend/vllm.go:14-22` — gfx110+gfx906 env-only
- `backend/vllm_omni.go:11-19` — gfx110 env-only

## Priority 3 — Move `pkg/quantization/image.go:92` `gfx906` branch to GPUProfile lookup — COMPLETED

Shipped in `refactor/gpuprofile-quantizer-image-helper` (slice 4 of Track A).

- Added `ResolveImageFromProfile(format, *aiv1alpha2.GPUProfileSpec, vendor, arch)`
  in `pkg/quantization/image.go`, mirroring the
  `backend.ResolveBackendImage(b, profile, vendor, arch)` helper from slice 1.
  Precedence: `profile.Quantization.Images[format]` (when non-empty) wins;
  otherwise it falls through to the existing
  `ResolveImage(format, "", vendor, arch)` cascade (runtime override → arch env
  → generic env → hardcoded default).
- Removed the hardcoded `if gpuArch == "gfx906"` branch from
  `resolveGPTQROCmImage`. `deploy/gpuprofiles/gfx906.yaml:89` already declares
  `quantization.images.gptq`, and the controller-side path in
  `controllers/modelcache_quantization.go` populates `ProfileQuantizerImage`
  from that field via `backend.QuantizerImageFromProfile` before
  `ResolveImage` is consulted.
- The arch-specific env var (`FLEXINFER_QUANTIZER_GPTQ_ROCM_<ARCH>_IMAGE`) is
  preserved as the documented backstop for clusters running ahead of a
  GPUProfile reconcile.
- The new `ResolveImageFromProfile` helper is callsite-compatible — existing
  controllers continue to use `ResolveImage(format, params.ProfileQuantizerImage, ...)`
  via `JobParams.ProfileQuantizerImage`. Future refactors can adopt the
  profile-spec variant directly when convenient.

Tests:
- `pkg/quantization/image_test.go::TestResolveImageFromProfile_GPUProfileFirst`
  covers six cases: profile wins on gfx906, profile nil falls through to
  default on gfx906, profile-without-entry falls through on gfx1100, profile
  declares abliteration on gfx906, profile beats env override on gfx1100,
  profile-with-empty-string falls through on gfx1100.
- Updated `pkg/quantization/image_test.go::TestResolveImage_GPUArchMatrix` and
  `pkg/quantization/gptq_test.go::TestResolveImage_GPTQ_ROCm` to reflect the
  new fallback (no hardcoded gfx906 default).
- Updated `pkg/quantization/quantization_test.go::TestGPTQJobBuilder_BuildJob_AMDVendor_GFX906`
  to inject the gfx906 image via `ProfileQuantizerImage`, matching the
  production controller path.
- Updated `controllers/modelcache_quantization_reconcile_test.go::TestReconcileQuantizationWarmsRuntimeImageBeforeWorkerJob`
  to register a fake gfx906 GPUProfile with a `flexinfer/runtime` image so the
  warmup path still triggers under the new contract.

Files migrated:
- `pkg/quantization/image.go:84-100` — `gfx906` hardcoded branch removed,
  `ResolveImageFromProfile` added at top of file (~33 LOC).

## Priority 4 — `BackendCanary` status annotation contract (deferred)

The plan recommended a `BackendCanary` status annotation rather than a new
spec enum. This MR did not add it because none of the migrated callers needed
it yet. When the `gfx906`/`gfx1100` canary lanes start writing per-backend
canary results, add a typed helper:

- `func SetBackendCanary(profile *GPUProfile, backendName, status, reason string)` in `api/v1alpha2/gpuprofile_types.go`
- Stored under `metadata.annotations["ai.flexinfer.io/canary.<backend>"]` to avoid schema churn.

Wire consumers in a separate MR — keep this slice schema-only with one unit test.

## Priority 5 — Replace `BackendGPUCompatibility` map with profile-only lookup

`backend/gpu_compat.go:43-93` is the single largest hardcoded `(backend, arch)`
table. Every entry is duplicated in `deploy/gpuprofiles/*.yaml`. After
priority-2 lands, the map is only consulted when no profile is reconciled.
Once `LookupOrFetch` is wired everywhere (`controllers/gpuprofile_controller.go:55`),
delete the map and treat its entries as the seeds for the GPUProfile manifests
that ship in `platform/gitops`.

Files to touch:
- `backend/gpu_compat.go:43-93`
- `controllers/model_gpu.go:83-108` (`validateBackendGPUCompatibility`)
- `controllers/model_gpu.go:38-79` (`validateVRAMFit`)

Validation: confirm `scripts/check-runtime-profile-consistency.sh` covers all
backends after the map is removed; extend the check if it does not.

## Priority 6 — Strip `Default` from NVIDIA Maxwell (`sm_5`) rule entries

Slice 3 left the `Vendor: GPUVendorNVIDIA, ArchPrefix: "sm_5"` rule entries in
`backend/llamacpp.go` and `backend/mlc_llm.go` with their hardcoded `Default:`
images intact, because the `sm-52` GPUProfile only declares `ollama` and
`llamacpp` overrides. Once `mlc-llm` is added to `deploy/gpuprofiles/sm_52.yaml`
(and any Maxwell test on the GTX 980 Ti node has a chance to reconcile), the
Maxwell `Default:` strings can be dropped using the slice-3 pattern:

- `backend/llamacpp.go` — drop `registry.harbor.lan/flexinfer/llamacpp:cuda-maxwell`
  (already redundant with `sm_52.yaml::backends.llamacpp.image`).
- `backend/mlc_llm.go` — declare the Maxwell image in `sm_52.yaml`, then drop
  `registry.harbor.lan/flexinfer/mlc-llm:cuda-maxwell-v7` from the rule slice.

Validation: extend `TestResolveBackendImage_RealBackendsArchEnvOnly` to cover
the Maxwell profile path and the no-profile fallback.
