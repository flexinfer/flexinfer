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

CPU rules retained their `Default:` fields as the non-GPU fallback. NVIDIA
`sm_5` (Maxwell) arch defaults later moved to `deploy/gpuprofiles/sm_52.yaml`
in Priority 6, matching the same profile-owned image contract used for
`gfx110/gfx906`.

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

## Priority 4 — `BackendCanary` status annotation contract — COMPLETED

Shipped in `feat/backend-canary-annotations` (slice 6 of Track A — schema-only,
no consumers wired). The slice ships the contract recommended in slice 1 of
the gfx1100/gfx906 plan: canary state lives as ObjectMeta annotations on a
GPUProfile, not as a new `BackendProfile.Support` enum value.

Annotation contract (three keys per backend, scoped per-arch via the GPUProfile
the annotations live on):

- `flexinfer.ai/backend-canary-<backend>          : "true" | "false"`
  Whether the backend is in canary mode on this GPU architecture. Absent or
  any non-"true" value (case-insensitive, whitespace-tolerant) is "not canary".
- `flexinfer.ai/backend-canary-<backend>-since    : RFC3339 timestamp`
  When the canary state was last set. Refreshed on every Set call. Absent or
  unparseable -> zero time.
- `flexinfer.ai/backend-canary-<backend>-evidence : URL or path string`
  Free-form pointer to the validation matrix row, runbook, MR, or dashboard
  documenting why canary mode is active. Absent -> "".

The three keys move together: `SetBackendCanary` writes all three;
`ClearBackendCanary` deletes all three; `GetBackendCanary` surfaces the tuple.

Helpers (in `api/v1alpha2/gpuprofile_canary.go`):

- `GetBackendCanary(profile *GPUProfile, backend string) (isCanary bool, since time.Time, evidence string)`
  — returns the canary tuple, treating partial/malformed annotations
  conservatively (parse failures collapse to false / zero / "").
- `SetBackendCanary(profile *GPUProfile, backend string, evidence string)`
  — annotates the profile in-memory; refreshes "-since" on every call so
  Set can also serve as "renew evidence".
- `ClearBackendCanary(profile *GPUProfile, backend string)` — deletes the
  annotation triple. Other annotations on the profile are preserved.

The helpers mutate the GPUProfile object in memory only; controller callers
are responsible for persisting the change via Update or Patch. This keeps
the contract testable without a fake client and matches the slice-1..5
helper style.

Tests in `api/v1alpha2/gpuprofile_canary_test.go` (12 test functions, 29
subcases) cover: annotation key formatting, no-annotations / empty-backend
zero return, Set→Get roundtrip, Set overwrite refreshes the timestamp, Set
nil/empty no-op, Clear removes all three keys, Clear preserves unrelated
annotations, Clear nil/empty no-op, multiple backends coexist on the same
profile (and per-backend Clear isolates), and malformed-annotation parsing
(boolean false, whitespace+case, garbage truthy, unparseable timestamp,
since-only without evidence).

Files added:
- `api/v1alpha2/gpuprofile_canary.go` — annotation key constants, three
  helpers, godoc-documented contract.
- `api/v1alpha2/gpuprofile_canary_test.go` — unit tests.

No CRD schema changes — annotations are sufficient for status as the design
intended. `make manifests` produced only controller-gen version churn, which
was restored before commit.

Follow-up consumer shipped in `codex/backend-canary-event`:

- `controllers/gpuprofile_controller.go` now keeps a full-object GPUProfile
  cache next to the existing spec cache so ObjectMeta annotations are available
  without changing the `Lookup()` contract used by existing callers.
- `controllers/model_gpu.go::validateBackendGPUCompatibility` calls
  `GetBackendCanary(profile, backendName)` and emits a warning
  `BackendCanary` event when serving on a canary backend, including the
  timestamp and evidence pointer when present.
- `controllers/model_gpu_test.go` covers the event path, and
  `controllers/gpuprofile_controller_test.go` verifies that cold-cache API
  fetches preserve canary annotations in the full-object cache.

Remaining follow-up ideas:

- `scheduler/scheduler.go` may want a defense-in-depth check, though the
  scheduler currently has no GPUProfile cache in scope (same caveat as slice
  5's `LookupGPUArchSupport` call).
- A tiny `kubectl flexinfer canary <gpuprofile> <backend> --evidence=<url>`
  CLI surface (or operator-facing runbook step) that calls Set/Clear via the
  controller client.

## Priority 5 — Replace `BackendGPUCompatibility` map with profile-only lookup — COMPLETED

Shipped in `refactor/backend-compat-gpuprofile-first` (slice 5 of Track A).

- Added `backend.ResolveBackendGPUSupport(profile, backendName, gpuArch)` in
  `backend/gpu_compat.go`, mirroring the slice-1 / slice-4
  `ResolveBackendImage` / `ResolveImageFromProfile` precedence pattern.
  Profile-declared `backends.<name>.support` wins; the helper falls through to
  the legacy `BackendGPUCompatibility` map only when the profile is nil or
  declares no entry for the backend.
- Added `backend.IsBackendSupported(profile, backendName, gpuArch)` as the
  policy boolean. `SupportFull` and `SupportExperimental` return `true`
  (allow-with-caveats — the controller emits an `ExperimentalGPUSupport`
  event separately); `SupportUnsupported` and "no entry found" return
  `false`. The godoc explicitly documents this so future canary work can
  layer additional gates without redefining the static support semantics.
- Migrated `controllers/model_gpu.go::validateBackendGPUCompatibility` and
  `validateVRAMFit` to use `ResolveBackendGPUSupport`. Both functions used
  to write the profile-then-map cascade inline; they now call the shared
  helper, which removes a duplicated branch and aligns the precedence rule
  with the rest of slice 1-4.
- Kept `BackendGPUCompatibility` and `LookupGPUArchSupport` exported and in
  use by `scheduler/scheduler.go:292` (defense-in-depth filter at scheduler
  time, no GPUProfile cache available there). Both are now annotated as
  `Deprecated` in their godoc — new callers should prefer the
  GPUProfile-aware helpers, but the table itself is the documented backstop
  for nodes/processes outside the controller's profile cache.

Tests:
- `backend/gpu_compat_test.go::TestResolveBackendGPUSupport` covers eight
  cases: profile full wins, profile experimental wins, profile unsupported
  downgrades a legacy-full entry, profile-without-backend-entry falls
  through, profile with unknown support string falls through, nil profile
  uses the legacy map, nil profile + unknown backend returns not-found,
  profile entry with empty `Support` (image-only) falls through.
- `backend/gpu_compat_test.go::TestIsBackendSupported` documents the
  full→true / experimental→true / unsupported→false / no-entry→false
  policy mapping with both profile-driven and legacy-fallback cases.
- Existing `controllers/model_gpu_test.go::TestValidateBackendGPUCompatibility`
  and `controllers/model_controller_test.go` Maxwell/gfx906 tests continue to
  pass without modification — the controller-side migration preserves the
  observable behavior.

Files migrated:
- `backend/gpu_compat.go:42-50` — `BackendGPUCompatibility` annotated `Deprecated`.
- `backend/gpu_compat.go:96-105` — `LookupGPUArchSupport` annotated `Deprecated`.
- `backend/gpu_compat.go:132-187` — added `ResolveBackendGPUSupport` and `IsBackendSupported`.
- `controllers/model_gpu.go:43-61` — `validateVRAMFit` cascade folded into `ResolveBackendGPUSupport`.
- `controllers/model_gpu.go:88-115` — `validateBackendGPUCompatibility` cascade folded into `ResolveBackendGPUSupport`.

Validation:
- `make manifests` (controller-gen churn restored before commit).
- `go test ./backend/... ./controllers/... ./api/v1alpha2/... ./pkg/quantization/... ./pkg/runtime/... ./internal/runtime/... ./scheduler/...` — all green.
- `scripts/check-runtime-profile-consistency.sh` — passes; the script already
  inspects `deploy/gpuprofiles/*.yaml` against the runtime image manifests, no
  extension was needed because the support-level field was already present in
  every profile after slice 2.

Remaining legacy callers documented:
- `scheduler/scheduler.go:292` keeps `LookupGPUArchSupport` because the
  extender does not have a GPUProfileReconciler in scope. Wiring that in is
  a separate, larger refactor (the scheduler would need to load profiles
  from the cluster on its own); for now the legacy map is the documented
  backstop for the scheduler's defense-in-depth `SupportUnsupported` reject.

## Priority 6 — Strip `Default` from NVIDIA Maxwell (`sm_5`) rule entries — COMPLETED

Shipped in `backlog/61-maxwell-profile-images` via MR !306 ([Issue #61](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/61)).
Follow-up MR !307 cleared the post-merge `govulncheck` failure by moving CI to
Go 1.25.10 and `golang.org/x/net` v0.53.0.

The Maxwell image defaults now follow the same profile-owned contract as the
`gfx110/gfx906` arch defaults:

- `deploy/gpuprofiles/sm_52.yaml` declares the `mlc-llm` Maxwell image next to
  the existing `ollama` and `llamacpp` image overrides.
- `backend/llamacpp.go` and `backend/mlc_llm.go` keep the Maxwell env override
  rules but no longer embed hardcoded `Default:` image strings for `sm_5`.
- Nodes without a GPUProfile now fall through to the NVIDIA-generic backend
  image, which is the documented backstop; Maxwell-specific images require
  either the `sm_52` GPUProfile or the existing Maxwell env override.

Tests:
- `backend/gpu_compat_test.go::TestResolveBackendImage_RealBackendsArchEnvOnly`
  covers Maxwell no-profile fallback, profile image wins, and env override
  preservation for both `llamacpp` and `mlc-llm`.
- Backend-specific image tests now document the no-profile NVIDIA-generic
  fallback for Maxwell.
