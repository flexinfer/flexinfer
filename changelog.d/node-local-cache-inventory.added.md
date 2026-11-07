The node agent can inventory the `strategy: Local` model caches on its node: with
`--local-cache-root` (Helm `agent.localCacheInventory.enabled`, default off; `hostPath` defaults to
`/var/lib/flexinfer/models`) it sizes every `<base>/<namespace>/<name>` directory each `--interval`
(du-style allocated bytes, re-walked when the directory's mtime moves or after 10 min), joins it
with the Models of every namespace and exports `flexinfer_node_local_cache_bytes{node,namespace,model,orphan}`
and `flexinfer_node_local_cache_orphans{node}`. Anything directly under the base that is not a
Model namespace (a legacy flat cache, another tool's store) is reported with an empty namespace
as an orphan. Enabling it mounts the base read-only into the agent, grants cluster-wide `get`/`list`
on `models.ai.flexinfer` and adds the `FlexInferNodeLocalCacheOrphanBytesHigh` warning (orphans
above 50 GB on a node for 1 h, `alerting.thresholds.localCacheOrphanBytes`/`localCacheOrphanFor`).
It only reports, never deletes. On 2026-11-06 about 190 GB of orphaned caches on the 7900 XTX root
fed a 17-hour DiskPressure eviction that nothing attributed to them. Disabled, the chart renders
byte-identically (checked against `deploy/system/values-k3s.yaml`), so this is live-neutral.
