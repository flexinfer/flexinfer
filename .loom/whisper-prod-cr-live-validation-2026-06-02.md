# Evidence — production Whisper Model CR live validation (voice stack Slice 3a)

**Date**: 2026-06-02
**MR**: services/flexinfer!541 (merged, commit `a12b9abe`)
**CR**: `deploy/models/whisper-large-v3-turbo.yaml`
**Plan**: [30-implementation-plan-voice-stack-2026-06-01.md](30-implementation-plan-voice-stack-2026-06-01.md) STEP 1
**Verdict**: **PASS** — both residual risks closed; full demand-driven lifecycle proven end-to-end.

## What was tested

The production Whisper CR is demand-driven (`priority: 400`, `serverless.minReplicas: 0`, no
`forcePromotion`). The kill-test (v6) only proved the model *loads + registers* via `forcePromotion`;
it never POSTed audio and never exercised the swap-from-idle. This validation closes both gaps live
on `cblevins-7900xtx`.

## Timeline (2026-06-02)

| T | Event | Evidence |
|---|---|---|
| baseline | Whisper `Idle / Queued / 0 replicas`; 26B `Ready / Active` | deploying the CR was a no-op for the warm 26B |
| 08:43:24 | `POST /v1/audio/transcriptions` (known-text WAV) issued via `flexinfer-proxy` | — |
| 08:43:~33 | Whisper preempts 26B from idle | event: `Preempted by whisper-large-v3-turbo with priority 400`; 26B → `Preempted/Queued`, Whisper → `Loading/Active` |
| 08:44:07 | Whisper `Ready` (~43s cold-start; weights pre-staged by Local cache job) | `kubectl wait ... =Ready` condition met |
| 08:44:10 | Transcribe returns **HTTP 200**, 46.5s total | see below |
| (during) | Chat continuity: sister 26B on `cblevins-5930k` served a completion (`"chat is up"`) | zero chat outage |
| 08:50:14 | 26B reclaims GPU after Whisper's 5m idleTimeout | 26B → `Ready/Active`, Whisper → `Idle/Queued` (back to baseline) |

## Residual risk #1 — real transcription (CLOSED)

Known text: `The quick brown fox jumps over the lazy dog.` (macOS `say` → 16 kHz mono PCM WAV).

Response (HTTP 200, `__TIME=46.499722s`):
```json
{"duration":"2.7946875","language":"en",
 "text":" The quick brown fox jumps over the lazy dog.",
 "segments":[{"id":0,"avg_logprob":-0.2421,"compression_ratio":0.865,"end":2.68,
   "start":0.0,"temperature":0.0,"text":" The quick brown fox jumps over the lazy dog."}]}
```
Exact match. The `/v1/audio/transcriptions` endpoint vLLM auto-mounts from the
`WhisperForConditionalGeneration` architecture is real, not just inferred from v6's `/v1/models`.

## Residual risk #2 — demand-driven swap-from-idle (CLOSED)

`priority: 400` (> the 26B's 350) + `serviceLabels` + a proxy-routed request fired the demand-based
swap **without `forcePromotion`** — the exact mechanism the kill-test sidestepped. The 26B was
preempted, Whisper loaded and served, then after `idleTimeout: 5m` Whisper released and the 26B
reclaimed automatically. No stuck `State: Queued`, no manual intervention. The fallback
(`forcePromotion` for batch windows) is therefore **not needed** for the demand-driven cadence.

## Operational notes

- Full transcribe latency from cold (idle → swap → load → decode) was ~46s for a ~3s clip; the 26B
  reclaim cycle added ~6 min wall-clock (5m idle + ~1m reload). Acceptable for ICC's post-call batch
  cadence; for sub-5-min repeat transcribes the model stays warm and skips the swap.
- Chat has **zero outage** during ASR because the sister 26B on `cblevins-5930k` carries shared
  `serviceLabels` routes (`quality-chat`, `mid-chat`, `gpt-4`, …).

## Next

STEP 1 complete. Next is STEP 2 — pyannote diarization kill-test on `cblevins-radeonvii` (gfx906),
the brainstorm's riskiest assumption — gated before building the full Slice 4 Deployment.
