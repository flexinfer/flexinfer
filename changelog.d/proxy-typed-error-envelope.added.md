Every error the proxy originates about a lane now carries a typed envelope: an OpenAI error whose
`code` is one of `model_cold_starting`, `model_parked`, `model_preempted`, `model_failed`,
`lane_unresponsive`, `context_overflow`, `admission_rejected`, `queue_full` or `stalled_load`, with
a `flexinfer` block `{phase, loading_substage, eta_seconds, retry_after_seconds, reroute_to}`.
Every 503 carries `Retry-After`. Requests that enter the cold-start queue get
`X-FlexInfer-Activation: <phase>/<substage>` on every response, including the eventual 200, and a
caller can send `Prefer: wait=N` to get the envelope after N seconds instead of holding the
connection to the cold-start timeout. The ETA is the lane's cold-start p50 for its serving path
(from Prometheus when `FLEXINFER_PROXY_PROMETHEUS_URL` is set, else the last load the controller now
writes to `status.metrics.loadTimeSeconds`) minus the time the current load has run. The admission
413 code changed from `context_window_exceeded` to `admission_rejected`; messages keep their wording.
See `docs/specs/proxy-api.md`, "Error envelope".
