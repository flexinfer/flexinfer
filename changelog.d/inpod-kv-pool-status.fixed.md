`status.kvPool` now describes the in-pod load for a runtime-served vLLM Model instead of keeping
the last Deployment pod's observation.

The runtime's per-model health (`GET /api/v1/models/{name}/health`) reported `kvCacheTokens` and
`kvMaxConcurrency` only when it had read both vLLM lines, while `GET /api/v1/models` reported
each on its own. When the concurrency line was missing, health reported neither, and the
controller left `status.kvPool` on the pod it last saw: on 2026-11-19 the XTX warm lane kept the
name, UID and `observedAt` of a Deployment pod that no longer existed. Health now reports each
field once vLLM printed it.

On each Ready pass of a runtime-served lane, the controller replaces `status.kvPool` when it names
another pod, when the profile changed, or when it was observed before the running instance
started (the health `loadedAt`). The pool then holds the runtime's `kvCacheTokens`, the
`maxConcurrency` vLLM printed or, when the runtime has only the token count, tokens divided by
`maxModelLen` (the reconfigured value when the KV-cache reconfigure path set one), `observedAt`,
and the runtime pod's name and UID. A runtime with no profile still replaces another pod's entry,
with tokens and concurrency left empty. The recheck budget in `recheckedReplicaSets` carries over.
A derived concurrency sets the `KVPoolShort` condition but never triggers the in-pod recheck
reload; only the value vLLM printed does, as on the Deployment path. Rendered Deployments and
runtime load payloads are unchanged.
