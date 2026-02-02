# MCP Server Error Handling Guide

This document describes the standardized error handling patterns for loom-core MCP servers.

## Overview

All MCP servers should use the `pkg/mcperror` package for consistent, user-friendly error messages.

## Import

```go
import "github.com/crb2nu/loom/pkg/mcperror"
```

## Error Types

### Parameter Validation

Use these for input validation errors:

```go
// Missing required parameter
if owner == "" {
    return nil, mcperror.RequiredParam("owner")
}

// Invalid parameter value
if count < 0 {
    return nil, mcperror.InvalidParam("count", "must be positive")
}
```

### API Errors

Use these for external service errors:

```go
// HTTP error response (automatically provides user-friendly messages)
if resp.StatusCode >= 400 {
    return nil, mcperror.APIError("GitHub", resp.StatusCode, bodyText)
}

// Connection/timeout errors
if err != nil {
    return nil, mcperror.WrapAPI("GitHub", err)
}
```

### Service Errors

Use these for service-level issues:

```go
// Service unavailable
if !connected {
    return nil, mcperror.ServiceUnavailable("Qdrant", "connection refused")
}

// Missing configuration
if token == "" {
    return nil, mcperror.NotConfigured("GITHUB_TOKEN", "set via environment variable")
}

// Operation failure
if err != nil {
    return nil, mcperror.OperationFailed("database query", err)
}
```

### Resource Errors

Use these for resource-related issues:

```go
// Resource not found
return nil, mcperror.NotFound("repository", repoName)

// Parse error
if err != nil {
    return nil, mcperror.ParseError("JSON response", err)
}
```

## Error Codes

The package defines standard error codes:

| Code | Constant | When to Use |
|------|----------|-------------|
| `INVALID_INPUT` | `CodeInvalidInput` | Parameter validation failures |
| `NOT_FOUND` | `CodeNotFound` | Resource not found |
| `UNAUTHORIZED` | `CodeUnauthorized` | Authentication failures |
| `FORBIDDEN` | `CodeForbidden` | Permission denied |
| `TIMEOUT` | `CodeTimeout` | Operation timeouts |
| `SERVER_ERROR` | `CodeServerError` | Internal/external server errors |
| `CONNECTION_ERROR` | `CodeConnectionError` | Network/connection issues |
| `RATE_LIMITED` | `CodeRateLimited` | Rate limit exceeded |
| `VALIDATION_ERROR` | `CodeValidation` | Multiple field validation errors |

## Best Practices

### 1. Use Specific Error Types

Instead of:
```go
return nil, fmt.Errorf("owner and repo are required")
```

Use:
```go
if owner == "" {
    return nil, mcperror.RequiredParam("owner")
}
if repo == "" {
    return nil, mcperror.RequiredParam("repo")
}
```

### 2. Provide Context

The error helpers automatically provide context:
- `APIError` includes service name and HTTP status with user-friendly explanations
- `NotFound` includes the resource type and name
- `RequiredParam` includes the parameter name

### 3. Handle API Errors with Status Codes

`APIError` provides automatic user-friendly messages:

| Status Code | Message |
|-------------|---------|
| 401 | "authentication failed - check your API token" |
| 403 | "access forbidden - check permissions" |
| 404 | "resource not found" |
| 429 | "rate limit exceeded - try again later" |
| 5xx | "service unavailable - try again later" |

### 4. Never Panic

Tool handlers should never panic. Always return errors:

```go
// Bad
panic("unexpected nil value")

// Good
if value == nil {
    return nil, mcperror.ServerError("unexpected nil value")
}
```

### 5. Wrap External Errors

When calling external services, wrap errors with context:

```go
result, err := externalAPI.Call()
if err != nil {
    return nil, mcperror.WrapAPI("ExternalService", err)
}
```

## Example: Complete Handler

```go
func (s *server) handleGetUser(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
    // Validate required parameters
    username := getStringArg(args, "username", "")
    if username == "" {
        return nil, mcperror.RequiredParam("username")
    }

    // Validate parameter format
    if len(username) > 39 {
        return nil, mcperror.InvalidParam("username", "must be 39 characters or less")
    }

    // Make API call
    user, err := s.client.GetUser(ctx, username)
    if err != nil {
        // Check for specific error types
        if isNotFound(err) {
            return nil, mcperror.NotFound("user", username)
        }
        return nil, mcperror.WrapAPI("GitHub", err)
    }

    return mcp.JSONResult(user)
}
```

## Migration Checklist

When updating an MCP server to use standardized error handling:

- [ ] Add import for `github.com/crb2nu/loom/pkg/mcperror`
- [ ] Replace `fmt.Errorf` for required params with `mcperror.RequiredParam`
- [ ] Replace `fmt.Errorf` for validation with `mcperror.InvalidParam`
- [ ] Replace custom apiError types with `mcperror.APIError`
- [ ] Wrap external errors with `mcperror.WrapAPI`
- [ ] Replace panic with error returns
- [ ] Test that error messages are user-friendly

## Servers Updated

- [x] mcp-github (example implementation)
- [x] mcp-gitlab
- [x] mcp-k8s
- [ ] ... (remaining servers)
