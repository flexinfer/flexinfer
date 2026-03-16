#!/usr/bin/env bash
set -euo pipefail

# Wrapper to generate MCP client configs from registry.yaml using loom-core's `loom` CLI.
# Safe by default: runs in --dry-run mode unless --apply is passed.

ROOT="${1:-.}"
shift || true

cd "$ROOT"

LOOM_BIN="${LOOM_BIN:-services/loom-core/bin/loom}"
REGISTRY_DEFAULT="mcp/context/registry.yaml"

if [[ ! -x "$LOOM_BIN" ]]; then
  if command -v go >/dev/null 2>&1; then
    echo "Building loom binary (missing: $LOOM_BIN)..." >&2
    mkdir -p "$(dirname "$LOOM_BIN")"
    (cd services/loom-core && go build -o bin/loom ./cmd/loom)
  else
    echo "ERROR: loom binary not found and go is not installed." >&2
    exit 1
  fi
fi

# Check for --apply flag; default to --dry-run
APPLY=false
PASSTHROUGH_ARGS=()
for arg in "$@"; do
  if [[ "$arg" == "--apply" ]]; then
    APPLY=true
  else
    PASSTHROUGH_ARGS+=("$arg")
  fi
done

if [[ "$APPLY" == true ]]; then
  echo "Generating configs (--apply: writing files)..." >&2
  exec "$LOOM_BIN" generate configs --registry "$REGISTRY_DEFAULT" "${PASSTHROUGH_ARGS[@]}"
else
  echo "[dry-run] Previewing config generation (pass --apply to write)..." >&2
  exec "$LOOM_BIN" generate configs --registry "$REGISTRY_DEFAULT" --dry-run "${PASSTHROUGH_ARGS[@]}"
fi
