# Implementation plan — F3 retrieval read-path for agent / repo-in-context work

**Date:** 2026-06-25
**Lineage:** [.loom/brainstorm-next-inference-capabilities-2026-06-25.md](brainstorm-next-inference-capabilities-2026-06-25.md) (Resolution: workload = agent/repo-in-context → F3+F5 spine). Sequel to the 64K coherence proof ([[project_uncensored35b_64k_context]]).
**Status:** Slice 1 (kill-test) PASSED live 2026-06-25 → remaining slices unblocked.

## What this plan is

Build the **retrieval-augmented read path** as a first-class self-hosted-inference capability for agent / repo-in-context workloads, on top of the retrieval plane that **already half-exists**. The brainstorm's recommendation (F3, with F5 prefix-cache and F1 eval-gate as supporting members) is the spine.

## What already exists (discovered 2026-06-25 — narrows the gap)

- **Index half is BUILT and in production.** `deploy/tasks/codebase-reembed/` is a nightly CronJob that walks a repo, chunks it (line-windows 45/8, ≤700 chars), embeds via **bge-large** through `flexinfer-proxy`, and writes 1024-dim vectors to qdrant `codebase_memory_bge_v1`. Live state: **21,607 chunks** of loom-core, 1024-dim Cosine. Throughput ~70.9 emb/s in-cluster.
- **Retrieval primitives all work live** (validated 2026-06-25): `/v1/embeddings` (bge-large, 1024-dim) → qdrant `points/search` (real index, returns payload.path+text) → `/v1/rerank` (bge-reranker-v2-m3, cohere-style scores) → `/v1/chat/completions`. All Ready: bge-large-radeonvii, bge-reranker-radeonvii, qwen36-35b-mtp-uncensored (64K), gemma4-26b.
- **A sibling AST-chunked index exists**: morph `codebase_memory_v1` (the codebase-memory MCP's own collection).
- **F1's gauntlet skeleton exists**: `deploy/tasks/model-eval-gauntlet/` runs weekly — but measures **throughput only**, not retrieval quality or coherence.

**So the gap is NOT "build retrieval."** It is: (a) no agent-facing **read path** that does retrieve→rerank→generate as a serving capability; (b) chunking is naive line-windows, not evaluated against AST; (c) index coverage is loom-core-only; (d) no retrieval-quality **eval gate**.

## Riskiest assumption — KILLED 2026-06-25

**Assumption:** Retrieval (bge embed + reranker → qdrant) over a real repo answers at least as well as stuffing the same repo raw into the 64K window — i.e. rerank quality is worth the orchestration.

**Kill-test (ran live, in-cluster Job `f3-killtest`):** 16 adversarially-verified, non-guessable loom-core Q&A, each scored 3 ways (retrieval via the **production** `codebase_memory_bge_v1` index → rerank → generate; naive stuff-what-fits; no-context baseline) on the live 35B, graded by keyword-match + an independent gemma4 judge.

**Result — PASS, decisively:**

| condition | keyword-correct | judge-correct | avg ctx tokens |
|---|---|---|---|
| **retrieval** | **16/16** | **16/16** | 972 |
| naive (stuff ~30K tok) | 0/16 | 0/16 | 30,000 |
| baseline (no context) | 0/16 | 0/16 | 0 |

Evidence retrieved into top-6: **16/16**. Token savings: **30.9×**. Baseline 0/16 confirms the questions genuinely require the repo (non-guessable). Naive's model answers were honest "NOT FOUND" (fails on *coverage*, not hallucination — the model correctly reports the file isn't in its window). Confirmatory run (`f3-killtest-v2`, naive given its strongest shot: only the answer-bearing dirs `cmd/internal/pkg/mcp` stuffed to ~42.5K tokens near the 64K ceiling) **corroborates: retrieval 16/16, naive still 0/16, 43.7× fewer tokens** — all 16 evidence files fall outside even that generous window, because a 1,720-file repo doesn't fit any 64K window.

**Status:** passed 2026-06-25 (evidence: Job `f3-killtest` logs; `F3_RESULT_JSON … "verdict": "PASS"`). Harness preserved at `/private/tmp/f3-killtest/` (f3eval.py + questions.json + job.yaml).

## Slices

### Slice 1 — kill-test (DONE)
Prove retrieve+rerank ≥ stuffing on real repo-Q&A. ✓ PASS (above).

### Slice 2 — agent-facing read path (the core capability) — BUILT 2026-06-25
Productized the validated `f3eval.py` retrieval logic into a serving capability: the **`codebase-answer`** sibling service (`deploy/system/codebase-answer/`) — a CPU-only Deployment+Service running a stdlib `POST /v1/answer {query, collection?, top_k?, top_n?} → {answer, citations[{path,score}], context_tokens}` over `embed → qdrant-search → rerank → generate`. Mirrors the voice-stack sibling pattern (pyannote/kokoro). In-cluster consumers hit `http://codebase-answer.flexinfer-system.svc:8000/v1/answer`. Reuses the proven primitives; no new model work.
- Acceptance: live `/v1/answer` returns correct cited answers on loom-core questions (validated against the same kill-test question set).

### Slice 2.1 — proxy front-door — BUILT (code) 2026-06-25
Exposes Slice 2 through the platform endpoint: a `/v1/rag` reverse-proxy route in `flexinfer-proxy` mirroring `/diarize` — `handleCodebaseAnswer` (rewrites `/v1/rag` → sibling `/v1/answer`), `CodebaseAnswerUpstream` config + `FLEXINFER_CODEBASE_ANSWER_UPSTREAM` env, wired in the chart (`proxy.codebaseAnswerUpstream` defaults to the sibling Service). 4 unit tests pass (503-unset / 500-bad-url / forwards-with-path-rewrite / 502-down); full proxy package green; helm renders the env.
- **Activation:** dormant until the next proxy image publish + digest repin (the proxy image is digest-pinned; merging Go doesn't auto-roll it). Route is config-gated (empty upstream → 503), so zero serving-path risk meanwhile.
- Acceptance (post-rollout): `POST /v1/rag` through the proxy returns the same as the sibling `/v1/answer`.

### Slice 3 — chunking quality bake-off
The bge index uses naive line-windows; the morph `codebase_memory_v1` uses AST chunking. Run the kill-test harness against BOTH indexes (swap `COLLECTION`) and pick the winner, or justify keeping line-windows. Cheap: the harness already parameterizes the collection.
- Acceptance: a measured retrieval-quality comparison (line-window vs AST) on ≥30 questions.

### Slice 4 — index coverage + freshness
`codebase-reembed` indexes loom-core only, and the NFS `devbox-ws` export mirrors only loom/loom-core (flexinfer is absent — discovered during the kill-test). Generalize: index the repos agents actually work in; fix the source mount or repoint. Decide nightly vs on-change.
- Acceptance: ≥2 agent-relevant repos queryable through the Slice-2 endpoint.

### Slice 5 — retrieval-quality gate (F1 tie-in)
Extend `model-eval-gauntlet` (currently throughput-only) with a retrieval-quality + coherence dimension built from the kill-test harness, so index/chunker/model changes are gated, not hoped. This is the brainstorm's repurposed F1.
- Acceptance: a gauntlet run emits a retrieval-quality score row alongside throughput.

### Slice 6 — multi-turn prefill (F5 tie-in)
Validate the APC prefix-cache canary for agent loops (growing context re-sent each turn). Bench TTFT on turn N with/without APC.
- Acceptance: measured multi-turn TTFT improvement with prefix-cache on.

## Handoff
- Slices 2–6 → `plan-loom-core` / `feature-dev` as they're picked up. Slice 2 is the highest-leverage next step (the actual capability).
- Kill-test harness (reusable for Slices 3 & 5): `/private/tmp/f3-killtest/`.
