# gemma4-31b-gptq k_eq_v post-process (Flux-managed)

One-shot transformation that produces an alternative artifact directory
`gptq-w4-g128-keqv/` alongside the existing `gptq-w4-g128/` on the
`gemma4-31b-gptq` source PVC. The new directory has identical INT4
quantized weights plus v_proj shards on Gemma4 heterogeneous (k_eq_v)
full-attention layers, duplicated byte-for-byte from the corresponding
k_proj shards. That makes vLLM's GPTQ quant-state check pass during
`QKVParallelLinear.__init__` and lets the 31B serve on a 24 GiB card
without dequantizing attention (which would blow VRAM).

See `postprocess.py` for the full root-cause + algorithm writeup.

## Why this is in `deploy/tasks/` (not a one-shot operator job)

Flux applies this directory as part of the `flexinfer-models`
Kustomization. The Job is idempotent — the script exits 0 immediately
if the destination artifact is already valid — so Flux re-reconciles
are safe.

No `kubectl create configmap` or `kubectl apply -f` by hand. Just:

```
git merge  →  Flux reconciles  →  Job runs  →  artifact on PVC
```

## Files

| File | Purpose |
|---|---|
| `postprocess.py` | The transform (single source of truth; no duplicated copies). |
| `job.yaml` | Kubernetes Job spec. References the generated ConfigMap by the unhashed name; kustomize rewrites to the hashed name at build time. |
| `kustomization.yaml` | Registers the Job + generates the ConfigMap from `postprocess.py` via `configMapGenerator`. Script edits → new CM hash → new Job spec → Flux recreates the Job. |

## Lifecycle

1. **git merge** triggers Flux reconcile of `flexinfer-models`.
2. Flux applies `Job/gemma4-31b-keqv-postprocess` + `ConfigMap/gemma4-31b-keqv-script-<hash>`.
3. Scheduler picks any control-plane node (NFS PVC mounts anywhere, CPU-only).
4. Pod starts, mounts the script via ConfigMap, execs `python3 /script/postprocess.py`.
5. Script validates `config.json` + `index.json`, plans duplications, writes a single extra shard `model-keqv-vproj.safetensors`, hardlinks every other file into the new dir, rewrites the index. Validates q+k+v coverage before exit.
6. Pod exits 0. After 86400 seconds the TTL reaper cleans up.
7. Subsequent Flux reconciles are no-ops: the Job resource is unchanged, kustomize-controller doesn't recreate; and even if it did, the script's idempotence check sees a complete artifact and exits 0.

## Inspecting

```bash
export KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml

# Job status
kubectl get job gemma4-31b-keqv-postprocess -n flexinfer-system

# Live logs while running, or historical logs within the 24h TTL window
kubectl logs -n flexinfer-system -l job-name=gemma4-31b-keqv-postprocess --tail=200
```

## Rolling back

```bash
# Revert the source-path change in the Model CR (deploy/models/gemma4-31b-gptq.yaml)
# and remove this task directory from deploy/kustomization.yaml. Flux will
# prune both. The `gptq-w4-g128-keqv/` output stays on the PVC (harmless; can
# be hand-removed later).
```

## Editing the script

- Change `postprocess.py`, commit, merge. Kustomize generates a new ConfigMap hash. Flux recreates the Job with the new spec. Pod runs, hits the idempotence check (destination already valid) → exits 0. No-op.
- To **actually re-run** the transform (e.g. after a logic change that should re-process layers), bump `DST_DIR` in `job.yaml` to a new name (e.g. `gptq-w4-g128-keqv-v2`) and update the Model CR `source:` to match in the same MR.

## Known non-goals

- Does NOT re-quantize. All INT4 weights preserved byte-for-byte.
- Does NOT fix the 46 `mlp.down_proj` modules that hit Hessian recovery exhaustion and stayed FP16 during the original quant run. Those stay unquantized in the artifact. Cleaner MLP quant → separate re-quant task.
