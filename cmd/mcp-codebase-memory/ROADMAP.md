# mcp-codebase-memory roadmap

This server provides “codebase memory” for Loom via MCP: index code, store vectors + lightweight structure in Qdrant, and expose query tools (search/definition/references/context/callers/callees) plus watch-mode incremental updates.

## Current state (implemented)

- Go MCP server: `services/loom-core/cmd/mcp-codebase-memory`
- Core package: `services/loom-core/pkg/codebase`
- Storage: Qdrant payloads + vectors (`services/loom-core/pkg/codebase/qdrant`)
- Embeddings: Morph embeddings API (`services/loom-core/pkg/codebase/embed`)
- Indexers:
  - Go: `go/ast` (`services/loom-core/pkg/codebase/index/goindex`)
  - TS/JS/Python/Rust:
    - `cgo` builds: tree-sitter via `github.com/smacker/go-tree-sitter`
    - `!cgo` builds: regex fallbacks (best-effort)

## Guiding principles

- Prefer “works everywhere” builds (CI/static) while allowing better parsing when `cgo` is enabled.
- Keep Qdrant payloads forward/backward compatible (additive fields; tolerate missing fields).
- Indexing stays idempotent and incremental: delete-per-file then upsert; skip unchanged when `full_refresh=false`.
- Keep MCP tools stable; add new tools as additive capabilities.

## Phases (planned)

### Phase C — Git metadata (next)

Goal: attach lightweight git context to chunks to improve attribution and result quality.

- Add optional per-chunk fields (e.g., last commit short SHA + author) populated from `git blame`.
- Make it opt-in (flag/env/tool arg) to avoid slowing indexing for large repos.
- Store in Qdrant payload so results remain useful even after process restarts.

### Phase D — Graph tools

Goal: make structure queries easier than repeated callers/callees calls.

- `codebase_call_graph`: return edges from stored `calls[]` for a symbol (BFS with depth/limits).
- `codebase_module_graph`: return import/dependency edges between modules/files (best-effort per language).
- Optional render helpers (Mermaid/DOT) as string output.

### Phase E — Embedding flexibility

Goal: support more environments than “Morph API only”.

- Pluggable embedder interface:
  - Morph embeddings (current)
  - Optional local embeddings (e.g., sentence-transformers via a sidecar service, or another MCP server)
- Optional “no-embeddings” indexing mode (dummy vectors) so non-embedding tools (definition/context/text search/graphs) work without an embeddings API key.
- Cache embeddings per `(content_hash, model)` to avoid recompute on re-index.

### Phase F — Better chunking & retrieval

Goal: reduce “too large chunk” and improve context relevance.

- Chunk splitting for very large functions/types (windowing with overlap).
- Store extra lightweight signals (lexical tokens, identifiers) for improved hybrid reranking.
- Add a “raw text search” tool for exact match fallback.

### Phase G — Tree-sitter without CGO (longer-term)

Goal: remove CGO build friction while keeping multi-language parsing.

- Evaluate non-CGO tree-sitter options or pure-Go parsers per language.
- If not feasible, standardize “CGO build for release images” + “fallback for CI/dev” policy.
