The Radeon VII (gfx906) runtime moves to `rocm-gfx906-pyannote-6fc6fcbd9361`: the same image plus a
pyannote venv at `/opt/pyannote`, with the `flexinfer-hf-token` Secret mounted read-only for the
`pyannote` backend. GPUProfile `gfx906` lists `pyannote` in `bundledBackends`, but no lane is served
until the KT-DIAR window adds its certificate. Every existing lane runs the same binaries.
