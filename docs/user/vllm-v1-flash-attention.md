# vLLM V1 Engine and Flash Attention on ROCm

## Overview

FlexInfer's vLLM backend ships with the V0 engine and flash attention disabled on ROCm by default. This provides a stable baseline on both gfx1100 (RX 7900 series) and gfx906 (Radeon VII) architectures.

As upstream ROCm support matures, you can opt into experimental features per-model via the Model CRD `spec.config` fields.

## Config Keys

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `vllmEngineVersion` | string | `"v0"` | `"v0"` or `"v1"`. V1 engine uses Triton-based attention. |
| `enableFlashAttention` | bool | `false` | Enable Triton flash attention kernels. |
| `enableAiter` | bool | `false` | Enable AITER (Asynchronous Iteration). gfx1100 only. |

## Prerequisites

### Flash Attention Image

The default vLLM images are built with `BUILD_FA=0` (flash attention disabled at compile time). To use flash attention, you must use a FA-enabled image by setting the controller environment variable:

```bash
# For gfx1100
DEFAULT_VLLM_IMAGE_GFX1100=registry.harbor.lan/flexinfer/vllm:rocm-gfx1100-fa

# For gfx906
DEFAULT_VLLM_IMAGE_GFX906=registry.harbor.lan/flexinfer/vllm:rocm-gfx906-fa
```

These images are built from `build/Dockerfile.vllm-rocm-gfx1100-fa` and `build/Dockerfile.vllm-rocm-gfx906-fa` respectively.

## Example YAML

### V1 Engine Only (no flash attention)

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: llama-7b-v1
spec:
  backend: vllm
  source: "HF://meta-llama/Llama-2-7b-chat-hf"
  config:
    vllmEngineVersion: "v1"
```

### V1 Engine + Flash Attention

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: llama-7b-v1-fa
spec:
  backend: vllm
  source: "HF://meta-llama/Llama-2-7b-chat-hf"
  config:
    vllmEngineVersion: "v1"
    enableFlashAttention: true
```

### Full Opt-In (V1 + FA + AITER, gfx1100 only)

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: llama-7b-full-optin
spec:
  backend: vllm
  source: "HF://meta-llama/Llama-2-7b-chat-hf"
  config:
    vllmEngineVersion: "v1"
    enableFlashAttention: true
    enableAiter: true
```

## Expected Behavior

When you opt into these features, the controller:

1. Sets the corresponding environment variables (`VLLM_USE_V1=1`, `VLLM_USE_TRITON_FLASH_ATTN=1`, `VLLM_ROCM_USE_AITER=1`) on the vLLM container.
2. Emits informational Kubernetes events (`V1EngineOptIn`, `FlashAttentionOptIn`) so you can track which models use experimental features.

```bash
kubectl describe model llama-7b-v1-fa
# Events:
#   Normal  V1EngineOptIn       vLLM V1 engine enabled via spec.config.vllmEngineVersion=v1 (experimental)
#   Normal  FlashAttentionOptIn Triton flash attention enabled via spec.config.enableFlashAttention=true (experimental)
```

## Known Issues Per Architecture

### gfx1100 (RX 7900 XTX / XT)

- V1 engine may cause GPU hangs under sustained load with certain models.
- Triton flash attention may trigger SIGSEGV on models with unusual head dimensions.
- AITER is experimental and designed for MI300X; behavior on RDNA3 is best-effort.

### gfx906 (Radeon VII)

- V1 engine is less tested on Vega20 than on RDNA3.
- Triton flash attention compilation is slower on gfx906 (first inference penalty).
- `enableAiter` is ignored on gfx906 (not applicable to GCN5 architecture).

## Rollback

To revert to the safe baseline, remove the config keys:

```yaml
spec:
  config: {}
```

Or explicitly set the defaults:

```yaml
spec:
  config:
    vllmEngineVersion: "v0"
    enableFlashAttention: false
    enableAiter: false
```

The controller uses `"v0"` / `false` as defaults, so omitting the keys is equivalent to setting them explicitly.
