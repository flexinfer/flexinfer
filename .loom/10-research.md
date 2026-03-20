# AWQ vLLM Context Length Optimization Research

## Current State

The `vllm-qwen-awq` deployment runs **Qwen3-14B-AWQ** on a single AMD RX 7900 XTX (24GB VRAM) with ROCm.

### Current vLLM Serve Arguments

From [vllm-qwen-awq.yaml](file:///Users/cblevins/workspace/platform/gitops/k3s/ai/legacy/vllm/vllm-qwen-awq.yaml#L192-L209):

| Parameter                  | Current Value | Notes                                      |
| -------------------------- | ------------- | ------------------------------------------ |
| `--max-model-len`          | 8192          | Model natively supports 32K (131K w/ YaRN) |
| `--gpu-memory-utilization` | 0.90          | Conservative; 0.92–0.95 possible           |
| `--max-num-seqs`           | 6             | Low concurrent request count               |
| `--max-num-batched-tokens` | 384           | **Extremely** conservative                 |
| `--block-size`             | 32            | Standard                                   |
| `--swap-space`             | 8             | 8GB CPU offload space                      |
| `--enforce-eager`          | yes           | Disables HIP graph capture                 |
| `--enable-chunked-prefill` | yes           | Good for ITL                               |
| `--kv-cache-dtype`         | auto          | Currently FP16; not FP8                    |
| `--quantization`           | awq           | INT4 weights via AWQ                       |

### Environment Variables (Container)

- `VLLM_USE_V1=0` — V0 engine (V1 is now default in vLLM ≥0.12)
- `VLLM_ATTENTION_BACKEND=ROCM_FLASH` — Flash Attention on ROCm
- `VLLM_USE_TRITON_FLASH_ATTN=1` — Triton FA for RDNA3
- `VLLM_USE_TRITON_AWQ=1` — Triton AWQ path (required for ROCm)
- `PYTORCH_HIP_ALLOC_CONF=expandable_segments:True,max_split_size_mb:128`
- Resource limits: 48Gi memory, 1× AMD GPU

### Model Specification — Qwen3-14B-AWQ

- **Native context**: 32,768 tokens
- **Extended context**: up to 131,072 with YaRN RoPE scaling
- **AWQ model size**: ~9.5 GB (INT4 weights)
- **Source**: `Qwen/Qwen3-14B-AWQ` on HuggingFace

---

## Optimization Levers Identified

### 1. FP8 KV Cache (`--kv-cache-dtype fp8`)

**Impact: ~2× KV cache capacity → nearly double context length**

- Quantizes the KV cache from FP16 to FP8 (E4M3 format), halving per-token KV memory.
- Supported on ROCm ≥ 6.2 with vLLM ≥ 0.16.0.
- Minimal accuracy degradation for most tasks; AMD has validated this on MI300X and consumer RDNA3.
- Can be combined with AWQ weight quantization (they operate on different tensors).
- **Risk**: Slight precision loss in long-range attention patterns; acceptable for chat/code tasks.

### 2. Increase `--max-model-len` (8192 → 16384 or 32768)

**Impact: Directly enables longer context windows**

- Current 8K is only 25% of the model's native 32K.
- With FP8 KV cache, 32K context should fit in 24GB with a 14B AWQ model.
- KV cache memory for 32K context at FP8: ~2.6 GB (vs ~5.2 GB at FP16).
- Model weights (AWQ INT4): ~9.5 GB. Activations/overhead: ~2–3 GB.
- **Total estimated**: 9.5 + 2.6 + 3 ≈ 15.1 GB at 32K/FP8, leaving ~8.9 GB headroom on 24GB.
- **Conservative target**: 16384 (doubles current) with room for batched sequences.

### 3. Increase `--gpu-memory-utilization` (0.90 → 0.92–0.95)

**Impact: 0.5–1.2 GB more KV cache space**

- 0.90 × 24GB = 21.6 GB available; 0.95 × 24GB = 22.8 GB.
- The extra 1.2GB can hold additional ~4K–8K tokens of KV cache at FP8.
- **Risk**: Too high can cause OOM under peak load; 0.92 is safe, 0.95 is aggressive.

### 4. Increase `--max-num-batched-tokens` (384 → 2048+)

**Impact: Dramatically better throughput and TTFT**

- 384 is far too conservative—this limits prefill to 384 tokens per scheduler iteration.
- vLLM recommends ≥ 2048 for reasonable TTFT; 8192+ for peak throughput.
- With chunked prefill enabled, this controls the prefill chunk size.
- **Recommended**: 2048 (balanced ITL/TTFT) or 4096 (throughput-oriented).

### 5. Enable `--enable-prefix-caching`

**Impact: Reuses KV cache for repeated prefixes (system prompts, multi-turn)**

- Very effective for chat workloads where system prompt is shared.
- Combines well with chunked prefill.
- **Risk**: Minimal; slight metadata overhead. Already proven in the vLLM optimized deployment.

### 6. `--enforce-eager` Removal

**Impact: Enable HIP graph capture for decode throughput**

- HIP graphs (CUDA graphs on AMD) reduce CPU-GPU kernel launch overhead.
- On ROCm/RDNA3, reports indicate this is generally stable with vLLM's PIECEWISE mode.
- **Risk**: Some models have had correctness issues on ROCm 7.0 with graph capture. Need to test.
- **Recommendation**: Test without `--enforce-eager` first; fall back if stability issues arise.

### 7. Reduce `--max-num-seqs` or Keep at 6

- With longer context, each sequence consumes more KV memory.
- At 32K context, 6 simultaneous sequences × 32K tokens = 192K total KV tokens.
- May need to reduce to 3–4 concurrent sequences if pushing to 32K context.
- At 16K, 6 sequences is feasible with FP8 KV.

### 8. Consider V1 Engine (`VLLM_USE_V1=1`)

**Impact: Better scheduler, prefix caching on by default, improved batching**

- V1 engine is the default since vLLM 0.12 and has significant perf improvements.
- Chunked prefill is enabled by default in V1.
- **Risk**: needs validation with AWQ + ROCm + Triton FA. Conversation `7864d209` noted V1 was being used for Qwen3, suggesting it may already work.

---

## Memory Budget Analysis (24GB VRAM)

| Component            | FP16 KV @ 8K | FP16 KV @ 16K | FP16 KV @ 32K   | FP8 KV @ 16K | FP8 KV @ 32K    |
| -------------------- | ------------ | ------------- | --------------- | ------------ | --------------- |
| Model weights (AWQ)  | 9.5 GB       | 9.5 GB        | 9.5 GB          | 9.5 GB       | 9.5 GB          |
| KV cache (per seq)   | ~0.65 GB     | ~1.3 GB       | ~2.6 GB         | ~0.65 GB     | ~1.3 GB         |
| KV cache (6 seqs)    | ~3.9 GB      | ~7.8 GB       | ~15.6 GB        | ~3.9 GB      | ~7.8 GB         |
| Activations/overhead | ~2.5 GB      | ~2.5 GB       | ~2.5 GB         | ~2.5 GB      | ~2.5 GB         |
| **Total**            | **~15.9 GB** | **~19.8 GB**  | **~27.6 GB** ❌ | **~15.9 GB** | **~19.8 GB** ✅ |

> [!IMPORTANT]
> With FP8 KV cache, 32K context × 6 sequences fits in ~19.8 GB (82% of 24GB). This leaves comfortable headroom at `--gpu-memory-utilization=0.90`.

---

## Comparison with Dolphin Llama3 Deployment

The [vllm-llama3-optimized.yaml](file:///Users/cblevins/workspace/platform/gitops/k3s/ai/vllm/vllm-llama3-optimized.yaml) deployment runs a non-quantized 8B model at:

- `--max-model-len=16384` (2× the AWQ deployment)
- `--gpu-memory-utilization=0.92`
- `--max-num-seqs=48`
- No `--enforce-eager`
- No `--max-num-batched-tokens` limit
- No chunked prefill

This shows the 7900 XTX can handle 16K+ context comfortably.

---

## Sources

- Deployment manifest: `platform/gitops/k3s/ai/legacy/vllm/vllm-qwen-awq.yaml`
- Dockerfile: `platform/gitops/Dockerfiles/Dockerfile.vllm-rocm-awq`
- Comparison: `platform/gitops/k3s/ai/vllm/vllm-llama3-optimized.yaml`
- vLLM FP8 KV cache docs: vllm.ai/docs
- AMD ROCm FP8 validation: amd.com/vllm-rocm
- Qwen3-14B-AWQ model card: huggingface.co/Qwen/Qwen3-14B-AWQ
