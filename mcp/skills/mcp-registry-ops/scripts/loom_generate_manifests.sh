#!/usr/bin/env bash
set -euo pipefail

# Wrapper to generate MCP hub manifests from registry.yaml using loom-core's `loom` CLI.
# Safe by default: runs in --dry-run mode unless --apply is passed.

ROOT="${1:-.}"
shift || true

cd "$ROOT"

LOOM_BIN="${LOOM_BIN:-services/loom-core/bin/loom}"
REGISTRY_DEFAULT="mcp/context/registry.yaml"
OUTPUT_DIR_DEFAULT="platform/gitops/k3s/loom-hub/servers"

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
args=()
for arg in "$@"; do
  if [[ "$arg" == "--apply" ]]; then
    APPLY=true
  else
    args+=("$arg")
  fi
done

has_output_dir=false

for i in "${!args[@]}"; do
  case "${args[$i]}" in
    --output-dir)
      has_output_dir=true
      next=$((i + 1))
      if [[ $next -lt ${#args[@]} ]] && [[ "${args[$next]}" == "platform/gitops/k3s/mcp-hub/servers" ]]; then
        echo "INFO: remapping deprecated output dir to ${OUTPUT_DIR_DEFAULT}" >&2
        args[$next]="${OUTPUT_DIR_DEFAULT}"
      fi
      ;;
    --output-dir=*)
      has_output_dir=true
      if [[ "${args[$i]}" == "--output-dir=platform/gitops/k3s/mcp-hub/servers" ]]; then
        echo "INFO: remapping deprecated output dir to ${OUTPUT_DIR_DEFAULT}" >&2
        args[$i]="--output-dir=${OUTPUT_DIR_DEFAULT}"
      fi
      ;;
  esac
done

if [[ "$has_output_dir" == false ]]; then
  args+=(--output-dir "${OUTPUT_DIR_DEFAULT}")
fi

if [[ "$APPLY" == true ]]; then
  echo "Generating manifests (--apply: writing files)..." >&2
  exec "$LOOM_BIN" generate manifests --registry "$REGISTRY_DEFAULT" "${args[@]}"
else
  echo "[dry-run] Previewing manifest generation (pass --apply to write)..." >&2
  exec "$LOOM_BIN" generate manifests --registry "$REGISTRY_DEFAULT" --dry-run "${args[@]}"
fi
