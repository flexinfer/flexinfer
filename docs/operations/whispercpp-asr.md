# ASR on the Radeon VII (whisper.cpp)

Speech-to-text through the Model API. The `whispercpp` backend runs whisper.cpp's `whisper-server`
inside the gfx906 runtime pod on `cblevins-radeonvii`, beside the retrieval plane, Orpheus and
pyannote. It serves the OpenAI `POST /v1/audio/transcriptions` contract through `flexinfer-proxy`.

Decision, kill-test and apply plan:
- `.loom/10-research-voice-stack-and-gpu-placement-2026-10-30.md` §4, §4.1 (KT-ASR results) and §4.2;
- the harness and evidence in `.loom/kt-asr-2026-10-30/`.

## Architecture

```
caller ──/v1/audio/transcriptions (multipart, model=<lane or label>)──> flexinfer-proxy
   └─> Model Service ─> runtime model ingress (adds OpenAI `words`) ─> whisper-server (gfx906)
```

- **Binary:** `/opt/whispercpp/bin/whisper-server` in the gfx906 runtime image.
  - whisper.cpp `v1.9.5` (`build/runtime.yaml` `whispercpp_version`), HIP build for gfx906.
  - Statically linked, so it never loads llama.cpp's `libggml` from `/opt/llamacpp/bin`.
- **Weights:** `HF://ggerganov/whisper.cpp` with `config.modelFile`, for example
  `ggml-large-v3-turbo-q8_0.bin`.
  - The Local cache prefetch downloads only that file.
- **Ingress:** whisper-server lists sub-word tokens under `segments[].words`.
  - The runtime ingress (`internal/runtime/whispercpp_words.go`) merges them into the OpenAI
    top-level `words` list (`{word, start, end, probability}`).
  - ICC's aligner reads that list. A whispercpp Model always gets this ingress, with or without
    `FLEXINFER_RUNTIME_REQUEST_DRAIN`.
- **Routing:** the proxy resolves the multipart `model` field like any other Model.
  - No static route or upstream env is involved.
  - LiteLLM advertises the lane as `audio_transcription` because its `serve-paths` is audio-only.

## Request contract

```bash
curl -sS http://flexinfer-proxy.flexinfer-system.svc/v1/audio/transcriptions \
  -F model=whisper-large-v3-turbo-radeonvii \
  -F response_format=verbose_json \
  -F 'timestamp_granularities[]=word' \
  -F file=@meeting.wav
```

- **Audio:** WAV, MP3, FLAC or OGG. The runtime image has no ffmpeg, so convert m4a and webm
  first.
- **`response_format`:** `json` (default), `text`, `srt`, `vtt` or `verbose_json`.
- **Optional fields:** `language` (the lane default is `auto`), `prompt` and `temperature`.
- **Concurrency:** whisper-server handles one request at a time; later requests wait on its mutex.
- **Endpoints:** whisper-server also serves `POST /load`, which loads any server-side path, and
  `/inference`. The Model's `flexinfer.ai/serve-paths` must stay `/v1/audio/transcriptions` only.
- **Timeouts:** whisper-server sends nothing until the transcript is done.
  - The Model therefore sets `flexinfer.ai/lane-first-byte-budget`.
  - Size it for the longest recording you expect at the measured speed.

## Measured on the Radeon VII (KT-ASR, 2026-10-30)

`large-v3-turbo` q8_0, `GPU_MAX_HW_QUEUES=1`, `HSA_CU_MASK=0:0-39`:

| | Value |
|---|---|
| 60-minute meeting | 103.6 s, 34.7× real time (41.4× without the mask) |
| VRAM per process | 1,319 MiB |
| HWS compute queues | 3 (0 SDMA) |
| Load | 3.0–3.5 s |
| WER, AMI EN2001a 60 min | 21.0 % (production faster-whisper large-v3: 21.2 %) |
| gte / bge p95 during a transcription | −0.2 % / −9.6 % vs baseline (without the mask: +38–54 % / +13–40 %) |

**Why the CU mask.** Unmasked, whisper.cpp keeps all 60 compute units busy, and the hardware
scheduler queues gte's kernels behind it.
- `HSA_CU_MASK=0:0-39` in the Model's `config.env` confines whisper-server to 40 CUs. KFD spreads
  them evenly over the 4 shader engines.
- The retrieval lanes then run beside it at their baseline latency, while the lane stays 7× above
  the 5× real-time bar.
- The mask reaches whisper-server alone, through the runtime load payload.

## Placement

- **Priority:** below gte-qwen2 (114).
  - Humanizer (130, 11,000 MB) plus bge-large, bge-reranker and gte already uses 15,800 of
    16,384 MB.
  - A lane ranked above gte would therefore evict gte during a Humanizer burst. At its lower
    priority the ASR lane is evicted instead, and its in-pod reload takes seconds.
- **Queues:** warm-pinned (`minReplicas: 1`), with `hwQueueEstimate: 3`.
  - The card holds about 18 of 24 compute queues with the warm plane, pyannote and ASR.

## Operations

- **Health:** `GET /health` on the lane returns `{"status":"ok"}` once the model is loaded and
  503 while it loads.
- **Count queues on the node:** run from any privileged pod with `/sys` visible:
  ```bash
  for d in /sys/class/kfd/kfd/proc/*; do c=0; for q in $d/queues/*; do [ "$(cat $q/type)" = 0 ] && c=$((c+1)); done; echo "$(basename $d) $c"; done
  ```
- **Rollback:** delete or revert the Model. ICC keeps working against the CPU faster-whisper
  Deployment in namespace `ai` until its `ICC_ASR_WHISPER_URL` moves to the proxy.
