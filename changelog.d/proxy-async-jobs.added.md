The proxy runs async jobs (off by default, `FLEXINFER_JOBS_ENABLED`, chart `proxy.jobs`):
`background: true`, a `background` form field or `Prefer: respond-async` on the chat, image, audio
and video paths, or `POST /v1/jobs`, creates an `InferenceJob` and answers at once with an OpenAI
Responses-style job object; `GET /v1/jobs/{id}`, `GET /v1/jobs/{id}/content`, `POST
/v1/jobs/{id}/cancel` and `GET /v1/jobs` follow it. A runner in the proxy claims jobs by
`resourceVersion`, runs each through the proxy's own handler (cold start and demand, first-byte
budget, breaker, reroutes), one at a time per diffusers card, retries while the lane is cold or
busy, stores the output in MinIO (SigV4) or on a PVC, deletes it when the job is deleted, and
delivers Standard Webhooks with retries. The chart also gains `proxy.prometheusURL` for the error
envelope's ETA. See `docs/specs/proxy-api.md`, "Jobs".
