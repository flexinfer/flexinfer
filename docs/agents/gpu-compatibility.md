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
without OOM or restart. A matched warm eager request took 142.180s; compiling
the Wan transformer in place with `max-autotune-no-cudagraphs` reduced the
matched warm request to 106.123s (25.36%) and denoising from 125.493s to
90.875s (27.59%). The first request that populated the compilation cache took
375.484s, so cold callers must still allow for activation and a one-time compile
pass. Detailed evidence and immutable image digests are recorded in
`.loom/30-implementation-plan-video-gen-gfx1100-optimization-2026-07-14.md`.

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
| `HSA_USE_SVM` | `0` | Avoids unsupported Vega20 VMM/SVM memory probes |
| `PYTORCH_ROCM_ARCH` | `gfx906` | Target architecture |

**Note**: `HSA_ENABLE_SDMA=0` is essential on gfx906 (Vega20). Without it, the SDMA engine causes `HSA_STATUS_ERROR_MEMORY_APERTURE_VIOLATION` errors. Unlike gfx1100, do NOT set `HSA_OVERRIDE_GFX_VERSION` or `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL`.

These variables are automatically injected by `ROCmEnvVars()` in `backend/interface.go`.

## Qwen3.5 native MTP canary certificate

gfx906 has a canary certificate for dense Qwen3.5-9B GPTQ with one native MTP
draft token. The certificate binds this exact pair:

- runtime: `registry.harbor.lan/flexinfer/vllm@sha256:034f081861278a680fe54ddeb71db6446ce65f0a9c37ce9aecc061a99b1d40fc`;
- graft contract: `sha256:64189493708ff203f65a08e0ebde92cf9998271212b69cb390173a694f453134`.

The proven server uses eager mode, `TRITON_ATTN`, `maxNumSeqs: 1`,
`maxNumBatchedTokens: 256`, and `gpuMemoryUtilization: 0.80`. It also strips
the raw artifact's multimodal M-RoPE keys through `hfOverrides`. The 80% limit
is part of the certificate: 90% allowed KV allocation to consume Vega20's
GPTQ workspace floor and the first 256-token prefill returned
`hipErrorInvalidValue`.

The kill test completed a 9,001-token request at 16K and an 18,001-token
request at 32K. Two meaningful greedy MTP requests matched the baseline output
and accepted 8/12 draft tokens. Use
`deploy/debug/gfx906-qwen35-mtp-long-context-kill-test.yaml` to reproduce the
contract. Full evidence and artifact provenance are in
`.loom/iteration-gfx906-qwen35-mtp-long-context-2026-07-16.md`.

This certificate does not replace the generic gfx906 vLLM profile image.
Models must opt into the pinned image and exact artifact/config tuple.

## Qwen3.6-27B Fable Fusion GPTQ candidate

DavidAU's `Qwen3.6-27B-Fable-Fusion-711-...-MTP-GGUF` has an exact BF16
safetensors source at
`nightmedia/Qwen3.6-27B-Architect-Polaris2-Fable-B-F451`, revision
`5ae530c3ab85033856e75cb1efc63fb1bf82a133`. DavidAU identified that source in
the GGUF repository's discussion #3. Use the BF16 source for GPTQ; do not
dequantize a GGUF and quantize it again.

The staged build is `deploy/modelcaches/qwen36-27b-fable-gptq.yaml`. It emits a
text-only, symmetric W4/G128 artifact with a source-specific 128 x 1024
calibration policy. The first serving lane is deliberately demand-only at 8K
and uses the persistent gfx1100 runtime. Native MTP is not part of v1: the
source contains 14 `mtp.*` tensors, while FlexInfer's certified dense Qwen3.5
graft contract requires 15, including `fc.weight`.

Upstream vLLM's current compatibility table marks GPTQ unsupported on AMD GPU.
FlexInfer support is therefore a local runtime certificate, not portable
upstream support. The load-bearing patch skips `gptq_shuffle` for ROCm 4-bit
GPTQ; without it, projections load successfully but generate deterministic
token salad. Never point this artifact at a stock vLLM ROCm image.

The riskiest assumption is that the fully quantized GDN projections proven
coherent for the 9B Qwen3.5 artifact remain coherent for dense Qwen3.6-27B on
the same shuffle-guarded runtime. Publication remains warning-first, but
promotion requires a deterministic greedy coherence test and a multi-prompt
quality smoke on physical gfx1100.

Sources:

- https://huggingface.co/DavidAU/Qwen3.6-27B-Fable-Fusion-711-Uncensored-Heretic-NM-DAU-NEO-MAX-MTP-GGUF/discussions/3
- https://huggingface.co/nightmedia/Qwen3.6-27B-Architect-Polaris2-Fable-B-F451
- https://docs.vllm.ai/en/latest/features/quantization/
- https://docs.vllm.ai/en/latest/features/speculative_decoding/mtp/

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
