# Qwen3.5-27B GPTQ Garbage Inference — Root Cause Analysis

**Date:** 2026-03-24 (updated)
**Status:** Root cause confirmed — abliteration corrupted the FP16 source model

## Summary

The GPTQ-quantized Qwen3.5-27B model produces garbage inference output on AMD
gfx1100. **Root cause: abliteration (refusal direction removal) catastrophically
corrupted the FP16 model weights.** The refusal direction norm of 152.96, applied
to all 64 layers (including 48 GDN linear attention layers), destroyed the model's
ability to generate coherent text. GPTQ quantization faithfully preserved the
already-corrupted weights.

## Investigation Timeline

### Phase 1: vLLM GDN debugging (2026-03-22 – 2026-03-23)

Initial investigation focused on vLLM's Qwen3.5 GDN implementation:
- Confirmed `in_proj_ba` not quantized by GPTQModel (96×5120 below threshold)
- Added Patch 16 to `vllm_qwen35_patches.py`: post-load fixup replaces broken
  `QuantLinear` with `nn.Linear` for unquantized layers
- Confirmed weight loading correct: contiguous b/a split matches Qwen3.5 layout
- Confirmed FLA inputs (Q/K/V, gating) have reasonable magnitudes
- FLA output rapidly decays to near-zero: layer 0 std=0.08, layer 1 std=0.0004

### Phase 2: Bypass vLLM — direct GPTQModel test (2026-03-24)

Deployed `qwen35-gptq-transformers-test.yaml` using the GPTQ quantizer image
(transformers 5.3.0 + GPTQModel) to test the GPTQ checkpoint without vLLM:

```
Image: registry.harbor.lan/flexinfer/quantizer:gptq-rocm-gfx1100
Kernel: TritonV2QuantLinear
Model: /models/qwen35-27b-opus-distill-gptq/gptq-w4-g128
```

**Result: GARBAGE.** All three test prompts produced multilingual noise:
```
Response: corrections珠宝首饰 quil珠宝首饰 remarks perpendicular珠宝首饰...
Top logit for "2+2": "corrections" (not "4")
```

**Conclusion: The GPTQ weights are bad. Problem is NOT in vLLM.**

### Phase 3: FP16 abliterated source test (2026-03-24)

Deployed `qwen35-fp16-sanity-test.yaml` to test the pre-quantization FP16 model
(50 safetensors shards, ~54GB BF16) with CPU offload:

```
Model: /models/qwen35-27b-opus-distill-gptq (parent dir = abliterated FP16)
Device map: 20 GPU layers + 44 CPU layers
```

**Result: GARBAGE.** The FP16 model produces identical corruption:
```
Response: 錠 USERS institutooge сухо陪护 Plymouth prevalence...
Top logit for "2+2": "錠" (logit=10.875)
Entropy ratio: 0.7930 (near uniform = random)
```

**Conclusion: Abliteration corrupted the FP16 model BEFORE quantization.**

## Root Cause: Aggressive Abliteration

### Abliteration parameters

```json
{
  "layersModified": 64,
  "refusalDirNorm": "152.955399",
  "maxNormLayer": 63,
  "numSamples": 128,
  "status": "complete",
  "ts": "2026-03-20T16:42:00Z"
}
```

### Why abliteration failed on Qwen3.5

1. **All 64 layers modified**: Abliteration applied orthogonal projection to
   `o_proj`, `out_proj`, and `down_proj` in ALL layers, including 48 GDN linear
   attention layers with unique properties (non-square weight matrices, decay/gate
   mechanics)

2. **High refusal direction norm (152.96)**: For comparison, 7B models typically
   show norms of 20-50. The 27B model's 5120-dim hidden size contributes, but
   152.96 suggests the computed refusal direction captured significant model
   capability rather than just refusal behavior

3. **GDN layers are fragile**: The GatedDeltaNet recurrence (S = exp(g)·S + β·Δv⊗k)
   is sensitive to `out_proj` modifications. Unlike standard attention where the
   output projection simply remaps attention output, in GDN the output interacts
   with the residual stream which feeds into subsequent GDN decay/gate computations

4. **Only 128 calibration samples**: Insufficient to separate refusal-specific
   directions from capability-relevant directions in a 27B parameter model

## Architecture Background

Qwen3.5-27B uses a hybrid GatedDeltaNet architecture:
- **48 linear_attention layers** (GDN with conv1d + FLA)
- **16 full_attention layers** (standard multi-head attention)
- `decoder_sparse_step: 4` — every 4th layer is full attention

Weight matrix shapes (non-square):
- `o_proj`: [5120, 6144] (output projection for full attention)
- `out_proj`: [5120, 6144] (output projection for GDN)
- `down_proj`: [5120, 17408] (MLP down projection)

## Previous Findings (Still Valid)

### in_proj_ba not quantized by GPTQModel

GPTQModel 5.8.0 does not quantize `in_proj_ba` layers ([96, 5120]) — too few
output features. vLLM Patch 16 in `vllm_qwen35_patches.py` handles this by
replacing broken `QuantLinear` with `nn.Linear` post-load. This is correct
behavior and NOT the cause of garbage inference.

### vLLM version severely outdated

vLLM commit `e3eb146f7` (2026-02-28, pre-v0.17.0) is missing all Qwen3.5
fixes. Key missing PRs:
- #37448: Fix AttributeError in GDN layers with quantized models
- #36599: Triton autotuner warmup for GDN layers
- #36720: ROCm worker startup OOM fix

Minimum recommended version: **v0.18.0** (2026-03-20).

## Fix Plan

### Immediate: Re-download + GPTQ quantize (skip abliteration)

1. Delete the corrupted FP16 model from GPTQ PVC
2. Re-download the original `qwen35-27b-opus-distill` model (pre-abliteration)
3. GPTQ quantize directly (INT4, group_size=128, sym=true)
4. Test via GPTQModel direct inference first
5. If good, deploy via vLLM with existing patches

### Alternative: Use official Qwen/Qwen3.5-27B-GPTQ-Int4

Pre-quantized by Qwen team, no abliteration. Quick path to verify the full
GPTQ→vLLM pipeline works on gfx1100.

### Long-term: Fix abliteration for GDN architectures

- Skip GDN linear_attention layers (only abliterate full_attention layers)
- Increase calibration samples (128 → 512+)
- Add a refusal direction norm sanity check (reject if > 100)
- Validate model perplexity post-abliteration before proceeding to quantization

## Files

| File | Purpose |
|------|---------|
| `build/scripts/vllm_qwen35_patches.py` | Runtime patches for Qwen3.5 on vLLM |
| `deploy/debug/qwen35-gptq-direct-test.yaml` | GPTQ weight validation job |
| `deploy/debug/qwen35-gptq-transformers-test.yaml` | Direct GPTQModel inference test |
| `deploy/debug/qwen35-fp16-sanity-test.yaml` | FP16 abliterated model sanity test |
| `deploy/debug/kustomization.yaml` | Kustomize entry for debug manifests |
| `controllers/modelcache_abliteration.go` | Abliteration reconciler |
| `pkg/quantization/abliteration.go` | Abliteration job builder |
