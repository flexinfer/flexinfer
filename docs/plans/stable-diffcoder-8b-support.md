# Model Support: Stable-DiffCoder-8B-Instruct GGUF

This document provides deployment guidance for the Stable-DiffCoder-8B-Instruct model in GGUF format on FlexInfer.

## Model Overview

| Property | Value |
|----------|-------|
| Model | [mradermacher/Stable-DiffCoder-8B-Instruct-i1-GGUF](https://huggingface.co/mradermacher/Stable-DiffCoder-8B-Instruct-i1-GGUF) |
| Base Model | ByteDance-Seed/Stable-DiffCoder-8B-Instruct |
| Parameters | 8B |
| Architecture | LLaMA |
| License | MIT |
| Format | GGUF (imatrix weighted quants) |
| Use Case | Code generation |

### Available Quantizations

| Type | Size | Quality Notes |
|------|------|---------------|
| IQ1_S/M | 2.1-2.3 GB | Extreme compression |
| IQ2_XXS/XS/S/M | 2.5-3.1 GB | Very aggressive |
| Q2_K/Q2_K_S | 3.1-3.3 GB | Minimal |
| IQ3_XXS/XS/S/M | 3.4-3.9 GB | Low |
| Q3_K_S/M/L | 3.8-4.5 GB | Acceptable |
| **IQ4_XS** | **4.6 GB** | Good (recommended IQ) |
| **Q4_K_S** | **4.8 GB** | **Optimal size/speed/quality** |
| **Q4_K_M** | **5.1 GB** | **Fast, recommended** |
| Q5_K_S/M | 5.8-5.9 GB | Better quality |
| Q6_K | 6.8 GB | High quality |

---

## Backend Compatibility

### Summary Matrix

| Backend | GGUF Support | GPU | CPU | Recommendation |
|---------|--------------|-----|-----|----------------|
| **llamacpp** | Native | NVIDIA, AMD | Yes | **Use this** |
| ollama | No | N/A | N/A | Convert to Ollama format |
| vllm | No | N/A | N/A | Use HF safetensors |
| mlc-llm | No | N/A | N/A | Use MLC format |

### Why llamacpp?

The `llamacpp` backend is the only option for GGUF models:

- **Native GGUF support**: Designed by ggerganov specifically for this format
- **All quantization types**: Supports Q2-Q8 and IQ variants
- **Multi-vendor GPU**: Works on NVIDIA (CUDA), AMD (ROCm), and CPU-only
- **Direct loading**: No conversion needed

**Container Images**:

| GPU Vendor | Image |
|------------|-------|
| NVIDIA | `ghcr.io/ggerganov/llama.cpp:server-cuda` |
| AMD | `ghcr.io/ggerganov/llama.cpp:server-rocm` |
| CPU | `ghcr.io/ggerganov/llama.cpp:server` |

---

## VRAM/RAM Requirements

| Quantization | File Size | Est. VRAM (GPU) | Est. RAM (CPU) |
|--------------|-----------|-----------------|----------------|
| IQ4_XS | 4.6 GB | ~5.5 GB | ~7 GB |
| Q4_K_S | 4.8 GB | ~5.5 GB | ~7 GB |
| Q4_K_M | 5.1 GB | ~6 GB | ~8 GB |
| Q5_K_M | 5.9 GB | ~7 GB | ~9 GB |
| Q6_K | 6.8 GB | ~8 GB | ~10 GB |

*Note: Add ~1-2 GB for KV cache depending on context size.*

---

## Deployment Examples

### Example 1: NVIDIA GPU

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: stable-diffcoder
spec:
  backend: llamacpp
  source: HF://mradermacher/Stable-DiffCoder-8B-Instruct-i1-GGUF
  gpu:
    vendor: nvidia
    count: 1
    vramEstimateMB: 6000
  config:
    contextSize: 4096
    nGPULayers: 999
    batchSize: 512
    flashAttention: true
  serverless:
    idleTimeout: 5m
  serviceLabels:
    - code
    - textgen
```

### Example 2: AMD GPU (ROCm)

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: stable-diffcoder-amd
spec:
  backend: llamacpp
  source: HF://mradermacher/Stable-DiffCoder-8B-Instruct-i1-GGUF
  nodeSelector:
    gpu.amd.com/gpu-architecture: gfx1100
  gpu:
    vendor: amd
    count: 1
    vramEstimateMB: 6000
  config:
    contextSize: 4096
    nGPULayers: 999
    batchSize: 512
  serverless:
    idleTimeout: 5m
```

### Example 3: CPU-Only

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: stable-diffcoder-cpu
spec:
  backend: llamacpp
  source: HF://mradermacher/Stable-DiffCoder-8B-Instruct-i1-GGUF
  gpu:
    vendor: cpu
  resources:
    requests:
      cpu: "8"
      memory: 12Gi
    limits:
      cpu: "16"
      memory: 16Gi
  config:
    contextSize: 2048
    threads: 8
  serverless:
    idleTimeout: 10m
    coldStartTimeout: 180s
```

### Example 4: Shared GPU Pool

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: stable-diffcoder-shared
spec:
  backend: llamacpp
  source: HF://mradermacher/Stable-DiffCoder-8B-Instruct-i1-GGUF
  gpu:
    vendor: nvidia
    shared: code-models
    priority: 100
    vramEstimateMB: 6000
  config:
    contextSize: 4096
    nGPULayers: 999
    batchSize: 512
  serverless:
    idleTimeout: 5m
  litellm:
    servedModelName: stable-diffcoder
    aliases:
      - diffcoder
      - code-8b
```

---

## Configuration Reference

### llamacpp Config Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `contextSize` | int | 2048 | Context window in tokens |
| `nGPULayers` | int | 999 | GPU layer offload (999 = all, 0 = CPU only) |
| `batchSize` | int | 512 | Batch size for prompt processing |
| `threads` | int | 4 | CPU threads (CPU inference) |
| `flashAttention` | bool | false | Enable flash attention (GPU only) |
| `parallel` | int | 1 | Concurrent request handling |
| `cacheTypeK` | string | f16 | KV cache quantization (q4, q8, f16) |
| `cacheTypeV` | string | f16 | KV cache quantization (q4, q8, f16) |
| `ubatchSize` | int | 512 | Micro-batch size for throughput |
| `chatTemplate` | string | - | Chat template (chatml, llama2, etc.) |
| `metrics` | bool | false | Enable Prometheus metrics endpoint |

### GPU Configuration

| Field | Description |
|-------|-------------|
| `gpu.vendor` | `nvidia`, `amd`, or `cpu` |
| `gpu.count` | Number of GPUs (default: 1) |
| `gpu.shared` | Shared pool name for time-sharing |
| `gpu.priority` | Priority within shared pool (0-1000) |
| `gpu.vramEstimateMB` | VRAM estimate for scheduling |

---

## Quantization Selection Guide

| Use Case | Recommended Quant | Notes |
|----------|-------------------|-------|
| Best quality | Q6_K | Highest fidelity, ~8 GB VRAM |
| Balanced | Q5_K_M | Good quality, ~7 GB VRAM |
| Speed/size | **Q4_K_M** | **Best tradeoff, ~6 GB VRAM** |
| Memory constrained | Q4_K_S / IQ4_XS | Smaller, slight quality loss |
| Extreme compression | Q3_K_M or lower | Quality degradation expected |

For code generation, **Q4_K_M is recommended** as it provides a good balance of quality and performance.

---

## Performance Expectations

### GPU Inference (Q4_K_M)

| GPU | Est. Tokens/sec | Context | Notes |
|-----|-----------------|---------|-------|
| RTX 4090 | 80-120 | 4096 | Fastest consumer GPU |
| RTX 3090 | 50-80 | 4096 | Good performance |
| RX 7900 XTX | 60-90 | 4096 | Best AMD consumer |
| RTX 3060 12GB | 30-50 | 4096 | Entry-level adequate |

### CPU Inference (Q4_K_M)

| CPU | Est. Tokens/sec | Threads | Notes |
|-----|-----------------|---------|-------|
| AMD EPYC 7713 | 8-12 | 32 | Server-class |
| Intel Xeon Platinum | 6-10 | 32 | Server-class |
| Apple M2 Pro | 15-20 | 10 | Excellent efficiency |
| AMD Ryzen 9 5950X | 5-8 | 16 | Consumer high-end |

---

## Troubleshooting

### Model fails to load

1. Verify the source URI points to a specific GGUF file:
   ```yaml
   source: HF://mradermacher/Stable-DiffCoder-8B-Instruct-i1-GGUF/Stable-DiffCoder-8B-Instruct.Q4_K_M.gguf
   ```

2. Check cache status:
   ```bash
   kubectl get model stable-diffcoder -o jsonpath='{.status.cache}'
   ```

### Out of memory

1. Reduce `contextSize` (e.g., 2048 instead of 4096)
2. Use smaller quantization (Q4_K_S instead of Q5_K_M)
3. Enable KV cache quantization:
   ```yaml
   config:
     cacheTypeK: q8
     cacheTypeV: q8
   ```

### Slow inference on GPU

1. Verify all layers are on GPU:
   ```yaml
   config:
     nGPULayers: 999
   ```

2. Enable flash attention:
   ```yaml
   config:
     flashAttention: true
   ```

3. Check GPU utilization:
   ```bash
   nvidia-smi  # NVIDIA
   rocm-smi    # AMD
   ```

---

## Related Documentation

- [CPU-Only Backend Guide](../user/backends-cpu.md)
- [Models (v1alpha2)](../user/models-v1alpha2.md)
- [Configuration Reference](../CONFIGURATION.md)
