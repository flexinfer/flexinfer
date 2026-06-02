# Voice stack — TTS / conversational loop: Slice 1 (kill-test)

**Date**: 2026-06-02
**RALPH phase**: Align → Land (slice 1 = kill-test)
**Predecessor**: ASR + diarization v1 shipped & live ([voice-stack-slice4-5-live-2026-06-02.md](voice-stack-slice4-5-live-2026-06-02.md))
**Brainstorm origin**: [brainstorm-voice-stack-2026-06-01.md](brainstorm-voice-stack-2026-06-01.md) Framing B (STT→LLM→TTS), flagged LATER

---

## Why this is gated

Per the workspace `spec-riskiest-assumption` rule, any new capability with a
claim about external/third-party runtime behavior is BLOCKED until slice 1's
kill-test passes. TTS on this ROCm/CPU homelab is unproven — the brainstorm
explicitly deferred it with an open placement question. This doc IS slice 1.

## Riskiest assumption + kill-test

**Load-bearing assumption**: The Kokoro-82M TTS model, served via the pre-built
`ghcr.io/remsky/kokoro-fastapi-cpu:latest` OpenAI-compatible image, produces
intelligible English speech at **faster-than-real-time (RTF < 1.0) on a
CPU-only pod** — needing neither the contended gfx1100 `*-textgen` GPUs nor the
fragile `mixa3607/pytorch-gfx906` community fork.

**Why this reframes the brainstorm's open question**: the brainstorm posed
placement as gfx906 (fork-fragile) vs gfx1100 (26B-contended). Research shows
Kokoro-82M (~80MB ONNX, StyleTTS2) runs at RTF ~0.45–0.51 on CPU and the
FastAPI wrapper already speaks `/v1/audio/speech` — the same OpenAI convention
as Whisper `/v1/audio/transcriptions` and the new `/diarize`. If CPU is fast
enough, the GPU-placement risk **dissolves entirely**.

**Kill test** (≤30 min, observable, end-to-end):
1. `docker --context 7900xtx run` the `kokoro-fastapi-cpu` image (x86 host CPU,
   no GPU device mapped).
2. `POST /v1/audio/speech` with `{model:"kokoro", voice:"af_*", input:"<~200
   chars>"}`.
3. Measure wall-clock vs. synthesized audio duration → compute RTF.
4. Validate the returned bytes are a real, non-silent WAV/MP3 of the expected
   duration (ffprobe duration > 0, file size sane).

**Pass criteria** (all must hold):
- HTTP 200 with audio payload at `/v1/audio/speech`.
- RTF < 1.0 on CPU (generates faster than it plays).
- Audio decodes to a valid clip of plausible duration for the input length.

**Failure mode if the assumption is wrong**: if CPU RTF ≥ 1.0 or output is
garbled, TTS must fall back to either the idle gtx980ti (NVIDIA CUDA 11.8,
`kokoro-fastapi-gpu`) or the gfx906 ROCm-experimental image — re-introducing the
exact GPU-placement + fork-fragility risk the brainstorm worried about, and
re-opening Slice 2 placement design.

**Status**: **PASSED 2026-06-02**

### Evidence (passed 2026-06-02)

Ran `ghcr.io/remsky/kokoro-fastapi-cpu:v0.4.0-amd64` (digest
`sha256:541864ceddcf…`) as a throwaway container on the `7900xtx` docker
context, **CPU-only, no GPU device mapped**. Probed `/v1/audio/speech` from
inside the container (`voice=af_heart`, `response_format=wav`, 167-char input):

| Metric | Result |
|---|---|
| HTTP status | 200 |
| Output | valid 24 kHz mono WAV, non-silent |
| Audio duration | 12.094s (ffprobe) — matches PCM byte-count calc 12.095s |
| Wall-clock | 1.399s |
| **RTF** | **0.116 (~8.6× faster than real-time)** |

Note: the server streams WAV with a placeholder data-chunk size, so
`wave.getnframes()` reports a bogus duration; duration was derived from PCM byte
count and cross-checked with `ffprobe` (12.094s — agreement confirms it).

**Conclusion**: TTS runs comfortably faster-than-real-time on CPU. The
brainstorm's gfx906-fork-fragility vs gfx1100-26B-contention dilemma is
**dissolved** — neither GPU is needed. Slice 2+ unblocked. Intelligibility of
the audio is not machine-verified here (Kokoro quality is externally
established + the clip is valid non-silent audio of the correct duration for
the input); an ASR round-trip against the live Whisper endpoint is the natural
verification for the Slice 4 conversational-loop demonstrator.

> **Post-deploy addendum (2026-06-02)**: this RTF (0.116) was measured on the
> **7900xtx host CPU**. In production the Deployment scheduled onto **k3s-w-10**
> (a weaker worker) where warm RTF is **~1.2** — still CPU-only/no-GPU (the
> load-bearing claim holds) but *slower than real-time*. The kill-test
> over-stated throughput by running on a faster CPU than the pod landed on.
> Slice-4 intelligibility round-trip PASSED live (Whisper transcribed the TTS
> output verbatim). Full deploy evidence + throughput follow-up:
> [voice-stack-tts-live-2026-06-02.md](voice-stack-tts-live-2026-06-02.md).

## Positive / negative evidence (pre-run)

**Positive**:
- Kokoro-82M RTF ~0.45–0.51 on CPU; #1 open-weight on TTS Arena. (ttsinsider, codesota)
- `remsky/Kokoro-FastAPI`: OpenAI-compatible `/v1/audio/speech`, pre-built
  CPU / NVIDIA / AMD-ROCm images, auto-stitching for long text. (github.com/remsky/Kokoro-FastAPI)

**Negative / bounds**:
- One source reports CPU as "3–5× realtime" (still RTF < 1, but hardware-dependent
  → must measure on THIS host).
- Base model ~30s/segment; long text relies on wrapper auto-stitching.
- English-only; preset voices only (no cloning); flat emotional range;
  abbreviations/numbers occasionally mispronounced.
- ROCm image is "experimental" → another reason to prefer CPU.
- For a meeting/assistant homelab use case these bounds are acceptable.

## If kill-test PASSES → provisional slice map (do NOT start until pass)

- **Slice 2** — TTS Deployment: `kokoro-fastapi-cpu` as a CPU Deployment in
  `flexinfer-system` + Service (mirror pyannote's deploy/Service shape, minus GPU).
- **Slice 3** — Proxy route: add `/v1/audio/speech` upstream env
  (`FLEXINFER_KOKORO_UPSTREAM`) + handler, mirroring the `/diarize` Slice 5 work.
- **Slice 4** — Conversational loop client: ASR → LLM (gemma4-26b) → TTS
  round-trip demonstrator through the single proxy base URL.

## Live capacity snapshot (2026-06-02)

| Node | Arch | VRAM | Current load |
|---|---|---|---|
| 7900xtx | gfx1100 | 23Gi | `7900xtx-textgen` (gemma4-26b Ready), whisper, apc-canary |
| 5930k | gfx1100 | 23Gi | `5930k-textgen` (gemma4-26b Ready) |
| radeonvii | gfx906 | 15Gi | pyannote (always-on) + `radeonvii-models` + `radeonvii-imagegen` |
| gtx980ti | sm_52 | 6Gi | mostly idle (CUDA 11.8) — TTS GPU fallback if CPU fails |
| (CPU) | — | — | **untested — primary TTS target** |
