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

## Priority 1 — Push GPUProfile-first into env-vars on the runtime/Job paths

`controllers/model_runtime.go` and `pkg/quantization/gpu_job.go` still call
`backend.ROCmEnvVars(arch)` (`backend/interface.go:299`) before merging
`profile.Env`. That ordering means the in-code switch on `gfx110*/gfx90a/gfx942/gfx906`
"wins" until the controller-side merge fires later. The contract should be:
profile env declarations are authoritative for the architectures they cover,
and `ROCmEnvVars` is only consulted when the profile is nil OR has no env entries.

Files to touch:
- `backend/interface.go:299-353` (`ROCmEnvVars` switch)
- `controllers/model_deployment.go:189-196` (env merge)
- `controllers/model_runtime.go` (mirror the same merge order)
- `pkg/quantization/gpu_job.go` (job env)

Add a `backend.ResolveBackendEnv(profile, vendor, arch)` helper alongside
`ResolveBackendImage` and migrate all three callers in one MR.

## Priority 2 — Replace per-backend `Image()` rules with profile-first inside the backend

The `backend/comfyui.go:11-15`, `backend/diffusers.go:11-15`,
`backend/llamacpp.go:13-17`, `backend/mlc_llm.go:12-15`,
`backend/ollama.go:12-15`, `backend/vllm.go:14-19`, `backend/vllm_omni.go:11-17`
files all declare an `imageRules` slice that hardcodes `gfx110` and `gfx906`.
After this MR, every controller call already does GPUProfile-first via
`ResolveBackendImage`, so the in-backend tables only matter when no profile
exists. That is the correct fallback shape, but the hardcoded `Default`
strings still drift from `build/runtime.yaml`. Move the defaults into the
GPUProfile manifests under `deploy/gpuprofiles/*.yaml` and shrink each
backend's rule slice to env-var-only entries (no `Default`).

Files to touch (rule slices only):
- `backend/comfyui.go:11`
- `backend/diffusers.go:11`
- `backend/llamacpp.go:13`
- `backend/mlc_llm.go:12`
- `backend/ollama.go:12`
- `backend/vllm.go:14`
- `backend/vllm_omni.go:11`

Validation: ensure `deploy/gpuprofiles/gfx1100.yaml` and `gfx906.yaml` declare
a `backends.<name>.image` for every backend that previously had a `Default`.

## Priority 3 — Move `pkg/quantization/image.go:92` `gfx906` branch to GPUProfile lookup

`resolveGPTQROCmImage` (`pkg/quantization/image.go:84-100`) still has a
hardcoded `if gpuArch == "gfx906"` returning `DefaultGPTQROCmGFX906Image`.
The `ResolveImage(format, profileImage, ...)` entry point already accepts
`profileImage` from the GPUProfile, so the `gfx906` fallback only fires when
the profile is missing. Remove the hardcoded branch once `deploy/gpuprofiles/gfx906.yaml`
declares `quantization.images.gptq` (and the radeonvii cluster has had time
to reconcile).

Files to touch:
- `pkg/quantization/image.go:84-100`
- `deploy/gpuprofiles/gfx906.yaml`

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
