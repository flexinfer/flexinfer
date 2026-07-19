# codebase-answer — read-path RAG service (F3 Slice 2)

In-cluster service that answers a natural-language question over an embedded
codebase by **retrieving** the relevant chunks (not stuffing the whole repo into
the model's window) and generating a cited answer. This is the agent /
repo-in-context read path from the F3 plan
([.loom/30-implementation-plan-f3-retrieval-readpath-2026-06-25.md](../../../.loom/30-implementation-plan-f3-retrieval-readpath-2026-06-25.md)).

## Why

The F3 kill-test (2026-06-25) proved retrieval beats raw context-stuffing on
real repo-Q&A: **retrieval 16/16 vs naive 0/16, at 30.9× fewer tokens** — because
a 1,720-file repo doesn't fit any 64K window, but retrieval finds the right chunk
every time. This service productizes the validated
`embed → qdrant-search → rerank → generate` path.

## Pipeline

1. Embed the query via **bge-large** (`flexinfer-proxy` `/v1/embeddings`, 1024-dim).
2. Search the embedded-codebase index in **qdrant** (`codebase_memory_bge_v1`, the
   collection the nightly `deploy/tasks/codebase-reembed` job writes).
3. Rerank the top-N candidates via **bge-reranker** (`/v1/rerank`).
4. Generate a cited answer from the top-K chunks via the chat model (`/v1/chat/completions`).

All four primitives are existing flexinfer-proxy endpoints — this service is
CPU-only orchestration (no GPU, no NFS).

## API

```
POST /v1/answer
  {"query": "...", "collection"?: "...", "top_n"?: 24, "top_k"?: 6}
->
  {"answer": "...",
   "citations": [{"path": "pkg/...", "score": 0.79}, ...],
   "context_tokens": 972, "model": "...", "collection": "...", "elapsed_ms": 1234}

GET /readyz -> 200 when BGE embed/rerank and Qdrant are Ready and `loom-workhorse` is advertised
GET /livez  -> 200 when the local HTTP process is alive
```

## Configuration (env on the container)

| Env | Default | Purpose |
|-----|---------|---------|
| `PROXY_URL` | `http://flexinfer-proxy…` | OpenAI-compatible base (embed/rerank/chat) |
| `EMBED_MODEL` | `bge-large-radeonvii` | gfx906 bge embeddings lane |
| `RERANK_MODEL` | `bge-reranker-radeonvii` | gfx906 reranker lane |
| `CHAT_MODEL` | `loom-workhorse` | serverless 128K answer-generator route |
| `QDRANT_URL` | `http://192.168.50.176:6333` | canonical qdrant |
| `QDRANT_API_KEY` | _(secret)_ | `qdrant-credentials/api-key` |
| `DEFAULT_COLLECTION` | `codebase_memory_bge_v1` | embedded-codebase index |
| `RETR_N` / `RETR_K` | `24` / `6` | cosine candidates / reranked into context |
| `MAX_PER_PATH` | `0` | per-file cap on chunks in the top-K context; `0` = off (Slice 3.1) |

`POST /v1/answer` also accepts an optional `"max_per_path"` to override the env per
request.

## Prerequisites

- The nightly `deploy/tasks/codebase-reembed` job populates `codebase_memory_bge_v1`.
- `qdrant-credentials` secret (key `api-key`) in `flexinfer-system`.

## Try it (in-cluster)

```bash
kubectl -n flexinfer-system run rag-curl --rm -it --image=curlimages/curl --restart=Never -- \
  curl -s -X POST http://codebase-answer.flexinfer-system.svc:8000/v1/answer \
  -H 'Content-Type: application/json' \
  -d '{"query":"What env var disables TLS verification in the httpclient package?"}'
```

## Reliability

- Readiness requires the embedding and reranker routes to be warm, the chat
  route to remain advertised, and the default Qdrant collection to be
  searchable (green or yellow while its optimizer runs). The chat route may be
  cold: keeping this pod Ready lets an answer request reach the proxy and
  trigger its serverless cold-start queue. Liveness remains separate.
- `codebase-answer-alerts` fires when the nightly index is older than 30 hours,
  a re-embed Job fails, or this Deployment has no Ready replica.
- The re-embed job adaptively isolates and trims tokenizer-overflow chunks while
  preserving vector/payload alignment, then enables HNSW for small collections.

## Follow-ups

- **Slice 2.1** — proxy front-door: a `/v1/rag` reverse-proxy route in
  `flexinfer-proxy` (mirroring `/diarize`) so this is reachable through the
  platform endpoint. Needs a proxy rebuild + `FLEXINFER_CODEBASE_ANSWER_UPSTREAM`
  wiring; deferred so it can be validated on its own rollout.
- **Slice 3** — chunking bake-off (line-window `codebase_memory_bge_v1` vs AST
  `codebase_memory_v1`), reusing the kill-test harness.
- **Slice 3.1** — multi-file context diversification (`MAX_PER_PATH`): caps how
  many chunks one file contributes to the top-K context so multi-file answers get
  secondary files into the window. Shipped dormant (`0`); activate with
  `MAX_PER_PATH=3` on this Deployment once the harness measures a judge gain on the
  hard 18-Q set (`eval/f3-retrieval`).

## Reversibility

Fully additive: a CPU Deployment + Service + ConfigMap. Remove by deleting this
directory from `deploy/system/kustomization.yaml` (Flux prunes it).
