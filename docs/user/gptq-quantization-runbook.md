---
title: GPTQ Quantization Runbook
description: Operating GPTQ quantization jobs — diagnosing failures, recovering from stalls, and tuning for slow or numerically-tricky hardware.
---

# GPTQ Quantization Runbook

## 1. What this covers

Operational procedures for GPTQ INT4 quantization runs on FlexInfer, specifically:

- Recognizing the failure modes that have bitten us in production.
- Recovering a failed / stalled / exhausted ModelCache without a full re-download.
- Picking sensible `timeoutSeconds`, `maxMemoryGB`, and Hessian-recovery tunables for a new model.
- Responding to the Prometheus alerts the controller emits.

This is **not** a reference for the GPTQ algorithm itself — see `docs/design/quantization-pipelines.md` for architecture.

## 2. Pipeline at a glance

For a GPTQ ModelCache, the controller runs these Jobs in order:

1. **downloader** — pulls source weights from HuggingFace into the PVC.
2. **abliterate** (optional) — modifies weights to suppress refusal directions.
3. **quantize** — the GPTQModel run. This is the expensive one: typically 60–90% of pipeline wall time.
4. **publish** — packages the quantized output as an OCI artifact.

All four run as K8s `batch/v1.Job`s with `backoffLimit=2`, so up to **three** attempts per phase before the ModelCache is marked `Failed`. Each Job has its own `activeDeadlineSeconds` derived from the ModelCache's `spec.<phase>.timeoutSeconds`.

## 3. What GPTQ actually does (and why it's so slow)

For each transformer layer:

1. **Forward pass** with calibration data through the layer's modules (~seconds per layer).
2. **Hessian accumulation**: `X.T @ X` in FP32 for each linear module (cheap, piggybacks on step 1).
3. **Cholesky inverse** of the regularized Hessian to solve for optimal quantized weights. **This is where time goes.** Each Cholesky on a 21504² FP32 matrix is O(n³/3) ≈ 3.3 TFLOPs. On gfx906 (Vega20) with ROCm LAPACK on CPU fallback, one Cholesky is ~40 s. On gfx1100 with native GPU LAPACK, it's seconds.
4. **Pack** the quantized weights into the INT4 format and replace the module.

A 59-layer model has ~413 modules (7 per layer × 59). Most succeed on the first try. A handful are numerically ill-conditioned and need the Hessian recovery path.

## 4. Hessian recovery — what, why, when

### The problem

Some modules (especially large-fan-in ones like `mlp.down_proj` with input dim 21504) have Hessian matrices that are nearly singular on BF16 calibration activations — 7 mantissa bits of precision isn't enough when you're summing 58k samples worth of activation products. Cholesky fails on these matrices.

### The recovery loop (see `build/scripts/quantize_gptq.py:_patched_hessian_inverse`)

1. **Damp sweep** (attempt 0): add `damp × mean(diag)` to the diagonal, where `damp` starts at `GPTQ_DAMP_PERCENT_OVERRIDE` (default 0.05) and steps by `GPTQ_DAMP_AUTO_INCREMENT_OVERRIDE` (default 0.1). Retry Cholesky after each step up to `damp ≥ 1.0`.
2. **Diagonal floor** (attempts 1–6): if the damp sweep failed, apply `floor_base × 10^(attempt-1)` to the diagonal and try ONE Cholesky (no damp sweep at this tier). `floor_base` is `mean(diag) × GPTQ_HESSIAN_DIAG_FLOOR_SCALE` (default 0.01) under the default `mean` mode.
3. **Exhaustion**: if all 6 floor attempts still fail, log it and return `(None, 1.0)` so GPTQModel treats the module as unquantized (falls through to its own default handling).

### Why the defaults are what they are

Before MR !156, the floor was scaled by `abs_max × 1e-6` — ~1000× smaller than `damp × mean` for typical activations, so attempts 1–3 were invisibly weak. Modules would burn 60 min per sweep × 7 sweeps = 7 hours just to exhaust. The `mean × 0.01` default puts attempt 1 on a comparable scale to damp (20% of it), attempt 2 at 2× damp, attempt 3 at 20× damp — each attempt is meaningful.

`damp_step=0.1` cuts the initial sweep from 95 Cholesky iterations (`step=0.0015`) to 10, which is the single biggest wall-clock win on slow Cholesky backends.

### HIP error cascade (gfx906 specific)

A Cholesky failure on gfx906 can poison the HIP context — subsequent GPU linalg ops on the SAME tensor fault with `torch.AcceleratorError: HIP error: invalid argument`, including `torch.isfinite(H).sum()`. MR !156 wraps the pre-loop sanitize check and setup in try/except so a bad Hessian returns cleanly instead of crashing the whole job.

**Do not** move H to CPU as a workaround — the rocm/pytorch container is compiled without CPU LAPACK, so Cholesky on CPU dies with `LAPACK library not found in compilation` and EVERY module exhausts.

## 5. Picking timeouts and memory for a new model

### Expected per-layer wall time

| Hardware | ~min per layer (no recovery) | ~min per layer (w/ 1 problematic module) |
|----------|------------------------------|------------------------------------------|
| gfx1100 (24 GB) | 1–2 | 2–4 |
| gfx906 (16 GB) | 3–5 | 8–12 |

Problematic modules exhausting all 7 attempts add ~1 min each on the post-fix code (~10 min each with the pre-fix code — don't use old images).

### `timeoutSeconds` rule of thumb

```
timeoutSeconds = (per_layer_wall × num_layers + save_minutes + 30_min_buffer) × 60
```

For 31B on gfx906 (59 layers × ~10 min + 30 min save + buffer) = ~11 h — use 43200 (12 h) today. The CRD max is 172800 (48 h) post-MR !161.

### `maxMemoryGB` rule of thumb

Quantize phase: model size + ~10 GiB overhead for activations and Hessian accumulation.

**Save phase can be the bottleneck** — GPTQModel's `save_quantized` loads all shards and rewrites; peak RSS is close to the full FP16 model size. For 26B BF16 (~52 GB) we needed `maxMemoryGB=40` (container limit 52 Gi with the 12 GiB GPU driver overhead) to clear save. See MR !157.

### Node-selection gotcha

On the radeonvii / 5930k nodes we have one GPU each and also run the control plane (etcd). Container memory limits near `node.allocatable - kubelet_reserved` can tip the kubelet into eviction. Leave at least 5 GiB headroom.

## 6. Recovering a stuck / failed ModelCache

### 6.1 The ModelCache is `Failed` but the run got ~90% through

Happens on DeadlineExceeded. Three options:

**Option A — raise the timeout and retry.** Update `deploy/modelcaches/<name>.yaml`, commit, let Flux reconcile, then reset the failure state:

```bash
export KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml

# 1. Edit + commit deploy/modelcaches/<name>.yaml with a larger timeoutSeconds
# 2. Wait for Flux reconcile
flux reconcile source git flexinfer -n flux-system
flux reconcile kustomization flexinfer-models -n flux-system

# 3. Reset retry state so the controller will re-attempt
kubectl patch modelcache -n flexinfer-system <name> \
  --type=merge --subresource=status \
  -p '{"status":{"phase":"Quantizing","retryCount":0,"lastFailurePhase":null,"lastFailureTime":null}}'
```

**Option B — let the existing Job clock age out of the grace window, then delete it.** If the controller already created a Job with the old deadline, it will keep that deadline until recreated. Delete the Job once Flux has applied the new spec:

```bash
kubectl delete job -n flexinfer-system <name>-quantize
# Controller will recreate with the new deadline
```

**Option C — accept the loss and run again from scratch.** Reasonable if the model is small and the failure was early.

### 6.2 The ModelCache OOMKilled during save

Save phase OOMs are nasty: GPTQModel's resume only handles `stage=quantizing`, so an OOM at `stage=saving` restarts the whole quantize from layer 0. Fix the memory limit FIRST, then recover:

1. Update `deploy/modelcaches/<name>.yaml` with a larger `spec.quantization.maxMemoryGB`. (See §5 for sizing.)
2. Commit + reconcile Flux.
3. Delete the failed Job to force recreation with the new memory limit:
   ```bash
   kubectl delete job -n flexinfer-system <name>-quantize
   ```
4. The controller will recreate the Job. A fresh pod with the new memory limit runs from layer 0 — yes, all progress is lost. This is unfortunate but unavoidable until we ship real per-layer resume.

### 6.3 Quantize is running but stuck (no layer progress for 30+ min)

Check for Hessian recovery exhaustion:

```bash
POD=$(kubectl get pods -n flexinfer-system \
  -l job-name=<name>-quantize \
  -o jsonpath='{.items[0].metadata.name}')

# Is the process alive?
kubectl top pod -n flexinfer-system $POD

# Latest recovery events
kubectl logs -n flexinfer-system $POD \
  | grep -E 'Hessian recovery|damp recovery|diagonal floor' \
  | awk '!seen[$0]++' | tail -20
```

Interpretation:

- **Many `diagonal floor +N (attempt X/6)` lines within a few minutes** — recovery is working; just let it finish (attempt 6 is worst case ~5 min on gfx906 with `damp_step=0.1`).
- **One `starting damp recovery` with no follow-ups for 30+ min** — damp sweep is still running. On pre-fix code this can be an hour; with the post-MR !156 fix it's ~6 min per sweep. If you're on old code, bump `GPTQ_DAMP_AUTO_INCREMENT_OVERRIDE` via the controller env and redeploy.
- **`Hessian recovery exhausted`** — the module fell through to `(None, 1.0)` and the run should have continued. If it didn't, see the HIP cascade note in §4.

### 6.4 Job keeps getting recreated right after start

Check for image-drift preemption. MR !160 added a 10-minute grace window; runs older than that should be safe. Symptoms:

```bash
kubectl get events -n flexinfer-system \
  --field-selector involvedObject.name=<name>-quantize \
  --sort-by=.lastTimestamp | tail -15
```

If you see repeated `SuccessfulCreate` events with `QuantizerImageDrift` warnings within the first 10 min, the GPUProfile digest is disagreeing with the running Pod. Usually this is Flux reconciling — check if someone hand-patched the GPUProfile recently.

**To preempt a long-running job with a new image intentionally:**

```bash
kubectl annotate modelcache -n flexinfer-system <name> \
  flexinfer.ai/force-image-update=true --overwrite
```

Then delete the Job. The controller will recreate with the current GPUProfile image. Remove the annotation after.

## 7. Alert response matrix

These alerts ship from `charts/flexinfer/templates/prometheusrule.yaml` when `alerting.enabled=true`.

| Alert | First check | Action |
|-------|-------------|--------|
| `FlexInferQuantizationDeadlineApproaching` (>80% of deadline) | `JobProgressPercent` vs `QuantizationLayerIndex`. If layer count is >75% of total, the time estimate is accurate — let it finish. If layer count is much lower, job is genuinely slow. | Consider bumping `timeoutSeconds` before the critical alert fires. |
| `FlexInferQuantizationDeadlineCritical` (>95%) | Same as above. | Either extend timeout now (§6.1) or let it die and accept the restart cost. |
| `FlexInferQuantizationJobFailed` | `kubectl describe job` + pod termination message | Classify: OOM → §6.2, DeadlineExceeded → §6.1, HIP error → §4. |
| `FlexInferQuantizationStalled` (45 min no layer progress) | Hessian events, §6.3 | Usually just waiting on floor-attempt exhaustion or a genuinely slow module. If real stall (no CPU activity), restart the Pod. |

## 8. Known limitations (as of today)

- **No mid-run resume.** GPTQModel runs `model.quantize()` top-to-bottom; there's no `start_layer=N` knob. The `.flexinfer-gptq-cache/checkpoint.json` file tracks progress but is informational only — a restart re-quantizes every layer. Real resume needs a GPTQModel looper hook plus per-layer state persistence; not shipped yet.
- **No save-phase resume.** An OOM at `stage=saving` throws away the entire quantize pass. Mitigation: size memory correctly up front (§5).
- **`make deploy-quantizer-full` BuildKit stale content.** Observed once — the remote builder shipped a quantizer image containing stale script content despite `--no-cache`. MR !160 added a post-build md5 parity check that catches this before push. If you see `ERROR: Image script content mismatch`, the check did its job; retry the deploy or `docker system prune` on the remote context.

## 9. Related reading

- `docs/design/quantization-pipelines.md` — architecture.
- `build/scripts/quantize_gptq.py` — the actual Python quantize driver (Hessian-recovery patch starts around `patch_gptq_hessian_inverse`).
- `pkg/quantization/gptq.go` — controller-side Job construction.
- `controllers/modelcache_quantization.go` — reconcile loop, image-drift handling, telemetry.
- MR !156 — Hessian recovery on gfx906.
- MR !160 — image-drift grace window + deploy-script md5 check.
- MR !161 — `timeoutSeconds` CRD cap raised to 48 h.
