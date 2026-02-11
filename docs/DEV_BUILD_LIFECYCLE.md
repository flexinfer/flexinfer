# Dev Build Lifecycle (Agent-Safe)

Goal: iterate quickly on `loom`/`loomd` without breaking agents (Claude Code, Codex, Gemini CLI, VS Code, etc.) that call `~/.local/bin/loom`.

## Principles

- Always reference a stable path in client configs: `~/.local/bin/loom`.
- Never `cp` over an in-use binary on macOS. Install via atomic rename.
- Prefer `--loom-mode` for all client configs so clients only need a single MCP entry (`loom proxy`).
- Avoid daemon restarts while agents have active tool calls.

## Local Dev Upgrade (Recommended)

Use the built-in one-liner:

```bash
make dev-upgrade
```

This does:

1. Build `bin/loom` + `bin/loomd`
2. Atomically install to `~/.local/bin/loom` and `~/.local/bin/loomd` (keeping `*.prev`)
3. `loom sync all --regen --loom-mode --loom-binary ~/.local/bin/loom`
4. Restart daemon only if it is idle (0 active connections)
5. Smoke-test `loom proxy` initialization

Options:

- `RESTART_DAEMON=never make dev-upgrade`
- `RESTART_DAEMON=always make dev-upgrade`
- `INSTALL_DIR=/custom/bin make dev-upgrade`

## Rollback

Atomic install writes a single previous copy:

- `~/.local/bin/loom.prev`
- `~/.local/bin/loomd.prev`

Rollback (atomic):

```bash
scripts/install_atomic.sh ~/.local/bin/loom.prev ~/.local/bin/loom --no-backup
scripts/install_atomic.sh ~/.local/bin/loomd.prev ~/.local/bin/loomd --no-backup
```

Then restart daemon when idle:

```bash
loom restart
```

## Dev vs Stable Release

- Dev builds: frequent local upgrades via `make dev-upgrade` (no tags required).
- Stable builds: tag-based releases (`git tag vX.Y.Z`) and CI artifacts (see `.gitlab-ci.yml`).

## Compatibility Contract (Do Not Break)

To keep agents stable across fast iterations:

- `loom proxy` must remain backwards compatible with older generated configs (flags, IO).
- CLI must not require interactive prompts for common agent workflows.
- Daemon socket path should remain stable by default: `~/.config/loom/loom.sock`.
