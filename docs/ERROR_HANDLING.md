# MCP Server Error Handling Guide

This guide defines current error-handling standards for Loom Core MCP servers.

## Core Rules

1. Validate all input early.
2. Return structured errors via `pkg/mcperror`.
3. Return `mcp.ErrorResult(err), nil` from handlers for user-facing failures.
4. Wrap external/API failures with service context.
5. Do not panic in tool handlers.

## Recommended Imports

```go
import (
    "gitlab.flexinfer.ai/libs/mcp-go"

    "github.com/crb2nu/loom/pkg/mcperror"
    "github.com/crb2nu/loom/pkg/validate"
)
```

## Handler Pattern

```go
func (s *server) handleThing(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
    v := validate.NewArgs(args)
    project := v.Required("project")
    page := v.Int("page", 1)
    if err := v.Validate(); err != nil {
        return mcp.ErrorResult(err), nil
    }

    if page < 1 {
        return mcp.ErrorResult(mcperror.InvalidParam("page", "must be >= 1")), nil
    }

    out, err := s.client.Fetch(ctx, project)
    if err != nil {
        return mcp.ErrorResult(mcperror.WrapAPI("MyService", err)), nil
    }

    return mcp.JSONResult(out)
}
```

## Error Helpers

Use `pkg/mcperror` helpers consistently:

- Input: `RequiredParam`, `InvalidParam`, `Validation`, `ParseError`
- API/service: `APIError`, `WrapAPI`, `ServiceUnavailable`, `RateLimited`
- Resources: `NotFound`
- Configuration: `NotConfigured`
- Generic failures: `OperationFailed`, `ServerError`

## HTTP/API Mapping

When translating upstream HTTP errors, use `mcperror.APIError(service, statusCode, body)`.

Expected mapping:

- `401` -> `UNAUTHORIZED`
- `403` -> `FORBIDDEN`
- `404` -> `NOT_FOUND`
- `429` -> `RATE_LIMITED`
- `5xx` -> `SERVER_ERROR`

## Logging Expectations

- Log warnings/errors with operation context and identifiers.
- Do not silently discard errors (`_ = err`) unless truly ignorable and documented.
- Avoid logging secrets/token values.

## Checklist for New or Updated MCP Servers

- [ ] Uses `validate.NewArgs` for parsing and validation
- [ ] Returns structured errors via `mcp.ErrorResult`
- [ ] Wraps external failures with `mcperror.WrapAPI`/`APIError`
- [ ] No panics in handler paths
- [ ] Has tests for at least one error path per tool family
- [ ] Updates docs/CHANGELOG for behavior-visible changes

## Migration Tracker

Legacy servers use `return nil, err` or `return nil, fmt.Errorf(...)` in handler
functions. These should be migrated to `return mcp.ErrorResult(err), nil`.

**CI guardrail:** `scripts/ci/check_error_handling.sh` prevents the count from
increasing above the baseline (313). Lower the baseline as servers are migrated.

| Server | `nil,err` Count | Handler-level? | Status |
|--------|-----------------|----------------|--------|
| mcp-terraform | 24 | Yes (16 handler, 8 helper) | Pending |
| mcp-linear | 18 | Yes (12 handler, 6 helper) | Pending |
| mcp-pagerduty | 17 | Yes (13 handler, 4 helper) | Pending |
| mcp-vault | 17 | Yes (9 handler, 8 helper) | Pending |
| mcp-k8s | 16 | Yes (16 handler) | Pending |
| mcp-notion | 16 | Yes (11 handler, 5 helper) | Pending |
| mcp-git | 13 | Yes (13 handler) | Pending |
| mcp-sentry | 12 | Yes (8 handler, 4 helper) | Pending |
| mcp-aws | 12 | Mixed | Pending |
| mcp-mongodb | 12 | Mixed | Pending |
| mcp-elasticsearch | 11 | Mixed | Pending |
| mcp-prometheus | 10 | Yes (8 handler, 2 helper) | Pending |
| mcp-tavily | 10 | Yes (8 handler, 2 helper) | Pending |
| mcp-gcp | 10 | Mixed | Pending |
| mcp-grafana | 8 | Helper only | Done |
| mcp-morph-embeddings | 8 | Mixed | Pending |
| mcp-substack | 7 | Mixed | Pending |
| mcp-godot | 7 | Mixed | Pending |
| mcp-cloudflare | 6 | Helper only | Done |
| mcp-qdrant | 6 | Mixed | Pending |
| mcp-zep | 6 | Mixed | Pending |
| mcp-confluence | 6 | Mixed | Pending |
| mcp-loki | 6 | Mixed | Pending |
| mcp-browserkit | 6 | Mixed | Pending |
| mcp-sequentialthinking | 6 | Mixed | Pending |
| mcp-alertmanager | 5 | Mixed | Pending |
| mcp-gitlab | 5 | Yes (4 handler, 1 helper) | Pending |
| mcp-github-actions | 4 | Mixed | Pending |
| mcp-flux | 4 | Helper only | Done |
| mcp-argocd | 4 | Mixed | Pending |
| mcp-github | 3 | Mixed | Pending |
| mcp-youtube | 3 | Mixed | Pending |
| mcp-helm | 2 | Helper only | Done |
| mcp-jira | 2 | Mixed | Pending |
| mcp-minio | 2 | Mixed | Pending |
| mcp-docker | 1 | Helper only | Done |
| mcp-neo4j | 1 | Mixed | Pending |
| mcp-crypto | 1 | Mixed | Pending |
| mcp-redis | 0 | N/A | Done |
| mcp-slack | 6 | Mixed | Pending |

**"Helper only"** means all `return nil, err` instances are in utility functions
(HTTP wrappers, CLI runners) called by handlers that already convert via
`mcp.ErrorResult(err), nil`. These are correct and need no migration.

**Priority:** Migrate "Yes (N handler)" servers first -- those return raw Go
errors directly from tool handlers, violating the MCP protocol.
