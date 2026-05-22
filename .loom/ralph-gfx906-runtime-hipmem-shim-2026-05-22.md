# RALPH: gfx906 Runtime hipMemGetInfo Shim

Date: 2026-05-22

## Review

- Roadmap milestone: Lane 1 from `.loom/roadmap-unblock-plan-2026-05-21.md`,
  unblock `gfx906` production fallback through llama.cpp.
- Spec section: `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md`
  Slice 1 and the follow-up after standalone soak harvest.
- Prior decisions to preserve:
  - `gfx906` production textgen substrate is llama.cpp, not vLLM.
  - The standalone shimmed image passed the HIP memory-info gate, Qwen3 8B
    model-load smoke, and 24 hour standalone soak.
  - Do not add `default-chat-fallback` until the persistent
    `flexinfer-runtime-gfx906` path passes a proxy-backed soak with durable
    evidence.

## Align

- Slice name: `gfx906` persistent runtime hipMemGetInfo shim.
- Scope in:
  - Bake `libflexinfer_hipmeminfo_shim.so` into `build/Dockerfile.runtime-gfx906`.
  - Apply the same llama.cpp source patch to the runtime-built `llama-server`.
  - Add a proxy-backed soak manifest that writes JSONL and summary output to a
    PVC before alias promotion.
  - Record the pending runtime-promotion row in `.loom/60-validation-matrix.md`.
- Scope out:
  - No digest promotion until CI publishes a new runtime image.
  - No Flux reconcile from this source-only slice.
  - No `default-chat-fallback` or broad alias change.
  - No vLLM closeout language changes.
- Acceptance criteria:
  - `make dry-run-runtime-gfx906` shows the runtime Dockerfile path still
    resolves through the config-driven builder.
  - `kubectl apply --dry-run=client -f deploy/debug/gfx906-llamacpp-proxy-soak.yaml`
    accepts the soak manifest.
  - `git diff --check` passes.
- Dependencies/blockers:
  - The `publish_runtime_rocm_gfx906` CI job must publish the new digest.
  - Runtime digest promotion must supply the previous `gfx906` digest as
    rollback.
  - The live proxy-backed soak still needs cluster execution after promotion.

## Land

- Planned file areas:
  - `build/Dockerfile.runtime-gfx906`
  - `.gitlab-ci.yml`
  - `deploy/debug/gfx906-llamacpp-proxy-soak.yaml`
  - `.loom/60-validation-matrix.md`
- Implementation steps:
  1. Reuse `build/hipmemgetinfo_shim.cpp` and `build/patch-hipmemgetinfo.sh` in
     the live runtime Dockerfile.
  2. Add CI change triggers for the shim source/patch files.
  3. Add a proxy-backed soak Job with PVC-backed evidence output.

## Prove

- Tests to run:
  - `make dry-run-runtime-gfx906`
  - `kubectl apply --dry-run=client -f deploy/debug/gfx906-llamacpp-proxy-soak.yaml`
- Lint/static checks:
  - `git diff --check`
- CI checks:
  - `publish_runtime_rocm_gfx906` after merge should publish the candidate
    runtime digest for promotion.

## Handoff/Harvest

- Docs to update:
  - `.loom/60-validation-matrix.md` pending row now points at this slice.
- Agent-context entries to add:
  - Decision: persistent runtime image must carry the same shim before alias
    promotion.
  - Follow-up task: promote published digest, reconcile, run proxy-backed soak,
    then harvest PVC summary.
- Next-slice candidates:
  1. Promote the published `runtime:rocm-gfx906` digest with rollback metadata.
  2. Reconcile runtime DaemonSet and run `deploy/debug/gfx906-llamacpp-proxy-soak.yaml`.
  3. If proxy soak passes, add `default-chat-fallback` and close vLLM canary
     posture.
