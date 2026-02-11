# 2026-02 Quality and Onboarding Opportunities

> **Status:** In Progress
> **Reviewed:** 2026-02-11
> **Inputs:** recent commits (`v0.9.7..HEAD`), `go test ./...`, `golangci-lint run`

## Snapshot

- `go test ./...`: passing
- `golangci-lint run`: passing after one low-risk fix in `internal/devbox/detect/hash_test.go`
- current total Go coverage (`go tool cover -func`): **21.2%**

## Priority Opportunities

### 1) Raise coverage on MCP servers with no direct tests

Many `cmd/mcp-*` servers still have no package-level tests. High-value candidates:

- `mcp-devbox` (new, complex lifecycle and backend orchestration)
- `mcp-agent-context` (critical stateful core)
- `mcp-release`, `mcp-substack`, `mcp-itchio` (new additions)
- frequently used infra servers: `mcp-git`, `mcp-grafana`, `mcp-docker`, `mcp-redis`

Proposed baseline:

1. Add one happy-path test per server.
2. Add one argument validation/error-path test per server.
3. Add one external-call failure mapping test (`mcperror` response shape).

### 2) Add a docs guardrail in CI

Recent docs drift happened around HUD/devbox features and command workflows.

Proposed checks:

1. Add a CI job that fails if `README.md`, `docs/`, and `CHANGELOG.md` are unchanged for user-visible commits (convention/tag-based).
2. Add a lightweight command smoke check (`go run ./cmd/loom --help`) in CI to catch doc/command drift early.

Status update (2026-02-11):

- Implemented in GitLab CI + GitHub Actions.
- Added reusable script: `scripts/ci/check_docs_guardrails.sh`.

### 3) Improve first-run onboarding automation

Current onboarding requires users to infer sequence across multiple docs.

Proposed additions:

1. Add `make bootstrap-local` that runs:
   - `make build`
   - `make install-core`
   - `./bin/loom sync all --regen --loom-mode`
   - `./bin/loom check`
2. Print explicit next steps (`loom start`, `loom hud --port 3333`) at the end.

### 4) Strengthen devbox quality gates

`mcp-devbox` is now a core path and should have stronger regression protection.

Proposed additions:

1. Integration tests covering both Docker and K8s backend selection paths.
2. Tests for mount/workdir behavior in monorepo and out-of-tree project layouts.
3. Tests for async exec lifecycle cleanup (stale exec IDs, timeout behavior).

### 5) Standardize error-handling adoption across MCP servers

The documented standard (`validate` + `mcperror`) is clear, but adoption is uneven across older servers.

Proposed implementation:

1. Add a checklist-driven migration tracker for all `cmd/mcp-*` directories.
2. Prioritize high-traffic servers first.
3. Include at least one negative test per migrated server to lock in response format.

## Suggested Execution Order

1. Coverage baseline for `mcp-devbox`, `mcp-agent-context`, and newest MCP servers.
2. `bootstrap-local` onboarding command.
3. CI docs/command smoke guardrails.
4. Broader error-handling standardization sweep.
