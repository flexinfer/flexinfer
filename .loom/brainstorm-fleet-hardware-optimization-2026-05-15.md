# Brainstorm: Fleet-level hardware optimization for FlexInfer (gfx1100 + gfx906)

**Date**: 2026-05-15
**Triggered by**: User stepping up the abstraction ladder after the 5930k decode-perf rounds (vectorize −25%, cache-nan additional, R5 ngram spec-decode falsified −15%). Question: "make the most of my hardware" across the fleet, not "make gemma4-MoE on 5930k 5% faster again."
**Prior context**:
- `.loom/brainstorm-26b-5930k-decode-perf-2026-05-14.md` (round 1)
- `.loom/brainstorm-26b-5930k-decode-perf-round2-2026-05-14.md` (round 2)
- `.loom/r5-ngram-spec-decode-falsified-2026-05-14.md` (ngram spec-decode reverted)
- `.loom/gfx1100-gfx906-next-round-plan.md` (parallel tracks A–H, mostly unstarted)

**Hardware reality** (the constraint the user called out):

| Node | GPU | CPU | RAM | PCIe to GPU | Notes |
|---|---|---|---|---|---|
| cblevins-7900xtx | 1× RX 7900 XTX (gfx1100) | Ryzen 9 7900X3D (Zen4, 2023) | DDR5-5600+ | PCIe 5.0 x16 (~64 GB/s) | Modern, fast dispatch, high mem bandwidth |
| cblevins-5930k | 1× RX 7900 XTX (gfx1100) | i7-5930K (Haswell-E, 2014) | DDR4-2400 max | PCIe 3.0 x16 (~16 GB/s) | Same GPU as 7900xtx, very different host |
| cblevins-radeonvii | 1× Radeon VII (gfx906) | (unknown, older) | (modest) | PCIe 3.0/4.0 x16 | Deprecated for inference (no VMM, FA, FP8). DaemonSet currently `runtime-paused` per `deploy/system/values-k3s.yaml:354` |

Key learned constraints:
- CUDA graphs blocked at framework level for gemma4-MoE + ROCm + current vLLM.
- gfx906 capped at ~16 GB GPU alloc (VMM unsupported on Vega20).
- 5930k aggregate decode at C=8 is **431 tok/s** (per `MEMORY.md`) — host is fine at sustained GPU work, bad at Python dispatch loops.
- Most current 5930k workload (gemma4-MoE-27B with Python patch) is exactly the *worst* fit for that host's profile.

## Phase 1 — Diverge

### F1 — Workload-to-silicon re-allocation (move gemma4-MoE off 5930k entirely)

The previous two rounds optimized *within* the placement. The placement itself is wrong. 5930k hosts a Python-dispatch-bound MoE patch path on Haswell-E + DDR4 + PCIe3 — every per-forward optimization eventually hits a hardware ceiling that we cannot remove. Move gemma4-MoE serving exclusively to 7900x3d (Zen4 + DDR5 + PCIe5 mask Python dispatch). Give 5930k a dense INT4 GPTQ model where the work is fused-GEMM-bound and the host barely matters. Qwen3-14B-GPTQ already runs at 72-73 tok/s decode on gfx1100 (ExllamaV2). Same fleet, different placement, ~3-5x latency improvement on 5930k for *its* workload.

- **Bet**: per-host workload fit dominates per-host tuning. 5930k stops being a "second-rate 7900xtx" and becomes a competent dense-INT4 server.
- **Risk**: gemma4-MoE-27B quality characteristics (long context, reasoning) may not have a dense substitute. Need golden comparison. Concentrates traffic on one node — capacity ceiling moves.

### F2 — Switch 5930k backend to llama.cpp + GGUF for gemma4-MoE

If gemma4-MoE must stay on 5930k (quality reason), change the runtime, not the model. llama.cpp has native gemma4-MoE since b8637+. Its host/device split is fundamentally different from vLLM eager mode: tensor ops are batched in C++ before crossing to the GPU, so the host is doing C++ work + occasional GPU launches rather than a Python interpreter loop. The 5930k host is far better at the former.

- **Bet**: backend choice is the leverage when the workload is host-dispatch-bound. 30-50% latency plausible.
- **Risk**: lose vLLM features (paged attention, OpenAI-compatible streaming, continuous batching). Re-quantize to GGUF. Different runtime ops surface.

### F3 — Re-charter 5930k from "latency text" to "batch + imagegen"

5930k aggregate throughput at C=8 is 431 tok/s — better than 7900xtx at C=1. The node is bad at per-token latency, fine at sustained throughput. It already runs FluxPony imagegen primary. Lean into it: re-charter 5930k as batch text completion (async jobs, embeddings backfill, summarization) + imagegen. Move all interactive text to 7900x3d. Set 5930k's SLO on throughput, not p50/p95.

- **Bet**: chasing C=1 latency parity is the wrong KPI. Aggregate throughput is the real value of this node.
- **Risk**: requires app-layer batching infrastructure. Re-routes interactive traffic to 7900x3d — capacity becomes the new bottleneck.

### F4 — DDR5+PCIe5 warm tier on 7900x3d as fleet-wide L2 model cache

7900x3d swaps a 13 GB model from RAM → VRAM in ~1-2 s (PCIe5 + DDR5-5600). 5930k does the same swap in ~8-15 s (PCIe3 + DDR4-2400). Exploit asymmetry: 7900x3d hosts a hibernated catalog (6-8 distinct models, ~80 GB RAM resident); cold misses fall through to PVC. 5930k gets 1-2 pinned models with no swap, because swap on that host is too slow to be useful.

- **Bet**: 7900x3d's memory subsystem is being wasted as headroom-that-sits-idle. As an L2 cache it unlocks "any model on demand" UX.
- **Risk**: requires controller-level support for RAM-tier hibernation. Pinned-vs-swappable per-node policy needs CRD shape. RAM is shared with kubelet/other services.

### F5 — Radeonvii as dedicated batch quantization farm (+ optional embeddings)

gfx906 is deprecated for forward inference but 16 GB HBM2 @ 1 TB/s is still excellent for batch GEMM. The quant pipeline already has gfx906 muscle memory (community PyTorch image, numpy ABI matrix, transformers<5 pin, abliteration safeguards). Promote it from "rarely-used SDXL canary" to **the** quantization worker: all new model variants (NF4, AWQ, GPTQ-int4, GGUF Q4/Q5/Q6) queue overnight to radeonvii. Free up gfx1100 capacity entirely for inference. Optionally serve embeddings (nomic-embed-text currently on gtx980ti).

- **Bet**: deprecated silicon can be valuable as a batch worker even when it's bad online. Extracts persistent value from a node currently `runtime-paused`.
- **Risk**: gfx906 quant pipeline has known land-mines (~12 entries in MEMORY.md). Track B (disk-pressure unblock) must land first.

### F6 — Architecture-specific runtime images per host, not per backend

Currently `runtime:rocm-gfx1100-*` is shared between 7900x3d and 5930k despite their host profiles being totally different. 5930k benefits from a different vLLM config (smaller `max_num_batched_tokens`, eager-mode locked, no scheduler async). 7900x3d benefits from the opposite. Split into `runtime-gfx1100-fast` (7900x3d) and `runtime-gfx1100-thrifty` (5930k). Pre-compile kernels for the actual ISA + dispatch profile. Aligns with Track A controller hardening already in flight.

- **Bet**: per-host runtime tuning is the next layer once placement is right.
- **Risk**: doubles image matrix. Promotion script (`scripts/promote-runtime-digest.sh`) needs host-suffix variants.

### F7 — Multi-model density on gfx1100s (cooperative co-tenancy)

At C=1, 7900 XTX (61 TFLOPS FP16) is idle most of the time. With 24 GB VRAM and a 13 GB resident base, ~5-7 GB is free after KV cache. Host 2-3 distinct small models per GPU via cooperative scheduling (one vLLM engine per pod, separate models, scheduler arbitrates). Or vLLM multi-LoRA for adapter variants. Density unlocks more endpoints per dollar of silicon.

- **Bet**: GPU utilization is the unmeasured KPI. C=1 latency is good; density is the marginal win.
- **Risk**: VRAM contention. KV cache pressure under contention OOMs rather than slowing down. Scheduler complexity.

### F8 — Ship the cumulative −33% win, redirect engineering elsewhere

Round 1 (+4%), round 2 (−25%), cache-nan (more). 5930k went from 2.13x slower to ~1.60x slower on a 12-year-old CPU. Remaining gap is hardware-bound by physics. Tracks B (gfx906 disk pressure), D (qwen36 coherence), G (5930k fast-chat), H (Qwen3.5/3.6 triage) are all *unstarted* with higher EV. Declare victory on this surface.

- **Bet**: marginal value of further 5930k optimization < marginal value of any unstarted track.
- **Risk**: leaves F1–F4 wins on the table if any of them is quick.

## Phase 2 — Cross-Pollinate

### Combinations

- **F1 + F4** (re-allocation + warm tier): 7900x3d takes gemma4-MoE *and* serves a hibernated catalog via RAM-L2. 5930k pins one dense GPTQ model with zero swap. Each node does what its memory subsystem is best at. Cleanest "use asymmetry on purpose" pairing.
- **F3 + F5** (batch 5930k + radeonvii farm): both become async/batch tier. 7900x3d alone owns interactive. Fleet splits into one online node and two offline workers. Operationally simpler than mixed-mode per-node.
- **F2 + F6** (llama.cpp on 5930k + per-host image): if F2 is chosen, F6 follows — 5930k image becomes a llama.cpp variant, no longer "same image as 7900x3d." Track A's GPUProfile work supports this naturally.
- **F1 + F7** (re-allocation + density): after moving gemma4 off 5930k, freed VRAM headroom on 5930k makes density viable. Host 2-3 GPTQ models for distinct endpoints.

### Tensions

- **F1 (move workload) vs F2 (change runtime)**: opposing theories of where the bottleneck lives. F1 says "wrong workload for this host." F2 says "wrong runtime for this workload." Profile-driven, decidable with one llama.cpp smoke test.
- **F3 (batch-only 5930k) vs F7 (gfx1100 density)**: opposing utilization theories. F3 raises utilization by widening request width on existing model. F7 raises it by adding more models. Both improve $/token; target different bottlenecks.
- **F8 (stop) vs everything**: meta-tension. Right answer if any of F1–F7 takes >2 weeks. Wrong if F1+F4 is a few days of policy/manifest work.
- **F5 (radeonvii worker) vs Track B (radeonvii paused)**: F5 *requires* Track B (disk-pressure unblock). Gated, not opposed.

## Phase 3 — Converge

### Recommended: **F1 + F4** — re-allocate gemma4-MoE to 7900x3d; make 7900x3d a DDR5-tier hibernation cache; give 5930k a host-fit dense model

The decisive evidence is in the round-2 falsification doc: every per-forward optimization on 5930k+gemma4-MoE hits a hardware ceiling we cannot remove (Python dispatch latency bounded by CPU + DDR4 bus + PCIe3 link). The 5930k host is strong where GEMM is fused and host work is minimal (dense INT4 GPTQ models). Its weakness (slow per-op dispatch) is fatal for MoE patch paths. F1 fixes the placement.

F4 is the second leg because 7900x3d, after absorbing gemma4-MoE, still has leftover memory-subsystem capacity that 5930k cannot use anyway. Turning it into a hibernation tier multiplies the fleet's model catalog without buying hardware. Together: **each host does what its memory subsystem is good at; nothing competes with what its memory subsystem is bad at.** This subsumes 80% of F3/F6/F7's wins as second-order consequences (5930k pinned model removes scheduler complexity, per-host runtime image becomes straightforward, density on 5930k becomes viable since the host isn't fighting Python anymore).

Implementation is mostly policy + manifests: re-target gemma4 Model CR nodeSelector, hibernation policy CRD additions, swap-cost-aware controller decision. No new kernels, no upstream patches. First pass shippable in 3-5 days.

### Runner-up: **F3 + F5** — batch tier consolidation (5930k batch text + imagegen; radeonvii quant farm)

Tipping trigger: F1 blocked because gemma4-MoE-27B is the only model meeting the top user workflow's quality bar AND 7900x3d capacity is already saturated. In that case relax 5930k's latency SLO (it's already not meeting parity, just formalize it) and extract more value from radeonvii. Requires Track B to land first. Lower ceiling than F1+F4 on latency, higher floor: doesn't depend on workload replacement working.

### Open questions

1. **Is there a dense model in the gemma4-MoE quality band (reasoning, long context) that fits in ≤22 GB on gfx1100 INT4?** Candidates: Qwen3.5-27B-GPTQ already shipped (26 GB, tight at 8K context), Qwen3-32B-GPTQ if it exists, gemma4-26B dense if it exists. If yes, F1 is trivial. If no, F1 still works (5930k runs Qwen3-14B-GPTQ general + smaller reasoning model) but catalog story is messier.
2. **What's the actual RAM on cblevins-7900xtx?** F4 assumes 128 GB DDR5 (8 models cached). If 64 GB, L2 holds 2-3 models and F4's ROI changes meaningfully.
3. **What is the user's traffic mix?** If 80% interactive, F1+F4 obviously wins. If 80% batch (jobs, embeddings, summarization), F3+F5 is the better fit. Need actual queue depth + workload breakdown from prom/grafana.

## Handoff

- **F1 + F4 chosen** → `plan-loom-core` to write `.loom/NNN-product-spec-fleet-reallocation-2026-05-15.md` covering nodeSelector retargeting, hibernation policy CRD shape, swap-cost controller logic, validation matrix updates. Then `feature-dev` to ship manifest re-targeting first (smallest reversible slice), measure, then layer in hibernation.
- **F3 + F5 chosen** → bundle with Track B (gfx906 disk-pressure unblock) as gating prerequisite, then `feature-dev` for batch-tier SLO changes and quantization-job priority changes.
- **F2 chosen** (5930k → llama.cpp) → `research` first to confirm llama.cpp b8637+ gemma4-MoE quality vs vLLM goldens, then `feature-dev` to add llama.cpp backend variant on 5930k Model CR.
- **F8 chosen** (stop) → `feature-dev` to mark 5930k decode-perf surface closed in `.loom/00-index.md`, redirect to Tracks B/D/G/H from the existing parallel plan.
- Linked spec/plan doc (fill once it exists): `<.loom/NNN-...md>`
