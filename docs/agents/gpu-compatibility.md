# GPU Compatibility Reference

Per-architecture backend support and required configuration. For the quick-start overview, see [AGENTS.md](../../AGENTS.md).

## Compatibility Matrix

| Backend | RDNA3 (7900XTX) | Vega20 (Radeon VII) | Maxwell (980Ti) | Notes |
|---------|-----------------|---------------------|-----------------|-------|
| Ollama | ✅ Full | ✅ Full | ✅ Full | Universal compatibility |
| vLLM | ✅ Full | ✅ Full | ❌ Not supported | gfx906: BUILD_FA=0 image |
| MLC-LLM | ✅ Full | ✅ Full | ⚠️ Pre-compiled only | Needs `modelLibPath` on Maxwell |
| llama.cpp | ✅ Full | ✅ Full | ✅ Full | GGUF format, arch-specific images |
| Diffusers | ✅ Full | ⚠️ Experimental | ⚠️ Experimental | gfx906: runtime env override; Maxwell: SD 1.5 only, CUDA 11.8 image |
| ComfyUI | ✅ Full | ⚠️ Experimental | ❌ N/A | gfx906: runtime env override |

Every Maxwell cell above is served by a dedicated per-model Deployment, never by a persistent runtime pod. See [Maxwell runtime posture](#maxwell-runtime-posture-gtx-980-ti).

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

## Maxwell runtime posture (GTX 980 Ti)

**Decision (2026-10-12): Maxwell runs dedicated pods only.** There is no
persistent runtime DaemonSet on `cblevins-gtx980ti`. Each Model on the node
gets its own Deployment, and the image comes from the `sm-52` GPUProfile's
`backends.<name>.image` pin in `deploy/gpuprofiles/sm_52.yaml`. The profile
has no `spec.runtime` block at all. The CRD requires `runtime.image`, so the
block could not be kept with only `bundledBackends`. Without it,
`RuntimeProfileBundlesBackend` (`pkg/runtime/payload.go`) is permissive,
`FindRuntimeForNode` (`controllers/runtime_controller.go`) finds no runtime pod
matching the node's hostname selector, and the Model controller takes the
Deployment path.

Images that actually serve on the node:

| Backend | Image (`sm_52.yaml`) | Built by |
|---|---|---|
| diffusers | `flexinfer/diffusers:cuda-maxwell-0b19f7a8@sha256:145ee552…` | CI `publish_diffusers_cuda` (`build/Dockerfile.diffusers-cuda`). This is the live lane, `dreamshaper8-imagegen-gtx980ti` (minReplicas 1). |
| llamacpp | `flexinfer/llamacpp@sha256:18e8cb08…` (tags `cuda-maxwell-652d1f2e`, `cuda-maxwell-2ae7dc2d`) | CI `publish_llamacpp_cuda_maxwell` (`build/Dockerfile.llamacpp-cuda-maxwell`). No Model in `deploy/models/kustomization.yaml` uses it today. |
| ollama | `flexinfer/ollama:cuda-maxwell` (also Helm `ollama.maxwellImage`) | CI `publish_ollama_maxwell`. No Model uses it today. |
| mlc-llm | `flexinfer/mlc-llm:cuda-maxwell-v7` | CI `publish_mlcllm_maxwell`. No Model uses it today. |

`flexinfer/runtime:cuda-maxwell` was never built. The profile used to pin it,
but Harbor's `flexinfer/runtime` repository has no cuda or maxwell tag (41
artifacts checked 2026-10-12) and no CI job publishes one. The `cuda-maxwell`
profile in `build/runtime.yaml` is a recipe with no job and no consumer.

Why the node has no runtime:

- The node has one 6 GiB GPU, and the warm image-gen lane holds it. A runtime
  pod would have no second workload to host.
- The `cuda-maxwell` runtime recipe sets `diffusers: false`, so the only live
  Maxwell workload could not run inside it.
- `charts/flexinfer/templates/runtime-daemonset.yaml` is AMD-only. It mounts
  `/dev/kfd` and `/dev/dri` and sets no `runtimeClassName: nvidia` and no
  `nvidia.com/gpu` request.
- **Ivy Bridge SIGILL hazard.** `build/Dockerfile.runtime` builds llama.cpp
  for `GPU_VENDOR=nvidia` with only `-DGGML_CUDA=ON -DGGML_NATIVE=OFF`. ggml
  then defaults AVX2, FMA and BMI2 to ON. The host CPU is an i7-3770K (Ivy
  Bridge: AVX, SSE4.2, F16C; no AVX2, FMA or BMI2), so any runtime built from
  that recipe would SIGILL on its first llama.cpp call.
  `build/Dockerfile.llamacpp-cuda-maxwell` shows the fix:
  `-DGGML_AVX2=OFF -DGGML_FMA=OFF -DGGML_BMI2=OFF`.

Preconditions for adding a Maxwell runtime (all of them):

1. A second Maxwell workload that is worth co-hosting with image-gen on the
   6 GiB card.
2. NVIDIA wiring in the runtime DaemonSet template: `runtimeClassName: nvidia`,
   an `nvidia.com/gpu` request, and no AMD device mounts.
3. A `cuda-maxwell` runtime build carrying the Ivy Bridge GGML flags, plus
   `diffusers: true` if image-gen moves into the runtime.
4. A CI job that publishes an immutable `runtime:cuda-maxwell-<sha>` tag.
5. A `runtime.profiles` entry in `deploy/system/values-k3s.yaml` for
   `cblevins-gtx980ti`.
6. `spec.runtime` restored in `sm_52.yaml` with a digest pin and an explicit
   `bundledBackends` list.

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

Stateful KV-slot snapshots, local n-gram speculation, and MTP speculation are
explicit opt-ins.
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

### `draft-mtp` (capability `draft-mtp`)

`specType: draft-mtp` drives the MTP (NextN) head that ships inside the target
GGUF; there is no separate `--model-draft`. The certified envelope is
llama.cpp's default draft window, so `draftMax` and `draftMin` are rejected
with this spec type: the backend renders them as `--draft-max`/`--draft-min`,
which llama.cpp removed by b11847 (the server aborts at startup), and on
Qwen3.5-family GGUFs (`qwen35.nextn_predict_layers=1`) b11847 reuses the single
head for each of its default three draft steps. Only multi-head models clamp
the window to their head count.

gfx1100 certifies the S5 image
`registry.harbor.lan/library/llamacpp@sha256:95c1d3901faba5fbbfa9ac173c4b40c479bcc604f1a41f873a975ce0cee0f2a1`
(b11847 / ROCm 7.2.3) for this capability. The controller reports
`Schedulable=False BackendUnsupported` for any other image or architecture.
The evidence is matrix row `gfx1100-llamacpp-b11847-2026-10-24`, measured on
the 5930k with Qwen3.8-27B Q4_K_M at 16K context and one slot, against the same
pod path without `--spec-type`:

- 4/4 coherence on both arms.
- Decode median 44.3 vs 31.3 tok/s (1.42x) on the 256-token story gauntlet.
- On greedy story / code / list prompts, 1.33x / 1.88x / 2.30x, with draft
  acceptance 42 / 75 / 97 %.

The gain is largest on structured or repetitive output. Greedy output is not
bit-identical to the non-speculative path: two of the three prompts flipped one
near-tie token early, from batched-verification numerics, and stayed coherent.
Clients that need exact reproducibility across the two paths should leave
speculation disabled.

```yaml
spec:
  backend: llamacpp
  image: registry.harbor.lan/library/llamacpp@sha256:95c1d3901faba5fbbfa9ac173c4b40c479bcc604f1a41f873a975ce0cee0f2a1
  config:
    dedicatedDeployment: true
    specType: draft-mtp
```

See `build/README-gfx906.md` for detailed hardware documentation and troubleshooting.
