# Product Spec — vLLM Feature Parity on AMD (Wave 1)

**Date**: 2026-05-15
**Wave**: 1 of 2 (Wave 2 deferred, gated on upstream bug closures)
**Estimated effort**: 2-3 weeks single-engineer; 8-10 days with parallel sub-agents
**Predecessor brainstorm**: `.loom/brainstorm-vllm-feature-parity-amd-2026-05-15.md`
**Predecessor plan (track umbrella)**: `.loom/gfx1100-gfx906-next-round-plan.md`
**Status**: Draft — awaiting plan-loom-core handoff to `30-implementation-plan` track expansion

## Goal

Bring the AMD `gfx1100` (RDNA3 / RX 7900 XTX) and `gfx906` (Vega20 / Radeon VII) GPU classes closer to Nvidia feature parity on vLLM, by (a) consolidating to a single recent vLLM target (0.19.x) where most upstream ROCm work landed, (b) retiring our local gemma4-MoE Python patch in favor of vLLM's native `FusedMoE` Triton path where it works, (c) making feature support per architecture *declarative* in `GPUProfile` so future upgrades don't repeat the env-var sprawl, and (d) reviving gfx906 vLLM as a deliberately-narrow but real engine profile.

Wave 1 is **feature-parity-on-paper**: the V1 engine, native FusedMoE for BF16, FP8 KV emulation as an opt-in, declarative capability matrix, gfx906 vLLM in production. Wave 2 (out of scope) flips the piecewise-graph-capture perf win when upstream bugs close.

## Non-Goals

- **Not** Wave 2: do not attempt to flip `vllm.piecewiseGraphs` from `experimental` to `supported`. Upstream #39010 (CUDA-graph capture hang on ROCm 0.19) and #41622 (`hipErrorCapturedEvent` on V1 piecewise) must close first. Track them, don't fight them.
- **Not** tensor-parallel on gfx1100: upstream #38587 (RCCL TP=2 init failure on gfx1100) is open. TP=1 only this cycle.
- **Not** writing new ROCm kernels: V5 (Marlin INT4 ROCm port) is dropped because `turboquant-vllm` is third-party (`github.com/Alberto-Codes/turboquant-vllm`) and kernel improvements live either there or upstream in vLLM. V8 (upstream gemma4 HIP kernel contribution) is parked as long-tail outside this spec.
- **Not** a llama.cpp pivot or fleet workload re-allocation: those were explored in earlier brainstorms and explicitly out of scope per user direction.
- **Not** changing controller behavior on the schema-only slice (V7). Schema additions land first as no-op metadata; consumers come later.

## Users / Operators

- Homelab operator running `cblevins-7900xtx` (RX 7900 XTX), `cblevins-5930k` (RX 7900 XTX on Haswell-E host), and `cblevins-radeonvii` (Radeon VII).
- FlexInfer maintainer promoting runtime images and GPUProfile capability flips by digest.
- Future agents implementing Wave 2 (piecewise graph flip) against the capability matrix this spec lands.

## Current Evidence

- vLLM mainline `gemma4.py` natively uses `FusedMoE` with a `custom_routing_function`. Merged in **v0.19.0** via [vllm-project/vllm#38826](https://github.com/vllm-project/vllm/pull/38826). Requires `transformers >= 5.5.0`. Routing kernel gate is `is_cuda_alike() or is_xpu()`; HIP returns true for `is_cuda_alike()`, so the Triton routing kernel path is taken on RDNA3. ([Q1 research, 2026-05-15](./brainstorm-vllm-feature-parity-amd-2026-05-15.md#q1--gemma4--fusedmoe-in-upstream-vllm-found))
- vLLM V1 engine on gfx1100 has open critical bugs: [vllm-project/vllm#39010](https://github.com/vllm-project/vllm/issues/39010) (CUDA-graph hang on ROCm 0.19, `--enforce-eager` only workaround), [#41622](https://github.com/vllm-project/vllm/issues/41622) (`hipErrorCapturedEvent` on V1 piecewise), [#38587](https://github.com/vllm-project/vllm/issues/38587) (RCCL TP=2 init failure on gfx1100). V0 is removed in vLLM ≥ 0.18 — no "stay on V0" option past 0.17.x. ([Q2 research, 2026-05-15](./brainstorm-vllm-feature-parity-amd-2026-05-15.md#q2--v1-engine-on-gfx1100-experimental-perf-positive-features-blocked-by-open-bugs))
- `GPUProfileSpec` already has `Features` (FP16/BF16/FP8/FlashAttention/INT4/INT8), `Backends map[string]BackendProfile`, and `Env []corev1.EnvVar`. `BackendProfile.Support` is currently a free-form string ("full"/"experimental"/"unsupported") — narrow enough to extend without a v1beta1 bump (`api/v1alpha2/gpuprofile_types.go:24-152`).
- Repo has **three** vLLM versions in flight: `0.14.0rc0` (Navi base image), `0.17.0+rocm700` (default in `build/Dockerfile.runtime:35`, `Dockerfile.runtime-gfx906:59`, `runtime.yaml`), `0.18.0+rocm700` (prebuilt in `Dockerfile.vllm-gfx1100-v018:17`), pinned commit `50cd5674` (experimental gemma4 in `runtime.yaml`'s `gfx1100-gemma4-experimental` profile). This is the actual operational pain.
- `turboquant-vllm` is third-party: `https://github.com/Alberto-Codes/turboquant-vllm.git` (per `build/Dockerfile.runtime:45`, `Dockerfile.runtime-gfx906:69`). Provides both the Tq4 GPTQ backend (entry-point `tq4_backend` in group `vllm.general_plugins`) and a KV cache codec (`FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC=turboquant`, `build/runtime-entrypoint.sh:270-271`). `deploy/models/gemma4-e4b-turboquant.yaml` rollout active; `gemma4-31b-gptq-long.yaml` annotated `flexinfer.ai/promotion-state: turboquant-canary-disabled-pending-shared-primitives-boot`.
- gfx906 runtime DaemonSet is **paused** (`flexinfer.ai/runtime-paused: "true"`, `deploy/system/values-k3s.yaml:354`) pending Track B (disk-pressure unblock). Spec for that lives in `.loom/gfx1100-gfx906-next-round-plan.md` Track B.
- Current production gfx1100 env vars: `VLLM_USE_TRITON_FLASH_ATTN=0` (uses CK FA from base image), `VLLM_ROCM_USE_AITER=0` (correctly off — MI300X-only), `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1`, `VLLM_USE_V1=0` (V0 engine). gfx906: same except no AOTriton, no AITER (`build/runtime.yaml:20-54`, `:215-238`).

## Requirements

1. **GPUProfile capability matrix is the source of truth for vLLM feature support per arch.**
   - Add a typed `VLLM` capability struct on `BackendProfile`. Fields enumerate feature support level per arch.
   - All current Dockerfile-level env-var feature flags (`VLLM_USE_V1`, `VLLM_USE_TRITON_FLASH_ATTN`, etc.) become *defaults injected from the profile*, not Dockerfile constants. (Defaults injection lands in a follow-up slice; the schema lands first as no-op.)
   - Schema must accommodate future Wave 2 flips without breaking changes.

2. **One canonical vLLM version per profile.**
   - `gfx1100` profile pins exactly one vLLM patch version from the 0.19.x series. Document the upstream commit hash in the Dockerfile.
   - `gfx906` profile pins the same 0.19.x version (or earliest 0.19.x that supports gfx906 community wheels, documented).
   - `gfx1100-gemma4-experimental` profile is either deleted or rebased onto the canonical version. The pinned commit `50cd5674` exists only because mainline drifted past the patch script; after V2a (patch retirement for BF16) the experimental profile may not be needed at all.

3. **gemma4-MoE Python patch is retired where upstream covers it.**
   - For **BF16 gemma4-26b**: native vLLM FusedMoE path validated against the coherence gauntlet (6/6 exact-match or documented FP16-reduction-divergence equivalent to MR !363). Patch removed for BF16.
   - For **GPTQ-INT4 gemma4-26b**: sandbox canary against the native FusedMoE INT4 weight-loader path. If passes coherence, retire patch. If fails (related upstream bugs #38912, #39000 in the INT4/MXFP4 weight-loader area), keep `patch_vllm_env_override_torch29.py` version-pinned against the 0.19.x target and file an upstream issue.

4. **V1 engine on gfx1100 ships with safe defaults.**
   - `cudagraph_mode: NONE` (forced eager) is the default until Wave 2.
   - `enforce_eager: true` retained on gemma4-MoE model CRs through this cycle.
   - Capability matrix entry: `vllm.v1Engine: supported`, `vllm.piecewiseGraphs: experimental` with `default-off`.

5. **FP8 KV cache emulation available as opt-in.**
   - Model CR can request `kv_cache_dtype: fp8` and the runtime accepts it via the existing `patch_vllm_env_override_torch29.py` wiring (or its 0.19.x replacement).
   - Quality and perf overhead measured against `kv_cache_dtype: auto` baseline on a non-production model before any production model opts in.
   - Capability matrix entry: `vllm.fp8KVEmulation: experimental`.

6. **gfx906 vLLM revival as a narrow-but-real profile.**
   - Image: `registry.harbor.lan/flexinfer/runtime:rocm-gfx906-vllm` built from existing `build/Dockerfile.vllm-rocm-gfx906`, retargeted to vLLM 0.19.x.
   - Features: V1 engine, eager mode, paged attention, prefix caching, continuous batching, GPTQ via ExllamaV2 (turboquant Tq4 backend if it builds on Vega20; else stock vLLM GPTQ).
   - No FA (no kernel exists for gfx906 in upstream Triton/AOTriton/CK).
   - Models capped at 14 GiB resident (VMM ceiling on Vega20 is 16 GiB; 2 GiB reserved for activations/KV).
   - **Gated by Track B** (gfx906 disk-pressure unblock). V6 cannot ship until the DaemonSet is unpaused. Spec must surface this dependency, not paper over it.

7. **Coordinate with Track A (controller hardening).**
   - Track A is touching `controllers/gpuprofile_controller.go` and `backend/interface.go` in parallel. The schema delta in this spec must merge cleanly with Track A's API additions.
   - Use a shared base branch for the GPUProfile schema slice; both tracks rebase onto it.

## Architectural Plan

### GPUProfile schema delta (V7, no-op rollout)

Add to `api/v1alpha2/gpuprofile_types.go`. All fields `omitempty` so existing GPUProfiles still validate.

```go
// BackendProfile describes a backend's support level and optional image override for this arch.
type BackendProfile struct {
    // Support level: "full", "experimental", or "unsupported". (existing field)
    Support string `json:"support"`

    // Image overrides the default container image for this backend on this architecture.
    // +optional
    Image string `json:"image,omitempty"` // existing

    // VLLM declares vLLM-specific feature capability per architecture.
    // Only meaningful when Support != "unsupported" and the backend key is "vllm".
    // +optional
    VLLM *VLLMCapabilities `json:"vllm,omitempty"`
}

// VLLMCapabilities declares per-arch vLLM feature support and engine-arg defaults.
// Each capability field is "supported" | "experimental" | "unsupported".
type VLLMCapabilities struct {
    // V1Engine reports vLLM V1 engine support. V0 was removed upstream in ≥0.18.
    // +optional
    V1Engine string `json:"v1Engine,omitempty"`

    // PiecewiseGraphs reports V1 piecewise CUDA-graph capture support.
    // gfx1100 as of 2026-05: "experimental" pending upstream #39010 / #41622.
    // +optional
    PiecewiseGraphs string `json:"piecewiseGraphs,omitempty"`

    // FlashAttention reports which FA backend is supported. One of "ck", "triton",
    // "aotriton", or "none". gfx1100 today: "ck" with "aotriton" experimental.
    // gfx906 today: "none".
    // +optional
    FlashAttention string `json:"flashAttention,omitempty"`

    // FusedMoETriton reports vLLM-native FusedMoE Triton kernel support
    // (covers gemma4, mixtral, qwen3-moe, etc.). gfx1100 BF16: "supported"
    // (validated). gfx1100 INT4: "experimental" (weight-loader path unproven).
    // +optional
    FusedMoETriton string `json:"fusedMoETriton,omitempty"`

    // FP8KVEmulation reports INT8-with-FP8-scale KV cache emulation support
    // for arches without native FP8 hardware (RDNA3, Vega20).
    // +optional
    FP8KVEmulation string `json:"fp8KVEmulation,omitempty"`

    // MarlinINT4 reports Marlin INT4 GEMM kernel support. CUDA-only upstream;
    // ROCm variants (Conch, rocm_aiter_marlin) ship in newer vLLM.
    // turboquant-vllm Tq4 backend is our current INT4 path, tracked separately.
    // +optional
    MarlinINT4 string `json:"marlinINT4,omitempty"`

    // Defaults specifies default engine args injected when this profile is selected.
    // Wave 1 lands the field; controller consumers come in a follow-up slice.
    // +optional
    Defaults *VLLMDefaults `json:"defaults,omitempty"`
}

// VLLMDefaults specifies per-arch default vLLM engine args.
type VLLMDefaults struct {
    // CudagraphMode is the default --cudagraph-mode engine arg.
    // One of "NONE", "PIECEWISE", "FULL". gfx1100 Wave 1: "NONE".
    // +optional
    CudagraphMode string `json:"cudagraphMode,omitempty"`

    // EnforceEager forces enforce_eager=true at the engine level.
    // +optional
    EnforceEager *bool `json:"enforceEager,omitempty"`

    // KVCacheDtype default. One of "auto", "fp8", "fp8_e4m3", "fp8_e5m2".
    // Model CRs may override.
    // +optional
    KVCacheDtype string `json:"kvCacheDtype,omitempty"`
}
```

**Backfill at t=0** (no behavior change; encodes current truth):

| Field | `gfx1100` value | `gfx906` value |
|---|---|---|
| `v1Engine` | `supported` (target 0.19.x, V0 removed) | `supported` (target 0.19.x, V0 removed) |
| `piecewiseGraphs` | `experimental` (default-off) | `unsupported` |
| `flashAttention` | `ck` | `none` |
| `fusedMoETriton` | `experimental` (BF16 works, INT4 unproven) | `experimental` |
| `fp8KVEmulation` | `experimental` | `unsupported` |
| `marlinINT4` | `unsupported` (turboquant Tq4 is our path) | `unsupported` |
| `defaults.cudagraphMode` | `NONE` | `NONE` |
| `defaults.enforceEager` | `true` (until Wave 2) | `true` |
| `defaults.kvCacheDtype` | `auto` | `auto` |

### Controller logic delta

**Wave 1 schema-only slice: zero controller behavior change.** New fields are added, `make manifests` re-generates the CRD, deployed `GPUProfile` YAMLs are updated with the backfill values above. No reconciler reads the new fields yet.

A follow-up slice (post-Wave 1, pre-Wave 2) will add a `pkg/vllm/capability.go` helper that:
- Resolves `BackendProfile.VLLM` for a given `Model`'s target architecture.
- Returns engine args derived from `defaults`.
- Rejects Model CRs whose `kv_cache_dtype` requires a capability the profile declares `unsupported`.
- Surfaces a status condition on the Model when an `experimental` capability is opted into.

This follow-up is *not* in Wave 1 scope. Listing it here so reviewers know the schema is forward-compatible.

### Runtime image consolidation

Wave 1 targets exactly two production runtime images:

| Image tag | Built from | vLLM | Replaces |
|---|---|---|---|
| `runtime:rocm-gfx1100` | `build/Dockerfile.runtime` with profile `gfx1100` | one pinned 0.19.x patch + commit hash | current `0.14.0rc0` base + `0.17.0+rocm700` pip, the `gfx1100-gemma4-experimental` profile pinning `50cd5674` |
| `runtime:rocm-gfx906-vllm` | `build/Dockerfile.vllm-rocm-gfx906` retargeted | same 0.19.x | currently-built-but-unused gfx906 vLLM image |

Dockerfiles to **delete or merge** at the end of Wave 1:
- `build/Dockerfile.vllm-gfx1100-v018` — superseded by the consolidated `Dockerfile.runtime` on 0.19.x.
- `build/Dockerfile.vllm-rocm-gfx1100-fa` — V1-only build flavor, superseded.
- `build/Dockerfile.vllm-rocm-gfx1100` — old V0 source build, superseded.
- `build/Dockerfile.vllm-nightly-rocm-gfx1100` — kept only if used by a tracked validation lane; otherwise delete.
- `build/Dockerfile.vllm-rocm-gfx906-fa` — superseded if `Dockerfile.vllm-rocm-gfx906` covers the consolidated profile.

Patch scripts to **decide on**:
- `build/scripts/patch_vllm_env_override_torch29.py` — currently rebased against `50cd5674`. If V2a + V2b both succeed against 0.19.x native FusedMoE, this script can be **deleted**. If V2b fails, the script must be **re-targeted** against the 0.19.x source tree (one-time engineering cost: ~1 day).

### `build/runtime.yaml` delta

Replace the current three vLLM version references with one. Drop the `gfx1100-gemma4-experimental` profile if V2a closes the case for the patch. Update `gfx906` profile to set `vllm: true` and a new `vllm_version` matching `gfx1100`. Add a new top-level `consistency_test` invariant: "all profiles using vLLM declare the same `vllm_version`."

## Sequencing (8 slices, mostly sequential with two parallel pairs)

1. **Slice 1 — V7 schema-only (reversible, ~1-2 days)**
   - Add the Go types above to `api/v1alpha2/gpuprofile_types.go`.
   - `make manifests` to regenerate CRDs in `config/crd/` and `charts/flexinfer/crds/`.
   - Update `deploy/gpuprofiles/gfx1100.yaml` and `gfx906.yaml` with backfill values.
   - Unit test: `api/v1alpha2/zz_generated.deepcopy_test.go` round-trip parity on the new fields.
   - **Acceptance**: `make manifests` clean diff; `go test ./api/v1alpha2/...` green; deployed GPUProfile manifests render with new fields and a `kubectl get gpuprofile gfx1100 -o yaml | yq '.spec.backends.vllm.vllm'` returns the backfilled block.
   - **Rollback**: revert single MR. No runtime impact; controller still ignores the fields.

2. **Slice 2 — V4 sandbox build for `gfx1100` on vLLM 0.19.x (~2-3 days)**
   - Pick a 0.19.x patch version (`v0.19.0`, `v0.19.1`, or latest patch at sandbox time). Document the upstream commit hash in `build/Dockerfile.runtime` and `build/runtime.yaml`.
   - Confirm `transformers >= 5.5.0` works with current GPTQModel + abliteration pipeline against a small smoke (R1 mitigation).
   - Build `runtime:rocm-gfx1100-0.19-sandbox` tagged image. Do not promote to digest.
   - **Acceptance**: image builds clean; `pip show vllm` reports 0.19.x; `python -c "from vllm.model_executor.models.gemma4 import Gemma4MoE"` succeeds; entrypoint registers `tq4_backend` and accepts `FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC=turboquant`.
   - **Rollback**: don't promote. Current digest stays in production.

3. **Slice 3 — V1-vs-V0 perf parity benchmark (R2 gate, ~1 day)**
   - Side-by-side `qwen3-14b-gptq` benchmark on `cblevins-7900xtx`: current production image (V0, 72-73 tok/s decode reference) vs sandbox image (V1, `cudagraph_mode: NONE`, `enforce_eager: true`).
   - **Acceptance**: V1 decode tok/s ≥ 95% of V0 baseline (regression budget 5%).
   - **If fails**: Wave 1 stops here. File the regression as a known issue, defer V4 to Wave 2.
   - **Rollback**: don't promote.

4. **Slice 4 — V2a — BF16 gemma4 native FusedMoE canary (~1-2 days, parallel with Slice 5)**
   - Cluster smoke on the sandbox image: deploy a BF16 gemma4-26b ModelCache pointing at the native FusedMoE path (no patch script applied). Run the coherence gauntlet (6 prompts at `temperature=0` matched against 7900xtx goldens).
   - **Acceptance**: 6/6 exact-match, or 5/6 with the 6th divergence explained by documented FP16-reduction non-associativity (matching MR !363 acceptance).
   - **If passes**: patch retired for BF16 path; the model executor registration in the consolidated `Dockerfile.runtime` skips the patch.

5. **Slice 5 — V2b — GPTQ-INT4 gemma4 native FusedMoE canary (~1-2 days, parallel with Slice 4)**
   - Cluster smoke on the sandbox image: deploy `gemma4-26b-a4b-gptq` (current production artifact) pointing at native FusedMoE INT4 path (no patch script). Run the coherence gauntlet.
   - **Acceptance** (pass): 6/6 exact-match → patch retired for INT4 too. `patch_vllm_env_override_torch29.py` deleted.
   - **Acceptance** (fail): document failure mode, file upstream issue. Re-rebase the patch script against 0.19.x (one-time ~1 day). Patch stays for INT4 path; capability matrix entry `fusedMoETriton: experimental` confirmed accurate.

6. **Slice 6 — V3 — FP8 KV emulation overhead measurement (~1 day, parallel with Slice 7)**
   - Pick a non-critical model (e.g., `qwen3-14b-gptq` with a low-priority Model CR variant or a dev-namespace deployment). Run decode benchmark with `kv_cache_dtype: auto` vs `kv_cache_dtype: fp8`.
   - Measure: decode tok/s delta, KV memory consumption delta, output coherence on the gauntlet.
   - **Acceptance**: emulation overhead ≤ 10% decode regression for ≥ 1.8x KV memory savings. Coherence preserved (6/6 or documented divergence).
   - **If passes**: `fp8KVEmulation` capability stays `experimental`, opt-in via Model CR documented as safe-for-eval.

7. **Slice 7 — V4 production promotion on `gfx1100` (~1 day, gated on Slices 3+4+5)**
   - Promote sandbox image digest to production via `scripts/promote-runtime-digest.sh gfx1100 --digest <sha256> --apply`.
   - Update `runtime.yaml`, `deploy/gpuprofiles/gfx1100.yaml`, Helm values to the new digest.
   - Flux reconcile. Validate both production gemma4 instances (`gemma4-26b-a4b-gptq` on 7900xtx, `-5930k` on 5930k) Ready within budget (≤15 min cold-load each).
   - Flip capability matrix entries per Slice 4/5 results.
   - **Acceptance**: both gemma4 instances Ready; canary rows updated in `.loom/60-validation-matrix.md`; `qwen3-14b-gptq` reference benchmark within 5% of pre-promotion.
   - **Rollback**: re-run promotion script with previous digest; revert GPUProfile capability flips.

8. **Slice 8 — V6 — gfx906 vLLM revival (~2-3 days, gated on Track B)**
   - **Pre-gate**: Track B (gfx906 disk-pressure unblock) must have landed. Verify `kubectl describe node cblevins-radeonvii | grep -A2 Conditions` shows no `DiskPressure` and the runtime DaemonSet is un-paused (`flexinfer.ai/runtime-paused` annotation removed from `deploy/system/values-k3s.yaml:354`).
   - Build `runtime:rocm-gfx906-vllm` from retargeted `build/Dockerfile.vllm-rocm-gfx906` on the same vLLM 0.19.x version.
   - Deploy a single small model (e.g. `qwen3-1.7b` or a ≤14 GiB resident GPTQ) to `cblevins-radeonvii`. Run coherence smoke.
   - Flip `gfx906` GPUProfile capability matrix entries per actual results (likely: V1 supported, piecewise unsupported, FA none, FusedMoE experimental if MoE model tested).
   - **Acceptance**: small-model canary serves HTTP 200 with coherent output; 24h burn-in with no DiskPressure regression; matrix row added.
   - **Rollback**: re-pause runtime DaemonSet via annotation; revert GPUProfile flip.

## Validation Matrix Delta (rows to add to `.loom/60-validation-matrix.md`)

The existing matrix uses the audit fields in `60-validation-matrix.md:24-37` (`artifact`, `context_length`, `gpu_class`, `backend`, `support_level`, `runtime_image`, `oci_ref`, `observed_failure_mode`, `canary_command`, `rollback_digest`, `spec_roadmap_link`, `promotion_decision`). Add the following rows during Wave 1 execution:

| Slice | Row title | Initial `promotion_decision` |
|---|---|---|
| 2 | `runtime:rocm-gfx1100` v0.19.x sandbox build, no canary | `pending` |
| 3 | `qwen3-14b-gptq` V1-vs-V0 regression benchmark on `gfx1100` | `pending` (then `pass`/`fail`) |
| 4 | `gemma4-26b-a4b` BF16 native FusedMoE canary on `gfx1100` | `pending` (then `pass` or `fail`) |
| 5 | `gemma4-26b-a4b-gptq` INT4 native FusedMoE canary on `gfx1100` | `pending` |
| 6 | `qwen3-14b-gptq` FP8 KV emulation overhead canary | `pending` |
| 7 | `runtime:rocm-gfx1100` production promotion to digest `<sha256>` | `pending` (then `promote`) |
| 8 | `runtime:rocm-gfx906-vllm` minimal-feature revival canary on `cblevins-radeonvii` | `block` (gated on Track B) → `pending` (post-Track B) → `pass` |

## Acceptance Criteria — "Wave 1 done"

All of the following true:

1. `api/v1alpha2/gpuprofile_types.go` includes `VLLMCapabilities` and `VLLMDefaults` types. `make manifests` clean. `go test ./api/v1alpha2/...` green.
2. `deploy/gpuprofiles/gfx1100.yaml` and `gfx906.yaml` carry the backfilled capability matrix.
3. `runtime:rocm-gfx1100` is built from one vLLM 0.19.x version pinned by commit hash. `build/runtime.yaml` has exactly one `vllm_version` for `gfx1100` (plus the gfx906 mirror).
4. At least one of {V2a (BF16), V2b (INT4)} ships with patch retired; if V2b fails, that's documented with an upstream issue link and `patch_vllm_env_override_torch29.py` rebased to 0.19.x. If both pass, `patch_vllm_env_override_torch29.py` is deleted.
5. V1 engine in production on `gfx1100` with `cudagraph_mode: NONE`; `qwen3-14b-gptq` reference within 5% of pre-promotion baseline.
6. FP8 KV emulation overhead row in validation matrix with measured numbers.
7. `gfx906` vLLM runtime image built and deployed to `cblevins-radeonvii` *if* Track B has landed; otherwise the slice is held as `block` and acceptance defers to a Wave 1.5 close-out.
8. Capability matrix entries flipped to reflect measured truth (not aspiration) on both arches.
9. `.loom/00-index.md` updated to point at this spec and a follow-up `30-implementation-plan` slice or Wave 2 placeholder.

## Dependencies

- **Track A (controller hardening)** in `.loom/gfx1100-gfx906-next-round-plan.md`: shares `api/v1alpha2/gpuprofile_types.go`. Use shared base branch; coordinate merge order.
- **Track B (gfx906 disk-pressure unblock)** in same plan: **hard gate** for Slice 8. Slice 8 is `block` until Track B lands.
- **Worktree coordination**: `.worktrees/backlog-31-vllm-gfx906-build` and `.worktrees/codex-abliteration-telemetry-heartbeats` may overlap with this work. Run `git -C /Users/cblevins/workspace/services/flexinfer worktree list` before allocating a worktree for Wave 1 and reuse an aligned branch if one exists.
- **Upstream gates for Wave 2** (not blocking Wave 1): [vllm-project/vllm#39010](https://github.com/vllm-project/vllm/issues/39010), [#41622](https://github.com/vllm-project/vllm/issues/41622), [#38587](https://github.com/vllm-project/vllm/issues/38587). Subscribe to issue updates; flip capability matrix entries when closed.

## Risks and Mitigations

### R1 — `transformers >= 5.5.0` may break GPTQModel + abliteration pipeline

`memory/MEMORY.md` documents at least three transformers-version pitfalls (5.x with PyTorch 2.3 → `NameError: name 'nn' is not defined`; transformers/integrations/accelerate breakage; Qwen3.5 vocab_size nesting). The gfx906 quant image pins `transformers<5` for PyTorch 2.3 compatibility. Moving to `>=5.5.0` may not work on gfx906 at all.

- **Mitigation**: Slice 2's sandbox build is the validation point. Run a smoke quantization (small model: Qwen3-1.7B or smaller) against the sandbox image's transformers version before locking the V4 target.
- **Contingency**: if `>=5.5.0` breaks gfx906 quantization, the gfx906 profile stays on the older transformers via a separate `transformers_constraint` in `build/runtime.yaml`. vLLM 0.19.x reportedly requires `>=5.5.0` only for the gemma4 model file; gfx906 may not need gemma4 anyway, so the constraint can split per profile.

### R2 — V1 + eager may regress vs V0 on gfx1100

V1 engine has scheduler and KV-manager overhead even with graph capture disabled. Without piecewise graphs the perf advantage of V1 is small or zero on RDNA3. We may ship a slower runtime in exchange for paper features.

- **Mitigation**: Slice 3 is a gate, not a check. If `qwen3-14b-gptq` regresses >5% on V1+eager vs V0, **Wave 1 stops at Slice 2**. The schema (Slice 1) still ships (it's a no-op); V4 defers to Wave 2 when graph capture is unblocked.
- **Contingency**: pin production at vLLM 0.17.x (last V0-supporting version) on the new image build; ship Slice 1 + Slice 8 (gfx906 revival on 0.17.x V0) only.

### R3 — `gemma4-e4b-turboquant` rollout status is ambiguous

`deploy/models/gemma4-e4b-turboquant.yaml` looks live. `gemma4-31b-gptq-long.yaml` is annotated `flexinfer.ai/promotion-state: turboquant-canary-disabled-pending-shared-primitives-boot`. The turboquant KV codec entrypoint must survive the V4 upgrade or these models break.

- **Mitigation**: before Slice 7 production promotion, audit:
  - `kubectl get model gemma4-e4b-turboquant -o yaml | yq '.status'` — is it actually Ready in prod?
  - Sandbox image post-build: `python -c "import importlib.metadata as md; eps=[e.name for e in md.entry_points(group='vllm.general_plugins')]; assert 'tq4_backend' in eps"`. Already part of `Dockerfile.runtime:328` boot self-test.
  - `grep FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC build/runtime-entrypoint.sh` still wires the env to the turboquant codec on the new image.
- **Contingency**: if turboquant-vllm doesn't build cleanly against vLLM 0.19.x, file an issue against `github.com/Alberto-Codes/turboquant-vllm`. Hold the `gemma4-e4b-turboquant` model on the previous runtime digest while the rest of the fleet upgrades (Model CR can pin a runtime image per-model).

## Rollout / Backout

- **Roll out by slice**, in the sequencing order above. Each slice's acceptance gate gates the next.
- Slice 1 is reversible (single MR revert).
- Slices 2-6 are sandbox/canary work only; rolling back means "don't promote." No prod impact.
- Slice 7 is the only slice with prod impact. Promotion uses `scripts/promote-runtime-digest.sh gfx1100 --digest <sha256> --apply` with a tracked `rollback_digest` per the validation matrix contract.
- Slice 8 is gated on Track B; if Track B regresses after Slice 8 lands, re-pause the gfx906 runtime DaemonSet (`flexinfer.ai/runtime-paused: "true"` in `deploy/system/values-k3s.yaml`) and revert GPUProfile flips.
- For each slice, the rollback path is documented in the slice description above. Per workspace policy: never `kubectl edit` on prod; always revert via GitOps manifest.

## Effort Estimates (single-engineer, calendar days)

| Slice | Estimate | Parallelizable | Notes |
|---|---|---|---|
| 1 — V7 schema-only | 1-2 | — | Pure Go + CRD regen + YAML backfill |
| 2 — V4 sandbox build | 2-3 | — | Includes R1 transformers compat smoke |
| 3 — V1-vs-V0 perf benchmark | 1 | — | Hard gate for Wave 1 continuation |
| 4 — V2a BF16 canary | 1-2 | with Slice 5 | Coherence gauntlet ≈ 30-60 min wallclock |
| 5 — V2b INT4 canary | 1-2 | with Slice 4 | May result in patch-rebase work (+1 day if fails) |
| 6 — V3 FP8 KV emulation | 1 | with Slice 7 | Decode benchmark on non-prod model |
| 7 — V4 prod promotion | 1 | — | Includes R3 turboquant audit |
| 8 — V6 gfx906 vLLM revival | 2-3 | gated on Track B | If Track B not landed, this slice is `block` |
| **Total** | **10-15 single-engineer days** | | **≈ 2-3 calendar weeks**; **8-10 days with parallel sub-agents on Slices 4+5 and Slice 6 alongside Slice 7** |

## Out-of-Scope Appendix

Listed here so reviewers can confirm the spec respects user-stated scope:

- **V5 (Marlin INT4 ROCm kernel work)**: turboquant-vllm is third-party. Kernel improvements live upstream or in that repo. Track but don't plan.
- **V8 (upstream gemma4 HIP kernel contribution)**: long-tail; parallel ~10% bandwidth track outside this spec.
- **Wave 2 (piecewise graph capture flip, RCCL TP=2, gfx906 V1+graphs)**: gated on upstream #39010, #41622, #38587 closing. Re-plan when one or more closes.
- **llama.cpp pivot on 5930k**: explored in `.loom/brainstorm-fleet-hardware-optimization-2026-05-15.md`, user redirected to vLLM-only scope.
- **Fleet workload re-allocation (move gemma4-MoE off 5930k entirely, etc.)**: same brainstorm, same redirect.
- **`gemma4-31b-gptq-long.yaml` turboquant canary**: held under `turboquant-canary-disabled-pending-shared-primitives-boot`; not promoted as part of Wave 1.
- **MLC backend fast-chat resilience on 5930k (Track G)**: separate track in `.loom/gfx1100-gfx906-next-round-plan.md`.
- **Qwen3.5/3.6 coherence triage (Track H)**: separate track; touches `build/scripts/vllm_qwen35_patches.py` which is independent of the gemma4 MoE patch.

## Sources

- `api/v1alpha2/gpuprofile_types.go:24` (current GPUProfileSpec)
- `api/v1alpha2/gpuprofile_types.go:112` (current BackendProfile.Support)
- `build/runtime.yaml:20` (gfx1100 profile, current env vars)
- `build/runtime.yaml:215` (gfx906 profile, vLLM disabled)
- `build/Dockerfile.runtime:35` (default vLLM_VERSION=0.17.0+rocm700)
- `build/Dockerfile.runtime:45` (TURBOQUANT_REPO third-party origin)
- `build/Dockerfile.runtime:270` (vLLM install path)
- `build/Dockerfile.runtime:326` (turboquant-vllm pip install)
- `build/Dockerfile.runtime:328` (boot self-test for tq4_backend entrypoint)
- `build/Dockerfile.runtime-gfx906:59` (gfx906 VLLM_VERSION)
- `build/Dockerfile.runtime-gfx906:69` (gfx906 turboquant repo)
- `build/Dockerfile.vllm-gfx1100-v018:17` (0.18.0 prebuilt image)
- `build/runtime-entrypoint.sh:270` (FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC wire-up)
- `build/scripts/patch_vllm_env_override_torch29.py:250` (FP8 KV cache wiring)
- `deploy/system/values-k3s.yaml:354` (gfx906 runtime-paused annotation)
- `deploy/gpuprofiles/gfx1100.yaml:32` (current backend support declarations)
- `deploy/gpuprofiles/gfx906.yaml:29` (current backend support declarations)
- `deploy/models/gemma4-e4b-turboquant.yaml:14` (turboquant model rollout)
- `deploy/models/gemma4-31b-gptq-long.yaml:74` (turboquant-canary-disabled annotation)
- `.loom/brainstorm-vllm-feature-parity-amd-2026-05-15.md` (brainstorm + research findings)
- `.loom/gfx1100-gfx906-next-round-plan.md` (Track A/B coordination)
- `.loom/60-validation-matrix.md:24-37` (validation contract)
- `.loom/r5-ngram-spec-decode-falsified-2026-05-14.md` (prior round-2 falsification context)
- `https://github.com/vllm-project/vllm/pull/38826` (gemma4 native FusedMoE in 0.19.0)
- `https://github.com/vllm-project/vllm/issues/39010` (V1 CUDA-graph hang on ROCm)
- `https://github.com/vllm-project/vllm/issues/41622` (V1 piecewise capture crash)
- `https://github.com/vllm-project/vllm/issues/38587` (RCCL TP=2 on gfx1100)
- `https://github.com/Alberto-Codes/turboquant-vllm` (third-party plugin origin)
