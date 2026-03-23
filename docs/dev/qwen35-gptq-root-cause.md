# Qwen3.5-27B GPTQ Garbage Inference — Root Cause Analysis

**Date:** 2026-03-23
**Status:** Root cause identified, fix pending

## Summary

The GPTQ-quantized Qwen3.5-27B model produces garbage inference output via vLLM on
AMD gfx1100. Root cause: **`in_proj_ba` layers were not quantized by GPTQModel but
vLLM's GPTQ loader converts ALL linear layers to QuantLinear**, causing 48 of 64
layers to operate on random/zero-initialized weights.

## Architecture Background

Qwen3.5-27B uses a hybrid GatedDeltaNet architecture:
- **48 linear_attention layers** (GDN with conv1d + FLA)
- **16 full_attention layers** (standard multi-head attention)

Each linear attention layer has an `in_proj_ba` projection (`[96, 5120]`) that
projects input hidden states into the `a` and `b` components used by
`fused_gdn_gating` to compute the decay rate `g` and update gate `beta`.

## Root Cause

### 1. GPTQModel skips `in_proj_ba` during quantization

GPTQModel 5.8.0 did not quantize the `in_proj_ba` layers. The checkpoint contains:

```
model.layers.{0..62}.linear_attn.in_proj_ba.weight  → full-precision BF16
model.layers.{0..62}.linear_attn.in_proj_ba.qweight  → MISSING
model.layers.{0..62}.linear_attn.in_proj_ba.scales   → MISSING
```

This is likely because the layer shape `[96, 5120]` has only 96 output features,
which may fall below GPTQModel's minimum quantization threshold.

### 2. vLLM's GPTQ loader replaces ALL linear layers with QuantLinear

vLLM does not check whether individual layers have quantized weights in the
checkpoint. It converts every linear layer to a quantized kernel (ExllamaV2 or
TritonV2QuantLinear). When `in_proj_ba` loads, the full-precision `.weight` tensor
is treated as unexpected and discarded; the expected `qweight/scales/qzeros` tensors
are missing and randomly initialized.

### 3. Cascading corruption

With random `in_proj_ba` weights:
- `a` and `b` projections produce random values
- `fused_gdn_gating(A_log, a, b, dt_bias)` computes random `g` (decay) and `beta`
- FLA operates on corrupted Q/K/V/g/beta → garbage attention output
- All 48 linear attention layers are affected → model output is pure noise

## Evidence

### Diagnostic data from vLLM (before root cause found)

```
Layer 0 FLA output: std=0.080     (some signal, first layer)
Layer 1 FLA output: std=0.000375  (near-zero, corruption accumulates)
Layer 2 FLA output: std=0.000252  (further decay)
```

- Both naive PyTorch and Triton FLA implementations produce identical garbage
- Hidden states flow correctly through the pipeline (no framework-level corruption)
- The corruption originates from the FLA INPUTS, not the FLA kernel itself

### Debug job confirmation

The debug job (`deploy/debug/qwen35-gptq-direct-test.yaml`) confirmed:

```
in_proj_ba full-precision (.weight): 48 tensors
in_proj_ba quantized (qweight/etc): 0 tensors
CONFIRMED: in_proj_ba was NOT quantized by GPTQModel
```

## Fix Options

### Option A: Patch vLLM's GPTQ weight loader (preferred)

Add a check in vLLM's `GptqWeightsLoader` to detect layers that have `.weight` in
the checkpoint but no `.qweight`. Keep these as regular `nn.Linear` instead of
converting to `QuantLinear`.

**Files:** `vllm/model_executor/layers/quantization/gptq.py`

This fix handles mixed quantized/unquantized checkpoints generically and would
benefit any model where some layers are too small to quantize.

### Option B: Re-quantize with explicit target modules

Re-run GPTQModel quantization with `in_proj_ba` explicitly listed in
`modules_to_not_convert` (or ensure it IS quantized by lowering the threshold).

**Trade-off:** Re-quantization takes ~2 hours on gfx1100 and requires the full
FP16 model (~54 GB). Option A is faster and more general.

### Option C: Runtime patch in vllm_qwen35_patches.py

Add a section to the existing startup patch that detects unquantized `in_proj_ba`
layers post-load and replaces the broken `QuantLinear` with `nn.Linear` loaded from
the checkpoint's full-precision weights.

**Trade-off:** Fragile, model-specific. Option A is better long-term.

## Files

| File | Purpose |
|------|---------|
| `build/scripts/vllm_qwen35_patches.py` | Runtime patches for Qwen3.5 on vLLM |
| `deploy/debug/qwen35-gptq-direct-test.yaml` | Debug job (ConfigMap + Job) |
| `deploy/debug/kustomization.yaml` | Kustomize entry for debug manifests |
