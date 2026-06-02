# Kokoro Text-to-Speech (CPU) + conversational loop

Text-to-speech for the voice stack, completing the conversational loop
alongside Whisper ASR and pyannote diarization. Runs as a pre-built
OpenAI-compatible FastAPI Deployment ([remsky/Kokoro-FastAPI][1]) on **CPU**,
reachable through `flexinfer-proxy` at `POST /v1/audio/speech`.

## Architecture

```
              ┌─/v1/audio/transcriptions─> Whisper Model CR   (gfx1100)  [ASR]
ICC / client ─┼─/v1/chat/completions──────> gemma4-26b Model CR (gfx1100) [LLM]
              ├─/v1/audio/speech──────────> Kokoro Deployment  (CPU)     [TTS]
              └─/diarize──────────────────> pyannote Deployment (gfx906)  [diar]
```

- **Service**: `kokoro-tts.flexinfer-system.svc:8880`
- **Proxy route**: `/v1/audio/speech` → `FLEXINFER_KOKORO_UPSTREAM`
  (`deploy/system/values-k3s.yaml` → `proxy.kokoroUpstream`)
- **Image**: `registry.harbor.lan/flexinfer/kokoro-fastapi-cpu:v0.4.0-amd64`
  (digest-pinned in the Deployment), mirrored from
  `ghcr.io/remsky/kokoro-fastapi-cpu:v0.4.0-amd64`
- **No GPU**: the 2026-06-02 kill-test measured **RTF 0.116 (~8.6× real-time)**
  on CPU, so TTS runs as a plain CPU Deployment — no `amd.com/gpu`, no
  privileged, no `/dev/kfd`. This sidesteps both the gfx906 community-fork
  fragility and the gfx1100 26B-textgen contention the brainstorm worried about.

## Why this is NOT a Model CR

Like pyannote, Kokoro is a hand-wired sibling Deployment, not a flexinfer Model.
Its request body carries `model: "kokoro"`, which is not a flexinfer Model name,
so the proxy routes `/v1/audio/speech` by **static path** (`handleSpeech`)
rather than the model resolver. (Whisper's `/v1/audio/transcriptions` *can* go
through the resolver because Whisper *is* a Model CR.)

## Usage

```bash
# Direct TTS through the proxy
curl -sS -X POST http://flexinfer-proxy.flexinfer-system.svc/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{"model":"kokoro","voice":"af_heart","input":"Hello from the homelab.","response_format":"wav"}' \
  -o reply.wav

# List voices
curl -sS http://kokoro-tts.flexinfer-system.svc:8880/v1/audio/voices
```

Returns audio bytes (`audio/wav` by default). The OpenAI Python/JS SDKs work
drop-in by pointing `base_url` at the proxy.

## Conversational loop demonstrator (`cmd/voice-loop`)

`cmd/voice-loop` exercises the whole stack through the single proxy URL:
ASR → LLM → TTS, with an optional Whisper round-trip to verify the synthesized
speech is intelligible (not just non-silent bytes).

```bash
go run ./cmd/voice-loop \
  -proxy http://localhost:18080 \
  -text "what time is the meeting" \
  -out reply.wav -verify
# ASR transcript : (skipped for -text)
# LLM reply      : The meeting is at noon.
# TTS audio      : 192044 bytes (audio/wav) → reply.wav
# Round-trip ASR : the meeting is at noon
```

Pass `-audio question.wav` instead of `-text` to start from speech. The engine
lives in `internal/voiceloop` (fully unit-tested with httptest mocks).

> **Note:** `-verify` and `-audio` drive `/v1/audio/transcriptions`, which is a
> demand-driven Whisper Model CR — the first call cold-starts Whisper and
> preempts the 26B textgen, then releases it. Expect a one-time cold-start
> latency on the first ASR call.

## Operations

```bash
# status
kubectl get deploy kokoro-tts -n flexinfer-system
kubectl logs deploy/kokoro-tts -n flexinfer-system

# health
kubectl exec deploy/kokoro-tts -n flexinfer-system -- \
  curl -s localhost:8880/health
# {"status":"healthy"}
```

Models are baked into the image, so the pod is Ready in seconds — no gated
download, no HF token, no persistent cache.

## Caveats

- **English-only**, preset voices only (no cloning), neutral/flat emotional
  range, and occasional mispronunciation of abbreviations/numbers — acceptable
  for a homelab meeting/assistant use case.
- Base model targets ~30s segments; the wrapper auto-stitches longer text.
- **Image-bump hygiene**: re-mirror to Harbor and repin the digest on any
  version change, and re-run the kill-test probe
  (`.loom/tts-killtest-plan-2026-06-02.md`) before trusting a new build.
- ghcr pull gotcha: a stale cached ghcr credential on the build host returns
  `denied: denied` on public images — `docker logout ghcr.io` then pull an
  explicit `-amd64` tag.

See `.loom/tts-killtest-plan-2026-06-02.md` for the kill-test evidence.

[1]: https://github.com/remsky/Kokoro-FastAPI
