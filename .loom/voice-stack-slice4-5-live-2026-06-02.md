# Evidence — Voice stack Slice 4+5 LIVE (pyannote Deployment + proxy /diarize)

**Date**: 2026-06-02
**MR**: services/flexinfer!543 (merged, commit `227b3e4b`)
**Verdict**: **DONE + LIVE-VALIDATED**. The meeting-transcription voice stack
(ASR + diarization) is callable through one base URL.

## What shipped

- **Slice 4** — `deploy/system/pyannote-diarization/{deployment,service,kustomization}.yaml`:
  digest-pinned (`@sha256:3ea89869…`) FastAPI service on `cblevins-radeonvii`.
  GPU access mirrors the gfx906 runtime: `privileged` + `/dev/kfd` + `/dev/dri`,
  **no `amd.com/gpu` device-plugin claim**, so it shares the Radeon VII
  cooperatively instead of locking it and starving the serverless image-gen Models.
- **Slice 5** — `internal/proxy/pyannote.go` `handleDiarize` + Config/Proxy wiring;
  Helm template env + `values-k3s.yaml` `proxy.pyannoteUpstream`. `/diarize`
  reverse-proxies to `FLEXINFER_PYANNOTE_UPSTREAM`.
- Operator doc `docs/operations/pyannote-diarization.md`.

## CI + deploy

- MR pipeline 12539 green (vet/fmt/lint/helm + unit/proxy/integration/gpugroup tests).
- Master pipeline 12541: `build_binaries` + `publish` (flexinfer-proxy:master) + `publish_helm_chart` succeeded.
- Flux: `flexinfer-system` + `flexinfer-models` kustomizations Ready at `227b3e4b`;
  HelmRelease `flexinfer` upgrade succeeded (`v1330`, chart `1.0.2+227b3e4b`).
- Proxy pod recreated to pull the freshly-published `:master` binary (Flux does
  not auto-detect a same-tag digest change).

## Live validation

pyannote Deployment Ready 1/1; health:
```json
{"status":"ok","cuda":true,"device":"AMD Radeon VII"}
```

End-to-end `/diarize` **through the proxy** (single base URL):
```text
POST http://flexinfer-proxy.flexinfer-system.svc/diarize  (-F file=@clip.wav -F num_speakers=2)
→ HTTP 200, 0.32s
{"segments":[{"start":0.031,"end":10.966,"speaker":"SPEAKER_00"},
             {"start":10.966,"end":11.472,"speaker":"SPEAKER_01"}],
 "num_speakers":2,"speakers":["SPEAKER_00","SPEAKER_01"]}
```
`GET /v1/models` on the same base URL still lists `whisper-large-v3-turbo`, so ASR
+ diarization share one endpoint exactly as the plan intended.

## Voice stack — final state

| Capability | Endpoint | Node | Status |
|---|---|---|---|
| ASR (transcription) | `POST /v1/audio/transcriptions` | gfx1100 (7900xtx) | ✅ live (demand-driven, idle-to-zero) |
| Diarization (who-spoke-when) | `POST /diarize` | gfx906 (radeonvii) | ✅ live (always-on, cooperative GPU) |

A meeting-transcription client sends the recording to both and merges word
timestamps with speaker turns. ICC integration lives in the ICC repo.

## Remaining (optional)

- Slice 6: concurrent-load + co-tenancy soak (pyannote ↔ image-gen on radeonvii;
  Whisper swap ↔ 26B on 7900xtx).
- TTS / full conversational loop (brainstorm "LATER") — needs its own kill-test.
- Fork-coupling watch: re-run `build/scripts/pyannote_diarize_probe.py` after any
  `mixa3607/pytorch-gfx906` base-image bump (the torchaudio_compat shim is tied to
  torch/torchaudio 2.9/2.8).
