A deleted Model's `cache.strategy: Local` directory (`<hostPath>/<namespace>/<name>` on its GPU
node) now follows an on-delete policy: the Model annotation `flexinfer.ai/local-cache-on-delete`
(`retain` | `delete`), else the controller default `DEFAULT_LOCAL_CACHE_ON_DELETE` (Helm
`controller.runtime.localCache.onDelete`, default `retain`, today's behavior). `delete` runs a
one-shot `<name>-cache-cleanup` Job on the node that removes exactly that directory and logs the
KiB freed; a Model recreated under the same name cancels a cleanup that has not run yet. Nothing
reaped these directories before: eight 18 GB copies of one canary artifact tripped the 7900 XTX
root into a 17-hour DiskPressure eviction on 2026-11-06. Live-neutral until the default is flipped.
