# Docker Major Dependency Rollout

Tracking issue: [#21](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/21)

Status: staged; implementation is intentionally split from routine Renovate
minor/patch batches.

## Goal

Move Docker base-image majors through isolated image lanes so Python, CUDA,
ROCm, and PyTorch changes do not land as an undifferentiated dependency batch.
The rollout covers these families:

- Python 3.14 for lightweight Python utility images and any derived build tools.
- CUDA 12.9 for NVIDIA quantizer/runtime images, excluding Maxwell lanes.
- ROCm 6.4 for MLC-LLM images and ROCm build environments.
- PyTorch-bearing ROCm images where the base tag also changes the Python or
  ROCm ABI.

## Non-Goals

- No cluster-wide default image flip in the same MR as a base-image build.
- No Maxwell CUDA 12.x migration. GTX 980 Ti / `sm_52` stays on CUDA 11.8
  because that is the documented compatible lane.
- No `gfx906` promotion from experimental to default support as part of the
  dependency refresh. Runtime promotion still requires canary evidence in the
  validation matrix.
- No scheduler/controller contract changes unless a runtime canary proves a
  compatibility break that cannot be contained in image tags or GPUProfiles.

## Current Evidence

- Routine minor/patch Docker updates have already landed separately in
  `docs/planning/next-roadmap.md`.
- `build/README-rocm.md` documents the ROCm 6.4 MLC source-build path and the
  `gfx1100` environment contract.
- `build/README-gfx906.md` documents the fragile `gfx906` lane and the reason
  vLLM remains canary-only.
- `build/README-maxwell.md` documents the Maxwell CUDA 11.8 constraint.
- `.gitlab/ci/runtime-publish.yml` keeps long-running runtime-image publish jobs
  isolated and includes manual break-glass jobs for heavyweight ROCm rebuilds.

## Rollout Sequence

| Phase | Scope | Target files | Gate |
|-------|-------|--------------|------|
| 0 | Inventory and pin current known-good tags | `build/Dockerfile.*`, `.gitlab/ci/runtime-publish.yml`, `build/runtime.yaml`, `deploy/system/values-k3s.yaml` | `git diff --check`; record current image tags/digests before editing |
| 1 | Python 3.14 utility-image spike | `build/Dockerfile.model-tools`, `build/Dockerfile.quantizer-gguf`, CI helper images if used by scripts | Build image; run script smoke tests that import the packaged CLIs; do not touch GPU images |
| 2 | CUDA 12.9 NVIDIA modern lane | `build/Dockerfile.quantizer-gptq`, `build/Dockerfile.quantizer-awq`, any non-Maxwell CUDA runtime path | Build image; run quantizer import smoke; run one small GPTQ/AWQ dry-run or fixture-backed validation |
| 3 | ROCm 6.4 MLC lane | `build/Dockerfile.mlc-rocm64-full`, `build/Dockerfile.mlc-rocm64-gfx1100`, `build/Dockerfile.mlc-rocm64-gfx906` | Build image; run MLC import/compile smoke; run `q4f32_1` model smoke on `gfx1100`; keep `gfx906` canary-only |
| 4 | PyTorch-bearing ROCm image refresh | `build/Dockerfile.diffusers-rocm*`, `build/Dockerfile.comfyui-rocm-*`, `build/Dockerfile.finetune*`, ROCm quantizer images | Build image; run backend import smoke; run one `gfx1100` textgen or imagegen canary per backend touched |
| 5 | Runtime publish and promotion | `.gitlab/ci/runtime-publish.yml`, `build/runtime.yaml`, `deploy/system/values-k3s.yaml`, `.loom/60-validation-matrix.md` | Publish commit-specific tags first; promote stable tags only after canary evidence is linked |

## Build/Test Matrix

| Lane | Hardware | Images | Required checks |
|------|----------|--------|-----------------|
| NVIDIA modern | CUDA-capable non-Maxwell node | GPTQ/AWQ quantizer images and any CUDA runtime images moved to 12.9 | Docker build, `python -c 'import torch'`, CUDA availability smoke, quantizer fixture/dry-run |
| NVIDIA Maxwell | `cblevins-gtx980ti` / `sm_52` | `build/Dockerfile.mlc-cuda-maxwell`, `build/Dockerfile.llamacpp-cuda-maxwell`, `build/Dockerfile.ollama-cuda-maxwell` | No CUDA 12.9 change; verify docs still state CUDA 11.8 and FP32/compiled-library constraints |
| AMD `gfx1100` | `cblevins-5930k`, `cblevins-7900xtx` | ROCm MLC, vLLM, diffusers, ComfyUI, finetune, quantizer images | Docker build, `python -c 'import torch; print(torch.version.hip)'`, backend import, one direct inference/image canary |
| AMD `gfx906` | Radeon VII lane | `build/Dockerfile.*gfx906`, unified `gfx906` runtime paths | Docker build where supported, import smoke, conservative canary only; do not promote default support without validation-matrix evidence |
| CI publish | BuildKit runner | Runtime publish jobs in `.gitlab/ci/runtime-publish.yml` | Manual pipeline first, commit-specific tag pushed, stable tag update only after hardware canary passes |

## Tagging and Rollback

- Publish every major-base candidate with a commit-specific tag before moving a
  stable tag, for example `runtime:rocm-gfx1100-<short-sha>` or
  `quantizer:gptq-cuda129-<short-sha>`.
- Keep the previous stable tag in `deploy/system/values-k3s.yaml` or
  `build/runtime.yaml` until the new tag has hardware evidence.
- Roll back by reverting the values/runtime-profile tag change, not by mutating
  a pushed image in place.
- If a stable tag must move, record the old digest in the MR description and in
  `.loom/60-validation-matrix.md` so Flux can be returned to the prior digest.
- For ROCm lanes, roll back all coupled components together when the base image
  changes Python, HIP, PyTorch, or vLLM at the same time. Mixed ABI rollbacks are
  not a supported state.

## Acceptance Criteria

- Major Docker updates remain outside routine Renovate minor/patch batches.
- Each touched image lane has an explicit hardware or import smoke before stable
  tag promotion.
- Maxwell remains pinned to CUDA 11.8 unless a separate hardware-supported plan
  supersedes `build/README-maxwell.md`.
- `gfx906` remains canary-gated unless validation evidence promotes it.
- `docs/planning/next-roadmap.md` links this rollout capsule as the staged plan
  for issue `#21`.

## Ready-for-Implementation Slices

1. Python utility images: update only Python-based non-GPU helper images, build
   them locally or in CI, and run import smoke tests.
2. CUDA 12.9 modern NVIDIA quantizer images: update one quantizer path, publish
   a commit-specific tag, and run a fixture-backed quantizer check before adding
   another CUDA image.
3. ROCm 6.4 MLC images: update/publish MLC candidates and validate `gfx1100`
   `q4f32_1` inference before considering `gfx906`.
4. PyTorch-bearing ROCm images: refresh one backend family at a time and require
   a direct canary on the target GPU architecture.
