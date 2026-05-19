# RALPH: gfx906 vLLM Worker Diagnostics

Date: 2026-05-19
Branch: `codex/gfx906-vllm-worker-diagnostics`

## Review

- Roadmap milestone: ROCm `gfx1100`/`gfx906` platform enhancements, especially
  RG-3/RG-4 validation evidence for the `gfx906` textgen canary lane.
- Spec sections: `.loom/60-validation-matrix.md`,
  `.loom/ralph-gfx906-vllm-smoke-2026-05-17.md`,
  `docs/planning/rocm-gfx1100-gfx906-platform-slice.md`.
- Prior decisions to preserve:
  - Keep `gfx906` vLLM `experimental` until a real HTTP 200 canary response.
  - Do not spend another loop changing model/profile flags until the worker
    crash at OPT-125M weight load is visible or patched in the image.
  - Keep the Radeon VII canary scaled to zero and canary-scoped.

## Align

- Slice name: gfx906 vLLM worker diagnostics.
- Scope in:
  - Move the standalone gfx906 vLLM image compatibility hooks into a tracked,
    testable installer script.
  - Add Python faulthandler, C++ stack-trace env, and multiprocessing child
    exception logging so the next canary image surfaces the OPT weight-load
    failure instead of only reporting `Engine process failed to start`.
  - Add a static contract check so future edits keep the diagnostics wired.
  - Record the next smoke expectation in the validation docs.
- Scope out:
  - Promoting `gfx906` vLLM support.
  - Changing the live GPUProfile image digest before CI publishes a new image.
  - Re-running cluster canaries from this source-only slice.
  - Retrying Qwen-family SWA/rope flags.
- Acceptance criteria:
  - `build/Dockerfile.vllm-rocm-gfx906` installs the same compatibility hooks
    plus worker diagnostics from a tracked script.
  - The installer compiles and can install/import hooks in a temporary
    site-packages directory without requiring Torch, Triton, or Transformers.
  - `scripts/check-runtime-patch-contracts.py` fails if the diagnostics wiring
    is removed.
  - Docs state that the next live smoke should use the newly published digest
    to capture the child-process traceback or faulthandler output.
- Dependencies/blockers:
  - The actual fix still depends on a rebuilt/published
    `registry.harbor.lan/flexinfer/vllm:rocm-gfx906` digest and a live Radeon
    VII smoke.
  - Agent-context MCP was unavailable during review; tracked `.loom` docs are
    the continuity source for this loop.

## Land

- Planned file areas:
  - `build/Dockerfile.vllm-rocm-gfx906`
  - `build/scripts/install_vllm_gfx906_compat.py`
  - `scripts/check-runtime-patch-contracts.py`
  - `.gitlab/ci/runtime-publish.yml`
  - `.loom/60-validation-matrix.md`
- Implementation steps:
  1. Extract compatibility hooks from the Dockerfile into an installer script.
  2. Add worker diagnostics and Dockerfile env for tracebacks.
  3. Add static checks and validation-matrix handoff notes.

## Prove

- Tests to run:
  - `python3 -m py_compile build/scripts/install_vllm_gfx906_compat.py scripts/check-runtime-patch-contracts.py`
  - `python3 build/scripts/install_vllm_gfx906_compat.py --target /tmp/flexinfer-gfx906-vllm-hooks`
  - `PYTHONPATH=/tmp/flexinfer-gfx906-vllm-hooks python3 -c 'import flexinfer_vllm_worker_diagnostics'`
  - `python3 scripts/check-runtime-patch-contracts.py`
- Lint/static checks:
  - `git diff --check`
  - `kubectl kustomize deploy/models`
- CI checks:
  - Run normal MR pipeline.
  - Manually trigger `publish_vllm_rocm_gfx906` after merge if the source
    pipeline passes, then pin the new digest only after the live smoke captures
    useful failure detail or reaches HTTP 200.

## Handoff/Harvest

- Docs to update:
  - `.loom/60-validation-matrix.md`
  - `.loom/ralph-gfx906-vllm-smoke-2026-05-17.md`
- Agent-context entries to add:
  - Finding: OPT-125M currently dies in the vLLM worker at weight load with no
    child traceback in the parent logs.
  - Decision: add diagnostics to the image before another model/profile retry.
- Next-slice candidates:
  - Publish the diagnostic gfx906 vLLM image, update GPUProfile with its digest,
    and rerun the OPT canary to capture the child failure.
  - If diagnostics reveal a Python/Torch/VLLM exception, patch the image at the
    narrow failing call and repeat the OPT canary.
