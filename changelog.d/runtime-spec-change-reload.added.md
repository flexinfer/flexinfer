Runtime-served Models now pick up spec edits without anyone unloading them by hand. The runtime
load payload carries the Model's `metadata.generation` and a `specHash` of the rendered launch
(backend, model path, config, env). The runtime records both per loaded model and reports them in
`/api/v1/models/{name}/health` and `/api/v1/status` (which now also lists `activeModels`). When a
Ready or Failed instance was started from an older generation and a different launch hash, the
controller sends a load with `reload: true`. The runtime restarts that model under `opMu`. With
request drain on, it answers 409 until accepted requests finish, and the controller keeps retrying
the reload rather than unloading the lane. The Model is published as `Loading`
(`RuntimeReloading`, substage `Initializing`, Endpoints removed), so the proxy queues, and a
`RuntimeReload` event is recorded. Only the Active member of a shared group reloads. Spec edits
that render the same launch (priority, serverless knobs) do not reload. A hash drift without a spec
edit, such as a controller upgrade or the proxy's direct load, waits for the next edit. Runtimes
and loads that predate tracking report no hash and are adopted as-is, so rolling this controller
reloads nothing. A reload the runtime rejects outright leaves the old instance serving and records
a throttled `RuntimeReloadFailed` warning.
