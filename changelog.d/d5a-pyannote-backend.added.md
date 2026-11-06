New `pyannote` backend (D5-a): pyannote.audio speaker diarization as a runtime lane at
`/v1/audio/diarizations`, served by the bundled `build/server-pyannote.py` from a venv that
`build/Dockerfile.runtime-gfx906-pyannote` layers onto the gfx906 runtime (not built by CI). The
Model names the HF pipeline in `spec.source` and the token Secret in `spec.config.hfTokenSecret`;
the runtime pod mounts the Secret and the server reads it from a file. The backend is gated by a
new GPUProfile certificate, `pyannote/speaker-diarization`, bound to the profile's digest-pinned
`spec.runtime.image`: without it the controller fails the Model with condition
`BackendCertificate=False` (reason `BackendNotCertified`). No profile carries the certificate, so
nothing live changes; the KT-DIAR window builds the layer, certifies gfx906 and applies
`deploy/debug/pyannote-diarization-radeonvii.yaml`.
