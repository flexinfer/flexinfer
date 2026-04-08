# Research: Weaver System Hardening & Expansion

**Date**: 2026-04-04
**Scope**: `pkg/weaver/`, daemon integration, HUD panel, standalone MCP server

## 1. Current State Assessment

The weaver system (renamed from "orchestra" on 2026-04-03) is a multi-domain query orchestrator that classifies user queries, dispatches them to domain-specific subagents running curated tool sets in parallel, and synthesizes combined results. It replaces 5-10 sequential MCP tool calls with a single `weaver__query` request.

### What's Working

| Component | Files | Status |
|-----------|-------|--------|
| Router (classify → dispatch → synthesize) | `pkg/weaver/router.go` | Production-ready |
| 6 built-in domains with system prompts | `pkg/weaver/domain.go`, `prompts.go` | Production-ready |
| 7 compound tools | `pkg/weaver/compound.go` | Production-ready |
| FlexInfer responses client | `pkg/weaver/responses_client.go` | Working, gaps below |
| YAML domain overrides | `pkg/weaver/domain_yaml.go` | Production-ready |
| Daemon integration | `internal/daemon/weaver_*.go` | Production-ready |
| HUD REST API (4 endpoints) | `internal/hud/domain/weaver/` | Working, gaps below |
| HUD frontend panel | `WeaverPanel.svelte` | Working |
| Prometheus metrics (6 collectors) | `pkg/weaver/metrics.go` | Production-ready |
| OTel tracing (5 span types) | `pkg/weaver/router.go` | Production-ready |
| Unit + integration tests | `*_test.go` (22 files) | Comprehensive |

Source: `pkg/weaver/router.go:1-485`, `pkg/weaver/config.go:1-124`, `pkg/weaver/domain.go:1-161`

### Test Coverage Summary

- Router: query disabled, status, registry, gather, classify+dispatch+synthesize, token budget
- Config: defaults, custom values, validation, RequireEnabled, model behaviors
- Domain: CRUD, ToolToDomains, DefaultDomains, system prompts, tool counts
- Domain YAML: valid/missing/invalid YAML, merge override
- Compound: 11+ tests covering tool definitions, dispatch, custom query, output functions
- Responses client: terminal response, tool calls, tool definitions, token metrics
- Executor: execute tool, error, invalid args, output extraction
- Adapter: tool filtering, no match, resolve call
- Telemetry: 11 tests
- Integration: 4 end-to-end tests (classify→dispatch→synthesize, compound, timeout, model behavior)

Source: `go test ./pkg/weaver/... -v -count=1` (all pass, 2.4s)

## 2. Issues Found

### 2.1 Env Var Naming Inconsistency (LOW effort, HIGH impact)

Four env vars still use the `ORCHESTRA_` prefix after the weaver rename:

```go
// config.go:18-21
EnvMaxIterations = "ORCHESTRA_MAX_ITERATIONS"
EnvTokenBudget   = "ORCHESTRA_TOKEN_BUDGET"
EnvTimeout       = "ORCHESTRA_TIMEOUT"
EnvMaxConcurrent = "ORCHESTRA_MAX_CONCURRENT"
```

These should become `WEAVER_*` for consistency with `WEAVER_ENABLED`, `WEAVER_ROUTER_MODEL`, `WEAVER_SUBAGENT_MODEL`.

### 2.2 No Retry Logic on FlexInfer Calls (MEDIUM effort, HIGH impact)

The responses client makes a single HTTP call per LLM request. If FlexInfer returns a transient error (5xx, timeout, connection reset), the entire subagent fails.

```go
// responses_client.go:157 — circuit breaker wraps the call but doesn't retry
err := c.client.Breaker().Execute(func() error { ... })
```

The circuit breaker prevents cascading failures but doesn't retry recoverable errors. A single 503 during a multi-domain query kills one domain's results.

### 2.3 No Query ID for Log Correlation (LOW effort, HIGH impact)

The router logs classification results and subagent outcomes, but there's no query ID to correlate them:

```go
// router.go:235
r.logger.Debug("classified query", "query", query, "domains", valid)
// router.go:389
r.logger.Warn("subagent failed", "domain", domain, "error", err, "latency_ms", latencyMs)
```

In concurrent usage, these logs interleave with no way to match them.

### 2.4 Hard-Coded HTTP Client Timeout (LOW effort, MEDIUM impact)

```go
// responses_client.go:169
httpClient := &http.Client{Timeout: 60 * time.Second}
```

This creates a new `http.Client` per request with a hard-coded 60s timeout. Should use a shared client and make the timeout configurable.

### 2.5 Synthesis Has No Timeout (LOW effort, MEDIUM impact)

`classify()` and each subagent have context timeouts, but `synthesize()` runs under the parent context with no explicit timeout:

```go
// router.go:299
answer := r.synthesize(ctx, results, req.Query)
```

If the router model is slow, synthesis can hang indefinitely after all subagents complete.

### 2.6 Tool Existence Not Validated at Startup (MEDIUM effort, MEDIUM impact)

Domains reference tools by name, but the adapter silently returns an empty list if tools don't exist in the daemon's tool cache:

```go
// adapter.go:48-49 — silent filter, no warning
```

A domain with 0 matching tools produces garbage results. Should warn at startup and/or log when tools are missing.

### 2.7 HUD Metrics Derived from Ring Buffer (LOW effort, MEDIUM impact)

`handleMetrics` in the HUD derives totals from the history ring buffer (max 100 entries):

```go
// handlers.go:86-130
total := len(result.Entries)
```

After 100+ queries, lifetime metrics are lost. Should expose Prometheus counters directly or maintain a persistent summary.

### 2.8 No Model Behavior Config File (LOW effort, LOW impact)

Model behaviors (currently just Qwen3's `/no_think` prefix) are hard-coded:

```go
// config.go:39-42
return map[string]ModelBehavior{
    "qwen3": {UserMessagePrefix: "/no_think\n"},
}
```

Adding a new model behavior requires a code change and rebuild.

### 2.9 Stale Orchestra References in Function Names (LOW effort, LOW impact)

Several function names in `weaver_tools.go` still say "Orchestra":

```go
// weaver_tools.go:118
func (d *Daemon) handleOrchestraToolQuery(...)
// weaver_tools.go:120
func (d *Daemon) handleOrchestraQuery(...)
```

These are internal but inconsistent with the rename.

### 2.10 Missing HUD Domain Tests (MEDIUM effort, MEDIUM impact)

The `internal/hud/domain/weaver/` handlers have no test coverage. Other HUD domains have handler tests.

## 3. Improvement Categories

### A. Consistency & Cleanup
- Rename `ORCHESTRA_*` env vars → `WEAVER_*`
- Rename stale `handleOrchestra*` function names
- Add backward-compatible aliases for env vars (read old name as fallback)

### B. Resilience
- Add retry with exponential backoff in responses client
- Add synthesis timeout
- Reuse HTTP client instead of creating per-request
- Validate domain tools exist at startup, log warnings

### C. Observability
- Add query ID (UUID) propagated through classify → dispatch → synthesize
- Expose Prometheus metrics directly to HUD (not derived from ring buffer)
- Add structured log fields for query ID, domain, iteration count

### D. Configuration
- Load model behaviors from `~/.config/loom/weaver-behaviors.yaml`
- Make HTTP client timeout configurable via `WEAVER_HTTP_TIMEOUT`
- Make history buffer size configurable

### E. Testing
- Add HUD domain handler tests
- Add circuit breaker failure tests
- Add concurrent subagent race condition tests

## 4. Risk Assessment

| Change | Risk | Mitigation |
|--------|------|------------|
| Env var rename | Breaks existing `ORCHESTRA_*` users | Read old name as fallback, deprecation warning |
| Retry logic | Could amplify load on FlexInfer | Limit to 2 retries, backoff, only on 5xx/timeout |
| Query ID | None (additive) | — |
| Synthesis timeout | Could truncate valid slow synthesis | Default to `cfg.Timeout` (same as subagent) |
| Tool validation | Startup delay if tool cache slow | Validate async after router creation |

## Sources

- `pkg/weaver/config.go:14-29` — env var constants and defaults
- `pkg/weaver/router.go:109-170` — Query() flow
- `pkg/weaver/router.go:239-307` — dispatch() and subagent dispatch
- `pkg/weaver/router.go:407-446` — synthesize()
- `pkg/weaver/responses_client.go:153-201` — doRequest() with circuit breaker
- `pkg/weaver/responses_client.go:169` — hard-coded HTTP timeout
- `internal/hud/domain/weaver/handlers.go:86-130` — metrics from history
- `internal/daemon/weaver_tools.go:118-134` — stale Orchestra function names
- `internal/daemon/weaver_embed.go:14-69` — startup flow
- `go test ./pkg/weaver/... -v -count=1` — all pass
