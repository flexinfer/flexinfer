# Product Spec: Weaver System Hardening & Expansion

**Date**: 2026-04-04
**Research**: `74-research-weaver-hardening-2026-04-04.md`
**Implementation Plan**: `76-implementation-plan-weaver-hardening-2026-04-04.md`

## Goal

Harden the weaver system for production reliability, improve observability for debugging, and clean up post-rename inconsistencies — all without changing the public tool interface.

## Non-Goals

- New domains or compound tools (deferred)
- Query result caching (deferred — needs usage patterns first)
- Rate limiting (deferred — MaxConcurrent is sufficient for now)
- Frontend panel redesign (current panel is adequate)

## Changes

### P0: Consistency & Naming Cleanup

**ENV-001: Rename ORCHESTRA_* env vars to WEAVER_***

| Old | New | Default |
|-----|-----|---------|
| `ORCHESTRA_MAX_ITERATIONS` | `WEAVER_MAX_ITERATIONS` | 8 |
| `ORCHESTRA_TOKEN_BUDGET` | `WEAVER_TOKEN_BUDGET` | 4096 |
| `ORCHESTRA_TIMEOUT` | `WEAVER_TIMEOUT` | 30s |
| `ORCHESTRA_MAX_CONCURRENT` | `WEAVER_MAX_CONCURRENT` | 4 |

Backward compatibility: `LoadConfigFromEnv()` reads new name first, falls back to old name, logs deprecation warning if old name is found.

**FUNC-001: Rename stale handleOrchestra* functions**

Internal daemon functions still named `handleOrchestraQuery`, `handleOrchestraGather`, `handleOrchestraToolQuery`, `handleOrchestraToolGather` → rename to `handleWeaverQuery`, `handleWeaverGather`, `handleWeaverToolQuery`, `handleWeaverToolGather`.

### P0: Resilience

**RETRY-001: Add retry with exponential backoff to responses client**

- Retry on: HTTP 5xx, connection reset, context deadline exceeded (if parent context still alive)
- Do NOT retry on: 4xx, JSON parse errors, circuit breaker open
- Max retries: 2 (3 total attempts)
- Backoff: 500ms, 1500ms
- Log each retry at WARN level with attempt number and error

**TIMEOUT-001: Add synthesis timeout**

- `synthesize()` gets `context.WithTimeout(ctx, cfg.Timeout)` matching subagent timeout
- If synthesis times out, fall back to concatenated domain results (existing fallback path)

**HTTP-001: Reuse HTTP client**

- Create `*http.Client` once in `FlexInferResponsesClient` constructor
- Make timeout configurable via `WEAVER_HTTP_TIMEOUT` (default: 60s)

### P1: Observability

**QID-001: Add query ID for log correlation**

- Generate UUID at start of `Query()`
- Propagate via `slog.Logger.With("query_id", id)` through classify, dispatch, subagent, synthesize
- Include in `QueryHistoryEntry` for HUD display
- Include in OTel span attributes

**TOOLS-001: Validate domain tools at startup**

- After router creation in `weaver_embed.go`, call `lister.ListTools()` and check each domain's tool list against available tools
- Log WARN for each missing tool: `"weaver: domain %s references missing tool %s"`
- Don't fail startup — just surface the gap

**METRICS-001: Add lifetime metrics to HUD**

- Add `loom/weaver/metrics` IPC handler that returns Prometheus counter values directly
- HUD `handleMetrics` calls this instead of deriving from history buffer
- Fallback: if Prometheus scrape fails, derive from history (current behavior)

### P2: Configuration

**BEHAVIORS-001: Load model behaviors from YAML**

- Path: `~/.config/loom/weaver-behaviors.yaml`
- Format:
  ```yaml
  behaviors:
    - prefix: "qwen3"
      user_message_prefix: "/no_think\n"
    - prefix: "deepseek"
      user_message_prefix: "<think>off</think>\n"
  ```
- Hard-coded defaults remain as fallback
- Missing file is not an error

### P2: Testing

**TEST-001: HUD domain handler tests**

- Test all 4 handlers: status, domains, history, metrics
- Mock bridge caller returning canned JSON
- Assert response shapes and graceful nil/error handling

## Success Criteria

1. `WEAVER_*` env vars work, `ORCHESTRA_*` still work with deprecation log
2. A transient FlexInfer 503 during a query retries and succeeds on second attempt
3. All logs from a single query share the same `query_id`
4. `make build && go test ./pkg/weaver/... ./internal/daemon/... ./internal/hud/...` all pass
5. HUD metrics endpoint returns lifetime totals, not just last-100

## Acceptance

- No public tool interface changes (weaver__query, weaver__gather, compound tools unchanged)
- All existing tests continue to pass
- No new dependencies added
