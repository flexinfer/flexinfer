# RALPH: gfx906 llama.cpp HIP memory-info probe

Date: 2026-05-21
Branch: `codex/gfx906-llamacpp-meminfo-probe`

## Review

- Roadmap milestone: gfx906 conservative production lane / llama.cpp substrate.
- Spec section(s): `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md`.
- Prior decisions to preserve:
  - vLLM on gfx906 is feasibility-only after standalone HIP probes segfaulted
    below the vLLM layer.
  - Production gfx906 textgen should use llama.cpp if the ROCm memory-info
    path can be isolated and stabilized.
  - The 24h llama.cpp soak cannot start until the qwen3-8b Radeon VII crash is
    isolated.

## Align

- Slice name: gfx906 llama.cpp `hipMemGetInfo` isolation probe.
- Scope in:
  - Add a standalone debug Job that uses the same
    `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3` image as the
    GPUProfile.
  - Compile and run a minimal HIP `hipMemGetInfo` probe inside that image.
  - Compare current profile env, no `HSA_OVERRIDE_GFX_VERSION`,
    `ROCR_VISIBLE_DEVICES=0`, and `HIP_VISIBLE_DEVICES=0` +
    `GPU_DEVICE_ORDINAL=0`.
- Scope out:
  - No production Model, GPUProfile, runtime digest, or alias changes.
  - No 24h soak, because the qwen3-8b load failure must be isolated first.
- Acceptance criteria:
  - Debug manifest validates as Kubernetes YAML.
  - Probe logs print per-case exit markers even when a scenario fails.
  - Results distinguish image/ROCm memory-info failure from FlexInfer
    model-load behavior.
- Dependencies/blockers:
  - Requires live access to `cblevins-radeonvii`.
  - If `hipcc` is absent from the llama.cpp image, the Job exits with an
    explicit `compile_skipped missing_hipcc` result and the next slice should
    switch to a precompiled probe or run `llama-server` directly against a tiny
    GGUF.

## Land

- Planned file areas:
  - `deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml`
  - `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md`
  - `.loom/ralph-gfx906-llamacpp-meminfo-probe-2026-05-21.md`
- Implementation steps:
  1. Add the probe manifest.
  2. Record why this is a pre-soak gate in the spec.
  3. Validate YAML and targeted repository tests.

## Live Evidence

- Applied `deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml` to
  `flexinfer-system`.
- Pod scheduled on `cblevins-radeonvii`.
- Image pulled successfully:
  `registry.harbor.lan/library/llamacpp@sha256:b6b4c5db0fadda8068272afff86d5a67a8330ca24c5480a13a1432070b99bc16`.
- `llama-server --version` detected:
  - `Device 0: AMD Radeon VII`
  - `gfx906:sramecc+:xnack-`
  - `VMM: no`
  - `Wave Size: 64`
- The probe compiled successfully after forcing
  `HIP_PATH=/opt/rocm` and `--rocm-path=/opt/rocm`.
- Every environment variant reached `hipGetDeviceCount`,
  `hipSetDevice(0)`, and `hipGetDeviceProperties(0)` successfully.
- Every environment variant failed at the same call:
  - `hipMemGetInfo=1:invalid argument`
  - `free_bytes=0`
  - `total_bytes=0`

Per-case exits:

| Case | Result |
|---|---|
| `current-profile` | exit `4`, `hipMemGetInfo=invalid argument` |
| `no-gfx-override` | exit `4`, `hipMemGetInfo=invalid argument` |
| `rocr-visible-only` | exit `4`, `hipMemGetInfo=invalid argument` |
| `hip-visible-and-ordinal` | exit `4`, `hipMemGetInfo=invalid argument` |

## Outcome

The qwen3-8b llama.cpp failure is reproduced below FlexInfer and below model
loading. `hipMemGetInfo` itself is not viable on Radeon VII in the current
llama.cpp image, independent of the tested profile/device-visibility env
variants.

The next slice should patch or rebuild the llama.cpp image so the upstream
`ggml_backend_cuda_device_get_memory` path does not abort when
`hipMemGetInfo` returns `hipErrorInvalidValue`. Do not start the 24h soak or
promote `qwen3-8b-radeonvii` aliases until that image-level compatibility path
passes this probe.

## Prove

- Tests to run:
  - `kubectl apply --dry-run=client -f deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml`
  - `go test ./backend ./pkg/runtime ./internal/runtime`
- Lint/static checks:
  - `git diff --check`
- CI checks:
  - Branch/MR pipeline after push.

## Handoff/Harvest

- Docs to update:
  - Add the pre-soak gate to the llama.cpp production-lane spec.
- Agent-context entries to add:
  - Decision: isolate `hipMemGetInfo` with a standalone HIP probe before any
    production alias or soak changes.
- Next-slice candidates:
  1. Run the probe live on `cblevins-radeonvii` and capture evidence.
  2. If probe fails in all variants, patch or rebuild the llama.cpp image around
     `hipMemGetInfo`.
  3. If probe passes, rerun qwen3-8b with narrower llama.cpp memory-fit/model
     load flags.
