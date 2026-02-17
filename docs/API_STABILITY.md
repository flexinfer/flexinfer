# API Stability Policy

This document describes Loom Core compatibility expectations for contributors and integrators.

For shipped/in-progress feature status, see `docs/IMPLEMENTATION_STATUS.md`.

## Versioning Policy

Loom Core uses [Semantic Versioning](https://semver.org/), with an important caveat:

- Current releases are still `0.x`.
- Before `1.0.0`, occasional breaking changes may still happen.
- Breaking changes must be called out in `CHANGELOG.md` with migration notes.

## Stability Levels

### Stable Surfaces

These are expected to remain compatible across patch releases and usually across minor releases:

- `loom proxy` behavior used by generated client configs
- Existing MCP tool names and required parameters
- Registry-driven config generation and sync workflows (`loom generate`, `loom sync`)

### Best-Effort Stable (Pre-1.0)

These are intended to be stable, but may evolve between minor versions while the project is `<1.0.0`:

- User-facing CLI command set (`loom`)
- JSON response shapes for commonly consumed tool outputs
- Public package APIs under `pkg/`

### Unstable/Internal

These can change at any time:

- `internal/*` packages
- HUD internal API contracts not documented as public
- daemon internals (routing/process/cache implementations)

## Public Package Surface (`pkg/`)

The following packages are considered reusable public code:

- `pkg/agentcontext`
- `pkg/codebase`
- `pkg/context`
- `pkg/env`
- `pkg/generator`
- `pkg/httpclient`
- `pkg/lifecycle`
- `pkg/mcperror`
- `pkg/mcplog`
- `pkg/mcpotel`
- `pkg/pathsec`
- `pkg/poll`
- `pkg/portforward`
- `pkg/profiles`
- `pkg/registry`
- `pkg/secrets`
- `pkg/skills`
- `pkg/strutil`
- `pkg/sync`
- `pkg/templatevars`
- `pkg/toolexec`
- `pkg/tunnel`
- `pkg/validate`
- `pkg/validator`

`pkg/testutil` is test-support code and may evolve with less strict compatibility guarantees.

## MCP Tool Contract Rules

For existing tools:

1. Tool names should not be renamed.
2. Required params should not be removed or made stricter without migration.
3. Optional params may be added.
4. Response objects may add fields but should not remove widely used fields without a deprecation window.

## CLI Compatibility

Core command families expected to remain available:

- Daemon lifecycle: `start`, `stop`, `status`, `restart`, `reload`, `daemon ...`
- Config workflows: `generate ...`, `sync ...`, `validate ...`
- Operations: `servers`, `tools ...`, `tunnel ...`, `secrets ...`, `auth ...`, `doctor ...`
- Runtime interfaces: `proxy`, `hud`, `agent ...`

Notes:

- Human-readable output may change.
- Prefer `--json` output where available for automation.

## Configuration Compatibility

Primary stable config surfaces:

- `registry.yaml` (workspace canonical metadata)
- Generated client config files produced by `loom generate`/`loom sync`
- `~/.config/loom/config.yaml` daemon/CLI config (if used)

Rules:

1. Additive fields are preferred.
2. Existing field semantics should remain stable.
3. Deprecated fields should be documented before removal.

## Breaking Change Process

When a breaking change is unavoidable:

1. Add an explicit `CHANGELOG.md` entry.
2. Document migration steps.
3. Keep a compatibility shim/alias where feasible.
4. Remove deprecated behavior in a later minor/major release (depending on impact).

## Validation Checklist

Before merging compatibility-sensitive changes:

```bash
go test ./...
go vet ./...
```

For command-level sanity:

```bash
go run ./cmd/loom --help
go run ./cmd/loom proxy --help
```

## Reporting Regressions

If you hit an unexpected compatibility break, include:

- Loom version (before/after)
- command/tool impacted
- expected vs actual behavior
- minimal reproduction
