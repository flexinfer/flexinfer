# mcp-codebase-memory

MCP server for semantic codebase indexing + search.

Supports Go, TypeScript, JavaScript, Python, and Rust indexing.

## Tools

- `codebase_stats`
- `codebase_delete_repo`
- `codebase_index_start`
- `codebase_watch_start`
- `codebase_index_poll`
- `codebase_watch_poll`
- `codebase_index_cancel`
- `codebase_watch_stop`
- `codebase_search`
- `codebase_text_search`
- `codebase_get_definition`
- `codebase_get_references`
- `codebase_get_context`
- `codebase_find_callers`
- `codebase_find_callees`
- `codebase_call_graph`
- `codebase_module_graph`

## Configuration (env vars)

Qdrant:

- `CODEBASE_QDRANT_URL` (fallback: `QDRANT_URL`, default: `http://localhost:6333`)
- `CODEBASE_QDRANT_API_KEY` (fallback: `QDRANT_API_KEY`)
- `CODEBASE_QDRANT_COLLECTION` (default: `codebase_memory_v1`)
- `CODEBASE_QDRANT_DISTANCE` (default: `Cosine`)

Embeddings (OpenAI-compatible; defaults to Morph):

- `CODEBASE_EMBED_API_KEY` (fallback: `MORPH_API_KEY`, `OPENAI_API_KEY`) (required for semantic search and embedding-enabled indexing)
- `CODEBASE_EMBED_BASE_URL` (fallback: `MORPH_BASE_URL`, `OPENAI_BASE_URL`, `OPENAI_API_BASE`, default: `https://api.morphllm.com/v1`)
- `CODEBASE_EMBED_MODEL` (fallback: `MORPH_EMBED_MODEL`, `OPENAI_EMBED_MODEL`, `OPENAI_EMBEDDING_MODEL`, default: `morph-embedding-v3`)

Indexing:

- `CODEBASE_REPO_ID` (optional default `repo_id` if not provided per-call)
- `CODEBASE_DISABLE_EMBEDDINGS` (default: `false`) (if true, defaults `codebase_index_start`/`codebase_watch_start` to store chunks with dummy vectors)
- `CODEBASE_EMBED_BATCH_SIZE` (default: `64`)
- `CODEBASE_UPSERT_BATCH_SIZE` (default: `64`)
- `CODEBASE_INDEX_CONCURRENCY` (default: `4`) (controls full-index and watch worker concurrency)
- `CODEBASE_SCROLL_LIMIT` (default: `256`)
- `CODEBASE_MAX_FILE_BYTES` (default: `2097152`)
- `CODEBASE_GIT_METADATA` (default: `false`) (if true, attempts `git blame` to attach author/commit metadata)

HTTP:

- `HTTP_TIMEOUT` (seconds, default: `30`)
- `HTTP_RETRIES` (default: `0`)
- `TLS_SKIP_VERIFY` (default: `false`)

## Build

`make mcp-codebase-memory`

## Notes

- `codebase_index_start` defaults to indexing all supported languages if `languages` is omitted.
- Ignore behavior:
  - Built-in default globs ignore common paths (for example: `.git`, `node_modules`, `vendor`, `dist`, `build`).
  - Root and nested `.gitignore` files are honored for both full indexing and watch modes.
  - The matcher uses last-rule-wins semantics with `!` negation, and explicit `exclude` arguments are applied after `.gitignore` rules, so negation can re-include paths even if earlier rules would skip them.
- Use `embeddings=false` on `codebase_index_start` / `codebase_watch_start` to index without an embeddings API key (semantic search will not be useful).
- Set `full_refresh=false` for incremental indexing (skips unchanged files using the module chunk file hash).
- `codebase_search` supports `rerank=hybrid` and `lexical_weight` for lightweight hybrid reranking.
- `codebase_delete_repo` requires `confirm=true` (and supports `dry_run=true`).
- See `cmd/mcp-codebase-memory/ROADMAP.md` for planned phases.
