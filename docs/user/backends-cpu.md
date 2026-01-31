---
title: CPU-Only Backend Guide
description: Running inference on CPU without GPU acceleration.
---

# CPU-Only Backend Guide

FlexInfer supports CPU-only inference for environments without GPUs or for cost optimization. This guide covers performance expectations, hardware recommendations, and configuration best practices.

## When to Use CPU Inference

CPU inference is useful for:

- **Nodes without GPUs**: Development machines, CI/CD runners, edge devices
- **Cost optimization**: When GPU nodes are expensive and latency is acceptable
- **Small models**: Embedding models, TinyLlama, small classifiers
- **Testing/development**: Validating pipelines before GPU deployment
- **Overflow capacity**: Handle burst traffic when GPU capacity is exhausted

## Performance Expectations

CPU inference is **10-50x slower** than GPU inference. Actual performance depends on:

| Factor | Impact |
|--------|--------|
| Model size | Larger models = slower inference |
| Quantization | Q4_K_M is 2-3x faster than FP16 |
| CPU cores | More cores = higher throughput (to a point) |
| AVX support | AVX512 provides 15-30% speedup |
| Memory bandwidth | DDR5 helps with larger models |
| Batch size | Larger batches amortize overhead |

### Typical Performance (Llama 7B Q4_K_M)

| CPU | Tokens/sec | Cores Used |
|-----|-----------|------------|
| AMD EPYC 7713 (64C) | 8-12 | 32 |
| Intel Xeon Platinum 8375C | 6-10 | 32 |
| Apple M2 Pro | 15-20 | 10 |
| AMD Ryzen 9 5950X | 5-8 | 16 |
| Intel i9-13900K | 6-9 | 16 |

Compare to GPU inference:
- NVIDIA RTX 4090: 80-120 tok/s
- AMD RX 7900 XTX: 60-90 tok/s

## Supported Backends

Only **llama.cpp** supports efficient CPU-only inference. Other backends (vLLM, MLC-LLM) require GPU acceleration.

### Why llama.cpp for CPU?

- Highly optimized AVX/AVX2/AVX512 kernels
- GGUF quantization designed for CPU
- Active community with continuous optimizations
- Low memory footprint with quantized models

## Hardware Recommendations

### Minimum Requirements

| Model Size | RAM | CPU Cores | Notes |
|------------|-----|-----------|-------|
| 1-3B (TinyLlama) | 4 GB | 4 | Good for testing |
| 7B | 8-16 GB | 8 | Quantized (Q4_K_M) |
| 13B | 16-32 GB | 16 | Quantized required |
| 70B | 64+ GB | 32+ | Not recommended for CPU |

### CPU Features

Enable these for best performance:

```bash
# Check CPU features
cat /proc/cpuinfo | grep -E "avx|avx2|avx512"

# Example output for a well-supported CPU:
# flags: ... avx avx2 avx512f avx512dq avx512cd avx512bw avx512vl ...
```

**Priority order:**
1. AVX512 (best)
2. AVX2 (good)
3. AVX (minimum)
4. SSE4.2 (fallback, very slow)

### Thread Configuration

Set `threads` to **physical cores** (not hyperthreads):

```bash
# Linux: Get physical core count
nproc --all  # Total logical CPUs
lscpu | grep "Core(s) per socket"  # Physical cores per socket

# Example: 16 logical CPUs with hyperthreading = 8 physical cores
# Set threads: 8
```

**Guidelines:**
- Set threads to 50-75% of physical cores for single model
- Leave headroom for system processes
- For multiple models, divide cores proportionally

## Model Selection

### Recommended Quantizations

For CPU inference, use GGUF models with these quantizations:

| Quantization | Size vs FP16 | Quality | Speed | Recommended For |
|--------------|-------------|---------|-------|-----------------|
| Q4_K_M | 25% | Good | Fast | General use |
| Q5_K_M | 35% | Better | Medium | Quality-sensitive |
| Q6_K | 50% | Best | Slow | When quality matters |
| Q8_0 | 50% | Best | Slowest | Reference/comparison |
| Q2_K | 15% | Poor | Fastest | Not recommended |

### Model Sources

Download GGUF models from:
- [HuggingFace](https://huggingface.co/models?search=gguf)
- [TheBloke's collection](https://huggingface.co/TheBloke)

### Recommended CPU-Friendly Models

| Model | Parameters | Q4_K_M Size | Use Case |
|-------|-----------|-------------|----------|
| TinyLlama-1.1B | 1.1B | 0.7 GB | Testing, simple tasks |
| Phi-3-mini | 3.8B | 2.3 GB | Balanced performance |
| Llama-2-7B | 7B | 4.1 GB | General LLM |
| Mistral-7B | 7B | 4.1 GB | Better quality |
| Gemma-2B | 2B | 1.5 GB | Fast responses |
| Nomic-embed-text | 137M | 0.3 GB | Embeddings |

## Configuration

### Basic CPU Deployment

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: llama-7b-cpu
spec:
  backend: llamacpp
  model: llama-2-7b-chat.Q4_K_M.gguf
  modelCacheRef: llama-7b-gguf

  # Target CPU-only nodes
  nodeSelector:
    flexinfer.ai/gpu.vendor: ""

  llamacpp:
    threads: 8           # Physical cores
    contextSize: 4096    # Reduce for lower memory
    batchSize: 512       # Prompt processing batch
    nGPULayers: 0        # Explicit CPU-only
    flashAttention: false # GPU-only feature

  resources:
    limits:
      cpu: "8"
      memory: 16Gi
    requests:
      cpu: "4"
      memory: 8Gi

  # Longer timeouts for CPU
  minReplicas: 0
  idleTimeoutSeconds: 600
  coldStartTimeoutSeconds: 180
```

### Key Parameters

| Parameter | Description | CPU Recommendation |
|-----------|-------------|-------------------|
| `threads` | Inference threads | Physical cores (not hyperthreads) |
| `contextSize` | Max context length | 2048-4096 (lower = less memory) |
| `batchSize` | Prompt batch size | 256-512 |
| `nGPULayers` | GPU layer offload | 0 (CPU-only) |
| `flashAttention` | Flash attention | false (GPU-only) |

### Memory Calculation

Estimate memory requirements:

```
Memory = Model Size + Context Buffer + Overhead

Context Buffer = contextSize * 4 * layers * heads * headDim / 1024^3 GB
Overhead = ~20% of model size

Example (Llama-7B Q4_K_M, 4096 context):
- Model: 4.1 GB
- Context: ~0.5 GB
- Overhead: ~0.8 GB
- Total: ~5.4 GB (request 8 GB for safety)
```

## Examples

### TinyLlama (Fastest CPU Model)

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: tinyllama-cpu
spec:
  backend: llamacpp
  model: tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf
  modelCacheRef: tinyllama-gguf

  llamacpp:
    threads: 4
    contextSize: 2048
    batchSize: 256
    nGPULayers: 0

  resources:
    limits:
      cpu: "4"
      memory: 4Gi
    requests:
      cpu: "2"
      memory: 2Gi

  minReplicas: 0
  idleTimeoutSeconds: 300
  coldStartTimeoutSeconds: 60
```

### Embedding Model (CPU-Optimized)

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: nomic-embed-cpu
spec:
  backend: llamacpp
  model: nomic-embed-text-v1.5.Q8_0.gguf
  modelCacheRef: nomic-embed-gguf

  llamacpp:
    threads: 4
    contextSize: 8192   # Embeddings need longer context
    batchSize: 512
    nGPULayers: 0
    # Enable embedding mode
    embedding: true

  resources:
    limits:
      cpu: "4"
      memory: 2Gi
    requests:
      cpu: "2"
      memory: 1Gi

  minReplicas: 1        # Keep warm for low latency
  idleTimeoutSeconds: 0 # Never scale down
```

### Production 7B Model

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: mistral-7b-cpu
spec:
  backend: llamacpp
  model: mistral-7b-instruct-v0.2.Q4_K_M.gguf
  modelCacheRef: mistral-7b-gguf

  nodeSelector:
    # Target high-core-count nodes
    node.kubernetes.io/instance-type: c6a.8xlarge

  llamacpp:
    threads: 16          # 32 vCPUs = 16 physical
    contextSize: 8192
    batchSize: 512
    nGPULayers: 0
    parallel: 2          # Handle 2 concurrent requests

  resources:
    limits:
      cpu: "16"
      memory: 32Gi
    requests:
      cpu: "8"
      memory: 16Gi

  minReplicas: 0
  replicas: 1
  idleTimeoutSeconds: 900   # 15 min (slower cold start)
  coldStartTimeoutSeconds: 300
```

## ModelCache for CPU Models

Create a ModelCache to download and store GGUF models:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: llama-7b-gguf
spec:
  # HuggingFace repository
  source:
    type: huggingface
    repository: TheBloke/Llama-2-7B-Chat-GGUF
    filename: llama-2-7b-chat.Q4_K_M.gguf

  # Storage configuration
  storageStrategy: SharedPVC
  storageClassName: longhorn
  storageSize: 10Gi

  # Mount as read-only
  accessModes:
    - ReadOnlyMany
```

## Node Selection

### Label CPU-Only Nodes

The FlexInfer agent automatically labels nodes without GPUs:

```bash
# Check node labels
kubectl get nodes -o custom-columns=\
'NAME:.metadata.name,GPU:.metadata.labels.flexinfer\.ai/gpu\.vendor'
```

Nodes without GPUs will have an empty `flexinfer.ai/gpu.vendor` label.

### Manual Node Selection

For specific CPU node targeting:

```yaml
spec:
  nodeSelector:
    # Target nodes without GPUs
    flexinfer.ai/gpu.vendor: ""

    # Or target specific node types
    node.kubernetes.io/instance-type: c6a.8xlarge
```

### Tolerations for CPU Nodes

If CPU nodes have taints:

```yaml
spec:
  tolerations:
    - key: "dedicated"
      operator: "Equal"
      value: "cpu-inference"
      effect: "NoSchedule"
```

## Monitoring

### Key Metrics

Monitor CPU inference with these Prometheus metrics:

```promql
# Tokens per second (should be 5-20 for 7B model)
flexinfer_tokens_per_second{model="llama-7b-cpu"}

# Request latency (expect 10-60s for full response)
histogram_quantile(0.95, rate(flexinfer_request_duration_seconds_bucket[5m]))

# CPU utilization
container_cpu_usage_seconds_total{pod=~"llama-7b-cpu.*"}
```

### Alerts

```yaml
# Example alert for slow CPU inference
- alert: CPUInferenceSlow
  expr: flexinfer_tokens_per_second{model=~".*-cpu"} < 3
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: CPU inference is very slow
    description: "{{ $labels.model }} generating < 3 tok/s"
```

## Troubleshooting

### Model loads but inference is extremely slow

1. Check thread count matches physical cores
2. Verify AVX support: `cat /proc/cpuinfo | grep avx`
3. Reduce `contextSize` if memory-bound
4. Use more aggressive quantization (Q4_K_M vs Q8_0)

### Out of memory errors

1. Reduce `contextSize`
2. Use smaller quantization (Q4_K_M instead of Q6_K)
3. Increase pod memory limits
4. Use smaller model

### Model fails to start

1. Check ModelCache is Ready: `kubectl get modelcache`
2. Verify GGUF file exists in cache
3. Check pod logs: `kubectl logs <pod>`
4. Ensure llama.cpp image supports CPU-only mode

### Requests timeout during cold start

1. Increase `coldStartTimeoutSeconds` (CPU loads slower)
2. Consider keeping `minReplicas: 1` for latency-sensitive use
3. Pre-warm model before traffic

## Best Practices

1. **Use quantized models**: Q4_K_M provides best speed/quality tradeoff
2. **Right-size threads**: Match physical cores, not logical CPUs
3. **Reduce context for speed**: 2048-4096 is often sufficient
4. **Keep models warm**: Set `minReplicas: 1` for latency-sensitive workloads
5. **Use fast storage**: NVMe for ModelCache reduces cold start time
6. **Monitor memory**: Set requests/limits based on actual usage
7. **Consider embedding models**: They run well on CPU
8. **Test quantizations**: Benchmark Q4_K_M vs Q5_K_M for your use case
