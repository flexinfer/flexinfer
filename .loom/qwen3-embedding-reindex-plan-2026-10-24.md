# Qwen3-Embedding re-index plan (2026-10-24)

Status: **plan only**. No production collection was re-indexed.

Lanes: `deploy/models/qwen3-embedding-4b-radeonvii.yaml` (2560-dim) and
`deploy/models/qwen3-reranker-0p6b-radeonvii.yaml`.

Evidence: `.loom/60-validation-matrix.md` row `qwen3-embedding-gfx906-2026-10-24`,
`.loom/qwen3-embedding-qualification-2026-10-24.json`, and `docs/user/qwen3-embedding.md`.

## Recommendation

**GO for the canary re-index (step 1 below). Production cutovers stay gated on the canary.**

| Gate | Status |
| --- | --- |
| 1. Queue fix | **Passed 2026-10-24.** `GPU_MAX_HW_QUEUES=1` landed in the gfx906 GPUProfile (!1140). One runtime pod restart took the warm lanes from 26 to 14 compute queues (the card has 24). |
| 2. Lane gate | **Passed** in probes on the deployed configs. Both Qwen3 lanes resident make 20 queues, with no runlist oversubscription. During a 2.8-minute full-duty 4B embedding run, gte/bge/bge-reranker/orpheus answered directly at their Services with p95 11–143 ms and max 195 ms, and no errors. **Deployed and verified** (!1146, 17:43–17:47Z): both lanes are Ready/Active through `flexinfer-proxy` (cold first call 11.6 s / 4.5 s), the deployed vectors match the qualified run at cosine 1.000, the plane holds 20 queues, and the live lanes answered directly in ≤ 30 ms during the cold loads. |
| 3. Canary gate | **Open.** Needs Qdrant write access (both Qdrant instances are API-key protected). |

Quality favours the switch:

- Embedding: Qwen3-Embedding-4B beats gte-qwen2 (the production 1536 lane) by +1.9 macro
  nDCG@10 and +4.9 on this repository's Go code.
- Reranking: Qwen3-Reranker-0.6B beats bge-reranker-v2-m3 on all three sets.

The open item is capacity. A full `codebase_memory_v1` re-index is 6–9 GPU-hours on the VII
(see Capacity).

## Dimension decision: native 2560, no 1536 truncation

- No server can truncate today.
  - llama-server b11335 ignores the OpenAI `dimensions` field: a request with
    `dimensions: 1536` returns 2560 floats.
  - `flexinfer-proxy` forwards the body unchanged. It only validates `dimensions ≥ 1`
    (`pkg/validation/openai.go`).
  - Exposing 1536 would need new proxy code to truncate and renormalise.
- Truncation buys nothing on migration. Equal dimensions do not make vector spaces
  compatible, so every gte-era row must be re-embedded either way.
- Consumers adapt to 2560 by themselves:
  - agent-context and codebase-memory take the dimension from the first vector and refuse a
    mismatch instead of recreating. A new collection name (env override) is all they need.
  - pm hardcodes `VectorSize = 1536` (loom-core `pkg/pm/config.go:17`), but `pm_risks` holds
    0 points.
  - xfiles reads `XFILES_EMBEDDING_DIMENSIONS`.
- Storage is negligible: about 85k points × 2560 × 4 B ≈ 0.9 GB, against 0.5 GB at 1536.
- MRL truncation stays available client-side (truncate, then renormalise). On the code
  samples it is free down to 768: macro nDCG@10 is 0.9489 at 2560, 0.9492 at 1536, 0.9494 at
  1024, 0.9488 at 768 and 0.9459 at 512. A consumer that wants smaller collections can store
  1024. Choose before the re-index, because vectors cannot be re-cut afterwards without a
  reload from the full-width output.

Do **not** repoint `embeddings-1536` / `gte-qwen2-1.5b` at Qwen3:

- codebase-memory reuses cached vectors keyed on `embed_model` + `content_hash`, so unchanged
  chunks would keep gte vectors.
- agent-context rows would carry the same `embed_model` label for two spaces.

Consumers opt in by name (`qwen3-embedding` / `qwen3-embedding-4b`) and by a new collection
name.

## Query instruction

Qwen3-Embedding is instruction-aware on the **query side only**. Use the format
`Instruct: <task>\nQuery:<text>`, with no space after `Query:`. Documents get no prefix.

loom-core sends no prefix today, and xfiles already prefixes gte queries. On the code samples
raw queries scored the same as instructed ones (macro nDCG@10 0.9489 vs 0.9477), so the
prefix is optional for these consumers. If a consumer adds it later, that is a query-path
change and needs no re-index.

## Collections

The table covers Qdrant `192.168.50.176:6333`, inventoried 2026-10-24. In-cluster
codebase-memory writes to `qdrant-fast.loom-hub`, which is API-key protected and was **not**
inventoried. Operator: list it before step 3.

| Collection | Points | Dim | Writer | Plan |
|---|---:|---:|---|---|
| `codebase_memory_v1` | 64,893 | 1536 | codebase-memory | Every row is Morph-era (`morph-embedding-v3` or structure-only `""`); none is `embeddings-1536`. Semantic search here is already stale, so this is the biggest win. Run a full `codebase_index_start` into `codebase_memory_q3e4b_v1` (`CODEBASE_QDRANT_COLLECTION`). |
| `agent_context_v1` | 18,002 | 1536 | agent-context | **Mixes two spaces** (`morph-embedding-v3` and `embeddings-1536` rows). Re-embed from payload text into `agent_context_q3e4b_v1`. |
| `agent_plans_v1` | 1,986 | 1536 | agent-context | Re-embed into `agent_plans_q3e4b_v1` |
| `agent_memory_v1` | 828 | 1536 | agent-context | Re-embed. The dedupe thresholds 0.85/0.90/0.95 (`memory_hierarchy_core.go:21-41`) must be recalibrated first. |
| `agent_briefs_v1` | 98 | 1536 | agent-context | **Canary.** Re-embed into `agent_briefs_q3e4b_v1`. |
| `agent_patterns_v1`, `agent_tasks_v1`, `agent_annotations_v1` | 8 / 7 / 1 | 1536 | agent-context | Re-embed with the canary |
| `pm_risks`, `fractal_knowledge` | 0 / 0 | 1536 | pm / fractal | Recreate empty at 2560 after the loom-core `VectorSize` change |
| `xfiles-ufo` | 100 | 1536 named `dense` + sparse | xfiles | `scripts/reembed_collection.py --collection xfiles-ufo-q3e4b`, with `XFILES_EMBEDDING_DIMENSIONS=2560` |
| `codebase_memory_bge_*_v1` | 96,748 / 19,740 / 8,447 / 2,482 | 1024 | bge cron, codebase-answer, mills | **Out of scope.** These stay on bge-large. A later phase can follow once the Qwen3 lane has served codebase-memory for two weeks. |
| 4-dim metadata, `ws-*` (Kilo, 1024), `articles`/`codex` (384), bench (1-dim) | — | — | — | Untouched |

The existing backfill tools cannot migrate anything. `agent_embedding_backfill` and
`codebase_embedding_backfill` only repair placeholder rows in place, at the same dimension, and
take no target model or collection.

The agent-context collections need a one-off re-embed job:

1. Scroll the old collection with payloads.
2. Embed each row's stored text through `qwen3-embedding`: documents get no prefix; queries
   are not stored.
3. Upsert to the new name with the same point id and payload, plus `embed_model=qwen3-embedding-4b`.
4. Verify the counts match.

## Capacity

Measured on the VII with the warm plane and the Qwen3 reranker resident, `parallel 1`, 16
inputs per request:

- 826–903 input tok/s on code documents.
- 413–553 tok/s on short queries, where per-input overhead dominates.
- For comparison, bge-large runs at 5.1k tok/s and gte-qwen2 at 1.7k tok/s.

| Collection | Est. tokens | Est. VII time |
|---|---:|---:|
| `agent_briefs_v1` + small ones | < 0.1 M | < 3 min |
| `agent_context_v1` + plans + memory | ~4 M | ~1.5 h |
| `codebase_memory_v1` (2000-token chunks, 200 overlap) | ~20–26 M | ~6–9 h |

After the queue cap, a full-duty re-index did not move the live lanes' latency on the direct
path (p95 ≤ 143 ms). Running it on the VII during the day is therefore acceptable on GPU
grounds.

Two caveats:

- The live lanes still share the card's compute. Prefer an evening window for
  `codebase_memory_v1`.
- Pass consumer-side batches of ≤ 16 inputs.

`scripts/eval-retrieval-quality.py --duty-cycle` shows the pacing pattern if a job needs it.
The alternative is a temporary gfx1100 copy of the lane: the 7900 XTX has about 4× the fp16
throughput. The vectors are equivalent across backends. FP32 parity is 0.9994 on gfx906 and
0.99999 for fp16 MPS, so cross-backend vectors agree at the ~1e-3 cosine level.

Re-measure with `parallel` auto (4 slots, unified KV) before the big job. That is the main
tuning knob left.

## Write-path fallback

The gte CPU twin (`gte-qwen2-1p5b-cpu-5930k`, 1536-dim) cannot back a 2560 collection: the
dimension check refuses the write.

After cutover, a failed write lands as a placeholder row, and the existing backfill tools
repair it once the lane is back. Those tools work because the dimension then matches. A
Qwen3-4B CPU twin is not worth it: even the Mac M4's GPU managed only about 90 tok/s in fp16,
and a CPU would be slower.

## Cutover order

| Step | What | Rollback |
|---|---|---|
| 0 | Queue fix + wire lanes (gates 1–2): **done 2026-10-24** (!1140 + this MR) | Remove both entries from `deploy/models/kustomization.yaml`. Keep the queue cap. |
| 1 | **Canary:** re-embed `agent_briefs_v1` (98) and the three tiny agent collections into `*_q3e4b_v1`. Recall check: 30 known-item queries (brief titles → brief) must reach R@5 ≥ the gte collection's. | Drop the new collections |
| 2 | codebase-memory: full index into `codebase_memory_q3e4b_v1`. Set `CODEBASE_EMBED_MODEL=qwen3-embedding` and `CODEBASE_QDRANT_COLLECTION`. Add the query instruction in `search_handlers.go`. | Revert the env and keep the old collection; it is stale already |
| 3 | agent-context: re-embed job for context/plans/memory, recalibrate the dedupe thresholds, then switch `AGENT_CONTEXT_EMBED_MODEL` and the `*_COLLECTION` envs together | Revert the env; old collections are untouched |
| 4 | pm: `VectorSize` → 2560 (loom-core), recreate `pm_risks` | Revert the constant |
| 5 | xfiles: reembed into the new collection, flip the env | Flip back |
| 6 | After 14 days stable: delete the old 1536 collections, retire `gte-qwen2-1p5b-radeonvii` (frees 2.6 GB and 6 queues), keep the gte CPU twin until no 1536 reader remains | — |

Rerank: `qwen3-reranker` is opt-in by model name.

- agent-context sends `top_k` (ignored by llama-server, which returns all scores, as with bge).
- codebase-answer and xfiles use ordering only, so a switch is a model-name change.
- Each query+document pair must fit 1024 tokens with the template, against bge-reranker's 512.

## Results (qualification run, 2026-10-24)

Embedding, nDCG@10. bge and gte come from the live lanes; Qwen3 comes from the gfx906 Q8_0
lane on the deployed config.

| Model | Query | csn-go | csn-python | flexinfer-go | Macro |
|---|---|---:|---:|---:|---:|
| bge-large (1024) | raw | 0.953 | 0.901 | 0.803 | 0.885 |
| gte-qwen2 (1536) | instructed | 0.962 | 0.950 | 0.879 | 0.930 |
| Qwen3-Embedding-4B (2560) | raw | 0.969 | 0.950 | **0.928** | **0.949** |

Rerank of the bge-large top 20, nDCG@10 (first stage → bge-reranker-v2-m3 → Qwen3-Reranker-0.6B):

| Set | First stage | bge-reranker | Qwen3-Reranker |
|---|---:|---:|---:|
| csn-go | 0.942 | 0.915 | 0.957 |
| csn-python | 0.898 | 0.782 | 0.913 |
| flexinfer-go | 0.765 | 0.822 | 0.868 |

The rerankers agree on the top document 61–85% of the time.

Full tables, MRL sweep and method: `docs/user/qwen3-embedding.md`. Raw numbers:
`.loom/qwen3-embedding-qualification-2026-10-24.json`.
