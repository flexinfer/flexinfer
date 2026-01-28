# mcp-codebase-memory roadmap

This server provides “codebase memory” for Loom via MCP: index code, store vectors + lightweight structure in Qdrant, and expose query tools (search/definition/references/context/callers/callees) plus watch-mode incremental updates.

## Current state (implemented)

- Go MCP server: `services/loom-core/cmd/mcp-codebase-memory`
- Core package: `services/loom-core/pkg/codebase`
- Storage: Qdrant payloads + vectors (`services/loom-core/pkg/codebase/qdrant`)
- Embeddings: Pluggable `Embedder` interface (`services/loom-core/pkg/codebase/embed`)
  - `MorphClient` for Morph/OpenAI-compatible APIs (default)
  - `OllamaClient` for Ollama local embeddings (`CODEBASE_EMBED_PROVIDER=ollama`)
  - `DummyEmbedder` for no-embeddings mode
- Indexers:
  - Go: `go/ast` (`services/loom-core/pkg/codebase/index/goindex`)
  - TS/JS/Python/Rust:
    - `cgo` builds: tree-sitter via `github.com/smacker/go-tree-sitter`
    - `!cgo` builds: regex fallbacks (best-effort)
- **Phase C - Git metadata**: `annotateChunksWithGitMetadata()` in `pkg/codebase/gitmeta.go`
  - Per-chunk git blame info (commit SHA, author) stored in Qdrant payloads
- **Phase D - Graph tools**: `codebase_call_graph` and `codebase_module_graph` in `tools.go`
  - BFS traversal of stored `calls[]` edges with depth/limit controls
  - Module dependency edges from indexed imports
  - Mermaid output format support
- **Text search**: `codebase_text_search` tool for exact match fallback queries

## Guiding principles

- Prefer “works everywhere” builds (CI/static) while allowing better parsing when `cgo` is enabled.
- Keep Qdrant payloads forward/backward compatible (additive fields; tolerate missing fields).
- Indexing stays idempotent and incremental: delete-per-file then upsert; skip unchanged when `full_refresh=false`.
- Keep MCP tools stable; add new tools as additive capabilities.

## Phases (planned)

### Phase E — Embedding flexibility (complete)

Goal: support more environments than "Morph API only".

- ~~Pluggable embedder interface~~ (Done: `pkg/codebase/embed/embed.go`)
  - `Embedder` interface with `EmbedQuery`, `EmbedDocuments`, `Name`, `Model` methods
  - `MorphClient` implements `Embedder` for Morph/OpenAI-compatible APIs
  - `DummyEmbedder` for no-embedding mode
  - `NewServiceWithEmbedder()` for custom embedder injection
- ~~Optional "no-embeddings" indexing mode~~ (Done: `CODEBASE_DISABLE_EMBEDDINGS=true` uses `DummyEmbedder`)
- ~~Cache embeddings per `(content_hash, model)`~~ (Done: `GetFileEmbeddingCache` in qdrant client)
- ~~Optional local embeddings~~ (Done: `OllamaClient` for Ollama local embeddings)
  - Set `CODEBASE_EMBED_PROVIDER=ollama` to use Ollama
  - Defaults to `http://localhost:11434` and `nomic-embed-text` model
  - Also supports `CODEBASE_EMBED_PROVIDER=dummy` for explicit no-embeddings mode

### Phase F — Better chunking & retrieval (in progress)

Goal: reduce "too large chunk" and improve context relevance.

- ~~Chunk splitting for very large functions/types~~ (Done: `pkg/codebase/chunker`)
  - `SplitLargeChunks()` splits chunks exceeding MaxTokens into overlapping windows
  - Configurable MaxTokens (default 2000), OverlapTokens (default 200), MinTokens (default 50)
  - Preserves metadata (RepoID, FilePath, Language, GitCommit, etc.)
  - First window keeps docstring and imports; subsequent windows omit them
  - Extracts function calls from each window
- ~~Integration: Wire chunker into indexing pipeline~~ (Done: `service.go`)
  - Chunker runs after git metadata annotation, before embedding
  - Configurable via env: `CODEBASE_CHUNK_MAX_TOKENS`, `CODEBASE_CHUNK_OVERLAP_TOKENS`, `CODEBASE_CHUNK_MIN_TOKENS`
- [ ] Store extra lightweight signals (lexical tokens, identifiers) for improved hybrid reranking
- ~~Add a "raw text search" tool for exact match fallback.~~ (Done: `codebase_text_search`)

### Phase G — Tree-sitter without CGO (longer-term)

Goal: remove CGO build friction while keeping multi-language parsing.

- Evaluate non-CGO tree-sitter options or pure-Go parsers per language.
- If not feasible, standardize “CGO build for release images” + “fallback for CI/dev” policy.
