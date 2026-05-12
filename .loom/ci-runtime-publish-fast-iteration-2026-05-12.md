# RALPH Iteration Plan

## Review

- Roadmap milestone: Delivery acceleration / quant backlog reliability.
- Spec section(s): `.loom/30-implementation-plan.md` delivery loops; `.loom/brainstorm-ci-speed-2026-05-12.md`.
- Prior decisions to preserve:
  - Homelab GitOps still consumes `registry.harbor.lan/flexinfer/runtime:rocm-gfx1100`.
  - Runtime image publish should remain available for explicit web/API rebuilds.
  - Root `.gitlab-ci.yml` edits should not trigger multi-hour runtime image builds unless they actually change runtime publish inputs.

## Align

- Slice name: `gfx1100` runtime publish trigger/cache tightening.
- Scope in:
  - Move `publish_unified_rocm_gfx1100` into a dedicated CI include.
  - Remove broad root `.gitlab-ci.yml` from that job's `rules:changes`.
  - Add an explicit BuildKit registry cache ref for the `gfx1100` runtime image.
  - Raise the job timeout enough to seed the explicit cache on intentional runtime publish runs.
- Scope out:
  - Splitting the kitchen-sink runtime into multiple personas.
  - Changing controller image selection or Helm runtime defaults.
  - Runner/BuildKit infrastructure changes.
- Acceptance criteria:
  - Root `.gitlab-ci.yml` can change without matching `publish_unified_rocm_gfx1100` by path alone.
  - Runtime-publish CI changes remain triggerable through `.gitlab/ci/runtime-publish.yml`.
  - BuildKit imports an explicit cache ref before falling back to the runtime image tag.
  - BuildKit exports `mode=max` cache metadata to the explicit cache ref.
  - GitLab CI config validates.
- Dependencies/blockers:
  - Loom MCP agent-context writes are currently blocked by `Transport closed`.
  - The first intentional runtime publish after this change may still be long because it has to seed the new cache.

## Land

- Planned file areas:
  - `.gitlab-ci.yml`
  - `.gitlab/ci/runtime-publish.yml`
  - `.loom/`
- Implementation steps:
  1. Include `.gitlab/ci/runtime-publish.yml` from root CI.
  2. Move the `publish_unified_rocm_gfx1100` job into that include.
  3. Replace inline cache-only export with explicit registry cache import/export.
  4. Preserve manual web/API rebuild path.

## Prove

- Tests to run:
  - `glab ci lint --include-jobs`
  - `git diff --check`
- Lint/static checks:
  - CI YAML syntax via GitLab lint.
- CI checks:
  - MR pipeline should validate config and run regular checks.
  - Master should no longer run `publish_unified_rocm_gfx1100` for unrelated root CI edits after this include split is merged.

## Handoff/Harvest

- Docs to update:
  - This iteration note.
  - CI-speed brainstorm.
- Agent-context entries to add:
  - Blocked: Loom MCP transport closed; add later if the server recovers.
- Next-slice candidates:
  - Split `gfx1100` runtime personas so quantizer/Steam/llama.cpp do not all rebuild on serving-runtime changes.
  - Prebuild llama.cpp HIP artifacts or a builder image keyed by `LLAMACPP_VERSION` and `AMDGPU_TARGETS`.
  - Add a CI duration ledger for BuildKit phase timings.

## Follow-up: Master Runtime Publish Failure

MR !335 merged and MR pipeline `9041` passed, but master pipeline `9048`
failed in `publish_unified_rocm_gfx1100` after 2,817 seconds. The job no
longer failed from the old 2h timeout; instead, the runtime build reached
`build/scripts/vllm_gemma4_moe_gptq_patch.py` and exited because the Gemma4
patch script treated a missing `GPTQLinearMethod.apply` pattern as fatal even
though `build/scripts/vllm_qwen35_patches_nodiag.py` had already applied the
equivalent ROCm GPTQ reference fallback marker.

Follow-up acceptance:
- `vllm_gemma4_moe_gptq_patch.py` treats
  `FLEXINFER_QWEN35_GPTQ_ROCM_REFERENCE_PATCH` as an equivalent already-applied
  GPTQ ROCm fallback.
- The script remains fatal when neither the Gemma4 marker, the Qwen3.5 marker,
  nor the expected unpatched function body is present.
- The next master runtime publish should proceed past
  `vLLM Gemma4 MoE GPTQ patch applied at build time`.

## Follow-up: Runtime Trigger Completeness

Master pipeline `9068` for the marker fix did not run
`publish_unified_rocm_gfx1100` because `.gitlab/ci/runtime-publish.yml`
tracked `vllm_qwen35_patches_nodiag.py` but not
`vllm_gemma4_moe_gptq_patch.py`. The runtime publish trigger list must include
every build-time runtime patch script that can affect the `gfx1100` image.

## Follow-up: Gemma4 Missing-Model No-op

Master pipeline `9072` for the trigger fix correctly ran
`publish_unified_rocm_gfx1100`. It proved the Qwen marker fix worked:
`vllm_gemma4_moe_gptq_patch.py` detected
`FLEXINFER_QWEN35_GPTQ_ROCM_REFERENCE_PATCH` and continued. The next failure was
`FAILED — MLP fp16 clamp patch failed` because the pinned wheel runtime reported
`Gemma4 model files: none`, so `gemma4.py` was absent and the file-specific MLP
clamp helper returned failure.

Follow-up acceptance:
- Gemma4 file-specific helpers treat an absent `gemma4.py` as a successful
  no-op for wheel runtimes that do not ship Gemma4 model files.
- The script remains fatal for shared quantization/runtime patches that should
  exist in the installed vLLM tree.
- The next master runtime publish should proceed past
  `vLLM Gemma4 MoE GPTQ patch applied at build time`.
