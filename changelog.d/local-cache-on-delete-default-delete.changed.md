The k3s values set `controller.runtime.localCache.onDelete: delete`: deleting a Model with
`cache.strategy: Local` now removes `<hostPath>/<namespace>/<name>` from its node through the
one-shot `<name>-cache-cleanup` Job (!1329) instead of leaving the staged weights behind. A canary
that will be recreated under the same name keeps them with the annotation
`flexinfer.ai/local-cache-on-delete: retain`. Renders one env on the controller Deployment,
`DEFAULT_LOCAL_CACHE_ON_DELETE=delete`; inert on the live 51e6bd81 controller pin until the next
controller roll. Rollback = `retain`.
