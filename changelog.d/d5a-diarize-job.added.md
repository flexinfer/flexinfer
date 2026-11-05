`diarize=true` on `/v1/audio/transcriptions` (D5-a): a transcription that asks for speakers (the
`diarize` field, or `speaker` in `timestamp_granularities[]`) runs as an async job. The runner
transcribes on the ASR lane, diarizes on the pyannote lane the ASR Model names in
`flexinfer.ai/diarization-model`, and stores the ASR lane's verbose_json with `words[].speaker`,
`speakers`, `utterances` and the raw `diarization` turns; a retry resumes at the step that failed.
Without a certified diarization lane (condition `BackendCertificate=True`) the request answers 501
`diarization_not_certified`, and the ASR lane's `GET /v1/models/{name}` descriptor shows the same
code and reason under `flexinfer.diarization`. The whisper.cpp word merge moved to
`pkg/transcript`, its word type gaining an optional `speaker`; a transcription without `diarize` is
forwarded byte for byte as before and the runtime ingress output is unchanged.
