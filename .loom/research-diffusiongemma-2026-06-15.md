# Research: DiffusionGemma 26B-A4B-it — flexinfer support & quant features

- **Date**: 2026-06-15
- **Author**: claude-code
- **Model**: [`google/diffusiongemma-26B-A4B-it`](https://huggingface.co/google/diffusiongemma-26B-A4B-it)
- **Question**: What support and quantization features exist for DiffusionGemma, and can flexinfer's ROCm serving + quant pipeline run it?
- **Verdict**: 🚧 **Not a drop-in.** New (2026-06-10) diffusion-MoE architecture. vLLM support is NVIDIA-only + needs vLLM 0.24.0+; llama.cpp needs an unmerged PR; flexinfer's GPTQ/abliteration pipeline does not apply. Same Gemma-4 26B-A4B backbone as the existing `gemma4-26b-a4b` lane, whose AR sibling is the supported option today.

## Riskiest assumption + kill-test

**Load-bearing assumption**: flexinfer's existing ROCm serving stack (standalone `vllm:rocm6.3.4-multiarch` 0.6.3 + the runtime V1 vLLM, gfx1100/gfx906) or the llama.cpp lane can serve DiffusionGemma's block-diffusion inference without an upstream ROCm dLLM port.

**Kill test** (≤30 min, observable): On a gfx1100 node, attempt `vllm serve google/diffusiongemma-26B-A4B-it` with the documented `--hf-overrides '{"diffusion_sampler":"entropy_bound",...}'` using flexinfer's runtime vLLM image. Expected unambiguous failure: vLLM rejects `model_type: diffusion_gemma` (no `ModelState` registration in this version) or errors on the bidirectional/denoise path. Confirm version gap: `python -c "import vllm; print(vllm.__version__)"` in the runtime image vs the required **0.24.0+**.

**Failure mode if the assumption is wrong**: building a serving lane (GPUProfile, proxy wiring, prefetch) for a model the engine can't actually execute on AMD — re-running the "wire format correct, nothing renders" class of waste.

**Status**: **FAILED 2026-06-15** (by documentation evidence, not live run) — vLLM dLLM path is documented NVIDIA H100/H200-only and requires vLLM 0.24.0+ (flexinfer standalone is 0.6.3); llama.cpp diffusion needs unmerged PR #24423 + custom `llama-diffusion-cli`. A live kill-test would confirm in <30 min but the published support matrix already disconfirms the assumption.

## What it is (verified from live `config.json` + model card)

| Property | Value |
|---|---|
| `model_type` | `diffusion_gemma` (text: `diffusion_gemma_text`) |
| `architectures` | `DiffusionGemmaForBlockDiffusion` |
| transformers | **`5.8.0.dev0`** (unreleased dev build) |
| Params | 25.2B total / **3.8B active** |
| MoE | `num_experts: 128`, `top_k_experts: 8`, `moe_intermediate_size: 704` |
| Backbone | **Gemma-4 26B-A4B** (30 layers, full-attn @ {5,11,17,23,29}, sliding_window 1024, 256K ctx, `tie_word_embeddings`) — same as flexinfer `gemma4-26b-a4b` |
| Modality | Multimodal: text + image + video → text (`image-text-to-text`) |
| Size | ~50.5 GB BF16, 11 safetensors shards |
| HF stats | 311,788 downloads, 874 likes (2026-06-15); `gated: false`, Apache-2.0 |

**Generation paradigm** (the key difference): encoder-decoder *discrete diffusion*, not causal AR.
- AR encoder prefills → KV cache. Decoder applies **bidirectional attention** over a 256-token "canvas," cross-attends cached context.
- **Block-autoregressive multi-canvas sampling**: denoises 15–20 tokens per forward pass (≤48 denoise steps, entropy-bounded adaptive stopping). 1200+ tok/s @ batch-1 on **H100 FP8**.

## Engine support

| Engine | Status | Catch |
|---|---|---|
| transformers | ✅ Official | Needs `5.8.0.dev0` dev build, `DiffusionGemmaForBlockDiffusion` |
| vLLM | ✅ "First dLLM natively supported" | **vLLM 0.24.0+**, special `vllm/vllm-openai:gemma` image, model-runner-v2 `ModelState`. Docs list **NVIDIA H100/H200 only — no AMD/ROCm**. BF16. `--hf-overrides '{"diffusion_sampler":"entropy_bound","diffusion_entropy_bound":0.1}'`, `--max-num-seqs 4`, `--max-model-len 262144` |
| llama.cpp | ⚠️ Unmerged PR | **PR #24423** + custom `llama-diffusion-cli` (not `llama-server`); CUDA + Apple Metal; AMD not mentioned |

## Quant features

- **GGUF (community, [`unsloth/diffusiongemma-26B-A4B-it-GGUF`](https://huggingface.co/unsloth/diffusiongemma-26B-A4B-it-GGUF))**: Q4_K_M ≈16 GB (~18 GB RAM), Q8_0, plus 5/6/8-bit. BF16 ≈52 GB. **Runnable only via the llama.cpp diffusion PR build.**
- **No GPTQ/AWQ pre-quants** found. **No vLLM quant path** documented for the dLLM (BF16 only on that path).

## flexinfer applicability — 3 blockers

1. **Serving (ROCm vLLM): blocked.** Needs vLLM 0.24.0+ with diffusion `ModelState`/special image; flexinfer standalone is `0.6.3`, runtime V1 far behind. dLLM path is documented NVIDIA-only — no published ROCm support for the bidirectional/denoise kernels on gfx1100/gfx906.
2. **llama.cpp: experimental.** Unmerged PR + non-server CLI, AMD unconfirmed. flexinfer builds standard `ggml-org/llama.cpp` and serves via `llama-server` → no fit without a custom ROCm build of the diffusion branch.
3. **GPTQ/abliteration pipeline: doesn't apply.** `gptqmodel` won't recognize `diffusion_gemma` (would need a remap like the shipped qwen3.5-moe fix, commit 89d12614), *and* encoder-decoder + bidirectional attention + diffusion objective break GPTQ's causal-LM calibration assumptions. Abliteration refusal-direction extraction assumes causal decoder activations — not meaningful here.

## Recommendation

- **Today**: the AR sibling `google/gemma-4-26B-A4B-it` (already `gemma4-26b-a4b-gptq`, the daily driver) is the same backbone and is supported now.
- **Near-term DiffusionGemma path**: the unsloth **Q4_K_M GGUF (~16 GB, fits one gfx1100)** *if* the llama.cpp diffusion PR is built for ROCm — a candidate for the **experiment-platform** trial directive ([[project_experiment_platform]]), gated on ROCm dLLM support landing upstream.
- **Watch item**: re-run the kill-test when (a) flexinfer's vLLM reaches a version with diffusion `ModelState` AND has a ROCm build, or (b) llama.cpp PR #24423 merges with HIP support.

## Sources

- [HF model card](https://huggingface.co/google/diffusiongemma-26B-A4B-it) · live `config.json` + `README.md` + `/api/models` (HTTP 200, `model_type: diffusion_gemma`)
- [vLLM blog: first dLLM natively supported](https://vllm-project.github.io/2026/06/10/diffusion-gemma.html)
- [vLLM recipe (requirements: vLLM 0.24.0+, H100/H200, BF16)](https://recipes.vllm.ai/Google/diffusiongemma-26B-A4B-it)
- [unsloth GGUF](https://huggingface.co/unsloth/diffusiongemma-26B-A4B-it-GGUF) · [unsloth docs (llama.cpp PR #24423, llama-diffusion-cli)](https://unsloth.ai/docs/models/diffusiongemma)
- [Google developer guide](https://developers.googleblog.com/diffusiongemma-the-developer-guide/)
