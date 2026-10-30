# Voice stack audit and gfx1100 placement and priority strategy (2026-10-30)

**Owner goal:** "Ensure the voice/transcription/diarization stack is properly built and configured
to run alongside our current model layout (workhorse, retrieval stack, imagegen), and figure out a
proper strategy for priority and swapping. Ideally one of the gfx1100 always has the workhorse
available, with demand allowing the other to do imagegen or voice or video work."

**Sources:** live reads on 2026-10-30 between 14:50Z and 15:30Z through the flexinfer MCP, the k3s
MCP (get, describe, logs, read-only exec), Prometheus, Loki and Harbor's unauthenticated artifact
API. The only probes were short requests to lanes that were already Ready: one Orpheus completion,
two pyannote `/diarize` calls and two faster-whisper transcriptions. Nothing was activated,
patched or restarted. No load went to the 7900 XTX GPU.

**Deliverables:**
- This document.
- MR 1 `feat/voice-stack-fixes`, which is live-neutral.
- MR 2 `feat/gpu-placement-layout`, which is live-impacting. The parent session applies it in a
  scheduled window after the XTX soak.

## 0. Decisions in one screen

1. **The 5930k is the always-on workhorse card.** On it, `qwen38-27b-autoround-workhorse` runs at
   priority 900, `minReplicas: 1`, `warmPolicy: primary`. Nothing in that group can preempt it.
   The 5930k reported GPU-healthy for the whole 7-day window. The XTX was out for about 41 h of
   those 7 days (§2.4).
2. **The 7900 XTX becomes the flex card.** Its idle tenant is the Mills warm lane at priority 300,
   `minReplicas: 1`, `warmPolicy: ondemand`. The creative lanes move there at one shared priority,
   500: Qwen-Image 2.1, Wan T2V, Wan VACE and Stable Audio.
   - A creative request preempts the warm lane at once, because 500 is above 300.
   - Creative lanes queue first-come-first-served among themselves. Equal priority means a swap
     only after the holder has been idle for 2 min, so no render is killed mid-flight.
   - The warm lane comes back on its own when the creative lane idles out after 15 min, through the
     warm-pinned fallback in `chooseSharedGroupLeader`.
3. **Voice stays off the gfx1100 cards.**
   - TTS: Orpheus on the Radeon VII, plus Kokoro on CPU.
   - Diarization: pyannote on the VII.
   - ASR: target is whisper.cpp (ggml-hip) `large-v3-turbo` on the VII, behind a kill-test (§4).
     Until then, the faster-whisper CPU Deployment that ICC already uses stays the production ASR.
   - The parked vLLM Whisper lane on the XTX stays parked.
4. **Failover:**
   - XTX loss: creative and the Mills warm lane go down. The workhorse is untouched.
   - 5930k loss: label-addressed `workhorse` traffic moves to the XTX warm lane in one request,
     because the labels are now shared.
   - Two cases need small code changes: concrete-name callers (Mills, LiteLLM's alias rewrite), and
     a workhorse failover that must preempt an active creative job (§8, G1 to G3).
5. **S6b changes target.** In-pod swaps pay off on the card that swaps, which is now the XTX, not
   the 5930k (§7).

## 1. Current state (live, 2026-10-30)

| Card | Group, election | Active lane | Other members |
|---|---|---|---|
| `cblevins-5930k` (gfx1100, 24 GB, stable) | `5930k-textgen`, multiModel, budget 24,576 MB | `qwen38-27b-autoround-workhorse`: Ready, priority 500, ondemand, `minReplicas 0`, idle 12h, 22,000 MB | Preempted or Idle: qwen36-27b workhorse (500), fable (500), muse-glimmer (500), qwen-image-21 (500), wan21 T2V (500), wan21 VACE (600), qwen35-9b RP 5930k twin (550), stable-audio (450), qwen36-35b uncensored (390), qwen35-35b workhorse (300), openjev gfx1100 (200) |
| `cblevins-7900xtx` (gfx1100, 24 GB plus iGPU, flaky) | `7900xtx-textgen`, multiModel | `qwen38-27b-xtx-warm-canary`: Ready, priority 470, primary, `minReplicas 1`, 23,200 MB, `gpu.count 2`, runs as a dedicated Deployment | Idle, all statically parked behind the warm lane: orpheus XTX (460), snac (430), whisper (340, `litellm` off), 9B RP (500), 35B 128k (300), 35B MTP (90). The XTX runtime pod `cfefae18` holds no models; the GPU sits at 23.5 of 24.5 GB used. |
| `cblevins-radeonvii` (gfx906, 16 GB, 24 HWS queues) | `radeonvii-models`, multiModel, budget 16,384 MB, 22 queues for Models (24 minus 2 reserved for pyannote since !1198) | In the runtime pod: bge-large (120), bge-reranker (115), gte-qwen2 (114), orpheus (113), all warm-pinned. 6.0 GB of 16.4 GB used, including pyannote. | Cold: humanizer (130, 11,000 MB), gemma4-e4b (110), openjev (112), qwen3-embedding-4b (89), qwen3-reranker (88). pyannote runs outside the Model API (privileged, hostPath `/dev/kfd`). |
| `cblevins-gtx980ti` | none | dreamshaper8 imagegen | |

Election facts that drive everything below (`controllers/model_shared_gpu.go`):

- **Demand swap.** A demanded member preempts the Ready leader if its priority is strictly higher.
  At equal priority it preempts only if the leader saw no demand in the last
  `min(idleTimeout, 2m)`. There is a 5-minute anti-thrash cooldown after any preemption.
- **Primary reclaim.** A `warmPolicy: primary` lane takes the card back from a Ready borrower once
  the borrower is idle under that same 2-minute window. It does not wait for the borrower's
  `idleTimeout`.
- **Warm-pinned fallback.** With no member Ready, recent or demanded, the election prefers a member
  with `minReplicas ≥ 1`.
- **Static park.** A lower-priority member behind a warm-pinned or primary leader is stamped
  `primary/<leader>`. The proxy then fails it fast with 503 and records no demand, unless its VRAM
  (and on gfx906 its queue estimate) still fits beside the Active set.
- **GPU-degraded or unschedulable node.** The Model goes `Failed` with `Schedulable=False`
  `GPUDegraded`. The proxy fails queued requests fast (`ModelFailedError`), and a Failed member
  cannot win the election.

**Why imagegen is effectively broken today.** Qwen-Image (500) ties with a workhorse that rarely
idles for 2 minutes. It ranks below the 9B RP twin (550) and Wan VACE (600). Over 7 days it
recorded:
- 3 successes, 237 errors, 82 `lane_unresponsive` and 8 timeouts.
- On 10-29 from 23:52Z to 00:07Z, the studio re-queued every 30 s. The activation timed out after
  15 min because the 9B RP twin held the card (Loki, proxy).

The cause is not a 12 h `idleTimeout`. The swap depends on the 2-minute demand window, and the
workhorse keeps getting traffic: 15,525 requests in 7 days.

## 2. Measurements

### 2.1 Usage, last 7 days (`flexinfer_proxy_requests_total`)

| Lane | Success | Failure | Note |
|---|---|---|---|
| qwen38-27b-autoround-workhorse | 14,718 | 477 error, 94 timeout, 57 upstream, 175 `parked_behind_primary` | The 175 parks were from the S3 canary window on 10-26. |
| qwen38-27b-xtx-warm-canary | 74 | 461 error, 6 timeout | 196 errors and 0 successes during the 39 h XTX outage. |
| qwen-image-21-gfx1100 | 3 | 237 error, 82 lane_unresponsive, 8 timeout | |
| orpheus-3b-radeonvii | 107 | 0 | |
| wan21 ×2, stable-audio, snac | 0 real | 256 `endpoint_unsupported` each | The 256 are chat-path probes. |
| qwen35-9b-ablit-rp-5930k | 198 | 58 | Psyche. It preempted the workhorse twice on 10-29, about 6 and 81 min. |
| gte-qwen2-1p5b-radeonvii | 750,933 | | `embeddings-1536` is the LiteLLM top model at 1.1 req/s. |

**LiteLLM traffic:**
- `embeddings-1536`: 1.10 req/s.
- No `requested_model` label (health checks and `/v1/models`): 0.36 req/s.
- `qwen38-27b-autoround-workhorse` by its concrete name: 0.018 req/s.
- The alias `workhorse`: 0.0001 req/s.

The mobile HUD's polling does not reach a text GPU lane. Mills calls flexinfer-proxy directly with
concrete serving names, because its client validates ids against `/v1/models`, so labels 404.
Source: gitops `k3s/mills/deployment.yaml`.

### 2.2 Swap costs (`flexinfer_model_phase`, 1-minute resolution, 10-28 to 10-30)

"Election" means the Loki line `Preempting model` / `Model is active in shared group`.

| Transition | Election to Ready | Samples |
|---|---|---|
| 27B workhorse back in (dedicated, cache and compile cache warm, image resident) | **~3 min** (Loading 1–2 min, then Ready) | 5: 10-28 15:36→15:39, 16:05→16:09; 10-29 15:03→15:06, 16:47→16:50; 10-30 00:36→00:39 |
| Qwen-Image 2.1 first activation (18.3 GB image pull plus first compile) | **31 min** | 10-28 15:01→15:33 |
| Qwen-Image 2.1 warm swap-in | **~2–3 min** | 10-28 16:00→16:03; 10-29 14:58→15:01, 15:19→15:22 |
| qwen35-9b RP twin | ~2 min | 10-29 15:09→15:11, 23:14→23:15 |
| muse-glimmer 30B GGUF (llama.cpp) | ~3 min | 10-29 23:08→23:11 |
| XTX warm lane after the cold boot | ~4 min | runtime pod started 00:15:43Z → Ready 00:19Z |
| XTX warm lane restart | ~3 min | 10-29 15:42→15:45 |
| gfx906 in-pod llama.cpp loads (`flexinfer_runtime_model_load_duration_seconds`, 30 d) | **4–10 s** mean | orpheus 5.1 s (13 loads), bge 4.0 s, reranker 5.0 s, humanizer 10.0 s |
| June Whisper on XTX, vLLM 0.17, in the runtime pod | 43 s cold; transcript 46 s end-to-end for a 3 s clip | `.loom/whisper-prod-cr-live-validation-2026-06-02.md` |

A full creative round trip costs about **6 min of card time** on gfx1100 dedicated Deployments: about
3 min for the creative lane to come in, then about 3 min for the text lane to come back. Add 5 min
of cooldown before the next swap.

**Measurement gap (G5).** `flexinfer_model_swap_duration_seconds` is measured from `PreemptedAt`,
and `flexinfer_model_cold_start_duration_seconds` from the Ready=False transition. Both include the
time a lane spent queued. That is why the 30-day means read 1,656 s for the workhorse swap and
12,158 s for the Qwen-Image cold start. Neither is a load cost. §8 G5 proposes a load-only
histogram.

### 2.3 Voice probes

| Probe | Result |
|---|---|
| Orpheus `/v1/completions` via proxy, model `orpheus-3b-radeonvii` | HTTP 200 in 2.31 s. 188 audio tokens, about 2.3 s of speech. Decode 99.7 tok/s; real time is about 82 tok/s, so the token half runs at 1.2× real time on the VII. |
| pyannote `/diarize` via proxy, 11.6 s two-speaker clip (macOS `say`: Daniel, then Samantha) | HTTP 200. Speaker change found at 5.5–6.0 s, which matches the clip. **First call 15.6 s, second 0.40 s.** The first call pays a warm-up. |
| faster-whisper CPU (ns `ai`, on the 5930k), `Systran/faster-whisper-large-v3`, int8, same clip | Exact transcript apart from "radion". **First call 115 s** (model load), **warm 19.2 s**, which is 0.6× real time. A 1-hour meeting would take about 100 min. |
| `kokoro-tts` `/health`, `tts-lane` `/healthz`, pyannote `/health` | All healthy. Kokoro was not exercised: it runs on the XTX host. |

### 2.4 XTX availability (`flexinfer_gpu_healthy`, 30-minute resolution, 7 days)

- Healthy until 10-28 00:30Z.
- Degraded 10-28 00:30Z to 10-29 about 15:40Z, about 39 h.
- Dropped again 10-29 22:52Z.
- Cold-booted for the soak at 10-30 00:15Z.

Roughly 41 h out of 168 h. The 5930k and the VII stayed at 1 for the whole window. `tts-lane`
restarted at 15:40Z, 22:50Z and 00:15Z on 10-29 and 10-30, tracking the XTX. `kokoro-tts` has 9
restarts on the same node.

## 3. Voice stack audit

| Component | Built and pinned? | Model API? | Proxy / LiteLLM | Works today? | Broken or missing |
|---|---|---|---|---|---|
| **Orpheus-3B TTS tokens, VII** `orpheus-3b-radeonvii` | gfx906 runtime `90648c35`: Harbor tag `pinned-90648c350783`, in the radeonvii prewarm list. GGUF Q4_K_M in the Local cache. | Yes, warm-pinned at priority 113. | Proxy yes (`/v1/completions`, label `orpheus-tts`). LiteLLM off, by design. | **Yes** (probe above). | Evicted by humanizer demand 4 times in 7 days (`flexinfer_sharedgroup_preemptions_total` orpheus→humanizer): humanizer 11,000 MB + bge + reranker + gte = 15,800 MB, so orpheus (3,300 MB) no longer fits in 16,384. Reload costs about 5 s in-pod, but TTS stalls for the length of the humanizer burst. |
| **Orpheus XTX twin** `orpheus-3b-7900xtx` | Image resolves to `flexinfer/llamacpp:rocm6.4.4-gfx1100-b8173-v2`, which is tag-pinned. Harbor `153cf8fc` is present. | Yes, parked at priority 460, `minReplicas 0`. | Proxy: statically parked, so 503. | Parked by design (2026-09-13). | **Latent trap under MR 2:** with the warm lane at 300, a 460 twin would preempt it. That happens whenever the VII orpheus is not Ready, because the `orpheus-tts` group then cold-starts the top route-priority member. MR 2 drops it to 250. |
| **SNAC decode** `snac-decode-gfx1100` | XTX runtime `cfefae18` (`pinned-cfefae18061f`, prewarmed). | Yes, at priority 430. | Proxy: 2 `parked_behind_primary` in 7 days. | No. It is statically parked behind the 23,200 MB warm lane (24,700 MB > 24,576). | `tts-lane` 0.5.0 falls back to **local decode on the XTX GPU through hostPath `/dev/kfd`**, outside the election: no VRAM or queue admission, pinned to the flaky node. Same trap as the Orpheus twin under MR 2, so it drops to 250. |
| **tts-lane** (gitops `k3s/ai/tts`, labs/tts-lane) | `flexinfer/tts-lane:0.5.0` in Harbor (`6bc892d3`). Tag only, no `pinned-*` (gitops is not scanned). | No. | Its own ingress, `tts.lan`. | Yes. 9 speech requests on 10-27, the only ones in Loki's window. | Pinned to `cblevins-7900xtx` for GPU SNAC decode, so TTS goes down with the XTX host even though Orpheus is on the VII. Recommendation G9: unpin and decode on CPU. That is a gitops change. |
| **Kokoro-82M TTS, CPU** `kokoro-tts` | `flexinfer/kokoro-fastapi-cpu@11f49aaf` (`pinned-11f49aafe8a0`). | No (Deployment). | Proxy `/v1/audio/speech` is a static route. LiteLLM: no. | Healthy. | Prefers the XTX host and falls back to the 5930k. 9 restarts that track the XTX. Its RTF on the 5930k was never kill-tested; recommend testing, then flipping the preference. |
| **ASR (production)** `faster-whisper` in ns `ai` (gitops) | `docker.io/fedirz/faster-whisper-server@760e5e43`, a Docker Hub digest pin not mirrored to Harbor. Model `Systran/faster-whisper-large-v3` on a PVC. | No. | **Not reachable through flexinfer-proxy or LiteLLM.** ICC calls `faster-whisper.ai.svc:8000` directly (gitops ICC `ICC_ASR_WHISPER_URL`). | **Yes, but 0.6× real time** warm. First request after idle about 115 s. | Slow. Only one CPU replica on the host shared with the workhorse. Image not in Harbor. |
| **ASR (flexinfer)** `whisper-large-v3-turbo` | vLLM path; the GPUProfile `vllm.image` `7ac257f3` is pinned. On the XTX it would load in the runtime pod (`cfefae18`, vLLM 0.17), the build that passed in June. | Yes, parked: `litellm.enabled: false`, labels stripped. | Proxy returns 404 (parked gate) without touching demand. | No. The card is full. | It is servable on gfx1100 (June: HTTP 200, exact transcript), but every use would swap out a 23 GB 27B. Its header blamed the retired gemma4 26B; MR 1 fixes that. `voice-loop`'s default `-asr-model` points here, so ASR through the proxy has **no working lane**. |
| **Diarization** `pyannote-diarization` | `flexinfer/pyannote-diarization@3ea89869` (`pinned-3ea89869f61f`). | **No**: hand-written Deployment, privileged hostPath GPU. | Proxy `/diarize` is a static route (`FLEXINFER_PYANNOTE_UPSTREAM`). ICC calls the Service directly. | **Yes** (probe). | Outside VRAM admission. Its 2 compute queues are reserved by hand (`reservedHWQueues: 2`, !1198). First call 15.6 s. Model API move: see §5. |
| **Stable Audio** (music) `stable-audio-open-gfx1100` | `diffusers:rocm-gfx1100-audio2` plus `imageDigest d87f75f8`. **No `pinned-*` tag**: the pin guard only matches `repo@sha256:` lines. Not in any prewarm list. Last pulled 2026-08-23, rank 5 of about 32 in the repo. | Yes. | Proxy `/v1/audio/generations`. | Untested in 7 days: 0 real requests. | **MR 1:** writes the digest into `image:` so the guard tags it. The rendered image is unchanged (`backend.ApplyImageDigest`). |
| **voice-loop CLI** `cmd/voice-loop` | | | | Defaults are broken: `-chat-model gemma4-26b-a4b-gptq` was retired 2026-09-13, and `-asr-model whisper-large-v3-turbo` is parked. | **MR 1:** chat default becomes `workhorse`. ASR stays explicit until G6 or G7 land. |

## 4. ASR decision

**Riskiest assumption (as briefed):** "ASR is servable on gfx1100 at all."

**Verdict:** it is servable, and that was never the blocker.
- vLLM 0.17 on the XTX returned an exact transcript on 2026-06-02.
- Upstream fixed encoder-decoder on ROCm Triton attention:
  - vLLM PR #28346 added `ENCODER_DECODER` to `TRITON_ATTN`, merged 2025-11-21.
  - Issue #28184 was closed 2025-12-11 after a gfx1101 test.
  - vLLM's attention-backend matrix lists TRITON_ATTN with all attention types. AITER is gfx9 only.
    So a vLLM 0.32 Whisper lane on gfx1100 would set `attentionBackend: TRITON_ATTN`.

The blocker is placement. Both gfx1100 cards are filled by 22–23 GB text lanes. Any ASR request
there costs a ~3 min swap in and a ~3 min swap back, which was exactly the pathology that parked
the lane in June. It would also sit on the card that was down 41 h last week.

**Options compared:**

| Option | Availability | Speed | Fit | Evidence |
|---|---|---|---|---|
| vLLM Whisper on gfx1100 (XTX flex tier) | Only when the creative tier is free; down with the XTX | Fast once loaded; 3 min swap | Model API today | June PASS; vLLM #28346 |
| **whisper.cpp ggml-hip on the VII** | Always on: stable card, 10 GB VRAM and 8 queues free | Unknown on gfx906. Kill-test. | New thin backend plus a gfx906 image layer | whisper.cpp v1.9.5 (2026-10-06, ggml v0.26.0). The same ggml-hip already serves the VII llama.cpp lanes. Server supports `--inference-path /v1/audio/transcriptions` and `verbose_json` with segment words. A Radeon VII large-v3 HIPBLAS report exists without timings (debian-ai list, 2024-02). |
| CTranslate2 ROCm (faster-whisper on GPU) | n/a | Fast on RDNA | Needs ROCm 7. **gfx906 is not supported on ROCm 7**, and gfx1100 is full. | CT2 v4.7.0 (2026-02-03) added ROCm HIP (#1989) |
| faster-whisper CPU int8 (today), 7900X3D AVX-512 host | The 7900X3D is the **XTX host**, which hangs with the card | 0.6× real time on the 5930k CPU (measured); AVX-512 helps, but CT2 uses oneDNN on AMD and documents no VNNI gain | Gitops Deployment, outside flexinfer | Probe above; CT2 hardware docs |
| Parakeet-TDT-0.6B-v2 on CPU (onnx-asr) | Always on | RTFx about 36 on a 9800X3D; better WER (6.05 % vs about 7.4 for large-v3); word timestamps | Community OpenAI servers only; English only; CC-BY-4.0 | HF model card; onnx-asr README |

**Decision:** **whisper.cpp ggml-hip, `ggml-large-v3-turbo` (q8_0), warm-pinned on the Radeon VII in
the gfx906 runtime pod as a new thin `whispercpp` backend.**
- Labels: `asr`, `transcription`, `whisper`. LiteLLM mode `audio_transcription`.
- Fallback order if its kill-test fails:
  1. Parakeet-TDT-0.6B-v2 int8 on the 5930k CPU.
  2. faster-whisper `large-v3-turbo` on CPU.
- The vLLM XTX lane stays parked as an optional burst tier for long recordings in a creative window.

Why the VII:
- It is the only always-on accelerator with room.
- It already runs ggml-hip in production.
- Diarization already lives there, so the meeting pipeline runs on one node.
- It never preempts a 27B.

Why not Parakeet first: it is the strongest CPU option on paper. But it brings a new framework and
a community server, and it is English only. whisper.cpp reuses the toolchain we already build for
gfx906. Parakeet is the named pivot if the kill-test fails.

**Kill-test KT-ASR.** Run it before any backend code merges. It is a one-shot pod on the VII, built
off-CI is fine.
1. Build whisper.cpp v1.9.5 with `-DGGML_HIP=1 -DAMDGPU_TARGETS=gfx906` on the gfx906 runtime
   builder base. Run `whisper-server --inference-path /v1/audio/transcriptions` with
   `GPU_MAX_HW_QUEUES=1`, as the gfx906 profile sets.
2. Fixtures:
   - the 11.6 s two-speaker clip from §2.3, regenerated with `say`;
   - a 10-minute meeting excerpt;
   - a 60-minute recording.
3. Pass when **all** of these hold:
   - (a) Transcript within 5 % normalized WER of faster-whisper `large-v3` on the same files.
   - (b) Warm throughput at least **5× real time** on the 60-minute file, that is at most 12 min.
   - (c) VRAM at most 2.5 GB. At most 3 compute queues, read from
     `/sys/class/kfd/kfd/proc/*/queues`, keeping the plane total at or under 24.
   - (d) gte-qwen2 and bge-large p95 at their own Services within 20 % of baseline during the run.
     No `Runlist is getting oversubscribed` in dmesg.
   - (e) `response_format=verbose_json` returns segment and word timings that ICC's
     `asr_align.py` accepts.
4. If (b) fails, retry with flash attention off (`-nfa`) and `large-v3-turbo` f16. Then pivot to
   Parakeet with its own kill-test: RTFx ≥ 10 at 4 threads, WER ≤ faster-whisper.

## 5. Diarization: what moving pyannote into the Model API needs

Today's backends are comfyui, diffusers, llamacpp, mlc-llm, ollama, snac, steam, sunshine, vllm and
vllm-omni (`backend/registry.go`). None of them can host pyannote. Missing pieces:

1. **A `pyannote` backend.** A thin `backend/pyannote.go` shaped like `backend/snac.go` (71 lines),
   with these properties:
   - no image of its own, since it is a runtime subprocess;
   - port, `/health` probes and `NeedsVolume false`;
   - plus the runtime launcher entry for a `server-pyannote.py`.
2. **An image layer.** `pyannote.audio` 3.3.2 plus the `torchaudio_compat` shim, on a torch build
   with gfx906 kernels. Today that is the `mixa3607/pytorch-gfx906` base in
   `build/Dockerfile.pyannote-rocm-gfx906`. Before choosing between the shared gfx906 runtime image
   and a sibling, check whether its torch carries gfx906 Tensile. tts-lane's torch 2.9.1+rocm6.4
   does not.
3. **The gated HF token** (`flexinfer-hf-token`) in the runtime pod environment. This is a
   security-visible change.
4. **Proxy routing.** `/diarize` is a static path. Either keep it and point
   `FLEXINFER_PYANNOTE_UPSTREAM` at the Model's Service, or route by the multipart `model` field.
5. **Queue accounting.** Set `reservedHWQueues: 2 → 0` and Model `hwQueueEstimate: 2` in the same
   change. The gfx906 GPUProfile and the queue-admission code belong to the parallel slice. **This
   doc only states the need:** when pyannote becomes a Model, the 2 reserved queues must move from
   the profile to the Model, atomically.
6. **Upstream.** pyannote.audio 4.0.7 (2026-06-30) recommends `speaker-diarization-community-1`. It
   improves DER over 3.1, for example AMI-IHM 17.0 vs 18.8, and needs no GPU. Re-evaluate that
   pipeline with the move, not before.

Until then pyannote stays a Deployment. Its VRAM (about 1.65 GiB) is not in the election's budget.
The VII has the headroom, so that is acceptable.

## 6. Target layout

Priorities only matter within a group. The 5930k and the XTX are separate groups, so the 900 and
the 500 are not compared with each other.

| Card | Lane | Priority | Warm policy | minReplicas | idleTimeout | Aliases / labels | Role |
|---|---|---|---|---|---|---|---|
| **5930k** (always-on) | `qwen38-27b-autoround-workhorse` | **900** (was 500) | **primary** (was ondemand) | **1** (was 0) | 12h (unused) | `qwen38-27b`, `workhorse`, `loom-workhorse`, `reasoning`; `route-priority: 100` (new) | The workhorse. Never preempted in its group. |
| 5930k | every other member (qwen36-27b fallback, fable, glimmer, 9B RP twin, qwen36-35b uncensored, qwen35-35b, openjev) | unchanged | unchanged | 0 | unchanged | unchanged | Statically parked. The proxy answers 503 immediately. Owner decides which move to the XTX tier (§11). |
| **XTX** (flex) | `qwen38-27b-xtx-warm-canary` | **300** (was 470) | **ondemand** (was primary) | 1 | 12h | `mills-warm`, `mills-council`, `xtx-warm` plus **shared** `workhorse`, `loom-workhorse`, `reasoning`, `qwen38-27b` | Idle tenant. Context-overflow target. Workhorse failover. |
| XTX | `qwen-image-21-gfx1100` (moved) | 500 | ondemand | 0 | 15m | `qwen-image`, `qwen-image-2.1` | Studio |
| XTX | `wan21-t2v-1p3b-gfx1100` (moved) | 500 | ondemand | 0 | 15m (was 30m) | `video-gen`, `text-to-video`, … | Video |
| XTX | `wan21-vace-1p3b-i2v-gfx1100` (moved) | 500 (was 600) | ondemand | 0 | 15m | `video-i2v`, `image-to-video`, … | Video |
| XTX | `stable-audio-open-gfx1100` (moved) | 500 (was 450) | ondemand | 0 | 15m | `music-gen`, `text-to-music` | Music |
| XTX | `qwen35-9b-ablit-rp` | 500 (unchanged) | ondemand | 0 | 30m | `qwen35-9b-ablit-rp` | RP, in the creative tier as today |
| XTX | `snac-decode-gfx1100`, `orpheus-3b-7900xtx`, `whisper-large-v3-turbo`, `qwen35-35b-clean-gptq-workhorse-128k` | 250, 250, 290, 290 (were 430, 460, 340, 300) | ondemand | 0 | unchanged | unchanged | Kept below the idle tenant, so they stay parked exactly as today. SNAC can still sit beside a creative lane up to 23 GB. |
| XTX | `qwen35-35b-clean-mtp-32k` | 90 | ondemand | 0 | 30m | unchanged | Parked (unchanged) |
| **VII** | bge-large / bge-reranker / gte-qwen2 | 120 / 115 / 114 | warm | 1 | 30m | unchanged | Retrieval plane |
| VII | orpheus-3b-radeonvii | 113 | warm | 1 | 30m | `orpheus-tts` | TTS tokens |
| VII | *(KT-ASR, then MR)* whispercpp large-v3-turbo | 116 | warm | 1 | 30m | `asr`, `transcription`, `whisper` | ASR; about 2 GB, 3 queues |
| VII | pyannote (Deployment → Model later) | n/a (later 117) | always on | 1 | n/a | `/diarize` | Diarization |
| VII | humanizer, gemma4-e4b, openjev, qwen3-embedding, qwen3-reranker | unchanged in MR 2 | ondemand | 0 | unchanged | unchanged | Cold. Humanizer needs a decision (§11). |
| CPU | kokoro (XTX host CPU, falls back to 5930k), faster-whisper (5930k CPU, ns `ai`) | n/a | always on | 1 | n/a | `/v1/audio/speech`; ICC direct | TTS; ASR until KT-ASR passes |

**Why the always-on card is the 5930k:**
- Over the last 7 days the XTX was degraded about 41 of 168 h. The 5930k was never degraded.
- An always-on lane on a card that falls off PCIe is not always-on.
- On the flex card, losing the card costs only best-effort tenants (creative work and a second
  text copy).
- The soak's outcome does not change this. If the XTX recovers, creative availability improves. If
  it does not, nothing that must be up is on it.

**Why the XTX idle tenant is `ondemand` and `minReplicas: 1`, not `primary`:**
- A primary reclaims the card 2 minutes after the borrower's last request, ignoring its 15-minute
  `idleTimeout`. A studio user who thinks for 3 minutes would pay about 6 minutes of swaps every
  few images.
- A warm-pinned on-demand tenant waits for the creative lane to scale to zero (15 min of quiet),
  then wins the election through the warm-pinned fallback.
- A lane higher than 300 is not statically parked by a warm-pinned 300 leader, so every creative
  lane still takes the card instantly.

**Why all creative lanes share 500.** Equal priority swaps only after 2 idle minutes. The proxy's
per-model heartbeat keeps a lane's demand fresh while a request is in flight, so a Wan render is
never killed by a Qwen-Image click, and the reverse also holds.

**Under multiModel the gfx1100 budget is 24,576 MB:**

| Combination | Total | Result |
|---|---|---|
| warm lane 23,200 + SNAC 1,500 | 24,700 | Does not fit, so SNAC stays parked behind the warm lane. |
| qwen-image 18,000 + SNAC 1,500 | 19,500 | Fits. |
| stable-audio 7,000 + SNAC 1,500 | 8,500 | Fits. |

So TTS waveform decode can run on the GPU while a creative lane holds the card. Two dedicated
creative pods cannot co-schedule, because each claims both XTX device-plugin slots (`gpu.count: 2`).

## 7. Radeon VII queue and VRAM budget for voice; what S6b changes

**VII.** The queue budget for Models is 22: `maxHWQueues` 24 minus `reservedHWQueues` 2 for
pyannote.

| Set | Queues | Estimated VRAM |
|---|---|---|
| Warm set: bge, reranker, gte, orpheus (llama.cpp at `GPU_MAX_HW_QUEUES=1`, 3 queues each) | 12 | 8,100 MB |
| … plus whisper.cpp | 15 | about 10,100 MB |
| … plus pyannote as a Model (reservation removed) | 17 of 24 | about 11,750 MB |
| Headroom left | 7 queues | about 4.6 GB |

What the headroom allows:
- Any two cold llama.cpp lanes concurrently, or openjev alone (vLLM is estimated at 6 queues).
- Humanizer's 11,000 MB estimate never fits beside the voice set. Today it evicts orpheus instead
  (§3). Live use is 6.0 GB for the warm set plus pyannote, against 8.1 GB estimated, so estimates
  run about 35 % high. **Voice lanes are bounded by queues before VRAM:** each additional voice
  process costs 3 of the 7 spare queues.

**S6b.** S6b as planned un-parks the 5930k runtime DaemonSet onto the stacked 0.32 image
`ef28b7b0` and moves the workhorse in-pod. Under this layout the 5930k no longer swaps, so S6b's
payoff there shrinks to spec-change reloads with drain.

Swap cost lives on the XTX. Today an XTX swap is about 3 min of Deployment churn: drain,
amd.com/gpu slot release, pod start, weight load, graph capture. In-pod the measured gfx906 llama.cpp
swaps are 4–10 s. Large gfx1100 lanes will not get that low, because §R7 of the 10-27 review notes
there is no weights-in-RAM reuse and a 19 GB NVMe re-read per swap. Expect:
- the 27B at about 1.5–2.5 min;
- diffusers at about 1–2 min;
- SNAC (and whisper.cpp, if ever on gfx1100) co-resident beside a diffusers lane instead of parked.

**Recommendation:** retarget S6b to the `gfx1100` (XTX) runtime profile — repin `cfefae18` to
`ef28b7b0` and move the warm lane plus the creative lanes in-pod — and leave `gfx1100-5930k` parked.
Owner decision D3.

## 8. Failover behaviour and the gaps that need code

### Scenarios with today's mechanisms plus MR 2

1. **XTX drops** (the common case).
   - The sentinel marks the node degraded and every XTX lane goes `Failed` within one reconcile.
   - The proxy fails creative and warm-lane requests fast.
   - The workhorse is unaffected. Mills's concrete-name calls to the warm lane fail fast and walk
     its client fallback chain to the 5930k workhorse.
   - Recovery: smart-plug power cycle, then about 4 min to warm-lane Ready (measured 10-30).
   - Creative work is unavailable until then. That is accepted: best effort on the flaky card.
2. **5930k drops** (rare).
   - `workhorse` / `loom-workhorse` / `reasoning` / `qwen38-27b` label requests go to the XTX warm
     lane in one request, because it is a Ready claimant. If the XTX is running a creative job, the
     warm lane is preempted and holds no labels. The label then resolves to the stale Failed 5930k
     member, and requests fail fast until the creative lane idles out: up to 15 min plus about
     3 min reload. **Gap G2.**
   - LiteLLM `workhorse` and every concrete-name caller keep targeting the dead lane. **Gaps G1, G3.**
3. **Both up, creative demand.**
   - The creative lane preempts the warm lane at once (500 > 300); first output in about 3 min, or
     about 31 min on a first-ever pull. The prewarm in MR 2 removes the pull.
   - Mills requests to the warm lane queue behind the creative lane until it idles out. Label
     callers move to the 5930k because the workhorse labels are shared.
   - Requests over 32K that overflow the 5930k lane get the original 400 while the warm lane is
     preempted. `finishContextOverflow` requires a Ready target. **Gap G1** covers this too.

### Gaps and the smallest fix for each

None of these touch `pkg/runtime/payload.go`, `internal/proxy/queue.go`,
`controllers/model_controller.go`, `controllers/model_deployment.go` or the API types.

| # | Gap | Smallest change | Where |
|---|---|---|---|
| **G1** | Concrete-name failover. A request for a lane that is `Failed` or preempted by a higher-priority holder queues or fails. It never moves to an equivalent Ready lane. Mills uses only concrete names, and LiteLLM rewrites aliases to concrete names. | Annotation `flexinfer.ai/unavailable-reroute-to: <model>`. When the requested Model is `Failed`, or Queued with `PreemptedBy` naming a higher-priority holder, and the target is Ready with no open breaker, serve once on the target (no chaining), exactly as `finishContextOverflow` does. Put it on both 27B lanes, pointing at each other. About 80 lines and tests. | `internal/proxy/proxy.go`, the branch before `handleColdStart`; reuse `context_overflow.go` helpers |
| **G2** | No failover priority. The workhorse copy on the flex card cannot preempt a creative job while the always-on card is dead. | Annotation `flexinfer.ai/failover-for: <model>`. In the election, the effective priority is the maximum of its own and the target's while the target is `Failed` with `Schedulable=False` (GPUDegraded or NoMatchingNodes). With G1 the first rerouted request then preempts the creative lane in about 3 min, instead of waiting up to 18 min. | `controllers/model_shared_gpu.go`: priority accessor used by `better` and the demand path, fed from the namespace Model list already read in `handleSharedGPU` |
| **G3** | LiteLLM rewrites `workhorse` to `openai/qwen38-27b-autoround-workhorse`, so the proxy's label-group failover never sees the label. | In the gitops LiteLLM generator, when an alias is a serviceLabel claimed by two or more Models, emit `openai/<label>` and let the proxy pick the Ready claimant. A static `external-models.yaml` entry is not a substitute: it drops `chat_template_kwargs` and uses the short timeout. | platform/gitops `k3s/ai/litellm/litellm.yaml`, the `gather_aliases` callers |
| **G4** | Primary reclaim ignores the borrower's `idleTimeout` (2-minute window). | Not needed under this layout. If a primary idle tenant is ever wanted, reclaim at `max(2m, borrower idleTimeout)`. | `model_shared_gpu.go` warm-primary branch |
| **G5** | Swap and cold-start histograms include queued time (§2.2). | `flexinfer_model_activation_to_ready_seconds{model,group,serving_path}`, observed from the reconcile that set `SharedGroup.State=Active` to Ready. | `controllers/model_helpers.go` (`observeSharedSwap` neighbour) |
| **G6** | No ASR through flexinfer-proxy. Whisper is parked and ICC's faster-whisper is outside it. | Interim: `FLEXINFER_ASR_UPSTREAM`, a static fallback for `/v1/audio/transcriptions` when the named model is not a servable Model, mirroring `handleSpeech`. Final: G7. | `internal/proxy/kokoro.go` pattern plus a Helm value |
| **G7** | ASR backend for the decision in §4. | `backend/whispercpp.go` (snac-sized), a runtime launcher entry, and a whisper.cpp v1.9.5 HIP build stage in the gfx906 runtime image. Gated on KT-ASR. | backend, `internal/runtime`, build |
| **G8** | pyannote outside the Model API. | §5, five pieces, coordinated with the queue-admission slice. | backend, build, proxy |
| **G9** | tts-lane decodes SNAC on the XTX through hostPath, outside admission, and is pinned to the flaky host. | Drop the `cblevins-7900xtx` nodeSelector, set `SNAC_DEVICE=cpu`, keep `SNAC_LANE_BASE` (it uses the snac lane whenever that is admitted). | platform/gitops `k3s/ai/tts/deployment.yaml` |
| **G10** | The pin guard and `check-runtime-pin-drift.py` ignore `imageDigest:` pins. | Parse `imageDigest:` next to `image:`. MR 1 removes the only unprotected instance. | gitops `harbor-flexinfer-pin-guard/pin-tags.yaml`, `scripts/check-runtime-pin-drift.py` |
| **G11** | "Always available" is approximated. A GamingSession or an ungated GPU lease still parks the 5930k group. | Policy: keep gaming off `cblevins-5930k` (the gaming approvedImages list is empty today) and give 5930k leases a `Priority` below 900. No code. | `values-k3s.yaml` gaming policy, lease authors |

## 9. Riskiest assumption of the layout, and its kill-test

**Assumption.** After the soak, the XTX can carry the creative tier plus the warm lane through
repeated swaps without dropping off PCIe. Every past failure came under sustained compute: SMU hangs
at 40 s to 2m41s under a matmul burn. Creative lanes are bursty, high-draw diffusers work.

**Kill-test KT-FLEX.** Run it after the soak passes, in the MR 2 window.
1. Pull the creative images through the XTX prewarm.
2. Run three Qwen-Image 1024² renders, one Wan T2V 480p clip and one Stable Audio 40 s render,
   in that order, through the proxy.
3. Let the warm lane reclaim each time.

Pass when all of these hold:
- each creative first output arrives within 5 min;
- the warm lane is Ready within 5 min of the last creative lane's idle-out;
- 0 SMU or `amdgpu` errors in the host kern.log;
- `flexinfer_gpu_healthy{node="cblevins-7900xtx"} == 1` for 24 h afterwards.

On failure, roll back MR 2 (§10). Creative work then returns to the 5930k behind a workhorse
reverted to priority 500, which is today's behaviour.

## 10. Rollout, apply order, rollback

### MR 1 (`feat/voice-stack-fixes`, live-neutral)

Contents:
- this doc and its index link;
- the stable-audio digest moved into `image:`;
- the whisper manifest header refresh;
- the `voice-loop` default chat model;
- the pyannote operations doc refresh;
- a changelog fragment.

Merge at any time. Neither Flux nor the controller changes a running pod.

### MR 2 (`feat/gpu-placement-layout`, live-impacting)

Window: **after the soak verdict, no earlier than 2026-11-06 00:15Z**, with the owner present.

**Pre-checks:**
1. The XTX has been healthy for 7 days.
2. XTX root has 324 GB free (2026-10-30). This MR adds about 59 GB of images and about 85 GB of
   caches.
3. The 5930k workhorse is Ready.
4. No creative render is running.

**Apply order** (single merge; Flux applies HelmRelease values and Models in one reconcile):
1. Merge MR 2.
2. Reconcile the HelmRelease: `flux_reconcile kind=hr name=flexinfer namespace=flexinfer-system
   with_source=true`. This rolls the XTX prewarm DaemonSet so it pulls `runtime@2c94c6d9`,
   `diffusers-video@83cda206` and `diffusers@d87f75f8`.
3. Reconcile `flexinfer-models`. On the XTX side, the warm lane moves to 300 ondemand, and SNAC,
   the Orpheus twin, Whisper and the 128k lane drop below it. Nothing restarts; all were parked.
4. The four creative Models change node and group. Each starts a Local `cache-stage` Job on the
   XTX, and its Cached condition is False until the Job completes. Qwen-Image is 33 GB from HF. Do
   not send creative requests until all four are `Cached=True` and the prewarm pod is Ready.
5. The 5930k workhorse moves to 900 primary, `minReplicas 1`, `route-priority 100`. It is already
   Active, so its pod template does not change and it does not restart. The other 5930k members
   become statically parked; check that they report `ParkedBehindPrimary`.
6. Run KT-FLEX (§9).
7. **Later, separately (not in this MR):**
   - drop the creative images from the 5930k prewarm list;
   - clean the orphaned 5930k caches (`compile-cache-qwen-image-21-5930k`, staged creative weights
     under `/var/lib/flexinfer/models`);
   - land G1/G2;
   - retarget S6b.

Expected live effects:
- No text outage.
- Creative lanes are unavailable for the length of the cache staging (estimate 30–60 min).
- After that they serve in about 3 min from cold.

**Rollback.** `git revert` the MR 2 merge commit, then reconcile `flexinfer-models` and the
HelmRelease.
- The creative Models return to the 5930k, where their caches are still staged (`stagedKey` holds
  the old node selector, so they re-mark Ready without a download).
- The workhorse returns to 500 ondemand.
- The warm lane returns to 470 primary.

The XTX keeps the pulled images until image GC, which is harmless. Run step 7 only after a week
without rollback.

## 11. Owner decisions

1. **D1. 5930k text variants.** Under always-on, these are parked (503):
   - the 9B RP 5930k twin (198 Psyche calls in 7 days; preempted the workhorse twice on 10-29);
   - muse-glimmer (8 successes, 252 errors);
   - qwen36-27b fallback, fable, qwen35-35b, qwen36-35b uncensored, openjev-gfx1100.

   **Recommendation:** point Psyche at the XTX twin `qwen35-9b-ablit-rp` (creative tier, 500). Move
   none of the others unless someone uses them.
2. **D2. Humanizer vs voice on the VII.** Humanizer (130, 11,000 MB estimated) outranks the whole
   retrieval plane and evicts orpheus.

   **Recommendation:** measure its real VRAM, set `vramEstimateMB` to measured plus 10 %, and drop
   it to 105, below voice. If it still does not fit beside the warm set plus ASR, it parks by
   design.
3. **D3.** Retarget S6b to the XTX runtime profile (§7).
4. **D4.** ASR: approve KT-ASR (whisper.cpp on the VII), with Parakeet CPU as the pivot.
5. **D5.** Kokoro and tts-lane off the XTX host: Kokoro RTF test on the 5930k, plus G9.
6. **D6.** Accept that creative availability follows XTX availability. The alternative of creative
   on the 5930k and an always-on XTX puts the must-have lane on the flaky card.

## 12. Evidence pointers

- Phase timeline: `max by (model, phase) (flexinfer_model_phase{model=~"qwen-image-21-gfx1100|qwen38-27b-autoround-workhorse"}) == 1`,
  2026-10-28T03:00Z → 2026-10-30T15:00Z, step 1m.
- Election lines: Loki `{namespace="flexinfer-system"} |~ "Preempting model|Model is active in shared group"` (10-28).
- Studio failure: Loki proxy `qwen-image-21-gfx1100` lines, 10-29 23:52Z → 10-30 00:07Z.
- XTX health: `min by (node) (flexinfer_gpu_healthy)`, 7 days, step 30m.
- Preemptions: `increase(flexinfer_sharedgroup_preemptions_total[7d]) > 0`.
- Runtime in-pod loads:
  `sum by (model) (increase(flexinfer_runtime_model_load_duration_seconds_sum[30d])) / sum by (model) (increase(…_count[30d]))`.
- Harbor:
  - `api/v2.0/projects/flexinfer/repositories/<repo>/artifacts/<digest>?with_tag=true`;
  - diffusers repo listing: `d87f75f8` is rank 5 of about 32, with no `pinned-*` tag.
- Upstream:
  - vLLM PR #28346, #28376; issue #28184; attention backend matrix (docs.vllm.ai design/attention_backends);
  - whisper.cpp releases v1.9.5 (2026-10-06) and `examples/server/server.cpp`;
  - CTranslate2 CHANGELOG v4.7.0 (#1989);
  - faster-whisper README benchmark table;
  - nvidia/parakeet-tdt-0.6b-v2 model card;
  - pyannote-audio 4.0.7 README.
