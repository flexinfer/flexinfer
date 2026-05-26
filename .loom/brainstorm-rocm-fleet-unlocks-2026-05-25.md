# Brainstorm: What we can unlock with gfx1100 + gfx906 + our stack + determination

**Date**: 2026-05-25
**Triggered by**: After a 10-MR closure chain in one session (proxy port-cache,
CC-5/CC-6 kill-test, CC-6a admission filter live, Lane 1C alias promotion,
Lane 2 matrix closure), step up the abstraction ladder and ask: what would
make this fleet actually *special* — cool to demo, hard for anyone else to
replicate, and useful daily.

**Prior context**:

- `.loom/brainstorm-fleet-hardware-optimization-2026-05-15.md` — workload-to-
  silicon placement; the OPERATIONAL frame. This brainstorm is the
  complementary PRODUCT/CAPABILITY frame.
- `.loom/roadmap-unblock-plan-2026-05-21.md` — Lane 3 (deploy/swap tracing)
  and Lane 5 (major dep rollout) are still untouched.
- Today's wins make this question richer: gfx906 proxy lane is 0% failure
  and the admission filter is live. We have a control plane, not just point
  inference.

## What we actually have (and why it's special)

| | gfx1100 × 2 | gfx906 × 1 |
|---|---|---|
| Card | RX 7900 XTX | Radeon VII |
| VRAM | 24 GB GDDR6 | 16 GB **HBM2** (~1 TB/s bandwidth) |
| Best at | Modern transformers, vLLM, FlashAttention | High-bandwidth dense ops, but ROCm abandoned |
| Status | Production, Gemma4 26B serving | Production fallback + imagegen, hipMemGetInfo shim |

**The stack-level differentiator** (not just "we have ROCm cards"):

- A custom controller doing serverless cold-start, shared-GPU arbitration,
  runtime-image promotion.
- A custom proxy with prefix routing, max_tokens clamp, **admission filter
  (just shipped)**, label-group resolution.
- ConfigMap-stored context curves per lane.
- agent-context MCP (loom-core) for persistent multi-session state and
  inter-agent handoff.
- LiteLLM router above all of it.

Most ROCm projects are *point* inference. We have an **orchestration plane**.
That's the lens for every framing below: what does our control plane unlock
that pure-vLLM-on-a-card can't?

## Phase 1 — Diverge

### F1 — Cross-card speculative decoding (gfx906 draft → gfx1100 verify)

The 1.7B Qwen tool-router on the Radeon VII is fast and cheap. The 26B Gemma4
on a 7900 XTX is the quality primary. Wire them together: the 1.7B drafts N
tokens, the 26B verifies in a single forward pass, accept the prefix that
matches and reject the rest. Our proxy is already the natural integration
point — admission filter is right there, prefix routing is right there. **Spec
decoding becomes a transparent proxy-layer optimization** rather than a model-
level setting.

Public spec-decoding implementations live inside one engine on one card.
Cross-card spec decoding on heterogeneous ROCm with HBM2 drafting is genuinely
unexplored territory.

- **Bet**: 1.5-2.5× wall-clock decode tok/s on the 26B primary with zero
  quality loss, because the verifier is the same model. The proxy gets a new
  policy: per-route opt-in `flexinfer.ai/spec-decode: enabled`.
- **Risk**: cross-card RPC overhead between gfx906 (HBM2, fast inference) and
  gfx1100 (over network or shared filesystem) may eat the gain unless we put
  the draft engine inside the proxy or use grpc with a streaming protocol.
  Acceptance-rate distributions vary by content; coding-heavy traffic might
  only see 1.2× and the wins are dominated by the chat/QA distribution.

### F2 — gfx906 as a high-bandwidth embedding + rerank appliance

Radeon VII's 1 TB/s HBM2 is wasted on autoregressive LLM serving (decode is
compute-bound on Vega20 due to no FlashAttention). But for **batched embedding
and reranker** workloads — large matrix-multiply with linear memory access —
that bandwidth is exactly what matters. Repurpose the card as a dedicated
fast retrieval engine: BAAI/bge-large + bge-reranker-large with continuous
batching, exposed through our proxy as a `/v1/embeddings` and `/v1/rerank`
endpoint. Today we use Ollama-on-CPU for `nomic-embed-text` at ~50 emb/s.
Move it to gfx906 + a real embedding backend and we get 500-2000+ emb/s plus
reranker.

- **Bet**: 10-40× embedding throughput; reranker becomes free; foundation for
  every RAG workflow that follows. The whole jobsearch-app + agent-context
  ecosystem benefits.
- **Risk**: requires a backend choice (vLLM embeddings, TEI, candle, ...).
  Most don't have great ROCm support. Embedding-only is "useful but boring" —
  no demo factor unless paired with RAG.

### F3 — Heterogeneous tensor-parallel: run 70B-class models locally

24 GB + 16 GB = 40 GB combined, enough for a Q4 70B model with KV headroom.
ROCm 6.x has RCCL working across heterogeneous arches. Our controller can
orchestrate the placement (gfx1100 carries layer set A, gfx906 carries layer
set B). vLLM TP with `world_size=2` is supported on ROCm. If we eat the
PCIe-bandwidth cost between the two hosts (10GbE between nodes — modest), we
can serve Llama-3.1-70B-Instruct-AWQ or Qwen2.5-72B locally with no
single-card model fitting.

- **Bet**: the homelab serves a 70B-class model. **Nobody else with the same
  hardware is doing this.** Per-token latency may be modest (PCIe-bound), but
  it works.
- **Risk**: PCIe-over-network between hosts may not deliver enough bandwidth
  to keep both cards fed; you could end up slower than CPU offload on a
  single card. HBM2's bandwidth advantage is largely wasted when the
  bottleneck is the inter-card link. Cross-arch RCCL collective ops are
  still maturing.

### F4 — Long-context agent platform: prefix caching + admission + 32k+ context

Three things just landed that compose: admission filter (refuse over-budget
prompts in <1ms), prefix routing (same cache key → same replica), and we
already have Gemma4-26B serving at 8k. Push the warm primary to 32k with
FP8 KV cache + chunked prefill. Build an agent loop that uses the 32k window
as a working memory: system prompt + retrieved chunks + tool history all stay
**prefix-cached across turns**, only the last user turn is new. Single user-
turn latency drops to "decode-only on the suffix" — feels instant.

- **Bet**: a "Claude.ai-class" chat experience locally where successive turns
  feel near-instant because the proxy serves them from prefix cache, with the
  admission filter as the safety net for overflowing prompts. Daily-driver
  quality for codebase chat and long-form writing.
- **Risk**: 32k context on Gemma4-26B-GPTQ may push VRAM to the edge; quality
  delta from going Q4-KV needs measurement. Cache hit rate depends on
  application design — bad client design means no shared prefix.

### F5 — Self-improving image gen loop on gfx906

The Radeon VII already hosts FluxPony + SDXL-inpainting. Add a small VLM
(LLaVA-1.6-7B GPTQ or InternVL-1B) on the same card or on the gfx1100, wire
up a critique-and-refine loop: SDXL generates → VLM critiques on a fixed
rubric (subject placement, color balance, prompt fidelity) → critique gets
folded into the next prompt → regenerate. Run for 3-5 iterations. Output the
"best-scored" frame.

- **Bet**: noticeably better image quality than one-shot SDXL prompts for
  hard subjects (multi-object scenes, specific styles, text in images).
  Demos well.
- **Risk**: adds 30-90s per image. Latency tradeoff may not be worth it for
  casual use. The critique model has to actually understand the image —
  small VLMs are spotty.

### F6 — Custom gfx906 HIP kernels: beat upstream llama.cpp on Vega20

Vega20 is "abandoned" by AMD. llama.cpp's ROCm path on gfx906 doesn't have
FlashAttention or paged attention. Write a FlashAttention-2 ROCm port
specifically tuned for wavefront64, with the 1 TB/s HBM2 bandwidth assumption
baked in. Open-source the result. We get faster gfx906 inference for our own
use AND we get to be the people who fixed Vega20 inference for everyone with
a $200 used Radeon VII.

- **Bet**: 30-60% decode-throughput improvement on gfx906 for any model that
  uses the kernel. Upstream PR-able. Bragging rights + real engineering
  artifact.
- **Risk**: HIP kernel optimization is weeks-to-months of work. Vega20
  cooperates poorly with modern compute idioms (no FP8, no VMM, no
  warp-shuffle equivalents). May produce a partial speedup and a lot of
  scar tissue.

### F7 — Local Claude Code replacement: package what we have into a daily-driver

Pull the existing pieces together into a polished local coding assistant.
We have multi-model orchestration, a tool-router (qwen3-1.7b), a fast chat
primary (Gemma4-26B), agent-context for persistent memory, admission filter,
prefix caching, MCP server framework (loom-core). What we DON'T have is a
unifying agent loop that uses all of those well. Build one. Goal: when the
homelab is up, I drop in via VS Code or a CLI and code, with full local
sovereignty and a tool-call surface that matches Claude Code's.

- **Bet**: a daily-driver-quality offline coding assistant. The infra is
  basically there; this is integration + UX polish. The output quality
  ceiling is the model (26B GPTQ ≈ Claude 3 Haiku-ish), but for many real
  coding tasks that's fine.
- **Risk**: output-quality gap vs. frontier models is real. People bounce off
  homelab agents because the model isn't smart enough, not because the infra
  is bad. This bet requires accepting that ceiling.

### F8 — Overnight batch GPU appliance: idle-time work loop

The cluster is idle 8-12 hours/day. Build a job-queue + scheduler that turns
that idle time into work. Examples that actually have client demand: scrape
+ summarize 10K JD postings overnight for jobsearch-app; embed-and-index a
fresh codebase nightly; run an offline eval suite for any new model that
lands; precompute prefix-cache entries for "tomorrow's likely first prompts".
Admission filter + max_tokens clamp + prefix routing all keep individual
jobs bounded.

- **Bet**: convert dead GPU-hours into delivered work. Tightest fit for
  jobsearch-app which already wants bulk processing.
- **Risk**: the orchestrator is ~3 weeks of work for something queue-shaped
  that already exists in industry (Argo, Temporal, ...). May reinvent.

## Phase 2 — Cross-pollinate

### F1 + F4 — Speculative decoding on a long-context RAG agent

Long-context inference is decode-heavy: prefill is one big batch, decode is
many small steps. Speculative decoding accelerates decode. So F1 has its
biggest gains exactly on F4's workload. The combined story: 32k-context
agent with prefix-cached system + retrieved context **plus** 2× decode
speedup on the suffix generation. That's not "fast inference on a small
model" or "smart agent on a slow stack" — it's a structurally different
chat experience.

This combination is the bet **nobody else with the same hardware can match**,
because it relies on our orchestration layer routing the spec-decode pair
transparently.

### F4 + F7 — Long-context agent IS the Claude Code replacement

F4 produces a stack capable of being F7. F7 alone is "polish what we have";
F4 + F7 is "polish what we have AND raise the experience ceiling first."
Without F4, F7 caps out at "good for one-turn questions, bad at maintaining
context across a refactor." With F4, the agent sustains state across long
sessions.

### F2 + F8 — gfx906 embedding appliance feeds the nightly batch processor

The overnight batch loop's heaviest cost is usually embedding generation.
Make gfx906 the dedicated embedding plane (F2) and F8 becomes 10-40×
cheaper. They want to ship together.

### Tensions

- **F1 vs F6** (do-something-with-existing-kernels vs build-new-kernels):
  F1 ships in days; F6 ships in months. Both improve gfx906/gfx1100
  inference, but at completely different cost/risk profiles. The fork is:
  do you want a quick capability win or a deep technical artifact?
- **F3 vs F1**: F3 says "the unlock is bigger models." F1 says "the unlock
  is making current models faster." They're not compatible because heterog-
  TP saturates both cards on one model — no spare draft card.
- **F7 vs F8**: product polish (F7) vs automation backend (F8). Different
  audiences. F7 is interactive; F8 is batch. You can do both but probably
  not at the same time.

## Phase 3 — Converge

### Recommended: F1 + F4 (cross-card speculative decoding deployed on a long-context RAG agent lane)

> **Update 2026-05-25** — slice outcomes after live runs:
>
> - **CC-DR-1 (external HTTP draft+verify)**: **falsified**. Implemented end-to-end (`internal/proxy/spec_decode/` + `cmd/spec-decode-bench/`, MRs !493–!510) with a real gemma4-e4b-radeonvii draft and gemma4-26b-a4b-gptq verifier. Acceptance 42.6% (real signal, IDs aligned across the two backends), but measured speedup **0.05×** — three HTTP round-trips per Coordinate round (draft + prompt-token-count + verify) obliterate per-token amortisation. Structural finding, not a bug. The bench tool stays as the comparison harness; the reference implementation stays for future cross-card explorations.
> - **CC-DR-2 (32k canary)**: **shipped**. `gemma4-26b-a4b-gptq` runs at 32k with FP8 KV cache (~13 GB weights + ~3 GB KV @ 32k, ~22 GB GPU profile cap, 2 GB physical headroom on the 24 GB card). Live 19k-token-prompt smoke produced the correct answer in 20.2s.
> - **CC-DR-3 (proxy-integrated spec-decode)**: **rerouted**. The CC-DR-1 measurement made the original framing (proxy orchestrates draft/verify across pods) uneconomic. Pivoted to in-process server-side n-gram (prompt-lookup) SD via vLLM's built-in `--speculative-config`. Live measurement on `gemma4-26b-a4b-gptq` (MR !512): **p50 1.95× speedup, p95 2.17×, 82.5% vLLM-reported acceptance rate**, with zero controller code changes (the `speculativeConfig` passthrough at `backend/vllm.go:212-216` was already wired) and zero additional GPU memory. Full re-validation row in `.loom/60-validation-matrix.md`. The win is conditional on graph capture being enabled — the same config was correctly falsified on 2026-05-14 against the pre-graph-capture image (see `.loom/r5-ngram-spec-decode-falsified-2026-05-14.md`).
>
> **Net**: cross-card / cross-pod spec-decode is not the right lever on this hardware topology. In-process server-side SD is the lever, and it's already paying out 2× on the 26B primary. Remaining work is tuning (`num_speculative_tokens` 5 → 7, `prompt_lookup_max` 4 → 6 — per-position acceptance is still 68% at position 5, indicating headroom) and replication to the 5930k twin and qwen35 (once qwen35 serves coherently again).

The original slice plan, preserved for context:

This is the only framing that compounds: F1 alone gives 2× decode speedup
but lacks application context. F4 alone gives a great agent that's still
gated by decode latency. **Together**, they produce a single chat lane where
each turn is fast (prefix-cached prefill + 2× speculative decode) and the
agent maintains 32k of working memory across a session. That experience —
sub-second response + sustained-context coherence — is the difference between
"local LLM is a toy" and "I use the homelab daily." Both pieces also sit on
top of our just-shipped admission filter and prefix routing, so the
integration debt is small.

Three slices:

1. **CC-DR-1** Spec decode runtime spike: write a draft+verify reference
   implementation in `internal/proxy/spec_decode/` that posts a draft request
   to the 1.7B router and a verify request to the 26B primary, returns
   accepted tokens. Bench gate: ≥ 1.5× p50 decode tok/s on a held-out
   prompt set. No proxy integration yet.
2. **CC-DR-2** Long-context 32k canary: push `gemma4-26b-a4b-gptq` to 32k
   maxModelLen + FP8 KV, validate quality on the existing benchmark
   gauntlet. Validation matrix row required.
3. **CC-DR-3** Proxy-integrated spec-decode for the long-context lane:
   gate behind annotation `flexinfer.ai/spec-decode: enabled`, wire metric
   `flexinfer_proxy_spec_decode_acceptance_rate`. Measure end-to-end on a
   real chat workload.

### Runner-up: F7 (local Claude Code replacement)

If the user's actual want is "daily utility" more than "novelty + learning,"
F7 is the higher ROI path. The components exist; the gap is integration and
polish. F7 might also be a better candidate after F1+F4 ships, since F4
raises F7's experience ceiling. But as a *first* slice, F7 risks producing a
yet-another-homelab-agent that's bounded by the model's quality, not the
infra's quality.

### Open question

The path forks on a single question:

> **Are we optimizing for the experience of using the homelab daily, or for
> the engineering artifact of having built something nobody else has?**

If daily-use → F7 (Claude Code replacement) is the right first slice; the
spec-decode work is a later acceleration.

If novel artifact + daily speedup → F1 + F4 is the right first slice; the
local Cursor experience falls out naturally from the 32k-context agent that
emerges from F4.

The work is similar in scope (3-4 slices either way). The user's answer
determines which slice gets opened next.

## Sources

- `MEMORY.md` — fleet hardware capability summary
- `.loom/brainstorm-fleet-hardware-optimization-2026-05-15.md` — the
  operational complement to this brainstorm
- `.loom/roadmap-unblock-plan-2026-05-21.md` — current roadmap state
- `internal/proxy/` (admission + prefix + label routing) — the integration
  surface for F1/F4
- `docs/planning/context-bounded-admission-spec.md` — the just-shipped
  admission filter; the safety net for any of F1/F3/F4
- `services/loom-core/.loom/111-product-spec-weaver-qwen3-integration-2026-05-08.md`
  — qwen3-1.7B router that becomes the F1 draft model
