# Image prewarm reuse kill test

**Date**: 2026-07-12
**Cluster runtime**: k3s v1.33.4+k3s1, containerd 2.0.5-k3s2
**Node**: `k3s-w-12` (non-GPU worker)
**Digest**: `busybox:1.37.0@sha256:9532d8c39891ca2ecde4d30d7710e01fb739c87a8b9299685c63704296b16028`
**Result**: PASS

## Procedure and evidence

1. A node-pinned prewarm pod using the digest and `IfNotPresent` pulled 2,222,111 bytes in 2.3 seconds and became Ready.
2. While the prewarm holder remained Running, a second node-pinned consumer pod used the identical digest and `IfNotPresent`.
3. The second pod became Ready and kubelet emitted:

   ```text
   Container image "busybox:1.37.0@sha256:9532d8c39891ca2ecde4d30d7710e01fb739c87a8b9299685c63704296b16028" already present on machine
   ```

4. Both pods reported the same image ID:

   ```text
   docker.io/library/busybox@sha256:9532d8c39891ca2ecde4d30d7710e01fb739c87a8b9299685c63704296b16028
   ```

5. Both disposable pods were deleted after evidence collection.

## Disconfirming check

The test specifically looked for a second `Pulling`/`Successfully pulled` event despite digest identity and `IfNotPresent`. None occurred; kubelet reported local presence instead. The production design still retains a running prewarm holder because image-GC behavior after all containers release an image is a separate lifecycle concern.

## Conclusion

On the platform's current k3s/containerd versions, a digest held by a prewarm pod is reused by a later consumer on the same node without another layer transfer. This unblocks the digest-staged release lane.
