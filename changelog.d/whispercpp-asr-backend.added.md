New `whispercpp` backend: whisper.cpp's `whisper-server` runs inside flexinfer-runtime as an
OpenAI `/v1/audio/transcriptions` lane. The gfx906 runtime image now builds whisper.cpp v1.9.5
(ggml-hip, statically linked so it never loads llama.cpp's `libggml`) at
`/opt/whispercpp/bin/whisper-server`. The runtime's model ingress adds the OpenAI top-level
`words` list to `verbose_json` responses, because whisper-server lists sub-word tokens under each
segment; whispercpp Models always get that ingress, with or without request drain. HF downloads
for whispercpp Models are filtered to `config.modelFile`, as for llamacpp. Kill-test KT-ASR passed
on the Radeon VII with whisper.cpp confined to 40 of 60 compute units: 34.7x real time on a
60-minute meeting, 1.3 GB, 3 queues, retrieval latency unchanged
(`docs/operations/whispercpp-asr.md`). No Model uses the backend yet, so nothing live changes
until the Model and the runtime repin land.
