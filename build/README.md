# build/ — Image Build Map

Authoritative map of every Dockerfile in this directory: what builds it, what image it produces, and who consumes it. Update this table when adding or removing a Dockerfile.

Audit rule (CI greps are substring-prone — use exact match):

```bash
for f in build/Dockerfile.*; do b=$(basename "$f"); \
  grep -rqE "${b}[\"' ]|${b}\$" .gitlab-ci.yml .gitlab/ Makefile build/*.sh scripts/ || echo "ORPHAN: $b"; done
```

A file with no CI/Makefile/script reference is either an orphan (delete it) or an **off-CI manual recipe** (document it in the off-CI table below). Heavy ROCm images are deliberately built off-CI on the `7900xtx` Docker context because the in-cluster buildkit nodes cannot hold large from-source builds.

Build-node disk pressure is tracked in GitLab issue
[#35](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/35). Before large
ROCm/CUDA rebuilds, run `scripts/check-build-node-disk.sh` and follow
[Docker build node disk management](../docs/dev/build-node-disk-management.md)
for BuildKit GC, pruning, alert thresholds, and free-space requirements.

## ⚠ The two-image vLLM trap

There are TWO separate vLLM image families. Bumping one does not fix the other:

| Image | Dockerfile | Built by | Used for |
|---|---|---|---|
| `flexinfer/vllm` (standalone) | `Dockerfile.vllm-gfx1100-qwen35-patched-nodiag` | CI `publish_vllm_rocm_gfx1100_qwen35_patched` | `backend: vllm` Models that get their own pod |
| `flexinfer/runtime` (DaemonSet) | `Dockerfile.runtime` | `build/build-runtime.sh <arch> --push` (off-CI, manual) | In-process vLLM/transformers in the runtime DaemonSet |

## Go component images (CI `release` stage, `build_image` helper)

| Dockerfile | Image | Notes |
|---|---|---|
| `Dockerfile.manager.bin` | `flexinfer-controller` | Binary copied in; built by CI release job |
| `Dockerfile.agent.bin` | `flexinfer-agent` | " |
| `Dockerfile.sched.bin` | `flexinfer-scheduler` | " |
| `Dockerfile.bench.bin` | `flexinfer-bench` | " |
| `Dockerfile.proxy.bin` | `flexinfer-proxy` | " |
| `Dockerfile.flash-loader.bin` | `flexinfer-flash-loader` | " |
| `Dockerfile.model-tools` | `model-tools` | " |

The old from-source variants (`Dockerfile.agent`, `.proxy`, `.manager`, `.sched`, `.bench`, `.flash-loader`) were removed 2026-06-12 — the `.bin` variants are canonical.

## Runtime family

| Dockerfile | Built by | Image / consumer |
|---|---|---|
| `Dockerfile.runtime` | `build/build-runtime.sh <arch> --push` (off-CI, manual, 30–60 min) | `flexinfer/runtime:<arch>` DaemonSet runtime; contract-checked by `scripts/check-runtime-patch-contracts.py` |
| `Dockerfile.runtime-serving` | `.gitlab/ci/runtime-publish.yml` | Serving-slim runtime |
| `Dockerfile.runtime-gfx906` | CI (changes-gated job in `.gitlab-ci.yml`) | gfx906 runtime variant |
| `Dockerfile.runtime-torch-multiarch` | `build/build-torch-multiarch.sh` (off-CI, manual-only) | `flexinfer/torch:rocm6.3.4-multiarch` — one torch wheel carrying gfx906+gfx1100 device code |
| `Dockerfile.unified-gfx906` | CI `publish_unified_rocm_gfx906` | `flexinfer/runtime:unified-gfx906` — community PyTorch, replaces old per-task gfx906 images (incl. quantize via `USE_RUNTIME_FOR_QUANTIZE`) |

## vLLM family

| Dockerfile | Built by | Image / consumer |
|---|---|---|
| `Dockerfile.vllm-gfx1100-qwen35-patched-nodiag` | CI `publish_vllm_rocm_gfx1100_qwen35_patched` | `flexinfer/vllm` standalone (see trap above) |
| `Dockerfile.vllm-rocm` | CI `publish_vllm_rocm` | `library/vllm-api:rocm-navi` |
| `Dockerfile.vllm-rocm-gfx1100` / `-fa` | CI `publish_vllm_rocm_gfx1100` / `_fa` | `library/vllm-api:rocm-gfx1100` (+ flash-attn variant) |
| `Dockerfile.vllm-rocm-gfx906` / `-fa` | CI `publish_vllm_rocm_gfx906` / `_fa` | gfx906 vllm-api (+ flash-attn variant) |
| `Dockerfile.vllm-rocm-multiarch` / `-serve` | `build/build-vllm-multiarch.sh` / `-serve.sh` (off-CI) | `flexinfer/vllm:rocm6.3.4-multiarch[-serve]` — unified gfx906+gfx1100; `-serve` digest pinned by gfx906 GPUProfile |
| `Dockerfile.vllm-nightly-rocm-gfx1100` | `make` target (`DOCKER_CONTEXT_GPU`) | nightly vLLM experiment image |
| `Dockerfile.vllm-omni-rocm-gfx1100` | `make` target | `flexinfer/vllm-omni:rocm-gfx1100` (GPUProfile ref) |

## Backend images

| Dockerfile | Built by | Image / consumer |
|---|---|---|
| `Dockerfile.llamacpp-rocm-gfx1100` | CI `publish_llamacpp_rocm_gfx1100` + `make` | gfx1100 llama.cpp server |
| `Dockerfile.llamacpp-rocm-gfx906` | CI `publish_llamacpp_rocm_gfx906` + `make` | gfx906 llama.cpp (`library/llamacpp:rocm-gfx906-patched-*`, hipMemGetInfo patch) |
| `Dockerfile.llamacpp-cuda-maxwell` | CI `publish_llamacpp_cuda_maxwell` + `make` | GTX 980 Ti (sm_52, no-AVX2 host) |
| `Dockerfile.ollama-cuda-maxwell` | CI `publish_ollama_maxwell` + `make` | `flexinfer/ollama:cuda-maxwell` |
| `Dockerfile.mlc-cuda` | CI `publish_mlcllm_cuda` | MLC-LLM CUDA |
| `Dockerfile.mlc-cuda-maxwell` | CI `publish_mlcllm_maxwell` + `make` | `flexinfer/mlc-llm:cuda-maxwell-*` |
| `Dockerfile.mlc-rocm` | CI `publish_mlcllm_rocm` | MLC-LLM ROCm (legacy) |
| `Dockerfile.mlc-rocm64-full` | CI `publish_mlcllm_rocm64` (manual) + `make` | `library/mlc-llm:rocm64-src` (from-source) |
| `Dockerfile.mlc-rocm64-gfx906` | `make` target | `flexinfer/mlc-llm:rocm64-gfx906` (GPUProfile ref) |
| `Dockerfile.comfyui-rocm-gfx1100` | CI `publish_comfyui_rocm_gfx1100` | `flexinfer/comfyui:rocm-gfx1100` (GPUProfile ref) |
| `Dockerfile.comfyui-rocm-gfx906` | CI `publish_comfyui_rocm_gfx906` | `flexinfer/comfyui:rocm-gfx906` (GPUProfile ref) |
| `Dockerfile.diffusers-cuda` | CI `publish_diffusers_cuda` + `make` | diffusers CUDA |
| `Dockerfile.diffusers-rocm` | CI `publish_diffusers_rocm_gfx1100` + `make` | `library/diffusers-api:rocm-*` / `flexinfer/diffusers:rocm-gfx1100` |
| `Dockerfile.diffusers-rocm-gfx906` | CI `publish_diffusers_rocm_gfx906` + `make` | `flexinfer/diffusers:rocm-gfx906` (GPUProfile ref) |

## Quantizer images

| Dockerfile | Built by | Image / consumer |
|---|---|---|
| `Dockerfile.quantizer-gptq` | CI `build_quantizer_gptq` | CUDA GPTQ quantizer (gptqmodel 7.x / torch 2.8 pin set — see MR !612) |
| `Dockerfile.quantizer-awq` | CI `build_quantizer_awq` | CUDA AWQ quantizer |
| `Dockerfile.quantizer-gguf` | CI `build_quantizer_gguf` | GGUF quantizer |

## Off-CI manual recipes (referenced images, no CI job — do NOT delete)

These look orphaned to a CI-only audit but are the only build recipes for images pinned by live config:

| Dockerfile | Image | Pinned by |
|---|---|---|
| `Dockerfile.pyannote-rocm-gfx906` | `flexinfer/pyannote-diarization@sha256:…` | `deploy/system/pyannote-diarization/deployment.yaml` (live voice stack) |
| `Dockerfile.quantizer-gptq-rocm` | `flexinfer/quantizer:gptq-rocm-gfx1100` | `pkg/quantization/gpu_job.go` (`DefaultGPTQROCmImage`) + `deploy/system/values-k3s.yaml` |
| `Dockerfile.quantizer-awq-rocm` | `flexinfer/quantizer:awq-rocm-gfx1100` | `deploy/system/values-k3s.yaml` (`images.quantization.awq`) |
| `Dockerfile.mlc-rocm64-gfx1100` | `flexinfer/mlc-llm:rocm64-gfx1100` | `deploy/gpuprofiles/` |

## Removed 2026-06-12 (recoverable from git history)

`abliteration-overlay`, `ablitfix`, `mlc-rocm64-build`, `mlc-rocm64-hipblas`, `quantizer-gptq-rocm-gfx906` (superseded by `unified-gfx906`), `quantizer-overlay`, `runtime-patch` (patch wiring lives in `Dockerfile.runtime` now), `vllm-gfx1100-v018`, and the six from-source Go component Dockerfiles plus `comfyui-rocm`, `vllm-omni-rocm` (superseded base variants). Harbor tags built from them keep serving; the Dockerfiles are frozen in history at commit `3749d287^`.
