# mcp-codebase-memory

MCP server for semantic codebase indexing + search.

Supports Go, TypeScript, JavaScript, Python, and Rust indexing.

## Tools

- `codebase_stats`
- `codebase_delete_repo`
- `codebase_index_start`
- `codebase_index_poll`
- `codebase_index_cancel`
- `codebase_search`
- `codebase_get_context`
- `codebase_find_callers`
- `codebase_find_callees`

## Configuration (env vars)

Qdrant:

- `CODEBASE_QDRANT_URL` (fallback: `QDRANT_URL`, default: `http://localhost:6333`)
- `CODEBASE_QDRANT_API_KEY` (fallback: `QDRANT_API_KEY`)
- `CODEBASE_QDRANT_COLLECTION` (default: `codebase_memory_v1`)
- `CODEBASE_QDRANT_DISTANCE` (default: `Cosine`)

Embeddings (Morph):

- `CODEBASE_EMBED_API_KEY` (fallback: `MORPH_API_KEY`) (required for search/index)
- `CODEBASE_EMBED_BASE_URL` (fallback: `MORPH_BASE_URL`, default: `https://api.morphllm.com/v1`)
- `CODEBASE_EMBED_MODEL` (fallback: `MORPH_EMBED_MODEL`, default: `morph-embedding-v3`)

Indexing:

- `CODEBASE_REPO_ID` (optional default `repo_id` if not provided per-call)
- `CODEBASE_EMBED_BATCH_SIZE` (default: `64`)
- `CODEBASE_UPSERT_BATCH_SIZE` (default: `64`)
- `CODEBASE_INDEX_CONCURRENCY` (default: `4`) (reserved for future parallel indexing)
- `CODEBASE_SCROLL_LIMIT` (default: `256`)
- `CODEBASE_MAX_FILE_BYTES` (default: `2097152`)

HTTP:

- `HTTP_TIMEOUT` (seconds, default: `30`)
- `HTTP_RETRIES` (default: `0`)
- `TLS_SKIP_VERIFY` (default: `false`)

## Build

`make mcp-codebase-memory`

## Notes

- `codebase_index_start` defaults to indexing all supported languages if `languages` is omitted.
- `codebase_delete_repo` requires `confirm=true` (and supports `dry_run=true`).
