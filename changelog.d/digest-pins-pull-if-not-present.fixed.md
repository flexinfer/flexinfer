Dedicated Model pods whose image is pinned by digest (`@sha256:`) now render
`imagePullPolicy: IfNotPresent` instead of `Always`. A digest is immutable, so a node that
already holds the image can start the pod without the registry; with `Always` the kubelet
re-resolved the digest on every start and a resident image was useless the moment Harbor
retention had dropped it (`qwen38-27b-xtx-warm-canary`, 2026-10-20: 24 h of
ImagePullBackOff beside an 11 GB local copy of the same digest). Tag references keep `Always`.
Rolling the controller re-renders existing Deployments, so each Ready digest-pinned lane
restarts once (GPU lanes use maxSurge 0: one cold start each).
