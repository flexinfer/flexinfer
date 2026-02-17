# Documentation Ownership and Update Cadence

This document defines who updates Loom Core docs and when updates are required.

## Ownership Model

- Feature owner: updates user/developer docs for behavior or workflow changes in the same PR.
- Area maintainer: reviews docs for correctness in their subsystem (`daemon`, `hud`, `devbox`, `mcp-*`).
- Release owner: confirms `README.md`, `docs/IMPLEMENTATION_STATUS.md`, and `CHANGELOG.md` are aligned before cutting a release.

## Required Update Triggers

Update documentation when any of these change:

- CLI command names/flags/examples (`loom ...`).
- MCP tool names/args/response semantics.
- Daemon config schema keys or defaults.
- Security/auth flows (token, OIDC, mTLS, OAuth 2.1).
- User-facing operational workflows (bootstrap, sync, upgrade/reload, HUD).

## Minimum Files to Evaluate Per User-Visible Change

1. `CHANGELOG.md`
2. `docs/IMPLEMENTATION_STATUS.md`
3. At least one of:
   - `README.md`
   - `docs/USER_GUIDE.md`
   - `docs/DEVELOPER_GUIDE.md`
   - feature-specific deep docs under `docs/`

## Cadence

- Per PR: update docs in the same branch as the code change.
- Weekly: review `docs/IMPLEMENTATION_STATUS.md` against `ROADMAP.md` and active planning notes.
- Per release cut: run docs/CLI guardrails and resolve drift before tagging.

## Verification Checklist

```bash
scripts/ci/check_docs_guardrails.sh
go run ./cmd/loom --help
go run ./cmd/loom agent --help
```

If command behavior changed, also verify related help pages (for example `loom auth --help`, `loom proxy --help`).

## Source of Truth Hierarchy

1. Runtime behavior in code (`cmd/`, `internal/`, `pkg/`)
2. `CHANGELOG.md` for release-facing deltas
3. `docs/IMPLEMENTATION_STATUS.md` for shipped vs in-progress state
4. Top-level and deep docs (`README.md`, `docs/*.md`)
