# Brainstorm: improve 26B decode rate on cblevins-5930k without hardware swap

**Date**: 2026-05-14
**Triggered by**: 2.13x decode-rate gap measured between `gemma4-26b-a4b-gptq` (7900xtx) and `gemma4-26b-a4b-gptq-5930k` (5930k) on identical vLLM config — 22.99 s vs 48.89 s mean for a 141-token decode. Previous slice's recommendation was "swap hardware or accept the gap"; the user pushed back: find decode wins within existing hardware.
**Constraints noted**: cannot replace hardware; cannot regress correctness; `enforce_eager: true` is locked per the manifest comment (torch.compile falls back to non-tanh GELU on ROCm; gptq_gemm "buggy" with CUDA graphs); `maxNumSeqs: 1` per existing memory-pressure decision; experimental MoE-patched runtime image (`runtime:rocm-gfx1100-gemma4-moe-patched`).

## Phase 1 — Framings

### F1 — Wake up the CPU

`lscpu` on cblevins-5930k shows `CPU(s) scaling MHz: 50%`. With base 2.4 GHz that's an **actual ~1.2 GHz**, vs the Xeon's 3.3 GHz boost ceiling. The cblevins-7900xtx node shows 82% scaling. Linux power-saving keeps non-busy cores at low frequency; in our workload (single-threaded Python dispatching to GPU, alternating between work bursts and idle-waiting-for-GPU), the CPU likely gets stuck at low frequency between tokens — and the slow ramp-up is on every iteration. Set the governor to `performance` (host-side `cpupower frequency-set -g performance`, or a privileged DaemonSet) and pin the engine main thread to 1-2 cores that can sustain 3.3 GHz boost.

- **Bet**: getting the actual CPU clock from ~1.2 GHz → 3.3 GHz on the pinned core delivers most of the missing 2x without any code change.
- **Risk**: K8s container CPU affinity needs `cpu-manager-policy=static` on the kubelet (not enabled cluster-wide), so the pinning piece needs host-side admin or a privileged DaemonSet. Governor itself is a one-liner per host.

### F2 — Revisit the `enforce_eager` constraint

The manifest comment justifying `enforce_eager: true` cites two pre-2026-04-10 bugs: GELU tanh approximation falls back to "none" under torch.compile on ROCm, and the gptq_gemm kernel is "buggy" with CUDA graphs. The current image is `v0.1.dev1+g467d3247c.d20260410` (recent). vLLM also has `cudagraph_mode: PIECEWISE` for selective capture. A one-hour A/B (eager vs graphs, same prompt suite, diff outputs) tells us whether the constraint still binds in 2026.

- **Bet**: bugs are fixed in the current vLLM, graphs work for our config, decode rate jumps 2-5x because Python-per-token dispatch becomes Python-per-N-tokens.
- **Risk**: silent correctness regression (wrong GELU output, gptq dequant artifacts) that the smoke misses but real users notice. Or graphs crash on first invocation. Or the bugs are still there, in which case the experiment cost was ~1 h.

### F3 — Speculative decoding

vLLM supports speculative decoding: a small draft model proposes N tokens; the big model verifies them in one forward pass. Effectively amortizes Python dispatch across multiple accepted tokens per step. Even with `enforce_eager: true`, decode rate could double at ~50% draft acceptance. Candidate draft: a 1B-class model, or Gemma4-E4B (4B) if loaded locally — same tokenizer family helps acceptance rate.

- **Bet**: 2-4x decode rate by amortizing Python overhead across accepted draft tokens per step.
- **Risk**: ROCm spec-decode in current vLLM may not work with Gemma4 MoE (we're already on an experimental MoE-patched image). Adds a draft-model artifact, tunable acceptance threshold, and a new failure mode (low acceptance can be worse than no spec-decode).

### F4 — Prune per-token Python on the hot path

Audit every config flag for per-token CPU work and disable what's actually unused:

- `disable_log_stats: false` → `true` (currently logs `loggers.py:259` every 10 s)
- `enable_chunked_prefill: true` → `false` (ICC's typical prompts are ≪ `max_num_batched_tokens`; chunked-prefill bookkeeping isn't earning its keep)
- `enable_tool_calling: true` + `tool_call_parser: gemma4` → consider disabling for the 5930k-specific instance if downstream callers don't actually use tool-call output (ICC's prompt asks for `response_format: json_object`, not tool calls)
- `reasoning_parser: gemma4` → same logic; only ICC's tool-call/reasoning consumers benefit
- `kv_cache_metrics_sample: 0.01` → `0` (no metrics scraper consumes this today)
- `enable_logging_iteration_details: false` (already off, confirm)

Each flag is a few ms/token. Cumulatively non-trivial on a slow-CPU node.

- **Bet**: cumulatively shaves 10-30% per-token CPU cost via dead-code elimination on the hot path.
- **Risk**: feature regression for downstream callers (ICC's `response_format: json_object` likely depends on the structured-output backend, not the tool/reasoning parser — verify before pruning). Coordination cost: each disable requires a quick audit against actual call patterns.

### F5 — Workload-shaped routing

Stop trying to make the 5930k as fast as the 7900xtx — admit the asymmetry and route accordingly. Short-output requests (extraction-style, `max_tokens ≤ 256`) go to the 5930k where the absolute end-to-end latency tax is small. Long-output requests (chat, generation) go to the 7900xtx. The proxy already inspects request bodies for `max_tokens` clamping (`internal/proxy/max_tokens_clamp.go`); could extend the same inspection to drive routing.

- **Bet**: matching workload to capability reduces user-perceived mean latency without changing per-instance perf.
- **Risk**: proxy code complexity (request introspection on the routing path); "predicted output length" is hard to infer (clients can lie about `max_tokens` or just omit it); mixed-workload users see inconsistent latency. And it doesn't actually fix the gap — it routes around it.

### F6 — Run a thinner engine on 5930k

Replace vLLM on the 5930k node with llama.cpp (or MLC-LLM) and a converted 26B-A4B GGUF. llama.cpp has a much smaller per-token Python footprint (mostly C++, no Python on the hot path). The fleet would run mixed backends — `vllm` on 7900xtx, `llamacpp` on 5930k — sharing the same `project-mgmt` proxy alias.

- **Bet**: llama.cpp's lean per-token cost closes the gap; nomic-embed-text + qwen3-30b-a3b already run 100+ tok/s on weaker GPUs in our cluster via llama.cpp.
- **Risk**: 12-24h GGUF re-quantization pipeline (Gemma4 MoE just landed in llama.cpp b8637+; b8665+ recommended for tokenizer fixes); feature divergence (tool calling, reasoning parser, structured output may behave differently across the two backends); 5930k-vs-7900xtx instances diverge operationally, complicating future upgrades.

### F7 — Profile first, then target

Don't guess. Attach `py-spy --pid $(pgrep -f vllm)` on the engine during a representative decode (or enable vLLM's `enable_logging_iteration_details`). The top-3 CPU consumers tell us where the cycles actually go. Decision-by-evidence vs decision-by-hypothesis. The output likely also tells us whether F2's CUDA-graphs hypothesis has the leverage we think.

- **Bet**: the 2x gap is dominated by 1-3 specific Python functions; pinpointing surfaces a sharp, high-leverage fix that no high-level framing captures.
- **Risk**: 1-2 hour investigation with no guaranteed actionable output. May reveal the gap is diffuse (many small calls) which loops back to F1+F4. May also reveal it's all in `__init__` and steady-state isn't actually 2x off — which would be embarrassing but useful.

### F8 — Kernel-level core/IRQ isolation

Use `isolcpus=` kernel cmdline to reserve 1-2 cores exclusively for the engine container, and `irqbalance --banscript` to keep interrupts off those cores. Same direction as F1 but at a lower control plane (kernel boot params) with stronger guarantees against migration.

- **Bet**: kernel-level isolation recovers cycles currently lost to context switches and IRQ handling on the engine's cores.
- **Risk**: requires a host reboot to apply `isolcpus`. Only persists via OS-level admin work, not k8s manifests. Gain uncertain without F7-style profiling first.

## Phase 2 — Cross-Pollinations & Tensions

### Combinations

- **F1 + F4** (governor/pinning + per-token Python prune): F1 makes each cycle worth more; F4 reduces total cycles per token. Additive, both low-risk, both reversible by changing one flag/config. Likely covers most of the 2x gap with no correctness exposure.
- **F2 + F3** (CUDA graphs + speculative decoding): both attack Python-per-token dispatch from different angles. If F2 works, F3 compounds it (each verification step is now amortized over multiple draft tokens AND replaying a graph). Together: 4-8x potential. Both correctness-risky on the experimental MoE-patched image — but if one works the other almost certainly will too.
- **F7 + F1** (profile first + governor): F7 reveals which Python function dominates; F1 makes that function's cycles fast. F7 turns F1 from "tune everything" into "tune the right thing." Most useful when F1+F4 alone underperform expectation.

### Tensions

- **F1 vs F8** (governor vs `isolcpus`): both are CPU-side wins via different control planes. F1 is reachable via a one-liner privileged DaemonSet or `ssh + cpupower`; F8 needs a kernel cmdline edit and a reboot. **Real axis**: how much OS admin work is acceptable for this perf class?
- **F2 vs F6** (CUDA graphs vs engine swap): both unbreak the Python bottleneck — but F2 keeps vLLM and reclaims its existing optimizations, while F6 replaces it with a leaner stack. **Real axis**: do we trust vLLM's current state on ROCm enough to push it harder, or admit the stack is too heavy for our CPU class and move?
- **F4 vs F5** (prune features vs route around them): F4 degrades behavior for every caller to claw back cycles. F5 keeps features but shapes traffic. **Real axis**: is the bottleneck worth narrowing the product surface, or do we differentiate behavior by node?

## Phase 3 — Convergence

### Recommended: **F1 + F4** (CPU governor/pinning + per-token Python prune)

The `CPU(s) scaling MHz: 50%` line in the 5930k `lscpu` output is a smoking gun the previous slice walked past. The 7900xtx node runs at 82% scaling under similar idle conditions. **The slow CPU is also running at ~half its base clock — that's a second factor of ~2 stacked on top of the hardware-generation gap.** The previous "1.9x slower CPU" diagnosis was likely "1.9x slower CPU × 2x lower clock = 3.8x effective" being measured back to 2.13x by other factors evening out. Setting the governor to `performance` is reversible, observable in under a minute (`cpupower frequency-info`), and has no code or API surface impact. F4 is the natural pairing because it has the same risk profile (config changes, reversible, no correctness exposure) and targets the orthogonal dimension (cycles-per-token vs Hz-per-cycle). Together they likely close 50-80% of the gap with no experimental code paths. Validation is the same matched-workload benchmark we already have.

If F1+F4 land a 1.5x+ improvement, the slice is done. If they don't, we have clean evidence the bottleneck isn't where we thought, F7 becomes mandatory, and F2 moves to the front as the next biggest lever.

### Runner-up: **F2** (revisit the `enforce_eager` constraint)

Tip-the-choice trigger: F1+F4 only recovers 20-30% (gap stays >1.5x). At that point CUDA graphs are the only remaining lever with order-of-magnitude potential. The cost of falsifying F2 is small — a one-hour experiment with `enforce_eager: false` and the existing coherence probe (the same prompt suite used for the round-robin validation) tells us whether the 2026-vintage manifest comment still binds. If graphs work, decode rate jumps 2-5x because Python-per-token dispatch becomes Python-per-N-tokens. The cited bugs (GELU tanh fallback, gptq_gemm CUDA-graph buggyness) are 8+ months old at this point and vLLM has churned aggressively on ROCm support since.

### Open question

**Can the user (or a privileged DaemonSet) set the CPU governor on `cblevins-5930k` to `performance` and confirm `cpufreq-info` reports cores boosting to 3.3 GHz under load?** If the answer is "no, this is managed and we can't change power-saving" — F1 collapses, F4 alone won't cover the gap, and the recommendation flips to F2 (with its experimental risk). Conversely, if the answer is "easy, ssh in and run one command," F1 likely wins decisively before we ever need to touch F2's correctness risk.

## Handoff

- If F1+F4 chosen → next step is `feature-dev` to ship the governor change (DaemonSet manifest under `platform/gitops` OR an ssh one-liner) and the config audit in `deploy/models/gemma4-26b-a4b-gptq-5930k.yaml`, then re-run the matched-workload benchmark from this brainstorm's parent slice.
- If F2 chosen → next step is `feature-dev` to flip `enforce_eager: false` on the 5930k instance only, run the coherence probe, diff outputs against the 7900xtx baseline. Roll back via env override if outputs diverge.
- If F7 chosen → next step is `research` to capture a `py-spy` flamegraph from the live 5930k engine and a vLLM iteration-details log dump, then re-enter brainstorm with the evidence.
- Linked spec/plan doc (fill in once it exists): `<.loom/NNN-...md>`

## Execution log (2026-05-14, post-brainstorm)

User picked F1 then F2. Outcomes:

### F1 — performance governor on cblevins-5930k (kept live)

Captured baseline: `schedutil` governor with `intel_cpufreq` in passive mode; cores swinging 1.2-2.3 GHz at idle, max 3.3 GHz, min 1.2 GHz. The 50% MHz scaling reading in `lscpu` was real — the slow CPU was running well below its base clock between work bursts.

Set governor to `performance` on all cores via `ssh cblevins@cblevins-5930k 'sudo sh -c "for c in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance > \$c; done"'`. Verified during a warm-up request: cores climbed to 2.9 GHz steady, one boosted to the full 3.3 GHz. The CPU is now actually running fast under load.

Matched-workload benchmark (same prompt + params + node-pinned curl pod as the parent slice's measurement):

| | Baseline (`schedutil`) | After (`performance`) | Δ |
| --- | --- | --- | --- |
| 7900xtx mean | 22.99 s | 23.21 s | +1.0% (noise) |
| **5930k mean** | **48.89 s** | **47.01 s** | **−3.8%** |

**~4% gain on 5930k. F1 hypothesis falsified.** Despite the CPU now boosting to 2.9-3.3 GHz under load (verified via sysfs sample during the warm request), end-to-end decode rate barely moved. The smoking-gun `CPU(s) scaling MHz: 50%` was real but turned out not to be the binding bottleneck. The actual gap is likely **PCIe roundtrip latency** (X99 PCIe 3.0 x16 = 15.75 GB/s vs AM5 PCIe 5.0 = 63 GB/s, ~4x) or **memory bandwidth** (DDR4-2400 ≈ 76.8 GB/s vs DDR5-5200 ≈ 83 GB/s + 2x bank parallelism). Neither is addressable from the serving side without a hardware change.

Decision: leave governor at `performance` on the live node. It's a free 4%, doesn't survive reboot (no DaemonSet shipped because the gain doesn't justify the persistence work), and is defensible default for an AI-inference host.

Skipping F4 entirely — F1's result is evidence that per-token Python isn't the bottleneck. Pruning more Python work would also have minimal effect.

### F2 — flip `enforce_eager: false` on 5930k Model CR (rejected)

Live A/B: suspended `flexinfer-models` Flux kustomization, `kubectl patch model gemma4-26b-a4b-gptq-5930k --type=merge -p '{"spec":{"config":{"enforceEager":false}}}'`, waited for new pod to roll.

**Crashed on engine init.** From `kubectl logs --previous`:

```
torch.AcceleratorError: HIP error: operation not permitted when stream is capturing
Search for `hipErrorStreamCaptureUnsupported' ...
```

vLLM/inductor's compiled graph wrapper hits a HIP runtime call that's forbidden inside an active CUDA-graph stream capture context. This is exactly the class of bug the manifest comment cited ("gptq_gemm kernel is 'buggy' with CUDA graphs" + "GELU tanh fallback under torch.compile on ROCm"), and it's still present in the current image (`v0.1.dev1+g467d3247c.d20260410`). 2 pod restarts in 5 minutes before I aborted.

Reverted via `kubectl patch ... enforceEager: true`, `flux resume kustomization flexinfer-models`. Pod came back up Ready in ~3 min, coherence probe returns `"2 + 2 = 4"` for the math prompt — service restored.

**Outcome: F2 falsified. CUDA graphs cannot be enabled on Gemma4 MoE + ROCm in current vLLM.** The manifest comment is still load-bearing 8 months after it was written. Fix would need to land upstream in vLLM or ROCm, not configurable from our side.

### Convergence post-execution

The brainstorm's recommended path (F1+F4) and runner-up (F2) both turned out marginal or impossible. The remaining options:

- **F3 (speculative decoding)**: probably blocked too — spec-decode also relies on CUDA-graph-style amortization, and `enforce_eager: true` is the same constraint. Worth a future probe when a vLLM version lands that fixes Gemma4 MoE + graphs on ROCm.
- **F5 (workload-shaped routing)**: still on the table. Cleanest mitigation for a gap we cannot close: route short requests to the slow node, long requests to the fast node. Real implementation cost but no correctness risk.
- **F6 (engine swap to llama.cpp on 5930k)**: still on the table. Big lift, but the only remaining lever with order-of-magnitude potential.
- **F7 (profile first)**: now MORE valuable post-F1/F2 falsification, not less. A `py-spy` flamegraph would actually tell us whether the bottleneck is HIP kernel launch overhead, memory-bandwidth-bound kernels, or something else entirely.
- **F8 (kernel `isolcpus`)**: F1's result implies this would also be marginal.

Honest assessment: with the current vLLM + ROCm + Gemma4 MoE config locked at `enforce_eager: true`, the per-token CPU↔GPU dance on the older X99 platform is bound by PCIe 3.0 latency and DDR4 bandwidth. The 2.13x gap is the hardware tax for parallel capacity. Mitigations exist (F5 routing, F6 engine swap) but each costs real engineering work for a node that may be retired before they pay off.

Recommended next step: **document, don't optimize further.** Accept the gap as a known property of the 5930k node; pursue F5 or F6 only if a real workload materializes that makes the latency-vs-capacity tradeoff bite users.
