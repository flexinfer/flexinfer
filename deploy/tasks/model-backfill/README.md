# ModelBackfill example

`model-eval-gemma4-5930k` runs the existing `model-eval-gauntlet` CronJob
template once the warm `gemma4-26b-a4b-gptq-5930k` lane has been foreground-idle
for 30 continuous minutes. The copied Job has a 15-minute active deadline and is
cancelled if new foreground demand or gaming intent appears.

The CronJob remains the source of the benchmark command, model list, thresholds,
and persistence settings. Keep the referenced model in its `MODELS` environment
value. `ModelBackfill` only decides when that template may run; it does not
rewrite the template.

This example deliberately does not request a GPU. The Job calls an already-warm
endpoint using the internal background workload class injected by the
controller. Exclusive GPU work, model eviction, and `GPULease` acquisition are
outside this contract.

Inspect it with:

```bash
kubectl -n flexinfer-system get modelbackfill model-eval-gemma4-5930k
kubectl -n flexinfer-system describe modelbackfill model-eval-gemma4-5930k
job=$(kubectl -n flexinfer-system get modelbackfill model-eval-gemma4-5930k \
  -o jsonpath='{.status.jobName}')
test -z "$job" || kubectl -n flexinfer-system get job "$job"
```

To stop or defer the example, set `spec.suspend: true` in
`model-eval-5930k.yaml`. Suspension cancels an active attempt and prevents a new
one from starting.
