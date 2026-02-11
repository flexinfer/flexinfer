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
