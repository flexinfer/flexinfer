# Abliteration Pipeline

FlexInfer can remove the "refusal direction" from transformer model weights during `ModelCache` provisioning. This runs as an ordered phase before quantization.

## Overview

Abliteration removes the learned refusal behavior from LLMs by:

1. Running contrastive prompts (harmful vs harmless) through the model
2. Computing mean activation differences at each decoder layer
3. Orthogonalizing weight matrices against this "refusal direction"

The result is a model that responds to all prompts without refusal, suitable for uncensored deployment.

## Pipeline Order

```
Download (BF16) → Abliterate → [Finetune] → [Quantize] → Ready
```

Each phase blocks until the previous one reaches `Ready`. Abliteration modifies weights in-place on the PVC, so re-abliteration requires a fresh download.

## Declarative Configuration

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: my-model-gptq
  namespace: flexinfer-system
spec:
  source: "org/model-name"
  abliteration:
    numSamples: 256              # contrastive prompt pairs
    targetLayers: "auto"         # auto-detects full-attention layers
    weightMatrices:
      - o_proj                   # attention output projection
      - down_proj                # MLP down projection
    useGPU: true
    maxMemoryGB: 56
    timeoutSeconds: 14400        # 4 hours
    normThreshold: "100"         # abort if refusal norm exceeds
    ablitateLmHead: false        # MUST be false (see Known Issues)
    nodeSelector:
      flexinfer.ai/gpu.arch: gfx1100
```

## AbliterationSpec Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `numSamples` | int | 128 | Number of contrastive prompt pairs for activation capture |
| `targetLayers` | string | `"auto"` | Layer selection: `"auto"`, `"10-55"` (range), `"0,1,5,10"` (explicit) |
| `weightMatrices` | []string | `["o_proj", "down_proj"]` | Weight matrices to orthogonalize |
| `useGPU` | bool | false | Use GPU for activation capture (faster but needs VRAM) |
| `maxMemoryGB` | int | 56 | Container memory limit |
| `timeoutSeconds` | int | 14400 | Job deadline (4 hours default) |
| `normThreshold` | string | `"100"` | Max L2 norm of refusal direction. Abort if exceeded. |
| `ablitateLmHead` | bool | false | Abliterate the output projection. **Must be false.** |
| `skipGDNLayers` | bool | true | Skip GDN (linear attention) layers in hybrid architectures |
| `skipVisionLayers` | bool | true | Skip vision encoder layers in VLMs |
| `nodeSelector` | map | (inherited) | GPU-specific node constraints for abliteration job |

## Architecture-Specific Guidance

### Dense Models (e.g. Gemma4 31B, Qwen3.5 9B)

```yaml
abliteration:
  weightMatrices: ["o_proj", "down_proj"]  # safe for dense models
  maxMemoryGB: 96                          # 31B BF16 needs ~61 GB + overhead
```

Both `o_proj` and `down_proj` can be abliterated safely on dense models.

### MoE Models (e.g. Gemma4 26B-A4B)

```yaml
abliteration:
  weightMatrices: ["o_proj"]               # ONLY shared attention output
  maxMemoryGB: 56                          # 26B MoE needs less RAM (3.8B active)
```

Expert FFN weights (`block_sparse_moe/experts`) must NOT be abliterated. The `abliterate.py` script auto-skips expert parameters, but restricting `weightMatrices` to `["o_proj"]` provides double protection.

### Hybrid GDN Architectures

Gemma4 and Qwen3.5 use GatedDeltaNet (GDN) linear attention for most layers, with full attention at intervals:

| Model | Total Layers | GDN Layers | Full Attention | Abliterated |
|-------|-------------|------------|----------------|-------------|
| Gemma4 26B-A4B | 30 | 25 | 5 [5,11,17,23,29] | 5 layers |
| Gemma4 31B | 60 | 50 | 10 [5,11,...,59] | 10 layers |
| Qwen3.5 9B | 64 | 48 | 16 | 16 layers |

With `targetLayers: "auto"` and `skipGDNLayers: true` (default), only full-attention layers are abliterated. GDN layers use linear attention mechanisms that don't encode refusal the same way.

## Safety Features

### Norm Guard

If the computed refusal direction norm exceeds `normThreshold`, abliteration aborts. A high norm means the direction captures model capability (not just refusal behavior) — abliteration would degrade output quality.

### Perplexity Validation

After abliteration, the script computes perplexity on a validation set. If perplexity exceeds 50 or is NaN, abliteration is marked as failed. This prevents corrupted weights from wasting downstream quantization time.

### Streaming Save

Weights are saved using streaming safetensors (one module at a time) to avoid full model materialization. This is critical on nodes with limited RAM or NFS-backed PVCs.

## Memory Requirements

| Model Size | GPU VRAM | Container Memory | Est. Time |
|------------|----------|------------------|-----------|
| 9B | 16 GB | 32 Gi | ~20 min |
| 14B | 16 GB | 48 Gi | ~40 min |
| 26B MoE | 24 GB | 56 Gi | ~30 min |
| 31B Dense | 16 GB | 96 Gi | ~60 min |

### ROCm GPU Driver Memory Warning

Same as quantization: ROCm/HIP GTT allocations are outside the container cgroup. Set `maxMemoryGB` to `total_node_RAM - 20` for AMD GPU nodes.

### gfx906 (Radeon VII) Special Handling

The controller automatically applies gfx906 workarounds:

- `torch.cuda.mem_get_info` shim (VMM not supported on Vega20)
- `caching_allocator_warmup` skip (exceeds 16 GB VRAM)
- Safe sharded load (avoids meta tensor crash)
- GPU memory cap at 12 GB (not 20 GB)

## Spec Change Detection

The controller computes a SHA-256 hash of `AbliterationSpec` and stores it in annotation `flexinfer.ai/ablit-spec-hash`. Changes to any abliteration parameter trigger:

1. Delete the abliteration job + all downstream jobs (quantize, publish)
2. Reset download status (abliteration modifies weights in-place)
3. Restart from download phase

Manual trigger: set annotation `flexinfer.ai/reabliterate: "true"` to force re-abliteration.

## Status Fields

| Field | Description |
|-------|-------------|
| `layersModified` | Count of abliterated decoder layers |
| `refusalDirNorm` | L2 norm of mean refusal direction (diagnostic) |
| `abliterationTime` | Wall-clock duration |
| `progress` | 0-100% from structured telemetry |
| `progressDetail` | Human-readable status |
| `failureMessage` | Error logs on failure |

## Troubleshooting

**Abliteration stuck waiting for download:**
- Check download job status: `kubectl get jobs -n <ns> | grep downloader`
- Verify `.download_complete` marker exists on PVC.

**Norm threshold exceeded:**
- The refusal direction is too strong (captures capability). Try:
  - Increase `normThreshold` (e.g., `"200"`)
  - Reduce `numSamples`
  - Use explicit `targetLayers` to exclude problematic layers

**Perplexity validation failed:**
- Weights may be corrupted. Check `failureMessage` for details.
- Try reducing `weightMatrices` to just `["o_proj"]`.

**lm_head corruption (gibberish output):**
- **Root cause**: `ablitateLmHead: true` + accelerate disk offloading corrupts lm_head during streaming save. In-place weight modifications are lost when the model uses disk offloading.
- **Fix**: Set `ablitateLmHead: false` (default since 2026-04-01). If already corrupted, replace lm_head with original weights from the base model.

**Job OOMKilled on gfx906:**
- Set `FLEXINFER_ABLITERATION_GPU_MAX_MEMORY_GB=12` (not 20) and `FLEXINFER_ABLITERATION_CPU_MAX_MEMORY_GB=56` in GPUProfile env vars.
- The 16 GB Radeon VII VRAM is tight — aggressive CPU offload is needed.

**Re-abliteration requires re-download:**
- Abliteration modifies weights in-place. The controller automatically resets download status when abliteration spec changes. To manually trigger: delete the downloader job and remove `.download_complete` from the PVC.

## Known Issues

- `ablitateLmHead` must be `false` until `save_streaming_safetensors` is fixed to flush offloaded tensors before save (tracked since 2026-04-01).
- NFS PVC saves are slow (~30 min for 27 GB). Use NVMe-backed storage classes when possible.
- gfx906 abliteration is ~2x slower than gfx1100 due to CPU offloading overhead.
