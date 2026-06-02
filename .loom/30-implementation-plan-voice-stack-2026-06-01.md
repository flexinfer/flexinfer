# Implementation Plan — FlexInfer Voice Stack (ASR + Diarization), execution reconciliation

**Date**: 2026-06-01
**Reconciles**: [brainstorm-voice-stack-2026-06-01.md](brainstorm-voice-stack-2026-06-01.md) + master plan [asr-diarization-7900xtx-plan-2026-05-18.md](asr-diarization-7900xtx-plan-2026-05-18.md)
**Scope (user, 2026-06-01)**: meeting pipeline (ASR + diarization) as v1, conversational/TTS planned next, split across gfx1100 (ASR) + gfx906 (diarization).
**Purpose**: this doc is the *execution order* + status truth. It does not reproduce the 450-line master plan — it points at it and supersedes its status.

---

## Status truth (what's actually done, 2026-06-01)

| Master-plan slice | State | Evidence |
|---|---|---|
| **Slice 1 — Whisper kill-test (runtime gate)** | ✅ **PASS 2026-05-21** | [ralph-whisper-kill-test-v6-evidence-2026-05-21.md](ralph-whisper-kill-test-v6-evidence-2026-05-21.md): Model CR `Ready`, `/v1/models` returns `whisper-large-v3-turbo`, vLLM 0.17.0 auto-resolves `WhisperForConditionalGeneration`, FA Triton on RDNA, 67s cold-start |
| **Slice 2 — GPUProfile `audioTranscription` flag** | ✅ shipped | !423 (`gfx1100: experimental`, `gfx906: unsupported`) |
| **Slice 3b — vLLM `--task` wiring** | ✅ shipped then DROPPED | !423 added it; !464 removed it — vLLM 0.17+ auto-resolves the task from architecture, no CLI flag |
| **Slice 3a — production Whisper Model CR** | ✅ **DONE + LIVE-VALIDATED 2026-06-02** | MR !541 merged; `deploy/models/whisper-large-v3-turbo.yaml`. Both residual risks closed live (real transcript HTTP 200 + demand-driven swap-from-idle w/o forcePromotion + 26B reclaim). Evidence: [whisper-prod-cr-live-validation-2026-06-02.md](whisper-prod-cr-live-validation-2026-06-02.md) |
| **Slice 4 — pyannote diarization on gfx906** | ✅ **DONE + LIVE 2026-06-02** | MR !543 merged. Kill-test (!542) confirmed the riskiest assumption; this deployed the digest-pinned sibling Deployment on radeonvii (cooperative `/dev/kfd` GPU sharing, no `amd.com/gpu` lock). Pod Ready 1/1, health `cuda:true device:AMD Radeon VII`, direct `/diarize` returns 2-speaker segments. Evidence: [pyannote-gfx906-killtest-passed-2026-06-02.md](pyannote-gfx906-killtest-passed-2026-06-02.md) + [voice-stack-slice4-5-live-2026-06-02.md](voice-stack-slice4-5-live-2026-06-02.md) |
| **Slice 5 — proxy `/diarize` route** | ✅ **DONE + LIVE 2026-06-02** | MR !543 merged. `handleDiarize` reverse-proxy + `FLEXINFER_PYANNOTE_UPSTREAM` (Helm). End-to-end validated: `POST /diarize` through the proxy → HTTP 200 speaker segments; `/v1/audio/transcriptions` + `/diarize` now share one base URL. |
| **Slice 6 — load test under contention** | ⬜ OPTIONAL / not run | Both endpoints live; a formal concurrent-load + co-tenancy soak (pyannote ↔ image-gen on radeonvii, Whisper ↔ 26B on 7900xtx) remains as optional hardening. |

**Two residual risks the kill-test did NOT close — both CLOSED LIVE 2026-06-02** ([evidence](whisper-prod-cr-live-validation-2026-06-02.md)):
1. ✅ Real transcription verified — `POST /v1/audio/transcriptions` returned HTTP 200 with an exact transcript (not just `/v1/models`).
2. ✅ Demand-driven swap-from-idle verified — priority 400 + serviceLabels + a routed request preempted the 26B **without `forcePromotion`**; Whisper served, then released and the 26B reclaimed after the 5m idleTimeout. The forcePromotion fallback is **not needed** for the demand-driven cadence.

---

## Execution order (brainstorm sequencing → master-plan slices)

```
STEP 1 (feature-dev now) ── Slice 3a: production Whisper Model CR  [LOW RISK, proven serving]
        │   demand-driven, minReplicas:0 → deploying is a no-op for the 26B until first transcribe
        │   live-validate: (a) real transcript POST, (b) swap-from-idle behavior
        ▼
STEP 2 (next cycle) ─────── Slice 4: pyannote diarization on gfx906  [RISKIEST ASSUMPTION — kill-test first]
        │   gated by its own ≤30min kill-test (see below) before building the full Deployment
        ▼
STEP 3 ─────────────────── Slice 5: proxy /diarize route
        ▼
STEP 4 ─────────────────── Slice 6: load test + canonical demo (meeting audio → diarized transcript)
        ▼
LATER (open question) ──── TTS / conversational loop — needs its own kill-test (where does TTS run?)
```

**Why this order**: Step 1 is the proven, low-risk piece and, because it's demand-driven with `minReplicas:0`, shipping it is a no-op for the warm 26B until someone actually transcribes. It unblocks the canonical demo with zero risk to production chat. Everything riskier (pyannote/gfx906) is gated behind its own kill-test, and if that kill-test fails the system degrades gracefully to ASR-only (the brainstorm runner-up) with ~70% of the work still reusable.

---

## STEP 1 design — production Whisper Model CR (this feature-dev)

New file `deploy/models/whisper-large-v3-turbo.yaml`, added to `deploy/models/kustomization.yaml`.

**Inherits from the proven v6 kill-test spec** (Local cache on `nvme-1r-gpu`, `maxModelLen:448`, `gpuMemoryUtilization:0.30`, `enforceEager:true`, `dtype:half`, image inherited from gfx1100 GPUProfile, **no `task` field**). **Differs** to make it a real production endpoint:

| Field | Kill-test v3 | Production | Why |
|---|---|---|---|
| `gpu.forcePromotion` | `true` | **omitted (false)** | demand-driven, not permanent 26B eviction |
| `gpu.priority` | 400 | **400** | `> 350` so a routed transcribe request fires the demand-based swap (master plan OQ#14 "mechanisms that DO work" #1) |
| `serverless.minReplicas` | 1 | **0** | idle-to-zero; the 26B keeps the GPU until a transcribe arrives |
| `serverless.idleTimeout` | 30m | **5m** | release the GPU back to the 26B quickly after a batch |
| `serviceLabels` | none | **`whisper-large-v3-turbo`, `whisper`, `asr`** | routable through the proxy → single base URL for ICC |
| `litellm` | none | **enabled** + alias `whisper-large-v3-turbo-7900xtx` | catalog/routing parity with the 26B |

**Acceptance (local, this MR)**: `kustomize build deploy/models` succeeds with the new CR; `go test ./backend/...` green (no code change, regression guard); YAML schema-valid against the CRD.

**Acceptance (live, user-initiated cutover — flagged, not auto-run)**:
1. Merge → Flux reconciles. **No 26B impact** (Whisper at 0 replicas).
2. First real validation = POST audio:
   ```
   curl -sS -X POST http://flexinfer-proxy.flexinfer-system.svc/v1/audio/transcriptions \
     -F file=@sample.wav -F model=whisper-large-v3-turbo -F response_format=verbose_json
   ```
   Pass = HTTP 200 + recognizable transcript (closes residual risk #1).
3. Observe the swap: `kubectl get model gemma4-26b-a4b-gptq -o jsonpath='{.status.phase}'` should show `Preempted`/`Pending` during transcribe; sister 26B on cblevins-5930k carries chat throughout (closes residual risk #2).
4. **If swap-from-idle deadlocks** (model stuck `State: Queued, replicas:0`, never activates): fallback = operator flips `gpu.forcePromotion: true` for batch windows (proven path), OR file the controller follow-up to make the proxy activation path write demand for queued shared-GPU members. Document outcome in a `-evidence` doc.

---

## STEP 2 design — pyannote kill-test (riskiest assumption, gates Slice 4)

Per workspace spec rule, Slice 4 is BLOCKED until this kill-test runs live.

**Load-bearing assumption**: `pyannote/speaker-diarization-3.1` runs end-to-end on `cblevins-radeonvii` (gfx906) under `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3` + `HSA_OVERRIDE_GFX_VERSION=9.0.6`, co-resident with FLUX Fill, producing correct RTTM turns at ~500 MiB GPU.

**Kill-test (≤30 min)**: build `Dockerfile.pyannote-rocm-gfx906`, run a one-shot pod on radeonvii, POST a 30s 2-speaker WAV to `/diarize`, assert ≥2 speaker segments with plausible timestamps + GPU residency ~500 MiB without OOMing FLUX Fill.

**Failure mode**: collapse to ASR-only (brainstorm runner-up). ~70% of work (Whisper deploy, proxy, demo) reusable; only the diarization service is lost.

**Status**: ✅ **PASSED 2026-06-02** — pyannote ran on the Radeon VII GPU (1.65 GiB, correct 2-speaker split at the right boundary). Cost: a 7-fix `torchaudio_compat` shim for torchaudio-2.9 ↔ pyannote-3.3.2 drift + operator HF license acceptance. Evidence: [pyannote-gfx906-killtest-passed-2026-06-02.md](pyannote-gfx906-killtest-passed-2026-06-02.md).

---

## Open question (gates the LATER conversational/TTS step)

Where does TTS run? gfx906 PyTorch-only (Piper/Kokoro) frees the big GPU but inherits the fork fragility; gfx1100 is safer but contends with the 26B group. **Needs its own kill-test** — do not let it ride on the diarization decision.

---

## Handoff

- **Now**: feature-dev STEP 1 (this session) → production Whisper CR + kustomization wiring → ship MR → hand off live-validation (user-initiated, production-impacting cutover).
- **Next cycle**: STEP 2 pyannote kill-test as its own feature-dev/rapid-iteration loop.
- Master plan slices 4–6 detail (Dockerfile, FastAPI surface, proxy handler, load-test harness) remain authoritative — reuse them verbatim when those steps come up.
