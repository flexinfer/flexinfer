# gemma4-31b-gptq-keqv post-process

One-shot operator Job that produces an alternative artifact directory
`gptq-w4-g128-keqv/` alongside the existing `gptq-w4-g128/` on the
`gemma4-31b-gptq` source PVC. The new directory contains the same INT4
quantized weights PLUS v_proj shards on heterogeneous layers
(duplicated from k_proj), letting vLLM load the model without the
`Detected some but not all shards of ... qkv_proj are quantized`
ValueError.

## Why

See the header comment in [`../../scripts/gemma4_keqv_postprocess.py`](../../scripts/gemma4_keqv_postprocess.py)
for the full story. Short version: Gemma4 dense attention uses
`k_eq_v` semantics on full-attention layers (v reuses k at inference
time, so the source checkpoint omits v_proj). vLLM handles it via
`Gemma4Model._weight_iterator`, but the GPTQ quant-state check in
`QKVParallelLinear.__init__` fires BEFORE that iterator runs and
rejects the artifact. Duplicating k shards into v shards on disk
makes the quant-state check pass; numerically v == k which is what
the runtime weight iterator would have produced anyway.

## Prerequisites

- The 31B dense GPTQ artifact exists at `gptq-w4-g128/` on PVC
  `gemma4-31b-gptq` in `flexinfer-system`. Verify:
  ```bash
  kubectl get pvc gemma4-31b-gptq -n flexinfer-system -o jsonpath='{.status.phase}{"\n"}'
  # should print: Bound
  ```
- The 31B Model CR is scaled to zero (no serving pod holds the PVC):
  ```bash
  kubectl get deploy gemma4-31b-gptq -n flexinfer-system
  # DESIRED should be 0
  ```

## Apply

Run from the services/flexinfer repo root. The apply is two resources:
a ConfigMap carrying the script + a Job that mounts it.

### 1. Create the ConfigMap from the source script

```bash
export KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml
kubectl create configmap gemma4-31b-keqv-script \
  -n flexinfer-system \
  --from-file=postprocess.py=build/scripts/gemma4_keqv_postprocess.py
```

### 2. Dry-run first (RECOMMENDED)

Edit `job.yaml` and set `DRY_RUN=1`, then:

```bash
kubectl apply -f build/jobs/gemma4-31b-keqv/job.yaml
kubectl wait --for=condition=complete job/gemma4-31b-keqv-postprocess \
  -n flexinfer-system --timeout=5m
kubectl logs -n flexinfer-system -l job-name=gemma4-31b-keqv-postprocess
```

Expected log lines include model_type, num_hidden_layers,
attention_k_eq_v, list of heterogeneous layers, and a duplication plan
that's ~40-50 tensor copies across 10 layers.

### 3. Apply for real

Revert `DRY_RUN` to `"0"` in `job.yaml`, then delete the dry-run Job
(Job names are immutable) and re-apply:

```bash
kubectl delete -f build/jobs/gemma4-31b-keqv/job.yaml
kubectl apply -f build/jobs/gemma4-31b-keqv/job.yaml
kubectl logs -n flexinfer-system -l job-name=gemma4-31b-keqv-postprocess -f
```

On success the Job exits 0 and `gptq-w4-g128-keqv/` exists on the PVC
with the same file count as the source plus one extra shard
`model-keqv-vproj.safetensors`.

### 4. Validation

Before promoting, launch a throwaway serving pod against the new
artifact path and request one completion. See the follow-up MR that
updates the Model CR for the concrete steps.

### 5. Cleanup

After the artifact is promoted and the 31B is serving:

```bash
kubectl delete -f build/jobs/gemma4-31b-keqv/job.yaml
kubectl delete configmap gemma4-31b-keqv-script -n flexinfer-system
```

## Idempotency and re-runs

- The script refuses to overwrite a non-empty `DST_DIR`. Re-running
  requires either deleting the partial output or pointing `DST_DIR` at
  a different name.
- To pick up a change to `build/scripts/gemma4_keqv_postprocess.py`,
  re-create the ConfigMap BEFORE re-applying the Job:
  ```bash
  kubectl delete configmap gemma4-31b-keqv-script -n flexinfer-system
  kubectl create configmap gemma4-31b-keqv-script \
    -n flexinfer-system \
    --from-file=postprocess.py=build/scripts/gemma4_keqv_postprocess.py
  kubectl delete -f build/jobs/gemma4-31b-keqv/job.yaml
  kubectl apply -f build/jobs/gemma4-31b-keqv/job.yaml
  ```

## What it does NOT do

- Does NOT re-quantize. All existing INT4 weights are preserved byte-
  for-byte. Only new v_proj entries are added.
- Does NOT fix the 46 `mlp.down_proj` modules that hit Hessian recovery
  exhaustion and stayed FP16. Those remain in the artifact as-is. If
  you want cleaner MLP quantization, that's a re-quant, not a
  post-process.
- Does NOT change `config.json`, tokenizer files, or any other non-
  safetensors artifact. The target directory is a hardlink overlay of
  the source plus one new shard.
