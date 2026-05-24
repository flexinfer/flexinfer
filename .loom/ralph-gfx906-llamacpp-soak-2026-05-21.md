# RALPH: gfx906 llama.cpp Production-Lane Canary

Date: 2026-05-21

## Intent

Pick up the non-Whisper slice while Claude owns the Whisper ASR lane: prove or falsify the `gfx906` llama.cpp production-lane soak assumption from `spec-gfx906-llamacpp-production-lane-2026-05-20.md`.

## Actions

- Applied the dormant `qwen3-8b-radeonvii` manifest as a temporary live canary.
- Patched it live to use `Local` cache, `minReplicas: 1`, priority `140`, force promotion, and canary-only LiteLLM/service labels.
- Waited for `qwen3-8b-radeonvii-cache-stage` to complete.
- Recycled `flexinfer-runtime-gfx906` once after the previous `qwen3-1p7b-tools-radeonvii` unload wedged the runtime API before the 8B subprocess could start.
- Captured the 8B llama.cpp ROCm failure, then deleted the temporary canary.
- Lowered `qwen3-1p7b-vllm-radeonvii` canary priority from `130` to `100` live and in `deploy/models/qwen3-1p7b-vllm-radeonvii.yaml` so the idle vLLM canary cannot hold the shared Radeon VII group above the resident tool-router model.
- Added a runtime unload guard so a killed backend that is never reaped cannot block future load requests forever.

## Evidence

- Cache stage succeeded and staged:
  - `/models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf`
  - size `5027783488`
- Clean runtime retry launched llama.cpp with:
  - `--model /models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf`
  - `--ctx-size 16384`
  - `--n-gpu-layers 999`
  - `--flash-attn on`
  - `-fit off`
- llama.cpp detected the GPU:
  - `ggml_cuda_init: found 1 ROCm devices`
  - `Device 0: AMD Radeon VII, gfx906:sramecc+:xnack- (0x906), VMM: no, Wave Size: 64`
- The backend aborted during model load:
  - `ROCm error: invalid argument`
  - `ggml_backend_cuda_device_get_memory`
  - `hipMemGetInfo(free, total)`
  - `signal: aborted (core dumped)`
- Rollback status:
  - `qwen3-1p7b-tools-radeonvii`: `Ready`, `Active`, priority `120`
  - `qwen3-1p7b-vllm-radeonvii`: `Idle`, queued behind tool-router, priority `100`
- Restore smoke:
  - proxy route `/model/qwen3-1p7b-tools-radeonvii/v1/chat/completions`
  - response content: `Blue`
  - timings included `predicted_per_second: 73.3568`

## Outcome

The 24h production soak did not start. The riskiest assumption was falsified before soak: llama.cpp can see the Radeon VII but crashes on `hipMemGetInfo` during GPU-backed model load, even with `fitOff`, `HSA_OVERRIDE_GFX_VERSION=9.0.6`, `HSA_ENABLE_SDMA=0`, and `HSA_USE_SVM=0` supplied by the runtime/profile path.

The useful shipped fix from this slice is adjacent: the vLLM canary no longer preempts the default tool-router lane while idle, and the runtime unload path now has a bounded post-SIGKILL wait.

## Next Blocker

The next `gfx906` llama.cpp slice should isolate `hipMemGetInfo` outside FlexInfer:

1. Run `/opt/llamacpp/bin/llama-server` or a minimal HIP memory-info probe inside the same runtime image on `cblevins-radeonvii`.
2. Compare env variants:
   - current profile env
   - no `HSA_OVERRIDE_GFX_VERSION`
   - `ROCR_VISIBLE_DEVICES=0` only
   - `HIP_VISIBLE_DEVICES=0` plus `GPU_DEVICE_ORDINAL=0`
3. If the minimal probe fails, treat this as a ROCm/runtime-image compatibility bug.
4. If the probe passes, narrow to llama.cpp memory-fit/model-load behavior.

## Validation

- `go test ./internal/runtime` passed.
- Live restore smoke against `qwen3-1p7b-tools-radeonvii` passed.
