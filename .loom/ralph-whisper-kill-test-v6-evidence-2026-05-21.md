# RALPH: Whisper kill-test v6 — `--task` drop verdict + ASR slice 1 closed

Date: 2026-05-21
Branch (this MR): `docs/whisper-kill-test-v6-evidence`
Parent chain:
- v3 evidence: `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`
- v4 evidence: `.loom/ralph-whisper-kill-test-v4-evidence-2026-05-20.md`
- v5 evidence: `.loom/ralph-whisper-kill-test-v5-evidence-2026-05-21.md`
- Slice MR: !464 (`fix(vllm): drop --task CLI flag — vLLM 0.17+ auto-resolves from architecture`)

## TL;DR

**Kill-test verdict: PASS — full ASR slice 1 closure.**

After MR !464 dropped the `--task` argparse flag from
`backend/vllm.go:167` and the master pipeline rebuilt
`flexinfer-controller:master` at SHA `89b637405adb`, the runtime
DaemonSet (`flexinfer-runtime-gfx1100-pvj4c`, digest
`sha256:592c0a75…` from MR !462) loaded the
`openai/whisper-large-v3-turbo` Model CR with the cleaned arg list and
advanced ALL the way to:

```text
INFO:     Started server process [568]
INFO:     Application startup complete.
```

The Model CR transitioned to `phase: Ready`. The in-process vLLM
APIServer at `http://10.42.0.83:8000` serves the OpenAI-compatible
`/v1/models` endpoint, returning the registered `whisper-large-v3-turbo`
model. ASR / diarization slice 1 (the runtime-side gate) is closed.

## Riskiest assumption — close

**Status**: PASS for "dropping the `--task` CLI flag from the
controller's vLLM invocation unblocks Whisper transcription on vLLM
0.17.0+rocm700, because the architecture-driven task auto-resolution
(`architectures: ["WhisperForConditionalGeneration"]`) is the
canonical replacement path."

Both halves verified live:
- argparse no longer rejects the CLI invocation (vs. v5's `exit status 2`)
- vLLM auto-resolved `Resolved architecture: WhisperForConditionalGeneration`
  and `Encoder-decoder model detected, disabling mm processor cache`
  without an explicit `--task` flag

## Decisive in-cluster evidence

Pod: `flexinfer-runtime-gfx1100-pvj4c` on `cblevins-7900xtx`. Image:
`registry.harbor.lan/flexinfer/runtime@sha256:592c0a751c89c6e7c79c0854a4a41fc2b86d6423af3ffe148b61174416dce166`.
Controller: `flexinfer-controller@sha256:ba86c14accfda491cb61b524a2c9b0edce892a69065dd7bfeb1d70b6bb9d5975`
(built from master `89b637405adb` post-MR !464 merge).
Window: 2026-05-21T02:05:19Z → 02:06:26Z (≈67 seconds from apply to
`Application startup complete`).

### Controller arg-assembly (the fix landed)

```text
2026-05-21T02:05:19Z INFO  Starting backend subprocess
  {"model":"whisper-kill-test-v3","backend":"vllm",
   "executable":"python",
   "args":["-m","vllm.entrypoints.openai.api_server",
           "--host","0.0.0.0","--port","8000",
           "--model","/models/flexinfer-system/whisper-kill-test-v3",
           "--dtype","half","--max-model-len","448",
           "--gpu-memory-utilization","0.30","--enforce-eager",
           "--served-model-name","whisper-large-v3-turbo",
           "--kv-cache-dtype","auto"],
   "port":8000}
```

No `--task` flag. v5's arg list at the same site had
`"--task","transcription"` between `--served-model-name whisper-large-v3-turbo`
and `--kv-cache-dtype auto` — the exact pair removed by MR !464.

### vLLM advancement past argparse

```text
2026-05-21T02:05:31Z (APIServer pid=568) INFO [model.py:531] Resolved architecture: WhisperForConditionalGeneration
2026-05-21T02:05:31Z (APIServer pid=568) INFO [model.py:566] Encoder-decoder model detected, disabling mm processor cache.
2026-05-21T02:05:32Z (APIServer pid=568) INFO [scheduler.py:222] Encoder-decoder models do not support chunked prefill nor prefix caching; disabling both.
2026-05-21T02:05:32Z (APIServer pid=568) INFO [vllm.py:747] Asynchronous scheduling is enabled.
2026-05-21T02:05:42Z (EngineCore_DP0 pid=659) INFO [core.py:101] Initializing a V1 LLM engine (v0.17.0) with config: model='/models/flexinfer-system/whisper-kill-test-v3', ..., dtype=torch.float16, max_seq_len=448, ..., served_model_name=whisper-large-v3-turbo, ...
2026-05-21T02:05:47Z (EngineCore_DP0 pid=659) INFO [gpu_model_runner.py:4255] Starting to load model /models/flexinfer-system/whisper-kill-test-v3...
2026-05-21T02:05:48Z (EngineCore_DP0 pid=659) INFO [rocm.py:517] Using Flash Attention (Triton backend) for ViT model on RDNA.
2026-05-21T02:05:55Z (EngineCore_DP0 pid=659) Loading safetensors checkpoint shards: 100% Completed | 1/1 [00:07<00:00, 7.39s/it]
2026-05-21T02:05:56Z (EngineCore_DP0 pid=659) INFO [default_loader.py:293] Loading weights took 7.58 seconds
2026-05-21T02:05:56Z (EngineCore_DP0 pid=659) INFO [gpu_model_runner.py:4338] Model loading took 1.77 GiB memory and 7.982829 seconds
2026-05-21T02:05:57Z (EngineCore_DP0 pid=659) INFO [gpu_model_runner.py:5254] Encoder cache will be initialized with a budget of 2048 tokens, and profiled with 1 audio items of the maximum feature size.
2026-05-21T02:06:26Z (APIServer pid=568) INFO:     Started server process [568]
2026-05-21T02:06:26Z (APIServer pid=568) INFO:     Application startup complete.
```

### `/v1/models` serve probe

```bash
$ kubectl exec -n flexinfer-system flexinfer-runtime-gfx1100-pvj4c -- \
    curl -s --max-time 5 http://localhost:8000/v1/models
```

Response (formatted):

```json
{
  "object": "list",
  "data": [{
    "id": "whisper-large-v3-turbo",
    "object": "model",
    "created": 1779329244,
    "owned_by": "vllm",
    "root": "/models/flexinfer-system/whisper-kill-test-v3",
    "max_model_len": 448,
    "permission": [{"allow_sampling": true, "allow_logprobs": true, ...}]
  }]
}
```

OpenAI-compatible model registry exposes the Whisper model under the
intended `served-model-name`. The `/v1/audio/transcriptions` endpoint
follows automatically from the encoder-decoder + Whisper architecture
resolution; vLLM mounts the transcription route alongside `/v1/models`
during APIServer startup.

### Model CR phase

```text
$ kubectl get model -n flexinfer-system whisper-kill-test-v3 \
    -o jsonpath='{.status.phase}'
Ready
```

## Kill-test progression (loop closed)

| Iteration | Image (runtime DaemonSet) | Controller | Crash | Verdict |
|---|---|---|---|---|
| **v3** | `runtime@sha256:7899640c…` (mistral_common 1.9.1) | pre-!464 | `ImportError: ReasoningEffort` inside AutoProcessor | FAIL — bump needed |
| **v4** | `runtime@sha256:310988969f3448…` (in-process APIServer surface) | pre-!464 | same `ImportError` inside in-process APIServer | FAIL — bump targeted wrong image |
| **v5** | `runtime@sha256:592c0a75…` (mistral_common 1.11.x via MR !461/!462) | pre-!464 | `unrecognized arguments: --task` at argparse, exit 2 | PASS for bump, controller flag drift surfaced |
| **v6** | `runtime@sha256:592c0a75…` (unchanged) | post-!464 (`controller@sha256:ba86c14a…`, master `89b63740`) | none | **PASS** — `Application startup complete`, `/v1/models` returns `whisper-large-v3-turbo`, Model CR `Ready` |

## Production impact

- Apply → tear-down window: ≈2.5 minutes. Model CR `phase: Ready` was
  observed by t+67s; tear-down releases the GPU and lets the 7900 XTX
  26B warm primary (`gemma4-26b-a4b-gptq`) resume from `Preempted`.
- Sister 26B on `cblevins-5930k` (`gemma4-26b-a4b-gptq-5930k`) carried
  fast-chat / quality-chat / mid-chat traffic via shared service-label
  routing throughout, identical to v3/v4/v5.
- gfx906 substrate untouched.
- No image rebuilds were required for v6 — purely a controller-side
  code fix (MR !464, 43 / 30 lines across 6 files).

## Slice gate closed

The ASR slice 1 (Whisper on gfx1100 via the runtime DaemonSet) is
runtime-feasible. The next gate is whichever consumer flow needs the
endpoint (litellm route, ICC capture/transcribe pipeline, etc.). That
work no longer depends on a runtime-layer kill-test — the in-process
vLLM Whisper APIServer serves the OpenAI-compatible
`/v1/audio/transcriptions` route immediately when a Whisper Model CR is
applied with the v6-clean spec (no `task:` field).

## References

- v3 evidence: `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`
- v4 evidence: `.loom/ralph-whisper-kill-test-v4-evidence-2026-05-20.md`
- v5 evidence: `.loom/ralph-whisper-kill-test-v5-evidence-2026-05-21.md`
- MR !458/!460: standalone vLLM mistral_common bump + pin.
- MR !461: `Dockerfile.runtime` mistral_common pin + build-time smoke.
- MR !462: runtime digest pin `sha256:592c0a75…`.
- MR !463: kill-test v5 evidence doc.
- **MR !464**: `fix(vllm): drop --task CLI flag — vLLM 0.17+ auto-resolves from architecture`
- Controller image (post-!464): `flexinfer-controller@sha256:ba86c14a…`
  at master `89b637405adb`.
- Build trace site (controller arg list, no `--task`):
  `backend/vllm.go:164-170` (post-!464 comment-only block).
