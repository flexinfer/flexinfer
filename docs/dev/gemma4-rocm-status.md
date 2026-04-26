---
title: Gemma4 ROCm Status
description: "Living status for Gemma4 quantized runtime gates on unified gfx1100 runtimes."
---

# Gemma4 ROCm Status

This document tracks the current managed state of the Gemma4 quantized
deployments on the unified `gfx1100` runtime path.

It also defines a reusable **quantized-artifact promotion gate** for other
families (Qwen, Llama, Mistral, etc.): the gate is motivated by Gemma4 findings
but is intentionally model-family agnostic.

Update this document whenever a tuning change lands or a new blocker is found.

## Current profiles

| Model ID | Model CR | Node | Attention / KV path | Intent |
|----------|----------|------|---------------------|--------|
| `gemma4-31b-gptq` | `gemma4-31b-gptq` | `cblevins-7900xtx` | `TRITON_ATTN` + float16 KV | Current warm primary at the validated 2K ceiling (`minReplicas: 1`) |
| `gemma4-26b-a4b-gptq` | `gemma4-26b-a4b-gptq` | `cblevins-7900xtx` | `TRITON_ATTN` + float16 KV | 8K rollback baseline / fallback (`minReplicas: 0`) |
| `gemma4-26b-a4b-gptq-long` | `gemma4-26b-a4b-gptq-long` | `cblevins-7900xtx` | `TRITON_ATTN` + FP8 KV | 16K canary only (`minReplicas: 0`, `warmPolicy: ondemand`, priority matches the primary for demand swaps) |

## Current profile knobs

| Model ID | `maxModelLen` | `maxNumBatchedTokens` | `gpuMemoryUtilization` | Serverless |
|----------|---------------|-----------------------|------------------------|------------|
| `gemma4-31b-gptq` | `2048` | runtime default | `0.95` | `minReplicas: 1` |
| `gemma4-26b-a4b-gptq` | `8192` | `512` | `0.95` | `minReplicas: 0` |
| `gemma4-26b-a4b-gptq-long` | `16384` | `160` | `0.98` | `minReplicas: 0` |

## Latest baseline

Date: **2026-04-26**

Current finding:

- The current Gemma4 26B-A4B hybrid artifact (`gptq-w4-g128-attnfp16-clean`) is
  coherent and stable at **8K**.
- The same artifact is too large/risky for default **32K** serving on a single
  24 GB gfx1100 card.
- A 32K FP16-KV boot failed cleanly on 2026-04-26: vLLM needed 6.88 GiB of KV
  cache after loading the hybrid artifact, but only 1.87 GiB remained. The
  canary is therefore narrowed to 16K with `kvCacheDtype: fp8_e4m3` for live
  validation.
- The smaller dense-validated artifact path remains blocked behind the
  `cblevins-5930k` memory guard; do not uncordon that node or remove the taint
  just to accelerate this lane.

## Quantized-artifact promotion gate (reusable)

Use this gate before promoting any quantized artifact (any model family) from
canary to default:

1. **Artifact validation at target context**
   - correctness/repetition checks pass (probe script + production parser path)
   - warm and cold runs are both clean
2. **Memory headroom validation**
   - no OOM or allocator instability at target context on intended GPU class
3. **Canary containment**
   - `minReplicas: 0`
   - `warmPolicy: ondemand` (or equivalent non-primary policy)
   - no alias/default swap while unvalidated
4. **Explicit promotion change**
   - only after 1-3 pass, raise context/default aliases in a separate change

Controller enforcement:

- Mark guarded models with
  `flexinfer.ai/promotion-gate: quantized-artifact-v1`.
- Warm-primary promotion (`serverless.minReplicas > 0`, serverless disabled,
  or `config.warmPolicy: primary`) requires either
  `flexinfer.ai/promotion-validation: passed` or
  `flexinfer.ai/promotion-evidence: <artifact-or-runbook-ref>`.
- Canary / scale-to-zero operation remains allowed without evidence so
  candidate artifacts can be probed deliberately.
- The controller reports the decision in the Model `PromotionGate` condition.

## Long-context readiness probe (promotion gate input)

Use `scripts/probe-gemma4-long-context.sh` for repeatable target-context
validation before promoting any long-context quantized canary. The probe runs:

- short sanity: `2 + 2`
- medium context: repeated-token prompt with a retained verification code
- long context: default ~30k-token prompt with the same retained verification code

It writes both JSON and Markdown artifacts, records prompt/completion tokens and
elapsed time, and exits nonzero if the model returns obvious garbage, repetition,
or the wrong answer.

Common runs:

```bash
kubectl -n ai port-forward svc/litellm 18000:8000
ENDPOINT=http://127.0.0.1:18000 ./scripts/probe-gemma4-long-context.sh
```

Direct service URL with cluster metadata/log hints:

```bash
ENDPOINT=http://litellm.ai.svc.cluster.local:8000 \
  AUTH_TOKEN=sk-litellm-master-key \
  MODEL=gemma4-26b-a4b-gptq \
  POD_SELECTOR='app=gemma4-26b-a4b' \
  ./scripts/probe-gemma4-long-context.sh
```

## Features working

| Feature | Status | Notes |
|---------|--------|-------|
| Unified `gfx1100` runtime path | Working | No separate debug runtime required |
| Managed Gemma4 CRD deployment | Working | 26B baseline + 31B on-demand + long canary reconcile through Flux |
| LiteLLM aliases | Working | Baseline aliases are pinned to the 26B 8K primary |
| Tool calling | Working | Gemma parser path remains enabled on baseline profiles |
| Conservative rollout gates | Working | Canary remains scale-to-zero and non-primary |
| 8K baseline coherence | Working | Current hybrid serves coherently at 8K on gfx1100 |

## Features still being chased

| Feature | Status | Current read |
|---------|--------|--------------|
| Smaller long-context artifact | In progress | Needed before promoting beyond 8K baseline |
| 16K promotion validation | In progress | 26B canary uses FP8 KV and remains scale-to-zero until probe evidence passes |
| 32K promotion validation | Blocked on artifact | Current hybrid needs a smaller artifact; FP16 KV boot needs 6.88 GiB with only 1.87 GiB available |
| Compressed-tensors + FP8 KV lane | Planned canary | Separate compressed-tensors artifact work remains disabled/non-default until validated |
| AITER on ROCm | Blocked / deferred | `TRITON_ATTN` remains the stable path on RDNA3 |
| Production-grade long-context default | Deferred | Keep long profiles non-primary until gate checks pass |
| Speculative decoding | Not started | No Gemma4 speculator path wired yet |
| FP8-centric KV path | Planned canary | Reference config only; not promoted |

## Gemma4 GPTQ Pipeline Models

### 26B-A4B MoE (GPTQ INT4)

| Field | Value |
|-------|-------|
| ModelCache | `gemma4-26b-a4b-gptq` |
| Model CR | `gemma4-26b-a4b-gptq` |
| Source | `google/gemma-4-26B-A4B-it` |
| Node | `cblevins-7900xtx` (gfx1100) |
| Pipeline | Download BF16 (~27 GB) → Abliterate → GPTQ INT4 (~7-13 GB) |
| PVC | 96 Gi (nvme-1r-gpu) |
| Shared Group | `7900xtx-textgen` (priority 200, on-demand fallback) |
| Aliases | `gemma4-26b`, `gemma4-26b-a4b`, `gemma4-moe` |

**MoE Architecture**: 25.2B total / 3.8B active, 128 experts top-8, 30 layers (25 GDN + 5 full-attention).
Current hybrid export is validated at 8K; it is not promoted for default 16K/32K service.
The separate `gemma4-26b-a4b-gptq-long` canary runs at priority 250 so explicit
16K demand can swap it in ahead of the idle 31B primary for validation only.

**Abliteration safety**: Only `o_proj` (shared attention output). Expert FFN weights auto-skipped. `ablitateLmHead: false` (save corruption bug).

**Quantization config**: `sym=true`, `descAct=false`, `maxSamples=512` (MoE expert coverage), `timeoutSeconds=43200` (12h for 640 expert modules).

### 31B Dense (GPTQ INT4)

| Field | Value |
|-------|-------|
| ModelCache | `gemma4-31b-gptq` |
| Model CR | `gemma4-31b-gptq` |
| Source | `google/gemma-4-31B-it` |
| Node | `cblevins-7900xtx` (gfx1100) |
| Pipeline | GPTQ INT4 runtime serving from `gptq-w4-g128-keqv` |
| Status | Warm primary at 2K (`minReplicas: 1`) |

**Dense Architecture**: 30.7B params, 60 layers (50 GDN + 10 full-attention). Requires 128 GB RAM node for abliteration + save overhead.

**Abliteration**: Both `o_proj` and `down_proj` (safe for dense models, no MoE experts). `maxMemoryGB=96`.

**Quantization config**: `maxMemoryGB=96`, `maxSamples=256` (no MoE), `timeoutSeconds=28800` (8h).

### GPTQ Performance on ROCm

| Model | Decode tok/s | Prompt tok/s | VRAM | Context |
|-------|-------------|-------------|------|---------|
| 26B-A4B MoE INT4 | ~72 | ~1800 | ~13 GB | 8K baseline |
| 31B Dense INT4 | TBD | TBD | ~20 GB | 2K validated ceiling |

ExLlama v2 kernels (HIP-compiled) with `sym=true` achieve 7x faster decode than AWQ on gfx1100.

## Reference-only future canary (disabled)

The following is a reference snippet for compressed-tensors + FP8 KV
experiments. Keep this out of live defaults until artifact validation gates pass.

```yaml
# reference only: do not include in deploy/models/kustomization.yaml yet
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: gemma4-31b-gptq-fp8kv-canary
  annotations:
    flexinfer.ai/promotion-gate: quantized-artifact-v1
    flexinfer.ai/promotion-state: canary-reference-only
spec:
  backend: vllm
  source: pvc://gemma4-31b-gptq/gemma4-31b-gptq/compressed-tensors-fp8kv-candidate
  serverless:
    enabled: true
    minReplicas: 0
  config:
    quantization: compressed-tensors
    kvCacheDtype: fp8_e4m3
    maxModelLen: 16384
    warmPolicy: ondemand
```

## Deployment Reliability (2026-04-13)

| Feature | Status | Notes |
|---------|--------|-------|
| GPUProfile watch | Working | Controller watches GPUProfile CRs; image changes trigger reconciliation |
| Image drift detection | Working | Stale running jobs auto-deleted on GPUProfile image update |
| Script version marker | Working | `FLEXINFER_SCRIPT_VERSION=v12` checked at job startup |
| Deploy automation | Working | `make deploy-quantizer QUANTIZER_ARCH=gfx1100` |
| Spec hash with image | Working | `quantSpecHashWithImage()` includes resolved image in hash |

## Next tuning queue

1. Validate the 26B 16K FP8-KV canary with warm/cold probe evidence.
2. Produce a smaller 26B artifact candidate for 32K validation.
3. Keep long-context canaries non-primary (`minReplicas: 0`, `warmPolicy: ondemand`)
   until promotion criteria are met.
4. Validate compressed-tensors + FP8 KV on a dedicated canary before any alias/default changes.
5. Generalize this gate to additional quantized model families in shared docs/manifests.
