#!/usr/bin/env bash
set -euo pipefail

PORT_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/loom/hud.port"
PORT="${1:-}"

if [[ -z "$PORT" && -f "$PORT_FILE" ]]; then
  PORT="$(tr -d '[:space:]' < "$PORT_FILE")"
fi
if [[ -z "$PORT" || ! "$PORT" =~ ^[0-9]+$ ]]; then
  PORT=3333
fi

probe() {
  local url="$1"
  if curl -kfsS --max-time 2 "$url/api/status" >/dev/null 2>&1; then
    echo "$url"
    return 0
  fi
  return 1
}

probe "https://127.0.0.1:${PORT}" || probe "http://127.0.0.1:${PORT}" || echo "http://127.0.0.1:${PORT}"
