# Model CRDs for Deployment

FlexInfer uses Custom Resource Definitions (CRDs) to manage model deployments declaratively. This is the detailed CRD reference; for the quick-start overview, see [AGENTS.md](../../AGENTS.md).

## ModelDeployment CRD

The `ModelDeployment` CRD is the primary way to deploy inference workloads:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-8b-abliterated
  namespace: flexinfer-system
  labels:
    app: qwen3-abliterated
    backend: mlc-llm
    gpu-vendor: amd
spec:
  backend: mlc-llm           # Backend: mlc-llm, ollama, vllm, llama-cpp
  model: Qwen3-8B-abliterated-q4f32_1-MLC  # Model name or path
  replicas: 1

  # Backend-specific configuration (for MLC-LLM)
  mlcllm:
    mode: server             # local, interactive, server
    modelLibPath: /models/Qwen3-8B-abliterated-q4f32_1-MLC/lib_rocm_gfx1100.so
    gpuMemoryBytes: 23000000000
    jitPolicy: "OFF"         # ON, OFF, REDO, READONLY
    overrides:
      prefillChunkSize: 512
      maxTotalSeqLength: 32768

  # Resource requests/limits
  resources:
    requests:
      amd.com/gpu: "1"
      cpu: "4"
      memory: 16Gi
    limits:
      amd.com/gpu: "1"
      cpu: "8"
      memory: 24Gi

  # Node scheduling
  nodeSelector:
    kubernetes.io/hostname: cblevins-5930k  # Target specific node

  # Health checks
  healthCheck:
    initialDelaySeconds: 120
    periodSeconds: 30
    timeoutSeconds: 10
    failureThreshold: 3
```

## ModelCache CRD

The `ModelCache` CRD manages model storage and caching:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: qwen3-8b-abliterated-mlc
  namespace: flexinfer-system
spec:
  storageStrategy: SharedPVC  # SharedPVC, NodeLocal, Memory
  existingClaimName: mlc-models-nfs
  modelPath: Qwen3-8B-abliterated-q4f32_1-MLC
  source: huggingface://huihui-ai/Qwen3-8B-abliterated
  storageSize: 20Gi
```

### Storage Strategies

| Strategy | Location | Switch Time | Use Case |
|----------|----------|-------------|----------|
| `SharedPVC` | NFS/RWX PVC | ~4-5s | Shared models across nodes |
| `NodeLocal` | `/var/lib/flexinfer/models` | ~4-5s | Per-node disk cache |
| `Memory` | `/dev/shm/flexinfer` | **~2-3s** | RAM cache for fast switching |

## RAM-Cached Models (Memory Strategy)

The `Memory` storage strategy uses `/dev/shm` (tmpfs) to cache models in RAM, providing ~40-50% faster cold starts compared to NVMe-based loading.

### Prerequisites

```bash
# Check /dev/shm size on GPU node (default: 50% of RAM)
ssh gpu-node-1 "df -h /dev/shm"
# Example: tmpfs 32G 0 32G 0% /dev/shm

# Optionally resize if needed
ssh gpu-node-1 "sudo mount -o remount,size=40G /dev/shm"
```

### Example: RAM-Cached Multi-Model Setup

```yaml
# ModelCache with RAM storage
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: qwen3-8b-ram
spec:
  source: HF://mlc-ai/Qwen3-8B-abliterated-q4f16_1-MLC
  storageStrategy: Memory
  nodeSelector:
    kubernetes.io/hostname: gpu-node-1
---
# ModelDeployment using RAM cache
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-8b-chat
spec:
  backend: mlc-llm
  model: Qwen3-8B-abliterated-q4f16_1-MLC
  modelCacheRef: qwen3-8b-ram
  replicas: 1
  minReplicas: 0  # Enable scale-to-zero
  nodeSelector:
    kubernetes.io/hostname: gpu-node-1
```

### RAM Cache Benefits

| Benefit | Description |
|---------|-------------|
| **Faster cold start** | ~2-3s from RAM vs ~4-5s from NVMe |
| **Full VRAM per model** | 100% GPU memory available (no sharing) |
| **More models cached** | RAM cheaper than VRAM; cache 5+ models |
| **Serverless ready** | Works with scale-to-zero for on-demand activation |

### Expected Performance

| Action | Time | Notes |
|--------|------|-------|
| First cold start (NFS → RAM) | ~30s | One-time cache population |
| Subsequent cold start (RAM → VRAM) | ~2-3s | Fast RAM-based loading |
| Model switch (scale down A, up B) | ~3-4s | Concurrent scale operations |
| Hot request (already in VRAM) | <100ms | No loading needed |

### Checking Cache Status

```bash
# View all model caches and their strategies
flexinfer cache status

# Example output:
# NAME           STRATEGY   PATH                             READY  SOURCE
# qwen3-8b-ram   Memory     /dev/shm/flexinfer/qwen3-8b-ram  Ready  HF://mlc-ai/...
# qwen3-3b-ram   Memory     /dev/shm/flexinfer/qwen3-3b-ram  Ready  HF://mlc-ai/...
```

## Scheduling to AMD 7900XTX Nodes

The cluster has two AMD 7900XTX GPU nodes:
- `cblevins-5930k` - Intel i7-5930K + 7900XTX
- `cblevins-7900xtx` - AMD Zen4 + 7900XTX

### Targeting a Specific Node

```yaml
spec:
  nodeSelector:
    kubernetes.io/hostname: cblevins-5930k
```

### Targeting Any AMD GPU Node

```yaml
spec:
  nodeSelector:
    flexinfer.ai/gpu.vendor: AMD
```

### Example: Split Models Across Both 7900XTX Nodes

Deploy 8B on cblevins-5930k:
```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-8b-abliterated
spec:
  backend: mlc-llm
  model: Qwen3-8B-abliterated-q4f32_1-MLC
  nodeSelector:
    kubernetes.io/hostname: cblevins-5930k
  mlcllm:
    mode: server
    modelLibPath: /models/lib_rocm_gfx1100.so
```

Deploy 32B on cblevins-7900xtx:
```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-32b
spec:
  backend: mlc-llm
  model: Qwen3-32B-q4f16_1-MLC
  nodeSelector:
    kubernetes.io/hostname: cblevins-7900xtx
  mlcllm:
    mode: local
```

## MLC-LLM Backend Configuration

### Mode Selection

| Mode | Max Batch | KV Cache | Use Case |
|------|-----------|----------|----------|
| `local` | 4 | ~8k tokens | Low memory, interactive use |
| `interactive` | 1 | Full context | Single user, maximum context |
| `server` | 128 (configurable) | Max possible | High throughput, multiple users |

### Memory Optimization (Server Mode)

In server mode, MLC-LLM pre-allocates KV cache based on batch size and context length. Use the `overrides` section to control memory usage:

| Override | Purpose | Example |
|----------|---------|---------|
| `maxNumSequence` | Max concurrent requests (batch size) | `2` for low concurrency |
| `maxTotalSeqLength` | Total KV cache tokens | `131072` for 128k context |
| `gpuMemoryUtilization` | Fraction of VRAM to use (0.0-1.0) | `"0.85"` for 85% |
| `prefillChunkSize` | Prefill attention chunk size | `2048` for throughput |

**Memory Formula (approximate):**
```
Total VRAM = Model Weights + KV Cache + Temp Buffers

KV Cache ≈ max_num_sequence × max_total_seq_length × ~55 bytes/token (for Qwen 7B)

Example: 2 sequences × 131072 tokens × 55 bytes = ~14.4GB KV cache
```

**Example: 7B Model with 128k Context on 24GB GPU**
```yaml
mlcllm:
  mode: server
  modelLibPath: /models/Qwen2.5-7B-abliterated-v2-q4f32_1-MLC/lib_rocm_gfx1100.so
  overrides:
    maxNumSequence: 2           # 2 concurrent requests
    maxTotalSeqLength: 131072   # 128k total context
    prefillChunkSize: 2048      # Larger chunks for throughput
    gpuMemoryUtilization: "0.85" # Use 85% of 24GB = ~20GB
```

This configuration yields ~20GB VRAM usage (85% of 24GB), leaving headroom for stability.

### Quantization & TVM Bug Workaround

**Important**: Use `q4f32_1` quantization (NOT `q4f16`) for Qwen3 models on ROCm to avoid TVM segfault bug.

See: https://github.com/mlc-ai/mlc-llm/issues/3283

### Pre-compiled vs JIT Compilation

For production, always use pre-compiled model libraries:

```yaml
mlcllm:
  modelLibPath: /models/Qwen3-8B-abliterated-q4f32_1-MLC/lib_rocm_gfx1100.so
  jitPolicy: "OFF"
```

JIT compilation is useful for development but adds significant startup time (2-5 minutes).

## Common Operations

### View all model deployments
```bash
kubectl get modeldeployment -n flexinfer-system
```

### Check model cache status
```bash
kubectl get modelcache -n flexinfer-system
```

### Scale a deployment
```bash
kubectl scale deployment qwen3-8b-abliterated -n flexinfer-system --replicas=2
```

### Update node selector
```bash
kubectl patch modeldeployment qwen3-8b-abliterated -n flexinfer-system \
  --type=merge -p='{"spec":{"nodeSelector":{"kubernetes.io/hostname":"cblevins-7900xtx"}}}'
```

### Check pod placement
```bash
kubectl get pods -n flexinfer-system -o wide | grep qwen
```

---

## Benchmark Results (AMD 7900XTX)

Tested January 2026 on AMD Radeon RX 7900 XTX (24GB VRAM) nodes.

| Model | Quantization | Context | Tokens/sec | VRAM Usage |
|-------|--------------|---------|------------|------------|
| Qwen3-8B-Abliterated | q4f32_1 | 32k | **107 tok/s** | ~12GB |
| Qwen3-32B | q4f16_1 | 32k | **37 tok/s** | ~22GB |

### Benchmark Methodology

Run from within cluster using Python for accurate timing:

```bash
kubectl run bench --image=python:3.11-alpine --restart=Never -- sleep 600
kubectl exec bench -- python3 -c '
import urllib.request, json, time

url = "http://MODEL-SERVICE.flexinfer-system.svc.cluster.local:8000/v1/chat/completions"
data = json.dumps({
    "model": "/models",
    "messages": [{"role": "user", "content": "Your prompt here"}],
    "max_tokens": 200
}).encode()

for i in range(5):
    start = time.time()
    req = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=60) as resp:
        result = json.loads(resp.read())
    tokens = result["usage"]["completion_tokens"]
    elapsed = time.time() - start
    print(f"Run {i+1}: {tokens} tokens in {elapsed:.2f}s = {tokens/elapsed:.1f} tok/s")
'
kubectl delete pod bench
```
