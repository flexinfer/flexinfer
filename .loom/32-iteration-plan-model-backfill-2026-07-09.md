# ModelBackfill frontier iteration

**Date:** 2026-07-09  
**Goal:** turn otherwise-idle, already-warm inference capacity into useful eval
artifacts without evicting serving models or delaying foreground work.

## Riskiest assumption + kill-test

**Load-bearing assumption:** background requests against a warm model can be
distinguished from foreground demand, and the background Job can be cancelled
quickly enough that foreground service keeps the same model process and normal
latency.

**Kill test:** run a direct background completion loop against the warm
`gemma4-26b-a4b-gptq-5930k` Service, issue one request through
`flexinfer-proxy`, watch `Model.status.lastActiveTime`, and delete the background
Job as soon as foreground demand changes. Pass only when cancellation is within
30 seconds and the model pod UID/restart count do not change.

**Failure mode if wrong:** an "idle" evaluator competes with interactive work,
restarts the warm model, or cannot yield promptly; increased utilization would
come at the cost of the serving SLO.

**Status:** passed 2026-07-09. Foreground demand was detected and the background
Job deleted in 6 seconds; model pod UID was unchanged and restart count stayed
at zero. The test authorizes shared, service-consuming backfill only. Exclusive
GPU Jobs and GPULease-driven model eviction remain out of scope until their own
live kill-test passes.

## Scope

In scope:

- An internal background request class that never creates cold-start demand.
- A one-shot `ModelBackfill` CRD that starts a CPU-side Job from a referenced
  same-namespace CronJob only after its model has been foreground-idle for a
  configured window.
- Immediate cancellation on new foreground demand or gaming-session intent,
  followed by a retry after the next idle window.
- A deployable model-eval gauntlet example and observable controller metrics.

Out of scope:

- Acquiring `GPULease`, unloading a warm model, or scheduling a GPU-requesting
  Job.
- Mutating Flux-owned Model specs or routing aliases.
- Maximizing utilization as an objective independent of useful artifacts and
  foreground latency.

## Slice contracts

### S1 — request priority signal

Files: `internal/proxy/**`, `agents/benchmarker/**`, `pkg/gauntlet/**`,
`pkg/benchmarkconfig/**`, `cmd/flexinfer-bench/**`.

Acceptance:

- `X-FlexInfer-Internal-Workload-Class: background` is stripped before
  forwarding.
- A background request serves only an already-Ready Model and does not touch
  `status.lastActiveTime`; it never cold-starts or queues.
- `FLEXINFER_WORKLOAD_CLASS=background` marks all benchmark and gauntlet HTTP
  requests.
- Unit tests cover foreground compatibility, background Ready serving, and
  background non-Ready rejection.

### S2 — ModelBackfill controller

Files: `controllers/modelbackfill_controller*.go`, manager wiring, RBAC.

Acceptance:

- The controller waits for a Ready model and a continuous idle window.
- It creates one owner-referenced Job from `spec.templateRef`, injects the
  background workload class, and applies `maxRunDuration` as the Job deadline.
- New foreground demand, gaming intent, suspension, spec changes, or deletion
  delete the Job before returning to a waiting/suspended/terminal state.
- The Job template is rejected if it requests GPU resources; this slice shares
  a warm endpoint and must not compete for the card.
- Reconcile tests prove ordering, idempotency, retry-after-preemption, terminal
  handling, and finalizer cleanup.

### S3 — operations and proof surface

Files: controller metrics, chart dashboard/RBAC as needed,
`deploy/tasks/model-backfill/`, docs, and roadmap.

Acceptance:

- The existing model-eval gauntlet is available as a `ModelBackfill` example
  targeting the warm 5930k lane.
- Metrics count starts, completions, failures, and foreground/gaming
  preemptions, plus useful running seconds.
- Runbook explains the foreground-first contract and the exclusive-GPU
  non-goal.

## Integration order

1. Shared CRD/type foundation.
2. S1 and S2 in parallel; S3 in parallel against the frozen API.
3. Generate CRDs/deepcopy, run targeted tests, full `make test`, build, and
   self-review before shipping.
