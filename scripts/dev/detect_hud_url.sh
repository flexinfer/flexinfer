#!/usr/bin/env bash
set -euo pipefail

PORT="${1:-3333}"

probe() {
  local url="$1"
  if curl -kfsS --max-time 2 "$url/api/status" >/dev/null 2>&1; then
    echo "$url"
    return 0
  fi
  return 1
}

probe "https://127.0.0.1:${PORT}" || probe "http://127.0.0.1:${PORT}" || echo "https://127.0.0.1:${PORT}"
