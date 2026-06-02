# Voice stack TTS + conversational loop — LIVE (Slices 2-4)

**Date**: 2026-06-02
**MR**: services/flexinfer!544 (merged, commit `aefe61ab`)
**Predecessor**: ASR + diarization v1 ([voice-stack-slice4-5-live-2026-06-02.md](voice-stack-slice4-5-live-2026-06-02.md))
**Kill-test**: [tts-killtest-plan-2026-06-02.md](tts-killtest-plan-2026-06-02.md) (PASSED 2026-06-02)

## What shipped

The full voice stack is now callable through one proxy base URL:

| Capability | Route | Backend | Node |
|---|---|---|---|
| ASR | `POST /v1/audio/transcriptions` | Whisper Model CR | gfx1100 (demand-driven) |
| LLM | `POST /v1/chat/completions` | gemma4-26b Model CR | gfx1100 |
| **TTS** | `POST /v1/audio/speech` | **Kokoro Deployment** | **CPU (k3s-w-10)** |
| Diarization | `POST /diarize` | pyannote Deployment | gfx906 |

- **Slice 2**: `kokoro-tts` CPU Deployment + Service (`deploy/system/kokoro-tts/`), digest-pinned Harbor mirror of `kokoro-fastapi-cpu:v0.4.0-amd64`. No GPU, no privileged, no gated token.
- **Slice 3**: proxy `/v1/audio/speech` static route (`internal/proxy/kokoro.go` `handleSpeech`) + `FLEXINFER_KOKORO_UPSTREAM` Helm wiring.
- **Slice 4**: conversational-loop demonstrator (`internal/voiceloop` + `cmd/voice-loop`).

## Live validation (2026-06-02)

**Slice 2 — direct + health** (in `kokoro-tts` pod):
```
/health → {"status":"healthy"}
/v1/audio/speech → 110104 bytes, valid 24kHz mono WAV
```

**Slice 3 — through the proxy** (`flexinfer-proxy.flexinfer-system.svc`):
```
POST /v1/audio/speech → HTTP 200, Content-Type audio/wav, valid RIFF WAV
```

**Slice 4 — full conversational loop + intelligibility round-trip**
(through the proxy, in-cluster):
```
LLM  : "What is the capital of France?" → "The capital of France is Paris."  (1.2s)
TTS  : "The capital of France is Paris." → 1.98s WAV                          (2.5s)
ASR  : <that WAV> → "The capital of France is Paris."   (verbatim; whisper cold-start 62.8s)
```
The round-trip ASR matches the reply **exactly** — machine proof the synthesized
speech is intelligible, closing the one gap the kill-test could not verify.

## Throughput caveat (RESOLVED 2026-06-02)

The kill-test measured RTF **0.116 (~8.6× real-time)** on the **7900xtx host CPU**,
but the Deployment first landed on **k3s-w-10** (a weaker 4-core worker) where
warm steady-state was **RTF ~1.07** (294 chars → 18.0s audio in ~19.2s) — i.e.
*slower* than real-time. The kill-test over-stated throughput by running on a
faster CPU than where the pod scheduled. The load-bearing claim still held:
**TTS needs no GPU** (no gfx906 fork, no gfx1100 26B contention).

**Fix (MRs !545 + !546)**: hard-pinned `kokoro-tts` to **cblevins-7900xtx** via
`requiredDuringSchedulingIgnoredDuringExecution` nodeAffinity + a
`dedicated=gpu:NoSchedule` toleration, raised CPU (req 1→2, lim 4→8), and set
`OMP_NUM_THREADS=8`. Digest pin unchanged. (!545 first tried a *soft* preference,
which lost to the scheduler's resource-balancing score and drifted the pod to the
emptier-but-slower gtx980ti — Ivy Bridge i7-3770K, no AVX2/FMA; !546 made it a
hard pin.)

**Verified in-cluster** (same 294-char probe, warm, median of 3, RTF = wall ÷ audio):

| Placement | Warm RTF |
|---|---|
| k3s-w-10 (4-core VM, before) | **1.068** |
| cblevins-gtx980ti (soft-affinity drift) | worse (Ivy Bridge, no AVX2) |
| **cblevins-7900xtx (after, hard pin)** | **0.207** (~4.8× real-time) |

- **5.2× faster** than the k3s-w-10 baseline; comfortably under the RTF < 1.0
  target. Long-form synthesis now keeps ahead of playback.
- **No GPU disruption**: gemma4-26b (same node) stayed `Running`/0-restarts; the
  conversational loop is sequential so LLM and TTS barely overlap. Node memory
  16%; TTS bursts to 8 cores only during synthesis, leaving 16 for GPU serving.
- Operator doc updated with the placement rationale: `docs/operations/kokoro-tts.md`.

## Operational notes

- Models baked into the image → pod Ready in seconds (no gated download).
- The `-verify`/`-audio` paths of `cmd/voice-loop` drive demand-driven Whisper;
  the first ASR call cold-starts Whisper (~60s) and preempts the 26B, then releases.
- Image-bump hygiene: re-mirror to Harbor, repin digest, re-run the kill-test probe.

Operator doc: `docs/operations/kokoro-tts.md`.
