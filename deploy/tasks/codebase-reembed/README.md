# codebase-reembed — idle-time batch appliance (Sprint 2 / S2.2)

Nightly CronJob that re-embeds an in-cluster codebase through the **gfx906 HBM2
retrieval plane** (bge-large via `flexinfer-proxy`) and writes 1024-dim vectors to
a dedicated Qdrant collection during the idle window.

This is the in-cluster batch consumer that the S1.4b proof redirected here: an
interactive consumer hitting the public Cloudflare gateway saw 3.3 emb/s (WAN
round-trip bound), but a **latency-insensitive in-cluster batch hitting
`flexinfer-proxy` directly runs at ~70.9 emb/s** (self-hosted, free), so the HBM2
throughput actually lands. See `.loom/60-validation-matrix.md` and
`.loom/30-implementation-plan-hardware-utilization-2026-06-03.md` (Sprint 2 S2.2).

## What it does

1. Mounts the shared devbox workspace NFS export read-only.
2. Walks `REPO_PATH` for source/doc files (extension allowlist, dir denylist).
3. Splits each file into overlapping line-window chunks.
4. Embeds chunks in batches via the OpenAI-compatible `/v1/embeddings` endpoint
   (`bge-large-radeonvii` on gfx906).
5. Upserts vectors into Qdrant with deterministic UUIDv5 point IDs, so nightly
   re-runs overwrite in place (no duplicates).

The chunker is deliberately simple (line windows, not AST) — the goal is to prove
batch throughput, not to mirror codebase-memory's AST chunker. The morph
`codebase_memory_v1` collection is **not touched**; this writes only
`codebase_memory_bge_v1`.

## Configuration

All knobs are env vars on the container (`cronjob.yaml`):

| Env | Default | Purpose |
|-----|---------|---------|
| `EMBEDDINGS_URL` | `http://flexinfer-proxy...` | OpenAI-compatible embeddings base |
| `EMBED_MODEL` | `bge-large-radeonvii` | gfx906 bge lane |
| `QDRANT_URL` | `http://192.168.50.176:6333` | canonical Qdrant (holds morph `codebase_memory_v1`) |
| `QDRANT_API_KEY` | _(from secret)_ | `qdrant-credentials/api-key` (required for the default target) |
| `COLLECTION` | `codebase_memory_bge_v1` | target collection (1024-dim, Cosine) |
| `REPO_PATH` / `REPO_NAME` | `/workspace/services/loom-core` | source repo |
| `BATCH_SIZE` | `64` | embeddings batch size |
| `CHUNK_LINES` / `CHUNK_OVERLAP` | `45` / `8` | line-window chunking |
| `MAX_INPUT_CHARS` | `700` | cap for the complete embedding input, including the repo/path prefix |
| `MAX_FILES` / `MAX_CHUNKS` | `0` (unbounded) | safety clamps; truncation is logged |

## Prerequisites

A `qdrant-credentials` secret (key `api-key`) in `flexinfer-system` for the
canonical Qdrant at `192.168.50.176:6333`:

```bash
kubectl -n flexinfer-system create secret generic qdrant-credentials \
  --from-literal=api-key="$QDRANT_API_KEY"
```

## Operations

```bash
# Manual one-shot run from the CronJob:
kubectl -n flexinfer-system create job --from=cronjob/codebase-reembed reembed-adhoc
kubectl -n flexinfer-system logs -f job/reembed-adhoc

# Pause the nightly schedule:
kubectl -n flexinfer-system patch cronjob/codebase-reembed -p '{"spec":{"suspend":true}}'
```

Expected final log line: `re-embed DONE files=… chunks=… elapsed=…s emb/s=…`.

## Reversibility

Fully additive: a new Qdrant collection + a CronJob. Remove by deleting this
directory from `deploy/kustomization.yaml` (Flux prunes the CronJob/ConfigMap);
the `codebase_memory_bge_v1` collection can be dropped independently.
