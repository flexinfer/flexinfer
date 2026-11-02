`diarize=true` transcriptions on the Radeon VII (D5-a): the `pyannote-diarization-radeonvii`
lane (pyannote speaker-diarization-3.1 inside the gfx906 runtime pod, scale-to-zero) joins
`deploy/models`, and `whisper-large-v3-turbo-radeonvii` names it with
`flexinfer.ai/diarization-model`, so a `diarize=true` request runs as a job that returns
speaker-attributed words. GPUProfile `gfx906` carries a provisional
`pyannote/speaker-diarization` certificate for the KT-DIAR window; the verdict replaces it or
reverts the lane. The standalone pyannote Deployment keeps serving `/diarize`.
