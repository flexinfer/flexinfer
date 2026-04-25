# Gemma4 26B/31B GPTQ + TurboQuant Plan

Date: 2026-04-25

## Goal

Get two production-usable Gemma4 gfx1100 serving lanes:

- `gemma4-26b-a4b`: abliterated, GPTQ weight-quantized, coherent at the chosen context target, and optionally promoted to a TurboQuant KV-cache lane only after quality gates pass.
- `gemma4-31b`: abliterated, GPTQ weight-quantized, repaired from the current corrupt artifact state, and then evaluated with TurboQuant only after the clean GPTQ lane is proven.

This plan intentionally separates GPTQ from TurboQuant:

- GPTQ is the stored weight quantization artifact produced by `ModelCache` jobs.
- TurboQuant is runtime KV/vector compression in the vLLM image. It is not a replacement for weight quantization and should not be used to hide a bad GPTQ artifact.

## Current Truth

### 26B A4B

- Current primary manifest is `deploy/models/gemma4-26b-a4b-gptq.yaml`.
- The active safe artifact is hybrid: MoE expert weights are GPTQ INT4 while dense attention/MLP remains source precision. The manifest notes that full dense GPTQ produced bad dequant/repeated-token output on ROCm/vLLM.
- The hybrid artifact loads at about 17.7 GiB on the 24 GiB 7900 XTX lane, so the current safe context is 8K. The docs warn not to raise context until a smaller validated artifact exists.
- The primary manifest requires `flexinfer.ai/gpu.count: "2"` because the node exposes an AMD dGPU plus iGPU through the device plugin. A count of `1` can place the pod on the iGPU and crash.
- The long canary manifest currently targets 32K with fp16 KV, but its selector still uses `flexinfer.ai/gpu.count: "1"`. That should be corrected before spending validation time.

### 31B Dense

- Current primary manifest is `deploy/models/gemma4-31b-gptq.yaml`.
- The current `gptq-w4-g128-keqv` artifact can load and allocate KV at `maxModelLen: 1920`, but inference emits pure `<pad>`.
- Manifest evidence points away from the `k_eq_v` post-process as the root cause. Layers 40-59 have repeated tensors across q/k/v/o/gate/up families, so the current artifact cannot be repaired by on-disk `k_eq_v` surgery.
- A clean 31B re-quant is required before any 31B TurboQuant work is meaningful.
- The earlier 31B TurboQuant lane OOMed before KV allocation: about 20.02 GiB weights plus about 3.57 GiB TurboQuant plugin state on a 23.98 GiB GPU. `gpuMemoryUtilization` and CPU offload do not fix that allocation path.

## External Findings

- Google Gemma4 model docs list 256K context for both Gemma4 31B Dense and 26B A4B, with Q4 base-weight memory around 17.4 GB for 31B and 15.6 GB for 26B A4B. Those figures exclude runtime overhead, KV, plugin state, fragmentation, and vLLM allocator behavior.
- vLLM documents ROCm support for Radeon RX 7900 series / gfx1100 on modern ROCm, but older ROCm guidance and repo history still support keeping `TRITON_ATTN`, `VLLM_ROCM_USE_AITER=0`, and fallback attention paths available for Gemma4.
- GPTQModel reports Gemma 1-4 and AMD ROCm support, making it the right weight-quantization base compared with older AutoGPTQ paths.
- TurboQuant public material describes KV/vector compression, not model-weight GPTQ. Treat it as a runtime memory lever after artifact correctness is proven.
- Community llama.cpp/TurboQuant testing reports global TurboQuant over Gemma4 26B A4B MoE can catastrophically degrade perplexity, especially without sliding-window attention bypass. Treat this as a risk signal for layer-selective TurboQuant, not as proof that 26B is impossible.

Key external sources:

- https://ai.google.dev/gemma/docs/core
- https://ai.google.dev/gemma/docs/core/model_card_4
- https://research.google/blog/turboquant-redefining-ai-efficiency-with-extreme-compression/
- https://arxiv.org/html/2504.19874v1
- https://docs.vllm.ai/en/stable/getting_started/installation/gpu/
- https://docs.vllm.ai/en/stable/features/quantization/quantized_kvcache/
- https://rocm.docs.amd.com/en/docs-6.4.3/how-to/rocm-for-ai/inference-optimization/model-quantization.html
- https://github.com/modelcloud/gptqmodel
- https://github.com/ggml-org/llama.cpp/discussions/21526

## Git History Evaluation

The relevant git history points to a pipeline that has already learned several expensive lessons:

- `ee19d88e` and `8afc8ff8` moved the stack to GPTQModel and ROCm/gfx906-compatible quantization.
- `e6f9b5bb`, `82bdad84`, `f519a830`, `badbc14f`, `602d086d`, and `bde445f0` hardened the Gemma4 pipeline and moved 26B A4B toward full MoE expert quantization.
- `96d091e3` removed TurboQuant from 26B after page-size mismatch and reduced the default context. This is why 26B should re-enter TurboQuant through a canary, not the primary lane.
- `3b6f52b9` kept Hessian on device after CPU LAPACK failed in the ROCm/PyTorch image. That explains why 31B quantization must budget GPU and CPU memory carefully instead of assuming CPU solve fallback is available.
- `0fb31ecd` added GPTQ resume and per-layer reload behavior. Use it, but add integrity checks so resume cannot silently preserve repeated late-layer tensors.
- `078914ae` moved 31B `k_eq_v` work into the Flux-managed task cascade. Keep the post-process after clean quantization, but do not treat it as a cure for repeated tensor corruption.
- `b64f0502` closed the 31B TurboQuant 24 GiB long-context lane because the plugin allocation OOMed before KV sizing.

Correct reading of the history: do not force TurboQuant onto the current artifacts. Produce clean GPTQ artifacts first, then use TurboQuant as a separately gated runtime optimization.

## Correct Direction

1. Preserve the current 26B hybrid 8K lane as the known-good fallback.
2. Fix the 26B long canary selector to require the real dGPU lane before probing 16K/32K.
3. Finish or rerun the 26B dense-validated artifact path with strict output and cosine checks. Promote only if it is coherent and smaller than the hybrid artifact.
4. Rebuild 31B from source with corruption guards before `k_eq_v`; current 31B `keqv` output is not recoverable.
5. Re-apply `k_eq_v` only after the clean 31B quantization passes artifact integrity checks.
6. Keep 31B production capped at a conservative context until it passes text coherence, artifact integrity, and a context ladder.
7. Patch TurboQuant primitive allocation only after clean GPTQ lanes exist. The 31B fix should share immutable rotation/codec primitives across layers and avoid split rotation allocations when the PyTorch codec is enabled on ROCm.
8. Reintroduce TurboQuant through canaries:
   - 26B: layer-selective or SWA-aware first, because global MoE TurboQuant has external quality-risk signals.
   - 31B: boot-only first, then 4096, 8192, 16384 context ladder if primitive sharing gets past the previous OOM.
9. Treat FP8 KV cache as an independent lower-risk canary, not a substitute for TurboQuant validation.

## Workstreams

### A. Guardrails Before GPU Time

Acceptance criteria:

- `gemma4-26b-a4b-gptq-long.yaml` uses the same dGPU-safe selector as primary 26B.
- 31B quantization emits an integrity manifest that detects repeated q/k/v/o/gate/up tensors before `k_eq_v`.
- Resume mode refuses to reuse a cached late layer when tensor signatures collide unexpectedly.
- Save/finalization never deletes source checkpoints before artifact validation and durable copy complete.

### B. 26B Fully Working Lane

Steps:

1. Keep `gemma4-26b-a4b-gptq` warm/primary at 8K until a replacement passes gates.
2. Correct the 32K long canary selector.
3. Run the long canary first at 16K, then 32K with the existing fp16 KV path.
4. Finish dense-validated rebuild from `deploy/modelcaches/gemma4-26b-a4b-gptq-dense.yaml`.
5. Compare hybrid vs dense-validated:
   - artifact validator clean,
   - short generation coherence,
   - no repeated-token collapse,
   - VRAM at load,
   - max context reached.
6. Promote the smallest coherent artifact. If dense GPTQ still degrades output, keep hybrid as primary and document the dense path as blocked.

### C. 31B Fully Working Lane

Steps:

1. Treat current `gptq-w4-g128-keqv` as corrupt and non-promotable.
2. Re-quantize 31B on the gfx906/Radeon VII pipeline with current 12h timeout and 96 GB memory budget, but add late-layer corruption guards.
3. Reduce corruption risk with lower `n_parallel_calib_samples`, stronger per-layer save validation, or smaller resume chunks if repeated tensor signatures appear again.
4. Apply `k_eq_v` after clean quantization to satisfy vLLM quant-state checks.
5. Validate at `maxModelLen: 1920`, then test 2048/4096 only after output is coherent.
6. Promote 31B only if it can answer a short deterministic prompt without `<pad>` collapse and passes artifact integrity.

### D. TurboQuant Runtime Work

Steps:

1. Patch `build/scripts/patch_turboquant_quantizer_gpu_qr.py` to share immutable TurboQuant primitives by device/head geometry/bits/seed/codec.
2. Gate the behavior with `TQ4_SHARE_PRIMITIVES=1`.
3. Keep E4B TurboQuant as the fast regression probe.
4. For 31B, prove boot-only first. Success means the model reaches KV-cache sizing instead of OOMing in attention-module construction.
5. For 26B, start with layer-selective/SWA-aware TurboQuant rather than all-layer global compression.
6. Run a context ladder and a quality gauntlet before any TurboQuant manifest enters Flux by default.

## Validation Matrix

Minimum gates before declaring a lane "fully working":

- Artifact integrity:
  - no repeated late-layer tensor signatures,
  - quantization metadata matches expected Gemma4 family,
  - `k_proj`/`v_proj` quant-state checks pass where required.
- Runtime load:
  - pod lands on the intended gfx1100 dGPU,
  - active image digest matches the intended runtime profile,
  - load completes without OOM,
  - KV-cache sizing is logged.
- Quality:
  - instruction-tuned checkpoints are probed through chat formatting,
  - deterministic short prompts are coherent,
  - no `<pad>` collapse,
  - no repeated-token loop,
  - at least one long-context prompt passes at the target context.
- Operations:
  - warm policy matches confidence level,
  - canaries stay out of default Flux reconciliation until promoted,
  - docs and manifests agree on current `maxModelLen`.

## Open Questions

- Is the current 26B dense-validated rebuild still running, complete but unvalidated, or failed after the last save-memory bump?
- Should 31B re-quant lower calibration parallelism immediately, or first reproduce with the current settings plus the new repeated-tensor guard?
- Should FP8 KV cache be the first 26B long-context memory canary before TurboQuant, given vLLM has official ROCm FP8 KV documentation?
- Do we want to preserve the 31B corrupt artifact under a forensic name, or delete it after the clean rebuild succeeds?
