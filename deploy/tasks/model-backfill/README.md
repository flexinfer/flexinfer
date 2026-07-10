# ModelBackfill example

The examples run the existing `model-eval-gauntlet` CronJob template once their
warm model lanes have been foreground-idle for 30 continuous minutes. The
copied Jobs have bounded active deadlines and are cancelled if new foreground
demand or gaming intent appears.

The CronJob remains the source of the benchmark command and persistence
settings. A declaration can set literal `spec.env` overrides for its copied Job
containers, allowing each model to select its own target, probe, and thresholds
without duplicating the CronJob. `FLEXINFER_WORKLOAD_CLASS` is reserved and
always injected as `background` by the controller.

`model-eval-qwen3-radeonvii` uses a literal `READY` probe because the deployed
Qwen chat template does not satisfy the generic arithmetic probe. Its live
profile kill-test passed 3/3 while leaving model `lastActiveTime`, serving pod
UID, readiness, and restart count unchanged.

This example deliberately does not request a GPU. The Job calls an already-warm
endpoint using the internal background workload class injected by the
controller. Exclusive GPU work, model eviction, and `GPULease` acquisition are
outside this contract.

Inspect it with:

```bash
kubectl -n flexinfer-system get modelbackfill model-eval-gemma4-5930k
kubectl -n flexinfer-system get modelbackfill model-eval-qwen3-radeonvii
kubectl -n flexinfer-system describe modelbackfill model-eval-gemma4-5930k
job=$(kubectl -n flexinfer-system get modelbackfill model-eval-gemma4-5930k \
  -o jsonpath='{.status.jobName}')
test -z "$job" || kubectl -n flexinfer-system get job "$job"
```

To stop or defer an example, set `spec.suspend: true` in its declaration.
Suspension cancels an active attempt and prevents a new one from starting.
