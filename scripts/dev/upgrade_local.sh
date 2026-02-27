#!/usr/bin/env bash
set -euo pipefail

# Dev upgrade routine: build, atomically install binaries, regen+sync configs in loom mode,
# and (optionally) restart the daemon only when idle.
#
# Path safety:
# - Always installs to INSTALL_DIR (default ~/.local/bin)
# - Also installs to the currently active `loom` PATH directory when user-writable
#   (prevents stale binaries when PATH prefers e.g. ~/go/bin/loom)

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
RESTART_DAEMON="${RESTART_DAEMON:-auto}" # auto|always|never

cd "$ROOT"

resolve_dir() {
  local d="$1"
  mkdir -p "$d"
  (cd "$d" && pwd -P)
}

dir_in_list() {
  local needle="$1"
  shift
  local d
  for d in "$@"; do
    if [[ "$d" == "$needle" ]]; then
      return 0
    fi
  done
  return 1
}

echo "== Build =="
make loom loomd mcp-hub-wrapper >/dev/null

echo "== Install (atomic) =="
chmod +x scripts/install_atomic.sh
PRIMARY_DIR="$(resolve_dir "$INSTALL_DIR")"
declare -a INSTALL_TARGET_DIRS=("$PRIMARY_DIR")

ACTIVE_LOOM_BIN="$(command -v loom 2>/dev/null || true)"
if [[ -n "${ACTIVE_LOOM_BIN:-}" ]]; then
  ACTIVE_LOOM_DIR="$(resolve_dir "$(dirname "$ACTIVE_LOOM_BIN")")"
  if ! dir_in_list "$ACTIVE_LOOM_DIR" "${INSTALL_TARGET_DIRS[@]}"; then
    case "$ACTIVE_LOOM_DIR" in
      "$HOME"|"$HOME"/*)
        if [[ -w "$ACTIVE_LOOM_DIR" ]]; then
          INSTALL_TARGET_DIRS+=("$ACTIVE_LOOM_DIR")
        else
          echo "WARNING: active loom dir not writable, skipping extra install: $ACTIVE_LOOM_DIR"
        fi
        ;;
      *)
        echo "WARNING: active loom dir is outside HOME, skipping extra install: $ACTIVE_LOOM_DIR"
        ;;
    esac
  fi
fi

for dir in "${INSTALL_TARGET_DIRS[@]}"; do
  scripts/install_atomic.sh "$ROOT/bin/loom"  "$dir/loom"
  scripts/install_atomic.sh "$ROOT/bin/loomd" "$dir/loomd"
  scripts/install_atomic.sh "$ROOT/bin/mcp-hub-wrapper" "$dir/mcp-hub-wrapper"
done

RUN_LOOM="$PRIMARY_DIR/loom"
RUN_LOOMD="$PRIMARY_DIR/loomd"

echo "Installed targets:"
for dir in "${INSTALL_TARGET_DIRS[@]}"; do
  echo "  - $dir/loom"
done
echo "Versions:"
"$RUN_LOOM" --version || true
"$RUN_LOOMD" --version 2>/dev/null || true

if [[ -n "${ACTIVE_LOOM_BIN:-}" ]]; then
  ACTIVE_VER="$("$ACTIVE_LOOM_BIN" --version 2>/dev/null || true)"
  EXPECTED_VER="$("$RUN_LOOM" --version 2>/dev/null || true)"
  if [[ -n "$ACTIVE_VER" && -n "$EXPECTED_VER" && "$ACTIVE_VER" != "$EXPECTED_VER" ]]; then
    echo "ERROR: active PATH loom is stale after install."
    echo "  command -v loom -> $ACTIVE_LOOM_BIN"
    echo "  active version  -> $ACTIVE_VER"
    echo "  expected        -> $EXPECTED_VER"
    echo "Fix PATH order or set INSTALL_DIR to the active loom directory."
    exit 3
  fi
fi

echo "== Regen + Sync (loom mode) =="
"$RUN_LOOM" sync all --regen --loom-mode --loom-binary "$RUN_LOOM"

echo "== Daemon =="
case "$RESTART_DAEMON" in
  never)
    echo "Skipping daemon restart (RESTART_DAEMON=never)"
    ;;
  always)
    "$RUN_LOOM" restart
    ;;
  auto)
    # Prefer drain_ready field from status output (added in TD-SESSION-05).
    # Falls back to legacy "Connections: X active" parsing for older daemons.
    if out=$("$RUN_LOOM" status 2>/dev/null); then
      drain_ready="$(echo "$out" | awk -F'drain_ready=' '/drain_ready=/{print $2}' | tr -d '[:space:]' || true)"
      if [[ "$drain_ready" == "true" ]]; then
        "$RUN_LOOM" restart
      elif [[ "$drain_ready" == "false" ]]; then
        echo "Daemon not drain-ready (in-flight RPCs); skipping restart (set RESTART_DAEMON=always to force)"
      else
        # Legacy fallback: parse "Connections: X active, Y idle"
        active="$(echo "$out" | awk '/^Connections:/{print $2}' | tr -d '[:space:]' || true)"
        if [[ -n "${active:-}" && "${active:-0}" =~ ^[0-9]+$ && "${active:-0}" -eq 0 ]]; then
          "$RUN_LOOM" restart
        else
          echo "Daemon has active connections (${active:-unknown}); skipping restart (set RESTART_DAEMON=always to force)"
        fi
      fi
    else
      echo "Could not query daemon status; skipping restart"
    fi
    ;;
  *)
    echo "Unknown RESTART_DAEMON=$RESTART_DAEMON (expected auto|always|never)" >&2
    exit 2
    ;;
esac

echo "== HUD =="
HUD_PLIST="$HOME/Library/LaunchAgents/com.loom.hud.plist"
if [ -f "$HUD_PLIST" ]; then
  launchctl stop com.loom.hud 2>/dev/null || true
  sleep 1
  launchctl start com.loom.hud
  sleep 2
  if lsof -ti :3333 >/dev/null 2>&1; then
    echo "HUD restarted via launchctl — http://127.0.0.1:3333"
  else
    echo "WARNING: HUD failed to restart via launchctl. Check ~/.config/loom/logs/hud.log"
  fi
else
  # Fallback: manual nohup restart if no plist installed.
  HUD_PID=$(lsof -ti :3333 2>/dev/null | head -1 || true)
  if [ -n "$HUD_PID" ]; then
    kill "$HUD_PID" 2>/dev/null || true
    sleep 1
    if kill -0 "$HUD_PID" 2>/dev/null; then kill -9 "$HUD_PID" 2>/dev/null || true; fi
    echo "Killed old HUD (PID $HUD_PID)"
    nohup "$RUN_LOOM" hud --port 3333 > /tmp/loom-hud.log 2>&1 &
    sleep 2
    if lsof -ti :3333 >/dev/null 2>&1; then
      echo "HUD restarted — http://127.0.0.1:3333"
    else
      echo "WARNING: HUD failed to restart. Check /tmp/loom-hud.log"
    fi
  else
    echo "No HUD process on port 3333 and no launchd plist; skipping"
  fi
fi

echo "== Smoke (proxy initialize) =="
python3 - "$RUN_LOOM" <<'PY'
import json, subprocess, sys
msg = {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"upgrade-smoke","version":"0"}}}
p = subprocess.Popen([sys.argv[1], "proxy"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
out, err = p.communicate(json.dumps(msg) + "\n", timeout=5)
if p.returncode != 0:
  raise SystemExit(f"proxy exited {p.returncode}: {err[:400]}")
if not out.strip():
  raise SystemExit("proxy produced no output")
resp = json.loads(out.splitlines()[0])
assert resp.get("result", {}).get("serverInfo", {}).get("name") == "loom", resp
print("OK:", resp["result"]["serverInfo"]["name"], "v"+resp["result"]["serverInfo"]["version"])
PY

echo "OK"
