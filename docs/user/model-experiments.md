# Model experiments

`ModelExperiment` turns a one-off canary into a declarative, bounded lifecycle:

1. The controller creates an owned `Model` from `spec.candidate` and warm-pins it. It removes copied LiteLLM aliases and forces the served model name to the isolated candidate name.
2. After the candidate reports `Ready`, it copies the referenced CronJob into a one-shot Job.
3. The Job is forced to evaluate only the owned candidate through `MODELS=<candidate>=<backend>`.
4. Job success or failure becomes a durable typed verdict in status.
5. The candidate is deleted immediately to release hardware. The Job remains as evidence while its run is retained.

This version is a verdict system, not an automatic promotion system. It never edits an existing or Flux-owned `Model`.

## Artifact-gated launch

Use `spec.artifactGate` when the candidate depends on a long-running
`ModelCache` pipeline. The experiment may be applied immediately, but it does
not create its GPU candidate until the referenced same-namespace cache is
`Ready` and the requested evidence is present:

```yaml
spec:
  artifactGate:
    modelCacheRef: qwen35-35b-a3b-clean-gptq
    requireValidation: true
    requirePublishedDigest: true
    requireSourceMatch: true
```

`requireValidation` requires a successful validator result with a completion
timestamp. `requirePublishedDigest` requires a completed OCI publish and a
valid `sha256` digest. `requireSourceMatch` prevents a ready but unrelated cache
from opening the gate: PVC candidates must live at or below the cache's status
path, while OCI candidates must use the published repository (and the exact
digest when immutable publish evidence is required). The created candidate is
annotated with the cache name, UID, generation, and published digest,
preserving the artifact evidence used to open the gate. Waiting at the gate
does not start the experiment timeout or claim a GPU. Cache status changes wake
matching experiments through an indexed watch, so blocked experiments do not
poll.

Missing, failed, unvalidated, and digestless caches leave the experiment in
`Blocked` with a specific reason and no candidate. Repairing or completing the
cache automatically retries the gate.

## Recurring certification

Set `spec.repeatAfter` to re-run a successful experiment after a cooldown. Failed
runs remain terminal, so a broken candidate cannot repeatedly claim hardware.
Every recurring run receives distinct generation-and-run names, and all newly
created children carry generation and run fence labels. Run 1 keeps the original
one-shot child names for upgrade compatibility. Retained evidence from an
earlier run cannot satisfy a later run.

```yaml
spec:
  repeatAfter: 24h
  historyLimit: 5
```

`status.run` identifies the active or most recently completed run,
`status.nextRunAt` reports the next successful-run recurrence, and
`status.history` contains prior typed verdicts. `historyLimit` defaults to five
and may be set from 1–20. When the limit is exceeded, the oldest status record
and its retained evidence Job are deleted. The current verdict stays in
`status.verdict` until the next run begins.

## Example

The repository includes `deploy/debug/modelexperiment-smoke.yaml`. Its core shape is:

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: ModelExperiment
metadata:
  name: qwen-router-smoke
  namespace: flexinfer-system
spec:
  timeout: 15m
  candidate:
    backend: llamacpp
    image: registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3
    source: HF://rippertnt/Qwen3-1.7B-Q4_K_M-GGUF
    gpu:
      vendor: cpu
    config:
      ggufFile: qwen3-1.7b-q4_k_m.gguf
      nGPULayers: 0
      jinja: true
      reasoningFormat: none
    nodeSelector:
      kubernetes.io/hostname: cblevins-radeonvii
  gauntlet:
    templateRef: model-eval-gauntlet
    env:
      ITERS: "1"
      MIN_DURATION: 5s
      BATCH_SIZE: "16"
      GAUNTLET_EXPECT: "4"
```

`MODELS` is reserved and rejected in `spec.gauntlet.env`. This prevents accidentally benchmarking a production lane and recording its result as the canary verdict.

## Observe and clean up

```bash
kubectl -n flexinfer-system get modelexperiment
kubectl -n flexinfer-system describe modelexperiment qwen-router-smoke
JOB=$(kubectl -n flexinfer-system get modelexperiment qwen-router-smoke -o jsonpath='{.status.jobName}')
kubectl -n flexinfer-system logs "job/$JOB"
kubectl -n flexinfer-system delete modelexperiment qwen-router-smoke
```

Phases progress through `Deploying`, `Serving`, and `Evaluating`, then terminate as `Succeeded` or `Failed`. A recurring successful experiment remains `Succeeded` until `status.nextRunAt`, then advances to a fresh `Deploying` run. `Blocked` means the declaration or referenced CronJob needs correction. Setting `spec.suspend: true` removes active candidate and Job resources.

The default timeout is 30 minutes. A timeout, candidate startup failure, lost candidate, or failed gauntlet produces a failed verdict and releases the candidate.
