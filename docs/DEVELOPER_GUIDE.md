# Loom Core Developer Guide

This guide is for contributors working on the Loom Core Go codebase.

## Repo layout

- `cmd/loom/`: CLI for config generation/sync and daemon management
- `cmd/loomd/`: daemon (local MCP hub + routing)
- `cmd/mcp-*/`: MCP server binaries
- `internal/`: daemon/process/router internals
- `pkg/`: shared libraries (registry, profiles, sync, validation, etc.)

## Build, test, lint

```bash
make build
go test ./...
golangci-lint run
```

Notes:

- `generated/` is local output from `loom generate configs` and should not be committed.
- `services/loom-core/go.mod` uses a local `replace` for `gitlab.flexinfer.ai/libs/mcp-go` → `../../libs/mcp-go` during workspace development.

## Local dev loop

```bash
make build
cp -f bin/loom  ~/.local/bin/loom
cp -f bin/loomd ~/.local/bin/loomd
./bin/loom restart
./bin/loom sync all --regen
```

## Adding or updating an MCP server

1. Implement under `cmd/mcp-<name>/main.go`.
2. Keep tool schemas stable (inputs/outputs). Prefer additive changes.
3. Run `go test ./...` and `make build`.
4. Regenerate/sync configs so clients pick up tool schema changes:
   - `./bin/loom generate configs --target all`
   - `./bin/loom sync all --regen`

## Debugging

- Run daemon in foreground for interactive debugging: `./bin/loomd --debug`
- Inspect daemon logs:
  - `~/.config/loom/logs/daemon.log`
  - `~/.config/loom/logs/daemon.err`

