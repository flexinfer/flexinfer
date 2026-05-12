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
