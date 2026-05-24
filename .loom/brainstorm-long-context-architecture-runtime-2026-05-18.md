# Brainstorm: Long-context architecture changes as FlexInfer runtime opportunities

**Date**: 2026-05-18
**Triggered by**: Sebastian Raschka's May 16, 2026 article on recent LLM architecture changes: KV sharing, per-layer embeddings, layer-wise attention budgets, compressed convolutional attention, mHC, and compressed attention caches.
**Constraints noted**: Keep the output useful for FlexInfer runtime/backlog planning. Treat model architecture changes as constraints and opportunities for serving, scheduling, profiling, and benchmark surfaces rather than as a training-program proposal.

## Phase 1 — Framings

### F1 — KV Cache Shape Becomes a First-Class Runtime Contract

The article's strongest operational signal is that models are no longer well described by parameter count, context length, and quantization alone. Gemma 4-style cross-layer KV sharing, MLA-style latent caches, and DeepSeek V4-style sequence-compressed caches all imply different memory growth curves under long context. FlexInfer could represent cache shape explicitly in `ModelDeployment`/profile metadata so scheduling and admission decisions reason about expected cache bytes per token, cache sharing ratio, and whether the backend stores one cache entry per token or compressed blocks.

- **Bet**: Cache-shape metadata gives better placement and safer context-limit defaults than today's model-size heuristics.
- **Risk**: Backends do not expose enough reliable metadata, so this becomes hand-maintained YAML that drifts.

### F2 — Benchmark the Context Curve, Not Just Tokens per Second

Current serving benchmarks often collapse performance into one TPS number. The architecture trend in the article makes that inadequate: some models win only after long contexts make KV cache or attention compute dominant. FlexInfer could add a benchmark profile that measures prefill throughput, decode throughput, VRAM slope, and latency at several context lengths, then stores the curve in benchmark ConfigMaps for scheduler scoring and operator reporting.

- **Bet**: A small fixed suite such as 2k, 8k, 32k, 128k tokens reveals placement risks and model advantages that a single short-context TPS number hides.
- **Risk**: Long-context benchmarks are slow and expensive enough that users avoid running them unless the workflow is carefully scoped.

### F3 — Attention Pattern Awareness for Backend Compatibility

Laguna's mixed sliding-window/global attention with per-layer query-head budgets, ZAYA1's compressed convolutional attention, and DeepSeek's CSA/HCA all increase backend-specific implementation risk. FlexInfer can treat attention pattern support as a backend capability matrix, similar to GPU architecture compatibility: GQA, MQA, sliding window, MLA, CCA, CSA/HCA, PLE, and mHC become model features that the controller can validate against backend/runtime images.

- **Bet**: Early compatibility rejection is more valuable than discovering unsupported kernels during pod startup or first request.
- **Risk**: The feature taxonomy changes quickly and could outpace the controller unless the initial implementation stays advisory.

### F4 — Long-Context Scheduling Should Include Memory Bandwidth Pressure

mHC and compressed attention are not just FLOP stories. They change residual-state movement, cache layout, and memory traffic. FlexInfer's scheduler could start separating compute-bound and bandwidth-bound long-context workloads, especially across 7900XTX, Radeon VII/gfx906, and CPU host-memory fallback paths. The first step could be observational: record long-context VRAM pressure, prefill latency, decode latency, and GPU utilization together before changing placement logic.

- **Bet**: Memory-traffic-sensitive scoring avoids pathological placements on older cards where raw VRAM capacity is not the only bottleneck.
- **Risk**: Hardware counters may be too inconsistent across ROCm generations to automate cleanly at first.

### F5 — Model Cards Need an "Inference Geometry" Section

The article repeatedly shows that public model descriptions are increasingly architectural, but serving systems need the operational translation. FlexInfer could extend internal model docs or CR annotations with an "inference geometry" section: active parameters, KV cache type, cache compression ratio, sliding-window/global layer pattern, max practical context by GPU class, and known backend kernel requirements.

- **Bet**: A lightweight documentation convention helps agents and operators make better deployment choices before any controller code changes.
- **Risk**: Documentation alone does not prevent unsafe deployments unless later wired into validation.

### F6 — Local Serving Differentiator: Pick Models That Exploit Cheap Long Context

Instead of chasing the largest open-weight model that fits, FlexInfer could bias its local model portfolio toward architectures that make long context cheap on commodity GPUs. Gemma 4 E models, ZAYA1, and DeepSeek V4-Flash-style designs suggest a practical product angle: "usable long context on local hardware" matters more than leaderboard maximum quality for many agent workflows.

- **Bet**: Operators feel the benefit of cheaper long context immediately in coding, retrieval, and multi-step agent loops.
- **Risk**: Architecture efficiency claims may not translate to available ROCm/vLLM/llama.cpp support soon enough.

### F7 — Runtime Images Become Architecture-Specialized, Not Just GPU-Specialized

FlexInfer already thinks in terms of GPU profiles and backend images. The next axis may be architecture-specialized runtime images: one image/profile lane for conventional GQA/sliding-window models, one for MLA/latent-cache models, one for CCA-style kernels, and one for DeepSeek-style compressed attention. The controller would select an image by model feature flags and GPU architecture instead of using a generic backend image per vendor.

- **Bet**: Specialized runtime lanes reduce startup failures and allow tuned images without bloating the default runtime.
- **Risk**: Too many image variants increase CI/build cost and make rollout evidence harder to maintain.

## Phase 2 — Cross-Pollinations & Tensions

### Combinations

- **F1 + F2**: Define cache-shape metadata, then verify it with a context-curve benchmark. The metadata can be hand-authored initially, but benchmark evidence decides whether scheduler scoring should trust it.
- **F3 + F7**: Start with an advisory model-feature compatibility matrix, then let validated feature flags drive runtime-image selection for models that need specialized kernels.
- **F5 + F6**: Build a curated "long-context local" model catalog where each entry includes inference geometry and real FlexInfer benchmark curves.

### Tensions

- **F1 vs. F5**: The decision is whether to encode inference geometry as controller-enforced API fields now or keep it as documentation until enough models are measured.
- **F2 vs. F7**: Benchmark breadth wants fewer moving parts per run; runtime specialization creates more lanes that each need evidence.
- **F6 vs. F3**: Product pull wants to try efficient new models early; platform safety wants to reject unsupported architecture features until backend support is proven.

## Phase 3 — Convergence

### Recommended: F1 + F2

The best first slice is to make long-context cache behavior measurable before making it enforceable. Add a small "context curve" benchmark/reporting concept that records prefill, decode, VRAM, and failure point across a few context sizes, and pair it with optional cache-shape metadata in model/profile docs. This fits FlexInfer's current evidence-gated runtime posture and avoids hard-coding immature architecture claims into the controller too early.

### Runner-up: F3

The compatibility matrix becomes more attractive if the next model bring-up fails because a backend silently lacks support for one of these attention variants. In that case, the smallest useful implementation would be an advisory validator that warns on unsupported feature flags before moving into runtime-image selection.

### Open question

Which near-term local model target should anchor the first context-curve measurement: the existing Gemma/Qwen serving lanes, or a new architecture-efficient candidate such as Gemma 4 E, ZAYA1, or DeepSeek V4-Flash?

## Handoff

- If chosen -> next step is: `research` for source-backed model/runtime metadata, then `feature-dev` for a narrow benchmark/reporting slice.
- Linked spec/plan doc (fill in once it exists): `<.loom/NNN-...md>`
