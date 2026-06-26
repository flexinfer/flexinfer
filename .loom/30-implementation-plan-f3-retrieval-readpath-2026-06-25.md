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

### Slice 3 — chunking quality bake-off — UNBLOCKED + first finding 2026-06-25
The easy 16-Q set saturates retrieval at 16/16, so it can't discriminate chunking. Built a **harder** 18-Q set (`eval/f3-retrieval/questions.hard.loomcore.json`: multi-file / behavioral / long-construct / semantic-distance) that does NOT saturate → Slice 3 unblocked. Ran the harness (retrieval-only, `SKIP_NAIVE`) on the hard set across two bge chunkings:

| chunking | chunks | ev_retrieved | judge CORRECT |
|---|---|---|---|
| current 45/8 | 21,607 | 15/18 | 8/18 |
| high-overlap 40/20 | 34,379 | 16/18 | 9/18 |

Findings: (1) the hard set discriminates (8–9/18 vs 16/16); (2) **chunk overlap is not the lever** — +59% chunks bought a within-noise +1, even with a freshness confound favoring high-overlap (collection dropped); (3) **the bottleneck is answer synthesis, not retrieval recall** — `ev_retrieved` 15–16/18 but `judge` ~8–9/18: the right file is usually found, the model fails to assemble the answer on multi-file questions. (bge-vs-morph-AST compare is still blocked: morph embed API is down (522) + qdrant unreachable from the Mac.)
- **Redirect:** Slice 3+ effort → more/multi-file context per query (`RETR_K`↑, pull `secondary_paths`) or a stronger answer model — NOT chunk tuning. A clean same-snapshot bake-off (tooling ready) would tighten finding (2) but is low-priority given finding (3).
- Acceptance: ✅ measured discriminating comparison on 18 questions (above).

### Slice 3.1 — multi-file context diversification — BUILT (code, dormant) 2026-06-25
Acts on Slice 3 finding (3): the right files are retrieved (`ev_retrieved` 15–16/18) but the top-K context can be **dominated by one file**, so multi-file questions never get the secondary file into the window even when it was in top-N. Added `diversify_selection(order, paths, top_k, max_per_path)` — a pure, unit-tested selection that caps per-file chunk count and back-fills so context is never starved below the plain top-K slice. Pulls `secondary_paths` into context (the named lever) with **no new upstream calls** (re-selects from the already-fetched top-N rerank), so zero added latency / failure modes. Wired into both read paths: `eval/f3-retrieval/f3eval.py` (retrieval condition) and `deploy/system/codebase-answer/configmap.yaml` (inline mirror; the service also accepts a per-request `max_per_path`).
- **Opt-in / reversible:** env `MAX_PER_PATH` default **0 = disabled = byte-for-byte current behavior** (asserted by a test case + a live parity check vs the canonical fn). The live service is unchanged until the env is flipped — mirrors the Slice 2.1 dormant-until-validated pattern. Activation = `MAX_PER_PATH=3` on the `codebase-answer` Deployment (GitOps one-liner).
- **Proven:** `eval/f3-retrieval/test_readpath.py` (11 stdlib-`unittest` cases) green; new `readpath_test` CI lint job (`python:3.12-alpine`) gates it; ConfigMap inline Python compiles; mirror parity verified.
- **Handoff (live measurement):** run the harness on the hard 18-Q set with `MAX_PER_PATH=3` vs `0` (same snapshot, `SKIP_NAIVE=1`) and compare `judge`. If multi-file judge improves, flip the service env. Needs the cluster (proxy + qdrant) — out of reach from the Mac.
- Acceptance: ✅ correct, tested, CI-gated diversification wired into both read paths, dormant by default; live judge-delta measurement queued.

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
