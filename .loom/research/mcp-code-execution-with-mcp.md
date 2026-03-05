# MCP Code-Execution Research (Anthropic Nov 4, 2025)

## Source
- Anthropic engineering post: https://www.anthropic.com/engineering/code-execution-with-mcp

## What this introduces
The post describes a **client/runtime pattern**, not a new MCP wire-level primitive:

1. Expose MCP tools as code APIs (filesystem or searchable index) instead of preloading every tool definition into model context.
2. Run agent-written code in a sandbox so intermediate data stays out of context unless explicitly returned/logged.
3. Add progressive disclosure (`search_tools`, optional detail levels) so agents fetch only needed tool definitions.
4. Support persistent local state/skills in the execution environment.

## Why this matters for loom-core
loom-core already has building blocks:
- Aggregated tool cache via daemon RPC (`loom/tools`) and proxy reuse.
- Streamable HTTP support.
- Registry + generator pipeline for cross-client config.

But the current implementation is still mostly direct-call oriented and misses key pieces for the code-execution pattern.

## Current gaps (code-level)

### 1) Protocol/version consistency
- `mcp-go` still defaults `ProtocolVersion` to `2024-11-05` and only partially carries newer version constants.
  - `libs/mcp-go/types.go`
- loom proxy/daemon initialize paths mix versions:
  - remote path uses `20250618`, local path still uses legacy.
  - `cmd/loom/proxy.go`
  - `internal/daemon/daemon_dispatch.go`
  - `internal/daemon/daemon_toolcache.go`
- Integration tests still assert `2024-11-05`.
  - `internal/integration/mcp_test.go`

### 2) Tool schema/result fidelity for API generation
- `mcp.Tool` currently has `name`, `description`, `inputSchema` only (no `outputSchema`, no annotations).
- `CallToolResult` has `content` + `isError` only (no structured result payload).
  - `libs/mcp-go/types.go`
- This blocks robust typed wrapper generation for tools-as-code.

### 3) Streamable HTTP/server compliance gaps
- GET on streamable endpoint returns 405 (server-initiated channel deferred).
  - `libs/mcp-go/streamable_http.go`
- Header behavior for protocol negotiation is not explicit enough for modern clients.
  - `libs/mcp-go/streamable_http.go`
- Auth middleware returns 401 without OAuth-style challenge header (`WWW-Authenticate`) for token flows.
  - `internal/daemon/auth.go`

### 4) Discovery API is too coarse for progressive disclosure
- We have `loom/tools`, but not a queryable API (`search_tools`, detail levels, paging).
  - `internal/daemon/daemon_dispatch.go`
  - `internal/daemon/daemon_toolcache.go`

### 5) Config generation does not exploit modern MCP client fields
- TOML generation emits command-based entries only.
  - `pkg/generator/configs.go`
- Registry schema has `Server.URL`, but generator doesn’t map URL-based server configs into target output.
  - `libs/fi-mcp-kit/pkg/registry/registry.go`
- Codex vendored schema supports richer fields (`url`, `bearer_token(_env_var)`, `http_headers`, `env_http_headers`, `enabled_tools`, `disabled_tools`, `scopes`, etc.)
  - `libs/fi-mcp-kit/pkg/validator/schemas/codex_config.json`

## Recommended enablement plan

### Phase 0: protocol baseline (required first)
1. Standardize initialize protocol negotiation to `20250618` with fallback support.
2. Update all daemon/proxy initialize call sites and tests.
3. Add explicit protocol header handling in streamable HTTP request/response paths.
4. Add `WWW-Authenticate: Bearer ...` on 401 for token/OAuth paths.

### Phase 1: tools-as-code discovery surface
1. Add daemon RPC methods:
   - `loom/tools/search` with `query`, `servers`, `limit`, `cursor`, `detail`.
   - `loom/tools/get` with exact tool id and full schema payload.
2. Implement `detail` levels:
   - `name`
   - `summary` (name + description)
   - `schema` (full input/output/annotations where available)
3. Back these methods from existing tool cache + manifest.

### Phase 2: code API projection
1. Add `loom codeapi generate` command that emits wrappers from cached tool metadata:
   - output root: `.loom/codeapi/servers/<server>/<tool>.ts`
2. Generate stable typed wrappers around daemon call path (`loom/call`).
3. Emit `index.ts` per server + top-level exports for simple import ergonomics.

### Phase 3: execution harness
1. Add `loom code run` (or daemon endpoint) to run generated/agent code in sandbox.
2. Enforce time/memory/network limits.
3. Integrate optional redaction/tokenization middleware in the call pipeline so sensitive values can remain out of model context by default.

### Phase 4: config/registry upgrades
1. Extend generator mapping so targets that support URL-native MCP entries can emit them from `Server.URL`.
2. For Codex, map supported optional fields where present:
   - `enabled_tools`, `disabled_tools`, `scopes`, `http_headers`, auth fields.
3. Keep legacy command-mode generation as fallback.

## Minimal first slice (recommended)
Deliver this increment first:
1. Phase 0 protocol cleanup + tests.
2. `loom/tools/search` with `detail=name|summary|schema`.
3. `loom codeapi generate` (TypeScript wrappers only).

This gets 80% of the Anthropic pattern benefits (progressive disclosure + code APIs) without committing immediately to a full in-daemon code execution runtime.
