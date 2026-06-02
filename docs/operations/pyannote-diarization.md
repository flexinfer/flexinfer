# Pyannote Speaker Diarization (gfx906)

Speaker diarization ("who spoke when") for the voice stack, complementing
Whisper ASR. Runs as a hand-written FastAPI Deployment on `cblevins-radeonvii`
(Radeon VII / gfx906), reachable through `flexinfer-proxy` at `POST /diarize`.

## Architecture

```
ICC ──> flexinfer-proxy ──/v1/audio/transcriptions──> Whisper Model CR (gfx1100)
                         └─/diarize──────────────────> pyannote Deployment (gfx906)
```

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

See `.loom/pyannote-gfx906-killtest-passed-2026-06-02.md` for the kill-test
evidence and the full compatibility-shim breakdown.
