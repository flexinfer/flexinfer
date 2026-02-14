#!/usr/bin/env bash
set -euo pipefail

# Dev upgrade routine: build, atomically install to ~/.local/bin, regen+sync configs in loom mode,
# and (optionally) restart the daemon only when idle.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
RESTART_DAEMON="${RESTART_DAEMON:-auto}" # auto|always|never

cd "$ROOT"

echo "== Build =="
make loom loomd >/dev/null

echo "== Install (atomic) =="
chmod +x scripts/install_atomic.sh
scripts/install_atomic.sh "$ROOT/bin/loom"  "$INSTALL_DIR/loom"
scripts/install_atomic.sh "$ROOT/bin/loomd" "$INSTALL_DIR/loomd"

echo "Installed:"
"$INSTALL_DIR/loom" --version || true
"$INSTALL_DIR/loomd" --version 2>/dev/null || true

echo "== Regen + Sync (loom mode) =="
"$INSTALL_DIR/loom" sync all --regen --loom-mode --loom-binary "$INSTALL_DIR/loom"

echo "== Daemon =="
case "$RESTART_DAEMON" in
  never)
    echo "Skipping daemon restart (RESTART_DAEMON=never)"
    ;;
  always)
    "$INSTALL_DIR/loom" restart
    ;;
  auto)
    # Only restart if idle to avoid interrupting in-flight tool calls.
    # Expected status line: "Connections: X active, Y idle"
    if out=$("$INSTALL_DIR/loom" status 2>/dev/null); then
      active="$(echo "$out" | awk '/^Connections:/{print $2}' | tr -d '[:space:]' || true)"
      if [[ -n "${active:-}" && "${active:-0}" =~ ^[0-9]+$ && "${active:-0}" -eq 0 ]]; then
        "$INSTALL_DIR/loom" restart
      else
        echo "Daemon has active connections (${active:-unknown}); skipping restart (set RESTART_DAEMON=always to force)"
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
HUD_PID=$(lsof -ti :3333 2>/dev/null | head -1 || true)
if [ -n "$HUD_PID" ]; then
  kill "$HUD_PID" 2>/dev/null || true
  sleep 1
  if kill -0 "$HUD_PID" 2>/dev/null; then kill -9 "$HUD_PID" 2>/dev/null || true; fi
  echo "Killed old HUD (PID $HUD_PID)"
  nohup "$INSTALL_DIR/loom" hud --port 3333 > /tmp/loom-hud.log 2>&1 &
  sleep 2
  if lsof -ti :3333 >/dev/null 2>&1; then
    echo "HUD restarted — http://127.0.0.1:3333"
  else
    echo "WARNING: HUD failed to restart. Check /tmp/loom-hud.log"
  fi
else
  echo "No HUD process on port 3333; skipping"
fi

echo "== Smoke (proxy initialize) =="
python3 - "$INSTALL_DIR/loom" <<'PY'
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
