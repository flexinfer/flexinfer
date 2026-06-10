# GPU Compatibility Reference

Per-architecture backend support and required configuration. For the quick-start overview, see [AGENTS.md](../../AGENTS.md).

## Compatibility Matrix

| Backend | RDNA3 (7900XTX) | Vega20 (Radeon VII) | Maxwell (980Ti) | Notes |
|---------|-----------------|---------------------|-----------------|-------|
| Ollama | ✅ Full | ✅ Full | ✅ Full | Universal compatibility |
| vLLM | ✅ Full | ✅ Full | ❌ Not supported | gfx906: BUILD_FA=0 image |
| MLC-LLM | ✅ Full | ✅ Full | ⚠️ Pre-compiled only | Needs `modelLibPath` on Maxwell |
| llama.cpp | ✅ Full | ✅ Full | ✅ Full | GGUF format, arch-specific images |
| Diffusers | ✅ Full | ⚠️ Experimental | ❌ N/A | gfx906: runtime env override |
| ComfyUI | ✅ Full | ⚠️ Experimental | ❌ N/A | gfx906: runtime env override |

## Maxwell (GTX 980 Ti) Configuration

Maxwell GPUs (compute capability 5.x) require special handling:

1. **vLLM**: Not supported - use Ollama or llama.cpp instead
2. **MLC-LLM**: Requires pre-compiled model library
   ```yaml
   spec:
     backend: mlc-llm
     mlcllm:
       modelLibPath: /models/Model-q4f32_1-MLC/lib_cuda_maxwell.so
       gpuMemoryBytes: 5000000000  # 5GB limit for 6GB card
       jitPolicy: "OFF"
   ```

## RDNA3 (RX 7900 XTX) Configuration

Full support across all backends:

```yaml
spec:
  backend: mlc-llm
  mlcllm:
    mode: server
    modelLibPath: /models/Model-MLC/lib_rocm_gfx1100.so
    overrides:
      maxNumSequence: 2
      maxTotalSeqLength: 131072
      gpuMemoryUtilization: "0.85"
  nodeSelector:
    amd.com/gpu.arch: gfx1100
```

## ROCm gfx1100 Stability Requirements

PyTorch-based backends (diffusers, vLLM) on gfx1100 (RX 7900 XTX) require specific environment variables to prevent SIGSEGV crashes:

| Environment Variable | Value | Purpose |
|---------------------|-------|---------|
| `HSA_OVERRIDE_GFX_VERSION` | `11.0.0` | Enables RDNA3 GPU support |
| `PYTORCH_ROCM_ARCH` | `gfx1100` | Target architecture for PyTorch |
| `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL` | `1` | **Critical**: Enables experimental AOTriton flash attention |
| `HIP_VISIBLE_DEVICES` | `0` | GPU device selection |
| `ROCR_VISIBLE_DEVICES` | `0` | ROCm runtime device selection |

**Note**: The `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1` setting is essential for stability on gfx1100. Without it, PyTorch operations like attention can trigger SIGSEGV crashes.

These variables are automatically injected by the `ROCmEnvVars()` helper in `backend/interface.go` and are baked into all ROCm Dockerfiles.

## Vega20 (gfx906) Configuration

AMD Radeon VII / MI50 with 16GB HBM2 VRAM:

```yaml
spec:
  backend: llamacpp
  source: "HF://TheBloke/Mistral-7B-Instruct-v0.2-GGUF"
  modelFileName: "mistral-7b-instruct-v0.2.Q4_K_M.gguf"
  nodeSelector:
    flexinfer.ai/gpu.arch: gfx906
  resources:
    limits:
      amd.com/gpu: 1
  config:
    contextSize: 8192
    nGPULayers: 999
```

## ROCm gfx906 Environment

| Environment Variable | Value | Purpose |
|---------------------|-------|---------|
| `HSA_ENABLE_SDMA` | `0` | **Critical**: Disables SDMA engine to prevent memory faults |
| `PYTORCH_ROCM_ARCH` | `gfx906` | Target architecture |

**Note**: `HSA_ENABLE_SDMA=0` is essential on gfx906 (Vega20). Without it, the SDMA engine causes `HSA_STATUS_ERROR_MEMORY_APERTURE_VIOLATION` errors. Unlike gfx1100, do NOT set `HSA_OVERRIDE_GFX_VERSION` or `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL`.

These variables are automatically injected by `ROCmEnvVars()` in `backend/interface.go`.

See `build/README-gfx906.md` for detailed hardware documentation and troubleshooting.
