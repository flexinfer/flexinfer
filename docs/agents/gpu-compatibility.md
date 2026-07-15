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

## gfx1100 Video Generation

`deploy/models/wan21-t2v-1p3b-gfx1100.yaml` provides the first validated video
lane: Wan 2.1 T2V 1.3B at up to 832x480 through a dedicated ROCm 6.4.1 /
PyTorch 2.6 Diffusers image. It is cold by default (`minReplicas: 0`) and shares
the `7900xtx-textgen` GPU group, so a request temporarily preempts that node's
text model while the other gfx1100 workhorse remains available.

The synchronous endpoint returns one base64-encoded MP4. Frames must be `4k+1`
and no greater than 81; width and height must be multiples of 16 and no larger
than 832x480 by pixel count.

```bash
curl -sS http://flexinfer-proxy.flexinfer-system.svc/v1/videos/generations \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "video-gen",
    "prompt": "a red fox running through a meadow, cinematic tracking shot",
    "size": "832x480",
    "num_frames": 33,
    "fps": 16,
    "num_inference_steps": 20,
    "guidance_scale": 5,
    "seed": 42,
    "response_format": "b64_json"
  }' | jq -r '.data[0].b64_json' | base64 --decode > wan.mp4

ffprobe -v error -select_streams v:0 \
  -show_entries stream=codec_name,width,height,avg_frame_rate,nb_frames \
  -show_entries format=duration wan.mp4
```

The live 2026-07-14 gate produced exactly 33 H.264 frames at 832x480 and 16 fps
without OOM or restart. The 20-step request took 6m57s, so callers should allow
for a long synchronous response in addition to cold activation. Detailed
evidence and the immutable image digest are recorded in
`.loom/30-implementation-plan-video-gen-gfx1100-2026-07-14.md`.

To disable the lane, remove or delete the parent `Model`; do not scale or delete
its generated Deployment or pod because the controller recreates children.

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
