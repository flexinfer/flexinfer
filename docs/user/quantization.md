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
| `nParallelCalibSamples` | unset | 1–256 | Optional parallel calibration batch size. Lower values reduce peak VRAM by processing fewer samples on GPU at once. |
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

### Calibration Recipes

Use calibration settings as a memory control, not only as a quality knob:

| Scenario | Suggested Settings | Notes |
|----------|--------------------|-------|
| 8B model on 24GB VRAM | defaults | Usually fits without extra CPU offload. |
| 14B GPTQ on 24GB VRAM | `maxSamples: 256`, `maxSeqLen: 4096`, `nParallelCalibSamples: 32`, `maxMemoryGB: 56` | Good first pass when GPU memory is tight but host RAM is available. |
| 14B AWQ on 24GB VRAM | `nParallelCalibSamples: 32`, `maxMemoryGB: 56` | AWQ consumes `nParallelCalibSamples` directly as `N_PARALLEL_CALIB_SAMPLES`. |
| 27B+ GPTQ on single GPU | `maxSamples: 128`, `maxSeqLen: 2048`, `gpuMemoryFraction: "0.80"` | Prefer smaller calibration batches first, then increase samples only after the job reaches quantization. |

Unset `nParallelCalibSamples` lets the backend library choose its own calibration batching. Setting it to a lower value such as `16` or `32` limits how many samples are active on the GPU at once. That reduces peak VRAM pressure, but it can increase CPU memory and wall-clock time because calibration is split into more batches.

For AMD/ROCm jobs, FlexInfer automatically injects `PYTORCH_HIP_ALLOC_CONF=expandable_segments:True` so PyTorch can reuse fragmented HIP memory segments instead of failing on reserved-but-unused memory. Keep this automatic setting unless you are debugging allocator behavior in a custom image.

### GPTQ-Specific Parameters

| Field | Default | Description |
|-------|---------|-------------|
| `sym` | `true` | Symmetric quantization. `true` is required for ExLlama v2 kernels, the fastest decode path on ROCm. |
| `descAct` | `false` | Activation reordering. `false` = faster inference. `true` = slightly better quality. |
| `dynamicExclusion` | `auto` | Module exclusion strategy. `auto` detects hybrid architectures and keeps attention/expert/vision/MTP at full precision. `gdn` keeps GDN `linear_attn.*` modules full precision while quantizing full-attention and FFN modules. `none` quantizes all modules (pure INT4). |
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

- **Omitted**: FlexInfer does not set `N_PARALLEL_CALIB_SAMPLES`; the quantizer backend chooses its default batching.
- **High value**: more samples can be active on the GPU at once. This is faster when the model fits, but it can cause OOM on constrained nodes.
- **Low value (e.g., 16-32)**: batches N samples on GPU at a time. This reduces peak VRAM usage, but can increase CPU memory pressure and wall-clock time.

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

Quantization jobs run on GPU nodes when `useGPU: true`. If your GPU nodes have a `dedicated=gpu:NoSchedule` taint, the job needs a matching toleration. The controller automatically adds this default toleration to GPU quantization, abliteration, finetune, publish, and validation jobs.

If your cluster uses additional GPU taints, add them to the ModelCache `spec.tolerations`. FlexInfer appends these to the default GPU toleration:

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
    - key: workload
      value: batch
      operator: Equal
      effect: NoSchedule
  quantization:
    format: GPTQ
    bits: 4
    groupSize: 128
    useGPU: true
```

Use a per-phase `quantization.nodeSelector` when quantization needs a different node than download or serving, for example a larger-RAM GPU host. The top-level `spec.tolerations` still apply to the quantization job and image warmup job.

### Progress and Status

Watch both the Kubernetes Job and the ModelCache status. The Job tells you whether the pod is active, failed, or complete; the ModelCache status carries FlexInfer's phase and progress detail.

```bash
kubectl get modelcache my-model-gptq -n flexinfer-system -o jsonpath='{.status.currentPhase}{"\n"}{.status.quantization.progressDetail}{"\n"}'
kubectl get jobs -n flexinfer-system | grep my-model-gptq
kubectl logs -n flexinfer-system job/my-model-gptq-quantize -c quantizer -f
```

During an active job, `status.currentPhase` is `quantization`, `status.quantization.progress` is a best-effort percentage capped below 100 until completion, and `status.quantization.progressDetail` shows elapsed time or the latest completed layer when the controller can read it from pod logs.

After success, `status.quantization` records the format, quantization type, original and compressed size, compression ratio, start/completion time, and any calibration parameters used. This status survives quantizer image updates while the ModelCache is Ready; a GPUProfile image drift only restarts jobs that are still inside the early image-drift grace window.

### Architecture-Specific Images

The controller selects the quantizer image based on the target GPU architecture:

| GPU Arch | Image | Base |
|----------|-------|------|
| NVIDIA (any) | `quantizer:gptq` | CUDA 12.2, PyTorch 2.3 |
| AMD gfx1100 | `quantizer:gptq-rocm-gfx1100` | ROCm 6.4.1, PyTorch 2.6 |
| AMD gfx906 | `quantizer:gptq-rocm-gfx906` | ROCm 6.2.3, PyTorch 2.3 |

Override with env vars: `FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX906_IMAGE`, `FLEXINFER_QUANTIZER_GPTQ_ROCM_IMAGE`, or `FLEXINFER_QUANTIZER_GPTQ_IMAGE`.

## Full Pipeline: Download → Abliterate → Quantize → Ready

For models that need refusal-direction removal before quantization (e.g. Gemma4, Qwen3.5), the `ModelCache` pipeline executes ordered phases with strict guards:

```
Pending → Download → Abliterate → Quantize → Publishing → Ready
```

Each phase blocks until its predecessor reaches `Ready`. Configure the full pipeline in a single `ModelCache`:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: gemma4-26b-a4b-gptq
  namespace: flexinfer-system
spec:
  source: "google/gemma-4-26B-A4B-it"
  storageStrategy: SharedPVC
  storageSize: "96Gi"
  secretRef: "hf-token"
  nodeSelector:
    flexinfer.ai/gpu.arch: gfx1100

  abliteration:
    numSamples: 256
    targetLayers: "auto"          # auto-skips GDN layers
    weightMatrices: ["o_proj"]    # MoE safety: only shared attention output
    useGPU: true
    maxMemoryGB: 56
    normThreshold: "100"
    ablitateLmHead: false         # CRITICAL: must be false (save corruption bug)

  quantization:
    format: GPTQ
    bits: 4
    groupSize: 128
    sym: true
    useGPU: true
    maxMemoryGB: 48
    calibration:
      maxSeqLen: 2048
      maxSamples: 512             # 2x default for MoE expert coverage
```

See [Abliteration Pipeline](abliteration.md) for full abliteration documentation.

## Gemma4 MoE Quantization

Gemma4 26B-A4B is a Mixture-of-Experts model with 128 experts per MLP layer. It requires special quantization handling.

### Architecture

- **Total parameters**: 25.2B (BF16: ~50 GB)
- **Active parameters**: 3.8B per token (top-8 experts selected)
- **Experts**: 128 per layer × 5 MLP-only layers = 640 expert modules
- **Hybrid layers**: 25 GDN (linear attention) + 5 full attention [5,11,17,23,29]

### GPTQ INT4 Result

Full MoE GPTQ quantization (GPTQModel >= 6.0.3) produces **7-13 GB** output that fits in 24 GB VRAM without CPU offload. This enables:

- 32K context window with 95% GPU memory utilization
- 16 concurrent sequences (MoE load-balances across experts)
- ~72 tok/s decode on gfx1100 via ExLlama kernels

### MoE-Specific Configuration

```yaml
quantization:
  format: GPTQ
  bits: 4
  groupSize: 128
  sym: true                     # required for ExLlama v2 kernels
  useGPU: true
  maxMemoryGB: 48
  timeoutSeconds: 43200         # 12h for 640 expert modules
  calibration:
    maxSeqLen: 2048
    maxSamples: 512             # 128 experts × top-8 needs high sample count
```

**Why 512 calibration samples?** Default 256 samples / 640 experts ≈ 0.4 samples per expert. Many experts may never be sampled, leading to uninformed quantization. 512 samples ensures better expert coverage.

**Disk offload**: The controller's Gemma4 model policy automatically enables `offload_to_disk=true` for Hessian computation. Without this, 640 expert Hessians (~100 MB each) would exceed 24 GB VRAM.

### MoE Abliteration Safety

Expert FFN weights (`block_sparse_moe/experts`) must NOT be abliterated — modifying expert routing weights corrupts the gating mechanism. Configure:

```yaml
abliteration:
  weightMatrices: ["o_proj"]    # ONLY shared attention output projection
  targetLayers: "auto"          # auto-skips GDN layers (25 of 30)
```

Double protection: the CRD restricts to `o_proj` AND `abliterate.py` auto-skips expert parameters.

### Gemma4 31B Dense GPTQ

The dense 31B variant (30.7B params, 60 layers) needs more resources:

```yaml
quantization:
  maxMemoryGB: 96               # 61 GB BF16 + quantization overhead
  timeoutSeconds: 28800         # 8 hours (2x layers vs 26B-A4B)
  calibration:
    maxSamples: 256             # no MoE, default is fine

abliteration:
  weightMatrices: ["o_proj", "down_proj"]  # safe for dense models
  maxMemoryGB: 96               # 61 GB BF16 weights
```

The 31B model requires a 128 GB RAM node (e.g. Radeon VII host). 64 GB nodes are too tight for abliteration + save overhead.

### Gemma4 Model Comparison

| Model | Type | BF16 Size | INT4 Size | VRAM Fit | Calibration | Est. Time |
|-------|------|-----------|-----------|----------|-------------|-----------|
| 26B-A4B | MoE | 50 GB | 7-13 GB | 24 GB | 512 samples | 12-24h |
| 31B | Dense | 61 GB | ~16 GB | 24 GB (tight) | 256 samples | 4-8h |

## Deployment Reliability

### GPUProfile Watch

The controller watches `GPUProfile` resources. When an administrator updates a quantizer image digest in a GPUProfile, the controller automatically:

1. Detects the hash change (resolved image is part of the spec hash)
2. Deletes running quantization jobs with the stale image
3. Recreates jobs with the new image

No manual intervention needed — updating the GPUProfile YAML triggers reconciliation.

### Image Drift Detection

If a quantization job is actively running and the GPUProfile image changes:

- Controller compares the running container image against the currently resolved image
- On mismatch, the stale job is deleted with a `QuantizerImageDrift` warning event
- A new job is created with the correct image on the next reconcile

### Script Version Marker

The quantizer image contains `quantize_gptq.py` with a version constant (`FLEXINFER_SCRIPT_VERSION`). The controller's wrapper script checks this at job startup:

```
Image has v6, controller expects v7 → FATAL: Script version mismatch. Rebuild quantizer image.
```

This prevents silent failures from stale images where runtime patches no longer match.

### Deploy Automation

Use the deploy script to automate the full quantizer build+deploy cycle:

```bash
# Build quantizer, push to registry, update GPUProfile, apply to cluster
make deploy-quantizer QUANTIZER_ARCH=gfx1100

# Above + rebuild controller + rollout restart
make deploy-quantizer-full QUANTIZER_ARCH=gfx1100

# With job restart
./scripts/deploy-quantizer.sh gfx1100 --restart-job gemma4-26b-a4b-gptq-quantize
```

## Backend Compatibility

- `GGUF`: `llamacpp`, `ollama`
- `AWQ`: `vllm`
- `GPTQ`: `vllm` (including AMD ROCm via ExLlama kernels)
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
- The download PVC uses `.flexinfer_cached*` marker files to skip repeat downloads. Changing the model source, revision, allow/ignore patterns, or model path requires deleting the stale PVC contents or the whole PVC so the controller can rebuild from a clean source. Interrupted downloads can leave metadata files without weights — check for the `.download_complete` marker and expected weight files, not directory contents alone.
- Changing `spec.quantization` or setting annotation `flexinfer.ai/requantize: "true"` resets only quantization-derived status and jobs. It does not delete the downloaded source directory or prior quantized outputs, so live models can keep serving the previous artifact until the new run succeeds.

**Quality gate fails:**
- Verify baseline/candidate eval prompts and dataset are identical.
- Verify acceptance units (0..1 vs 0..100) are correctly passed.
- For ROCm gfx1100 targets, prefer `GGUF` baselines first, then compare alternative formats.

**Gemma4 MoE quantization OOM:**
- 640 expert Hessians exceed 24 GB VRAM. Verify the Gemma4 model policy is applying `offload_to_disk=true`. Check `QUANTIZE_MODEL_POLICIES` env var in the job.
- Reduce `maxSamples` from 512 to 256 if the job still OOMs.

**Gemma4 quantization takes 12+ hours:**
- Normal for full MoE GPTQ: 640 experts × Hessian + quantize per expert. Set `timeoutSeconds: 43200` (12h) in the spec. The controller's `DefaultActiveDeadlineSeconds` is 86400 (24h).

**Script version mismatch at job startup:**
- The quantizer image has a stale `quantize_gptq.py`. Rebuild the image: `make deploy-quantizer QUANTIZER_ARCH=gfx1100`. The version check (`FLEXINFER_SCRIPT_VERSION`) prevents silent patch failures.

**GPUProfile image update didn't trigger re-quantization:**
- Verify the controller has the GPUProfile watch (requires controller image from commit `e6c08a55` or later). Check for `QuantizerImageDrift` events: `kubectl get events -n flexinfer-system --field-selector reason=QuantizerImageDrift`.

**Re-quantization after abliteration fix:**
- Must clean all pipeline markers. Delete jobs (`-abliterate`, `-quantize`), remove marker files (`.abliteration-status.json`, `.flexinfer-gptq-*`, `gptq-w4-g128/`), and reset the spec-hash annotation. The controller will restart from the appropriate phase.
