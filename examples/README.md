# FlexInfer Examples

Example ModelDeployment configurations for various backends and use cases.

## Serverless (Scale-to-Zero)

FlexInfer supports Knative-style serverless scaling, allowing models to scale down to zero replicas when idle and automatically scale up when requests arrive.

### Key Configuration Fields

| Field | Default | Description |
|-------|---------|-------------|
| `spec.minReplicas` | 0 | Minimum replicas (set to 0 for serverless) |
| `spec.idleTimeoutSeconds` | 300 | Time (seconds) before scaling to minReplicas |
| `spec.coldStartTimeoutSeconds` | 60 | Max wait time for model activation on cold start |

### Quick Start

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: my-model
spec:
  backend: ollama
  model: llama3:8b
  replicas: 1
  minReplicas: 0        # Enable scale-to-zero
  idleTimeoutSeconds: 300
```

### How It Works

1. **Scale Down**: After `idleTimeoutSeconds` of no requests, the controller scales replicas to `minReplicas`
2. **Scale Up**: When a request arrives at `replicas=0`, the proxy:
   - Queues the request
   - Triggers scale-up to 1 replica
   - Waits for the model to become ready (up to `coldStartTimeoutSeconds`)
   - Forwards the request once ready
3. **Request Buffering**: Multiple requests during cold start are queued and drained when ready

### Cold Start Times by Backend

| Backend | Typical Cold Start | Notes |
|---------|-------------------|-------|
| llama.cpp | 3-10s | Fastest cold start, GGUF format |
| Ollama | 5-15s | Model loading from disk |
| MLC-LLM | 5-20s | Faster with RAM cache (`storageStrategy: Memory`) |
| vLLM | 10-30s | CUDA compilation on first start |

### Best Practices

1. **Use RAM Cache for MLC-LLM**: Combine with `ModelCache` using `storageStrategy: Memory` for sub-5s cold starts
2. **Set Appropriate Timeouts**: Match `coldStartTimeoutSeconds` to your backend's startup time
3. **Consider Idle Timeout**: Balance cost savings vs cold start latency based on traffic patterns
4. **LiteLLM Integration**: Enable `litellm.enabled: true` for automatic request routing via LiteLLM proxy

## GPUGroup (Multi-Model GPU Sharing)

GPUGroup enables multiple models to share a single GPU with priority-based preemption and anti-thrashing protection. Perfect for homelabs where you want to run both text generation and image generation on the same GPU.

### Key Features

- **Priority-Based Preemption**: Higher priority models can preempt lower priority ones
- **Anti-Thrashing**: Prevents rapid model flip-flopping with configurable hysteresis
- **Demand Signaling**: Proxy signals queue depth to controller for smart scheduling
- **Auto RAM Caching**: Automatically create RAM caches for fast model swaps

### Quick Start

```yaml
# 1. Create GPUGroup
apiVersion: ai.flexinfer/v1alpha1
kind: GPUGroup
metadata:
  name: homelab-gpu
spec:
  models:
    - name: textgen-model
      priority: 100       # Higher = more important
    - name: imagegen-model
      priority: 50
  scalingPolicy:
    strategy: Exclusive   # One model at a time
  antiThrashing:
    enabled: true
    minimumRunDurationSeconds: 30
    cooldownAfterPreemptionSeconds: 60
    requestQueueThreshold: 3
  autoCacheModels: true

---
# 2. Link ModelDeployments to GPUGroup
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: textgen-model
spec:
  backend: mlc-llm
  model: Qwen3-8B-q4f16_1-MLC
  gpuGroupRef: homelab-gpu  # Link to GPUGroup
  priority: 100             # Match GPUGroup member priority
  minReplicas: 0
```

### How Model Swapping Works

1. **Request Arrives** for inactive model
2. **Proxy Queues** request and signals demand via GPUGroup annotations
3. **Controller Evaluates** anti-thrashing rules:
   - Active model has run for `minimumRunDurationSeconds`
   - Queue threshold met (`requestQueueThreshold`)
   - Demand persisted for `hysteresisWindowSeconds`
4. **Graceful Drain** of current model (wait for in-flight requests)
5. **Scale Down** current model to 0
6. **Scale Up** requested model to 1
7. **Drain Queue** and serve pending requests

### Anti-Thrashing Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `minimumRunDurationSeconds` | 30 | Model must run this long before swap |
| `cooldownAfterPreemptionSeconds` | 60 | Can't swap back for this duration |
| `requestQueueThreshold` | 3 | Need this many queued requests to swap |
| `hysteresisWindowSeconds` | 10 | Demand must persist for this duration |

### Supported Backends

GPUGroup works with all inference backends:
- **Text Generation**: ollama, vllm, llamacpp, mlc-llm
- **Image Generation**: comfyui, vllm-omni, diffusers

## Example Files

| File | Description |
|------|-------------|
| `gpugroup-multi-model.yaml` | Multi-model GPU sharing with textgen + imagegen |
| `serverless-multi-backend.yaml` | Complete serverless examples for all backends |
| `ram-cached-models.yaml` | RAM-cached models with serverless for fast cold starts |
| `llama3-8b.yaml` | Basic Ollama deployment |
| `phi3-mini-nvidia.yaml` | Ollama on NVIDIA GPU with benchmarking |

## Backend-Specific Configuration

### Ollama

Simplest setup. Good for quick deployments and CPU fallback.

```yaml
spec:
  backend: ollama
  model: llama3:8b
```

### vLLM

High-throughput serving with PagedAttention. Best for production with NVIDIA GPUs.

```yaml
spec:
  backend: vllm
  model: Qwen/Qwen2.5-7B-Instruct
  vllm:
    dtype: auto
    maxModelLen: 32768
    gpuMemoryUtilization: "0.85"
```

### llama.cpp

Fast inference with GGUF models. Works with both NVIDIA and AMD GPUs.

```yaml
spec:
  backend: llamacpp
  model: /models/model.gguf
  llamacpp:
    contextSize: 8192
    nGPULayers: 999  # Offload all layers to GPU
    flashAttention: true
```

### MLC-LLM

Optimized for AMD GPUs (ROCm) and NVIDIA. Best with RAM caching.

```yaml
spec:
  backend: mlc-llm
  model: Qwen3-8B-q4f16_1-MLC
  modelCacheRef: my-ram-cache  # Use ModelCache for fast cold starts
  mlcllm:
    mode: server
    overrides:
      maxNumSequence: 4
```

## GPU Resources

### NVIDIA GPUs
```yaml
resources:
  limits:
    nvidia.com/gpu: "1"
```

### AMD GPUs
```yaml
resources:
  limits:
    amd.com/gpu: "1"
```

## Monitoring Serverless Deployments

```bash
# Watch deployment status
kubectl get modeldeployments -w

# Check proxy logs for scale events
kubectl logs -f deployment/flexinfer-proxy -n flexinfer-system

# View serverless metrics
curl http://flexinfer-proxy:9090/metrics | grep -E "(scale_ups|queue)"
```

## Troubleshooting

### Cold Start Timeout

If requests timeout during cold start:
1. Increase `coldStartTimeoutSeconds`
2. Check pod events: `kubectl describe pod <pod-name>`
3. Verify GPU resources are available

### Model Not Scaling Down

If model doesn't scale to zero:
1. Check `lastAccessTime`: `kubectl get modeldeployment <name> -o yaml | grep lastAccessTime`
2. Ensure no requests are still being made
3. Verify `idleTimeoutSeconds` has elapsed

### Scale-Up Race Condition

The controller uses optimistic concurrency to prevent race conditions between:
- Proxy updating `lastAccessTime` and `spec.replicas`
- Controller checking idle status

If you see unexpected scale-down during requests, ensure you're running the latest controller version.
