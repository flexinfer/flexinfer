# Pyannote Speaker Diarization (gfx906)

Speaker diarization ("who spoke when") for the voice stack, complementing
Whisper ASR. Runs as a hand-written FastAPI Deployment on `cblevins-radeonvii`
(Radeon VII / gfx906), reachable through `flexinfer-proxy` at `POST /diarize`.

## Architecture

```
ICC ──> faster-whisper.ai.svc:8000 ──/v1/audio/transcriptions──> faster-whisper (CPU, namespace ai)
    └─> pyannote-diarization.flexinfer-system.svc:8000 ──/diarize──> pyannote Deployment (gfx906)

flexinfer-proxy ──/diarize──> pyannote Deployment (same Service, static route)
                └─/v1/audio/transcriptions──> no servable lane (whisper-large-v3-turbo is parked)
```

As of 2026-10-30, ICC calls both services directly (gitops
`k3s/integration-command-center/deployment.yaml`, `ICC_ASR_WHISPER_URL` and
`ICC_ASR_DIARIZE_URL`). The Whisper Model CR on the 7900 XTX has been parked
since 2026-06-23. The planned always-on ASR lane is whisper.cpp on this same
card. See `.loom/10-research-voice-stack-and-gpu-placement-2026-10-30.md`
§3–§5, which also lists what moving pyannote under the Model API needs.

- **Service**: `pyannote-diarization.flexinfer-system.svc:8000`
- **Proxy route**: `/diarize` → `FLEXINFER_PYANNOTE_UPSTREAM`
  (`deploy/system/values-k3s.yaml` → `proxy.pyannoteUpstream`)
- **Image**: `registry.harbor.lan/flexinfer/pyannote-diarization:rocm-gfx906`
  (digest-pinned in the Deployment), built from `build/Dockerfile.pyannote-rocm-gfx906`
- **GPU sharing**: privileged + `/dev/kfd`+`/dev/dri`, **no** `amd.com/gpu`
  claim — so it co-exists with image-gen instead of locking the card.

## Prerequisites

The pyannote models are **gated** on HuggingFace. The token in
`flexinfer-system/flexinfer-hf-token` (key `HF_TOKEN`) must belong to an account
that has accepted the user conditions for **both**:

- https://hf.co/pyannote/segmentation-3.0
- https://hf.co/pyannote/speaker-diarization-3.1

(The third sub-model, `wespeaker-voxceleb-resnet34-LM`, is not gated.) Without
acceptance the pod logs a `GatedRepoError 403` at startup and never goes Ready.

## Usage

```bash
curl -sS -X POST http://flexinfer-proxy.flexinfer-system.svc/diarize \
  -F file=@meeting.wav
# optional: force a known speaker count
#  -F num_speakers=2
```

Response:

```json
{
  "segments": [
    {"start": 0.03, "end": 12.97, "speaker": "SPEAKER_00"},
    {"start": 12.97, "end": 24.13, "speaker": "SPEAKER_01"}
  ],
  "num_speakers": 2,
  "speakers": ["SPEAKER_00", "SPEAKER_01"]
}
```

A typical meeting-transcription flow: send the recording to
`/v1/audio/transcriptions` (Whisper) and `/diarize` (pyannote), then merge the
word timestamps with the speaker turns client-side.

## Operations

```bash
# status
kubectl get deploy pyannote-diarization -n flexinfer-system
kubectl logs deploy/pyannote-diarization -n flexinfer-system

# health (cuda + device)
kubectl exec deploy/pyannote-diarization -n flexinfer-system -- \
  curl -s localhost:8000/health
# {"status":"ok","cuda":true,"device":"AMD Radeon VII"}
```

Cold start downloads ~50 MiB of gated models into the pod's emptyDir cache
(`HF_HOME=/cache/hf`); readiness allows up to ~5 min for first load. Restarts
re-download (no PVC by design — the working set is tiny).

## Caveats

- **Fork coupling**: the gfx906 PyTorch fork ships torch/torchaudio far newer
  than pyannote 3.3.2 expects, requiring `build/scripts/torchaudio_compat.py`
  (a 7-fix import shim). A future fork bump can break it — re-run the kill-test
  probe (`build/scripts/pyannote_diarize_probe.py`) after any base-image change.
- Benign startup warnings: `MIOpen hipMemGetInfo error status:1` (un-fixable
  Vega20 issue) and a TF32-disabled reproducibility note — neither affects output.
- Auto speaker-count detection can under-count on very short or acoustically
  uniform audio; pass `num_speakers` when the count is known.
- **Outside GPU admission**: the Deployment is not a Model, so the shared-group
  election does not count its VRAM (~1.65 GiB). Its hardware compute queues
  (2) are held back from the Models by `reservedHWQueues: 2` on the gfx906
  GPUProfile (!1198). If pyannote becomes a Model, move those 2 queues from
  the profile reservation to the Model's `hwQueueEstimate` in the same change.
- **First call after idle is slow**: on 2026-10-30 an 11.6 s clip took 15.6 s
  on the first request and 0.40 s on the next. Send one warm-up request before
  you time a batch.

See `.loom/pyannote-gfx906-killtest-passed-2026-06-02.md` for the kill-test
evidence and the full compatibility-shim breakdown.

## Under the Model API (D5-a)

The `pyannote` backend runs the same pipeline as a runtime lane, so the
election counts its VRAM and queues and a `diarize=true` transcription job can
call it (design §6.5 of
`.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md`).
Nothing in this section is live yet; the KT-DIAR window plan
(`.loom/32-iteration-plan-d5a-diarize-job-2026-11-01.md`) turns it on.

| Piece | Where |
|---|---|
| Backend (env, probes, venv launcher) | `backend/pyannote.go` |
| Server: `POST /v1/audio/diarizations`, `GET /health` | `build/server-pyannote.py` (tests: `scripts/test_server_pyannote.py`) |
| Image layer: venv `/opt/pyannote` on the gfx906 runtime digest | `build/Dockerfile.runtime-gfx906-pyannote` (not built by CI) |
| Certificate gate | `controllers/model_backend_certificate.go`, condition `BackendCertificate` |
| Model | `deploy/debug/pyannote-diarization-radeonvii.yaml` (not applied by Flux) |

What changes against the Deployment:

- **Token by file.** `spec.config.hfTokenSecret: flexinfer-hf-token` names the
  Secret; the gfx906 runtime profile mounts it read-only at
  `/var/run/flexinfer/secrets/flexinfer-hf-token` and the server reads
  `PYANNOTE_HF_TOKEN_FILE`. The token never enters a load payload, a command
  line or the runtime pod's environment.
- **Queues move to the Model.** The lane declares `hwQueueEstimate: 3` (one
  HIP process under the profile env: `GPU_MAX_HW_QUEUES=1`, SDMA off). Retire
  the Deployment and set the profile's `reservedHWQueues` to 0 in the same
  change, so the 2 reserved queues are not counted twice.
- **One request at a time.** The server serializes pipeline calls, so the
  lane never holds two pipelines' activations.
- **Response.** `{"object": "audio.diarization", "model", "duration",
  "num_speakers", "speakers", "segments": [{"start", "end", "speaker"}]}`;
  speaker bounds are `num_speakers` or `min_speakers`/`max_speakers`, 1-64.
