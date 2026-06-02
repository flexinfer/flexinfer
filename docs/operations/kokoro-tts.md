# Kokoro Text-to-Speech (CPU) + conversational loop

Text-to-speech for the voice stack, completing the conversational loop
alongside Whisper ASR and pyannote diarization. Runs as a pre-built
OpenAI-compatible FastAPI Deployment ([remsky/Kokoro-FastAPI][1]) on **CPU**,
reachable through `flexinfer-proxy` at `POST /v1/audio/speech`.

## Architecture

```
              ┌─/v1/audio/transcriptions─> Whisper Model CR   (gfx1100)  [ASR]
ICC / client ─┼─/v1/chat/completions──────> gemma4-26b Model CR (gfx1100) [LLM]
              ├─/v1/audio/speech──────────> Kokoro Deployment  (CPU, 7900xtx) [TTS]
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

## Node placement & throughput

TTS is **CPU-bound** (torch 2.8.0+cpu, 82M StyleTTS2) and warm RTF is dominated
by **per-core clock**, not core count. Placement therefore matters:

| Node | CPU | Warm RTF (294-char input) |
|---|---|---|
| k3s-w-10 (4-core VM) | weak | **1.07** — slower than real-time |
| cblevins-gtx980ti | i7-3770K Ivy Bridge (no AVX2/FMA) | worse than k3s-w-10 |
| **cblevins-7900xtx** (pinned) | strong 24-core | **0.207** — ~4.8× real-time |

The Deployment **hard-pins to `cblevins-7900xtx`** via
`requiredDuringSchedulingIgnoredDuringExecution` nodeAffinity plus a
`dedicated=gpu:NoSchedule` toleration (`deploy/system/kokoro-tts/deployment.yaml`).

- **Why required, not preferred**: a soft preference loses to the scheduler's
  resource-balancing score. Both GPU nodes share the `dedicated=gpu` taint our
  toleration opens, so a soft preference drifted the pod to the emptier but
  much slower gtx980ti. A hard pin is the only reliable placement.
- **Why no availability cost**: gemma4-26b (the LLM half of the stack) already
  runs on 7900xtx, so TTS shares fate with the LLM — if that node is down the
  voice stack is down regardless.
- **Why it doesn't starve GPU serving**: GPU inference is GPU-bound (~16/24
  cores requested on the node, ~8 free). TTS requests 2 cores, caps at 8, and
  `OMP_NUM_THREADS=8` pins torch intra-op threads to the cap so it can't
  oversubscribe the 24-core host. The conversational loop is sequential
  (LLM finishes → TTS) so the two barely overlap.

**If 7900xtx is decommissioned or renamed**, update the `nodeAffinity` hostname
(and the gemma4-26b Model CR placement) — the pod will stay `Pending` until a
node matches. Keep `OMP_NUM_THREADS` in lock-step with `resources.limits.cpu`.

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
