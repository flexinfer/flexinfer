---
title: ROCm gfx1100/gfx906 Platform Slice
description: Capability matrix and first reconciliation slice for AMD ROCm GPU lanes.
---

# ROCm gfx1100/gfx906 Platform Slice

> Last updated: 2026-05-06

This plan turns the `.loom` `gfx1100/gfx906` platform spec into shippable
increments. The first increment is intentionally conservative: make the current
support truth consistent before changing APIs or building new runtime images.

## Iteration Plan

### Review

- Roadmap milestone: AMD ROCm `gfx1100` and `gfx906` full-platform enhancement round.
- Spec sections: `.loom/gfx1100-gfx906-platform-enhancements-spec.md`,
  `.loom/gfx1100-gfx906-platform-enhancements-plan.md`.
- Prior decisions to preserve:
  - Spec capsules are the default for multi-file feature delivery.
  - Runtime promotion must be digest-pinned after canary validation.
  - `gfx906` is a conservative lane with tighter memory and slower GPTQ recovery.

### Align

- Slice name: Capability Matrix Reconciliation.
- Scope in:
  - Checked-in capability matrix for `gfx1100` and `gfx906`.
  - Resolve the current `gfx906` vLLM support mismatch.
  - Document which lanes are full, experimental, and canary-only.
- Scope out:
  - CRD schema changes.
  - Runtime image rebuilds.
  - Cluster canaries.
  - Metrics/dashboard label changes.
- Acceptance criteria:
  - `gfx906` vLLM is no longer documented as a full default lane.
  - The matrix distinguishes runtime default support from dedicated canary image paths.
  - Rollback is docs/profile-only and does not require CRD conversion.
- Dependencies/blockers:
  - Live canaries are still required before promoting `gfx906` vLLM or MLC-LLM.
  - Agent-context was unavailable during this loop (`Transport closed`), so handoff is captured in this document.

### Land

- Planned file areas:
  - `deploy/gpuprofiles/gfx906.yaml`
  - `build/README-gfx906.md`
  - `docs/planning/rocm-gfx1100-gfx906-platform-slice.md`
  - `docs/planning/next-roadmap.md`
- Implementation steps:
  1. Demote `gfx906` vLLM from `full` to `experimental` in the deployed GPUProfile.
  2. Update the gfx906 backend guide to reflect current runtime support and required Vega20 env vars.
  3. Add this support matrix and link it from the next roadmap.

### Prove

- Tests to run:
  - `git diff --check`
  - `rg -n "gfx906|vLLM|runtime:rocm-gfx906|support:" deploy/gpuprofiles/gfx906.yaml build/README-gfx906.md docs/planning/rocm-gfx1100-gfx906-platform-slice.md`
- Lint/static checks:
  - YAML parse for changed manifests when local tooling is available.
- CI checks:
  - Not required for docs/profile-only planning slice until staged in an MR.

### Handoff/Harvest

- Docs to update:
  - `.loom/gfx1100-gfx906-platform-enhancements-plan.md`
  - `.loom/50-worklog.md`
- Agent-context entries to add:
  - Finding: `gfx906` vLLM was documented/profiled as full while the unified runtime disables it.
  - Decision: demote to experimental until hardware canary and digest promotion.
- Next-slice candidates:
  - Runtime promotion consistency test for `build/runtime.yaml` vs GPUProfiles.
  - Validation matrix expansion for runtime canary evidence.
  - `gfx906` textgen/offload canary jobs.

## Capability Matrix

| GPU lane | Runtime profile | Full/default lanes | Experimental or canary lanes | Required guardrails |
|---|---|---|---|---|
| `gfx1100` RDNA3 | `runtime:rocm-gfx1100` / digest-pinned GPUProfile runtime | vLLM, llama.cpp, Ollama, diffusers, MLC-LLM | Gemma4/TurboQuant and long-context variants remain canary until runtime evidence is captured | `HSA_OVERRIDE_GFX_VERSION=11.0.0`, `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1`, dGPU-only device indices on mixed iGPU hosts |
| `gfx906` Vega20 | `runtime:rocm-gfx906` / digest-pinned GPUProfile runtime | llama.cpp, Ollama, GPTQ/abliteration workflows | vLLM, MLC-LLM, diffusers, ComfyUI | `HSA_OVERRIDE_GFX_VERSION=9.0.6`, `HSA_ENABLE_SDMA=0`, `HSA_USE_SVM=0`, CPU offload for imagegen, conservative warmups |

## Current Reconciliation Notes

- `build/runtime.yaml` disables `vllm` in the `gfx906` unified runtime because
  the current PyTorch 2.3 base is too old for that lane.
- `deploy/gpuprofiles/gfx906.yaml` now marks `vllm` experimental, matching the
  runtime reality while preserving a path for dedicated image canaries.
- `build/README-gfx906.md` now treats vLLM and MLC-LLM as canary/experimental
  instead of full default lanes.
- `gfx906` docs now match the deployed/runtime Vega20 env requirements:
  `HSA_OVERRIDE_GFX_VERSION=9.0.6`, `HSA_ENABLE_SDMA=0`, `HSA_USE_SVM=0`, and
  `PYTORCH_ROCM_ARCH=gfx906`.

## Rollback

Revert this slice if live evidence proves the previous `gfx906` vLLM full-support
claim is already valid. Otherwise, the promotion path is to validate a dedicated
vLLM image on Radeon VII, record the canary in `.loom/60-validation-matrix.md`,
promote the image digest, and then raise support from experimental to full.

## Sources

- `.loom/gfx1100-gfx906-platform-enhancements-spec.md`
- `.loom/gfx1100-gfx906-platform-enhancements-plan.md`
- `build/runtime.yaml`
- `deploy/gpuprofiles/gfx906.yaml`
- `build/README-gfx906.md`
