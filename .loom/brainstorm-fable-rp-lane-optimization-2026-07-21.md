# Brainstorm: Fable 27B gfx1100 optimization as the 9B-RP lane successor

**Date**: 2026-07-21
**Triggered by**: Research additional model, quantization, runtime, and hardware optimizations that could make the qualified Qwen3.6-27B Fable GPTQ lane a practical replacement for `qwen35-9b-ablit-rp`.
**Constraints noted**: One 24 GiB RX 7900 XTX (`gfx1100`); vLLM/FlexInfer is the preferred serving substrate; preserve the exact Fable finetune; do not disturb the live lane while researching; replacement must be usable for interactive roleplay rather than merely load successfully.

## Baseline and decision boundary

The comparison must use the repaired 9B lane, not its older corrupted canary.
The `gptq_shuffle` skip restored coherent base and rank-64 LoRA output. Its
controlled decode ceiling is about 4.8 output tok/s, its 131,072-token window
has passed needle recall, and it is warm-primary on `cblevins-7900xtx`.

The Fable 27B exact-source W4/G128 GPTQ already passes deterministic coherence
and a five-prompt quality smoke on the native vLLM 0.23 RDNA3 W4A16 kernel. It
uses 16.68 GiB for model residency, exposes about 54,954 GPU KV tokens under
the qualified 8K configuration, and decodes at about 8.4-8.8 tok/s with one
sequence. It is therefore already around 1.8x faster than the controlled 9B
decode result. Its current usability deficits are demand-only placement and a
roughly two-minute cold start, an 8K safety envelope, single-request scheduling,
and verbose thinking tokens on simple RP turns.

The replacement decision is consequently not “can 27B beat 9B decode?” It is:
can it retain that advantage once it is warm, handles realistic multi-turn RP,
supports a useful context window and modest concurrency, and passes direct RP
quality comparisons against the 9B LoRA?

## Phase 1 — Framings

### F1 — Profile the actual gfx1100 critical path

Treat the current 8.7 tok/s result as an unexplained performance profile, not a
fixed ceiling. Capture prefill, decode, GPU utilization, CPU synchronization,
and operator attribution for native W4A16 linears, GDN/FLA recurrent kernels,
attention, sampling, and request scheduling. The current text plugin replaces
every loaded FLA autotuner's candidate list with the configuration having the
fewest stages and warps. That avoided a four-stage LLVM compile hang but likely
leaves some safe configurations unexplored. Only build a per-operator gfx1100
configuration map after a profile proves those kernels are material.

- **Bet**: A small number of FLA or synchronization hot spots dominate token time and admit safer configurations above the blanket minimum.
- **Risk**: Decode is bandwidth-bound in the W4A16/GDN path, so additional kernel tuning adds complexity without a measurable end-to-end win.

### F2 — Optimize the RP product profile, not raw tokens

Make the Fable lane warm-primary and default its RP alias to
`enable_thinking=false`. Current simple answers often spend roughly 160-194
tokens in hidden-style deliberation that arrives in `content`, so the user sees
the cost directly. Request-level `chat_template_kwargs` can test this behavior;
FlexInfer would need a small vLLM config-to-CLI addition for a durable
`--default-chat-template-kwargs` setting. Preserve a separate thinking-enabled
alias or explicit request override for tasks that benefit from it.

- **Bet**: Fable's direct non-thinking RP voice remains at least as good as the 9B LoRA while substantially reducing time to a complete answer.
- **Risk**: The finetune relies on its reasoning preamble for scene consistency or instruction following, so disabling it makes roleplay shallower even though it is faster.

### F3 — Tune the native vLLM 0.23 execution envelope

Canary the features already supported by the qualified runtime: bounded CUDA
graph capture, FP8 E4M3 KV, chunked prefill controls, and modest request
concurrency. Start with capture sizes `[1,2,4]`, then test `maxNumSeqs` 1/2/4/8
and `maxNumBatchedTokens` 2048/4096/8192. Use two partial prefill slots with at
most one long prefill so a long scene does not stall a short turn. FP8 KV is a
capacity lever first, not a promised decode accelerator. vLLM 0.23's local
hybrid-GDN certificate found warmup scale calculation unsafe because the
recurrent state is not initialized for that path, so start with explicit fixed
scales, validate quality, then bake dataset-calibrated scales. Walk context
16K -> 32K -> 48K -> 64K and stop at the first quality or fit failure.

- **Bet**: vLLM 0.23's bounded graph path works for dense text-only Qwen3.5 on gfx1100 and removes enough launch/CPU overhead to improve interactive latency without sacrificing stability.
- **Risk**: Hybrid attention/GDN graph breaks or graph-memory reservation erase the gain; FP8 KV may preserve fit but degrade long-context recall if scales are poor.

### F4 — Repair and use the model's native MTP head

The Fable source has 14 root `mtp.*` tensors but lacks `mtp.fc.weight`; the
official 27B architecture has 15. A narrowly scoped experimental artifact could
graft only that missing official BF16 FC tensor, preserve or selectively
quantize the draft head, and run one-token native MTP. This is credible on the
platform because a separate Qwen3.5 gfx1100/vLLM 0.23 experiment achieved
80.18% draft acceptance and a 1.23x median workload speedup after surgically
quantizing MTP experts. Startup is not enough: require acceptance, constrained
parity, per-workload floors, and a clean fault scan.

- **Bet**: Fable retained a compatible tuned MTP head and only the official FC tensor was omitted during publication.
- **Risk**: The mixed-provenance FC tensor is semantically incompatible with the finetuned 14-tensor head, or extra residency removes the KV/graph headroom needed on 24 GiB.

### F5 — Requantize for quality rather than headline bit width

Use a small RP/long-context calibration suite to compare the current W4/G128
artifact with W4/G32 and, only if profiling justifies it, selectively higher
precision for the most sensitive modules. Keep four-bit symmetric weights and
`descAct=false` so the native W4A16 path remains available. Do not preserve all
GDN projections in BF16: the estimated footprint no longer fits the card.
W4/G32 costs more scale metadata and can be slower; it earns promotion only if
blind RP or recall tests show a material quality win.

- **Bet**: Group-32 or narrowly selected higher-precision modules recover useful Fable quality while staying within the single-card fast path.
- **Risk**: The existing quant is already coherent; the new artifact consumes scarce VRAM and throughput for an imperceptible quality difference.

### F6 — Use the published GGUF through llama.cpp as an alternate substrate

DavidAU already publishes exact Fable Q4K GGUF variants with MTP data, while
llama.cpp supports `draft-mtp`. A controlled llama.cpp ROCm A/B could avoid the
missing-safetensors MTP repair and may suit single-stream consumer GPUs. Keep it
as a runner-up substrate because FlexInfer's vLLM lane already has operational
integration, metrics, batching, and a qualified 8.7 tok/s result; llama.cpp MTP
performance has varied by backend and model in upstream reports.

- **Bet**: The exact Q4K MTP GGUF produces better single-user latency or longer usable context on gfx1100 than the vLLM GPTQ artifact.
- **Risk**: ROCm kernels or MTP acceptance underperform, and the new backend loses vLLM's batching and operational maturity without a user-visible gain.

### F7 — Keep the card resident and make hardware behavior repeatable

Move the candidate to the 7900 XTX text card as a warm-primary canary while the
9B becomes an on-demand rollback target. Pre-stage the artifact locally and
avoid CPU offload: the observed PCIe link is x4, so it is a cold-load/offload
penalty but not the main GPU-resident decode bottleneck. Benchmark fixed
performance/memory clocks and an explicit power cap against automatic behavior
under identical thermals; retain changes only if throughput-per-watt and tail
latency improve without errors. Keep AITER disabled because its documented
support targets Instinct gfx942/gfx950, not gfx1100.

- **Bet**: Warm residency plus stable clocks removes most user-visible variance and makes the 27B lane feel faster than its raw decode number suggests.
- **Risk**: Fixed clocks add heat and power with little sustained gain, while occupying the warm card harms other workloads before replacement quality is proven.

## Phase 2 — Cross-Pollinations & Tensions

### Combinations

- **F2 + F3 + F7 — the “interactive RP profile”**: warm residency removes cold start, non-thinking generation removes hundreds of avoidable tokens, and bounded graph/scheduler tuning improves actual token latency. This is the fastest route to a usable successor without changing model weights.
- **F1 + F3 — evidence-guided execution tuning**: first compare eager and bounded graph configurations, then profile the winning arm. Only replace the plugin's blanket-minimum FLA policy if recurrent kernels remain material in that profile.
- **F3 + F4 — capacity before speculation**: FP8 KV and a conservative 32K context create the memory margin for a one-token MTP head; MTP is promoted only after its additional residency and graph capture fit together.
- **F2 + F5 — RP-specific quant validation**: thinking-off makes a direct roleplay corpus useful for comparing G128 and G32 without reasoning length overwhelming the quality signal.
- **F4 + F6 — two MTP proofs**: the published GGUF provides a useful behavioral reference for whether Fable's MTP head has meaningful acceptance before investing in the riskier safetensors graft.

### Tensions

- **F3 vs. the 9B context contract**: the 9B has verified 131K recall. The 27B's current KV pool suggests 32K-48K is realistic and FP8 may make 64K practical, but a 131K single-card promise is unlikely without surrendering concurrency or changing hardware. Whether 64K is sufficient is the central product decision.
- **F3 concurrency vs. single-stream latency**: the RDNA3 W4A16 kernel uses a scalar decode path below batch M=16. Moving from one to four sequences can raise aggregate throughput but may not accelerate one user's stream; the benchmark must report both.
- **F4 speculation vs. creative sampling**: high temperature and repetition penalties can lower target acceptance. RP defaults and MTP need to be tested together rather than extrapolated from greedy code/Q&A.
- **F5 quality vs. F3 capacity**: G32 or selective BF16 spends the same VRAM needed for longer FP8 KV, graph capture, or an MTP head. Quality has to win a blind evaluation to justify that trade.
- **F6 simplicity vs. operational consistency**: llama.cpp may be excellent for one stream, but a second serving substrate is only worthwhile if its measured end-to-end advantage is substantial.

## Phase 3 — Convergence

### Recommended: F2 + F3 + F7, followed by F1

Build one reversible `fable-rp-canary` profile on an idle 7900 XTX using the
already qualified artifact and runtime digest. Make it warm, default RP requests
to non-thinking, and run a small factorial A/B rather than changing everything
at once:

1. Eager/FP16-KV/one sequence at 8K (qualified control).
2. Bounded graph/FP16-KV/one sequence at 8K.
3. Winning execution mode with FP8 KV at 32K.
4. The winning 32K arm with sequences 2 and 4, batched tokens 4096, and guarded partial prefill.
5. Automatic prefix caching only on a repeated multi-turn transcript, with cache-hit and output-parity evidence.
6. Context ladder to 48K and 64K only after the 32K arm passes.

Run a warm, thinking-off 9B+LoRA control beside it (sequentially on the same
card where possible). Evaluate time-to-first-token, output tok/s, time to a
complete 150-token answer, aggregate throughput, p95 per-stream latency, GPU
memory, restarts/faults, long-context recall, and blinded RP preference. Once a
winning runtime tuple is known, profile it and replace the plugin's global
minimum FLA configuration only where the trace shows an actual bottleneck.

This path has the highest leverage-to-risk ratio because it changes no model
weights, directly targets the cold-start/thinking/context/concurrency gaps, and
uses capabilities already present in the vLLM backend. It also separates three
different kinds of win: user-perceived latency, single-stream decode, and
aggregate throughput.

### Runner-up: F4 — source-specific one-token MTP

Pursue the one-tensor MTP graft if the native profile plateaus below the desired
latency or if the exact GGUF demonstrates strong Fable-specific MTP acceptance.
Use the existing gfx1100 certificate thresholds: at least 60% target-verified
acceptance, at least 1.15x median speedup, no workload below 0.95x, constrained
greedy parity, and zero ROCm/HSA/OOM faults. Do not combine the first MTP probe
with a new quant, new scheduler, and new context size.

### Ideas to defer or reject

- **AITER on gfx1100**: unsupported hardware lane today; keep it disabled.
- **Sub-four-bit quantization**: loses the qualified native W4A16 kernel and raises quality risk.
- **Fully BF16-preserved GDN**: does not fit the 24 GiB target with useful headroom.
- **AWQ migration without an A/B**: native support exists, but there is no evidence it improves this model/card over the qualified GPTQ path.
- **N-gram speculative decoding for general RP**: a previous local Goodhart test improved aggregate throughput while badly regressing novel long-form generation. It is unsuitable as the default creative lane.
- **CPU weight/KV offload**: PCIe x4 makes it hostile to interactive latency; keep the model resident.
- **128K as the first context target**: establish 32K quality, then 48K/64K. A hard 131K requirement likely changes the hardware or model decision.

### Replacement gates

The Fable profile should replace, rather than merely supplement, the 9B-RP lane
only when all of these hold:

- Warm availability: no demand-path model load for ordinary RP requests and a documented rollback to the 9B parent `Model`.
- Performance: at least 8.0 output tok/s for the controlled single-stream suite, at least 1.5x the 9B's controlled decode result, and no more than 10% regression from the qualified Fable control on any workload.
- Responsiveness: thinking-off materially reduces median time to a complete answer; report TTFT separately so token suppression is not misrepresented as a kernel speedup.
- Concurrency: two and four simultaneous RP sessions complete without OOM/preemption loops, with per-stream p95 latency and aggregate throughput reported.
- Context: 32K needle and multi-turn recall pass at minimum; 48K/64K are promotion bonuses. If 131K is a hard contract, Fable is not yet a full replacement.
- Quality: blinded multi-turn RP evaluation ties or beats the 9B rank-64 LoRA, with coherence, character consistency, repetition, refusal posture, and prose preference scored separately.
- Stability: zero restart, ROCm/HSA fault, NaN, or malformed tool/chat output across a soak that includes long prompts and cancellation.
- Operations: immutable model/runtime digests, local cache Ready on the target node, metrics enabled, and a one-command parent-CR rollback.

### Open question

Is the 9B lane's verified 131K recall window a hard compatibility requirement,
or is a faster, higher-quality 32K-64K Fable RP lane an acceptable replacement?
That answer determines whether the recommended work is a profile promotion or
only a new premium RP lane.

## Riskiest assumption + kill-test

> Every brainstorm-derived plan must surface its riskiest load-bearing
> assumption explicitly. See the `spec-riskiest-assumption` skill.

**Load-bearing assumption**: vLLM 0.23 with FlexInfer's Qwen3.5 text plugin can serve the exact Fable 27B W4/G128 artifact on one RX 7900 XTX in bounded graph mode with FP8 E4M3 KV at 32K, preserving RP/recall quality while improving median interactive latency by at least 15% over a matched 32K eager/FP16-KV control.

**Kill test**: On an idle gfx1100 with the artifact and runtime digests already cached, first replay one qualified 8K eager/FP16-KV sentinel, then run sequential 32K eager/FP16-KV control and 32K bounded-graph/FP8-KV candidate arms. Use capture size `1`, `maxNumSeqs=1`, fixed FP8 scales, and thinking disabled for both 32K arms; larger capture sizes belong to the later concurrency test. After readiness, run three repeats each of short RP dialogue, a 4K multi-turn transcript, an 18K retrieval/continuation prompt, and constrained greedy continuations. Record TTFT, decode tok/s, complete-answer latency, graph-dispatch evidence, VRAM, output hashes for constrained probes, recall, and runtime logs. Pass only if the 32K control loads cleanly and the candidate has no restart/fault/NaN, passes constrained parity and recall, and improves median complete-answer latency by at least 15% with no workload slower than 0.95x the 32K control. This is executable in under 30 minutes once all arms are cached; model startup time is part of the observation but excluded from warm latency.

**Disconfirming search performed**: upstream Qwen/vLLM guidance warns that hybrid-attention graph modes and MTP on AMD remain workload-sensitive, and vLLM documents graph-memory and FP8-scale tradeoffs. The older local 9B runtime also crashed during graph capture. The separate local vLLM 0.23 gfx1100 MTP certificate is positive but not proof for this dense Fable artifact.

**Failure mode if wrong**: The proposed successor would be optimized around a graph/FP8 tuple that crashloops, loses long-context quality, or consumes enough graph memory to erase its useful KV headroom. The lane must then remain eager, and the next work should be profiler-guided FLA tuning or the llama.cpp/MTP alternate rather than broader rollout.

**Status**: run; disconfirmed as written. The 32K graph/FP8-KV arm preserved
exact recall and accelerated short dialogue, but regressed the 2,278-token
multi-turn workload by 16.2% and long-context decode by 41.7% versus the matched
eager/FP16 control. It also reduced useful KV capacity. FP8 KV is rejected for
this profile.

The failure isolated FP8 rather than graph mode. Graph/FP16-KV at a 0.94 GPU
memory budget subsequently passed a cold start, exact 32K recall, and one/two/
four-stream bounded-graph tests. The four-stream result reached 47.0744 median
aggregate output tok/s with 16.5238s p95 per-stream complete-answer latency,
zero restarts/faults, and automatic restoration of the 9B parent. The repaired
downstream runtime slice is therefore unblocked; public replacement remains
gated on blinded RP preference, a warm fault/cancellation soak, and the product
decision about the 9B lane's verified 131K context contract.

## Handoff

- The isolated `rapid-dev-iteration-loop` is complete; next steps are the warm
  mixed-prompt/cancellation soak and blinded RP preference evaluation.
- Linked research: `.loom/10-research.md` section “Fable 27B gfx1100 optimization and 9B-RP replacement (2026-07-21)”.
- Do not promote the public alias until the context requirement is decided.

## Primary sources

- Native RDNA3 W4A16 GPTQ kernel and its scalar/WMMA dispatch: https://github.com/vllm-project/vllm/pull/41394
- vLLM 0.23 release: https://github.com/vllm-project/vllm/releases
- vLLM 0.23 compilation and CUDA graph configuration: https://docs.vllm.ai/en/v0.23.0/api/vllm/config/compilation/
- vLLM 0.23 FP8 KV cache: https://docs.vllm.ai/en/v0.23.0/features/quantization/quantized_kvcache/
- vLLM automatic prefix caching behavior: https://docs.vllm.ai/en/v0.23.0/features/automatic_prefix_caching/
- Official Qwen3.5 vLLM recipe, including AMD/MTP cautions: https://github.com/vllm-project/recipes/blob/main/Qwen/Qwen3.5.md
- Official Qwen3.5-27B model/config: https://huggingface.co/Qwen/Qwen3.5-27B
- Exact Fable GGUF publication and MTP usage notes: https://huggingface.co/DavidAU/Qwen3.6-27B-Fable-Fusion-711-Uncensored-Heretic-NM-DAU-NEO-MAX-MTP-GGUF
- AITER supported hardware: https://github.com/ROCm/aiter
- llama.cpp speculative/MTP documentation: https://github.com/ggml-org/llama.cpp/blob/master/docs/speculative.md
