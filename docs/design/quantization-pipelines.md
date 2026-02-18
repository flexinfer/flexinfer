# Quantization Pipelines Design

**Status:** In Progress (GGUF/AWQ/GPTQ/EXL2/FP8 and auto-selection implemented; quality validation follow-up pending)
**Author:** FlexInfer Team
**Created:** 2026-01-31

## Tracking

- [#7 Quantization pipelines execution](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/7)
- [#10 Quantization quality validation gate](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/10)

## Overview

Quantization pipelines enable automatic conversion of full-precision models to quantized formats during caching. This reduces VRAM usage and improves inference speed, especially important for homelab users with limited GPU memory.

## Use Cases

1. **VRAM Optimization**: Run larger models on smaller GPUs (e.g., 70B model on 24GB VRAM)
2. **Speed Improvement**: Quantized models often run faster due to reduced memory bandwidth
3. **Cost Reduction**: Fit more models on fewer GPUs in shared environments
4. **Automatic Format Selection**: Choose optimal quantization based on GPU capabilities

## Architecture

```
┌─────────────────┐     ┌────────────────────┐     ┌─────────────────┐
│   HuggingFace   │────▶│  Quantization Job  │────▶│  Model Cache    │
│   Model (FP16)  │     │  (llama.cpp/AWQ)   │     │  (GGUF/AWQ)     │
└─────────────────┘     └────────────────────┘     └─────────────────┘
                               │
                               ▼
                        ┌──────────────┐
                        │   Model Pod  │
                        │ (Uses Cache) │
                        └──────────────┘
```

## API Design

### ModelCache with Quantization

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: llama3-q4
spec:
  source: HF://meta-llama/Llama-3-8B-Instruct
  storageStrategy: SharedPVC

  # Quantization configuration
  quantization:
    # Target format
    format: GGUF  # GGUF, AWQ, GPTQ, EXL2, FP8

    # Quantization level for GGUF
    ggufType: Q4_K_M  # Q2_K, Q3_K_S, Q4_K_M, Q5_K_M, Q6_K, Q8_0

    # For AWQ/GPTQ
    bits: 4
    groupSize: 128

    # Processing options
    useGPU: true  # Use GPU for quantization (faster)
    maxMemoryGB: 24  # Memory limit for quantization job

  pvcName: model-cache-pvc
  size: 50Gi
```

### Model Using Quantized Cache

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: llama3-8b
spec:
  backend: llamacpp  # or ollama, vllm-awq
  source: cache://llama3-q4  # Reference quantized cache
  gpu:
    count: 1
    vramEstimateMB: 6000  # Reduced from ~16GB
```

## Quantization Formats

### GGUF (llama.cpp)

Best for: CPU inference, mixed CPU/GPU, consumer GPUs

| Type | Bits | Size (7B) | Quality | Speed |
|------|------|-----------|---------|-------|
| Q2_K | 2-3 | ~2.5GB | Lower | Fastest |
| Q3_K_S | 3 | ~3GB | Acceptable | Fast |
| Q4_K_M | 4 | ~4GB | Good | Fast |
| Q5_K_M | 5 | ~5GB | Great | Medium |
| Q6_K | 6 | ~5.5GB | Excellent | Medium |
| Q8_0 | 8 | ~7GB | Near-FP16 | Slower |

### AWQ (Activation-aware Weight Quantization)

Best for: NVIDIA GPUs, high-throughput serving

| Bits | Group Size | Size (7B) | Quality |
|------|------------|-----------|---------|
| 4 | 128 | ~4GB | Excellent |
| 4 | 64 | ~4.5GB | Better |

### GPTQ

Best for: NVIDIA GPUs, wide compatibility

| Bits | Size (7B) | Quality |
|------|-----------|---------|
| 4 | ~4GB | Good |
| 8 | ~7GB | Great |

### EXL2 (ExLlamaV2)

Best for: Maximum speed on NVIDIA GPUs

| Bits | Size (7B) | Quality |
|------|-----------|---------|
| 2.5 | ~2.5GB | Lower |
| 4 | ~4GB | Good |
| 6 | ~5.5GB | Great |

## Implementation

### Phase 1: GGUF Quantization

1. Add `quantization` field to ModelCache CRD
2. Create llama.cpp quantization job template
3. Implement quantization job controller
4. Update Model controller to detect quantized caches

### Phase 2: AWQ/GPTQ Support

1. Add AutoAWQ job for AWQ quantization
2. Add auto-gptq job for GPTQ quantization
3. Integrate with vLLM for serving AWQ/GPTQ models

### Phase 3: Auto-Selection

1. Detect GPU capabilities (VRAM, compute capability)
2. Recommend optimal quantization format
3. Auto-quantize based on Model requirements

## Job Templates

### GGUF Quantization Job

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: quantize-llama3-q4km
spec:
  template:
    spec:
      containers:
        - name: quantize
          image: ghcr.io/flexinfer/quantizer:gguf
          command:
            - /bin/sh
            - -c
            - |
              # Download model
              huggingface-cli download meta-llama/Llama-3-8B-Instruct \
                --local-dir /workspace/source

              # Convert to GGUF FP16
              python convert_hf_to_gguf.py /workspace/source \
                --outfile /workspace/fp16.gguf

              # Quantize to Q4_K_M
              ./llama-quantize /workspace/fp16.gguf \
                /cache/llama3-q4km.gguf Q4_K_M

              # Cleanup intermediate files
              rm /workspace/fp16.gguf
          volumeMounts:
            - name: workspace
              mountPath: /workspace
            - name: cache
              mountPath: /cache
          resources:
            limits:
              memory: 32Gi
              cpu: "8"
      volumes:
        - name: workspace
          emptyDir: {}
        - name: cache
          persistentVolumeClaim:
            claimName: model-cache-pvc
      restartPolicy: Never
```

### AWQ Quantization Job

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: quantize-llama3-awq
spec:
  template:
    spec:
      containers:
        - name: quantize
          image: ghcr.io/flexinfer/quantizer:awq
          command:
            - python
            - -c
            - |
              from awq import AutoAWQForCausalLM
              from transformers import AutoTokenizer

              model_path = "meta-llama/Llama-3-8B-Instruct"
              quant_path = "/cache/llama3-awq"

              model = AutoAWQForCausalLM.from_pretrained(
                  model_path,
                  safetensors=True
              )
              tokenizer = AutoTokenizer.from_pretrained(model_path)

              model.quantize(
                  tokenizer,
                  quant_config={
                      "w_bit": 4,
                      "q_group_size": 128,
                      "zero_point": True,
                  }
              )

              model.save_quantized(quant_path)
              tokenizer.save_pretrained(quant_path)
          volumeMounts:
            - name: cache
              mountPath: /cache
          resources:
            limits:
              nvidia.com/gpu: "1"
              memory: 48Gi
      volumes:
        - name: cache
          persistentVolumeClaim:
            claimName: model-cache-pvc
      restartPolicy: Never
```

## Status Updates

### ModelCache Status with Quantization

```yaml
status:
  phase: Ready
  path: /models/llama3-q4km.gguf
  sizeBytes: 4294967296  # 4GB
  quantization:
    format: GGUF
    type: Q4_K_M
    originalSizeBytes: 16106127360  # 15GB original
    compressionRatio: 3.75
    quantizationTime: "2m34s"
  readyNodes: 1
  totalNodes: 1
```

## Metrics

New Prometheus metrics for quantization:

```
# Quantization job duration
flexinfer_quantization_duration_seconds{model,format,type}

# Compression ratio achieved
flexinfer_quantization_compression_ratio{model,format}

# Quantization job status
flexinfer_quantization_jobs_total{model,status}  # status: success, failed

# Cache sizes by format
flexinfer_cache_size_bytes{model,format}
```

## CLI Commands

```bash
# List available quantization formats
$ flexinfer quantize formats
FORMAT  BITS  BACKENDS           NOTES
GGUF    2-8   llamacpp, ollama   Best for consumer GPUs
AWQ     4     vllm               NVIDIA only, high throughput
GPTQ    4-8   vllm, tgi          Wide compatibility
EXL2    2-6   exllamav2          Fastest on NVIDIA

# Quantize a cached model
$ flexinfer quantize my-model --format GGUF --type Q4_K_M

# Check quantization status
$ flexinfer cache status
NAME           FORMAT    SIZE    COMPRESSION  STATUS
llama3-q4km    GGUF/Q4   4GB     3.75x        Ready
mistral-awq    AWQ/4bit  4.2GB   3.5x         Quantizing (45%)

# Estimate VRAM for quantized model
$ flexinfer estimate llama3-8b --format GGUF --type Q4_K_M
Estimated VRAM: 5.2GB (includes KV cache for 4K context)
Recommended GPU: RTX 3060 12GB or better
```

## Validation Rules

1. **Format-Backend Compatibility**:
   - GGUF → llamacpp, ollama
   - AWQ → vllm (NVIDIA only)
   - GPTQ → vllm, tgi
   - EXL2 → exllamav2

2. **GPU Requirements**:
   - AWQ/GPTQ quantization requires NVIDIA GPU
   - GGUF quantization can run on CPU

3. **Size Constraints**:
   - Warn if quantized size > 80% available VRAM
   - Error if quantization format unsupported by backend

## Open Questions

1. **Quality Validation**: How to verify quantization quality? Automated perplexity testing?

2. **Incremental Updates**: Re-quantize when base model updates?

3. **Multi-GPU Quantization**: Support quantizing models larger than single GPU memory?

4. **iMatrix Calibration**: Support custom calibration datasets for better quality?

## References

- [llama.cpp Quantization](https://github.com/ggerganov/llama.cpp/blob/master/examples/quantize/README.md)
- [AutoAWQ](https://github.com/casper-hansen/AutoAWQ)
- [GPTQ-for-LLaMA](https://github.com/qwopqwop200/GPTQ-for-LLaMa)
- [ExLlamaV2](https://github.com/turboderp/exllamav2)
