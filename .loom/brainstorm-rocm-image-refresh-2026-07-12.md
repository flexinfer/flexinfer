# Brainstorm: Predictable ROCm image refreshes

**Date**: 2026-07-12
**Triggered by**: A bridge pod is pulling a very large mutable ROCm image; containerd growth from 148 GB to 167 GB proves forward progress, but the refresh is operationally opaque and tied to workload recovery.
**Constraints noted**: Preserve the current pull; avoid disrupting a pull that is advancing; subsequent restarts should reuse node-local content; fit FlexInfer's Kubernetes/Harbor/Flux platform; production consumers should remain reproducible and rollbackable.

## Problem statement

How should FlexInfer make very large ROCm image refreshes observable, non-disruptive, and reusable without coupling a workload restart to an expensive mutable-tag pull?

## Phase 1 — Framings

### F1 — Accept the cold pull, improve its telemetry

Keep mutable tags and `imagePullPolicy: Always`, but surface node containerd growth, kubelet pull events, elapsed pull time, registry throughput, and a long-pull alert that distinguishes continued byte growth from a true stall. The platform explains the wait instead of changing the release path.

- **Bet**: Refreshes are rare enough that operator confidence, not latency or reproducibility, is the main problem.
- **Risk**: Every mutable-tag restart remains an unbounded deployment event, and different nodes can resolve the same tag to different content.

### F2 — Immutable digest per release

Resolve each successfully built ROCm tag to `repository@sha256:...`, validate it, and update consumers through the existing digest-promotion workflow. Set `IfNotPresent` for digest references so kubelet performs a cheap local content check after the first pull. Mutable tags remain publication aliases, never runtime identities.

- **Bet**: Digest promotion can become the only supported production path for heavyweight runtimes.
- **Risk**: Digest pinning makes rollout deterministic but does not prevent the first consumer from waiting for the full pull.

### F3 — Pre-stage, then activate

Extend the existing `imagePrewarm` DaemonSet pattern into a release gate. A digest-specific prewarm workload targets the intended GPU node, pulls and holds the image, and must report Ready before Flux changes the bridge/runtime consumer to that digest. The old bridge continues serving during staging.

- **Bet**: There is enough temporary disk headroom for old and new image content to coexist through activation and rollback.
- **Risk**: The current sleeping-container prewarmer is only pod-level evidence; without explicit status and timeout semantics, operators can still confuse slow progress with failure.

### F4 — First-class ImageCache reconciliation

Introduce an `ImageCache`/`RuntimeArtifact` CR reconciled per node. Desired state names an immutable digest and node selector; status reports phase (`Resolving`, `Pulling`, `Ready`, `Failed`), start time, last observed byte growth, digest, node, and error. Model/runtime reconciliation refuses activation until the required artifact is Ready on its assigned node.

- **Bet**: Image lifecycle is important enough to justify a durable API and controller state machine shared by runtime, quantizer, and image-generation lanes.
- **Risk**: Kubernetes/containerd do not expose portable per-pull byte progress through the normal API; accurate progress may require privileged CRI/containerd integration in the node agent.

### F5 — Shrink and stratify the artifact

Treat the long pull as an image-composition defect. Keep the existing slim `gfx1100-serving` persona, split optional quantizer/gaming/diffusers tooling into separate images, and enforce compressed-size/layer-change budgets in CI. Stable ROCm/PyTorch base layers change rarely; small FlexInfer overlays change often.

- **Bet**: Most refreshes touch upper layers, so disciplined layer ordering and persona separation materially reduce transferred bytes.
- **Risk**: ROCm base layers remain intrinsically large, and aggressive persona splitting can multiply compatibility matrices and operational images.

### F6 — Registry-to-node distribution lane

Move heavyweight artifact delivery out of pod startup entirely: use a node-local distribution agent or containerd-native peer-to-peer/lazy-pull mechanism to seed content before Kubernetes schedules consumers. Harbor remains authoritative, while nodes fetch in controlled background windows with bandwidth and concurrency limits.

- **Bet**: Fleet size or refresh frequency will grow enough that dedicated distribution amortizes its infrastructure cost.
- **Risk**: For the current small heterogeneous GPU fleet, new snapshotters, P2P services, or privileged agents add more failure modes than they remove.

### F7 — Blue/green node capacity

Never refresh the active bridge node. Stage the new digest on a spare compatible GPU node, validate it there, shift routing, then refresh the old node. Image pull latency becomes a capacity-management concern rather than an availability concern.

- **Bet**: Compatible spare GPU capacity exists when refreshes occur.
- **Risk**: The platform's heterogeneous GPUs and scarce high-end nodes make a true same-architecture spare unreliable or expensive.

## Phase 2 — Cross-Pollinations & Tensions

### Combinations

- **F2 + F3 + F1 — digest-staged release lane**: Digest identity supplies reproducibility, prewarming removes the pull from the bridge restart path, and progress telemetry makes the staging gate diagnosable. Unlike merely doing all three, the key new property is an explicit phase boundary: publication does not authorize activation; cache readiness does.
- **F3 + F5 — persona-aware staging**: Prewarm only the smallest compatible runtime persona for the lane, while retaining the broad unified image as a fallback. This lowers transfer cost without forcing an immediate redesign of every image.
- **F4 seeded by F3 — declarative evolution path**: Start with the existing DaemonSet as the pull actuator and define a stable status contract around it. Promote that contract to a CR/controller only after the workflow proves useful, avoiding premature CRI integration.

### Tensions

- **F1 vs. F4**: The real axis is whether pull progress is merely operator information or controller-owned desired state that can gate activation.
- **F5 vs. F6**: Reduce bytes at the source versus build a better system for moving inherently large bytes. The current fleet favors source reduction; a larger homogeneous fleet could reverse that choice.
- **F3 vs. F7**: Spend disk headroom on one node or spend GPU capacity on two. Current hardware scarcity makes disk the more available redundancy resource.

## Phase 3 — Convergence

### Recommended: F2 + F3 + F1 — digest-staged release lane

Build on capabilities FlexInfer already has: immutable digest promotion, `imagePrewarm` DaemonSets, Harbor, and runtime profiles. CI publishes a commit tag and records its digest; GitOps first updates a node-targeted prewarm entry for that digest, leaving the active bridge untouched. The prewarm pod reaching Ready, the node reporting the digest present, and disk/byte telemetry continuing to advance form the activation gate. Only then does a second GitOps change promote the bridge/runtime consumer to the same digest with `IfNotPresent`. Retain the previous digest and its prewarm holder through a soak window for a cheap rollback, then remove the old holder and let image GC reclaim it. Alert on elapsed time only when both kubelet state and node storage/content observations show no progress for a defined window; a slow pull with increasing bytes is `Pulling`, not `Stalled`.

The first implementation slice should remain chart-and-runbook sized: add digest-only validation for production prewarm profiles, a distinct release annotation/label, documented readiness checks, and a promotion script gate. A later slice can expose node-agent metrics such as `flexinfer_image_pull_state`, `flexinfer_image_pull_elapsed_seconds`, `flexinfer_image_pull_last_progress_timestamp_seconds`, and node image-store free bytes. Do not make exact byte percentage a v1 requirement because compressed registry bytes, unpacked snapshot bytes, and shared layers are different quantities.

### Runner-up: F4 — first-class ImageCache reconciliation

Choose the CR/controller path if image staging must become fully automatic across many nodes or if runtime scheduling needs a hard guarantee that a digest is resident before assignment. It offers the cleanest product model and status surface, but should follow evidence from the DaemonSet-based lane: otherwise the team risks building privileged containerd integration before proving that Kubernetes pod readiness plus coarse node-store progress is insufficient.

### Open question

What minimum free-space reserve must remain on each GPU node while both the current and candidate ROCm digests are retained for rollback?

## Proposed platform flow

1. **Publish**: CI pushes a commit/version tag, resolves the manifest digest, records compressed size and compatibility profile, and never mutates a production consumer.
2. **Stage**: GitOps adds the digest to an `imagePrewarm` profile targeted to the bridge node; preflight rejects staging if the node lacks the configured reserve for candidate plus rollback retention.
3. **Observe**: The platform reports `Pending`, `Pulling`, `Ready`, or `Stalled`; `Stalled` requires no new kubelet progress and no image-store/content growth for the stall window.
4. **Verify**: Confirm the digest is present on the target node and run a lightweight image contract check or disposable canary without stopping the active bridge.
5. **Activate**: Promote the exact digest to the bridge/runtime manifest. With `IfNotPresent`, restart uses local content rather than refreshing a tag.
6. **Soak and reclaim**: Keep the previous digest warm for the rollback window, then remove its holder and allow controlled image GC.

## Riskiest assumption + kill-test

> Every brainstorm-derived plan must surface its riskiest load-bearing
> assumption explicitly. See the `spec-riskiest-assumption` skill.

**Load-bearing assumption**: On the k3s GPU nodes' current containerd configuration, a digest pulled and held by the FlexInfer prewarm DaemonSet is reused by a subsequently created bridge/runtime pod referencing the identical digest, without transferring the heavyweight layers again.

**Kill test**: On one non-critical GPU lane, select a small disposable image digest first (or an already-downloaded ROCm candidate if safe). Record `crictl images`, containerd filesystem usage, and registry/network counters. Deploy a node-pinned prewarm pod with that digest and wait for Ready. Then create a second node-pinned pod using the identical digest and `imagePullPolicy: IfNotPresent`. Pass only if the second pod reaches container creation quickly, kubelet events do not show a fresh multi-layer pull, the image ID matches the digest, and containerd/network growth stays within a small metadata-only tolerance. Delete only the disposable second pod; retain the prewarm holder for observation. This can be completed in under 30 minutes with the small image and exercises the exact kubelet/containerd reuse path.

**Disconfirming search/check**: Inspect the node's kubelet/containerd configuration for image GC thresholds, snapshotter behavior, and any policy that prunes content still referenced by a running prewarm container; specifically look for evidence that `IfNotPresent` with a digest can still re-download layers or that a sleeping holder does not prevent GC.

**Failure mode if wrong**: The proposed readiness gate would claim an image is staged while bridge activation still incurs the full ROCm transfer, so the release lane would not decouple refresh latency from restart.

**Status**: passed 2026-07-12 ([live evidence](kill-test-image-reuse-2026-07-12.md))

> The downstream slice plan was unblocked by the live k3s/containerd reuse test.

## Handoff

- If chosen → next step is: `plan-loom-core`
- Linked spec/plan doc (fill in once it exists): `<.loom/NNN-...md>`
