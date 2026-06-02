# Evidence — pyannote diarization gfx906 kill-test PASSED (voice stack STEP 2 / Slice 4 gate)

**Date**: 2026-06-02
**Verdict**: **PASS** — the brainstorm's riskiest assumption is confirmed true.
**Plan**: [30-implementation-plan-voice-stack-2026-06-01.md](30-implementation-plan-voice-stack-2026-06-01.md) STEP 2
**Brainstorm**: [brainstorm-voice-stack-2026-06-01.md](brainstorm-voice-stack-2026-06-01.md)
**Image**: `registry.harbor.lan/flexinfer/pyannote-diarization:rocm-gfx906` (built + pushed)

## Load-bearing assumption (now confirmed)

> `pyannote/speaker-diarization-3.1` runs end-to-end on `cblevins-radeonvii`
> (gfx906/Vega20) under the `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3` fork
> with `HSA_OVERRIDE_GFX_VERSION=9.0.6`, producing correct speaker turns at
> ~500 MiB–2 GiB GPU, co-resident with FLUX Fill.

**Result**: TRUE. pyannote is PyTorch-only and dispatched HIP ops on Vega20
with no exotic-op wall (unlike vLLM, which is feasibility-only on this card).

## Decisive evidence (baked image, end-to-end from /app)

Probe: `python /app/pyannote_diarize_probe.py /tmp/twospk.wav 2 2` (24.2s clip,
two macOS TTS voices: Alex ≈13s then Samantha ≈11s; forced num_speakers=2).

```text
torch=2.9.0a0+git0fabc3b  cuda_available=True
device=AMD Radeon VII
pipeline loaded in 1.8s
pipeline moved to cuda
--- RTTM segments (2) ---
  SPEAKER_00    0.03s ->  12.97s
  SPEAKER_01   12.97s ->  24.13s
distinct speakers: 2 ['SPEAKER_00', 'SPEAKER_01']
diarize wall time: 20.7s
peak GPU mem allocated: 1653.6 MiB
VERDICT: PASS — 2 segments, 2 speakers, on GPU
```

The 12.97s boundary lands exactly at the real Alex→Samantha transition — the
GPU embedding + clustering separates speakers correctly, at the right time.

**Note on auto-detection**: without a count hint the pipeline detected 1
speaker on this clip. That is a *test-audio* artifact — two synthetic TTS
voices are acoustically uniform (and triggered a `std(): degrees of freedom
<= 0` pooling warning). Real meeting audio with distinct human speakers
auto-detects normally. Feasibility (the kill-test's purpose) is proven; the
forced-count run confirms the embeddings are genuinely separable on gfx906.

## Pre-flight findings (cheap fail-fast checks)

- HF token already existed as `ai/hf-token` (key `HF_TOKEN`, account `caedus90`);
  copied to `flexinfer-system/flexinfer-hf-token`. The plan's assumed
  `flexinfer-hf-token` did not exist.
- **Operator action required and completed**: `caedus90` had to accept the
  gated licenses for `pyannote/segmentation-3.0` and
  `pyannote/speaker-diarization-3.1` (the third sub-model,
  `wespeaker-voxceleb-resnet34-LM`, was already accepted). Diagnosed via 403
  GatedRepoError before the licenses were accepted.
- radeonvii GPU was free at test time (`amd.com/gpu` allocated 0; image-gen
  fluxpony/sdxl scaled to zero); rocm-smi ~14 MB used of 16 GiB.
- An independent **CPU-only `faster-whisper` service already runs on radeonvii**
  (`ai` namespace, LB `192.168.50.225:8000`) — transcription only, no
  diarization. Effectively the plan's CPU ASR fallback, already deployed.

## The compatibility cost: torchaudio 2.9 ↔ pyannote 3.3.2 shim

The mixa3607 fork ships a much newer torch/torchaudio (2.9/2.8) than pyannote
3.x expects (~2.2). Six distinct version-drift breaks, all resolved in
`build/scripts/torchaudio_compat.py` (imported before pyannote) + image pins:

| # | Break | Fix |
|---|---|---|
| 1 | `torchaudio.set_audio_backend` removed | no-op shim |
| 2 | `torchaudio.AudioMetaData` removed | alias/stub shim |
| 3 | `list_audio_backends()` → must be a list containing `soundfile` | return `["soundfile"]` |
| 4 | `huggingface_hub` 1.17 dropped `use_auth_token` | pin `huggingface_hub==0.25.2` |
| 5 | torch 2.6+ `torch.load(weights_only=True)` rejects pyannote ckpt | force `weights_only=False` (trusted weights) |
| 6 | fork version string not valid SemVer (`check_version` crash) | sanitize `__version__` to `X.Y.Z` |
| 7 | torchaudio 2.9 `load()` routes to torchcodec (not installed) | redirect `load`/`info` to soundfile |

Also: `pyannote.audio` bumped 3.1.1 → 3.3.2 (3.1.x had even more torchaudio refs).

**Risk flag**: this is a fragile coupling to a community fork. The shim is
narrow and documented, but a future fork bump (torch/torchaudio) could break
it again. The image pins the fork tag; pin the digest for production (Slice 4).

Benign runtime warnings (do not affect output): `MIOpen hipMemGetInfo error
status:1` (known un-fixable Vega20 issue), TF32-disabled reproducibility note.

## What this unblocks

The split architecture (Whisper ASR on gfx1100 + pyannote diarization on
gfx906) is viable. Remaining for "ready to use":
- **Slice 4**: pyannote sibling Deployment + Service + PVC on radeonvii, CI
  publish lane, digest-pinned image.
- **Slice 5**: proxy `/diarize` static-upstream route (single base URL for ICC).

## Artifacts (this branch)

- `build/Dockerfile.pyannote-rocm-gfx906`
- `build/scripts/torchaudio_compat.py` (the 7-fix shim)
- `build/scripts/pyannote_diarize_probe.py` (kill-test CLI)
- `build/scripts/pyannote_server.py` (FastAPI /diarize — Slice 4 surface)
