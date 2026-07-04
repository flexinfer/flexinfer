# Docker Build Node Disk Management

FlexInfer image builds are disk-heavy. ROCm and CUDA runtime images are commonly
8-15 GiB each, and the larger from-source ROCm builds can create much larger
temporary BuildKit snapshots before producing a final image. Keep build-cache
growth explicit so image publishing fails early instead of halfway through a
multi-hour build with `no space left on device`.

## Preflight Check

Run the checked-in preflight before large local or remote builds:

```bash
# Local Docker builder, using the active Docker context.
scripts/check-build-node-disk.sh --local-docker

# In-cluster BuildKit used by GitLab CI.
BUILDKIT_HOST=tcp://buildkitd-central.ci-build.svc.cluster.local:1234 \
BUILDKIT_NAMESPACE=ci-build \
BUILDKIT_POD_SELECTOR='app=buildkitd-central' \
scripts/check-build-node-disk.sh --kubernetes-buildkit --buildctl-du

# Ad hoc path check, useful on a builder host.
FLEXINFER_BUILD_MIN_FREE_GIB=200 scripts/check-build-node-disk.sh --path /var/lib/docker
```

Defaults are intentionally conservative for the shared builder:

| Setting | Default | Meaning |
|---|---:|---|
| `FLEXINFER_BUILD_MIN_FREE_GIB` | `120` | Fail if the checked filesystem has less free space. |
| `FLEXINFER_BUILD_MAX_USED_PCT` | `85` | Fail if the checked filesystem is at or above this utilization. |

Use `200` GiB or more before refreshing multiple ROCm images. Use the
off-CI 7900xtx builder for the multi-arch torch build; that path has a much
larger peak working set than the central BuildKit deployment can safely absorb.

## BuildKit Garbage Collection

Enable BuildKit garbage collection on builder nodes instead of relying only on
manual pruning. The exact file path depends on how buildkitd is deployed
(`/etc/buildkit/buildkitd.toml` for a host daemon; a ConfigMap or mounted config
for the Kubernetes daemon), but the policy shape should stay close to this:

```toml
[worker.oci]
  gc = true
  reservedSpace = "80GB"
  maxUsedSpace = "350GB"
  minFreeSpace = "120GB"

[[worker.oci.gcpolicy]]
  filters = ["type==source.local", "type==exec.cachemount", "type==source.git.checkout"]
  keepDuration = "168h"
  maxUsedSpace = "80GB"

[[worker.oci.gcpolicy]]
  all = true
  keepDuration = "720h"
  reservedSpace = "80GB"
  maxUsedSpace = "350GB"
```

After changing the daemon config, restart buildkitd and confirm it responds:

```bash
buildctl --addr "${BUILDKIT_HOST}" debug info
scripts/check-build-node-disk.sh --kubernetes-buildkit --buildctl-du
```

## Manual Cleanup

Prefer targeted BuildKit cleanup before broad Docker pruning:

```bash
# Remove old BuildKit cache records on the remote daemon.
buildctl --addr "${BUILDKIT_HOST}" prune --filter 'until=168h' --force

# Local Docker builder only. Inspect first, prune second.
docker system df
docker builder prune --filter 'until=168h'
docker system prune --filter 'until=168h'
```

Avoid `docker system prune -a --volumes` on shared builders unless the active
image users and persistent volumes are understood. It can remove images that
long-running manual build scripts expect to reuse.

## Monitoring

Alert on both capacity and trend for builder filesystems:

- warning: builder filesystem above 80% used or below 150 GiB free for 30 minutes
- critical: builder filesystem above 90% used or below 80 GiB free for 10 minutes
- ticket follow-up: if BuildKit cache grows for two consecutive days after GC is
  enabled, lower `maxUsedSpace` or shorten the first policy's `keepDuration`

For Kubernetes-hosted BuildKit, the Prometheus signal usually comes from
node-exporter `node_filesystem_*` metrics on the node that backs the BuildKit
PVC or hostPath. For a single host builder, alert on `/var/lib/docker` and the
filesystem containing BuildKit's root.

## Build-Sizing Notes

| Build class | Expected final size | Recommended free space |
|---|---:|---:|
| Go component `.bin` image | <1 GiB | 40 GiB |
| Single backend image | 8-15 GiB | 120 GiB |
| ROCm runtime refresh | 15-30 GiB | 200 GiB |
| Multi-arch torch from source | 30+ GiB final, much higher temporary use | off-CI 7900xtx builder only |
