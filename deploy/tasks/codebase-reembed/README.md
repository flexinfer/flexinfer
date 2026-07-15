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
2. For **each repo** in `REPOS`, walks its path for source/doc files (extension
   allowlist, dir denylist).
3. Splits each file into overlapping line-window chunks.
4. Embeds chunks in batches via the OpenAI-compatible `/v1/embeddings` endpoint
   (`bge-large-radeonvii` on gfx906).
5. Upserts vectors into the repo's Qdrant collection with deterministic UUIDv5
   point IDs (namespaced by repo name), so nightly re-runs overwrite in place
   (no duplicates) and two repos never collide.
6. After a complete repository scan, removes obsolete point IDs that were not
   produced by the new scan. A partial or failed run keeps the prior index.

Generated output trees such as `coverage`, `htmlcov`, and `.nyc_output` are
excluded from discovery so test reports do not pollute retrieval or consume the
Radeon VII embedding window.

The chunker is deliberately simple (line windows, not AST) — the goal is to prove
batch throughput, not to mirror codebase-memory's AST chunker. The morph
`codebase_memory_v1` collection is **not touched**.

## Multi-repo coverage (F3 Slice 4)

The job indexes **one or more repos in a single run**, each into its **own**
collection, configured via the `REPOS` env (`;`-separated `name=path[=collection]`
records). Per-repo collections keep retrieval isolated — the read-path
(`codebase-answer` `/v1/answer`) selects a repo via its `collection` field, and
there is no cross-repo result mixing. Current set:

| Repo | Path (NFS mirror) | Collection |
|------|-------------------|------------|
| `loom-core` | `/workspace/services/loom-core` | `codebase_memory_bge_v1` (legacy name — unchanged) |
| `loom` | `/workspace/services/loom` | `codebase_memory_bge_loom_v1` |
| `flexinfer` | `/workspace/services/flexinfer` | `codebase_memory_bge_flexinfer_v1` |
| `flexdeck` | `/workspace/services/flexdeck` | `codebase_memory_bge_flexdeck_v1` |

A missing repo path is **skipped with a `WARN`** (one absent repo never fails the
nightly); the job exits non-zero only if *no* repo indexed. An omitted collection
defaults to `codebase_memory_bge_<sanitized-name>_v1`.

When `REPOS` is empty, the legacy single-repo `REPO_PATH`/`REPO_NAME`/`COLLECTION`
env is used — byte-for-byte the pre-Slice-4 behaviour.

> **NFS mirror:** the `devbox-ws` export is kept populated for these repos by the
> platform/gitops **`devbox-repo-mirror`** CronJob (`k3s/devbox/repo-mirror.yaml`),
> which clones `flexinfer`/`flexdeck` from gitlab-vm into
> `/workspace/services/<repo>` at 03:00 America/New_York — one hour before this
> job's window. `loom`/`loom-core` remain operator-maintained working copies on
> the same export. To add a new repo: append it to the mirror's `MIRROR_REPOS`
> **and** to `REPOS` here (a path that is missing on the mirror is skipped with a
> `WARN`, never fatal, so the two changes can land in either order).

## Configuration

All knobs are env vars on the container (`cronjob.yaml`):

| Env | Default | Purpose |
|-----|---------|---------|
| `EMBEDDINGS_URL` | `http://flexinfer-proxy...` | OpenAI-compatible embeddings base |
| `EMBED_MODEL` | `bge-large-radeonvii` | gfx906 bge lane |
| `QDRANT_URL` | `http://192.168.50.176:6333` | canonical Qdrant (holds morph `codebase_memory_v1`) |
| `QDRANT_API_KEY` | _(from secret)_ | `qdrant-credentials/api-key` (required for the default target) |
| `REPOS` | `loom-core=…;loom=…` | multi-repo index set: `;`-sep `name=path[=collection]` (see above) |
| `COLLECTION` | `codebase_memory_bge_v1` | single-repo fallback collection (used only when `REPOS` is empty) |
| `REPO_PATH` / `REPO_NAME` | _(unset)_ | single-repo fallback source (used only when `REPOS` is empty) |
| `BATCH_SIZE` | `64` | embeddings batch size |
| `CHUNK_LINES` / `CHUNK_OVERLAP` | `45` / `8` | line-window chunking |
| `MAX_INPUT_CHARS` | `700` | cap for the complete embedding input, including the repo/path prefix |
| `EMBED_TOKEN_HEADROOM` | `480` | adaptive retry target below BGE/llama.cpp's 512-token limit |
| `QDRANT_INDEXING_THRESHOLD_KB` | `5000` | build HNSW once a segment exceeds ~5 MiB |
| `MAX_FILES` / `MAX_CHUNKS` | `0` (unbounded) | per-repo safety clamps; truncation is logged |

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

Expected final log line: `re-embed DONE repos=4/4 files=… chunks=… elapsed=…s emb/s=…`
(`repos=N/M` = N indexed of M configured; N<M means a path was missing/skipped).

After a run, query a specific repo through the read-path service by passing its
collection, e.g. `curl -s codebase-answer.flexinfer-system.svc:8000/v1/answer -d
'{"query":"…","collection":"codebase_memory_bge_loom_v1"}'`.

## Tests

The multi-repo parsing/chunking logic is unit-tested offline (no cluster):

```bash
python3 deploy/tasks/codebase-reembed/test_reembed.py
```

The test extracts the canonical script from `configmap.yaml` (single source of
truth) and exercises `parse_repos` / `point_id` / `chunk_file`. CI gates it via
the `reembed_test` lint job on changes to the ConfigMap or test.

## Reversibility

Fully additive: a new Qdrant collection + a CronJob. Remove by deleting this
directory from `deploy/kustomization.yaml` (Flux prunes the CronJob/ConfigMap);
the `codebase_memory_bge_v1` collection can be dropped independently.
