# Spec: Training participates in shared-GPU scheduling (GPU lease)

- **Date**: 2026-06-20
- **Author**: claude-code
- **Lineage**: F1×F2 model factory ([f1-implementation-plan-2026-06-19.md](f1-implementation-plan-2026-06-19.md)) — operator chose "wire training into the shared-GPU scheduler first" over preempting a production lane for slice 4's live kill-test.
- **Goal**: A CRD-driven training Job can obtain a gfx1100 GPU on a *shared* card by cleanly parking the incumbent serving lane for the duration of training, then restoring it — instead of grabbing `amd.com/gpu` and contending (Pending forever) or forcing a manual operator preemption.

## Why this is the blocker

Both gfx1100 GPUs are permanently held by serving leaders (the uncensored 35B on the 5930k; gemma4 primary's 2-slot claim on the 7900xtx). Today:
- **Serving** time-shares a card via `controllers/model_shared_gpu.go::chooseSharedGroupLeader` — one leader Deployment runs, the rest are parked (scaled to 0, `Status.Phase=Preempted`, `PreemptedAt` set), keyed on `gpu.shared` + `gpu.priority` + `forcePromotion` + Ready-first/warm-pinned + `SwapCooldown`.
- **Training/quant Jobs** (`pkg/quantization/finetune.go`, `gpu_job.go`) request `amd.com/gpu: 1` directly with `PriorityClassName: PriorityClassTransform` + a node selector. They are **invisible to the election** and contend at the device-plugin layer → `Insufficient amd.com/gpu` → Pending while a serving leader holds the card.

There is no "hold/suspend/maintenance" concept on `Model` to lean on.

## Riskiest assumption + kill-test (this IS Slice 1)

**Load-bearing assumption**: the finetune controller can place a **scheduler-honored hold** on a shared card that (a) makes `chooseSharedGroupLeader` park the incumbent serving leader and **keep it parked** (not re-promote on its continuous reconcile) while the hold is active, (b) frees the `amd.com/gpu` slot so the training Job schedules, and (c) on release, the serving leader re-promotes cleanly to Ready — with no strand, no flap, no two-workloads-on-one-card race.

**Why it's risky**: the shared-GPU reconcile runs continuously and *will* re-promote a parked warm-primary unless the hold is a first-class input to the election. A naive "scale the serving Deployment to 0" fights the controller (it re-promotes next tick). And the device-plugin handoff (serving pod terminates → GPU frees → Job binds) has a window where a fast re-elect or a slow pod-termination could strand or double-book the card. (Recall the warm-pinned-leader and whisper-phantom bugs — the election has sharp edges.)

**Kill test (≤ half a day)**: implement the minimal lease (Slice 2's `GPULease` honored by the election) on a real shared card. Acquire a lease against the 5930k-textgen group → assert the 35B serving leader goes `Preempted` and its pod terminates → launch a trivial GPU Job (`amd-smi`/torch CUDA-available probe requesting `amd.com/gpu:1`) → assert it reaches `Completed` on the freed card → release the lease → assert the 35B re-promotes to Ready and serves. **Observable outcome**: Job `Completed` + serving restored within one `SwapCooldown`, OR a specific failure (re-promote race / strand / double-book) that bounds the design.

**Failure mode if wrong**: the election can't be cleanly held from outside without a deeper refactor → fall back to a coarser mechanism (operator-gated maintenance window, or a dedicated training-only card) and re-scope F1 around that.

**Pair with negative search**: before declaring pass, check the inverse — does releasing the lease while the Job is *still running* (crash/restart of the finetune controller mid-train) leave the card double-booked or the serving lane stranded? The lease must be crash-safe (owner-ref / TTL / reconcile-on-restart).

**Status**: **PASSED 2026-06-20** — live kill-test ran on the `5930k-textgen` group against the `qwen36-35b-mtp-uncensored-5930k` daily-driver. Acquire lease → 35B parked (`Preempted`/`Queued`, `PreemptedBy=gpu-lease/killtest-manual`, pod terminated, card freed) → GPU probe Job bound to `cblevins-5930k` and reached `Completed` (`cuda_available True`, `AMD Radeon RX 7900 XTX`) → release lease → 35B re-promoted `Loading`→`Ready` in 2m46s and served a coherent round-trip. No strand, no flap, no double-book (probe completed before release). Operational note: the running controller had to be `rollout restart`ed to pick up the lease-aware `:master` image (`imagePullPolicy: Always`); after restart the lease was honored within one reconcile of leader-election. Runbook: [runbook-gpu-lease-kill-test-2026-06-20.md](runbook-gpu-lease-kill-test-2026-06-20.md).

### Slice-1 implementation note (what landed 2026-06-20)

The election now honors a GPU lease (`feat/gpu-lease-scheduler`):
- **Carrier**: a labeled ConfigMap `gpu-lease-<group>` (label `ai.flexinfer/gpu-lease=<group>`) holding `{group,node,owner,acquiredAt,expiresAt}`. Chosen over a CRD for slice 1 to avoid codegen/RBAC churn; the election contract is `groupHasActiveLease` so slice 2 can promote it to a `GPULease` CRD without touching the election. ([controllers/gpu_lease.go](controllers/gpu_lease.go))
- **Election**: `chooseSharedGroupLeaders(..., leased bool)` returns **no leader** while leased → every serving member parks via the existing scale-to-0 → `Preempted` path and stays parked (beats even `forcePromotion`). ([controllers/model_shared_gpu.go](controllers/model_shared_gpu.go))
- **handleSharedGPU**: reads the lease each reconcile; on a lease read error it **fails open toward serving** (proceeds as unleased) so a transient API blip cannot strand the lane. Parked members get `PreemptedBy=gpu-lease/<owner>`.
- **Crash-safety**: TTL (`expiresAt`) — the election ignores an expired lease, so a dead acquirer can't strand serving forever. Acquirer also sets an owner-ref (second backstop).
- **Backward-compatible**: with no lease ConfigMap present (every existing group today), behavior is byte-for-byte unchanged.

Not yet landed (deliberately deferred): the finetune-controller acquire/poll/release loop (slice 3) and the live kill-test (run via the runbook with a manual lease ConfigMap + probe Job — the finetune controller is not required to exercise the riskiest assumption).

## Design (minimal, reuses the existing park/restore)

Add a lightweight **`GPULease`** the shared-GPU election treats as the highest-priority transient member of a `gpu.shared` group:

- **Mechanism**: model the lease as a synthetic, forced, top-priority group member (mirrors `forcePromotion` "wins on priority" path at `model_shared_gpu.go:84-100`). While a lease is active for group G on node N, the election returns "no servable leader" for G (all serving members park via the existing scale-to-0 → `Preempted`). The election must *not* re-promote them until the lease clears.
- **Carrier**: a `GPULease` CRD (or a status/annotation on a sentinel) keyed by `{group, node, owner=ModelCache, ttlSeconds, expiresAt}`. Owner-ref to the ModelCache so it's GC'd; TTL so a dead controller can't strand serving forever.
- **Lifecycle (finetune controller)**: before launching a GPU training Job on a shared card → acquire lease → wait for incumbent pod gone + `amd.com/gpu` free → launch Job → poll to terminal → release lease (delete) → election re-promotes serving. Crash-safe: on controller restart, reconcile re-derives the lease from the ModelCache phase; TTL backstops.
- **Authorization/policy**: a `gpu.priority` (or a `trainingPreempts: bool`/min-priority threshold) on `ModelCache.spec.gpu` so training only preempts serving it outranks; default conservative (only preempt when the incumbent is idle / below a threshold, or require explicit opt-in).

## Slices

> Progress: **Slice 1 ✅ (MR !672, live kill-test PASSED 2026-06-20)** · **Slice 2 ✅ (MR !674, `GPULease` CRD)** · Slices 3–5 open.

1. **Slice 1 ✅ — Lease kill-test (riskiest assumption)** — implement the minimal `GPULease` honored by `chooseSharedGroupLeader` + the acquire/poll/release loop in the finetune controller; run the live kill-test above on the 5930k-textgen group with a trivial GPU Job. Gate everything else on this.
2. **Slice 2 ✅ — `GPULease` carrier + election integration** — `GPULease` CRD (`api/v1alpha2/gpulease_types.go`) + deepcopy/manifests/RBAC; `findActiveLease` reads CRs first with legacy-ConfigMap fallback; internal struct renamed `GPULease`→`activeLease`; CR + fallback unit tests. (Election park-and-hold + re-promote already landed in slice 1.)
3. **Slice 3 — Finetune controller integration** — acquire-before-Job / release-after-Job in `reconcileFinetune`; crash-safety (owner-ref + TTL + reconcile-on-restart); `ModelCache.spec.gpu.priority` + preempt policy; metrics (lease held duration, serving downtime).
4. **Slice 4 — Wire into F1 slice 4** — the original CRD-driven train→serve→validate kill-test now schedules without manual preemption; example `ModelCache(Finetune)` + Qwen3-1.7B LoRA-enabled vLLM lane + `LoRAAdapter`.
5. **Slice 5 — Hardening** — SwapCooldown interplay, multi-GPU cards (7900xtx 2-slot: lease one slot vs both), training-vs-training queueing, observability + a runbook.

## Open questions
- Lease as a CRD vs a status field on the ModelCache (CRD is cleaner for cross-controller visibility + GC; status field is lighter). Lean CRD.
- Co-residency vs time-share: on the 7900xtx (2 slots) a 1.7B QLoRA might *co-reside* with gemma4 (lease 1 of 2 slots) — but gemma4 claims both. Start with time-share (park), revisit co-residency in Slice 5.
- Should the proxy/HUD surface "card held by training" so a user doesn't read the parked serving lane as an outage? (Mirror the whisper-phantom lesson.)
