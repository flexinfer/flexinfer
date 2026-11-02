New `InferenceJob` CRD (`ai.flexinfer/v1alpha2`, short name `ijob`): one asynchronous inference
request with its model, path, request body (inline, a ConfigMap/Secret ref, or stored inputs), a
Standard Webhooks endpoint, a TTL, a QoS class and an idempotency key; its status carries the
OpenAI Responses background phase (`queued`, `in_progress`, `completed`, `failed`, `cancelled`,
`incomplete`), progress, lifecycle times, the stored output, the error, the runner and the lane.
The chart installs the CRD and grants the proxy and the controller access. Nothing creates jobs
yet; the proxy's job API and runner follow. See `docs/specs/crds.md`.
