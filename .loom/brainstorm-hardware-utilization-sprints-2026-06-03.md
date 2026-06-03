# Brainstorm: Next flexinfer sprints to make the most of the hardware fleet

**Date**: 2026-06-03
**Triggered by**: Voice stack live, F4 agent-loop compound complete, in-process
n-gram SD paying 2× on the 26B primary, 32k FP8-KV canary shipped. The
point-capabilities are landing; the question steps up a level: which silicon is
still under-utilized, and what's the highest-leverage way to extract more from
the fleet we already own?
**Constraints noted**: No new hardware. Fleet = 2× RX 7900 XTX (gfx1100, 24 GB
GDDR6), 1× Radeon VII (gfx906, 16 GB HBM2 ~1 TB/s), 1× GTX 980 Ti (sm_52, 6 GB),
CPU nodes. ROCm-only GPU path; gfx906 vLLM is feasibility-only (Vega20 C++
fused-path segfault), llama.cpp is the proven gfx906 substrate.

**Prior context**:
- `.loom/brainstorm-rocm-fleet-unlocks-2026-05-25.md` — the predecessor. Its
  F1+F4 recommendation (cross-card spec-decode on a long-context agent) has
  since resolved: cross-card SD **falsified** (0.05× — HTTP roundtrips), 32k
  FP8-KV canary **shipped**, in-process n-gram SD **2× and live**, F4 agent-loop
  client **complete**. Its **F2 (gfx906 embedding/rerank appliance) was never
  built** and is the strongest surviving thread.
- `.loom/brainstorm-fleet-hardware-optimization-2026-05-15.md` — operational
  workload-to-silicon placement frame.
- `00-index.md` Current Goal — F4 compound complete; remaining F4 item is the
  `F4-prefix-cache-flip` canary promotion (row 193, conditional).

## What's actually under-utilized (the lens)

| Silicon | Today | Gap |
|---|---|---|
| Radeon VII (gfx906, 1 TB/s HBM2) | diarization (pyannote, live), imagegen, LLM fallback, 1.7b router | **HBM2 bandwidth wasted on autoregressive decode** — never put on bandwidth-bound embedding/rerank. `bge-radeonvii.yaml` + `embeddings.yaml` exist as stubs; agent-context/RAG still lean on Ollama-CPU nomic (~50 emb/s) |
| 2× RX 7900 XTX (gfx1100) | gemma4-26b primary + 5930k twin, `maxNumSeqs: 1` | **No batching** — fleet collapses at parallelism >2 (42% timeouts); n-gram SD 2× shipped on primary only, not replicated to twin/qwen35 |
| GTX 980 Ti (sm_52, 6 GB) | e4b GGUF only | Barely used; candidate "cheap brain" tier or retire |
| Whole fleet | per-model warm policies | **Idle 8–12 h/day**; no idle-batch loop; no per-card utilization observability (#28 open) |

## Phase 1 — Framings

### F1 — gfx906 retrieval plane (the HBM2 appliance)
Finish the embedding + rerank service the predecessor flagged. bge-large +
bge-reranker on the Radeon VII, proxy `/v1/embeddings` + `/v1/rerank`, replacing
Ollama-CPU nomic (~50 emb/s). HBM2's ~1 TB/s is exactly right for batched matmul
with linear memory access — the one workload where Vega20 beats the gfx1100s.
`bge-radeonvii.yaml` already exists as a stub, so this is finish-not-start.
- **Bet**: 10–40× embedding throughput; reranker ~free; every RAG consumer
  (agent-context, codebase-memory, jobsearch) compounds off it.
- **Risk**: same Vega20 C++/HIP fragility that killed vLLM there forces CPU
  fallback → zero win. (llama.cpp embeddings is the lower-risk backend bet.)

### F2 — Throughput-first: batch the cards we already own
Both 26B lanes run `maxNumSeqs: 1`. Raise concurrency, replicate the proven 2×
n-gram SD to the 5930k twin + qwen35, tune SD params (5→7 tokens). Pure config +
replication, no new models.
- **Bet**: 3–5× aggregate fleet throughput, unlocking concurrent multi-client use.
- **Risk**: per-token latency regression on single-user path; 22 GB KV ceiling
  caps batch depth at 32k; gains invisible until a concurrent workload exists.

### F3 — Idle-time batch appliance
Convert 8–12 idle GPU-h/day into work: nightly codebase re-embed/index, JD
scrape+summarize for jobsearch, offline model-eval gauntlet, prefix-cache
prewarming. Admission filter + max_tokens clamp bound each job.
- **Bet**: dead GPU-hours become product value; tightest fit for jobsearch +
  agent-context.
- **Risk**: ~3 weeks to reinvent Argo/Temporal; needs real demand.

### F4 — Fleet observability + placement (close #28)
GPUGroup metrics → Grafana per-card utilization / tokens-per-watt / idle-time,
then drive workload-to-silicon placement and right-size warm policies from
evidence. Measure before optimizing.
- **Bet**: turns every later optimization into an evidence-backed one; modest effort.
- **Risk**: dashboards aren't capability; can become yardwork that defers wins.

### F5 — Heterogeneous big-model lane (70B-class)
Combine 24+16(+24) GB across cards/hosts via vLLM TP / RCCL to serve a Q4 70B no
single card fits.
- **Bet**: homelab serves a 70B — a genuine capability-ceiling raise.
- **Risk**: PCIe-over-10GbE starves the cards; HBM2 advantage wasted at the link
  bottleneck; cross-arch RCCL immature → possibly slower than single-card offload.

### F6 — Multimodal appliance: one endpoint, whole fleet
Generalize the voice-stack placement pattern: one API (text+vision+voice+
image-gen) routing each modality to its best silicon — vision-OCR + qwen-omni +
Flux + Whisper + Kokoro behind one proxy surface.
- **Bet**: a coherent local multimodal assistant using every card for its strength.
- **Risk**: integration sprawl; N model lifecycles; breadth over depth.

### F7 — "Cheap brain" tier on the weak silicon
GTX 980 Ti + qwen3-1.7b-router as an always-warm tier for high-QPS/low-IQ work
(classification, routing, guardrails, draft tokens, structured extraction),
reserving the 7900 XTXs for hard reasoning.
- **Bet**: offloading cheap frequent calls to free/weak silicon raises effective
  7900 XTX availability.
- **Risk**: 980 Ti (sm_52, no-AVX2 host) so constrained ops cost may exceed gain.

### F8 — Consolidate into a daily-driver product
Stop building infra; package the complete F4 agent-loop + voice + SD + 32k into a
polished local Claude-Code-class daily driver (VS Code / CLI).
- **Bet**: infra is basically there; integration + UX polish makes it a habit.
- **Risk**: quality ceiling is the 26B model, not the infra.

## Phase 2 — Cross-Pollinations & Tensions

### Combinations
- **F1 + F3**: embedding is the heaviest cost of the nightly batch loop — build
  the gfx906 retrieval plane first, idle re-embed/index becomes 10–40× cheaper.
  One arc.
- **F2 + F4**: can't tune batch depth / SD params without per-card utilization +
  KV-pressure visibility. F4 is the instrument; F2 the experiment. F4→F2 is
  evidence-backed throughput tuning.
- **F1 + F6**: retrieval plane is the memory backbone of a multimodal RAG
  assistant — F1 gives F6 something to remember with.

### Tensions
- **F2 vs F5** — *serve more vs serve bigger.* Batching spends spare KV/cards on
  concurrency; heterogeneous TP saturates everything on one big model. Opposite
  uses of the same VRAM headroom.
- **(F1/F2/F4) vs F8** — *extract-more-from-silicon vs polish-the-product.* Is
  the bottleneck the silicon's output or our ability to use it daily?
- **F6 vs F8** — *breadth vs depth.*

## Phase 3 — Convergence

### Recommended: F1 + F3, thin F4 slice first ("retrieval + utilization" arc)
F1 is the single clearest under-utilized-silicon win — HBM2 is genuinely the
right tool for embedding/rerank, a stub manifest already exists, and it has the
broadest downstream leverage (every RAG/agent-context/jobsearch/codebase-memory
consumer compounds off it). A thin F4 slice goes first as the instrument to
prove the win and right-size everything after; F3 follows because idle-batch
re-embed rides on F1's appliance. A 3-sprint arc ending in measurably higher
utilization plus a RAG backbone the rest of the stack builds on.

### Runner-up: F2 (throughput-first batching)
If real concurrent multi-client load exists or is imminent, raising `maxNumSeqs`
+ replicating SD is the cheapest, fastest win — pure config, days not weeks.
Tips the choice: evidence of actual concurrency demand over single-user latency.

### Open question
What are we optimizing for next — **utilization/throughput** (extract more from
current silicon: F1/F2/F3/F4), **capability ceiling** (F5 70B / F6 multimodal),
or **product polish** (F8 daily driver)? The arc sequence depends on the answer.

## Riskiest assumption + kill-test

**Load-bearing assumption**: A ROCm embedding backend (llama.cpp embeddings
first choice, else TEI or vLLM embeddings) runs **GPU-resident on gfx906/Vega20**
and delivers materially higher batched-embedding throughput than the current
Ollama-CPU `nomic-embed` baseline (~50 emb/s) — i.e. the HBM2 bandwidth is
actually reachable, not blocked by the same Vega20 C++/HIP fragility that made
vLLM feasibility-only there.

**Kill test**: Deploy one candidate (start with llama.cpp embeddings — proven
working gfx906 substrate per MEMORY.md) serving bge-large on `cblevins-radeonvii`.
Run a 1000-document batched embedding benchmark. PASS = ≥5× the CPU baseline
emb/s **AND** `rocm-smi` shows GPU utilization (not CPU fallback) during the run.
≤30 min once the image exists. Also: inspect whether `bge-radeonvii.yaml` /
`embeddings.yaml` already serve on GPU or are inert stubs.

**Failure mode if wrong**: Every ROCm embedding backend falls back to CPU or
segfaults on Vega20 → the appliance delivers no throughput win and we'd be
building proxy plumbing for nothing. Pivot: run embeddings on a gfx1100 lane
(steals capacity from the 26B primary) or accept the CPU baseline.

**Status**: **PASSED (feasibility leg) 2026-06-03** — see
`60-validation-matrix.md` "2026-06-03 S1.1 kill-test". llama.cpp embeddings run
GPU-resident on gfx906 (25/25 layers offloaded, peak GPU 100%, no segfault):
bge-large 8,952 tok/s, nomic 23,719 tok/s compute. The Vega20 fragility that made
vLLM feasibility-only does NOT affect the embedding path. The ≥5×-vs-incumbent
*throughput* leg is deferred to Sprint 0 S0.3 (nomic@980Ti baseline couldn't be
activated — stuck in 980 Ti shared-group queue). Disconfirming search done:
documented ROCm-7 gfx906 segfaults exist but did not manifest on this path.

## Handoff
- If chosen → next step: `plan-loom-core` (write the sprint plan for the chosen arc)
- Linked spec/plan doc (fill in once it exists): `.loom/30-implementation-plan-<arc>-2026-06-03.md`
