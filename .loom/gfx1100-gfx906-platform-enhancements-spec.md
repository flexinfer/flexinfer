# gfx1100/gfx906 Platform Enhancements Spec

Date: 2026-05-06

## Goal

Make AMD `gfx1100` and `gfx906` first-class platform lanes across FlexInfer: operators should be able to choose either GPU class and get predictable backend selection, runtime images, quantization behavior, validation gates, and rollback paths without re-reading incident notes.

## Non-Goals

- Do not make `gfx906` match `gfx1100` throughput or model capacity.
- Do not promote every experimental runtime profile to default.
- Do not replace live cluster canaries with unit tests; GPU runtime behavior still needs hardware evidence.
- Do not broaden scope to Maxwell/CUDA except where shared abstractions require compatibility.

## Users / Operators

- Homelab operator running RX 7900 XTX (`gfx1100`) and Radeon VII / Vega20 (`gfx906`) nodes.
- FlexInfer maintainer promoting runtime images and quantizer images by digest.
- Future agents implementing platform slices with source-backed acceptance criteria.

## Current Evidence

- `GPUProfileSpec` is explicitly intended to replace hardcoded per-arch logic across backends, controllers, and quantization packages, and already includes features, backend support, env vars, memory budgets, quantization images, runtime image, and image-pull policy fields (`api/v1alpha2/gpuprofile_types.go:24`-`99`).
- The `GPUProfileReconciler` caches profiles by architecture and offers `LookupOrFetch` so other reconcilers can use profile data after restarts (`controllers/gpuprofile_controller.go:34`-`99`).
- The backend interface still asks each backend for an image based on vendor and architecture, so runtime/profile image selection is split between backend code and GPUProfile data (`backend/interface.go:138`-`152`).
- `gfx1100` runtime config is broad: vLLM, llama.cpp, Ollama, diffusers, Steam, bitsandbytes, and quantizer packages are enabled; it uses a ROCm 7.2 Navi vLLM base with ROCm 6.4 dev image and RDNA3 env defaults (`build/runtime.yaml:20`-`54`).
- `gfx906` runtime config is narrower: vLLM is disabled because the PyTorch 2.3 base is too old, llama.cpp/Ollama/diffusers are enabled, bitsandbytes is not included by default, and Vega20 stability env vars are set (`build/runtime.yaml:215`-`238`).
- The deployed `gfx1100` GPUProfile marks vLLM, llama.cpp, diffusers, Ollama, ComfyUI, and MLC-LLM as full support, with digest-pinned runtime and quantizer images (`deploy/gpuprofiles/gfx1100.yaml:32`-`52`, `deploy/gpuprofiles/gfx1100.yaml:88`-`112`).
- The deployed `gfx906` GPUProfile marks vLLM, llama.cpp, and Ollama as full, diffusers/ComfyUI/MLC-LLM as experimental, and pins runtime/quantizer digests (`deploy/gpuprofiles/gfx906.yaml:29`-`41`, `deploy/gpuprofiles/gfx906.yaml:76`-`88`).
- There is a support mismatch to resolve: `build/runtime.yaml` disables `gfx906` vLLM while `deploy/gpuprofiles/gfx906.yaml` declares vLLM full support (`build/runtime.yaml:222`-`227`, `deploy/gpuprofiles/gfx906.yaml:29`-`31`).
- FLUX docs already distinguish `gfx1100` and `gfx906` memory behavior: `gfx1100` can run NF4 without CPU offload; `gfx906` needs CPU offload, conservative `512x512` warmups, source-built bitsandbytes, and Vega20 env vars (`docs/user/backends-rocm-gfx1100.md:417`-`460`).
- GPTQ docs capture a major `gfx906` difference: Cholesky recovery is much slower and HIP failures can poison the ROCm context, while `gfx1100` has native GPU LAPACK performance (`docs/user/gptq-quantization-runbook.md:32`-`63`, `docs/user/gptq-quantization-runbook.md:67`-`82`).
- Runtime digest promotion already has a script and doc path that update GPUProfile manifests and Helm runtime profiles after a validated image digest is selected (`scripts/promote-runtime-digest.sh:1`-`40`, `docs/dev/runtime-digest-promotion.md:1`-`52`).
- The spec-driven delivery guide requires PR-sized slices, target modules, validation commands, rollout/backout notes, and a validation matrix for runtime canaries (`docs/planning/spec-driven-delivery.md:22`-`63`).

## Requirements

1. **Single Capability Contract**
   - GPUProfile must become the authoritative source for per-arch backend support, runtime image digests, env injection, memory budgets, and quantization/runtime gates.
   - Any remaining backend hardcoding for `gfx1100`/`gfx906` must either consume GPUProfile data or document why it cannot.

2. **Backend Support Truth**
   - Resolve support-level mismatches between `build/runtime.yaml`, `deploy/gpuprofiles/*.yaml`, Helm values, docs, and examples.
   - Support levels must distinguish `full`, `experimental`, `canary`, and `unsupported` or an equivalent state model with clear promotion criteria.

3. **Runtime Promotion Discipline**
   - Runtime tags can be built freely, but cluster consumers should use digest-pinned images after canary validation.
   - Promotion records must include profile, backend, image digest, model/artifact, GPU class, canary command, result, and rollback digest.

4. **gfx1100 Next Capabilities**
   - Stabilize the Navi vLLM lane for Qwen3.5/Gemma4, TurboQuant, and long-context constraints.
   - Preserve image-generation gains: NF4 FLUX, multipart image edits, warmup strategy, and 1024px canaries.

5. **gfx906 Next Capabilities**
   - Make the Radeon VII lane honest and useful: strong llama.cpp/Ollama/GPTQ/abliteration support, conservative diffusers support, explicit vLLM status, and documented memory/time defaults.
   - Source-built bitsandbytes and ROCm env behavior must be repeatable or clearly excluded from default profiles.

6. **Full-Platform Observability**
   - Operators should see which GPUProfile and runtime digest a model is using.
   - Metrics/dashboards should separate runtime readiness, image digest, backend support level, GPU arch, cold-load timing, and canary status.

7. **Validation Matrix**
   - `.loom/60-validation-matrix.md` should evolve from a mostly gfx1100 quantization table into the canonical matrix for `gfx1100` and `gfx906` runtime promotion evidence.

## Acceptance Criteria

- A clean diff can show one authoritative support matrix for `gfx1100` and `gfx906` across GPUProfile, build matrix, Helm values, docs, and examples.
- `gfx906` vLLM is either supported by a validated runtime canary or downgraded from `full` with a tracked path back to promotion.
- `scripts/promote-runtime-digest.sh` can promote or dry-run both profiles, including any profile-specific model manifests listed in `build/runtime.yaml`.
- At least four canary rows are recorded in `.loom/60-validation-matrix.md`: `gfx1100` textgen, `gfx1100` imagegen, `gfx906` textgen/quantization, and `gfx906` imagegen/offload.
- Controller/unit tests cover profile lookup, image/env precedence, backend support gating, and memory budget selection for both arches.
- Operator docs explain what is safe by default, what is experimental, and how to roll back a bad runtime digest.

## Validation Plan

- Local tests:
  - `go test ./api/v1alpha2/... ./backend/... ./controllers/... ./pkg/quantization/... ./internal/proxy/...`
  - `scripts/test-promote-runtime-digest.sh`
  - `make manifests` when CRD fields change
- Static consistency checks:
  - Compare `build/runtime.yaml` profiles with `deploy/gpuprofiles/*.yaml`, `deploy/system/values-k3s.yaml`, docs, and examples.
  - Run `git diff --check`.
- Cluster canaries:
  - `gfx1100` vLLM textgen smoke with decode/prompt TPS and long-context ceiling.
  - `gfx1100` diffusers FLUX text-to-image and Fill/edit smoke at served warmup sizes.
  - `gfx906` llama.cpp or Ollama textgen smoke plus GPTQ/abliteration status check.
  - `gfx906` diffusers offload smoke at `512x512`.

## Rollout / Backout

- Roll out by profile, not by all runtimes at once.
- Promote image digests with `scripts/promote-runtime-digest.sh <profile> --digest <sha256> --apply`.
- Reconcile Flux only after validation matrix rows are updated.
- Back out by re-running the promotion script with the previous digest, then reconciling Helm/GPUProfile manifests.

## Open Questions

- Should `canary` be a first-class `BackendProfile.support` value, or should canary state live in status/annotations to avoid CRD schema churn?
- Should `build/runtime.yaml` generate GPUProfile manifests to prevent drift, or should a consistency test be enough?
- Is `gfx906` vLLM strategically worth reviving, or should the next round explicitly steer Vega20 toward llama.cpp/Ollama/GPTQ plus limited diffusers?
- Where should runtime canary evidence live long-term: `.loom/60-validation-matrix.md`, a public docs file, GPUProfile status, or all three at different levels of detail?

## Sources

- `api/v1alpha2/gpuprofile_types.go:24`
- `api/v1alpha2/gpuprofile_types.go:54`
- `api/v1alpha2/gpuprofile_types.go:86`
- `api/v1alpha2/gpuprofile_types.go:90`
- `controllers/gpuprofile_controller.go:34`
- `controllers/gpuprofile_controller.go:55`
- `backend/interface.go:138`
- `build/runtime.yaml:20`
- `build/runtime.yaml:215`
- `deploy/gpuprofiles/gfx1100.yaml:32`
- `deploy/gpuprofiles/gfx1100.yaml:88`
- `deploy/gpuprofiles/gfx906.yaml:29`
- `deploy/gpuprofiles/gfx906.yaml:76`
- `docs/user/backends-rocm-gfx1100.md:417`
- `docs/user/backends-rocm-gfx1100.md:449`
- `docs/user/gptq-quantization-runbook.md:32`
- `scripts/promote-runtime-digest.sh:36`
- `docs/planning/spec-driven-delivery.md:31`
