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

## Certificate-gated llama.cpp features

Stateful KV-slot snapshots and local n-gram speculation are explicit opt-ins.
On a GPU, the controller admits them only when the selected `GPUProfile`
contains a complete backend/capability certificate whose image exactly matches
the digest-pinned image that the model will launch. A complete certificate has
the artifact value plus `-since` and `-evidence` annotations. Missing evidence,
tag-only images, artifact drift, and untested parameter envelopes fail closed.

gfx906 currently certifies llama.cpp b8173 (`2e7e638`) for:

- `slotSavePath` under the persistent `/models` mount;
- `specType: ngram-simple` with exactly `draftMax: 16` and `draftMin: 1`.

The proven artifact is
`registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407`.
The persistent runtime image has not passed this certificate yet, so GPU models
must set `dedicatedDeployment: true`. gfx1100 remains locked until its own
artifact/hardware kill-test is recorded; the gfx906 certificate does not carry
across architectures or images.

```yaml
spec:
  backend: llamacpp
  config:
    dedicatedDeployment: true
    slotSavePath: /models/.flexinfer/slots/qwen3-8b
    specType: ngram-simple
    draftMax: 16
    draftMin: 1
```

The gfx906 kill-test saved and restored 536 tokens across a full server restart,
ran two parallel slots, accepted 528/528 draft tokens, and measured 2.99x the
baseline decode rate without sequence divergence. llama.cpp b8173 can underfill
the requested output by at most one final draft batch in this mode; clients that
require exact `max_tokens` filling should leave speculation disabled. See
`.loom/iteration-gfx906-llamacpp-stateful-spec-2026-07-15.md` for the pinned
artifact, acceptance gate, measurements, and restoration evidence.

See `build/README-gfx906.md` for detailed hardware documentation and troubleshooting.
