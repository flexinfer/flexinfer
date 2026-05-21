# RALPH: gfx906 llama.cpp Model-Load Smoke on Shim

Date: 2026-05-21
Slice: gfx906 llama.cpp Qwen3 8B model-load smoke after HIP memory-info shim

## Review

- Roadmap milestone: unblock the Radeon VII / `gfx906` llama.cpp production
  lane before any 24 hour soak, alias promotion, or vLLM closeout work.
- Prior slice: MR !467 landed the standalone llama.cpp
  `hipMemGetInfo` shim and proved the minimal HIP probe on `cblevins-radeonvii`.
- Spec section: `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md`.

## Align

- Scope in: one live model-load smoke using the shimmed standalone image and
  the staged Qwen3 8B GGUF cache on the Radeon VII node.
- Scope out: 24 hour soak, service alias promotion, Flux/GitOps changes, and
  changing the reconciled CPU fallback tool-router model.
- Acceptance criteria:
  - The immutable shim image starts on `cblevins-radeonvii`.
  - `llama-cli` loads
    `/models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf` with GPU
    layers enabled.
  - The process reaches llama.cpp ROCm memory breakdown and exits `0` after a
    short generation.
  - The existing `qwen3-1p7b-tools-radeonvii` route still returns a one-word
    smoke response afterward.

## Land

No repo source changes were required for the live smoke. The one-off debug Job:

- used
  `registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407`;
- mounted `/var/lib/flexinfer/models` at `/models`;
- requested `amd.com/gpu: 1`;
- ran `llama-cli` with `--gpu-layers 999`, `--flash-attn on`,
  `--cache-type-k q4_0`, `--cache-type-v q4_0`, `--ctx-size 8192`, and
  `LD_PRELOAD=/opt/flexinfer/lib/libflexinfer_hipmeminfo_shim.so`.

The first attempt used llama.cpp conversation auto-mode and sat at the `>`
prompt. It was deleted and rerun with `--single-turn` and
`--no-display-prompt`, producing a bounded pass/fail result.

## Prove

Model-load smoke verdict: PASS.

Key signals:

- The image pull on `cblevins-radeonvii` completed in `2m56.545s`; pulled image
  size was `3224726049` bytes.
- `llama-cli` saw `AMD Radeon VII`, `gfx906`, and `VMM: no`.
- The shim logged repeated raw `hipMemGetInfo err=1` fallbacks to sysfs VRAM
  totals.
- Qwen3 8B loaded successfully and printed memory breakdown:
  `model=4455 MiB`, `context=324 MiB`, `compute=304 MiB`.
- Short generation completed with `Prompt: 175.2 t/s` and
  `Generation: 81.1 t/s`.
- The command exited `SMOKE_EXIT=0` and `SMOKE_RESULT PASS`.

Restore check:

- `qwen3-1p7b-tools-radeonvii` remained `Ready`.
- Proxy smoke returned JSON content `Blue`.
- Restore timings were `prompt_per_second=107.95` and
  `predicted_per_second=69.51`.

Raw transcript:
`.loom/local/validation/gfx906-llamacpp/2026-05-21/model-load-shim-smoke.log`.

Debug objects were cleaned up after the transcript was captured:

- `job/gfx906-llamacpp-model-load-smoke`
- `configmap/gfx906-llamacpp-model-load-smoke`

## Handoff

The next RALPH gate is the controlled 24 hour soak from
`.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md` Slice 1.

Start condition:

- Use the same shimmed image and Qwen3 8B GGUF path proven here.
- Do not promote aliases or default fallback routing until the soak proves zero
  model pod restarts, acceptable decode latency, and no SDXL inpainting
  regression.

Recommended next-slice evidence:

- Soak traffic generator manifest or command.
- Restart count for the target model runtime.
- p95 decode latency or per-run token timing summary.
- Radeon VII GPU memory telemetry peak.
- SDXL inpainting lane Ready/Idle status throughout the soak window.
