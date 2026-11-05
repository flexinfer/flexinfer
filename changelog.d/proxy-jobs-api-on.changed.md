The k3s proxy turns on the async InferenceJob API (`proxy.jobs`: MinIO store in `daemon`, bucket
`flexinfer-jobs`, Secret `flexinfer-jobs-minio`) on the image it already runs (master 72628e0e).
`background: true` and `/v1/jobs*` now run as jobs; imagegen-ui renders videos through them, and
`diarize=true` transcriptions answer `diarization_not_certified` until a diarization lane is
certified. Synchronous requests route as before.
