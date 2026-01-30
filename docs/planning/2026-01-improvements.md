# loom-core: Comprehensive Improvement Plan

> **Status:** Completed
> **Implemented:** 2026-01-28
> **Updated:** 2026-01-29 (additional test coverage, new server deployments)

## Summary

This plan addressed critical security, error handling, and resource management issues across loom-core MCP servers and packages.

## Master Tracker

| Phase | Status | Priority | Description |
|-------|--------|----------|-------------|
| 1 | Completed | Critical | Security hardening (path traversal, file safety) |
| 2 | Completed | High | Error handling correctness fixes |
| 3 | Completed | Medium | Resource leak fixes (client pooling) |
| 4 | Completed | High | Test coverage for core packages |

---

## Phase 1: Security Hardening

### 1.1 Created pkg/pathsec Utility

**File:** `pkg/pathsec/pathsec.go`

New security utility package providing:
- `ValidatePath(path, allowedRoot)` - Validates path is within boundary after symlink resolution
- `ValidateFileSize(path, maxBytes)` - Checks file size limits
- `CleanPath(path)` - Cleans and makes path absolute
- `IsSubpath(parent, child)` - Checks path containment
- `ContainsTraversal(path)` - Quick check for traversal patterns
- `SafeJoin(base, elem)` - Safe path joining with validation

**Tests:** `pkg/pathsec/pathsec_test.go` - Comprehensive tests including symlink attack scenarios

### 1.2 Fixed mcp-morph-fast-apply

**File:** `cmd/mcp-morph-fast-apply/main.go`

Changes:
- Added path validation using `pathsec.ValidatePath()`
- Added file size limit (10MB max input)
- Added response size limit (20MB max response)
- Fixed ignored `json.Marshal` error
- Fixed ignored `http.NewRequestWithContext` error
- Added shared HTTP client for connection reuse

### 1.3 Fixed mcp-filesystem

**File:** `cmd/mcp-filesystem/main.go`

Changes:
- Added `allowedRoot` boundary enforcement via `FILESYSTEM_ROOT` env var
- Added path validation for all handlers (`handleListDirectory`, `handleReadFile`, `handleSearchFiles`)
- Added file size limit (50MB max read)
- Added search result limit (10000 max matches)
- Added context cancellation check in search

---

## Phase 2: Error Handling Fixes

### 2.1 Fixed mcp-crypto

**File:** `cmd/mcp-crypto/main.go`

Changes:
- Fixed ignored `rand.Int` error in `handleRandomString`
- Added length validation (max 10000) to prevent DoS

### 2.2 Fixed mcp-zep

**File:** `cmd/mcp-zep/main.go`

Changes:
- Fixed ignored `http.NewRequestWithContext` errors in all handlers
- Fixed ignored `json.Marshal` error in `handleAddMessages`
- Added shared HTTP client for connection reuse

### 2.3 Fixed pkg/agentcontext

**File:** `pkg/agentcontext/service.go`

Changes:
- Replaced `fmt.Printf` warnings with result metadata (`_warning`, `_persist_error`)
- Added proper error surfacing for persist operations

---

## Phase 3: Resource Leak Fixes

### 3.1 Fixed mcp-youtube

**File:** `cmd/mcp-youtube/main.go`

Changes:
- Added singleton YouTube client using `sync.Once`
- All handlers now share a single client instance

### 3.2 Fixed mcp-morph-fast-apply HTTP Client

Added package-level `httpClient` variable with 90s timeout, used by all requests.

### 3.3 Fixed mcp-zep HTTP Client

Added package-level `httpClient` variable with 30s timeout, used by all requests.

---

## Phase 4: Test Coverage

### 4.1 Added pkg/agentcontext Tests

**File:** `pkg/agentcontext/service_test.go`

Tests for:
- `GenerateID` determinism and uniqueness
- `ContentHashFunc` determinism and uniqueness
- `EstimateTokens` accuracy
- `uniqueStrings` deduplication and sorting
- `priorityRank` ordering
- `getBool`, `toFloat` helper functions
- Payload conversion functions (`taskToPayload`, `payloadToTask`, etc.)

### 4.2 Added pkg/secrets Tests

**File:** `pkg/secrets/store_test.go`

Tests for:
- `Manager.Get` priority order and fallback behavior
- `Manager.Set` writing to primary backend
- `Manager.Delete` from primary backend
- `Manager.List` deduplication across backends
- `EnvBackend` read-only behavior
- Error types (`ErrNotFound`, `ErrReadOnly`)
- `FileBackend` persistence, multiple keys, basic operations
- `Manager` with empty backends and all-read-only backends

**Coverage improved:** 18.3% → 39.2%

### 4.3 Extended pkg/agentcontext Tests

Additional tests added:
- Session/Entry payload conversions
- Filter helper functions (`toString`, `toInt`, `toStringSlice`)
- Constants and type definitions
- Edge cases for nil/minimal payloads

**Coverage improved:** 6.1% → 8.8%

---

## Verification

```bash
# Build all
go build ./...

# Run all tests
go test ./...

# Run specific package tests
go test -v ./pkg/pathsec/...
go test -v ./pkg/agentcontext/...
go test -v ./pkg/secrets/...
```

---

## Commits

The changes were committed in logical phases:

1. `fix(security): add pathsec utility and harden filesystem/morph servers`
2. `fix: handle ignored errors in crypto, zep, and agentcontext`
3. `fix: add client pooling to youtube, zep, and morph-fast-apply`
4. `test: add coverage for agentcontext and secrets packages`
5. `docs: add planning documentation`

---

## Additional Work (2026-01-29)

### New MCP Server Deployments

Added deployment manifests to loom-hub for:

**mcp-github-actions** (`platform/gitops/k3s/loom-hub/servers/github-actions/`)
- Tools: list_workflows, get_workflow, list_workflow_runs, get_workflow_run, list_workflow_jobs, get_job_logs, list_artifacts

**mcp-slack** (`platform/gitops/k3s/loom-hub/servers/slack/`)
- Tools: search_messages, list_channels, get_channel_history, list_users, get_user_info, get_channel_info, get_permalink

Commit: `feat(loom-hub): add mcp-github-actions and mcp-slack servers`

### Test Coverage Sprint (2026-01-30)

Additional packages with new test coverage:

| Package | Before | After |
|---------|--------|-------|
| pkg/validate | 0% | 100% |
| pkg/httpclient | 0% | 92.6% |
| pkg/generator | 0% | 21.0% |
| pkg/tunnel | 17.8% | 36.2% |

Tests cover:
- Args validation (required, int, string, bool, slice, enum, pattern, length)
- HTTP client retry logic, backoff, context cancellation
- Path/token resolution and plaintext secret detection
- SSH tunnel connection lifecycle, transport state management

Commit: `test: add coverage for validate, httpclient, generator, tunnel packages`
