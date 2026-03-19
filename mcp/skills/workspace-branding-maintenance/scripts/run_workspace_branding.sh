#!/usr/bin/env bash
set -euo pipefail

# Safe wrapper around libs/banner-kit/scripts/workspace_branding_maintenance.sh
# Defaults to --dry-run.

ROOT="${1:-.}"
shift || true

cd "$ROOT"

SCRIPT="libs/banner-kit/scripts/workspace_branding_maintenance.sh"
if [[ ! -f "$SCRIPT" ]]; then
  echo "ERROR: missing banner-kit script: $SCRIPT" >&2
  exit 1
fi

if [[ "${APPLY:-0}" != "1" ]]; then
  exec bash "$SCRIPT" --dry-run "$@"
fi

exec bash "$SCRIPT" "$@"
