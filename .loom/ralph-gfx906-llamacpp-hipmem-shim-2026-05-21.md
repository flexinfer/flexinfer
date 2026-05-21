# RALPH Iteration Plan

Date: 2026-05-21
Slice: gfx906 llama.cpp HIP memory-info shim

## Review

- Roadmap milestone: unblock the Radeon VII / `gfx906` production fallback lane before any soak, alias promotion, or vLLM closeout work continues.
- Spec sections: `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md` and `.loom/60-validation-matrix.md`.
- Prior decisions to preserve: MR !466 proved the standalone llama.cpp probe reached ROCm and failed because raw `hipMemGetInfo` returned `hipErrorInvalidValue`; model-load retry remains blocked until this minimal probe passes.

## Align

- Slice name: image-level `hipMemGetInfo` compatibility shim for the standalone gfx906 llama.cpp image.
- Scope in: `build/Dockerfile.llamacpp-rocm-gfx906`, a small preloaded shim, the debug probe image reference, and validation-matrix evidence.
- Scope out: model-load soak, default/fallback alias promotion, and changing the reconciled CPU fallback router.
- Acceptance criteria:
  - Candidate image builds and is pushed with an immutable digest.
  - `deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml` completes on `cblevins-radeonvii`.
  - Probe logs show `hipMemGetInfo`, `hipMalloc4096`, and post-malloc `hipMemGetInfo` all returning success.
  - Matrix records the digest, rollback, and remaining soak gate.
- Dependencies/blockers: requires Harbor push access and the Radeon VII node to pull the candidate image.

## Land

- Planned file areas:
  - `build/hipmemgetinfo_shim.cpp`
  - `build/Dockerfile.llamacpp-rocm-gfx906`
  - `deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml`
  - `.loom/60-validation-matrix.md`
- Implementation steps:
  1. Add an `LD_PRELOAD` shim that calls the real HIP function first, then falls back to sysfs VRAM totals when ROCm returns an invalid value.
  2. Bake the shim into the standalone gfx906 llama.cpp image and preload it by default.
  3. Pin the debug probe to the pushed digest and run the live kill-test.

## Prove

- Tests to run:
  - `docker build --check -f build/Dockerfile.llamacpp-rocm-gfx906 .`
  - `docker build -f build/Dockerfile.llamacpp-rocm-gfx906 -t registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim .`
  - `docker push registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim`
  - `kubectl apply --dry-run=client -f deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml`
  - `kubectl apply -f deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml`
  - `kubectl -n flexinfer-system wait --for=condition=complete --timeout=600s job/gfx906-llamacpp-hipmeminfo-probe`
- Lint/static checks:
  - `git diff --check`
- CI checks:
  - Push branch and use standard MR pipeline after local proof.

## Handoff/Harvest

- Docs to update: `.loom/60-validation-matrix.md`.
- Agent-context entries to add: decision that the memory-info gate is unblocked by an image shim, and finding that underlying ROCm still returns `err=1`.
- Next-slice candidates:
  - Run a model-load smoke on the shimmed image.
  - If model load passes, start the 24 hour llama.cpp soak.
  - If model load fails in a later HIP op, record that op as the next kill-test instead of promoting aliases.
