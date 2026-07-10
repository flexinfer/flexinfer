# Model experiments

`ModelExperiment` turns a one-off canary into a declarative, bounded lifecycle:

1. The controller creates an owned `Model` from `spec.candidate` and warm-pins it. It removes copied LiteLLM aliases and forces the served model name to the isolated candidate name.
2. After the candidate reports `Ready`, it copies the referenced CronJob into a one-shot Job.
3. The Job is forced to evaluate only the owned candidate through `MODELS=<candidate>=<backend>`.
4. Job success or failure becomes a durable typed verdict in status.
5. The candidate is deleted immediately to release hardware. The Job remains for evidence until the experiment is deleted.

This version is a verdict system, not an automatic promotion system. It never edits an existing or Flux-owned `Model`.

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
kubectl -n flexinfer-system logs job/qwen-router-smoke-gauntlet
kubectl -n flexinfer-system delete modelexperiment qwen-router-smoke
```

Phases progress through `Deploying`, `Serving`, and `Evaluating`, then terminate as `Succeeded` or `Failed`. `Blocked` means the declaration or referenced CronJob needs correction. Setting `spec.suspend: true` removes active candidate and Job resources.

The default timeout is 30 minutes. A timeout, candidate startup failure, lost candidate, or failed gauntlet produces a failed verdict and releases the candidate.
