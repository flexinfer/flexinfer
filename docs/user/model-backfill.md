---
title: Opportunistic Model Backfill
description: Run bounded background evaluations against warm models without evicting serving workloads.
---

# Opportunistic Model Backfill

`ModelBackfill` turns otherwise-idle time on an already-warm model into useful
artifacts such as benchmarks or coherence verdicts. Foreground service always
wins: the controller waits for a continuous foreground-idle window, runs a
CPU-side Job copied from a same-namespace CronJob, and cancels that Job when new
foreground demand or gaming intent appears.

ModelBackfill shares the model process. It does not reserve the GPU, unload the
model, change routing, or mutate a Flux-owned `Model`.

## Contract

- The referenced `Model` must already be `Ready`. Backfill cannot cold-start or
  queue a model.
- `templateRef` must name a CronJob in the same namespace.
- The CronJob template must not request or limit `amd.com/gpu`,
  `nvidia.com/gpu`, or another GPU resource.
- The controller injects `FLEXINFER_WORKLOAD_CLASS=background` into the copied
  Job. Background requests serve a Ready model without updating foreground
  demand.
- `idleFor` is continuous. Any foreground request resets the window.
- `maxRunDuration` becomes the Job active deadline. The default is 30 minutes;
  set an explicit bound for production declarations.
- `repeatAfter` is an optional successful-run cooldown. Zero keeps the
  declaration one-shot; nonzero values must be at least one minute. Failed Jobs
  never repeat automatically.
- `env` can override up to 32 literal environment variables in every regular
  Job container. Invalid variable names are blocked. The controller reserves
  `FLEXINFER_WORKLOAD_CLASS` so declarations cannot weaken background safety.
- Foreground demand, a GamingSession targeting the model node, suspension, a
  spec change, or deletion cancels the owned Job.

This is intentionally not a `GPULease` workflow. Use ModelBackfill for work that
consumes an existing endpoint. Exclusive GPU Jobs need a separate, live-tested
lease contract because they evict serving capacity.

## Example

The deployed example waits 30 minutes before copying the existing
`model-eval-gauntlet` template and caps the attempt at 15 minutes:

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: ModelBackfill
metadata:
  name: model-eval-gemma4-5930k
  namespace: flexinfer-system
spec:
  modelRef: gemma4-26b-a4b-gptq-5930k
  templateRef: model-eval-gauntlet
  idleFor: 30m
  maxRunDuration: 15m
  repeatAfter: 24h
  env:
    MODELS: gemma4-26b-a4b-gptq-5930k=vllm
    GAUNTLET_API: chat
```

The CronJob remains the source of its command and result stores. Use `spec.env`
to specialize a shared template's model list, probe, or thresholds. Overrides
replace existing literal or `valueFrom` entries in every regular container;
variables absent from the template are appended.

Recurring templates must be idempotent. Use a durable result store when run
history matters: the benchmark ConfigMap keeps only the latest value, while the
configured Postgres store appends a timestamped row per run.

## Operate

```bash
kubectl -n flexinfer-system get modelbackfill
kubectl -n flexinfer-system describe modelbackfill model-eval-gemma4-5930k
job=$(kubectl -n flexinfer-system get modelbackfill model-eval-gemma4-5930k \
  -o jsonpath='{.status.jobName}')
test -z "$job" || kubectl -n flexinfer-system get job "$job"
```

Expected phases:

| Phase | Meaning |
|---|---|
| `WaitingForIdle` | Model is not Ready or the continuous idle window is incomplete. |
| `Starting` | The controller admitted an attempt and is creating its Job. |
| `Running` | The owned background Job is active. |
| `Cancelling` | Foreground demand, gaming intent, or another cancellation condition won. |
| `Succeeded` | The Job completed. With `repeatAfter`, `status.nextRunTime` reports when it may re-enter idle admission. |
| `Failed` | The Job failed and remains terminal until the spec changes. |
| `Suspended` | `spec.suspend: true`; no attempt may run. |
| `Blocked` | The reference or Job template violates the contract. |

To cancel an attempt and prevent repeats, set `spec.suspend: true`. To rerun a
one-shot or failed declaration, change its spec intentionally or create a new
ModelBackfill name; do not delete its owned Job directly because the controller
owns that child lifecycle.

## Monitor

The controller manager exposes:

| Metric | Meaning |
|---|---|
| `flexinfer_model_backfill_starts_total{backfill,namespace,model}` | Admitted Job attempts, including retries after preemption. |
| `flexinfer_model_backfill_completions_total{backfill,namespace,model,result}` | Terminal attempts by bounded result. |
| `flexinfer_model_backfill_preemptions_total{backfill,namespace,model,reason}` | Attempts cancelled for foreground or gaming priority. |
| `flexinfer_model_backfill_useful_running_seconds_total{backfill,namespace,model}` | Cumulative time spent running background work; waiting time is excluded. |

Useful work and foreground safety are the success criteria. A higher GPU
utilization percentage by itself is not.

## Troubleshooting

| Symptom | Check |
|---|---|
| Stays `WaitingForIdle` | The model must be Ready and receive no foreground requests for the full `idleFor` window. Inspect `status.reason`, `status.message`, and `status.idleSince`. |
| Becomes `Blocked` | Confirm the CronJob exists in the same namespace, none of its containers requests a GPU resource, and every `spec.env` key is a valid non-reserved environment name. |
| Repeatedly preempted | This is correct under foreground traffic. Increase `idleFor`, shorten the template, or schedule the declaration for a quieter lane. |
| Successful but does not repeat | Confirm `repeatAfter` is nonzero and at least one minute. Inspect `status.nextRunTime`; the normal Ready, idle, gaming, lease, and contender gates run again after the cooldown. |
| Coherence fails but chat requests look healthy | Confirm the template uses `GAUNTLET_API=chat`. Reserve `completions` for base models that implement the legacy text-completion prompt shape. |
| Job reaches its deadline | Increase `maxRunDuration` only after confirming the work remains safely preemptible and useful. |
| Need a raw GPU Job | Do not weaken the template guard. ModelBackfill has no exclusive-GPU safety contract. |
