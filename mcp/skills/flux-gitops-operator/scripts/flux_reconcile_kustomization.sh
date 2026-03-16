#!/usr/bin/env bash
set -euo pipefail

NAME="${1:-}"
if [[ -z "$NAME" ]]; then
  echo "Usage: flux_reconcile_kustomization.sh <name> [namespace]" >&2
  exit 1
fi
NS="${2:-flux-system}"

if ! command -v flux >/dev/null 2>&1; then
  echo "ERROR: flux CLI not found on PATH" >&2
  exit 1
fi

flux reconcile kustomization "$NAME" -n "$NS"
if command -v rg >/dev/null 2>&1; then
  flux get kustomizations -n "$NS" | rg -n --fixed-strings "$NAME" || true
else
  flux get kustomizations -n "$NS" | grep -n --fixed-strings "$NAME" || true
fi
