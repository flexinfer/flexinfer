Speech-to-text joins the Model API: `whisper-large-v3-turbo-radeonvii` serves OpenAI
`/v1/audio/transcriptions` from whisper.cpp on the Radeon VII, warm-pinned beside the retrieval
plane (labels `asr`, `transcription`, `whisper`; LiteLLM `audio_transcription`). It runs at 34.7x
real time on a 60-minute meeting in 1.3 GB and 3 compute queues, confined to 40 of the 60 CUs so
gte and bge keep their baseline latency. The gfx906 GPUProfile bundles the `whispercpp` backend
and reserves 3 compute queues for pyannote, up from 2, after pyannote was measured at 3.
