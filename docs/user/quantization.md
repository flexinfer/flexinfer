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

## Backend Compatibility

- `GGUF`: `llamacpp`, `ollama`
- `AWQ`: `vllm`
- `GPTQ`: `vllm`
- `EXL2`: `exllamav2`
- `FP8`: `vllm`

If format/backend are incompatible, scheduling or startup will fail.

## Troubleshooting

- Quantization stuck in `Quantizing`:
  - `kubectl get jobs -n <ns> | grep quantize`
  - `kubectl logs job/<cache-name>-quantize -n <ns>`
- AWQ/GPTQ/EXL2/FP8 job fails quickly:
  - confirm `useGPU: true`
  - confirm quantizer image has required runtime dependencies (`awq`, `auto-gptq`, `exllamav2`, or FP8 tooling)
- Controller cannot pull quantizer image:
  - set `quantization.images.*` to reachable registry tags
- Quality gate fails:
  - verify baseline/candidate eval prompts and dataset are identical
  - verify acceptance units (0..1 vs 0..100) are correctly passed
  - for ROCm gfx1100 targets, prefer `GGUF` baselines first, then compare alternative formats
