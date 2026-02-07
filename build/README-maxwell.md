# MLC-LLM on Maxwell GPUs (GTX 980 Ti, etc.)

This document covers running MLC-LLM on NVIDIA Maxwell architecture GPUs (compute capability 5.x).

## Overview

Maxwell GPUs like the GTX 980 Ti can run MLC-LLM, but require special handling due to hardware limitations:

| GPU | Architecture | Compute Capability | VRAM | FP16 Support |
|-----|-------------|-------------------|------|--------------|
| GTX 980 Ti | Maxwell | 5.2 | 6GB | **No** |
| GTX Titan X | Maxwell | 5.2 | 12GB | **No** |
| Tesla M40 | Maxwell | 5.2 | 12GB | **No** |

## Key Limitation: No Native FP16 Support

Maxwell GPUs (sm_52) lack native FP16 (`__half`) intrinsics. This means:

- Models quantized with `q4f16_1` (4-bit weights, FP16 activations) **will not work**
- TVM generates CUDA code using `__half`, `__half2float`, etc. that fail to compile on sm_52
- **Solution**: Use FP32 quantization formats (`q0f32`, `q4f32_1`)

### Error Example (FP16 on Maxwell)

```
/tmp/tvm_kernels.cu(312): error: identifier "__half2float" is undefined
/tmp/tvm_kernels.cu(328): error: identifier "__half" is undefined
100 errors detected in the compilation of "tvm_kernels.cu"
```

## Supported Quantization Formats

| Format | Description | Maxwell Support | Memory Usage |
|--------|-------------|-----------------|--------------|
| `q0f32` | Full FP32, no quantization | **Yes** | High (~4x model size) |
| `q4f32_1` | 4-bit weights, FP32 activations | **Yes** | Medium |
| `q4f16_1` | 4-bit weights, FP16 activations | **No** | Low |
| `q3f16_1` | 3-bit weights, FP16 activations | **No** | Lowest |

## Curated Working Models

These models have been verified to work on Maxwell GPUs with 6GB VRAM:

### Small Models (< 6GB VRAM)

| Model | HuggingFace ID | Quantization | VRAM | Notes |
|-------|----------------|--------------|------|-------|
| Qwen3-0.6B | `mlc-ai/Qwen3-0.6B-q0f32-MLC` | q0f32 | ~5.1GB | Recommended starter |
| Qwen2.5-0.5B | `mlc-ai/Qwen2.5-0.5B-q0f32-MLC` | q0f32 | ~4.5GB | Good for testing |
| TinyLlama-1.1B | `mlc-ai/TinyLlama-1.1B-q4f32_1-MLC` | q4f32_1 | ~2.5GB | Fast, lightweight |
| Phi-2 | `mlc-ai/phi-2-q4f32_1-MLC` | q4f32_1 | ~4.0GB | Microsoft's small model |

### Pre-compiled Model Libraries

For Maxwell GPUs, you should pre-compile model libraries to avoid JIT compilation failures.
Store compiled `.so` files alongside model weights:

```
/models/Qwen3-0.6B-q0f32-MLC/
├── mlc-chat-config.json
├── ndarray-cache.json
├── params_shard_*.bin
├── tokenizer.json
└── maxwell-lib.so          # Pre-compiled for sm_52
```

### Finding FP32 Models

Search HuggingFace for compatible models:
```bash
# Models with FP32 quantization
https://huggingface.co/models?search=q0f32-MLC
https://huggingface.co/models?search=q4f32-MLC

# MLC-AI official models
https://huggingface.co/mlc-ai
```

## Performance Benchmarks

Tested on GTX 980 Ti (6GB VRAM):

| Model | Quantization | Tokens/sec | GPU Memory | Notes |
|-------|--------------|------------|------------|-------|
| Qwen3-0.6B | q0f32 | ~60 | 5.1GB | Recommended |
| TinyLlama-1.1B | q4f32_1 | ~45 | 2.5GB | Good quality/speed |
| Phi-2 | q4f32_1 | ~35 | 4.0GB | Better reasoning |

**Note**: Maxwell GPUs are significantly slower than modern GPUs. Expect 3-5x slower inference compared to RTX 30/40 series.

## Setup Guide

### 1. Build the Maxwell Docker Image

The Maxwell image uses CUDA 11.8 (last version supporting sm_52) with the devel base for JIT compilation:

```bash
docker build \
  -f build/Dockerfile.mlc-cuda-maxwell \
  -t registry.harbor.lan/flexinfer/mlc-llm:cuda-maxwell-v7 \
  build/
```

Key differences from standard MLC-LLM image:
- Base: `nvidia/cuda:11.8.0-devel-ubuntu22.04` (not runtime)
- PyTorch from CUDA 11.8 wheels: `pip install torch --index-url https://download.pytorch.org/whl/cu118`
- TVM built with `USE_CUTLASS=OFF` (Maxwell lacks FP16 for CUTLASS)
- LLVM and ninja-build included for JIT compilation

### 2. Download FP32 Model

Use a model with FP32 quantization from HuggingFace:

```bash
# Using huggingface_hub
pip install huggingface_hub
python3 -c "
from huggingface_hub import snapshot_download
snapshot_download(repo_id='mlc-ai/Qwen3-0.6B-q0f32-MLC', local_dir='Qwen3-0.6B-q0f32-MLC')
"
```

Available FP32 models from mlc-ai:
- `mlc-ai/Qwen3-0.6B-q0f32-MLC` (~900MB)
- Check [mlc-ai on HuggingFace](https://huggingface.co/mlc-ai) for more

### 3. Pre-compile for Maxwell

Models must be pre-compiled with Maxwell-specific options:

```bash
python3 -m mlc_llm compile \
  /path/to/Qwen3-0.6B-q0f32-MLC \
  --device cuda:0 \
  --opt "cutlass=0;cublas_gemm=1;cudagraph=0;flashinfer=0" \
  --output /path/to/Qwen3-0.6B-q0f32-MLC/maxwell-lib.so
```

**Required compile options for Maxwell:**
- `cutlass=0` - Disable CUTLASS (requires FP16)
- `flashinfer=0` - Disable FlashInfer (requires sm_80+)
- `cudagraph=0` - Disable CUDA graphs (optional, can help stability)
- `cublas_gemm=1` - Enable cuBLAS fallback for GEMM operations

### 4. Run MLC-LLM Server

Use the `--model-lib` flag to load the pre-compiled library:

```bash
python3 -m mlc_llm serve \
  /path/to/Qwen3-0.6B-q0f32-MLC \
  --model-lib /path/to/Qwen3-0.6B-q0f32-MLC/maxwell-lib.so \
  --host 0.0.0.0 \
  --mode local
```

**Important**: Without `--model-lib`, MLC-LLM will attempt JIT compilation which will fail on Maxwell with FP16 models.

### 5. Test Inference

```bash
curl -X POST http://localhost:8000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "Qwen3-0.6B-q0f32-MLC",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 50
  }'
```

## Kubernetes Deployment

FlexInfer deployments should not rely on ad-hoc `kubectl label node ...` for GPU targeting.
The `flexinfer-agent` DaemonSet discovers GPU hardware and applies stable node labels:

```bash
kubectl get nodes -L flexinfer.ai/gpu.vendor -L flexinfer.ai/gpu.arch -L flexinfer.ai/gpu.vram
```

### Example Pod

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: mlc-maxwell
spec:
  runtimeClassName: nvidia
  nodeSelector:
    nvidia.com/gpu.arch: Maxwell
  containers:
  - name: mlc
    image: registry.harbor.lan/flexinfer/mlc-llm:cuda-maxwell-v7
    command: ["python3", "-m", "mlc_llm"]
    args:
    - serve
    - /models/Qwen3-0.6B-q0f32-MLC
    - --model-lib
    - /models/Qwen3-0.6B-q0f32-MLC/maxwell-lib.so
    - --host
    - "0.0.0.0"
    - --mode
    - local
    resources:
      limits:
        nvidia.com/gpu: 1
        memory: 8Gi
    env:
    - name: MLC_GPU_SIZE_BYTES
      value: "5000000000"  # ~5GB for GTX 980 Ti
    volumeMounts:
    - name: models
      mountPath: /models
  volumes:
  - name: models
    persistentVolumeClaim:
      claimName: mlc-models-nfs
```

## Troubleshooting

### "identifier __half is undefined"

**Cause**: Attempting to use FP16 model on Maxwell GPU.

**Solution**: Use FP32 quantization (`q0f32` or `q4f32_1`).

### "nvcc not found" during JIT compilation

**Cause**: Using runtime image instead of devel image.

**Solution**: Use `nvidia/cuda:11.8.0-devel-ubuntu22.04` base image.

### "CUDA initialization: forward compatibility was attempted on non supported HW"

**Cause**: PyTorch built with CUDA 12.x on CUDA 11.8 driver.

**Solution**: Install PyTorch from CUDA 11.8 wheels:
```bash
pip install torch --index-url https://download.pytorch.org/whl/cu118
```

### Out of memory on 6GB GPU

**Cause**: Model + KV cache exceeds 6GB VRAM.

**Solutions**:
1. Use smaller model (Qwen3-0.6B fits in ~5GB)
2. Reduce `max_total_seq_length` in overrides
3. Use `--mode interactive` for smaller KV cache
4. Set `MLC_GPU_SIZE_BYTES` environment variable

### Compilation takes too long

JIT compilation on older GPUs can be slow. Pre-compile models and use `--model-lib` to skip JIT.

## Converting Models to FP32

If a model is only available in FP16, you can convert it:

```bash
# Download original model from HuggingFace
git clone https://huggingface.co/Qwen/Qwen3-0.6B

# Generate MLC config with FP32 quantization
mlc_llm gen_config Qwen3-0.6B \
  --quantization q0f32 \
  --conv-template qwen3 \
  -o Qwen3-0.6B-q0f32-MLC

# Convert weights
mlc_llm convert_weight Qwen3-0.6B \
  --quantization q0f32 \
  -o Qwen3-0.6B-q0f32-MLC
```

## Architecture Reference

### Maxwell Compute Capabilities

| GPU | Compute Capability | Notes |
|-----|-------------------|-------|
| GTX 750, 750 Ti | 5.0 | First Maxwell |
| GTX 960, 970, 980 | 5.2 | Maxwell GM204 |
| GTX 980 Ti, Titan X | 5.2 | Maxwell GM200 |
| Tesla M40, M60 | 5.2 | Data center Maxwell |

### FP16 Support Timeline

- **Maxwell (5.x)**: No native FP16
- **Pascal (6.x)**: FP16 at half rate (GP100 full rate)
- **Volta+ (7.0+)**: Full FP16 support with Tensor Cores

## FlexInfer ModelDeployment

FlexInfer automatically detects Maxwell GPUs via the node agent and applies appropriate settings.

### v1alpha2 `Model` (recommended)

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: qwen3-0-6b-maxwell
  namespace: flexinfer-system
spec:
  backend: mlc-llm
  source: HF://mlc-ai/Qwen3-0.6B-q0f32-MLC
  gpu:
    vendor: nvidia
    vramEstimateMB: 5200
  cache:
    strategy: SharedPVC
    storageClass: longhorn
    size: 6Gi
  config:
    jitPolicy: READONLY
    # Option A (recommended): compile to /models/<modelName>/maxwell-lib.so and omit modelLibPath
    # Option B: set modelLibPath explicitly
    # modelLibPath: /models/qwen3-0-6b-maxwell/maxwell-lib.so
```

Compile once into the cache PVC using the Job example in `docs/user/backends-maxwell.md`.

### v1alpha1 `ModelDeployment` (legacy)

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-maxwell
spec:
  backend: mlc-llm
  model: Qwen3-0.6B-q0f32-MLC
  modelCacheRef: qwen3-0.6b-maxwell  # Pre-cached model with compiled lib
  nodeSelector:
    flexinfer.ai/gpu.arch: sm_52  # Target Maxwell GPUs only
  mlcllm:
    mode: local
    modelLibPath: /models/maxwell-lib.so  # Pre-compiled for sm_52
    jitPolicy: "READONLY"  # Use pre-compiled library (avoid JIT on Maxwell)
    gpuMemoryBytes: 5000000000  # ~5GB for GTX 980 Ti
    compileOptions:
      useCutlass: false      # Disabled - requires FP16
      useFlashInfer: false   # Disabled - requires sm_80+
      useCublasGemm: true    # Enable cuBLAS fallback
      useCudaGraph: false    # Disabled for stability
    overrides:
      maxTotalSeqLength: 2048  # Reduced for limited VRAM
      prefillChunkSize: 256    # Smaller chunks for stability
  resources:
    limits:
      nvidia.com/gpu: 1
      memory: 8Gi
    requests:
      memory: 6Gi
```

### Node Labeling

The FlexInfer agent automatically detects Maxwell GPUs and applies labels:
```bash
# Labels applied by flexinfer-agent
flexinfer.ai/gpu.vendor: NVIDIA
flexinfer.ai/gpu.arch: sm_52
flexinfer.ai/gpu.vram: 6Gi
flexinfer.ai/gpu.int4: false  # No INT4 tensor cores
```

Use node selectors to target Maxwell-specific deployments:
```yaml
nodeSelector:
  flexinfer.ai/gpu.arch: sm_52
```

## References

- [MLC-LLM Documentation](https://llm.mlc.ai/docs/)
- [MLC-LLM Compile Models](https://llm.mlc.ai/docs/compilation/compile_models.html)
- [MLC-AI Models on HuggingFace](https://huggingface.co/mlc-ai)
- [NVIDIA CUDA Compute Capabilities](https://developer.nvidia.com/cuda-gpus)
