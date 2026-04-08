# Implementation Plan: Weaver System Hardening & Expansion

**Date**: 2026-04-04
**Research**: `74-research-weaver-hardening-2026-04-04.md`
**Product Spec**: `75-product-spec-weaver-hardening-2026-04-04.md`

## Execution Order

```
Slice 1 (env vars + naming)          ← prerequisite for clean diffs
    ↓
Slice 2 (resilience: retry + timeout + HTTP client)
    ↓
Slice 3 (observability: query ID + tool validation + metrics)
    ↓
Slice 4 (config: model behaviors YAML)
    ↓
Slice 5 (testing: HUD handler tests)
```

Slices 4 and 5 are independent and can run in parallel after Slice 3.

---

## Slice 1: Env Var Rename + Function Naming Cleanup

**Spec refs**: ENV-001, FUNC-001

### Changes

**`pkg/weaver/config.go`**
- Rename constants:
  ```go
  EnvMaxIterations = "WEAVER_MAX_ITERATIONS"
  EnvTokenBudget   = "WEAVER_TOKEN_BUDGET"
  EnvTimeout       = "WEAVER_TIMEOUT"
  EnvMaxConcurrent = "WEAVER_MAX_CONCURRENT"
  ```
- Add deprecated aliases:
  ```go
  EnvMaxIterationsDeprecated = "ORCHESTRA_MAX_ITERATIONS"
  EnvTokenBudgetDeprecated   = "ORCHESTRA_TOKEN_BUDGET"
  EnvTimeoutDeprecated       = "ORCHESTRA_TIMEOUT"
  EnvMaxConcurrentDeprecated = "ORCHESTRA_MAX_CONCURRENT"
  ```
- Update `LoadConfigFromEnv()` to try new name first, fall back to old, log deprecation:
  ```go
  func envWithFallback(newKey, oldKey string, defaultVal int) int {
      if v := env.Int(newKey, 0); v != 0 { return v }
      if v := env.Int(oldKey, 0); v != 0 {
          slog.Warn("deprecated env var", "old", oldKey, "new", newKey)
          return v
      }
      return defaultVal
  }
  ```

**`internal/daemon/weaver_tools.go`**
- Rename `handleOrchestraToolQuery` → `handleWeaverToolQuery`
- Rename `handleOrchestraToolGather` → `handleWeaverToolGather`

**`internal/daemon/daemon_dispatch_weaver.go`**
- Rename `handleOrchestraQuery` → `handleWeaverQuery`
- Rename `handleOrchestraGather` → `handleWeaverGather`
- Rename `handleOrchestraStatus` → `handleWeaverStatus`
- Rename `handleOrchestraHistory` → `handleWeaverHistory`
- Update all callers of these functions

**Tests**:
- Update `config_test.go`: test new env var names, test fallback reads old names, test deprecation warning

**Verification**:
```bash
go test ./pkg/weaver/... -v -count=1
go test ./internal/daemon/... -count=1
go build ./cmd/loom ./cmd/mcp-weaver
```

---

## Slice 2: Resilience (Retry + Synthesis Timeout + Shared HTTP Client)

**Spec refs**: RETRY-001, TIMEOUT-001, HTTP-001

### Changes

**`pkg/weaver/config.go`**
- Add constants:
  ```go
  EnvHTTPTimeout = "WEAVER_HTTP_TIMEOUT"
  DefaultHTTPTimeout = 60 * time.Second
  ```
- Add `HTTPTimeout time.Duration` field to `Config`
- Load in `LoadConfigFromEnv()`

**`pkg/weaver/responses_client.go`**
- Add `httpClient *http.Client` field to `FlexInferResponsesClient`
- Create once in `NewFlexInferResponsesClient()` with configurable timeout
- Update constructor signature: `NewFlexInferResponsesClient(client, behaviors, httpTimeout, logger)`
- In `doRequest()`:
  - Replace per-request `&http.Client{Timeout: 60 * time.Second}` with `c.httpClient`
  - Add retry loop around breaker.Execute:
    ```go
    const maxRetries = 2
    backoffs := [2]time.Duration{500*time.Millisecond, 1500*time.Millisecond}

    for attempt := 0; attempt <= maxRetries; attempt++ {
        err = c.client.Breaker().Execute(func() error { ... })
        if err == nil { break }
        if !isRetryable(err) { break }
        if attempt < maxRetries {
            c.logger.Warn("retrying FlexInfer request", "attempt", attempt+1, "error", err)
            select {
            case <-ctx.Done(): return nil, ctx.Err()
            case <-time.After(backoffs[attempt]):
            }
        }
    }
    ```
  - Add `isRetryable(err)` helper: true for 5xx status codes, connection reset, timeout (but NOT context canceled from parent)

**`pkg/weaver/router.go`**
- In `synthesize()`, wrap with timeout:
  ```go
  synthCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
  defer cancel()
  synth, err := r.client.CompleteSimple(synthCtx, ...)
  ```
- Update `runSubAgent()` to pass `cfg.HTTPTimeout` to `NewFlexInferResponsesClient()`

**`cmd/mcp-weaver/main.go`**
- Update `NewFlexInferResponsesClient` call site with new parameter

**Tests**:
- `responses_client_test.go`: test retry on 503, test no retry on 400, test max retries exhausted
- `router_test.go` or `integration_test.go`: test synthesis timeout produces fallback concatenation

**Verification**:
```bash
go test ./pkg/weaver/... -v -count=1
go build ./cmd/loom ./cmd/mcp-weaver
```

---

## Slice 3: Observability (Query ID + Tool Validation + Lifetime Metrics)

**Spec refs**: QID-001, TOOLS-001, METRICS-001

### Changes

**`pkg/weaver/router.go`**
- Add `"github.com/google/uuid"` import (already a transitive dep)
- At top of `Query()`:
  ```go
  queryID := uuid.New().String()[:8]  // short ID for readability
  qlog := r.logger.With("query_id", queryID)
  ```
- Pass `qlog` through `classify()`, `dispatch()`, `runSubAgent()`, `synthesize()` (add `logger *slog.Logger` parameter or use context)
- Add `queryID` to OTel span attributes
- Add `QueryID string` field to `QueryHistoryEntry`

**`pkg/weaver/domain.go` or `pkg/weaver/router.go`**
- Add `ValidateTools(lister ToolLister) []string` method on `DomainRegistry`:
  ```go
  func (r *DomainRegistry) ValidateTools(lister ToolLister) []string {
      available, _ := lister.ListTools()
      avSet := make(map[string]bool)
      for _, t := range available { avSet[t.Name] = true }
      var warnings []string
      for _, d := range r.List() {
          for _, tool := range d.Tools {
              if !avSet[tool] {
                  warnings = append(warnings, fmt.Sprintf("domain %q references missing tool %q", d.Name, tool))
              }
          }
      }
      return warnings
  }
  ```

**`internal/daemon/weaver_embed.go`**
- After `d.weaver = router`, call:
  ```go
  if warnings := router.Registry().ValidateTools(lister); len(warnings) > 0 {
      for _, w := range warnings {
          d.logger.Warn("weaver: " + w)
      }
  }
  ```

**`pkg/weaver/metrics.go`**
- Add `Summary() map[string]any` method on `Metrics`:
  ```go
  func (m *Metrics) Summary() map[string]any {
      // Read current counter values via prometheus.ToFloat64-style extraction
      // or maintain internal atomic counters alongside Prometheus collectors
  }
  ```
- Simplest approach: maintain parallel `atomic.Int64` counters for totalQueries, totalErrors, totalTokens, totalLatencyMs that are incremented alongside Prometheus updates

**`internal/daemon/daemon_dispatch_weaver.go`**
- Add `loom/weaver/metrics` IPC handler returning `d.weaver.MetricsSummary()`

**`internal/hud/domain/weaver/handlers.go`**
- Update `handleMetrics` to call `loom/weaver/metrics` first, fall back to history-derived metrics

**Tests**:
- Test query ID appears in history entries
- Test `ValidateTools` with missing tools
- Test metrics summary returns lifetime values

**Verification**:
```bash
go test ./pkg/weaver/... -v -count=1
go test ./internal/daemon/... -count=1
go test ./internal/hud/... -count=1
```

---

## Slice 4: Model Behaviors from YAML

**Spec ref**: BEHAVIORS-001

### Changes

**`pkg/weaver/config.go`**
- Add `LoadBehaviorsFromFile(path string) (map[string]ModelBehavior, error)`:
  ```go
  type behaviorsFile struct {
      Behaviors []struct {
          Prefix            string `yaml:"prefix"`
          UserMessagePrefix string `yaml:"user_message_prefix"`
      } `yaml:"behaviors"`
  }
  ```
- Add `DefaultBehaviorsPath() string` → `~/.config/loom/weaver-behaviors.yaml`
- In `LoadConfigFromEnv()`, after building default behaviors, merge file-based ones on top

**`internal/daemon/weaver_embed.go`**
- After loading YAML domains, load behaviors:
  ```go
  if bs, err := weaver.LoadBehaviorsFromFile(weaver.DefaultBehaviorsPath()); err != nil {
      d.logger.Warn("weaver: failed to load behaviors YAML", "error", err)
  } else if bs != nil {
      for k, v := range bs { cfg.ModelBehaviors[k] = v }
  }
  ```

**Tests**:
- Test valid behaviors YAML loads
- Test missing file returns nil
- Test invalid YAML returns error
- Test file behaviors override defaults

**Verification**:
```bash
go test ./pkg/weaver/... -v -count=1
```

---

## Slice 5: HUD Handler Tests

**Spec ref**: TEST-001

### Changes

**`internal/hud/domain/weaver/handlers_test.go`** (new)
- Mock `Deps` interface with:
  - `WriteJSON` that captures response
  - `WriteError` that captures error
  - `WeaverBridge()` returning a mock bridge caller
- Test cases:
  - `TestHandleStatus_WeaverEnabled` — bridge returns status JSON, handler writes it
  - `TestHandleStatus_NoBridge` — bridge is nil, handler returns `{"enabled": false}`
  - `TestHandleStatus_BridgeError` — bridge returns error, handler returns `{"enabled": false}`
  - `TestHandleDomains_Success` — extracts domains/models from status
  - `TestHandleHistory_Success` — passes through history entries
  - `TestHandleHistory_NoBridge` — returns empty entries
  - `TestHandleMetrics_Success` — computes avg latency, error rate from history
  - `TestHandleMetrics_Empty` — returns zero metrics

**Verification**:
```bash
go test ./internal/hud/domain/weaver/... -v -count=1
```

---

## Execution Summary

| Slice | Files Changed | New Files | Est. Tests |
|-------|--------------|-----------|------------|
| 1: Env vars + naming | 4 | 0 | 3-5 |
| 2: Resilience | 4 | 0 | 4-6 |
| 3: Observability | 6 | 0 | 4-6 |
| 4: YAML behaviors | 2 | 0 | 4 |
| 5: HUD tests | 0 | 1 | 8 |

## Key Files

| File | Slices |
|------|--------|
| `pkg/weaver/config.go` | 1, 2, 4 |
| `pkg/weaver/router.go` | 2, 3 |
| `pkg/weaver/responses_client.go` | 2 |
| `pkg/weaver/metrics.go` | 3 |
| `pkg/weaver/domain.go` | 3 |
| `internal/daemon/weaver_embed.go` | 3, 4 |
| `internal/daemon/weaver_tools.go` | 1 |
| `internal/daemon/daemon_dispatch_weaver.go` | 1, 3 |
| `internal/hud/domain/weaver/handlers.go` | 3 |
| `internal/hud/domain/weaver/handlers_test.go` | 5 (new) |
| `cmd/mcp-weaver/main.go` | 2 |

## Verification (after all slices)

```bash
go test ./pkg/weaver/... -v -count=1
go test ./internal/daemon/... -count=1
go test ./internal/hud/... -count=1
go build ./cmd/loom ./cmd/loomd ./cmd/mcp-weaver
pnpm --dir internal/hud/frontend build
```
