# Quantization Pipelines

FlexInfer can quantize model weights during `ModelCache` provisioning.

Supported formats today:
- `GGUF` (for `llamacpp` and `ollama`)
- `AWQ` (for `vllm`)
- `GPTQ` (for `vllm`)
- `EXL2` (for `exllamav2`)
- `FP8` (for `vllm`)

## Prerequisites

1. Ensure controller quantizer images are set for your registry:

```yaml
# charts/flexinfer/values.yaml
quantization:
  images:
    gguf: registry.harbor.lan/flexinfer/quantizer:gguf
    awq: registry.harbor.lan/flexinfer/quantizer:awq
    gptq: registry.harbor.lan/flexinfer/quantizer:gptq
    exl2: registry.harbor.lan/flexinfer/quantizer:exl2
    fp8: registry.harbor.lan/flexinfer/quantizer:fp8
```

2. Apply or upgrade FlexInfer chart.

## Request Quantization with CLI

Use the `flexinfer quantize` command against a `ModelCache`:

```bash
# GGUF (default)
flexinfer quantize llama3-8b --format GGUF --type Q4_K_M

# AWQ (GPU required)
flexinfer quantize llama3-8b --format AWQ --bits 4 --group-size 128 --use-gpu

# GPTQ (GPU required)
flexinfer quantize llama3-8b --format GPTQ --bits 4 --group-size 128 --use-gpu

# EXL2 (GPU required)
flexinfer quantize llama3-8b --format EXL2 --bits 4 --use-gpu

# FP8 (GPU required)
flexinfer quantize llama3-8b --format FP8 --bits 8 --use-gpu
```

Check available formats:

```bash
flexinfer quantize formats
```

Get an auto recommendation from model footprint + node constraints:

```bash
# Preview recommendation (no changes)
flexinfer quantize recommend llama3-8b

# Apply recommendation to ModelCache.spec.quantization (explicit opt-in)
flexinfer quantize recommend llama3-8b --apply
```

Check status:

```bash
flexinfer quantize status llama3-8b
flexinfer cache status
```

## Quantization Quality Validation Gate

Use the quality gate to compare a quantized artifact against a baseline model before rollout.

```bash
# Example: GGUF check on a gfx1100 workflow
flexinfer quantize validate \
  --format GGUF \
  --baseline-perplexity 9.50 \
  --candidate-perplexity 10.10 \
  --baseline-acceptance 94 \
  --candidate-acceptance 92
```

Expected passing signal:
- `Result: PASS`

Expected failing signal:
- `Result: FAIL`
- one or more `Failure:` lines with the violated threshold(s)
- non-zero process exit (safe for CI gates)

Acceptance rate input supports either:
- ratio (`0.92`)
- percent (`92`)

Policy thresholds (deterministic):

| Format | Max Perplexity Regression | Max Acceptance Drop |
|--------|---------------------------|---------------------|
| GGUF | +10.00% | 3.00pp |
| AWQ | +7.00% | 2.00pp |
| GPTQ | +8.00% | 2.50pp |
| EXL2 | +6.00% | 2.00pp |
| FP8 | +5.00% | 1.50pp |

## Declarative Quantization in ModelCache

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: llama3-8b-gguf
  namespace: flexinfer-system
spec:
  source: HF://meta-llama/Meta-Llama-3-8B
  storageStrategy: SharedPVC
  quantization:
    format: GGUF
    ggufType: Q4_K_M
```

For AWQ/GPTQ:

```yaml
quantization:
  format: AWQ # or GPTQ
  bits: 4
  groupSize: 128
  useGPU: true
```

## Calibration Tuning

AWQ and GPTQ quantization use calibration samples to determine optimal quantization ranges. The `calibration` field in the `QuantizationSpec` controls this process.

### CalibrationSpec Fields

| Field | Default | Range | Description |
|-------|---------|-------|-------------|
| `maxSeqLen` | 4096 | 128–32768 | Maximum token length per calibration sample |
| `maxSamples` | 256 | 8–2048 | Number of calibration samples from the dataset |
| `nParallelCalibSamples` | 16 | 1–256 | Parallel batch size — controls GPU↔CPU memory tradeoff |
| `dataset` | `mit-han-lab/pile-val-backup` | any HF dataset | HuggingFace dataset for calibration samples |

```yaml
quantization:
  format: GPTQ
  bits: 4
  groupSize: 128
  useGPU: true
  calibration:
    maxSeqLen: 4096
    maxSamples: 256
    nParallelCalibSamples: 32
    dataset: "mit-han-lab/pile-val-backup"  # or custom dataset
```

The default calibration dataset is `mit-han-lab/pile-val-backup` (requires the `zstandard` Python package for zstd decompression). Set `calibration.dataset` to use a different HuggingFace dataset.

### GPTQ-Specific Parameters

| Field | Default | Description |
|-------|---------|-------------|
| `sym` | `true` | Symmetric quantization. `true` is required for ExLlama v2 kernels, the fastest decode path on ROCm. |
| `descAct` | `false` | Activation reordering. `false` = faster inference. `true` = slightly better quality. |
| `dynamicExclusion` | `auto` | Module exclusion strategy. `auto` detects hybrid architectures and keeps attention/expert/vision/MTP at full precision. `none` quantizes all modules (pure INT4). |
| `gpuMemoryFraction` | `"0.80"` | Fraction of GPU VRAM available to quantization (e.g. `"0.95"`). Lower values leave headroom for ROCm GTT overhead. |

```yaml
quantization:
  format: GPTQ
  bits: 4
  groupSize: 128
  sym: true               # required for ExLlama v2 kernels on ROCm
  descAct: false           # false = faster, true = slightly better quality
  dynamicExclusion: "none" # pure INT4 — smaller output, fits more cards
  gpuMemoryFraction: "0.85"
  useGPU: true
```

On ROCm gfx1100, `sym=true` + `descAct=false` routes through `ExllamaLinearKernel` (HIP-compiled), achieving ~72-73 tok/s decode on a 14B model. AWQ on the same hardware reaches ~9.3 tok/s due to Triton dequant kernel overhead.

#### Dynamic Exclusion Modes

| Mode | Behavior | Typical Use |
|------|----------|-------------|
| `auto` | Detects hybrid architectures (e.g. Qwen3.5 GatedDeltaNet + attention). Excludes attention, shared expert, vision, and MTP modules from quantization. | Quality-focused; matches official Qwen GPTQ-Int4 approach. Produces larger output (~1.95x compression). |
| `none` | Quantizes all modules to the target bit width. | Size-focused; produces smaller output (~3.5x compression) that fits on smaller VRAM cards. |

For a 27B model: `auto` produces ~28 GB (doesn't fit 24 GB VRAM), `none` produces ~15 GB (fits with KV cache room).

### Memory Requirements

| Model Size | Format | GPU VRAM | Container Memory | Calibration Config | Est. Time |
|------------|--------|----------|------------------|--------------------|-----------|
| 8B | GPTQ INT4 | 24 GB | 32Gi | defaults (256 @ 4096) | ~30 min |
| 14B | GPTQ INT4 | 24 GB | 48Gi | defaults (256 @ 4096) | ~73 min |
| 27B | GPTQ INT4 | 16 GB+ | 96Gi | 128 @ 2048, nParallel=16 | ~2-3h |
| 14B | AWQ W4 | 24 GB | 56Gi | nParallel=32 | ~60 min |

### nParallelCalibSamples Tuning

This parameter controls the tradeoff between GPU VRAM and CPU memory during calibration:

- **Omitted / high value**: all samples processed on GPU simultaneously. Needs full VRAM for model weights + activations. Faster but can cause OOM on constrained nodes.
- **Low value (e.g., 16-32)**: batches N samples on GPU at a time, offloads between batches. Requires more CPU memory but reduces peak VRAM usage.

For 14B models on 24GB VRAM: `nParallelCalibSamples: 32` with 56Gi container memory is a good balance.

### ROCm GPU Driver Memory Warning

> **Critical for AMD GPUs:** ROCm/HIP allocates GTT (Graphics Translation Table) memory through the kernel DRM subsystem. This memory is system RAM but is **not tracked** by the container's cgroup memory limit.

On a 62 GiB node with a 48Gi container limit:
- Container uses ~40 GiB (tracked by cgroup)
- ROCm GTT + page tables use ~15-20 GiB (**not** tracked)
- Total: 55-60 GiB → node-level OOM

**Rule:** Set `maxMemoryGB` to `total_node_RAM - 20` for AMD GPU nodes.

The controller automatically sets `PYTORCH_HIP_ALLOC_CONF=expandable_segments:True` for AMD GPU quantization jobs, which prevents reserved-but-fragmented memory from causing OOM.

### GPU Node Tolerations

Quantization jobs run on GPU nodes. If your GPU nodes have a `dedicated=gpu:NoSchedule` taint, the job needs a matching toleration. The controller automatically adds this toleration to quantization jobs.

If you need custom tolerations, add them to the ModelCache `spec.tolerations`:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: my-model-gptq
spec:
  source: "org/model-name"
  tolerations:
    - key: dedicated
      value: gpu
      operator: Equal
      effect: NoSchedule
  quantization:
    format: GPTQ
    bits: 4
    groupSize: 128
    useGPU: true
```

### Architecture-Specific Images

The controller selects the quantizer image based on the target GPU architecture:

| GPU Arch | Image | Base |
|----------|-------|------|
| NVIDIA (any) | `quantizer:gptq` | CUDA 12.2, PyTorch 2.3 |
| AMD gfx1100 | `quantizer:gptq-rocm-gfx1100` | ROCm 6.4.1, PyTorch 2.6 |
| AMD gfx906 | `quantizer:gptq-rocm-gfx906` | ROCm 6.2.3, PyTorch 2.3 |

Override with env vars: `FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX906_IMAGE`, `FLEXINFER_QUANTIZER_GPTQ_ROCM_IMAGE`, or `FLEXINFER_QUANTIZER_GPTQ_IMAGE`.

## Backend Compatibility

- `GGUF`: `llamacpp`, `ollama`
- `AWQ`: `vllm`
- `GPTQ`: `vllm`
- `EXL2`: `exllamav2`
- `FP8`: `vllm`

If format/backend are incompatible, scheduling or startup will fail.

## Troubleshooting

**Quantization stuck in `Quantizing`:**
- Check the job: `kubectl get jobs -n <ns> | grep quantize`
- Check logs: `kubectl logs job/<cache-name>-quantize -n <ns>`

**Job OOMKilled:**
- On AMD GPUs, check if the node itself ran out of memory (GPU driver allocates outside cgroup). Reduce `maxMemoryGB`, `maxSamples`, or `maxSeqLen`.
- Check `kubectl describe pod <job-pod>` for the OOMKilled reason — if `Last State: Terminated (OOMKilled)`, reduce calibration params.

**`torch.cuda.OutOfMemoryError` in job logs:**
- Reduce `nParallelCalibSamples` to lower peak VRAM (e.g., 16 → 8).
- Reduce `maxSamples` or `maxSeqLen`.
- GPTQModel auto-offloads to disk when GPU memory is exhausted, but peak allocations during forward pass can still exceed VRAM.

**`RuntimeError: Numpy is not available`:**
- ROCm PyTorch images are compiled against numpy 1.x. If a dependency pulls numpy 2.x, PyTorch breaks. The quantizer Dockerfiles pin `numpy>=1.26,<2` to prevent this.

**AWQ/GPTQ/EXL2/FP8 job fails quickly:**
- Confirm `useGPU: true` is set.
- Confirm quantizer image has required runtime dependencies.

**Controller cannot pull quantizer image:**
- Set `quantization.images.*` to reachable registry tags in values.yaml.
- ROCm quantizer images are ~60GB — initial pull takes 20-30 min.

**Re-quantization after changing source model:**
- The download PVC uses a `.flexinfer_cached` marker file. Changing the model source requires deleting the PVC and letting the controller recreate it. Interrupted downloads leave metadata files without weight files — check for the `.download_complete` marker, not directory contents.

**Quality gate fails:**
- Verify baseline/candidate eval prompts and dataset are identical.
- Verify acceptance units (0..1 vs 0..100) are correctly passed.
- For ROCm gfx1100 targets, prefer `GGUF` baselines first, then compare alternative formats.
