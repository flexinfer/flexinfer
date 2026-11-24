The k3s values set `controller.runtime.localCache.onDelete: delete`: deleting a Model with
`cache.strategy: Local` now removes `<hostPath>/<namespace>/<name>` from its node through the
one-shot `<name>-cache-cleanup` Job (!1329) instead of leaving the staged weights behind. A canary
that will be recreated under the same name keeps them with the annotation
`flexinfer.ai/local-cache-on-delete: retain`. Renders one env on the controller Deployment,
`DEFAULT_LOCAL_CACHE_ON_DELETE=delete`, which the controller has read since the 6500e342 pin
(2026-11-18), so the policy applies from the merge. Rollback = `retain`.
