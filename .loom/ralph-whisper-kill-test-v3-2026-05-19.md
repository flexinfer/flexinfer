# RALPH iteration — Whisper kill-test v3 (cache layout fix)

**Loop**: roadmap-spec-ralph-loop
**Date**: 2026-05-19
**Parent spec**: `.loom/asr-diarization-7900xtx-plan-2026-05-18.md`
**Prior slice**: `9a9c2883` `fix(controller): sweep empty-Phase Models, fan out shared-group deletes` (MR !442, merged into master)
**Driver**: ASR-diarization plan's Slice 1 kill-test remains UNANSWERED — v2 (MR !436) was INCONCLUSIVE because the cache PVC files never landed at the runtime's hostPath mount. The Whisper-on-ROCm-gfx1100 question still has no live evidence. Full v2 evidence in `.loom/asr-diarization-kill-test-v2-inconclusive-2026-05-19.md`.

## Scope (in)
1. New `deploy/models/whisper-kill-test-v3.yaml` with corrected cache layout: `strategy: Local`, `storageClass: nvme-1r-gpu`, `size: 6Gi`. Same proven pattern as `qwen3-1p7b-vllm-radeonvii` (HF source + vLLM + Local cache).
2. Register in `deploy/models/kustomization.yaml`.
3. Keep `gpu.forcePromotion: true` (chooser still prefers Ready 26B; MR !442 did not change preference logic). Keep all v2 vLLM args (`task: transcription`, `maxModelLen: 448`, `gpuMemoryUtilization: 0.30`, `enforceEager: true`).
4. Carry forward v2's "no serviceLabels / no litellm" stance — kill-test is operator-validated via port-forward, not routable.

## Scope (out)
- Whisper production Model CR (Slice 3a, gated on this kill-test PASSED).
- pyannote diarization Deployment (Slice 4).
- proxy/litellm transcription routes (Slice 5).
- Any GPUProfile changes (no new capability flags this slice).
- The actual `curl /v1/audio/transcriptions` evaluation (queued as a follow-up live task; this slice ships the CR + waits for Ready phase).

## Acceptance criteria
- `kustomize build deploy/models/` composes cleanly (no schema errors).
- Manifest validates against the `Model` CRD (`kubectl apply --dry-run=client -f`).
- MR opened, CI green, merged to master.
- After Flux reconciles, the controller schedules a cache-prefetch job for `whisper-kill-test-v3` that lands files at `/var/lib/flexinfer/models/whisper-kill-test-v3/` on `cblevins-7900xtx`.
- Runtime pod (`flexinfer-runtime-gfx1100-*`) sees `/models/whisper-kill-test-v3/config.json` (verifiable via `kubectl exec ... -- ls /models/whisper-kill-test-v3/`).
- Model reaches `Phase: Ready` OR fails with a real ROCm/vLLM error (not a path-resolution error like v2's `HFValidationError`).
- Production impact: ~3 min preemption of `gemma4-26b-a4b-gptq` is acceptable (sister on 5930k carries quality-chat traffic via shared serviceLabels — same envelope as v2).

## Risk notes
- **Cache strategy change**: `Local` vs v2's `SharedPVC` is the *intended* fix. The v2 evidence + `qwen3-1p7b-vllm-radeonvii` precedent confirm Local writes to a per-node PVC mounted into both the prefetch job and the runtime DaemonSet under `/models/<name>/`. No two-stage `cache-copy` job is needed.
- **forcePromotion still required**: `controllers/model_shared_gpu.go:84-101` chooser still returns the Ready 26B first unless a force-promoted claimant exists. Keeping `forcePromotion: true` is the documented v2 mechanism. MR !442 changed only the startup-sweep filter and the delete-event fan-out, not chooser preference.
- **`nvme-1r-gpu` storage class** is single-replica Longhorn on the GPU nodes (`nodeSelector: gpu`, `diskSelector: nvme`). Per `platform/gitops/k3s/longhorn/storageclass-nvme-1r-gpu.yaml`. Already used by `gemma4-26b-a4b-gptq-cache` (50Gi) on the same node, so the PV class is proven.
- **Image source**: inherits from gfx1100 GPUProfile `vllm.image` pin (digest currently in `deploy/gpuprofiles/gfx1100.yaml`). No per-model override; the kill-test exercises the same image the production Whisper CR would use.

## Test plan
- Local: `kustomize build deploy/models/ | kubectl apply --dry-run=client -f -` — must report `model.ai.flexinfer/whisper-kill-test-v3 created (dry run)` with no validation errors.
- Local: `yamllint deploy/models/whisper-kill-test-v3.yaml` if available.
- CI: rely on standard pipeline (manifest lint + chart render).
- Cluster (post-merge, queued as follow-up task): observe Flux reconcile, prefetch job, `/models/whisper-kill-test-v3/` listing, Model phase transition, and port-forward `curl /v1/audio/transcriptions`.

## Dependency/blocker map
- No code-side blockers. Pure manifest change.
- Live evaluation (Slice 1 actual pass/fail) is queued as a separate task — gated on Flux reconciling this MR and the runtime image being available on the node.

## Riskiest assumption + kill-test

**Load-bearing assumption**: With `cache.strategy: Local` and `cache.storageClass: nvme-1r-gpu`, the controller schedules a `cache-prefetch` job that writes the Whisper safetensors + config.json into a PVC mount that the same-node runtime DaemonSet sees as `/models/whisper-kill-test-v3/<files>`. No `cache-copy` job is required, and vLLM resolves the local path without re-trying as an HF repo ID.

**Kill-test**: After Flux applies this MR, observe two things on `cblevins-7900xtx`:
1. `kubectl get job -n flexinfer-system -l ai.flexinfer/model=whisper-kill-test-v3` shows a `cache-prefetch` job that reaches `Completed`.
2. `kubectl exec -n flexinfer-system <runtime-pod-on-7900xtx> -- ls /models/whisper-kill-test-v3/` returns at least `config.json` and one `.safetensors` file.

If both observations hold, the cache layout assumption is confirmed and the runtime can proceed to load weights. From that point forward, any failure is a Whisper-on-ROCm-gfx1100 issue (the actual parent question), not a CR misconfiguration.

**Failure mode if the assumption is wrong**: Files land in a PVC the runtime cannot see (different node, different mount path, or different access mode). Controller logs would show `cache-prefetch` job completing but the runtime would crash on `HFValidationError: Repo id must be in the form...` (identical to v2 symptom). Recovery: tear down via the same pattern as `chore/tear-down-whisper-kill-test-v2`; switch to a `SharedPVC + cache-copy` layout matching gemma4-26b-a4b-gptq.

**Status**: not run

## Follow-up slice (queued, not part of this MR)
**Slice 1 live evaluation** — once Flux has reconciled and the runtime sees the files:
1. Port-forward the kill-test pod to 8000.
2. POST a ~10s LibriSpeech-clean WAV to `/v1/audio/transcriptions` with `model=whisper-large-v3-turbo`, `response_format=verbose_json`.
3. Record HTTP code, response text, server-log absence of "CUDA-only kernel" / "FlashInfer required" / "task=transcription not supported".
4. Either: tear down kill-test + start Slice 3a (production CR + serviceLabels/litellm) on PASS, OR record FAIL evidence and activate the whisper.cpp HIP fallback path documented in the plan.
