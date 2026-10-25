---
title: Qwen3-Embedding and Qwen3-Reranker on gfx906
description: Qwen3-Embedding-4B and Qwen3-Reranker-0.6B llama.cpp lanes on the Radeon VII retrieval plane, with their qualification against bge-large, gte-qwen2 and bge-reranker.
---

# Qwen3-Embedding and Qwen3-Reranker on gfx906

The Radeon VII retrieval plane has two cold, additive llama.cpp lanes:

| Lane | Manifest | Output | Aliases |
| --- | --- | --- | --- |
| Qwen3-Embedding-4B Q8_0 | `deploy/models/qwen3-embedding-4b-radeonvii.yaml` | `/v1/embeddings`, 2560-dim, L2-normalized | `qwen3-embedding`, `qwen3-embedding-4b` |
| Qwen3-Reranker-0.6B Q8_0 | `deploy/models/qwen3-reranker-0p6b-radeonvii.yaml` | `/v1/rerank`, `relevance_score` = P(yes) in [0, 1] | `qwen3-reranker`, `qwen3-reranker-0.6b` |

- Both run at priorities below every live member (89 and 88), with `minReplicas: 0`.
- Both are opt-in by model name. `embeddings`, `embeddings-1536`, `gte-qwen2-1.5b` and
  `rerank` keep their current meaning.
- Co-residency depends on the gfx906 GPUProfile's `GPU_MAX_HW_QUEUES=1` (see
  [Co-residency](#co-residency)).

## Artifacts

| Artifact | Pinned value |
| --- | --- |
| Embedding GGUF | `Qwen/Qwen3-Embedding-4B-GGUF@f4602530db1d980e16da9d7d3a70294cf5c190be`, `Qwen3-Embedding-4B-Q8_0.gguf`, 4,279,660,224 bytes, sha256 `b60ae5ce…d416949d` |
| Reranker GGUF | `ggml-org/Qwen3-Reranker-0.6B-Q8_0-GGUF@a02f48bb4f057028298c21fa033da2b30d7742d5`, `qwen3-reranker-0.6b-q8_0.gguf`, 639,153,184 bytes, sha256 `22c9979c…23429a48` |
| FP32 references | `Qwen/Qwen3-Embedding-4B@5cf2132abc99cad020ac570b19d031efec650f2b`, `Qwen/Qwen3-Reranker-0.6B@e61197ed45024b0ed8a2d74b80b4d909f1255473` (transformers, CPU) |
| Runtime | fleet gfx906 image `registry.harbor.lan/flexinfer/runtime@sha256:90648c350783694d4858e88a31aefcdf682d171571801905c3ddd64c64c530d7` (llama.cpp b11335) |

The reranker must come from a GGUF converted with llama.cpp's rerank head. The ggml-org file
carries:

- `cls.output.weight` `[1024, 2]`
- `qwen3.pooling_type = 4` (rank)
- `classifier.output_labels = [yes, no]`
- `tokenizer.chat_template.rerank`

Conversions made as a plain causal LM lack the head and cannot rerank. For example,
`mradermacher/Qwen3-Reranker-4B-GGUF` has no `cls.output`. The 4B reranker would not fit next
to the 4B embedding lane anyway.

## Requests

Qwen3-Embedding is instruction-aware on the query side. The model card's format is
`Instruct: <task>\nQuery:<text>`, with no space after `Query:`. Documents take no prefix. The
server appends `<|endoftext|>` itself (`add_eos_token` is set in the GGUF), so clients must not
add it.

```json
{
  "model": "qwen3-embedding",
  "input": [
    "Instruct: Given a code search query, retrieve relevant code snippets that answer the query\nQuery:retry an HTTP request with exponential backoff",
    "func retry(ctx context.Context, call func() error) error { ... }"
  ]
}
```

On these code-search samples, raw queries scored the same as instructed ones (macro nDCG@10
0.949 vs 0.948). Consumers that send no prefix today lose nothing measurable.

- llama-server **ignores** the OpenAI `dimensions` field and always returns 2560 floats.
  `flexinfer-proxy` forwards the body unchanged.
- MRL truncation is free down to 768 dims on this data (below), but it has to happen
  client-side: truncate, then renormalise.
- An input longer than `contextSize` (4096 tokens) returns HTTP 400
  `exceed_context_size_error`.

Rerank requests are cohere-style (`query`, `documents`, optional `top_n`). The lane's template
uses a fixed instruct: "Given a web search query, retrieve relevant passages that answer the
query". Rank pooling cannot split a pair across ubatches, so each query+document pair plus
about 70 template tokens must fit 1024 tokens.

llama-server answers an over-long pair with **HTTP 500** `input (N tokens) is too large to
process`, not 400. `bge-reranker-radeonvii` behaves the same at 512 tokens. Clients should cap
document length, not just retry.

Keep separate collections for each embedding model:

- bge-large is 1024-dim.
- gte-qwen2 is 1536-dim.
- Qwen3 is 2560-dim and a different space from both. Truncating it to 1536 does not make it
  compatible with gte.

The migration is in `.loom/qwen3-embedding-reindex-plan-2026-10-24.md`.

## Co-residency

The Radeon VII has 24 HWS compute queues. An uncapped llama-server takes 4–6 of them, and the
warm plane alone reached 26. Past 24 the runlist is oversubscribed and live lanes stall. The
mechanism and the evidence are in `docs/dev/gfx906-gpu-support.md`.

On 2026-10-24 the user approved `GPU_MAX_HW_QUEUES=1` in the gfx906 GPUProfile (!1140). The
runtime pod restarted once at 15:39:08Z, and the four warm lanes were Ready again within 26 s.

| | Queues | bge-large | gte-qwen2 | bge-reranker | orpheus |
| --- | ---: | --- | --- | --- | --- |
| Before restart (15:37Z, through the proxy, 30 requests each) | 26 | p50 0.31 / max 0.82 s | p50 0.31 / max 7.6 s | p50 0.31 / max 15.2 s | p50 0.28 / max 2.6 s |
| After restart (15:40Z, through the proxy, 40 each) | 14 | p50 0.21 / max 0.70 s | p50 0.16 / max 0.69 s | p50 0.19 / max 0.44 s | p50 0.23 / max 0.51 s |
| Both Qwen3 lanes resident, 4B embedding at full duty (16:05–16:08Z, direct to each lane's Service, 49 checks) | 20 | p50 8 ms / max 23 ms | p50 74 ms / max 195 ms | p50 14 ms / max 36 ms | p50 22 ms / max 37 ms |
| Deployed lanes just activated through the proxy (17:46Z, direct, 20 each) | 20 | p50 8 ms / max 18 ms | p50 12 ms / max 18 ms | p50 11 ms / max 23 ms | p50 10 ms / max 16 ms |

The deployed lanes (!1146) were verified through `flexinfer-proxy`:

- The first, cold call took 11.6 s for the embedding lane and 4.5 s for the reranker.
- Warm embedding p50 through the proxy is 0.155 s.
- The deployed embedding vectors match the qualification run at cosine 1.000.
- Throughput through the proxy is 906 tok/s on code documents.

With both Qwen3 lanes loaded, the total is 20 queues. No new `Runlist is getting
oversubscribed` message appeared after the cap. Admission estimates are
8,100 + 5,600 + 1,700 = 15,400 of 16,384 MB.

Humanizer (priority 130, 11,000 MB) evicts both lanes and orpheus when called. Callers see
the usual cold-start wait: 5.3 s for the embedding lane and 3.8 s for the reranker with warm
page cache.

`pyannote-diarization` and `faster-whisper` also run on this node outside the controller's
budget. Pyannote holds 2 queues.

**Measure through each lane's Service, not only through the proxy.** `flexinfer-proxy` runs on
`k3s-cp-1` with a 200m CPU limit and is CFS-throttled in 14–26% of periods all day. With the
card idle at 16:03Z, gte answered directly in 12–19 ms, but through the proxy in p50 0.79 s,
max 10 s. Proxy latency therefore does not show GPU contention. The before/after rows above
both include it.

## Results: 2026-10-24

### gfx906 serving

| Check | Qwen3-Embedding-4B Q8_0 | Qwen3-Reranker-0.6B Q8_0 |
| --- | --- | --- |
| GPU offload | `offloaded 37/37 layers to GPU`; token_embd CPU-mapped (393 MiB host) | `offloaded 29/29 layers to GPU` |
| Buffers at the manifest config | model 4,076 + KV 576 + compute 305 MiB | model 604 + KV 112 + compute 598 MiB |
| Per-process VRAM (KFD) | 5,105 MiB (ctx 4096 / ubatch 512) | 1,463 MiB (ctx 1024) |
| Load to `/health` | 5.3 s warm, 16.0 s cold from NVMe | 3.8 s |
| FP32 parity | minimum cosine 0.99942 over 13 inputs; tokens 956/956; single-vs-batch 0.99999995 | P(yes) within 0.0084 of transformers FP32 over 6 pairs, same order |
| Output | 2560 dims, norm 1.0; `dimensions` ignored | `relevance_score` = P(yes) |
| Throughput (`parallel 1`, 16 inputs per request) | 826–903 input tok/s on code documents; 413–553 tok/s on short queries, where per-input overhead dominates; 549k tokens in the corpus run | 1.2 s per 20-document query |

The live lanes measured through the proxy the same day: bge-large ~5.1k tok/s and gte-qwen2
~1.7k tok/s.

### Retrieval quality

Queries are docstrings or doc comments and documents are functions, truncated to 1,000
characters so bge-large's 512-token lane sees the same inputs. The sets:

- `csn-go`: CodeSearchNet go test, 1,000 queries over 1,000 functions.
- `csn-python`: CodeSearchNet python test, 800 queries over 1,000 functions.
- `flexinfer-go`: this repository's 498 Go doc comments with the identifier stripped, over the
  498 function bodies. Its queries are domain-relevant and it is the least saturated set.

Each model uses its own documented query prefix for the instructed rows. Qwen3 vectors come
from the gfx906 Q8_0 lane; bge and gte vectors come from the live lanes.

| Model | Queries | Dims | csn-go nDCG@10 | csn-python nDCG@10 | flexinfer-go nDCG@10 | Macro nDCG@10 | Macro MRR@10 | Macro R@1 |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| bge-large-en-v1.5 | raw | 1024 | 0.9528 | 0.9007 | 0.8027 | 0.8854 | 0.8578 | 0.7868 |
| bge-large-en-v1.5 | instructed | 1024 | 0.9493 | 0.8923 | 0.7951 | 0.8789 | 0.8507 | 0.7784 |
| gte-Qwen2-1.5B | raw | 1536 | 0.9535 | 0.9432 | 0.8624 | 0.9197 | 0.8999 | 0.8471 |
| gte-Qwen2-1.5B | instructed | 1536 | 0.9622 | 0.9495 | 0.8787 | 0.9301 | 0.9127 | 0.8638 |
| **Qwen3-Embedding-4B** | raw | 2560 | **0.9687** | 0.9502 | **0.9278** | **0.9489** | **0.9354** | **0.8962** |
| **Qwen3-Embedding-4B** | instructed | 2560 | 0.9642 | **0.9534** | 0.9256 | 0.9477 | 0.9342 | 0.8945 |

On flexinfer-go, Qwen3-4B is +4.9 nDCG@10 over gte-qwen2 instructed (the production lane's
best case) and +12.5 over bge-large.

MRL truncation of the Qwen3 vectors (raw queries; truncate, then renormalise):

| Dims | 2560 | 2048 | 1536 | 1024 | 768 | 512 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Macro nDCG@10 | 0.9489 | 0.9479 | 0.9492 | 0.9494 | 0.9488 | 0.9459 |
| flexinfer-go nDCG@10 | 0.9278 | 0.9248 | 0.9255 | 0.9282 | 0.9276 | 0.9166 |

### Reranking

The first stage is bge-large with raw queries, top 20. Each set used 100 queries (hash-sampled),
with documents truncated to 600 characters for bge-reranker's 512-token lane.

| Set | Gold in top 20 | First stage nDCG@10 | bge-reranker-v2-m3 | Qwen3-Reranker-0.6B | Top-1 agreement | Mean Spearman |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| csn-go | 0.99 | 0.942 | 0.915 | **0.957** | 0.85 | 0.58 |
| csn-python | 0.98 | 0.898 | 0.782 | **0.913** | 0.61 | 0.47 |
| flexinfer-go | 0.96 | 0.765 | 0.822 | **0.868** | 0.71 | 0.58 |

- bge-reranker lowers the strong CodeSearchNet first stages. Qwen3-Reranker raises all three.
- Recall@1 on flexinfer-go: first stage 0.60, bge-reranker 0.71, Qwen3 0.75.
- The two rerankers agree on the top document 61–85% of the time.
- Qwen3 costs 1.16–1.26 s per 20-document query on the VII, against 0.45–0.57 s for
  bge-reranker.

## Reproduce

Harness: `scripts/eval-retrieval-quality.py` (tests in `scripts/test_eval_retrieval_quality.py`).
It uses only the standard library except for numpy in `score` and `rerank`.

- `--duty-cycle` paces bulk work on a shared GPU.
- `--resume` continues an interrupted `embed` run.

```bash
python3 scripts/eval-retrieval-quality.py corpus --csn csn-go.json --csn csn-python.json --out corpus.json
python3 scripts/eval-retrieval-quality.py embed --corpus corpus.json \
  --base-url http://flexinfer-proxy.flexinfer-system.svc --model qwen3-embedding \
  --query-style qwen3 --out runs/qwen3-4b
python3 scripts/eval-retrieval-quality.py score --corpus corpus.json \
  --runs gte=runs/gte-qwen2 qwen3=runs/qwen3-4b --mrl --out score.json
python3 scripts/eval-retrieval-quality.py rerank --corpus corpus.json --first-stage runs/bge-large \
  --first-stage-kind q-raw --doc-chars 600 \
  --lane bge=http://flexinfer-proxy.flexinfer-system.svc=bge-reranker-radeonvii \
  --lane qwen3=http://flexinfer-proxy.flexinfer-system.svc=qwen3-reranker --out rerank.json
```

The CodeSearchNet inputs are `mteb/CodeSearchNetRetrieval@68e8f0731a656fa4bd5b7c81936d95ad48a39bfe`
`{go,python}-{queries,corpus,qrels}` (test split), saved as one JSON per language with keys
`queries`, `corpus` and `qrels`.

The qualification ran in throwaway `k3s ctr` containers on the fleet image, with the same
environment as the runtime's subprocesses (`HSA_ENABLE_SDMA=0`, `HSA_USE_SVM=0`, the hipmeminfo
shim, `GPU_MAX_HW_QUEUES=1`). A watchdog killed a probe whenever a live lane stalled.

Evidence:

- `.loom/qwen3-embedding-qualification-2026-10-24.json`
- matrix row `qwen3-embedding-gfx906-2026-10-24`
- local raw evidence in `.loom/local/qwen3-embedding-validation-2026-10-24/` (main checkout)
