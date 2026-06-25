# Brainstorm — Building on the 64K coherence proof + needle-bench: where does the next inference-engineering effort go?

**Date:** 2026-06-25
**Decision in one sentence:** The recent work proved 64K is robustly usable on the Qwen3.5/3.6 35B MoE (depth-independent recall, 5/5 multi-needle at 63K) and produced `scripts/context-needle-bench.py` (a *coherence* measurement tool, not just throughput) plus a public blog post — what is the highest-leverage next move to push self-hosted inference capabilities further?

## Framing of the decision

This is the sequel to [.loom/brainstorm-moe35b-vs-70b-daily-driver-2026-06-14.md](brainstorm-moe35b-vs-70b-daily-driver-2026-06-14.md). That doc asked "which model to invest in"; this one asks "now that we can *measure* long-context coherence and have proven 64K, what capability do we build next?"

What just landed (the work this builds on):

- **64K proven robustly usable**, not a toy single-needle pass — recall is *depth-independent* (no lost-in-the-middle, every depth 0–100% PASS at 48K/60K) and *multi-fact* (5/5 needles at 63K). The cliff past 64K is RoPE-coherence, not VRAM ([[project_uncensored35b_64k_context]]).
- **A measurement capability**: `context-needle-bench.py` with 5 modes (points / sweep / bisect / depth-grid / multi-needle), runnable inside a serving pod with no third-party deps. Tool extension shipped (flexinfer !716); blog updated (flexinfer-site !83).

"Related work" that the next move can compose with: the experiment-platform CRD ([[project_experiment_platform]]), the GPU lease scheduler ([[project_gpu_lease_scheduler]]), the now-working training/fine-tune lane ([[project_f1_training_killtest]]), MTP self-quant spec decode (+9% tok/s, [[project_abliterated_qwen_lanes]]), the idle embedding/retrieval plane (Radeon VII bge/reranker + qdrant, [[project_radeonvii_retrieval_plane_decontention]]), the APC prefix-cache canary, and the GTM/lead-gen engine ([[project_gtm_leadgen_engine]]).

## Riskiest assumption + kill-test

**Load-bearing assumption:** Silent coherence collapse is a *recurring* failure mode worth a standing promotion gate — not a one-off already solved by the 64K RoPE pin. (This is the bet under the recommended F1.)

**Kill-test (≤1 hr, uses the tool just shipped):** Run `context-needle-bench.py` against 2–3 *already-serving* models at and just past their pinned `max_model_len` — gemma4-26b at its 16K, the uncensored 35B at 64K, a 14B abliterated at its window. If ≥1 shows a coherence cliff inside or suspiciously near its advertised window (or a historically promoted model would have failed), the gate earns its place. If all serving models are robustly coherent across their advertised windows with healthy margin, the gate solves a problem we don't recurringly have → F1 drops below F3.

Pair with positive/negative search before declaring passed: search both "does forced-RoPE / quant degrade coherence inside the trained window" (positive) AND "vLLM long-context coherence is fine when max_model_len ≤ trained length" (negative) so we don't over-generalize the cliff to models that were never extrapolated.

**Failure mode if wrong:** We build promotion-gate infra (CRD acceptance hooks, gauntlet runner, per-model time tax) for a failure mode that was a one-off already fixed by the RoPE pin — eval shelfware that slows the very promotion loop it was meant to protect.

**Status:** not run

---

## Phase 1 — Diverge

### F1 · Institutionalize the bench as a promotion gate (measurement → enforced contract)
The bench is a one-shot manual tool today. Promote it: every model that wants to become a serving leader (new quant, new RoPE pin, new runtime image) must clear a coherence + depth + multi-needle gauntlet first. Wire it into the experiment-platform CRD / GPULease flow so a model *cannot* be promoted with a silent cliff. 64K stops being a blog snapshot and becomes a pinned, enforced contract.
- **Bet:** the durable output of this work isn't "64K" — it's the *capability to detect silent coherence collapse*, the failure mode every past incident shares (forced-RoPE garbage, qwen36 token-soup, MTP random-draft).
- **Risk:** eval is a tax; 7–9 min/model slows the promotion loop; becomes shelfware without an owner.

### F2 · Push the ceiling for real: YaRN / long-RoPE to earn native 128K
Today's 64K is *forced* extrapolation pinned at the cliff (rope_theta 10M, ~2× the trained 32K). Use the now-working training lane to do proper context extension — YaRN / NTK-aware scaling / continued-pretrain — with the bench as acceptance test.
- **Bet:** 64K is the soft ceiling of a *technique* (naive extrapolation), not a hard wall; proper scaling unlocks coherent 128K on the same cards.
- **Risk:** burns GPU-weeks; the 24GB KV budget may cap usable length regardless of coherence; might lose to simply swapping in a better-trained base model.

### F3 · Make the ceiling irrelevant: retrieval plane so 64K *behaves* like 1M
Raw context is brute force. The leverage move is RAG + agentic chunking on the already-idle embedding/retrieval plane (Radeon VII bge/reranker + qdrant) so the window is spent on *relevant* tokens. The bench already proved depth-independent recall — now build the retrieval layer that feeds it.
- **Bet:** for real workloads (codebases, doc Q&A), 64K + good retrieval beats 256K stuffed with noise — at a fraction of the VRAM.
- **Risk:** different discipline (chunking/rerank quality, orchestration); the hard part is integration, not the model.

### F4 · Capability breadth over context depth: an agentic / tool-use lane
Self-hosted's real edge is privacy + cost for *agentic* workloads, not matching a frontier context number. Invest in a verified tool-calling / coding lane (omnicoder-9b, qwen3-tools, claude-distill) and let 64K serve agent loops (long convos, repo-in-context).
- **Bet:** the platform's actual job is hosting agents cheaply and privately; context is an input, not the product.
- **Risk:** tool-use reliability on quantized local models is shaky; chasing hosted-frontier agentic quality is a losing race; scope is diffuse.

### F5 · Make 64K *fast*: prefill latency + KV, not recall
The depth-grid run took 7–9 min of prefill for ~60K tokens. A window you can't prefill quickly is a benchmark, not a product. Invest in prefill: prefix-cache (APC canary exists), MTP spec decode (already +9%), KV layout. Bench *time-to-first-token at depth*, not just recall.
- **Bet:** the binding constraint on long-context usefulness is prefill latency + KV memory — which the recent work just proved recall is *not*.
- **Risk:** prefill is compute-bound on aging AMD cards; APC only helps on cache hits; may be a hardware wall software can't move.

### F6 · Productize the measurement: "real context ceiling" as content + eval-as-a-service
The blog + tool already exist. Lean in: a public, reproducible cross-model "real context ceiling" benchmark becomes lead-gen for the GTM engine. "We measure what vendors hide" is the story; the capability *is* the marketing.
- **Bet:** the bench/blog is worth more as a go-to-market credibility asset than as internal infra.
- **Risk:** content is a different muscle; cross-model benchmarking is a real maintenance burden; GTM Slice 0 kill-test still hasn't passed.

### F7 · Harder eval: comprehension-at-length, not needle recall
Needle-in-haystack is necessary, not sufficient — passing says "not garbage," not "useful at 60K." Extend to multi-hop QA, code-edit-at-depth, summarization faithfulness, lost-in-the-middle on *reasoning*. The bench measures retrieval; the real question is comprehension.
- **Bet:** recall is the easy 80%; the quality story needs comprehension-at-length, which needle tests don't touch.
- **Risk:** needs ground-truth datasets + grading (LLM-judge); much heavier than string-match needles; scope-creeps into a full eval framework.

---

## Phase 2 — Cross-Pollinate

**Tension (the real axis): F2 vs F3 — push the ceiling vs. make it irrelevant.**
Spend GPU-weeks fine-tuning toward coherent 128K, or spend eng-weeks on retrieval so 64K is plenty? This is *more-context vs. smarter-context*. Naming which workloads we actually serve collapses most of the decision — and it's the same kind of "what is this model FOR" question that the prior brainstorm's open question raised.

**Combination: F1 + F7 → a tiered gauntlet.**
Tier 1 = recall/depth/multi-needle (fast, blocking — already built). Tier 2 = comprehension/reasoning-at-length (slower, advisory). F1 wires both into promotion; this *is* the natural acceptance contract for the experiment-platform CRD. Don't pick between "operationalize the easy test" and "build the hard test" — stage them.

**Combination: F5 + F1 → one tool, two contracts (near-free).**
The bench already walks length×depth and already hits the endpoint — capture TTFT/latency in the same pass. The promotion gate then covers *both* coherence and prefill-latency-at-depth at near-zero marginal cost. This is the cheapest combination on the board.

**Combination: F6 + F7 → the public benchmark *is* the comprehension benchmark.**
If we're building harder evals anyway, making them the reproducible public artifact means the marketing asset and the internal quality bar are one build, two payoffs (mirrors the F5+F4 logic in the prior doc).

---

## Phase 3 — Converge

**Recommended: F1 (bench → promotion gate), with F5's latency dimension folded in cheaply, sequenced ahead of F2/F3.**
The most valuable thing this work produced is not "64K" — that snapshot rots the instant a runtime image or RoPE pin changes. It's the *capability to detect silent coherence collapse*, and the project history is a parade of exactly that failure: models that load, pass short smoke, then silently degrade. A standing gauntlet that blocks promotion converts a class of expensive, late-discovered failures into a ~9-minute pre-merge check. It composes directly with the experiment-platform CRD and GPULease work already in flight, costs little GPU (runs in idle promotion windows), and is the *prerequisite* for safely attempting F2/F3 — you cannot responsibly push toward 128K without a gate proving you didn't break 32K. Fold in F5's TTFT-at-depth because the requests already run: the gate then covers the *other* thing that makes long context useless (latency), not just recall.

**Runner-up: F3 (retrieval plane makes 64K behave like 1M).**
Tips this way if the real workloads are doc/code Q&A where the idle embedding + qdrant infra already exists and the actual pain is "the model can't see my whole repo." Retrieval is a bigger capability jump there than another 32K of raw window, and it sidesteps the 24GB KV wall. It's runner-up only because it has its *own* eval problem — you'd want F1's gate to measure rerank quality anyway, so F1 is upstream of F3 succeeding.

**Open question (answer before committing):**
What is the 64K window actually *for*?
- "We promote model builds constantly and keep getting bitten by silent regressions" → **F1** is the bottleneck.
- "Agent loops / whole-repo-in-context / long conversations" → **F3/F4**; raw context is a means.
- "Credibility / inbound for the platform business" → **F6**.

The bench cannot tell you which — only the workload can.

---

## Resolution (2026-06-25) — workload = agent / repo-in-context

Operator answered the open question: **agent / repo-in-context work**. This resolves the F2↔F3 tension toward *smarter-context, not more-context*, and re-converges the recommendation:

**New spine: F3 (retrieval plane) + F5 (prefix-cache for multi-turn), with F1 demoted to supporting eval.**

Rationale for this workload specifically:
- **Raw context length is NOT the bottleneck** — 64K is already proven robust at depth and multi-needle. Pushing to 128K (F2) is the *wrong* investment for agent/repo work; it's solved enough.
- **F3 is the capability jump**: a real repo doesn't fit usefully even in 128K, and stuffing it raw wastes the window on irrelevant tokens. Retrieval over the *already-idle* bge/reranker + qdrant plane spends the 64K window on relevant chunks. Depth-independent recall (just proven) means retrieved chunks land reliably regardless of position.
- **F5 is non-obvious but critical here**: agent loops re-send a *growing* context every turn, so prefill latency compounds turn-over-turn. Prefix-cache (the existing APC canary) is the single biggest multi-turn latency win — far more impactful for agents than for one-shot prompts. F5 was "near-free gate dimension" under the F1 framing; under the agent framing it's a first-class spine member.
- **F1 doesn't disappear — it changes target**: instead of gating coherence-at-length, the gauntlet's job becomes measuring **retrieval/rerank quality** and **tool-call reliability** (the things that actually break agent loops). The bench tool generalizes from needle-recall toward F7-style task eval, but pointed at retrieval correctness.
- **F4 (tool-use lane)** is the capability the retrieval+fast-prefill substrate enables; sequence it after F3+F5 prove the substrate.

### Riskiest assumption for the chosen path (F3)

**Load-bearing assumption:** Retrieval (bge embed + reranker on the idle Radeon VII plane → qdrant) over a real repo produces answers at least as correct as stuffing the same content raw into the 64K window — i.e., rerank quality is good enough to be worth the orchestration.

**Kill-test (≤ half day):** Index one real repo (e.g. flexinfer itself) into qdrant via the existing bge plane. Take ~15 repo-Q&A / "where is X / how does Y work" questions with known answers. Run each three ways: (a) **naive** — stuff as much of the repo as fits in 64K raw; (b) **retrieval** — top-k chunks + rerank into a small window; (c) **baseline** — no context. Score correctness. Pass = (b) ≥ (a) on correctness *and* uses far fewer tokens. If (a) > (b), rerank quality isn't there yet and the raw 64K window is the better tool for our repo sizes → reconsider F3.

**Failure mode if wrong:** Build a retrieval/orchestration layer whose rerank quality is *worse* than stuffing the window — added latency and complexity for negative value.

**Status: PASSED 2026-06-25** (ran live, in-cluster Job `f3-killtest`).

### Kill-test result (live)

16 adversarially-verified, non-guessable loom-core Q&A, scored 3 ways on the live 35B (retrieval via the **production** `codebase_memory_bge_v1` qdrant index → bge-reranker → generate; naive stuff-what-fits; no-context baseline), graded by keyword-match + an independent gemma4 judge:

| condition | keyword | judge | avg ctx tokens |
|---|---|---|---|
| **retrieval** | **16/16** | **16/16** | 972 |
| naive (~30K tok) | 0/16 | 0/16 | 30,000 |
| baseline | 0/16 | 0/16 | 0 |

Evidence retrieved into top-6: 16/16. Token savings **30.9×**. Baseline 0/16 confirms questions genuinely require the repo; naive's failures were honest "NOT FOUND" (coverage, not hallucination). A confirmatory run favoring naive (relevant dirs only, ~42.5K tok near the 64K ceiling) still gives **retrieval 16/16, naive 0/16, 43.7×** — a 1,720-file repo doesn't fit any window. → **Retrieval beats stuffing decisively; F3 is validated.**

**Bonus discovery that reshapes the plan:** F3's *index half already exists in production* — `deploy/tasks/codebase-reembed/` nightly-embeds loom-core into `codebase_memory_bge_v1` (21,607 chunks, bge-large 1024-dim), and `deploy/tasks/model-eval-gauntlet/` is F1's gauntlet skeleton (throughput-only today). So the real gap is the agent-facing **read path** + chunking eval + index coverage + a retrieval-quality gate — not greenfield retrieval.

### Next step

Plan written: [.loom/30-implementation-plan-f3-retrieval-readpath-2026-06-25.md](30-implementation-plan-f3-retrieval-readpath-2026-06-25.md). Slice 1 (kill-test) DONE; **Slice 2 (the agent-facing read path: query→embed→qdrant→rerank→generate endpoint, reusing the proven primitives) is the highest-leverage next step.** F1 (retrieval-quality gate on the existing gauntlet) and F5 (APC prefix-cache for multi-turn) are Slices 5–6.

---

## Handoff

- **F1 (recommended)** → if the kill-test confirms recurring collapse, hand to `plan-loom-core` as a scoped spec: gauntlet runner + experiment-platform CRD acceptance hook + TTFT-at-depth capture. The kill-test itself is a `rapid-dev-iteration-loop` / `research` spike using the existing tool.
- **F3 (runner-up)** → `plan-loom-core` for a retrieval-plane spec (chunking + rerank quality eval on the existing bge/qdrant infra).
- **F2** → gated behind F1 (don't extend context without a gate); needs the training lane + GPU-weeks budget → its own spec.
- Lineage: this doc is the sequel to [[project_uncensored35b_64k_context]] and the prior [brainstorm-moe35b-vs-70b](brainstorm-moe35b-vs-70b-daily-driver-2026-06-14.md).
