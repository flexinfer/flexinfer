Runtime-served Models now report real load progress and get the KV-pool recheck the
dedicated-Deployment path has.

The runtime follows the backend output it already streams, splitting at `\r` as well as `\n`, so
each redraw of vLLM's tqdm shard bar is seen as it happens. Logging is unchanged: one entry per
`\n` line. While the model loads, its health response reports `loadingSubstage`
(`LoadingWeights`, `Compiling`, `ProfilingKV`, `Ready`) and `loadingMessage`. Once vLLM prints
its boot profile, it also reports `kvCacheTokens` and `kvMaxConcurrency`.

The controller maps these onto `status.loadingSubstage` with the Deployment path's values:
`ProfilingKV` reads as `Compiling` and `Ready` as `HealthCheckPending`. It no longer always
records `Initializing`, so the proxy's stalled-load fail-fast can now fire for a wedged
in-pod weight load. `LoadingProgressAt` moves only when the reported shard progress does.

On Ready, the controller records the profile in `status.kvPool` and the `KVPoolShort` condition.
When profiled concurrency is below 0.75 x `maxNumSeqs`, it reloads the instance in the runtime pod
once per load and counts it in `flexinfer_model_kv_pool_recheck_total`. The budget is
`runtime/<pod UID>/<spec hash>` in `kvPool.recheckedReplicaSets`, persisted before the reload. It
outlives the recheck reload itself and resets when the spec or the runtime pod changes. The vLLM
shard and KV-profile parsers moved to `backend` so both paths share them.
