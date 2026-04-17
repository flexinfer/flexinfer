# Quantization Pipelines Design

**Status:** Complete (GGUF/AWQ/GPTQ/EXL2/FP8, auto-selection, quality validation gate, abliteration pipeline, Gemma4 MoE support, GPUProfile watch, and deployment automation implemented)
**Author:** FlexInfer Team
**Created:** 2026-01-31
**Updated:** 2026-04-13

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

### Full Pipeline

```
┌────────────┐     ┌─────────────┐     ┌──────────────┐     ┌──────────────┐     ┌─────────────┐
│  Download   │────▶│  Abliterate  │────▶│   Finetune   │────▶│   Quantize   │────▶│    Ready     │
│  (BF16/FP16)│     │  (optional)  │     │  (optional)  │     │  (GPTQ/AWQ)  │     │  (INT4/INT8) │
└────────────┘     └─────────────┘     └──────────────┘     └──────────────┘     └─────────────┘
      │                   │                   │                    │                      │
      ▼                   ▼                   ▼                    ▼                      ▼
  PVC marker:        PVC marker:         PVC marker:          PVC marker:           Model Pod
 .download_complete  .abliteration-     .finetune-           gptq-w4-g128/         (uses cache)
                     status.json        status.json          quantize_config.json
```

Each phase is guarded: quantization blocks until download (and optionally abliteration/finetune) reaches `Ready`.

### Deployment Reliability

```
┌──────────────┐     ┌────────────────┐     ┌────────────────┐
│  GPUProfile   │────▶│   Controller   │────▶│  Quantize Job  │
│  (image ref)  │     │  (watches GP)  │     │  (version check)│
└──────────────┘     └────────────────┘     └────────────────┘
       │                     │                       │
  Image digest          Spec hash =              Script version =
  updated by admin    SHA(spec + image)         FLEXINFER_SCRIPT_VERSION
                           │
                    Hash mismatch →
                    delete stale job,
                    recreate with new image
```

### Legacy Architecture (simple path)

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

Best for: AMD ROCm (ExLlama kernels, 7x faster than AWQ) and NVIDIA GPUs

| Bits | Size (7B) | Size (26B MoE) | Quality | ROCm Performance |
|------|-----------|----------------|---------|------------------|
| 4 | ~4GB | ~7-13 GB | Good | 72 tok/s (gfx1100) |
| 8 | ~7GB | ~20 GB | Great | ~40 tok/s (gfx1100) |

GPTQ with `sym=true` routes through ExLlama v2 kernels (HIP-compiled on ROCm), achieving ~72 tok/s decode vs ~9.3 tok/s for AWQ on gfx1100.

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

## Gemma4 MoE Support

Gemma4 26B-A4B (128 experts, 25.2B total / 3.8B active) requires:

1. **Disk offload for Hessians**: 640 expert modules × ~100 MB Hessian each exceeds GPU VRAM. Model policy sets `offload_to_disk=true` automatically.
2. **High calibration samples**: 512 samples (2x default) for adequate expert coverage.
3. **MoE-safe abliteration**: Only `o_proj` weight matrix (shared attention output). Expert FFN weights are auto-skipped.
4. **GDN layer skip**: 25 of 30 layers are GDN (linear attention) — auto-skipped during abliteration.

### Model Policy Framework

Built-in model policies in `pkg/quantization/gptq.go` provide architecture-specific overrides:

| Policy | Match | Key Overrides |
|--------|-------|---------------|
| `gemma4-text` | `gemma4_text` model_type | `offload_to_disk=true`, 512 samples, `attn_implementation=eager` |
| `qwen3.5-text` | `qwen3_5_text` model_type | Manual sharded loader, text_config extraction, 16 samples |

Operator override: set `FLEXINFER_GPTQ_MODEL_POLICIES` env var with custom JSON to replace defaults.

## Deployment Reliability Features

### GPUProfile Watch (2026-04-13)

`ModelCacheReconciler` watches `GPUProfile` resources. Updates to quantizer image digests in GPUProfiles trigger automatic reconciliation of matching ModelCaches. The spec hash includes the resolved image, so GPUProfile changes are detected as spec drift.

### Image Drift Detection (2026-04-13)

Active quantization jobs are checked against the currently resolved image. If the GPUProfile image changes while a job is running, the stale job is deleted and recreated with the new image.

### Script Version Marker (2026-04-13)

`quantize_gptq.py` contains `FLEXINFER_SCRIPT_VERSION` (e.g., `"v12"`). The wrapper script checks this against the controller's `GPTQScriptVersion` constant at startup. Mismatch → immediate fatal exit with clear message.

### Deploy Automation (2026-04-13)

`scripts/deploy-quantizer.sh` automates: Docker build → push → digest extraction → GPUProfile YAML update → kubectl apply. Optional `--controller` flag rebuilds the controller. Optional `--restart-job NAME` deletes a named job for re-creation.

## Resolved Questions

1. **Re-quantize on base model updates**: Yes — spec hash change detection handles this. Update `spec.source` to trigger re-download + re-quantize.
2. **Multi-GPU quantization**: Not needed for current models. CPU offload + disk offload handles models up to 60B+ on single GPU.
3. **Custom calibration datasets**: Supported via `calibration.dataset` field (any HuggingFace dataset).
4. **MoE expert quantization**: Solved with GPTQModel >= 6.0.3 native Gemma4 support + disk offload.

## References

- [GPTQModel](https://github.com/ModelCloud/GPTQModel) (replacement for archived AutoGPTQ/AutoAWQ)
- [llama.cpp Quantization](https://github.com/ggml-org/llama.cpp)
- [ExLlamaV2](https://github.com/turboderp/exllamav2)
