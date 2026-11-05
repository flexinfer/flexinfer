The k3s proxy turns on the async InferenceJob API (`proxy.jobs`: MinIO store in `daemon`, bucket
`flexinfer-jobs`, Secret `flexinfer-jobs-minio`) and, in the same restart, moves from the 72628e0e
build (`sha256:bdd4bd25…`) to master 51e6bd81 (`sha256:d0439aac…`), so its direct-load path renders
`spec.config.vllmVersion` like the controller. `background: true` and `/v1/jobs*` now run as jobs;
imagegen-ui renders videos through them, and `diarize=true` transcriptions answer
`diarization_not_certified` until a diarization lane is certified. Synchronous requests route as before.
