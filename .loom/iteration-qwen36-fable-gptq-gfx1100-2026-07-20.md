# Rapid iteration: Qwen3.6-27B Fable GPTQ on gfx1100

## Scope

- Iteration goal: prove that the exact-source Fable Fusion W4/G128 artifact
  serves coherently and at useful speed on the FlexInfer gfx1100 vLLM runtime.
- Final status: qualified on physical gfx1100 with the native RDNA3 W4A16
  operator. The digest-pinned lane is coherent and about 14x faster than the
  legacy ROCm GPTQ reference path.
- Root cause: generic ROCm fused GPTQ handling corrupted production-shaped
  output, while the legacy reference path was correct but too slow. The proper
  fix was a vLLM 0.23 source build containing the gfx1100 operator plus the
  repository's text-only Qwen3.5 architecture plugin.

## Artifact pinning

- Branch: `codex/qwen36-fable-gfx1100-canary`
- Files touched: digest-pinned cache/Model activation plus temporary GPU-window
  and completed-build-window restoration manifests.
- Build profile: text-only GPTQ W4/G128, 128 x 1024 calibration.
- Qualified image digest: `registry.harbor.lan/flexinfer/runtime@sha256:2e9652edee30ed078843935ce5672280efd3585de0527d27703dd6880592981d`
- Reference image digest: `registry.harbor.lan/flexinfer/runtime@sha256:6ee8b3ed6bd0f80ba669f9a5a8525c9323592d1aa84fdf64b2a48f061fa4220e`
- Artifact digest: `sha256:285a044529f6321f954353c93c5a91f771cfdfc7445ad25af5d64b7926d83710`
- Upstream ref: `nightmedia/Qwen3.6-27B-Architect-Polaris2-Fable-B-F451@5ae530c3ab85033856e75cb1efc63fb1bf82a133`
- Probe manifest: `deploy/models/qwen36-27b-fable-gptq.yaml`
- Target node: `cblevins-5930k` (`gfx1100`)
- Cache/storage path: `pvc://qwen36-27b-fable-oci/qwen36-27b-fable`
- Model: `qwen36-27b-fable-gptq`

## Change

- Narrow patch point: enable only the digest-pinned pull cache and unique,
  demand-only Model; park the existing 5930k GPU leader for an isolated smoke.
- Why this is minimal: it changes no runtime image, quantization recipe,
  default alias, or production routing group.
- Isolation correction: `minReplicas: 0` alone did not park WAN because its
  explicit `warmPolicy: primary` still created a backend pod. Set that policy
  to `ondemand` for the canary window; restore both fields after the verdict.
- Proxy correction: the first cold-start request was rejected because the
  Gemma4 and Qwen3.5 text leaders still had `warmPolicy: primary` in the same
  `5930k-textgen` shared group. Temporarily make both demand-only as well; this
  changes no aliases, priorities, artifacts, or serving parameters.
- Runtime correction: the first scheduled pod inherited the generic gfx1100
  `vllm@sha256:a9b306...` profile image and exited before load because it does
  not recognize `--gdn-prefill-backend` or `--scheduler-reserve-full-isl`.
  Explicitly pin `spec.image` to the already-recorded Qwen3.6-capable unified
  `runtime@sha256:6ee8b3...` so image identity is no longer profile-dependent.
- Flag correction: the pinned unified runtime is vLLM 0.17 and also rejects
  those two newer CLI options. Omit only `gdnPrefillBackend` and
  `schedulerReserveFullISL`; retain the runtime's built-in ROCm GPTQ reference
  fallback, eager mode, text-only architecture override, and AIter disablement.
- Environment correction: after argument parsing, strict validation rejected
  legacy `VLLM_USE_TRITON_FLASH_ATTN` baked into the pinned runtime image.
  Set `failOnEnvironValidation: false` for this immutable runtime; all explicit
  Model/GPUProfile environment values remain unchanged and auditable.
- Native runtime correction: build vLLM 0.23 from source for gfx1100 and make
  the image verifier import `vllm._rocm_C` directly. Importing the dispatcher
  on a GPU-less builder did not prove that the ROCm extension was loadable.
- Architecture correction: the first native image stopped before weight load
  because upstream vLLM registers the multimodal Qwen3.5 wrapper but not the
  text-only architecture emitted by this quantizer. Install the existing
  `vllm-qwen35-text-plugin` in the native image and verify its entry point.

## Probe

- Commands: deterministic greedy direct/proxy completions, five prompt quality
  smoke, and a short throughput/VRAM snapshot.
- Pod: `qwen36-27b-fable-gptq-7f754cd9d4-l8pgr`, zero restarts, Ready on
  `cblevins-5930k`.
- Confirmed image ID:
  `registry.harbor.lan/flexinfer/runtime@sha256:6ee8b3ed6bd0f80ba669f9a5a8525c9323592d1aa84fdf64b2a48f061fa4220e`.
- Confirmed source: `pvc://qwen36-27b-fable-oci/qwen36-27b-fable`, whose
  ModelCache source is the pinned OCI digest recorded above.
- Expected success: stable English answers across repeated greedy prompts, no
  token salad, no engine errors, and VRAM fit below the 24 GiB card ceiling.

## Reference result

- Outcome: **PASS for coherence and gfx1100 fit; performance-constrained**.
- Load proof: all 5 safetensor shards loaded in 12.35s; vLLM reported 16.87 GiB
  model memory, 2.76 GiB available KV cache, 11,200 GPU KV tokens, and a 58.20s
  engine profile/cache/warmup stage. The final pod reached Ready with no
  restart after the compatibility corrections.
- Determinism: three temperature-0, seed-123 requests for `2+2` each returned
  exactly `4` (`deterministic_identical: true`).
- Quality smoke: 5/5 HTTP 200 and coherent/correct: `17*6 -> 102`; correct
  one-sentence ice-density explanation; valid Python list comprehension;
  exact valid JSON `{"status":"ok","count":3}`; coherent lighthouse sentence.
- Metrics after the suite: 9 successful requests, 0 errors, 118 generated
  tokens. Mean TTFT was 5.49s (`49.440959 / 9`); mean request TPOT was 1.650s,
  or about **0.61 output tok/s** (`14.846696 / 9`). This is correctness-first
  reference-fallback performance, not a production throughput promotion.
- VRAM snapshot: 23,955,951,616 / 25,753,026,560 bytes used (93.0%), leaving
  1,797,074,944 bytes (~1.67 GiB) physical headroom. Host RAM had 27 GiB
  available and swap use was 218 MiB.
- Runtime warnings remain non-fatal and pinned: the image carries legacy
  `VLLM_USE_TRITON_FLASH_ATTN`, lacks newer GDN/scheduler CLI flags, and logs
  unavailable optional Triton kernels. The active engine used Triton Attention
  plus the runtime's built-in ROCm GPTQ reference fallback.

## Native W4A16 result

- Outcome: **PASS for coherence, fit, determinism, and throughput**.
- Runtime identity: the serving pod used the qualified `2e9652ed...` digest
  with zero restarts. Both API and EngineCore loaded the text architecture
  plugin, resolved `Qwen3_5ForCausalLM`, and logged
  `Using RDNA3W4A16LinearKernel for AutoGPTQLinearMethod`.
- Load proof: all 5 checkpoint shards loaded. vLLM reported 16.68 GiB model
  memory and 54,954 GPU KV-cache tokens within the 24 GiB card envelope.
- Determinism: three temperature-0 requests returned identical coherent output.
  The chat template exposes a verbose `Thinking Process:` in `content`, but a
  256-token `2+2` request completed its reasoning and returned the final `4`.
- Quality smoke: coherent and correct responses for arithmetic, France's
  capital, a Python addition function, Rayleigh scattering, and exact JSON.
  No token salad or engine errors were observed.
- Throughput: short completions sustained about 8.4 tok/s; a 194-token response
  completed in 22.13s, about **8.77 output tok/s** gross. This is roughly 14x
  the 0.61 tok/s reference lane.
- Lifecycle: keep `minReplicas: 0`. A 10-minute serverless timeout covers the
  roughly two-minute cold start and multi-prompt sessions. The independent
  two-minute shared-GPU demand window still returns the card to the WAN warm
  primary promptly after traffic stops.

## Next

1. Keep this exact artifact/runtime pair available under the unique
   `qwen36-27b-fable-gptq` alias at minReplicas 0 and an 8K context.
2. Keep WAN as the warm primary. Equal priority 500 lets explicit Fable demand
   borrow its idle card while active video demand remains authoritative.
3. Treat larger context, MTP, alternative scheduler settings, and default-alias
   routing as separate changes with their own correctness and VRAM gates.
