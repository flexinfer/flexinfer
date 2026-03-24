#!/usr/bin/env bash
set -euo pipefail

# Dev upgrade routine: build, atomically install binaries, regen+sync configs and skills
# in loom mode, and (optionally) restart the daemon only when idle.
#
# Path safety:
# - Always installs to INSTALL_DIR (default ~/.local/bin)
# - Also installs to the currently active `loom` PATH directory when user-writable
#   (prevents stale binaries when PATH prefers e.g. ~/go/bin/loom)

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
RESTART_DAEMON="${RESTART_DAEMON:-auto}" # auto|always|never
HUD_URL_SCRIPT="$ROOT/scripts/dev/detect_hud_url.sh"

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

list_proxy_pids() {
  ps -Ao pid=,command= | awk -v self="$$" '
    {
      pid = $1
      $1 = ""
      sub(/^[[:space:]]+/, "", $0)
      if (pid != self && $0 ~ /(^|\/)loom proxy([[:space:]]|$)/) {
        print pid
      }
    }
  '
}

reap_proxy_processes() {
  local pids=()
  local pid
  local remaining=()
  local attempt

  while IFS= read -r pid; do
    [[ -n "$pid" ]] && pids+=("$pid")
  done < <(list_proxy_pids)

  if [[ "${#pids[@]}" -eq 0 ]]; then
    echo "No existing loom proxy clients to reap"
    return 0
  fi

  echo "Reaping loom proxy clients: ${pids[*]}"
  kill "${pids[@]}" 2>/dev/null || true

  remaining=("${pids[@]}")
  for attempt in 1 2 3 4 5; do
    sleep 0.2
    remaining=()
    for pid in "${pids[@]}"; do
      if kill -0 "$pid" 2>/dev/null; then
        remaining+=("$pid")
      fi
    done
    if [[ "${#remaining[@]}" -eq 0 ]]; then
      echo "Proxy cleanup complete"
      return 0
    fi
  done

  echo "Force-killing stubborn loom proxy clients: ${remaining[*]}"
  kill -9 "${remaining[@]}" 2>/dev/null || true
}

get_mobile_operator_token() {
  if [[ -n "${HUD_MOBILE_OPERATOR_TOKEN:-}" ]]; then
    printf '%s\n' "$HUD_MOBILE_OPERATOR_TOKEN"
    return 0
  fi

  local hud_env="$HOME/.config/loom/hud.env"
  if [[ -f "$hud_env" ]]; then
    awk -F= '/^HUD_MOBILE_OPERATOR_TOKEN=/{print substr($0, index($0, "=") + 1); exit}' "$hud_env"
  fi
}

probe_mobile_tasks() {
  local token="$1"
  [[ -z "$token" ]] && return 1

  python3 - "$token" <<'PY'
import json
import sys
import urllib.error
import urllib.request

req = urllib.request.Request(
    "http://127.0.0.1:3333/api/mobile/v1/tasks",
    headers={"Authorization": f"Bearer {sys.argv[1]}"},
)
try:
    with urllib.request.urlopen(req, timeout=3) as resp:
        payload = json.load(resp)
except (urllib.error.URLError, TimeoutError, json.JSONDecodeError):
    raise SystemExit(1)

raise SystemExit(0 if payload.get("ok") is True else 1)
PY
}

wait_for_mobile_tasks() {
  local token="$1"
  local attempts="${2:-8}"
  local delay="${3:-2}"
  local i

  for ((i = 1; i <= attempts; i++)); do
    if probe_mobile_tasks "$token"; then
      return 0
    fi
    sleep "$delay"
  done
  return 1
}

start_direct_daemon_fallback() {
  local registry_path="$1"
  local plist_path="$HOME/Library/LaunchAgents/com.loom.daemon.plist"
  local log_dir="$HOME/.config/loom/logs"
  local daemon_log="$log_dir/daemon.log"
  local daemon_err="$log_dir/daemon.err"
  local hud_env="$HOME/.config/loom/hud.env"

  echo "Launchd daemon restart came back unhealthy; switching to direct daemon fallback"

  if [[ -f "$plist_path" ]]; then
    launchctl unload "$plist_path" 2>/dev/null || true
  fi

  "$RUN_LOOM" stop >/dev/null 2>&1 || true
  sleep 1

  if [[ -f "$hud_env" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$hud_env"
    set +a
  fi

  export CACHE_BACKEND="${CACHE_BACKEND:-redis}"
  export REDIS_URL="${REDIS_URL:-redis://localhost:6379}"
  export HUD_BIND_ADDRESS="${HUD_BIND_ADDRESS:-0.0.0.0}"
  export HUD_PIPELINE_PROJECTS="${HUD_PIPELINE_PROJECTS:-services/loom-core}"

  mkdir -p "$log_dir"
  python3 - "$RUN_LOOMD" "$registry_path" "$daemon_log" "$daemon_err" <<'PY'
import os
import subprocess
import sys

daemon, registry, stdout_path, stderr_path = sys.argv[1:5]
with open(stdout_path, "ab", buffering=0) as stdout, open(stderr_path, "ab", buffering=0) as stderr:
    proc = subprocess.Popen(
        [daemon, "--registry", registry, "--hud-port", "3333"],
        stdin=subprocess.DEVNULL,
        stdout=stdout,
        stderr=stderr,
        start_new_session=True,
        close_fds=True,
        env=os.environ.copy(),
    )
print(proc.pid)
PY
  sleep 2
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
"$RUN_LOOM" sync skills all

echo "== Daemon =="
DAEMON_RESTARTED=false
case "$RESTART_DAEMON" in
  never)
    echo "Skipping daemon restart (RESTART_DAEMON=never)"
    ;;
  always)
    "$RUN_LOOM" restart
    DAEMON_RESTARTED=true
    ;;
  auto)
    # Prefer drain_ready field from status output (added in TD-SESSION-05).
    # Falls back to legacy "Connections: X active" parsing for older daemons.
    if out=$("$RUN_LOOM" status 2>/dev/null); then
      drain_ready="$(echo "$out" | awk -F'drain_ready=' '/drain_ready=/{print $2}' | tr -d '[:space:]' || true)"
      if [[ "$drain_ready" == "true" ]]; then
        "$RUN_LOOM" restart
        DAEMON_RESTARTED=true
      elif [[ "$drain_ready" == "false" ]]; then
        echo "Daemon not drain-ready (in-flight RPCs); skipping restart (set RESTART_DAEMON=always to force)"
      else
        # Legacy fallback: parse "Connections: X active, Y idle"
        active="$(echo "$out" | awk '/^Connections:/{print $2}' | tr -d '[:space:]' || true)"
        if [[ -n "${active:-}" && "${active:-0}" =~ ^[0-9]+$ && "${active:-0}" -eq 0 ]]; then
          "$RUN_LOOM" restart
          DAEMON_RESTARTED=true
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

if [[ "$DAEMON_RESTARTED" == "true" ]]; then
  echo "== Proxy Cleanup =="
  reap_proxy_processes
fi

echo "== HUD =="
echo "HUD is now embedded in loomd (--hud-port). Daemon restart covers HUD restart."
# Clean up legacy HUD plist if present.
HUD_PLIST="$HOME/Library/LaunchAgents/com.loom.hud.plist"
if [ -f "$HUD_PLIST" ]; then
  launchctl stop com.loom.hud 2>/dev/null || true
  echo "Stopped legacy HUD launchd agent (consider removing $HUD_PLIST)"
fi

if [[ "$DAEMON_RESTARTED" == "true" ]]; then
  echo "== Proxy Cleanup (post-restart check) =="
  reap_proxy_processes
fi

echo "== Smoke (proxy initialize) =="
python3 - "$RUN_LOOM" <<'PY'
import json, subprocess, sys
msg = {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"upgrade-smoke","version":"0"}}}
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

if [[ "$DAEMON_RESTARTED" == "true" ]]; then
  echo "== Mobile Smoke =="
  REGISTRY_PATH="$HOME/.config/loom/registry.yaml"
  MOBILE_OPERATOR_TOKEN="$(get_mobile_operator_token || true)"
  if [[ -n "${MOBILE_OPERATOR_TOKEN:-}" ]]; then
    if wait_for_mobile_tasks "$MOBILE_OPERATOR_TOKEN" 6 2; then
      echo "Mobile task endpoint OK"
    else
      start_direct_daemon_fallback "$REGISTRY_PATH"
      if wait_for_mobile_tasks "$MOBILE_OPERATOR_TOKEN" 16 2; then
        echo "Mobile task endpoint recovered via direct daemon fallback"
      else
        echo "WARNING: mobile task endpoint still unhealthy after direct daemon fallback"
      fi
    fi
  else
    echo "Skipped mobile smoke (no operator token found)"
  fi
fi

echo "OK"
